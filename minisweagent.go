// Package minisweagent provides the core types and interfaces of mini-swe-agent-go:
// the Model, Environment and Agent interfaces, the message types passed between them,
// and the location of the global config file.
//
// Implementations live in the agents, environments and models packages.
// Every use case should start with a run script (see cmd/ and examples/) that picks
// one agent, environment and model to run.
package minisweagent

import (
	"context"
	"encoding/json"
)

const Version = "0.1.0"

// Message is a single entry of the agent's trajectory. It mirrors the OpenAI chat message format
// ("role", "content", "tool_calls", ...) and carries bookkeeping data under the "extra" key, which is
// stripped before messages are sent to the LM. Nested values should be plain map[string]any.
type Message map[string]any

func (m Message) Role() string {
	role, _ := m["role"].(string)
	return role
}

// Extra returns the "extra" dict of the message (nil if not present).
func (m Message) Extra() map[string]any {
	extra, _ := m["extra"].(map[string]any)
	return extra
}

// Actions returns the actions parsed from a model response (stored under extra.actions).
func (m Message) Actions() []Action {
	var actions []Action
	if raw, ok := m.Extra()["actions"]; ok {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &actions)
	}
	return actions
}

// Action is a command that the agent wants to execute in the environment.
type Action struct {
	Command    string `json:"command"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Output is the result of executing an Action.
type Output struct {
	Output        string         `json:"output"`
	Returncode    int            `json:"returncode"`
	ExceptionInfo string         `json:"exception_info"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// Map returns the output as a dict for use in templates.
func (o Output) Map() map[string]any {
	m := map[string]any{"output": o.Output, "returncode": o.Returncode, "exception_info": o.ExceptionInfo}
	if o.Extra != nil {
		m["extra"] = o.Extra
	}
	return m
}

// Model is the interface for language models.
type Model interface {
	// Query the LM. The returned message must contain the parsed actions under extra.actions.
	// Returns an *Interrupt of kind FormatError if the response could not be parsed.
	Query(ctx context.Context, messages []Message) (Message, error)
	FormatMessage(msg Message) Message
	FormatObservationMessages(message Message, outputs []Output, templateVars map[string]any) ([]Message, error)
	TemplateVars() map[string]any
	Serialize() map[string]any
}

// Environment is the interface for execution environments.
type Environment interface {
	// Execute an action. Returns an *Interrupt of kind Submitted if the agent finished its task.
	Execute(ctx context.Context, action Action, cwd string) (Output, error)
	TemplateVars() map[string]any
	Serialize() map[string]any
}

// Agent is the interface for agents.
type Agent interface {
	Run(ctx context.Context, task string, extra map[string]any) (map[string]any, error)
	Save(path string, extra ...map[string]any) (map[string]any, error)
}
