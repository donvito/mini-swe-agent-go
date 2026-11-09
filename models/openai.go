package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

type OpenAIModelConfig struct {
	// Model name. Include the provider, e.g. "openai/gpt-5-mini" or "openrouter/anthropic/claude-sonnet-4.5".
	ModelName string `json:"model_name"`
	// Additional arguments passed to the API (e.g., temperature). litellm-specific keys like drop_params are
	// dropped; api_base, api_key, extra_headers and timeout are honored.
	ModelKwargs map[string]any `json:"model_kwargs"`
	// Provider (openai, anthropic, openrouter, gemini, ...). Inferred from the model name by default.
	Provider string `json:"provider"`
	// Base URL of the OpenAI-compatible API. Defaults to $<PROVIDER>_API_BASE or the provider's default URL.
	APIBase string `json:"api_base"`
	// Name of the environment variable holding the API key. Defaults to the provider's usual variable.
	APIKeyEnv string `json:"api_key_env"`
	// Cost per token. If unset, costs reported by the API or the litellm cost map are used.
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
	// Model registry (litellm format) for cost tracking.
	LitellmModelRegistry string `json:"litellm_model_registry"`
	// Set explicit cache control markers ("default_end"), e.g. for Anthropic models via OpenRouter.
	SetCacheControl string `json:"set_cache_control"`
	// Cost tracking mode: "default" (error if the cost can't be determined) or "ignore_errors".
	CostTracking string `json:"cost_tracking"`
	// Template used when the LM's output is not in the expected format.
	FormatErrorTemplate string `json:"format_error_template"`
	// Template used to render the observation after executing an action.
	ObservationTemplate string `json:"observation_template"`
	// Regex to extract multimodal content. Empty string disables multimodal processing.
	MultimodalRegex string `json:"multimodal_regex"`
	// Regex to extract the action from the LM's output (textbased models only).
	ActionRegex string `json:"action_regex,omitempty"`
}

type provider struct{ apiBase, apiKeyEnv string }

var providers = map[string]provider{
	"openai":       {"https://api.openai.com/v1", "OPENAI_API_KEY"},
	"anthropic":    {"https://api.anthropic.com/v1", "ANTHROPIC_API_KEY"},
	"openrouter":   {"https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"},
	"gemini":       {"https://generativelanguage.googleapis.com/v1beta/openai", "GEMINI_API_KEY"},
	"deepseek":     {"https://api.deepseek.com/v1", "DEEPSEEK_API_KEY"},
	"groq":         {"https://api.groq.com/openai/v1", "GROQ_API_KEY"},
	"mistral":      {"https://api.mistral.ai/v1", "MISTRAL_API_KEY"},
	"xai":          {"https://api.x.ai/v1", "XAI_API_KEY"},
	"together_ai":  {"https://api.together.xyz/v1", "TOGETHERAI_API_KEY"},
	"fireworks_ai": {"https://api.fireworks.ai/inference/v1", "FIREWORKS_AI_API_KEY"},
	"requesty":     {"https://router.requesty.ai/v1", "REQUESTY_API_KEY"},
	"ollama":       {"http://localhost:11434/v1", ""},
	"hosted_vllm":  {"", "HOSTED_VLLM_API_KEY"},
}

// Keys of model_kwargs that are not sent in the request body.
var droppedKwargs = map[string]bool{
	"drop_params": true, "custom_llm_provider": true, "api_base": true, "base_url": true, "api_key": true,
	"api_version": true, "extra_headers": true, "timeout": true, "num_retries": true,
}

// Message keys that are sent to the API (everything else, e.g. "extra", stays local).
var apiMessageKeys = []string{"role", "content", "name", "tool_calls", "tool_call_id", "cache_control", "reasoning_details"}

// OpenAIModel talks to any OpenAI-compatible chat completions API and uses tool calls to get actions.
type OpenAIModel struct {
	Config       OpenAIModelConfig
	provider     string
	apiModelName string
	apiBase      string
	apiKey       string
	client       *http.Client
}

// TextbasedModel is like OpenAIModel but parses actions from the response text with a regex instead of
// using tool calls.
type TextbasedModel struct{ *OpenAIModel }

func defaultConfig() OpenAIModelConfig {
	costTracking := os.Getenv("MSWEA_COST_TRACKING")
	if costTracking == "" {
		costTracking = "default"
	}
	return OpenAIModelConfig{
		ModelKwargs:          map[string]any{},
		LitellmModelRegistry: os.Getenv("LITELLM_MODEL_REGISTRY_PATH"),
		CostTracking:         costTracking,
		FormatErrorTemplate:  "{{ error }}",
		ObservationTemplate:  defaultObservationTemplate,
	}
}

func NewOpenAIModel(config map[string]any) (*OpenAIModel, error) {
	return newOpenAIModel(config, defaultConfig())
}

