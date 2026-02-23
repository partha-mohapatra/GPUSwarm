package model

import "time"

const (
	HeaderClientPeerID = "X-Infermesh-Client-Peer"
	HeaderPoWNonce     = "X-Infermesh-Pow-Nonce"
	HeaderClientName   = "X-Infermesh-Client-Name"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Model        string            `json:"model"`
	Messages     []Message         `json:"messages"`
	Stream       bool              `json:"stream"`
	MaxTokens    int               `json:"max_tokens,omitempty"`
	Requirements ModelRequirements `json:"requirements,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type ModelRequirements struct {
	SHA256    string `json:"sha256,omitempty"`
	Tokenizer string `json:"tokenizer,omitempty"`
	Quant     string `json:"quant,omitempty"`
	Backend   string `json:"backend,omitempty"`
}

type PeerContext struct {
	ClientPeerID string
	ClientName   string
	PoWNonce     uint64
}

type ModelSpec struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256,omitempty"`
	Tokenizer string `json:"tokenizer,omitempty"`
	Quant     string `json:"quant,omitempty"`
	Backend   string `json:"backend,omitempty"`
	MaxCtx    int    `json:"max_ctx,omitempty"`
}

type CapabilityDocument struct {
	PeerID        string      `json:"peer_id"`
	Self          bool        `json:"self,omitempty"`
	NodeName      string      `json:"node_name,omitempty"`
	Models        []ModelSpec `json:"models"`
	AnnounceAddrs []string    `json:"announce_addrs,omitempty"`
	GPU           string      `json:"gpu"`
	VRAMFreeMB    int         `json:"vram_free_mb"`
	QueueDepth    int         `json:"queue_depth"`
	AvgLatencyMS  int         `json:"avg_latency_ms"`
	Reliability   float64     `json:"historical_reliability"`
	GatewayURL    string      `json:"gateway_url"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type RoutingWeights struct {
	Latency       float64
	QueueDepth    float64
	VRAMPressure  float64
	Reliability   float64
	FavoriteBonus float64
	KnownBonus    float64
}

func (c CapabilityDocument) SupportsModel(model string) bool {
	for _, m := range c.Models {
		if m.Name == model {
			return true
		}
	}
	return false
}

func (c CapabilityDocument) SupportsRequirements(model string, r ModelRequirements) bool {
	for _, m := range c.Models {
		if m.Name != model {
			continue
		}
		if r.SHA256 != "" && m.SHA256 != r.SHA256 {
			continue
		}
		if r.Tokenizer != "" && m.Tokenizer != r.Tokenizer {
			continue
		}
		if r.Quant != "" && m.Quant != r.Quant {
			continue
		}
		if r.Backend != "" && m.Backend != r.Backend {
			continue
		}
		return true
	}
	return false
}
