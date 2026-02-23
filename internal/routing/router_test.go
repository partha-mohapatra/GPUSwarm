package routing

import (
	"testing"

	"infermeshai/internal/model"
)

func TestRankPrefersLowerScore(t *testing.T) {
	r := NewRouter(model.RoutingWeights{Latency: 1, QueueDepth: 10, VRAMPressure: 100, Reliability: 50, FavoriteBonus: 10, KnownBonus: 5})
	peers := []model.CapabilityDocument{
		{PeerID: "slow", GatewayURL: "http://slow", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 100, QueueDepth: 2, VRAMFreeMB: 20000, Reliability: 0.99},
		{PeerID: "fast", GatewayURL: "http://fast", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 20, QueueDepth: 0, VRAMFreeMB: 18000, Reliability: 0.95},
	}
	req := model.ChatCompletionRequest{Model: "llama3"}
	ranked := r.Rank(peers, req)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked peers, got %d", len(ranked))
	}
	if ranked[0].Capability.PeerID != "fast" {
		t.Fatalf("expected fast first, got %s", ranked[0].Capability.PeerID)
	}
}

func TestRankFiltersUnsupportedModel(t *testing.T) {
	r := NewRouter(model.RoutingWeights{Latency: 1})
	peers := []model.CapabilityDocument{{PeerID: "p1", Models: []model.ModelSpec{{Name: "other"}}, AvgLatencyMS: 10, VRAMFreeMB: 1000, Reliability: 1}}
	req := model.ChatCompletionRequest{Model: "llama3"}
	ranked := r.Rank(peers, req)
	if len(ranked) != 0 {
		t.Fatalf("expected no ranked peers, got %d", len(ranked))
	}
}

func TestRankFingerprintMatch(t *testing.T) {
	r := NewRouter(model.RoutingWeights{Latency: 1})
	peers := []model.CapabilityDocument{
		{
			PeerID: "p1",
			Models: []model.ModelSpec{{Name: "llama3", SHA256: "abc", Tokenizer: "tok1", Quant: "Q4", Backend: "ollama"}},
		},
	}
	req := model.ChatCompletionRequest{
		Model:        "llama3",
		Requirements: model.ModelRequirements{SHA256: "abc", Tokenizer: "tok1", Quant: "Q4", Backend: "ollama"},
	}
	ranked := r.Rank(peers, req)
	if len(ranked) != 1 {
		t.Fatalf("expected 1 ranked peer, got %d", len(ranked))
	}
	req.Requirements.SHA256 = "mismatch"
	ranked = r.Rank(peers, req)
	if len(ranked) != 0 {
		t.Fatalf("expected 0 ranked peers on fingerprint mismatch, got %d", len(ranked))
	}
}

func TestRankPreferFavoritesPolicy(t *testing.T) {
	r := NewRouter(model.RoutingWeights{Latency: 1, FavoriteBonus: 100})
	peers := []model.CapabilityDocument{
		{PeerID: "fav", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 100, VRAMFreeMB: 1000, Reliability: 1},
		{PeerID: "other", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 1, VRAMFreeMB: 1000, Reliability: 1},
	}
	req := model.ChatCompletionRequest{Model: "llama3"}
	ranked := r.RankWithPreferences(peers, req, Preferences{
		Policy:    "prefer_favorites",
		Favorites: map[string]struct{}{"fav": {}},
	})
	if len(ranked) != 1 || ranked[0].Capability.PeerID != "fav" {
		t.Fatalf("expected only favorite peer, got %+v", ranked)
	}
}

func TestRankManualPolicyAllowList(t *testing.T) {
	r := NewRouter(model.RoutingWeights{Latency: 1})
	peers := []model.CapabilityDocument{
		{PeerID: "p1", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 1, VRAMFreeMB: 1000, Reliability: 1},
		{PeerID: "p2", Models: []model.ModelSpec{{Name: "llama3"}}, AvgLatencyMS: 2, VRAMFreeMB: 1000, Reliability: 1},
	}
	req := model.ChatCompletionRequest{Model: "llama3"}
	ranked := r.RankWithPreferences(peers, req, Preferences{
		Policy:  "manual",
		Allowed: map[string]struct{}{"p2": {}},
	})
	if len(ranked) != 1 || ranked[0].Capability.PeerID != "p2" {
		t.Fatalf("expected only allowed peer, got %+v", ranked)
	}
}
