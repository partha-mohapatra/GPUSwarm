# infermeshd Chat CLI Spec

## Objective
Provide interactive prompt/response UX without introducing a second binary.

`infermeshd` remains a single executable and exposes two operator workflows:

- `infermeshd` or `infermeshd daemon`: run daemon (existing behavior)
- `infermeshd chat`: run interactive client prompt against daemon API

## Process Model

### Provider / server host
- Run daemon only (`mode=provider` or `mode=both`).

### Client host
- Run daemon (`mode=client` or `mode=both`) in one terminal.
- Run `infermeshd chat` in another terminal for prompts.

This preserves separation:
- daemon window: infra logs/mesh state
- chat window: user IO + streamed responses

## Command Interface

### `infermeshd chat` flags
- `-daemon-url` (default `http://127.0.0.1:5555`)
- `-model` (default `llama3-8b`)
- `-prompt` (one-shot prompt mode, non-interactive)

### REPL commands
- `/status` or `/current`
- `/quit` or `/exit`
- `/models`
- `/model <name|#>` (no arg opens picker)
- `/peers`
- `/use <peer_id|#>` (no arg opens picker)
- `/routing <auto|manual|prefer_known|prefer_favorites>`

Plain text lines are treated as prompts and streamed via `/v1/chat/completions`.

### Interactive UX
- command history with up/down keys (persisted)
- tab completion for slash commands
- arrow-key selection for model/peer picking
- startup behavior:
  - reset routing to `auto`
  - auto-select discovered model when configured model is missing
  - if multiple peers serve the selected model, prompt for `[auto]` vs pinned peer

## API Usage

Chat CLI talks only to existing daemon endpoints:
- `POST /v1/chat/completions` (streaming SSE relay)
- `GET /v1/mesh/peers`
- `POST /v1/mesh/select/{peer_id}`
- `PUT /v1/mesh/routing`

No daemon protocol change is required.

## Testing Requirements

### Unit / integration tests
- SSE parsing and token rendering
- REPL command handling (`/status`, `/model`, `/peers`, `/use`, `/routing`)
- error handling for non-2xx responses

### E2E test
- start provider + client services with mock backend
- run chat client call path against client daemon URL
- verify streamed response content is returned end-to-end

## Non-goals

- No TUI dashboard in this iteration
- No daemon auto-spawn in this iteration
- No changes to routing/discovery logic
