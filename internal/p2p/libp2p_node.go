package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	mdns "github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	disc_routing "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/multiformats/go-multiaddr"

	"infermeshai/internal/model"
)

const (
	capabilityProtocolID = "/infermesh/capability/1.0.0"
	inferenceProtocolID  = "/infermesh/infer/1.0.0"
	inferProxyProtocolID = "/infermesh/infer-proxy/1.0.0"
	registryProtocolID   = "/infermesh/registry/1.0.0"
	registryAnnounceID   = "/infermesh/registry-announce/1.0.0"
	heartbeatTopic       = "infermesh.heartbeat.v1"
)

type Libp2pConfig struct {
	ListenAddrs        []string
	BootstrapPeers     []string
	Rendezvous         string
	EnableMDNS         bool
	EnableNATPortMap   bool
	EnableHolePunching bool
	EnableAutoRelay    bool
	EnableRelayService bool
}

type Libp2pNode struct {
	host        host.Host
	dht         *dht.IpfsDHT
	routingDisc *disc_routing.RoutingDiscovery
	pubsub      *pubsub.PubSub
	topic       *pubsub.Topic
	sub         *pubsub.Subscription
	logger      *slog.Logger

	bootstrapPeers []peer.AddrInfo

	mu           sync.RWMutex
	heartbeatTTL time.Duration
	heartbeats   map[string]heartbeatEntry
	registry     map[string]heartbeatEntry
}

type heartbeatEntry struct {
	cap    model.CapabilityDocument
	seenAt time.Time
}

func NewLibp2pNode(ctx context.Context, cfg Libp2pConfig, identityPath string, logger *slog.Logger) (*Libp2pNode, error) {
	id, err := LoadOrCreateLibp2pIdentity(identityPath)
	if err != nil {
		return nil, err
	}

	listenAddrs := cfg.ListenAddrs
	if len(listenAddrs) == 0 {
		listenAddrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(listenAddrs...),
		libp2p.Identity(id.PrivKey),
	}
	// Relay service only starts when reachability is public; bootstrap nodes are expected
	// to be internet-reachable, so force public when relay service is requested.
	if cfg.EnableRelayService {
		opts = append(opts, libp2p.ForceReachabilityPublic())
	}
	// AutoRelay behavior is designed for privately reachable peers, so force private
	// to deterministically enable relay reservation/address advertisement.
	if cfg.EnableAutoRelay && !cfg.EnableRelayService {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	}
	if cfg.EnableAutoRelay || cfg.EnableRelayService {
		opts = append(opts, libp2p.EnableRelay())
	}
	if cfg.EnableNATPortMap {
		opts = append(opts, libp2p.NATPortMap())
	}
	if cfg.EnableHolePunching {
		opts = append(opts, libp2p.EnableHolePunching())
	}
	if cfg.EnableRelayService {
		opts = append(opts, libp2p.EnableRelayService())
	}
	if cfg.EnableAutoRelay {
		if staticRelays, err := parseStaticRelayPeers(cfg.BootstrapPeers); err == nil && len(staticRelays) > 0 {
			opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(staticRelays))
		} else {
			logger.Warn("auto relay requested but no static relay peers available; skipping auto relay", slog.Int("bootstrap_peers", len(cfg.BootstrapPeers)))
		}
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	dhtNode, err := dht.New(ctx, h)
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("create dht: %w", err)
	}
	if err := dhtNode.Bootstrap(ctx); err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("bootstrap dht: %w", err)
	}

	n := &Libp2pNode{
		host:           h,
		dht:            dhtNode,
		routingDisc:    disc_routing.NewRoutingDiscovery(dhtNode),
		logger:         logger,
		bootstrapPeers: parseBootstrapInfos(cfg.BootstrapPeers),
		heartbeatTTL:   90 * time.Second,
		heartbeats:     make(map[string]heartbeatEntry),
		registry:       make(map[string]heartbeatEntry),
	}

	if err := n.connectBootstrapPeers(ctx, cfg.BootstrapPeers); err != nil {
		logger.Warn("bootstrap peer connect partial failure", slog.String("error", err.Error()))
	}
	logger.Info("libp2p node started",
		slog.String("peer_id", h.ID().String()),
		slog.Int("bootstrap_configured", len(cfg.BootstrapPeers)),
		slog.Bool("nat_port_map", cfg.EnableNATPortMap),
		slog.Bool("hole_punching", cfg.EnableHolePunching),
		slog.Bool("auto_relay", cfg.EnableAutoRelay),
		slog.Bool("relay_service", cfg.EnableRelayService),
		slog.Bool("force_reachability_public", cfg.EnableRelayService),
		slog.Bool("force_reachability_private", cfg.EnableAutoRelay && !cfg.EnableRelayService),
		slog.Any("listen_addrs", h.Addrs()),
	)

	if cfg.EnableMDNS {
		rendezvous := cfg.Rendezvous
		if rendezvous == "" {
			rendezvous = "infermesh-mdns"
		}
		svc := mdns.NewMdnsService(h, rendezvous, &mdnsNotifee{h: h})
		if err := svc.Start(); err != nil {
			logger.Warn("mdns start failed", slog.String("error", err.Error()))
		}
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("create gossipsub: %w", err)
	}
	topic, err := ps.Join(heartbeatTopic)
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("join heartbeat topic: %w", err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("subscribe heartbeat topic: %w", err)
	}
	n.pubsub = ps
	n.topic = topic
	n.sub = sub

	go n.consumeHeartbeats(ctx)
	go n.advertiseLoop(ctx, cfg.Rendezvous)
	go n.bootstrapReconnectLoop(ctx)

	return n, nil
}

