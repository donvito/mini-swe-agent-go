package models

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

// MakeOutput creates an output message for DeterministicModel.
func MakeOutput(content string, actions []mswea.Action, cost float64) mswea.Message {
	return mswea.Message{
		"role":    "assistant",
		"content": content,
		"extra":   map[string]any{"actions": actionsToAny(actions), "cost": cost, "timestamp": now()},
	}
}

// MakeToolcallOutput creates an output message with OpenAI-style tool calls for DeterministicModel
// in toolcall mode. Each action gets a tool call ID if it doesn't have one.
func MakeToolcallOutput(content any, actions []mswea.Action, idPrefix string) mswea.Message {
	var toolCalls []any
	for i := range actions {
		if actions[i].ToolCallID == "" {
			actions[i].ToolCallID = fmt.Sprintf("%s_%d", idPrefix, i)
		}
		args, _ := json.Marshal(map[string]string{"command": actions[i].Command})
		toolCalls = append(toolCalls, map[string]any{
			"id": actions[i].ToolCallID, "type": "function",
			"function": map[string]any{"name": "bash", "arguments": string(args)},
		})
	}
	return mswea.Message{
		"role":       "assistant",
		"content":    content,
		"tool_calls": toolCalls,
		"extra":      map[string]any{"actions": actionsToAny(actions), "cost": 1.0, "timestamp": now()},
	}
}

func actionsToAny(actions []mswea.Action) []any {
	result := make([]any, len(actions))
	for i, a := range actions {
		m := map[string]any{"command": a.Command}
		if a.ToolCallID != "" {
			m["tool_call_id"] = a.ToolCallID
		}
		result[i] = m
	}
	return result
}

type DeterministicModelConfig struct {
	// Exact output messages to return in sequence. Each should have "role", "content" and "extra" (with "actions").
	Outputs             []mswea.Message `json:"outputs"`
	ModelName           string          `json:"model_name"`
	CostPerCall         float64         `json:"cost_per_call"`
	ObservationTemplate string          `json:"observation_template"`
	MultimodalRegex     string          `json:"multimodal_regex"`
}

// DeterministicModel returns predefined outputs (for testing). In toolcall mode, observations are formatted
// as tool result messages.
//
// Special actions: {"raise": err} returns err, "/sleep <seconds>" sleeps and "/warning <msg>" logs a warning;
// the latter two then move on to the next output.
type DeterministicModel struct {
	Config       DeterministicModelConfig
	Toolcall     bool
	currentIndex int
}

func NewDeterministicModel(config map[string]any, toolcall bool) (*DeterministicModel, error) {
	name := "deterministic"
	if toolcall {
		name = "deterministic_toolcall"
	}
	m := &DeterministicModel{
		Config:       DeterministicModelConfig{ModelName: name, CostPerCall: 1.0, ObservationTemplate: defaultObservationTemplate},
		Toolcall:     toolcall,
		currentIndex: -1,
	}
	rest := maps.Clone(config)
	delete(rest, "outputs")
	if err := utils.Decode(rest, &m.Config); err != nil {
		return nil, err
	}
	switch outputs := config["outputs"].(type) {
	case []mswea.Message:
		m.Config.Outputs = outputs
	case []any:
		for _, o := range outputs {
			msg, ok := o.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid deterministic model output: %v", o)
			}
			m.Config.Outputs = append(m.Config.Outputs, msg)
		}
	}
	return m, nil
}

func (m *DeterministicModel) Query(ctx context.Context, messages []mswea.Message) (mswea.Message, error) {
	m.currentIndex++
	if m.currentIndex >= len(m.Config.Outputs) {
		return nil, fmt.Errorf("deterministic model ran out of outputs (%d)", len(m.Config.Outputs))
	}
	output := m.Config.Outputs[m.currentIndex]
	retry, err := processTestActions(output)
	if err != nil {
		return nil, err
	}
	if retry {
		return m.Query(ctx, messages)
	}
	if err := GlobalStats.Add(m.Config.CostPerCall); err != nil {
		return nil, err
	}
	return output, nil
}

func processTestActions(output mswea.Message) (bool, error) {
	var actions []map[string]any
	switch raw := output.Extra()["actions"].(type) {
	case []map[string]any:
		actions = raw
	case []any:
		for _, a := range raw {
			if action, ok := a.(map[string]any); ok {
				actions = append(actions, action)
			}
		}
	}
	for _, action := range actions {
		if err, ok := action["raise"].(error); ok {
			return false, err
		}
		command, _ := action["command"].(string)
		if seconds, ok := strings.CutPrefix(command, "/sleep "); ok {
			s, _ := strconv.ParseFloat(seconds, 64)
			time.Sleep(time.Duration(s * float64(time.Second)))
			return true, nil
		}
		if warning, ok := strings.CutPrefix(command, "/warning"); ok {
			slog.Warn(warning)
			return true, nil
		}
	}
	return false, nil
}

func (m *DeterministicModel) FormatMessage(msg mswea.Message) mswea.Message {
	expanded, err := ExpandMultimodalContent(msg, m.Config.MultimodalRegex)
	if err != nil {
		return msg
	}
	return expanded.(mswea.Message)
}

func (m *DeterministicModel) FormatObservationMessages(message mswea.Message, outputs []mswea.Output, templateVars map[string]any) ([]mswea.Message, error) {
	if m.Toolcall {
		return FormatToolcallObservationMessages(message.Actions(), outputs, m.Config.ObservationTemplate, templateVars, m.Config.MultimodalRegex)
	}
	return FormatObservationMessages(outputs, m.Config.ObservationTemplate, templateVars, m.Config.MultimodalRegex)
}

func (m *DeterministicModel) TemplateVars() map[string]any { return utils.ToMap(m.Config) }

func (m *DeterministicModel) Serialize() map[string]any {
	return map[string]any{"info": map[string]any{"config": map[string]any{"model": utils.ToMap(m.Config), "model_type": utils.TypeName(m)}}}
}

var _ mswea.Model = (*DeterministicModel)(nil)
