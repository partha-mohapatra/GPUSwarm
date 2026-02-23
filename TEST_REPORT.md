# GPUSwarm Test Report

- Date (UTC): 2026-02-17 22:29 UTC
- Host OS: Ubuntu 22.04.1 LTS (Jammy)
- Architecture: x86_64
- Go toolchain: `go1.22.12` (`/tmp/go/bin/go`)

## Command

```bash
GOCACHE=/tmp/go-build-cache GOPATH=/tmp/go-path /tmp/go/bin/go test ./...
```

## Result

PASS

Package status:

- `infermeshai/internal/backend`: PASS
- `infermeshai/internal/control`: PASS
- `infermeshai/internal/p2p`: PASS
- `infermeshai/internal/routing`: PASS
- `infermeshai/tests`: PASS

## Covered Areas

- OpenAI-compatible streaming path (client -> provider -> backend)
- Peer failover and chaos mid-stream disconnect behavior
- Provider quota/rate enforcement
- Model fingerprint routing constraints
- Optional PoW enforcement flow
- Credit fairness behavior (spend/fail on depletion)
- Control primitives (credits/reputation/PoW)
- Ollama adapter conversion (`/api/chat` -> OpenAI SSE)
- libp2p discovery + capability query + inference stream transport
