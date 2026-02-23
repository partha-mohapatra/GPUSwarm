package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"infermeshai/internal/backend"
	"infermeshai/internal/control"
	"infermeshai/internal/model"
	"infermeshai/internal/p2p"
	"infermeshai/internal/routing"
)

type InferenceResult struct {
	StatusCode  int
	ContentType string
	Body        io.ReadCloser
}

type ClientOptions struct {
	PeerID        string
	PeerName      string
	RoutingPolicy string
	FavoritePeers []string
	BlockedPeers  []string
	AllowedPeers  []string
	StickyPeerTTL time.Duration
	PoWDifficulty int
	PoWMaxIters   uint64
	CreditCost    int64
	CreditLedger  *control.CreditLedger
	Reputation    *control.ReputationTracker
}

type RoutingSettings struct {
	RoutingPolicy string        `json:"routing_policy"`
	FavoritePeers []string      `json:"favorite_peers"`
	BlockedPeers  []string      `json:"blocked_peers"`
	AllowedPeers  []string      `json:"allowed_peers"`
	StickyPeerTTL time.Duration `json:"sticky_peer_ttl"`
}

type ClientService struct {
	discovery p2p.Discovery
	router    *routing.Router
	gateway   p2p.GatewayClient
	opts      ClientOptions
	mu        sync.Mutex
	known     map[string]struct{}
	sticky    map[string]stickyChoice
	logger    *slog.Logger
}

type stickyChoice struct {
	peerID  string
	expires time.Time
}

func NewClientService(discovery p2p.Discovery, router *routing.Router, gateway p2p.GatewayClient, opts ClientOptions, logger *slog.Logger) *ClientService {
	if opts.PoWMaxIters == 0 {
		opts.PoWMaxIters = 2_000_000
	}
	if opts.CreditCost == 0 {
		opts.CreditCost = 1
	}
	return &ClientService{
		discovery: discovery,
		router:    router,
		gateway:   gateway,
		opts:      opts,
		known:     make(map[string]struct{}),
		sticky:    make(map[string]stickyChoice),
		logger:    logger,
	}
}

func (s *ClientService) Infer(ctx context.Context, req model.ChatCompletionRequest) (*InferenceResult, error) {
	opts := s.optionsSnapshot()
	if opts.CreditLedger != nil {
		if !opts.CreditLedger.Spend(opts.CreditCost) {
			return nil, fmt.Errorf("insufficient credits")
		}
	}
	caps, err := s.discovery.ListCapabilities(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover peers: %w", err)
	}
	if opts.Reputation != nil {
		for i := range caps {
			localRep := opts.Reputation.Score(caps[i].PeerID)
			if localRep < caps[i].Reliability {
				caps[i].Reliability = localRep
			}
		}
	}
	pref := routing.Preferences{
		Policy:    opts.RoutingPolicy,
		Favorites: toSet(opts.FavoritePeers),
		Known:     s.knownPeers(),
		Allowed:   toSet(opts.AllowedPeers),
		Blocked:   toSet(opts.BlockedPeers),
	}
	ranked := s.router.RankWithPreferences(caps, req, pref)
	if len(ranked) == 0 {
		return nil, routing.ErrNoPeers
	}
	ranked = s.applyStickyPriority(req.Model, ranked)

	nonce := uint64(0)
	if opts.PoWDifficulty > 0 {
		if opts.PeerID == "" {
			return nil, fmt.Errorf("pow enabled but client peer id is empty")
		}
		nonce, _ = control.SolvePoW(opts.PeerID, req.Model, opts.PoWDifficulty, opts.PoWMaxIters)
	}

	var lastErr error
	for _, candidate := range ranked {
		res, err := s.gateway.Infer(ctx, candidate.Capability.GatewayURL, req, model.PeerContext{ClientPeerID: opts.PeerID, ClientName: opts.PeerName, PoWNonce: nonce})
		if err != nil {
			lastErr = err
			if opts.Reputation != nil {
				opts.Reputation.Penalize(candidate.Capability.PeerID, 0.1)
			}
			s.logger.Warn("peer infer failed",
				slog.String("peer_id", candidate.Capability.PeerID),
				slog.String("model", req.Model),
				slog.String("error", err.Error()),
			)
			continue
		}
		if opts.Reputation != nil {
			opts.Reputation.Reward(candidate.Capability.PeerID, 0.02)
		}
		s.markKnown(candidate.Capability.PeerID)
		s.updateSticky(req.Model, candidate.Capability.PeerID)
		return &InferenceResult{StatusCode: res.StatusCode, ContentType: res.ContentType, Body: res.Body}, nil
	}
	if lastErr == nil {
		lastErr = routing.ErrNoPeers
	}
	return nil, fmt.Errorf("all peers failed: %w", lastErr)
}

