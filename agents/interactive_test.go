package agents_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/agents"
	"github.com/donvito/mini-swe-agent-go/environments"
	"github.com/donvito/mini-swe-agent-go/internal/console"
)

func newInteractive(t *testing.T, model mswea.Model, input string, overrides map[string]any, configFile string) (*agents.InteractiveAgent, *bytes.Buffer) {
	t.Helper()
	env, _ := environments.NewLocalEnvironment(nil)
	agent, err := agents.NewInteractiveAgent(model, env, agentConfig(t, configFile, overrides))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	agent.Input, agent.Output = console.NewLineReader(strings.NewReader(input)), &out
	return agent, &out
}

func userMessages(agent *agents.InteractiveAgent) []string {
	var result []string
	for _, msg := range agent.Messages[2:] {
		if msg.Role() == "user" && !strings.Contains(text(msg), "returncode") {
			result = append(result, text(msg))
		}
	}
	return result
}

func TestInteractiveAgent(t *testing.T) {
	for _, f := range modelFactories {
		t.Run(f.name, func(t *testing.T) {
			t.Run("confirmation then submit", func(t *testing.T) {
				agent, out := newInteractive(t, f.make(t, []step{
					{"echo", []string{"echo hi"}},
					{"done", []string{fmt.Sprintf(submitCmd, "ok")}},
				}), "\n\n\n", nil, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" || info["submission"] != "ok\n" {
					t.Fatalf("got %v %v", info, err)
				}
				if strings.Count(out.String(), "Execute 1 action(s)?") != 2 || !strings.Contains(out.String(), "Agent wants to finish.") {
					t.Errorf("unexpected output:\n%s", out.String())
				}
			})

			t.Run("rejection and recovery", func(t *testing.T) {
				agent, _ := newInteractive(t, f.make(t, []step{
					{"dangerous", []string{"rm -rf /tmp/definitely-not-here"}},
					{"done", []string{fmt.Sprintf(submitCmd, "ok")}},
				}), "no way\n\n", map[string]any{"confirm_exit": false}, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" {
					t.Fatalf("got %v %v", info, err)
				}
				if msgs := userMessages(agent); len(msgs) != 1 || !strings.Contains(msgs[0], "rejected your commands with the following message: no way") {
					t.Fatalf("user messages: %q", msgs)
				}
			})

			t.Run("yolo switch, help and already-in-mode", func(t *testing.T) {
				agent, out := newInteractive(t, f.make(t, []step{
					{"1", []string{"echo 1"}},
					{"2", []string{"echo 2"}},
					{"done", []string{fmt.Sprintf(submitCmd, "ok")}},
				}), "/h\n/c\n/y\n\n", map[string]any{"cost_limit": 10}, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" || agent.Config.Mode != "yolo" {
					t.Fatalf("got %v %v (mode %s)", info, err, agent.Config.Mode)
				}
				for _, want := range []string{"Current mode: confirm", "Already in confirm mode.", "Switched to yolo mode."} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("output missing %q", want)
					}
				}
				if strings.Count(out.String(), "Execute 1 action(s)?") != 3 {
					t.Errorf("expected only the first action to be confirmed (3 prompts incl. re-prompts):\n%s", out.String())
				}
			})

			t.Run("whitelisted actions skip confirmation", func(t *testing.T) {
				agent, out := newInteractive(t, f.make(t, []step{
					{"ls", []string{"ls -la /"}},
					{"done", []string{fmt.Sprintf(submitCmd, "ok")}},
				}), "\n", map[string]any{"whitelist_actions": []any{`ls\b`, `echo 'COMPLETE`}}, f.config)
				if info, err := agent.Run(context.Background(), "task", nil); err != nil || info["exit_status"] != "Submitted" {
					t.Fatalf("got %v %v", info, err)
				}
				if strings.Contains(out.String(), "Execute") {
					t.Errorf("whitelisted action asked for confirmation:\n%s", out.String())
				}
			})

			t.Run("human mode", func(t *testing.T) {
				agent, _ := newInteractive(t, f.make(t, []step{{"done", []string{fmt.Sprintf(submitCmd, "from lm")}}}),
					"echo human-was-here\n/y\n\n", map[string]any{"mode": "human"}, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["exit_status"] != "Submitted" || info["submission"] != "from lm\n" || agent.NCalls != 1 {
					t.Fatalf("got %v %v (calls %d)", info, err, agent.NCalls)
				}
				found := false
				for _, msg := range agent.Messages {
					found = found || strings.Contains(text(msg), "human-was-here")
				}
				if !found {
					t.Error("output of human command not in trajectory")
				}
			})

			t.Run("new task after completion", func(t *testing.T) {
				agent, _ := newInteractive(t, f.make(t, []step{
					{"done", []string{fmt.Sprintf(submitCmd, "first")}},
					{"done again", []string{fmt.Sprintf(submitCmd, "second")}},
				}), "\nmore work please\n\n\n", nil, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["submission"] != "second\n" {
					t.Fatalf("got %v %v", info, err)
				}
				if msgs := userMessages(agent); len(msgs) != 1 || msgs[0] != "The user added a new task: more work please" {
					t.Fatalf("user messages: %q", msgs)
				}
			})

			t.Run("switch to human mode on confirmation", func(t *testing.T) {
				agent, _ := newInteractive(t, f.make(t, []step{{"x", []string{"echo x"}}}),
					"/u\necho COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT; echo manual\n", map[string]any{"confirm_exit": false}, f.config)
				info, err := agent.Run(context.Background(), "task", nil)
				if err != nil || info["submission"] != "manual\n" || agent.Config.Mode != "human" {
					t.Fatalf("got %v %v (mode %s)", info, err, agent.Config.Mode)
				}
			})

			t.Run("limits exceeded without terminal stops cleanly", func(t *testing.T) {
				agent, _ := newInteractive(t, f.make(t, []step{{"1", []string{"echo 1"}}, {"2", []string{"echo 2"}}}), "",
					map[string]any{"mode": "yolo", "step_limit": 1}, f.config)
				if info, err := agent.Run(context.Background(), "task", nil); err != nil || info["exit_status"] != "LimitsExceeded" {
					t.Fatalf("got %v %v", info, err)
				}
			})
		})
	}
}
