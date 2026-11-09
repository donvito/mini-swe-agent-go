// Package agents contains the agent control flow. See https://mini-swe-agent.com/latest/advanced/control_flow/
// for a visual explanation of the loop.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

// AgentConfig holds the settings of the default agent. Check the files in the config package for examples.
type AgentConfig struct {
	// Template for the system message (the first message).
	SystemTemplate string `json:"system_template"`
	// Template for the first user message specifying the task (the second message overall).
	InstanceTemplate string `json:"instance_template"`
	// Maximum number of steps the agent can take (0 = no limit).
	StepLimit int `json:"step_limit"`
	// Stop the agent after exceeding (!) this cost (0 = no limit).
	CostLimit float64 `json:"cost_limit"`
	// Stop the agent after this many seconds of wall-clock time (0 = no limit).
	WallTimeLimitSeconds int `json:"wall_time_limit_seconds"`
	// Exit after this many format errors in a row (0 = no limit).
	MaxConsecutiveFormatErrors int `json:"max_consecutive_format_errors"`
	// Save the trajectory to this path.
	OutputPath string `json:"output_path"`
}

func defaultAgentConfig() AgentConfig {
	return AgentConfig{CostLimit: 3.0, MaxConsecutiveFormatErrors: 3}
}

func (c *AgentConfig) validate() error {
	if c.SystemTemplate == "" || c.InstanceTemplate == "" {
		return errors.New("agent config requires system_template and instance_template")
	}
	return nil
}

// stepper contains the methods that agents built on top of DefaultAgent can override
// (Go has no virtual methods, so DefaultAgent dispatches these calls through its self field).
type stepper interface {
	AddMessages(messages ...mswea.Message) []mswea.Message
	Step(ctx context.Context) ([]mswea.Message, error)
	Query(ctx context.Context) (mswea.Message, error)
	ExecuteActions(ctx context.Context, message mswea.Message) ([]mswea.Message, error)
}

type DefaultAgent struct {
	Config                   *AgentConfig
	Messages                 []mswea.Message
	Model                    mswea.Model
	Env                      mswea.Environment
	ExtraTemplateVars        map[string]any
	Cost                     float64
	NCalls                   int
	NConsecutiveFormatErrors int
	startTime                time.Time
	fullConfig               any
	self                     stepper
}