func (s *ClientService) ListPeers(ctx context.Context) ([]model.CapabilityDocument, error) {
	caps, err := s.discovery.ListCapabilities(ctx)
	if err != nil {
		if s.opts.PeerID != "" {
			return []model.CapabilityDocument{{
				PeerID:     s.opts.PeerID,
				Self:       true,
				NodeName:   s.opts.PeerName,
				GatewayURL: "libp2p://" + s.opts.PeerID,
				UpdatedAt:  time.Now().UTC(),
			}}, nil
		}
		return nil, err
	}
	byPeer := make(map[string]model.CapabilityDocument)
	for _, c := range caps {
		if c.PeerID == s.opts.PeerID {
			c.Self = true
		}
		prev, ok := byPeer[c.PeerID]
		if !ok || c.UpdatedAt.After(prev.UpdatedAt) {
			byPeer[c.PeerID] = c
		}
	}
	if s.opts.PeerID != "" {
		if _, ok := byPeer[s.opts.PeerID]; !ok {
			// Surface local registration state even when no remote peers are discovered yet.
			byPeer[s.opts.PeerID] = model.CapabilityDocument{
				PeerID:     s.opts.PeerID,
				Self:       true,
				NodeName:   s.opts.PeerName,
				GatewayURL: "libp2p://" + s.opts.PeerID,
				UpdatedAt:  time.Now().UTC(),
			}
		}
	}
	out := make([]model.CapabilityDocument, 0, len(byPeer))
	for _, c := range byPeer {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *ClientService) GetRoutingSettings() RoutingSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RoutingSettings{
		RoutingPolicy: s.opts.RoutingPolicy,
		FavoritePeers: append([]string(nil), s.opts.FavoritePeers...),
		BlockedPeers:  append([]string(nil), s.opts.BlockedPeers...),
		AllowedPeers:  append([]string(nil), s.opts.AllowedPeers...),
		StickyPeerTTL: s.opts.StickyPeerTTL,
	}
}

func (s *ClientService) UpdateRoutingSettings(in RoutingSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.RoutingPolicy != "" {
		s.opts.RoutingPolicy = in.RoutingPolicy
	}
	if in.FavoritePeers != nil {
		s.opts.FavoritePeers = append([]string(nil), in.FavoritePeers...)
	}
	if in.BlockedPeers != nil {
		s.opts.BlockedPeers = append([]string(nil), in.BlockedPeers...)
	}
	if in.AllowedPeers != nil {
		s.opts.AllowedPeers = append([]string(nil), in.AllowedPeers...)
	}
	if in.StickyPeerTTL >= 0 {
		s.opts.StickyPeerTTL = in.StickyPeerTTL
	}
}

func (s *ClientService) SelectPeerManual(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts.RoutingPolicy = "manual"
	s.opts.AllowedPeers = []string{peerID}
}

func (s *ClientService) optionsSnapshot() ClientOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.opts
	cp.FavoritePeers = append([]string(nil), s.opts.FavoritePeers...)
	cp.BlockedPeers = append([]string(nil), s.opts.BlockedPeers...)
	cp.AllowedPeers = append([]string(nil), s.opts.AllowedPeers...)
	return cp
}

func (s *ClientService) knownPeers() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.known))
	for k := range s.known {
		out[k] = struct{}{}
	}
	return out
}

func (s *ClientService) markKnown(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known[peerID] = struct{}{}
}

func (s *ClientService) updateSticky(modelName, peerID string) {
	opts := s.optionsSnapshot()
	if opts.StickyPeerTTL <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky[modelName] = stickyChoice{peerID: peerID, expires: time.Now().Add(opts.StickyPeerTTL)}
}