func NewTextbasedModel(config map[string]any) (*TextbasedModel, error) {
	defaults := defaultConfig()
	defaults.ActionRegex = "```mswea_bash_command\\s*\\n(.*?)\\n```"
	defaults.FormatErrorTemplate = "Please always provide EXACTLY ONE action in triple backticks, found {{actions|length}} actions."
	m, err := newOpenAIModel(config, defaults)
	if err != nil {
		return nil, err
	}
	return &TextbasedModel{m}, nil
}

func newOpenAIModel(config map[string]any, defaults OpenAIModelConfig) (*OpenAIModel, error) {
	m := &OpenAIModel{Config: defaults, client: &http.Client{}}
	if err := utils.Decode(config, &m.Config); err != nil {
		return nil, err
	}
	if m.Config.ModelName == "" {
		return nil, errors.New("model_name is required")
	}
	if m.Config.CostTracking != "default" && m.Config.CostTracking != "ignore_errors" {
		return nil, fmt.Errorf("invalid cost_tracking %q (must be 'default' or 'ignore_errors')", m.Config.CostTracking)
	}
	m.resolveProvider()
	// Anthropic models get cache control by default (not for Anthropic's own OpenAI compatibility layer,
	// which doesn't support it).
	if _, ok := config["set_cache_control"]; !ok && isAnthropicModel(m.Config.ModelName) && m.provider != "anthropic" {
		m.Config.SetCacheControl = "default_end"
	}
	if timeout, ok := m.Config.ModelKwargs["timeout"].(float64); ok {
		m.client.Timeout = time.Duration(timeout * float64(time.Second))
	}
	if m.apiBase == "" {
		return nil, fmt.Errorf("no API base URL for model %q (provider %q); set model.api_base", m.Config.ModelName, m.provider)
	}
	return m, nil
}

