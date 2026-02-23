# GPUSwarm

GPUSwarm is a local-first peer-to-peer AI inference mesh. The runtime binary is `infermeshd`.

## Download Binaries (No Go Install)

Binaries are published as GitHub Release assets (not committed to git history):

- `infermeshd-linux-amd64`
- `infermeshd-linux-arm64`
- `infermeshd-darwin-amd64`
- `infermeshd-darwin-arm64`
- `infermeshd-windows-amd64.exe`
- `SHA256SUMS`

Download from:

- `https://github.com/partha-mohapatra/GPUSwarm/releases/latest`

Verify checksums:

```bash
sha256sum -c SHA256SUMS
```

## Minimal Setup (Recommended)

Run daemon with no flags:

```bash
./infermeshd-linux-amd64
```

First run launches interactive setup and asks only required inputs:

- mode: `client` / `provider` / `both`
- node name (`client_name` / `provider_name`)
- provider backend (Ollama / LM Studio / manual)
- models to expose (provider mode)

Defaults are auto-applied for transport/discovery, and config is saved to:

- `~/.infermeshai/config.yaml`

Subsequent starts ask whether to reuse saved config.

## Start Modes

### Provider

```bash
./infermeshd-linux-amd64 \
  -mode provider \
  -transport libp2p \
  -provider-name my-provider \
  -backend-kind ollama \
  -backend-url http://127.0.0.1:11434
```
The built-in defaults in `internal/config/config.go` already point `libp2p_bootstrap_peers` and `libp2p_rendezvous` at the EC2 bootstrap (public IP + `infermesh-rendezvous`), so you can omit those flags when testing.

### Client

```bash
./infermeshd-linux-amd64 \
  -mode client \
  -transport libp2p \
  -client-name my-client \
  -client-addr :5555
```
The same default bootstrap/rendezvous applies to clients, letting you run this command without extra arguments for quick testing.

### Chat UI (from client machine)

```bash
./infermeshd-linux-amd64 chat -daemon-url http://127.0.0.1:5555
```

Core chat commands:

- `/help` or `/`
- `/status`
- `/models`
- `/model [name|#]`
- `/peers`
- `/use [peer_id|#]`
- `/routing <auto|manual|prefer_known|prefer_favorites>`
- `/quit`

Detailed chat behavior/spec:

- `docs/chat_cli_spec.md`

## APIs

Client API (default `:5555`):

- `POST /v1/chat/completions`
- `GET /v1/mesh/peers`
- `GET /v1/mesh/routing`
- `PUT /v1/mesh/routing`
- `POST /v1/mesh/select/{peer_id}`

Provider self-inspection (default `:7777`):

- `GET /capabilities`

## Message Flow

1. Client and provider connect to bootstrap peer(s).
2. Provider advertises heartbeat/capabilities.
3. Client discovers/ranks peers.
4. Inference path selection:
   - direct: `client -> provider`
   - fallback: `client -> bootstrap (/infermesh/infer-proxy/1.0.0) -> provider`
5. Response is streamed end-to-end.

## Advanced Configuration

Config file supports YAML/JSON:

- `~/.infermeshai/config.yaml`

Useful advanced flags:

- transport/discovery: `-transport`, `-libp2p-bootstrap-peers`, `-libp2p-rendezvous`, `-libp2p-auto-relay`, `-libp2p-relay-service`, `-libp2p-nat-port-map`, `-libp2p-hole-punching`
- routing policy: `-routing-policy`, `-favorite-peers`, `-blocked-peers`, `-allowed-peers`, `-sticky-peer-ttl`
- provider controls: `-max-inflight`, `-rate-per-second`, `-burst`, `-max-tokens-per-request`, `-night-only`, `-idle-only`, `-pow-difficulty`
- config persistence: `-config`, `-save-config`

Architecture and scaling docs:

- `docs/infer_mesh_ai_architecture.md`
- `docs/scaling_capacity.md`

## AWS Bootstrap

AWS deployment templates/scripts:

- `deploy/aws/README.md`

## Build From Source

```bash
go test ./...
./scripts/build-all.sh
```