func (s *ClientService) applyStickyPriority(modelName string, ranked []routing.PeerScore) []routing.PeerScore {
	opts := s.optionsSnapshot()
	if opts.StickyPeerTTL <= 0 || len(ranked) < 2 {
		return ranked
	}
	s.mu.Lock()
	ch, ok := s.sticky[modelName]
	if ok && time.Now().After(ch.expires) {
		delete(s.sticky, modelName)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return ranked
	}
	for i := range ranked {
		if ranked[i].Capability.PeerID == ch.peerID {
			if i == 0 {
				return ranked
			}
			out := make([]routing.PeerScore, 0, len(ranked))
			out = append(out, ranked[i])
			out = append(out, ranked[:i]...)
			out = append(out, ranked[i+1:]...)
			return out
		}
	}
	return ranked
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, i := range items {
		if i == "" {
			continue
		}
		out[i] = struct{}{}
	}
	return out
}

type ProviderService struct {
	adapter        backend.Adapter
	limiter        *control.Limiter
	capability     *CapabilityState
	policy         control.ProviderPolicy
	loadProbe      control.LoadProbe
	creditLedger   *control.CreditLedger
	peerReputation *control.ReputationTracker
	logger         *slog.Logger
	inFlightCount  int64
}

func NewProviderService(
	adapter backend.Adapter,
	limiter *control.Limiter,
	capability *CapabilityState,
	policy control.ProviderPolicy,
	loadProbe control.LoadProbe,
	creditLedger *control.CreditLedger,
	peerReputation *control.ReputationTracker,
	logger *slog.Logger,
) *ProviderService {
	if loadProbe == nil {
		loadProbe = control.ProcLoadProbe{}
	}
	return &ProviderService{
		adapter:        adapter,
		limiter:        limiter,
		capability:     capability,
		policy:         policy,
		loadProbe:      loadProbe,
		creditLedger:   creditLedger,
		peerReputation: peerReputation,
		logger:         logger,
	}
}

func (s *ProviderService) Infer(ctx context.Context, req model.ChatCompletionRequest, peerCtx model.PeerContext) (*InferenceResult, func(), error) {
	if err := s.policy.Validate(ctx, req, time.Now(), s.loadProbe); err != nil {
		if s.peerReputation != nil && peerCtx.ClientPeerID != "" {
			s.peerReputation.Penalize(peerCtx.ClientPeerID, 0.02)
		}
		return nil, nil, err
	}
	if s.policy.PoWDifficulty > 0 {
		if !control.ValidatePoW(peerCtx.ClientPeerID, peerCtx.PoWNonce, req.Model, s.policy.PoWDifficulty) {
			if s.peerReputation != nil && peerCtx.ClientPeerID != "" {
				s.peerReputation.Penalize(peerCtx.ClientPeerID, 0.2)
			}
			return nil, nil, control.ErrInvalidPoW
		}
	}
	if s.peerReputation != nil && s.policy.MinPeerReputation > 0 && peerCtx.ClientPeerID != "" {
		if s.peerReputation.Score(peerCtx.ClientPeerID) < s.policy.MinPeerReputation {
			return nil, nil, control.ErrPeerReputationLow
		}
	}
	if err := s.limiter.Acquire(ctx); err != nil {
		return nil, nil, err
	}
	atomic.AddInt64(&s.inFlightCount, 1)
	start := time.Now()

	res, err := s.adapter.Infer(ctx, req)
	if err != nil {
		s.limiter.Release()
		atomic.AddInt64(&s.inFlightCount, -1)
		s.capability.UpdateLatency(time.Since(start))
		s.capability.RecordOutcome(false)
		if s.peerReputation != nil && peerCtx.ClientPeerID != "" {
			s.peerReputation.Penalize(peerCtx.ClientPeerID, 0.05)
		}
		return nil, nil, err
	}

	s.capability.SetQueueDepth(int(atomic.LoadInt64(&s.inFlightCount)))
	cleanup := func() {
		s.limiter.Release()
		atomic.AddInt64(&s.inFlightCount, -1)
		s.capability.SetQueueDepth(int(atomic.LoadInt64(&s.inFlightCount)))
		s.capability.UpdateLatency(time.Since(start))
		s.capability.RecordOutcome(true)
		if s.creditLedger != nil {
			s.creditLedger.Earn(1)
		}
		if s.peerReputation != nil && peerCtx.ClientPeerID != "" {
			s.peerReputation.Reward(peerCtx.ClientPeerID, 0.01)
		}
	}
	return &InferenceResult{StatusCode: res.StatusCode, ContentType: res.ContentType, Body: res.Body}, cleanup, nil
}
