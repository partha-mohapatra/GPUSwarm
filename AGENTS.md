# Agents.md — GPUSwarm Coding & Architecture Guide

This document instructs **Codex / AI agents / contributors** on how to write code for **GPUSwarm** so that it remains **architecturally sound, highly performant, well-tested, and cross-platform**.

This file is **normative**: agents MUST follow these rules unless explicitly overridden.

---

## 1. Project Scope & Principles

GPUSwarm is **infrastructure software**, not an application.

Agents must prioritize:

- Correctness over convenience
- Deterministic behavior over heuristics
- Bounded resource usage (CPU, memory, goroutines/threads)
- Failure isolation
- Testability in CPU-only environments
- Cross-platform portability

The primary deliverable is a single executable:

```
infermeshd
```

Running on:
- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64)

---

## 2. Architectural Constraints (MUST FOLLOW)

### 2.1 Process Model

- infermeshd MUST be a **single-process daemon**
- Modes are selected via CLI flags (`client`, `provider`, `both`)
- No external runtime dependencies at execution time

### 2.2 Layered Architecture (STRICT)

Code MUST be separated into the following layers:

1. **API Layer**
   - OpenAI-compatible HTTP + SSE
   - No business logic

2. **Routing Layer**
   - Peer scoring
   - Failover logic
   - No networking primitives

3. **P2P Layer**
   - libp2p identity
   - Encrypted streams
   - DHT discovery

4. **Backend Adapter Layer**
   - Ollama / LM Studio / mock backend
   - Stateless request forwarding

5. **Control Layer**
   - Quotas
   - Credits
   - Rate limiting

Cross-layer imports are forbidden.

---

## 3. Performance & Resource Rules

### 3.1 CPU Constraints

- No busy-wait loops
- All blocking I/O must be cancellable via context
- Goroutines / threads must be bounded
- Avoid reflection in hot paths

### 3.2 Memory Constraints

- Streaming must be zero-copy where possible
- No buffering of full responses in memory
- Maximum in-flight request memory must be bounded
- Use pools for frequently allocated objects

### 3.3 Streaming Rules

- Streaming is mandatory for inference paths
- Token chunks must be forwarded immediately
- Backpressure must propagate end-to-end

---

## 4. Backend Adapter Contract

All inference engines MUST implement the same interface:

```
Infer(ctx, Request) -> Stream<ResponseChunk>
```

Rules:
- Adapters must be stateless
- No retries inside adapters
- No routing logic inside adapters
- Mock backend MUST fully conform to this interface

---

## 5. Testing Requirements (MANDATORY)

### 5.1 Test Pyramid

Agents must implement:

1. **Unit Tests**
   - Routing logic
   - Scoring functions
   - Quota enforcement

2. **Integration Tests**
   - infermeshd client + provider
   - Mock backend streaming

3. **Chaos Tests**
   - Latency injection
   - Mid-stream disconnects
   - Peer disappearance

Any new feature MUST include tests at the appropriate level.

---

### 5.2 CPU-Only Guarantee

- No test may require a GPU
- No test may require Ollama or LM Studio
- All tests MUST pass using the mock backend

---

### 5.3 CI Compatibility

All tests MUST be runnable in:

- GitHub Actions (ubuntu-latest)
- < 2 CPU cores
- < 2GB RAM

Tests exceeding these limits are invalid.

---

## 6. Failure & Safety Rules

Agents must assume:

- Peers are unreliable
- Networks partition
- Streams terminate unexpectedly

Required behaviors:
- Fail fast on unrecoverable errors
- Retry only at routing layer
- Never panic in production paths
- All failures must be observable via logs

---

## 7. Cross-Platform Build Rules

### 7.1 Binary Generation

The project MUST produce static binaries for:

- linux/amd64
- linux/arm64
- darwin/amd64
- darwin/arm64
- windows/amd64

Builds must:
- Avoid platform-specific syscalls unless gated
- Use portable file paths
- Avoid epoll/kqueue-specific logic in core paths

---

### 7.2 CI Build Matrix (Required)

Agents MUST ensure a build matrix similar to:

- GOOS × GOARCH (or Rust target triples)
- Artifact upload per platform

Cross-compilation must succeed without local toolchains.

---

## 8. Observability Requirements

All long-running operations MUST:

- Emit structured logs
- Include peer_id, request_id, and model name
- Expose lightweight metrics hooks (counters, gauges)

No heavy telemetry libraries allowed.

---

## 9. Security Rules

- All P2P traffic MUST be encrypted
- Peer identity MUST be verified
- No plaintext inference payloads on the wire
- No hardcoded secrets

Self-signed or ephemeral identities are acceptable.

---

## 10. What Agents MUST NOT Do

- Do NOT introduce global mutable state
- Do NOT add cloud dependencies
- Do NOT add UI frameworks
- Do NOT add GPU-specific code outside adapters
- Do NOT bypass routing logic

---

## 11. Contribution Quality Bar

Code is acceptable ONLY IF:

- Architecture boundaries are respected
- Tests are included and meaningful
- CPU & memory usage are bounded
- Builds succeed on all supported platforms
- Mock backend tests pass

If any of these fail, the change MUST be rejected.

---

## 12. Summary for Codex / Agents

When in doubt:

- Choose the simpler design
- Prefer determinism over optimization
- Treat infermeshd like a kernel component, not an app
- Assume hostile networks
- Test everything without GPUs

---

## 13. Operational Memory (Do Not Forget)

These conventions are mandatory for all future agent actions in this repo.

### 13.1 Build Output Location

- All generated executables MUST be created under `dist/`.
- Agents MUST NOT create `infermeshd-*` binaries in repository root.
- Preferred build command:

```bash
./scripts/build.sh
```

- Cross-build example:

```bash
GOOS=darwin GOARCH=arm64 ./scripts/build.sh
```

### 13.2 Runtime File Location

- Runtime state files (identity, logs, config) MUST default to `~/.infermeshai/`.
- Agents MUST avoid creating runtime artifacts in repository root.

### 13.3 Verification Workflow

After touching runtime/discovery code, agents should validate:

1. Build from repo root to `dist/`
2. Start daemon from dist binary
3. Confirm `/v1/mesh/peers` shows local peer with `self=true`
4. Confirm logs include discovery snapshot details and DHT advertise with connected peer count

GPUSwarm must remain **portable, predictable, and resilient**.

---

*End of Agents.md*
