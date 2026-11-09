package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/agents"
	"github.com/donvito/mini-swe-agent-go/config"
	"github.com/donvito/mini-swe-agent-go/environments"
	"github.com/donvito/mini-swe-agent-go/models"
	"github.com/donvito/mini-swe-agent-go/utils"
)

type step struct {
	content  string
	commands []string
}

func agentConfig(t *testing.T, file string, overrides map[string]any) map[string]any {
	t.Helper()
	cfg, err := config.GetConfigFromSpec(file)
	if err != nil {
		t.Fatal(err)
	}
	return utils.RecursiveMerge(cfg["agent"].(map[string]any), overrides)
}

func makeTextModel(t *testing.T, steps []step) *models.DeterministicModel {
	var outputs []mswea.Message
	for _, s := range steps {
		var actions []mswea.Action
		for _, c := range s.commands {
			actions = append(actions, mswea.Action{Command: c})
		}
		outputs = append(outputs, models.MakeOutput(s.content, actions, 1.0))
	}
	m, err := models.NewDeterministicModel(map[string]any{"outputs": outputs}, false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func makeToolcallModel(t *testing.T, steps []step) *models.DeterministicModel {
	var outputs []mswea.Message
	for i, s := range steps {
		var actions []mswea.Action
		for _, c := range s.commands {
			actions = append(actions, mswea.Action{Command: c})
		}
		outputs = append(outputs, models.MakeToolcallOutput(s.content, actions, fmt.Sprintf("call_%d", i)))
	}
	m, err := models.NewDeterministicModel(map[string]any{"outputs": outputs}, true)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// modelFactories runs each test with a textbased and a toolcall model (and the matching config).
var modelFactories = []struct {
	name   string
	make   func(*testing.T, []step) *models.DeterministicModel
	config string
}{
	{"text", makeTextModel, "default.yaml"},
	{"toolcall", makeToolcallModel, "mini.yaml"},
}

func newAgent(t *testing.T, model mswea.Model, envConfig map[string]any, cfg map[string]any) *agents.DefaultAgent {
	t.Helper()
	env, err := environments.NewLocalEnvironment(envConfig)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agents.NewDefaultAgent(model, env, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func text(msg mswea.Message) string {
	switch c := msg["content"].(type) {
	case string:
		return c
	case []any:
		if len(c) > 0 {
			s, _ := c[0].(map[string]any)["text"].(string)
			return s
		}
	}
	return ""
}

const submitCmd = "echo 'COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT'\necho '%s'"

func TestDefaultAgent(t *testing.T) {
	for _, f := range modelFactories {
		t.Run(f.name, func(t *testing.T) {
			t.Run("successful completion", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{
					{"I'll echo a message", []string{"echo 'hello world'"}},
					{"Now finishing", []string{fmt.Sprintf(submitCmd, "Task completed successfully")}},
				}), nil, agentConfig(t, f.config, nil))
				info, err := agent.Run(context.Background(), "Echo hello world then finish", nil)
				if err != nil || info["exit_status"] != "Submitted" || info["submission"] != "Task completed successfully\n" || agent.NCalls != 2 {
					t.Fatalf("got %v %v (calls %d)", info, err, agent.NCalls)
				}
				if !strings.Contains(text(agent.Messages[1]), "Echo hello world then finish") {
					t.Errorf("task not rendered into instance template: %q", text(agent.Messages[1]))
				}
				if !strings.Contains(text(agent.Messages[3]), "hello world") {
					t.Errorf("observation missing output: %q", text(agent.Messages[3]))
				}
			})

			t.Run("step limit", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{{"1", []string{"echo 1"}}, {"2", []string{"echo 2"}}}), nil,
					agentConfig(t, f.config, map[string]any{"step_limit": 1}))
				info, _ := agent.Run(context.Background(), "task", nil)
				if info["exit_status"] != "LimitsExceeded" || agent.NCalls != 1 {
					t.Fatalf("got %v (calls %d)", info, agent.NCalls)
				}
			})

			t.Run("cost limit", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{{"1", []string{"echo 1"}}}), nil,
					agentConfig(t, f.config, map[string]any{"cost_limit": 0.5}))
				if info, _ := agent.Run(context.Background(), "task", nil); info["exit_status"] != "LimitsExceeded" {
					t.Fatalf("got %v", info)
				}
			})

			t.Run("timeout captures partial output", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{
					{"Output then sleep", []string{"echo $((111*9)); sleep 10"}},
					{"Quick finish", []string{fmt.Sprintf(submitCmd, "recovered")}},
				}), map[string]any{"timeout": 1}, agentConfig(t, f.config, nil))
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" || info["submission"] != "recovered\n" {
					t.Fatalf("got %v %v", info, err)
				}
				var timedOut []string
				for _, msg := range agent.Messages {
					if strings.Contains(text(msg), "timed out") {
						timedOut = append(timedOut, text(msg))
					}
				}
				if len(timedOut) != 1 || !strings.Contains(timedOut[0], "999") {
					t.Fatalf("timed out observations: %q", timedOut)
				}
			})

			t.Run("wall time limit", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{{"slow", []string{"sleep 1.1"}}, {"unreachable", []string{"echo no"}}}), nil,
					agentConfig(t, f.config, map[string]any{"wall_time_limit_seconds": 1}))
				info, _ := agent.Run(context.Background(), "task", nil)
				if info["exit_status"] != "TimeExceeded" || agent.NCalls != 1 {
					t.Fatalf("got %v (calls %d)", info, agent.NCalls)
				}
			})

			t.Run("empty actions and multiple actions", func(t *testing.T) {
				agent := newAgent(t, f.make(t, []step{
					{"No actions here", nil},
					{"Two at once", []string{"echo first", "echo second"}},
					{"done", []string{fmt.Sprintf(submitCmd, "done")}},
				}), nil, agentConfig(t, f.config, map[string]any{"cost_limit": 5}))
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" || agent.NCalls != 3 {
					t.Fatalf("got %v %v (calls %d)", info, err, agent.NCalls)
				}
				var observations []string
				for _, msg := range agent.Messages {
					if strings.Contains(text(msg), "returncode") {
						observations = append(observations, text(msg))
					}
				}
				if len(observations) != 2 || !strings.Contains(observations[0], "first") || !strings.Contains(observations[1], "second") {
					t.Fatalf("observations: %q", observations)
				}
			})

			t.Run("template vars and trajectory", func(t *testing.T) {
				output := filepath.Join(t.TempDir(), "sub", "traj.json")
				agent := newAgent(t, f.make(t, []step{{"done", []string{fmt.Sprintf(submitCmd, "ok")}}}), nil,
					agentConfig(t, f.config, map[string]any{"output_path": output, "wall_time_limit_seconds": 3600,
						"instance_template": "{{task}} calls={{n_model_calls}} limit={{wall_time_limit_seconds}} cost={{model_cost}}"}))
				if _, err := agent.Run(context.Background(), "T", nil); err != nil {
					t.Fatal(err)
				}
				if got := text(agent.Messages[1]); got != "T calls=0 limit=3600 cost=0.0" {
					t.Errorf("instance message = %q", got)
				}
				var traj map[string]any
				content, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(content, &traj); err != nil {
					t.Fatal(err)
				}
				info := traj["info"].(map[string]any)
				cfg := info["config"].(map[string]any)
				if info["exit_status"] != "Submitted" || info["submission"] != "ok\n" || traj["trajectory_format"] != "mini-swe-agent-1.1" ||
					cfg["agent_type"] != "agents.DefaultAgent" || cfg["environment_type"] != "environments.LocalEnvironment" ||
					cfg["model_type"] != "models.DeterministicModel" || len(traj["messages"].([]any)) != len(agent.Messages) {
					t.Fatalf("unexpected trajectory info: %v", info)
				}
				for _, msg := range agent.Messages[2:] {
					if _, ok := msg.Extra()["timestamp"]; !ok && msg.Role() != "exit" {
						t.Errorf("message without timestamp: %v", msg)
					}
				}
			})
		})
	}
}