func (m *OpenAIModel) resolveProvider() {
	m.provider, m.apiModelName = m.Config.Provider, m.Config.ModelName
	prefix, rest, hasPrefix := strings.Cut(m.Config.ModelName, "/")
	if m.provider == "" {
		if _, known := providers[prefix]; hasPrefix && known {
			m.provider = prefix
		} else if strings.Contains(strings.ToLower(m.Config.ModelName), "claude") {
			m.provider = "anthropic"
		} else if strings.HasPrefix(m.Config.ModelName, "gemini") {
			m.provider = "gemini"
		} else {
			m.provider = "openai"
		}
	}
	if hasPrefix && prefix == m.provider {
		m.apiModelName = rest
	}
	kwargString := func(key string) string { s, _ := m.Config.ModelKwargs[key].(string); return s }
	m.apiBase = firstNonEmpty(m.Config.APIBase, kwargString("api_base"), kwargString("base_url"),
		os.Getenv(strings.ToUpper(m.provider)+"_API_BASE"), providers[m.provider].apiBase)
	m.apiKey = kwargString("api_key")
	if keyEnv := firstNonEmpty(m.Config.APIKeyEnv, providers[m.provider].apiKeyEnv); m.apiKey == "" && keyEnv != "" {
		m.apiKey = os.Getenv(keyEnv)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// APIError is returned for unsuccessful API responses.
type APIError struct {
	StatusCode int
	Body       string
	URL        string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("API request to %s failed with status %d: %s", e.URL, e.StatusCode, e.Body)
	if e.StatusCode == http.StatusUnauthorized {
		msg += " You can permanently set your API key with `mini-go-extra config set KEY VALUE`."
	}
	return msg
}

// Retryable reports whether the request should be retried (rate limits, timeouts, server errors).
func (e *APIError) Retryable() bool {
	return e.StatusCode == 408 || e.StatusCode == 409 || e.StatusCode == 429 || e.StatusCode >= 500
}

func (m *OpenAIModel) complete(ctx context.Context, messages []mswea.Message, tools []any) (map[string]any, error) {
	body := map[string]any{}
	for k, v := range m.Config.ModelKwargs {
		if !droppedKwargs[k] {
			body[k] = v
		}
	}
	body["model"], body["messages"] = m.apiModelName, messages
	if tools != nil {
		body["tools"] = tools
	}
	if _, ok := body["usage"]; !ok && m.provider == "openrouter" {
		body["usage"] = map[string]any{"include": true}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(m.apiBase, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	if headers, ok := m.Config.ModelKwargs["extra_headers"].(map[string]any); ok {
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody), URL: url}
	}
	var response map[string]any
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, &APIError{StatusCode: 502, Body: "invalid JSON response: " + string(respBody), URL: url}
	}
	if choices, _ := response["choices"].([]any); len(choices) == 0 {
		return nil, &APIError{StatusCode: 502, Body: "response without choices: " + string(respBody), URL: url}
	}
	return response, nil
}

var retryMinWait, retryMaxWait = 4 * time.Second, 60 * time.Second

// completeWithRetry retries failed requests with exponential backoff (like tenacity in the Python version).
// The number of attempts can be set with MSWEA_MODEL_RETRY_STOP_AFTER_ATTEMPT (default 10).
func (m *OpenAIModel) completeWithRetry(ctx context.Context, messages []mswea.Message, tools []any) (map[string]any, error) {
	attempts, err := strconv.Atoi(os.Getenv("MSWEA_MODEL_RETRY_STOP_AFTER_ATTEMPT"))
	if err != nil {
		attempts = 10
	}
	for attempt := 1; ; attempt++ {
		response, err := m.complete(ctx, messages, tools)
		var apiErr *APIError
		if err == nil || attempt >= attempts || ctx.Err() != nil || (errors.As(err, &apiErr) && !apiErr.Retryable()) {
			return response, err
		}
		wait := min(max(time.Duration(1<<min(attempt-1, 16))*time.Second, retryMinWait), retryMaxWait)
		slog.Warn("Retrying model query", "attempt", attempt, "wait", wait, "error", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (m *OpenAIModel) prepareMessages(messages []mswea.Message) ([]mswea.Message, error) {
	prepared := make([]mswea.Message, len(messages))
	for i, msg := range messages {
		prepared[i] = mswea.Message{}
		for _, k := range apiMessageKeys {
			if v, ok := msg[k]; ok {
				prepared[i][k] = v
			}
		}
	}
	return SetCacheControl(prepared, m.Config.SetCacheControl)
}

func (m *OpenAIModel) query(ctx context.Context, messages []mswea.Message, tools []any, parse func(choice map[string]any) ([]mswea.Action, error)) (mswea.Message, error) {
	prepared, err := m.prepareMessages(messages)
	if err != nil {
		return nil, err
	}
	response, err := m.completeWithRetry(ctx, prepared, tools)
	if err != nil {
		return nil, err
	}
	cost, err := m.calculateCost(response)
	if err != nil {
		return nil, err
	}
	if err := GlobalStats.Add(cost); err != nil {
		return nil, err
	}
	choice, _ := response["choices"].([]any)[0].(map[string]any)
	actions, err := parse(choice)
	if interrupt, ok := mswea.AsInterrupt(err); ok {
		// The response and its cost must be persisted on format errors.
		extra := interrupt.Messages[0].Extra()
		extra["cost"], extra["response"] = cost, response
	}
	if err != nil {
		return nil, err
	}
	message, _ := choice["message"].(map[string]any)
	result := mswea.Message(maps.Clone(message))
	result["extra"] = map[string]any{"actions": actions, "response": response, "cost": cost, "timestamp": now()}
	return result, nil
}

func (m *OpenAIModel) Query(ctx context.Context, messages []mswea.Message) (mswea.Message, error) {
	return m.query(ctx, messages, []any{BashTool}, func(choice map[string]any) ([]mswea.Action, error) {
		message, _ := choice["message"].(map[string]any)
		toolCalls, _ := message["tool_calls"].([]any)
		return ParseToolcallActions(toolCalls, m.Config.FormatErrorTemplate, map[string]any{"finish_reason": choice["finish_reason"]})
	})
}

func (m *OpenAIModel) FormatMessage(msg mswea.Message) mswea.Message {
	expanded, err := ExpandMultimodalContent(msg, m.Config.MultimodalRegex)
	if err != nil {
		slog.Error("Invalid multimodal_regex", "error", err)
		return msg
	}
	return expanded.(mswea.Message)
}

func (m *OpenAIModel) FormatObservationMessages(message mswea.Message, outputs []mswea.Output, templateVars map[string]any) ([]mswea.Message, error) {
	return FormatToolcallObservationMessages(message.Actions(), outputs, m.Config.ObservationTemplate, templateVars, m.Config.MultimodalRegex)
}

func (m *OpenAIModel) TemplateVars() map[string]any { return utils.ToMap(m.Config) }

func (m *OpenAIModel) Serialize() map[string]any { return serializeModel(m, m.Config) }

func serializeModel(model any, config OpenAIModelConfig) map[string]any {
	configMap := utils.ToMap(config)
	if _, ok := config.ModelKwargs["api_key"]; ok {
		kwargs := maps.Clone(config.ModelKwargs)
		kwargs["api_key"] = "***"
		configMap["model_kwargs"] = kwargs
	}
	return map[string]any{"info": map[string]any{"config": map[string]any{"model": configMap, "model_type": utils.TypeName(model)}}}
}

func (m *TextbasedModel) Query(ctx context.Context, messages []mswea.Message) (mswea.Message, error) {
	return m.query(ctx, messages, nil, func(choice map[string]any) ([]mswea.Action, error) {
		message, _ := choice["message"].(map[string]any)
		content, _ := message["content"].(string)
		return ParseRegexActions(content, m.Config.ActionRegex, m.Config.FormatErrorTemplate, map[string]any{"finish_reason": choice["finish_reason"]})
	})
}

func (m *TextbasedModel) FormatObservationMessages(message mswea.Message, outputs []mswea.Output, templateVars map[string]any) ([]mswea.Message, error) {
	return FormatObservationMessages(outputs, m.Config.ObservationTemplate, templateVars, m.Config.MultimodalRegex)
}

func (m *TextbasedModel) Serialize() map[string]any { return serializeModel(m, m.Config) }

var _ mswea.Model = (*OpenAIModel)(nil)
var _ mswea.Model = (*TextbasedModel)(nil)
