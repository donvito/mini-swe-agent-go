# mini-swe-agent-go

Go port of https://github.com/SWE-agent/mini-swe-agent. The goal is the simplest, smallest, most readable agent.

## Layout

- `minisweagent` (repo root): interfaces (`Model`, `Environment`, `Agent`), `Message`/`Action`/`Output`, `Interrupt`
  (replaces Python's exceptions), global config dir / `.env` handling
- `agents/`: `DefaultAgent` loop + `InteractiveAgent`. Go has no virtual methods: `DefaultAgent` dispatches
  `AddMessages`/`Step`/`Query`/`ExecuteActions` through its `self` field so subclasses can override them.
- `environments/`, `models/`, `config/` (embedded YAML), `utils/` (merge, gonja templates, struct<->dict)
- `internal/console` (colors, spinner, single shared stdin reader), `internal/cli` (startup, config commands)
- `cmd/mini-go`, `cmd/mini-go-extra`, `examples/hello_world`

## Conventions

- Configs are dicts (`map[string]any`) decoded into structs with json tags via `utils.Decode`; template vars and
  serialization use `utils.ToMap` (keeps native types so templates render like Python/Jinja2).
- Messages are `mswea.Message` (a map); nested values must be plain `map[string]any` / `[]any`.
- Templates are Jinja2 (gonja v2, strict undefined). Keep builtin YAML configs identical to upstream where possible.
- Keep code minimal; don't catch errors just to rethrow. Read `os.Stdin` only through `console.Stdin`.
- Tests: no mocking; use `models.DeterministicModel`, the local environment and `httptest` servers.

## Verification

```bash
gofmt -l . && go vet ./... && go test ./...
GOOS=windows go build ./... && GOOS=linux go build ./...
```

Live check (needs `OPENROUTER_API_KEY`; isolates the global config dir):

```bash
go build -o /tmp/mini-go ./cmd/mini-go
MSWEA_GLOBAL_CONFIG_DIR=/tmp/mswea MSWEA_CONFIGURED=1 /tmp/mini-go -m openrouter/anthropic/claude-haiku-4.5 -y --exit-immediately -l 0.2 -t "..." </dev/null
```
