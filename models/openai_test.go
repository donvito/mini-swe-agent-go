package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
)

type fakeAPI struct {
	server   *httptest.Server
	requests []map[string]any
	headers  []http.Header
}

// newFakeAPI serves the given responses (status, body) in order.
func newFakeAPI(t *testing.T, responses ...[2]any) *fakeAPI {
	api := &fakeAPI{}
	var i atomic.Int32
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		api.requests, api.headers = append(api.requests, body), append(api.headers, r.Header.Clone())
		resp := responses[min(int(i.Add(1))-1, len(responses)-1)]
		w.WriteHeader(resp[0].(int))
		_, _ = w.Write([]byte(resp[1].(string)))
	}))
	t.Cleanup(api.server.Close)
	return api
}

const toolcallResponse = `{"choices": [{"finish_reason": "tool_calls", "message": {"role": "assistant", "content": "Let me look",
  "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "bash", "arguments": "{\"command\": \"ls -la\"}"}}]}}],
  "usage": {"prompt_tokens": 100, "completion_tokens": 10, "cost": 0.25}}`

func TestOpenAIModelToolcall(t *testing.T) {
	api := newFakeAPI(t, [2]any{200, toolcallResponse})
	t.Setenv("TEST_KEY", "secret")
	m, err := NewOpenAIModel(map[string]any{"model_name": "openai/gpt-test", "api_base": api.server.URL, "api_key_env": "TEST_KEY",
		"model_kwargs": map[string]any{"temperature": 0.5, "drop_params": true}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := m.Query(context.Background(), []mswea.Message{
		{"role": "system", "content": "sys"},
		{"role": "user", "content": "task", "extra": map[string]any{"secret": "local only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if actions := msg.Actions(); len(actions) != 1 || actions[0] != (mswea.Action{Command: "ls -la", ToolCallID: "call_1"}) {
		t.Errorf("actions = %v", actions)
	}
	if msg.Extra()["cost"] != 0.25 || msg["content"] != "Let me look" || msg.Extra()["response"] == nil {
		t.Errorf("message = %v", msg)
	}
	req := api.requests[0]
	if req["model"] != "gpt-test" || req["temperature"] != 0.5 || req["drop_params"] != nil || req["tools"] == nil {
		t.Errorf("request = %v", req)
	}
	if strings.Contains(mustJSON(req["messages"]), "local only") {
		t.Error("extra was sent to the API")
	}
	if got := api.headers[0].Get("Authorization"); got != "Bearer secret" {
		t.Errorf("Authorization = %q", got)
	}

	observations, err := m.FormatObservationMessages(msg, []mswea.Output{{Output: "file.txt\n", Returncode: 0}}, nil)
	if err != nil || len(observations) != 1 || observations[0]["role"] != "tool" || observations[0]["tool_call_id"] != "call_1" ||
		!strings.Contains(observations[0]["content"].(string), "file.txt") {
		t.Fatalf("observations = %v %v", observations, err)
	}
}

func TestOpenAIModelFormatErrorPersistsResponse(t *testing.T) {
	api := newFakeAPI(t, [2]any{200, `{"choices": [{"finish_reason": "length", "message": {"role": "assistant", "content": "thinking..."}}], "usage": {"cost": 0.5}}`})
	m, _ := NewOpenAIModel(map[string]any{"model_name": "openai/x", "api_base": api.server.URL,
		"format_error_template": `{% if finish_reason == "length" %}truncated{% else %}{{ error }}{% endif %}`})
	_, err := m.Query(context.Background(), nil)
	interrupt, ok := mswea.AsInterrupt(err)
	if !ok || interrupt.Kind != mswea.FormatError {
		t.Fatalf("expected format error, got %v", err)
	}
	msg := interrupt.Messages[0]
	if msg["content"] != "truncated" || msg.Extra()["cost"] != 0.5 || msg.Extra()["response"] == nil {
		t.Errorf("format error message = %v", msg)
	}
}

func TestTextbasedModel(t *testing.T) {
	api := newFakeAPI(t, [2]any{200, `{"choices": [{"message": {"role": "assistant", "content": "THOUGHT: hi\n\n` + "```mswea_bash_command\\necho hi\\n```" + `"}}], "usage": {"cost": 0.1}}`})
	m, _ := NewTextbasedModel(map[string]any{"model_name": "openai/x", "api_base": api.server.URL})
	msg, err := m.Query(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if actions := msg.Actions(); len(actions) != 1 || actions[0].Command != "echo hi" {
		t.Errorf("actions = %v", actions)
	}
	if api.requests[0]["tools"] != nil {
		t.Error("textbased model sent tools")
	}
	observations, _ := m.FormatObservationMessages(msg, []mswea.Output{{Output: "hi\n"}}, nil)
	if len(observations) != 1 || observations[0]["role"] != "user" {
		t.Errorf("observations = %v", observations)
	}
}

func TestRetries(t *testing.T) {
	retryMinWait, retryMaxWait = time.Millisecond, time.Millisecond
	t.Cleanup(func() { retryMinWait, retryMaxWait = 4*time.Second, 60*time.Second })
	api := newFakeAPI(t, [2]any{429, "rate limited"}, [2]any{500, "oops"}, [2]any{200, toolcallResponse})
	m, _ := NewOpenAIModel(map[string]any{"model_name": "openai/x", "api_base": api.server.URL})
	if _, err := m.Query(context.Background(), nil); err != nil || len(api.requests) != 3 {
		t.Fatalf("err=%v requests=%d", err, len(api.requests))
	}

	api = newFakeAPI(t, [2]any{401, "bad key"}, [2]any{200, toolcallResponse})
	m, _ = NewOpenAIModel(map[string]any{"model_name": "openai/x", "api_base": api.server.URL})
	_, err := m.Query(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "mini-go-extra config set") || len(api.requests) != 1 {
		t.Fatalf("expected immediate auth error, got %v after %d requests", err, len(api.requests))
	}
}

func TestCostTracking(t *testing.T) {
	noCost := `{"choices": [{"message": {"role": "assistant", "content": "", "tool_calls": [{"id": "1", "function": {"name": "bash", "arguments": "{\"command\": \"ls\"}"}}]}}],
	  "usage": {"prompt_tokens": 1000, "completion_tokens": 100, "prompt_tokens_details": {"cached_tokens": 200}}}`
	api := newFakeAPI(t, [2]any{200, noCost})
	m, _ := NewOpenAIModel(map[string]any{"model_name": "openai/x", "api_base": api.server.URL, "input_cost_per_token": 1e-6, "output_cost_per_token": 1e-5})
	msg, err := m.Query(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cost := msg.Extra()["cost"].(float64); cost < 0.00199 || cost > 0.00201 {
		t.Errorf("cost = %v, want 0.002", cost)
	}

	t.Setenv("MSWEA_COST_MAP_URL", "http://127.0.0.1:1/unreachable")
	t.Setenv("MSWEA_GLOBAL_CONFIG_DIR", t.TempDir())
	m, _ = NewOpenAIModel(map[string]any{"model_name": "openai/unknown-model-xyz", "api_base": api.server.URL})
	if _, err := m.Query(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "ignore_errors") {
		t.Errorf("expected cost error, got %v", err)
	}
	m, _ = NewOpenAIModel(map[string]any{"model_name": "openai/unknown-model-xyz", "api_base": api.server.URL, "cost_tracking": "ignore_errors"})
	if msg, err := m.Query(context.Background(), nil); err != nil || msg.Extra()["cost"] != 0.0 {
		t.Errorf("got %v %v", msg, err)
	}
}

func TestProviderResolution(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-key")
	for _, tc := range []struct {
		config                        map[string]any
		provider, apiModel, base, key string
		cacheControl                  string
	}{
		{map[string]any{"model_name": "openai/gpt-5"}, "openai", "gpt-5", "https://api.openai.com/v1", "", ""},
		{map[string]any{"model_name": "gpt-5"}, "openai", "gpt-5", "https://api.openai.com/v1", "", ""},
		{map[string]any{"model_name": "openrouter/anthropic/claude-sonnet-4.5"}, "openrouter", "anthropic/claude-sonnet-4.5", "https://openrouter.ai/api/v1", "or-key", "default_end"},
		{map[string]any{"model_name": "anthropic/claude-sonnet-4.5", "provider": "openrouter", "set_cache_control": nil}, "openrouter", "anthropic/claude-sonnet-4.5", "https://openrouter.ai/api/v1", "or-key", ""},
		{map[string]any{"model_name": "anthropic/claude-opus-4"}, "anthropic", "claude-opus-4", "https://api.anthropic.com/v1", "", ""},
		{map[string]any{"model_name": "gemini/gemini-3-pro"}, "gemini", "gemini-3-pro", "https://generativelanguage.googleapis.com/v1beta/openai", "", ""},
		{map[string]any{"model_name": "my-model", "model_kwargs": map[string]any{"api_base": "http://localhost:8000/v1", "api_key": "k"}}, "openai", "my-model", "http://localhost:8000/v1", "k", ""},
	} {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("GEMINI_API_KEY", "")
		m, err := NewOpenAIModel(tc.config)
		if err != nil {
			t.Fatal(err)
		}
		if m.provider != tc.provider || m.apiModelName != tc.apiModel || m.apiBase != tc.base || m.apiKey != tc.key || m.Config.SetCacheControl != tc.cacheControl {
			t.Errorf("%v: got provider=%s model=%s base=%s key=%s cache=%q", tc.config, m.provider, m.apiModelName, m.apiBase, m.apiKey, m.Config.SetCacheControl)
		}
	}
	for name, want := range map[string]bool{
		"anthropic/claude-sonnet-4.5": true, "openrouter/anthropic/claude-opus-4.6": true, "claude-3-5-haiku": true,
		"Jackrong/Qwopus3.8-27B-Coder-Flash-GGUF:Q4_K_M": false, "gpt-5": false, "deepseek/deepseek-v4.1-flash": false,
	} {
		if isAnthropicModel(name) != want {
			t.Errorf("isAnthropicModel(%q) = %v", name, !want)
		}
	}
	if _, err := NewOpenAIModel(map[string]any{"model_name": "hosted_vllm/x"}); err == nil {
		t.Error("expected error for provider without base URL")
	}
	m, _ := NewOpenAIModel(map[string]any{"model_name": "x", "model_kwargs": map[string]any{"api_key": "secret"}})
	if s := mustJSON(m.Serialize()); strings.Contains(s, "secret") {
		t.Errorf("api key leaked into serialization: %s", s)
	}
}

func TestGetModel(t *testing.T) {
	t.Setenv("MSWEA_MODEL_NAME", "")
	if _, err := GetModel("", nil); err == nil {
		t.Error("expected error without model name")
	}
	t.Setenv("MSWEA_MODEL_NAME", "openai/from-env")
	for class, want := range map[string]string{"": "models.OpenAIModel", "litellm": "models.OpenAIModel", "textbased": "models.TextbasedModel",
		"litellm_textbased": "models.TextbasedModel", "deterministic": "models.DeterministicModel"} {
		m, err := GetModel("", map[string]any{"model_class": class})
		if err != nil {
			t.Fatal(err)
		}
		model := m.Serialize()["info"].(map[string]any)["config"].(map[string]any)
		if model["model_type"] != want {
			t.Errorf("class %q: got %v, want %s", class, model["model_type"], want)
		}
	}
	m, _ := GetModel("openrouter/x", map[string]any{"model_name": "ignored"})
	if m.(*OpenAIModel).Config.ModelName != "openrouter/x" {
		t.Error("input model name should take precedence")
	}
	if _, err := GetModel("x", map[string]any{"model_class": "nope"}); err == nil {
		t.Error("expected error for unknown class")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
