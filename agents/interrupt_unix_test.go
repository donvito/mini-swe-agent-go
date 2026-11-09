//go:build unix

package agents_test

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"
)

func TestInteractiveAgentCtrlCInterruptsStep(t *testing.T) {
	agent, _ := newInteractive(t, makeToolcallModel(t, []step{
		{"slow", []string{"sleep 10"}},
		{"done", []string{fmt.Sprintf(submitCmd, "ok")}},
	}), "stop that\n", map[string]any{"mode": "yolo", "confirm_exit": false}, "mini.yaml")
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	info, err := agent.Run(context.Background(), "task", nil)
	if err != nil || info["exit_status"] != "Submitted" || time.Since(start) > 5*time.Second {
		t.Fatalf("got %v %v after %v", info, err, time.Since(start))
	}
	if msgs := userMessages(agent); len(msgs) != 1 || msgs[0] != "Interrupted by user: stop that" {
		t.Fatalf("user messages: %q", msgs)
	}
}
