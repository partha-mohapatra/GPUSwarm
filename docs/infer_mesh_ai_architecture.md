# Project Name: **GPUSwarm**

> *A peer-to-peer GPU-sharing network for offline and local-first Large Language Model inference.*

---

## 1. Vision

Modern LLMs require GPUs, but most GPUs sit idle for large portions of the day. At the same time, many developers and users want **offline, private, and cost-free access to powerful models**.

**GPUSwarm** democratizes GPU access by turning idle GPUs into a **peer-to-peer inference mesh**, inspired by BitTorrent, powered by libp2p, and compatible with existing local LLM runtimes like Ollama and LM Studio.

No cloud. No centralized GPUs. No paid certificates.

---

## 2. Core Goals

- Enable GPU-less machines to run LLM inference via nearby or trusted peers
- Preserve local-first and offline-first workflows
- Avoid centralized infrastructure
- Maintain owner control over GPU usage
- Support OpenAI-compatible APIs
- Scale organically via peer discovery

---

## 3. High-Level Architecture

### Node Types

#### 3.1 Client Node (Consumer)

- Runs on CPU-only machines
- Exposes a local OpenAI-compatible API
- Routes inference requests to peers

**Default Port**: `5555`

Responsibilities:

- Accept `/v1/chat/completions`
- Discover peers via DHT
- Select best peer based on capability and latency
- Stream responses back to client applications

---

#### 3.2 GPU Provider Node (Producer)

- Runs on machines with GPUs
- Hosts one or more LLM runtimes (Ollama, LM Studio, llama.cpp)
- Exposes inference gateway

**Gateway Port**: `7777`

Responsibilities:

- Advertise available models and capacity
- Enforce quotas and rate limits
- Forward requests to local LLM runtime
- Stream responses back to requester

---

## 4. Network Layer

### 4.1 libp2p Identity

- Each node generates a cryptographic keypair
- PeerID = hash(public key)
- No CA, no certificates, no renewal
- Identity is self-sovereign and verifiable

### 4.2 Peer Discovery

- Kademlia DHT for global discovery
- mDNS for LAN discovery
- Gossip-based heartbeat for liveness
- AutoNAT + hole punching (DCUtR) for direct NAT traversal
- Circuit Relay v2 fallback for peers behind CGNAT/symmetric NAT

### 4.3 Transport Security

- Encrypted streams using Noise or libp2p TLS
- Authentication via PeerID
- Replay protection and integrity guaranteed

### 4.4 Protocol Inventory (Current)

libp2p stream protocols:

- `/infermesh/capability/1.0.0`:
  - Query provider capability document from a peer.
- `/infermesh/infer/1.0.0`:
  - Direct client-to-provider inference stream.
- `/infermesh/infer-proxy/1.0.0`:
  - Bootstrap-assisted inference proxy fallback (`client -> bootstrap -> provider`).
- `/infermesh/registry/1.0.0`:
  - Bootstrap registry query for recently seen provider capabilities.

GossipSub topic:

- `infermesh.heartbeat.v1`:
  - Periodic provider heartbeat (liveness + advertised metadata).

HTTP API surface:

- `POST /v1/chat/completions` (client API)
- `GET /v1/mesh/peers` (client peer visibility)
- `GET /v1/mesh/routing` (client routing state)
- `PUT /v1/mesh/routing` (client routing policy update)
- `POST /v1/mesh/select/{peer_id}` (client manual peer select)
- `GET /capabilities` (provider self-inspection)

---

## 5. Model Advertisement & Capability Discovery

Each GPU node periodically publishes a capability document:

```json
{
  "peer_id": "QmXYZ...",
  "models": [
    {
      "name": "llama3-70b",
      "sha256": "abc123",
      "quant": "Q4_K_M",
      "backend": "ollama",
      "max_ctx": 8192
    }
  ],
  "gpu": "RTX 4090",
  "vram_free_mb": 18000,
  "queue_depth": 1,
  "avg_latency_ms": 40
}
```