func (n *Libp2pNode) Host() host.Host { return n.host }

func (n *Libp2pNode) Close() error {
	if n.sub != nil {
		n.sub.Cancel()
	}
	if n.topic != nil {
		_ = n.topic.Close()
	}
	if n.dht != nil {
		_ = n.dht.Close()
	}
	if n.host != nil {
		return n.host.Close()
	}
	return nil
}

func (n *Libp2pNode) PeerID() string { return n.host.ID().String() }

func (n *Libp2pNode) BootstrapPeers() []peer.AddrInfo {
	out := make([]peer.AddrInfo, 0, len(n.bootstrapPeers))
	for _, ai := range n.bootstrapPeers {
		cpy := peer.AddrInfo{ID: ai.ID, Addrs: append([]multiaddr.Multiaddr(nil), ai.Addrs...)}
		out = append(out, cpy)
	}
	return out
}

func (n *Libp2pNode) AdvertiseAddrs() []string {
	addrs := n.host.Addrs()
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, fmt.Sprintf("%s/p2p/%s", addr.String(), n.host.ID().String()))
	}
	return out
}

func (n *Libp2pNode) Peers() []peer.ID {
	peers := n.host.Network().Peers()
	out := make([]peer.ID, 0, len(peers))
	out = append(out, peers...)
	return out
}

func (n *Libp2pNode) PublishHeartbeat(ctx context.Context, cap model.CapabilityDocument) error {
	b, err := json.Marshal(cap)
	if err != nil {
		return err
	}
	return n.topic.Publish(ctx, b)
}

func (n *Libp2pNode) Heartbeats() []model.CapabilityDocument {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.snapshotMapLocked(n.heartbeats)
}

func (n *Libp2pNode) RegistryCapabilities() []model.CapabilityDocument {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	out := make(map[string]heartbeatEntry, len(n.heartbeats)+len(n.registry))
	n.pruneLocked(now, n.heartbeats)
	n.pruneLocked(now, n.registry)
	for k, v := range n.heartbeats {
		out[k] = v
	}
	for k, v := range n.registry {
		if cur, ok := out[k]; !ok || v.seenAt.After(cur.seenAt) {
			out[k] = v
		}
	}
	caps := make([]model.CapabilityDocument, 0, len(out))
	for _, v := range out {
		caps = append(caps, v.cap)
	}
	return caps
}

func (n *Libp2pNode) UpsertRegistryCapability(cap model.CapabilityDocument) {
	if strings.TrimSpace(cap.PeerID) == "" {
		return
	}
	if cap.UpdatedAt.IsZero() {
		cap.UpdatedAt = time.Now().UTC()
	}
	n.mu.Lock()
	n.registry[cap.PeerID] = heartbeatEntry{cap: cap, seenAt: time.Now()}
	n.mu.Unlock()
}

func (n *Libp2pNode) connectBootstrapPeers(ctx context.Context, raw []string) error {
	var lastErr error
	for _, r := range raw {
		maddr, err := multiaddr.NewMultiaddr(r)
		if err != nil {
			lastErr = err
			continue
		}
		ai, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			lastErr = err
			continue
		}
		n.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.PermanentAddrTTL)
		if err := n.host.Connect(ctx, *ai); err != nil {
			lastErr = err
			n.logger.Warn("bootstrap connect failed", slog.String("peer_id", ai.ID.String()), slog.String("error", err.Error()))
			continue
		}
		n.logger.Info("bootstrap connected", slog.String("peer_id", ai.ID.String()))
	}
	return lastErr
}