// flakyModel returns a (billed) format error for every nil output.
type flakyModel struct {
	*models.DeterministicModel
	outputs []mswea.Message
	index   int
}

func (m *flakyModel) Query(ctx context.Context, messages []mswea.Message) (mswea.Message, error) {
	m.index++
	if output := m.outputs[m.index-1]; output != nil {
		return output, nil
	}
	return nil, mswea.NewInterrupt(mswea.FormatError, mswea.Message{
		"role": "user", "content": "No tool calls found in the response.", "extra": map[string]any{"interrupt_type": "FormatError", "cost": 1.0},
	})
}

func newFlakyModel(t *testing.T, outputs ...mswea.Message) *flakyModel {
	m, _ := models.NewDeterministicModel(nil, true)
	return &flakyModel{DeterministicModel: m, outputs: outputs}
}

func TestFormatErrors(t *testing.T) {
	t.Run("repeated format errors terminate cleanly", func(t *testing.T) {
		agent := newAgent(t, newFlakyModel(t, nil, nil, nil, nil, nil), nil,
			agentConfig(t, "mini.yaml", map[string]any{"max_consecutive_format_errors": 2, "cost_limit": 0}))
		info, err := agent.Run(context.Background(), "task", nil)
		if err != nil || info["exit_status"] != "RepeatedFormatError" || agent.NCalls != 2 {
			t.Fatalf("got %v %v (calls %d)", info, err, agent.NCalls)
		}
	})
	t.Run("counter resets on success", func(t *testing.T) {
		good := models.MakeToolcallOutput("listing", []mswea.Action{{Command: "echo hello"}}, "a")
		submit := models.MakeToolcallOutput("done", []mswea.Action{{Command: fmt.Sprintf(submitCmd, "ok")}}, "b")
		agent := newAgent(t, newFlakyModel(t, nil, good, nil, submit), nil,
			agentConfig(t, "mini.yaml", map[string]any{"max_consecutive_format_errors": 2, "cost_limit": 0}))
		info, err := agent.Run(context.Background(), "task", nil)
		if err != nil || info["exit_status"] != "Submitted" || info["submission"] != "ok\n" {
			t.Fatalf("got %v %v", info, err)
		}
	})
	t.Run("format errors count against cost limit", func(t *testing.T) {
		agent := newAgent(t, newFlakyModel(t, nil, nil, nil, nil, nil, nil, nil, nil), nil,
			agentConfig(t, "mini.yaml", map[string]any{"cost_limit": 2.5, "step_limit": 8, "max_consecutive_format_errors": 0}))
		info, _ := agent.Run(context.Background(), "task", nil)
		if info["exit_status"] != "LimitsExceeded" || agent.NCalls != 3 || agent.Cost != 3.0 {
			t.Fatalf("got %v (calls %d, cost %v)", info, agent.NCalls, agent.Cost)
		}
	})
}