Clients may require exact or compatible model fingerprints.

---

## 6. Request Flow

### 6.1 Discovery and Selection Flow

1. Application sends request to client API (`localhost:5555` by default).
2. Client daemon parses model requirements.
3. Client builds peer candidates from:
   - currently connected libp2p peers
   - DHT rendezvous results
   - GossipSub heartbeat cache
   - bootstrap registry response (`/infermesh/registry/1.0.0`)
4. Routing score/policy is applied.
5. Best peer is selected.

### 6.2 Data Path Flow

1. Client attempts direct inference stream to selected provider (`/infermesh/infer/1.0.0`).
2. If direct stream open fails, client falls back to bootstrap proxy stream:
   - `client -> bootstrap (/infermesh/infer-proxy/1.0.0) -> provider`
3. Provider forwards request to local backend runtime (e.g., Ollama `:11434`).
4. Token stream is forwarded back through the same path to the client.

Streaming is preserved end-to-end for both direct and proxy paths.

### 6.3 Bootstrap Configuration Model

For internet-separated deployments, client and provider configuration only requires:

- `libp2p_bootstrap_peers`
- `libp2p_rendezvous`

Provider addresses are not manually configured on clients.

### 6.4 Route Learning and Retry Behavior

- Discovery capability probes are not required for every request.
- If a peer already has heartbeat/registry capability data, repeated capability probing is skipped.
- Failed capability probes are backoff-limited (short retry window) to avoid repeated dial spam.
- Inference transport caches successful route per peer:
  - `direct`
  - `proxy:<bootstrap_peer_id>`
- Subsequent requests prefer cached working route first, reducing repeated timeout-first behavior.

---

## 7. Routing Logic

Routing score example:

```
score =
  w1 * latency
+ w2 * queue_depth
+ w3 * vram_pressure
+ w4 * historical_reliability
```

Lowest score wins.

### 7.1 Routing Policies

In addition to pure score-based routing, nodes can apply policy overlays:

- `auto`: choose lowest score peer
- `prefer_known`: bias peers with successful history
- `prefer_favorites`: prefer user-marked favorite peers
- `manual`: route only to allowlisted peer IDs

### 7.2 Peer Preference Controls

Clients may configure:

- Favorite peers
- Blocked peers
- Allowed peers (manual mode)
- Sticky peer TTL (reuse successful peer per model for short windows)

All policy decisions remain bounded by model-fingerprint compatibility checks.

---

## 8. Resource Control & Safety

### GPU Owner Controls

- Max concurrent requests
- Max tokens per peer
- Idle-only serving mode
- Thermal and power limits
- Time-based availability (e.g., night-only)
- Optional PoW difficulty requirement
- Minimum client reputation threshold

### Automatic Throttling

- High VRAM usage → temporary unadvertise
- Overheating → reject new requests

---

## 9. Abuse Prevention

### 9.1 Credit-Based Fairness

- Earn credits by serving inference
- Spend credits by consuming inference
- Idle GPUs naturally accumulate credits

### 9.2 Reputation System

Peers track:

- Timeouts
- Aborted streams
- Invalid requests

Low reputation peers are deprioritized or blocked.

Providers may enforce hard rejection below a configured minimum reputation.

### 9.3 Optional Proof-of-Work

Clients may be required to compute lightweight PoW before inference.

---

## 10. Model Consistency

To ensure predictable behavior:

Must match:

- Model file hash
- Tokenizer

Should match:

- Quantization
- Backend implementation
- Sampling parameters

Model fingerprints are mandatory for routing decisions.

---

## 11. Optional Economy Layer (Future)

- Signed inference receipts
- Micropayments (UPI / Lightning / crypto)
- Verified provider badges
- Marketplace discovery

These are **opt-in** and not required for network operation.

---

## 12. Implementation Stack

