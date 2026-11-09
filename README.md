# mini-swe-agent-go

A Go port of [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent): a minimal AI coding agent that solves
tasks by running bash commands, one step at a time.

## Install

```bash
go install github.com/donvito/mini-swe-agent-go/cmd/mini-go@latest
go install github.com/donvito/mini-swe-agent-go/cmd/mini-go-extra@latest
```

## Quick start

```bash
mini-go-extra config set OPENROUTER_API_KEY sk-or-...
mini-go -m openrouter/anthropic/claude-sonnet-4.5 -t "Add a test for the parser"
```

Run `mini-go` without `-t` to type the task (finish with `Ctrl-D`). Useful flags:

| Flag | Meaning |
|------|---------|
| `-m MODEL` | Model, with provider prefix (`openai/`, `anthropic/`, `openrouter/`, `gemini/`, `deepseek/`, `ollama/`, ...) |
| `-y` | Yolo mode: run commands without asking |
| `-c FILE` or `-c key=value` | Config files and overrides, merged in order (replaces the default `mini.yaml`) |
| `-l 0.5` | Cost limit in dollars |

While it runs: press **Enter** to approve a command, type a comment to reject it, `Ctrl+C` to interrupt, and `/y`,
`/c`, `/u`, `/h` to switch between yolo, confirm, and human mode, or show help.

## Configuration

API keys and defaults go in a `.env` file managed with `mini-go-extra config setup|set|unset|edit`
(`~/Library/Application Support/mini-swe-agent-go/` on macOS, `~/.config/mini-swe-agent-go/` on Linux).

Agent settings live in YAML files. Start from [`config/mini.yaml`](config/mini.yaml) or the OpenRouter example in
[`configs/openrouter.yaml`](configs/openrouter.yaml), and make one the default with:

```bash
mini-go-extra config set MSWEA_MINI_CONFIG_PATH /path/to/your-config.yaml
```

For self-hosted models (vLLM, Ollama, LiteLLM proxy, ...), set `model.api_base`. Costs come from the API (OpenRouter),
`model.input_cost_per_token`/`output_cost_per_token`, or litellm's public price list; set
`model.cost_tracking: ignore_errors` for models without pricing.

## As a library

```go
model, _ := models.NewOpenAIModel(map[string]any{"model_name": "openai/gpt-5-mini"})
env, _ := environments.NewLocalEnvironment(nil)
agent, _ := agents.NewDefaultAgent(model, env, agentConfig) // the "agent" section of a config file
result, err := agent.Run(ctx, "Write a hello world script", nil)
```

See [`examples/hello_world`](examples/hello_world/main.go).

## Compared to the Python version

Same configs, prompts, and trajectory format. Not ported: the SWE-bench/ProgramBench runners, the trajectory inspector,
and the singularity, bubblewrap, swerex, and contree environments.

## Development

```bash
go vet ./... && go test ./...
```

## License

MIT. Based on [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent) by Kilian A. Lieret and Carlos E. Jimenez.