func NewDefaultAgent(model mswea.Model, env mswea.Environment, config map[string]any) (*DefaultAgent, error) {
	cfg := defaultAgentConfig()
	if err := utils.Decode(config, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	a := newDefaultAgent(model, env, &cfg, &cfg)
	a.self = a
	return a, nil
}

func newDefaultAgent(model mswea.Model, env mswea.Environment, config *AgentConfig, fullConfig any) *DefaultAgent {
	return &DefaultAgent{Config: config, Model: model, Env: env, ExtraTemplateVars: map[string]any{}, startTime: time.Now(), fullConfig: fullConfig}
}

func (a *DefaultAgent) TemplateVars(extra map[string]any) map[string]any {
	return utils.RecursiveMerge(
		utils.ToMap(a.fullConfig),
		a.Env.TemplateVars(),
		a.Model.TemplateVars(),
		map[string]any{"n_model_calls": a.NCalls, "model_cost": a.Cost, "elapsed_seconds": int(time.Since(a.startTime).Seconds())},
		a.ExtraTemplateVars,
		extra,
	)
}

func (a *DefaultAgent) renderTemplate(template string) (string, error) {
	return utils.Render(template, a.TemplateVars(nil))
}

func (a *DefaultAgent) AddMessages(messages ...mswea.Message) []mswea.Message {
	slog.Debug("Adding messages", "messages", messages)
	a.Messages = append(a.Messages, messages...)
	return messages
}

func (a *DefaultAgent) handleUncaughtError(err error) {
	a.self.AddMessages(a.Model.FormatMessage(mswea.Message{
		"role":    "exit",
		"content": err.Error(),
		"extra":   map[string]any{"exit_status": exitStatus(err), "submission": "", "exception_str": err.Error()},
	}))
}

// exitStatus returns a name for the error type, e.g. "APIError" (or "Error" for anonymous errors).
func exitStatus(err error) string {
	name := utils.TypeName(err)
	name = name[strings.LastIndex(name, ".")+1:]
	if name == "" || !unicode.IsUpper(rune(name[0])) {
		return "Error"
	}
	return name
}

// Run runs Step until the agent is finished. Returns the extra dict of the exit message
// (with exit_status and submission keys).
func (a *DefaultAgent) Run(ctx context.Context, task string, extra map[string]any) (map[string]any, error) {
	a.ExtraTemplateVars["task"] = task
	for k, v := range extra {
		a.ExtraTemplateVars[k] = v
	}
	a.Messages = nil
	system, err := a.renderTemplate(a.Config.SystemTemplate)
	if err != nil {
		return nil, err
	}
	instance, err := a.renderTemplate(a.Config.InstanceTemplate)
	if err != nil {
		return nil, err
	}
	a.self.AddMessages(
		a.Model.FormatMessage(mswea.Message{"role": "system", "content": system}),
		a.Model.FormatMessage(mswea.Message{"role": "user", "content": instance}),
	)
	for {
		_, err := a.self.Step(ctx)
		interrupt, isInterrupt := mswea.AsInterrupt(err)
		switch {
		case err == nil:
			a.NConsecutiveFormatErrors = 0
		case isInterrupt && interrupt.Kind == mswea.FormatError:
			// The call was billed before parsing failed, so the model query never got to charge it.
			cost, _ := interrupt.Messages[0].Extra()["cost"].(float64)
			a.Cost += cost
			a.NConsecutiveFormatErrors++
			a.self.AddMessages(interrupt.Messages...)
			if limit := a.Config.MaxConsecutiveFormatErrors; 0 < limit && limit <= a.NConsecutiveFormatErrors {
				a.self.AddMessages(mswea.ExitMessage("RepeatedFormatError", ""))
			}
		case isInterrupt:
			a.self.AddMessages(interrupt.Messages...)
		default:
			a.handleUncaughtError(err)
			_, _ = a.Save(a.Config.OutputPath)
			return a.lastExtra(), err
		}
		if _, err := a.Save(a.Config.OutputPath); err != nil {
			return nil, err
		}
		if a.Messages[len(a.Messages)-1].Role() == "exit" {
			return a.lastExtra(), nil
		}
	}
}

func (a *DefaultAgent) lastExtra() map[string]any {
	if len(a.Messages) == 0 {
		return map[string]any{}
	}
	return a.Messages[len(a.Messages)-1].Extra()
}

// Step queries the LM and executes the actions.
func (a *DefaultAgent) Step(ctx context.Context) ([]mswea.Message, error) {
	message, err := a.self.Query(ctx)
	if err != nil {
		return nil, err
	}
	return a.self.ExecuteActions(ctx, message)
}

// Query checks the limits, queries the model and adds the response to the messages.
func (a *DefaultAgent) Query(ctx context.Context) (mswea.Message, error) {
	if (0 < a.Config.StepLimit && a.Config.StepLimit <= a.NCalls) || (0 < a.Config.CostLimit && a.Config.CostLimit <= a.Cost) {
		return nil, mswea.NewInterrupt(mswea.LimitsExceeded, mswea.ExitMessage(string(mswea.LimitsExceeded), ""))
	}
	if limit := a.Config.WallTimeLimitSeconds; 0 < limit && limit <= int(time.Since(a.startTime).Seconds()) {
		return nil, mswea.NewInterrupt(mswea.TimeExceeded, mswea.ExitMessage(string(mswea.TimeExceeded), ""))
	}
	a.NCalls++
	message, err := a.Model.Query(ctx, a.Messages)
	if err != nil {
		return nil, err
	}
	cost, _ := message.Extra()["cost"].(float64)
	a.Cost += cost
	a.self.AddMessages(message)
	return message, nil
}

// ExecuteActions executes the actions of a model response and adds the observation messages.
func (a *DefaultAgent) ExecuteActions(ctx context.Context, message mswea.Message) ([]mswea.Message, error) {
	var outputs []mswea.Output
	for _, action := range message.Actions() {
		output, err := a.Env.Execute(ctx, action, "")
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}
	observations, err := a.Model.FormatObservationMessages(message, outputs, a.TemplateVars(nil))
	if err != nil {
		return nil, err
	}
	return a.self.AddMessages(observations...), nil
}

// Serialize returns the agent state as a JSON-compatible nested dict.
func (a *DefaultAgent) Serialize(extra ...map[string]any) map[string]any {
	lastExtra := a.lastExtra()
	exitStatus, _ := lastExtra["exit_status"].(string)
	submission, _ := lastExtra["submission"].(string)
	messages := a.Messages
	if messages == nil {
		messages = []mswea.Message{}
	}
	agentData := map[string]any{
		"info": map[string]any{
			"model_stats":  map[string]any{"instance_cost": a.Cost, "api_calls": a.NCalls},
			"config":       map[string]any{"agent": utils.ToMap(a.fullConfig), "agent_type": utils.TypeName(a.self)},
			"mini_version": mswea.Version,
			"exit_status":  exitStatus,
			"submission":   submission,
		},
		"messages":          messages,
		"trajectory_format": "mini-swe-agent-1.1",
	}
	return utils.RecursiveMerge(append([]map[string]any{agentData, a.Model.Serialize(), a.Env.Serialize()}, extra...)...)
}

// Save writes the trajectory to path (if not empty) and returns the serialized data.
// Extra dicts are recursively merged into the output data.
func (a *DefaultAgent) Save(path string, extra ...map[string]any) (map[string]any, error) {
	data := a.Serialize(extra...)
	if path == "" {
		return data, nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return data, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return data, err
	}
	return data, os.WriteFile(path, buf.Bytes(), 0o644)
}

var _ mswea.Agent = (*DefaultAgent)(nil)