- Language: Go or Rust
- Networking: libp2p
- Discovery: Kademlia DHT
- Messaging: GossipSub
- NAT traversal: AutoNAT + DCUtR
- Relay fallback: Circuit Relay v2
- API: OpenAI-compatible HTTP + SSE
- Runtime adapters: Ollama, LM Studio, llama.cpp

---

## 13. Runtime Configuration & Persistence

The daemon supports a single persisted config (`yaml` or `json`) that is mode-aware:

- `mode: client|provider|both`
- One-time configuration stored and reused on subsequent starts
- CLI flags can override config values

Default config path:

- `~/.infermeshai/config.yaml`

This avoids requiring long command lines for normal operation.

---

## 14. Peer Visibility API

Client nodes expose peer visibility endpoint:

- `GET /v1/mesh/peers`

Returns currently discovered providers with capability metadata (including provider name).

### 14.1 Provider Self-Inspection

Provider nodes expose local capability state:

- `GET /capabilities` on provider API (default `:7777`)

This allows provider operators to confirm what they are advertising without running a separate client daemon.

---

## 15. Observability and Logging

Structured logs are metadata-first and privacy-preserving by default.

Logged:

- peer discovery snapshots and counters
- transport errors and failover behavior
- heartbeat activity
- runtime configuration details
- route-learning effects (direct/proxy path usage via transport events)

Not logged by default:

- full prompts
- full model output text

This keeps operational insight high while minimizing accidental content leakage.

---

## 16. Testing Strategy (No GPU, No Ollama Required)

GPUSwarm is designed to be fully testable on CPU-only machines and in CI environments such as GitHub Actions. Inference is treated as a pluggable backend, allowing comprehensive validation without real models.

---

### 16.1 Mock Inference Backend

A **mock LLM backend** replaces Ollama/LM Studio during testing.

Capabilities:

- OpenAI-compatible `/v1/chat/completions` endpoint
- Token-by-token streaming using SSE or chunked HTTP
- Configurable latency per token
- Configurable max tokens
- Simulated failures (timeouts, mid-stream disconnects)
- Simulated queue depth

---

### 16.2 Single-Machine Test Topology

All components run as separate processes on one machine:

- infermeshd (client mode) → port 5555
- infermeshd (provider mode) → port 7777
- mock-backend → port 9000

Flow: Client → 5555 → libp2p stream → 7777 → 9000 → streamed response

---

### 16.3 Multi-Peer Simulation

Multiple provider nodes are launched on different ports with varying characteristics (fast, slow, error-prone).

Routing must adapt dynamically based on observed performance.

---

### 16.4 Client-Side Tests

- OpenAI API compatibility
- Streaming correctness
- Retry and timeout handling
- Peer failover

---

### 16.5 Provider-Side Tests

- Capability advertisement
- Quota enforcement
- Rate limiting
- Load-based throttling

---

### 16.6 Chaos Testing

- Random latency injection
- Connection drops
- Partial streams
- Peer disappearance

---

### 16.7 CI Testing (GitHub Actions)

- Launch mock backend
- Launch provider infermeshd
- Launch client infermeshd
- Run integration and chaos tests

---

## 17. Phased Roadmap

### Phase 1 – Minimal Mesh

- libp2p identity
- DHT discovery
- Single-model routing
- Ollama adapter

### Phase 2 – Intelligent Routing

- Load-aware selection
- Streaming reliability
- Model fingerprints
- Per-peer quotas

### Phase 3 – Trust & Fairness

- Credit accounting
- Reputation scoring
- Abuse mitigation

### Phase 4 – Ecosystem

- Plugin system
- UI dashboards
- Optional payments
- Verified nodes

---

## 18. Non-Goals

- Centralized inference
- Mandatory payments
- Cloud dependency
- Fine-tuning as a service

---

## 19. Summary

GPUSwarm is a foundational primitive for local-first AI: a decentralized inference layer that lets GPUs act like shared infrastructure without surrendering control.

It combines the resilience of P2P systems with the practicality of existing LLM tooling — enabling powerful models everywhere, even where GPUs are scarce.

---

*End of document*
