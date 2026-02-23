package routing

import (
	"errors"
	"math"
	"sort"

	"infermeshai/internal/model"
)

var ErrNoPeers = errors.New("no peers available")

type PeerScore struct {
	Capability model.CapabilityDocument
	Score      float64
}

type Preferences struct {
	Policy    string
	Favorites map[string]struct{}
	Known     map[string]struct{}
	Allowed   map[string]struct{}
	Blocked   map[string]struct{}
}

type Router struct {
	weights model.RoutingWeights
}

func NewRouter(weights model.RoutingWeights) *Router {
	return &Router{weights: weights}
}

func (r *Router) Score(c model.CapabilityDocument) float64 {
	vramPressure := 0.0
	if c.VRAMFreeMB > 0 {
		vramPressure = 1.0 / float64(c.VRAMFreeMB)
	} else {
		vramPressure = math.Inf(1)
	}
	reliabilityPenalty := 1.0 - c.Reliability
	if reliabilityPenalty < 0 {
		reliabilityPenalty = 0
	}
	return r.weights.Latency*float64(c.AvgLatencyMS) +
		r.weights.QueueDepth*float64(c.QueueDepth) +
		r.weights.VRAMPressure*vramPressure +
		r.weights.Reliability*reliabilityPenalty
}

func (r *Router) Rank(peers []model.CapabilityDocument, req model.ChatCompletionRequest) []PeerScore {
	return r.RankWithPreferences(peers, req, Preferences{})
}

func (r *Router) RankWithPreferences(peers []model.CapabilityDocument, req model.ChatCompletionRequest, pref Preferences) []PeerScore {
	scored := make([]PeerScore, 0, len(peers))
	policy := pref.Policy
	if policy == "" {
		policy = "auto"
	}
	hasFavCandidates := false
	for _, p := range peers {
		if _, ok := pref.Favorites[p.PeerID]; ok && p.SupportsRequirements(req.Model, req.Requirements) {
			hasFavCandidates = true
			break
		}
	}
	for _, p := range peers {
		if _, blocked := pref.Blocked[p.PeerID]; blocked {
			continue
		}
		if policy == "manual" && len(pref.Allowed) > 0 {
			if _, ok := pref.Allowed[p.PeerID]; !ok {
				continue
			}
		}
		if policy == "prefer_favorites" && hasFavCandidates {
			if _, ok := pref.Favorites[p.PeerID]; !ok {
				continue
			}
		}
		if !p.SupportsRequirements(req.Model, req.Requirements) {
			continue
		}
		score := r.Score(p)
		if _, ok := pref.Favorites[p.PeerID]; ok {
			score -= r.weights.FavoriteBonus
		}
		if policy == "prefer_known" {
			if _, ok := pref.Known[p.PeerID]; ok {
				score -= r.weights.KnownBonus
			}
		}
		scored = append(scored, PeerScore{Capability: p, Score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score < scored[j].Score
	})
	return scored
}

func (r *Router) SelectBest(peers []model.CapabilityDocument, req model.ChatCompletionRequest) (PeerScore, error) {
	return r.SelectBestWithPreferences(peers, req, Preferences{})
}

func (r *Router) SelectBestWithPreferences(peers []model.CapabilityDocument, req model.ChatCompletionRequest, pref Preferences) (PeerScore, error) {
	ranked := r.RankWithPreferences(peers, req, pref)
	if len(ranked) == 0 {
		return PeerScore{}, ErrNoPeers
	}
	return ranked[0], nil
}