func TestUncaughtErrorIsSavedAndReturned(t *testing.T) {
	boom := errors.New("boom")
	m, _ := models.NewDeterministicModel(map[string]any{"outputs": []mswea.Message{
		{"role": "assistant", "content": "", "extra": map[string]any{"actions": []any{map[string]any{"raise": boom}}}},
	}}, false)
	output := filepath.Join(t.TempDir(), "traj.json")
	agent := newAgent(t, m, nil, agentConfig(t, "default.yaml", map[string]any{"output_path": output}))
	info, err := agent.Run(context.Background(), "task", nil)
	if !errors.Is(err, boom) || info["exit_status"] != "Error" || info["exception_str"] != "boom" {
		t.Fatalf("got %v %v", info, err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("trajectory not saved: %v", err)
	}
}

func TestGetAgent(t *testing.T) {
	m := makeTextModel(t, nil)
	env, _ := environments.NewLocalEnvironment(nil)
	cfg := agentConfig(t, "default.yaml", nil)
	if a, err := agents.GetAgent(m, env, cfg, "interactive"); err != nil || utils.TypeName(a) != "agents.InteractiveAgent" {
		t.Errorf("got %T %v", a, err)
	}
	if a, err := agents.GetAgent(m, env, utils.RecursiveMerge(cfg, map[string]any{"agent_class": "default"}), "interactive"); err != nil || utils.TypeName(a) != "agents.DefaultAgent" {
		t.Errorf("got %T %v", a, err)
	}
	if _, err := agents.GetAgent(m, env, cfg, "nope"); err == nil {
		t.Error("expected error for unknown agent")
	}
	if _, err := agents.GetAgent(m, env, map[string]any{}, "default"); err == nil {
		t.Error("expected error for missing templates")
	}
}