func (n *Libp2pNode) AddPeerAddrs(peerID string, raw []string, ttl time.Duration) int {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return 0
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	added := 0
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(entry)
		if err != nil {
			continue
		}
		if ai, err := peer.AddrInfoFromP2pAddr(maddr); err == nil {
			if ai.ID != pid {
				continue
			}
			n.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, ttl)
			added += len(ai.Addrs)
			continue
		}
		n.host.Peerstore().AddAddr(pid, maddr, ttl)
		added++
	}
	return added
}

func (n *Libp2pNode) advertiseLoop(ctx context.Context, rendezvous string) {
	if rendezvous == "" {
		rendezvous = "infermesh-rendezvous"
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = n.routingDisc.Advertise(ctx, rendezvous)
		n.logger.Info("dht advertise", slog.String("rendezvous", rendezvous), slog.Int("connected_peers", len(n.host.Network().Peers())))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func parseStaticRelayPeers(raw []string) ([]peer.AddrInfo, error) {
	out := make([]peer.AddrInfo, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(r)
		if err != nil {
			return nil, err
		}
		ai, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, err
		}
		out = append(out, *ai)
	}
	return out, nil
}

func parseBootstrapInfos(raw []string) []peer.AddrInfo {
	out := make([]peer.AddrInfo, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(r)
		if err != nil {
			continue
		}
		ai, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			continue
		}
		out = append(out, *ai)
	}
	return out
}

func (n *Libp2pNode) DiscoverPeers(ctx context.Context, rendezvous string, limit int) []peer.AddrInfo {
	if rendezvous == "" {
		rendezvous = "infermesh-rendezvous"
	}
	ch, err := n.routingDisc.FindPeers(ctx, rendezvous)
	if err != nil {
		return nil
	}
	out := make([]peer.AddrInfo, 0, 8)
	for p := range ch {
		if p.ID == "" || p.ID == n.host.ID() {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (n *Libp2pNode) consumeHeartbeats(ctx context.Context) {
	for {
		msg, err := n.sub.Next(ctx)
		if err != nil {
			return
		}
		if msg.ReceivedFrom == n.host.ID() {
			continue
		}
		var cap model.CapabilityDocument
		if err := json.Unmarshal(msg.Data, &cap); err != nil {
			continue
		}
		if cap.UpdatedAt.IsZero() {
			cap.UpdatedAt = time.Now().UTC()
		}
		n.mu.Lock()
		n.heartbeats[cap.PeerID] = heartbeatEntry{cap: cap, seenAt: time.Now()}
		n.mu.Unlock()
		n.logger.Info("heartbeat received", slog.String("peer_id", cap.PeerID), slog.String("node_name", cap.NodeName))
	}
}

func (n *Libp2pNode) bootstrapReconnectLoop(ctx context.Context) {
	if len(n.bootstrapPeers) == 0 {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, ai := range n.bootstrapPeers {
				if ai.ID == "" {
					continue
				}
				if n.host.Network().Connectedness(ai.ID) == network.Connected {
					continue
				}
				n.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.PermanentAddrTTL)
				dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				err := n.host.Connect(dialCtx, ai)
				cancel()
				if err != nil {
					n.logger.Warn("bootstrap reconnect failed", slog.String("peer_id", ai.ID.String()), slog.String("error", err.Error()))
					continue
				}
				n.logger.Info("bootstrap reconnected", slog.String("peer_id", ai.ID.String()))
			}
		}
	}
}

func (n *Libp2pNode) pruneLocked(now time.Time, src map[string]heartbeatEntry) {
	for k, v := range src {
		if now.Sub(v.seenAt) > n.heartbeatTTL {
			delete(src, k)
		}
	}
}

func (n *Libp2pNode) snapshotMapLocked(src map[string]heartbeatEntry) []model.CapabilityDocument {
	now := time.Now()
	n.pruneLocked(now, src)
	out := make([]model.CapabilityDocument, 0, len(src))
	for _, v := range src {
		out = append(out, v.cap)
	}
	return out
}

type mdnsNotifee struct{ h host.Host }

func (n *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.h.Peerstore().AddAddrs(pi.ID, pi.Addrs, peerstore.TempAddrTTL)
	_ = n.h.Connect(context.Background(), pi)
}
