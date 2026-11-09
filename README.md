# mini-swe-agent-go

A Go port of [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent): the minimal AI software engineering agent
that solves tasks by issuing bash commands, one step at a time.

Like the original, it is deliberately small and readable:

- **Just bash.** The agent has a single tool, `bash`. No custom tools or editors.
- **Independent actions.** Every command runs in a fresh subprocess (`sh -c` locally or `docker exec`), so sandboxing is trivial.
- **Linear history.** The trajectory is exactly the list of messages that the LM sees.

It reads the same YAML configs and Jinja2 prompt templates as the Python version and writes trajectories in the same
format (`mini-swe-agent-1.1`).

## Install

```bash
go install github.com/donvito/mini-swe-agent-go/cmd/mini-go@latest
go install github.com/donvito/mini-swe-agent-go/cmd/mini-go-extra@latest
```

## Usage

```bash
mini-go                                # first run asks for your model and API key
mini-go -m openai/gpt-5-mini -t "Fix the failing test in tests/test_parser.py"
mini-go -m openrouter/anthropic/claude-sonnet-4.5 -y   # yolo mode: no confirmations
mini-go -c mini.yaml -c agent.step_limit=20 -c model.model_kwargs.temperature=0
mini-go -c mini_textbased.yaml         # parse actions from markdown code blocks instead of tool calls
mini-go --environment-class docker -c mini.yaml -c environment.image=python:3.12
```

During a run, you can press `Ctrl+C` to interrupt the agent and leave a comment. At any prompt you can switch modes:
`/y` (yolo), `/c` (confirm commands), `/u` (human: you type the commands), `/m` (multiline input), `/h` (help).

Manage the global config file (API keys, default model) with the commands below. It lives at
`~/Library/Application Support/mini-swe-agent-go/.env` on macOS and `~/.config/mini-swe-agent-go/.env` on Linux
(override the directory with `MSWEA_GLOBAL_CONFIG_DIR`), separate from the Python version's config.

```bash
mini-go-extra config setup
mini-go-extra config set OPENAI_API_KEY sk-...
mini-go-extra config unset OPENAI_API_KEY
mini-go-extra config edit
```

## As a library

```go
model, _ := models.NewOpenAIModel(map[string]any{"model_name": "openai/gpt-5-mini"})
env, _ := environments.NewLocalEnvironment(nil)
agent, _ := agents.NewDefaultAgent(model, env, agentConfig) // e.g. the "agent" section of config/mini.yaml
result, err := agent.Run(ctx, "Write a hello world script", nil)
```

See [`examples/hello_world`](examples/hello_world/main.go) and [`cmd/mini-go`](cmd/mini-go/main.go).

## Layout

| Package         | Contents                                                                                     |
|-----------------|----------------------------------------------------------------------------------------------|
| `minisweagent`  | `Model`, `Environment`, `Agent` interfaces, message types, interrupts, global config         |
| `agents`        | `DefaultAgent` (the loop) and `InteractiveAgent` (human in the loop)                         |
| `environments`  | `local` and `docker`                                                                         |
| `models`        | OpenAI-compatible toolcall and textbased models, deterministic test models                   |
| `config`        | Builtin configs (`mini.yaml`, `default.yaml`, `mini_textbased.yaml`) and `-c` spec parsing   |
| `cmd`           | `mini-go` and `mini-go-extra`                                                                |

## Models

There is no litellm in Go, so models talk to any **OpenAI-compatible chat completions API** directly.
The provider prefix of the model name selects the base URL and API key variable:

| Prefix          | API key variable       | Prefix         | API key variable        |
|-----------------|------------------------|----------------|-------------------------|
| `openai/`       | `OPENAI_API_KEY`       | `deepseek/`    | `DEEPSEEK_API_KEY`      |
| `anthropic/`    | `ANTHROPIC_API_KEY`    | `groq/`        | `GROQ_API_KEY`          |
| `openrouter/`   | `OPENROUTER_API_KEY`   | `mistral/`     | `MISTRAL_API_KEY`       |
| `gemini/`       | `GEMINI_API_KEY`       | `xai/`         | `XAI_API_KEY`           |
| `together_ai/`  | `TOGETHERAI_API_KEY`   | `fireworks_ai/`| `FIREWORKS_AI_API_KEY`  |
| `requesty/`     | `REQUESTY_API_KEY`     | `ollama/`      | (none, localhost:11434) |

For local or self-hosted models (vLLM, LiteLLM proxy, ...), set the base URL:

```yaml
model:
  model_name: my-model
  api_base: http://localhost:8000/v1   # or model_kwargs.api_base, or $<PROVIDER>_API_BASE
  api_key_env: MY_API_KEY              # name of the env var holding the key (optional)
  cost_tracking: ignore_errors
```

Model classes (`--model-class` or `model.model_class`): `openai` (default, tool calls), `textbased`, `openrouter`,
`openrouter_textbased`, `deterministic`, `deterministic_toolcall`. The Python names `litellm` and `litellm_textbased`
are accepted as aliases.

**Cost tracking** uses the cost reported by the API (OpenRouter), then `input_cost_per_token`/`output_cost_per_token`
from the model config, then litellm's public
[cost map](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json) (downloaded and cached
for a day in the global config dir; override with `MSWEA_COST_MAP_URL`, or point `litellm_model_registry` at a local
file in the same format). If the cost can't be determined the run stops, unless `cost_tracking: ignore_errors` (or
`MSWEA_COST_TRACKING=ignore_errors`) is set.

## Differences from the Python version

- Not ported: the SWE-bench/ProgramBench batch runners, the trajectory inspector, and the singularity, bubblewrap,
  swerex, contree environments; Portkey, the Responses API, and roulette models.
- Anthropic models get prompt cache markers by default, except through Anthropic's own OpenAI compatibility layer
  (which doesn't support them). Use `openrouter/anthropic/...` to get caching.
- `mini_textbased.yaml` sets `model_class: textbased`, so `--model-class` isn't needed.
- Prompts are plain line input (no prompt_toolkit history); multiline input ends with `Ctrl-D`.
- Template `tojson` doesn't escape non-ASCII characters (the JSON is still valid).

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

License: MIT (see [LICENSE.md](LICENSE.md)); prompts and design are from the original mini-swe-agent.
