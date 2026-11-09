package environments

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
)

func TestLocalEnvironment(t *testing.T) {
	dir := t.TempDir()
	env, err := NewLocalEnvironment(map[string]any{"cwd": dir, "env": map[string]any{"MY_VAR": "hello"}, "timeout": 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := env.Execute(ctx, mswea.Action{Command: "pwd; echo $MY_VAR; echo err >&2; exit 3"}, "")
	resolved, _ := os.Readlink(dir)
	if err != nil || out.Returncode != 3 || out.ExceptionInfo != "" || !strings.Contains(out.Output, "hello\nerr\n") ||
		!(strings.Contains(out.Output, dir) || (resolved != "" && strings.Contains(out.Output, resolved))) {
		t.Fatalf("got %+v %v", out, err)
	}
	if out, _ := env.Execute(ctx, mswea.Action{Command: "pwd"}, "/"); out.Output != "/\n" {
		t.Errorf("cwd override: %q", out.Output)
	}
	if out, _ := env.Execute(ctx, mswea.Action{Command: "printf '\\xff'"}, ""); out.Output != "\uFFFD" {
		t.Errorf("invalid UTF-8 should be replaced: %q", out.Output)
	}
}

func TestLocalEnvironmentTimeoutKillsProcessGroup(t *testing.T) {
	env, _ := NewLocalEnvironment(map[string]any{"timeout": 1})
	marker := t.TempDir() + "/marker"
	start := time.Now()
	out, err := env.Execute(context.Background(), mswea.Action{Command: "echo partial; (sleep 2; touch " + marker + ") & sleep 10"}, "")
	if err != nil || out.Returncode != -1 || out.Output != "partial\n" || !strings.Contains(out.ExceptionInfo, "timed out after 1 seconds") ||
		out.Extra["exception_type"] != "TimeoutExpired" || time.Since(start) > 5*time.Second {
		t.Fatalf("got %+v %v after %v", out, err, time.Since(start))
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("background child survived the timeout")
	}
}

func TestLocalEnvironmentContextCancellation(t *testing.T) {
	env, _ := NewLocalEnvironment(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := env.Execute(ctx, mswea.Action{Command: "sleep 10"}, ""); err != context.DeadlineExceeded {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestSubmission(t *testing.T) {
	env, _ := NewLocalEnvironment(nil)
	for command, want := range map[string]string{
		"echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT; printf 'line1\\nline2\\n'": "line1\nline2\n",
		"echo; echo '  COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT  '":                "",
	} {
		_, err := env.Execute(context.Background(), mswea.Action{Command: command}, "")
		interrupt, ok := mswea.AsInterrupt(err)
		if !ok || interrupt.Kind != mswea.Submitted || interrupt.Messages[0].Extra()["submission"] != want || interrupt.Messages[0].Role() != "exit" {
			t.Errorf("%q: got %v", command, err)
		}
	}
	for _, command := range []string{"echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT; exit 1", "echo x; echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT"} {
		if _, err := env.Execute(context.Background(), mswea.Action{Command: command}, ""); err != nil {
			t.Errorf("%q should not submit: %v", command, err)
		}
	}
}

func TestGetEnvironment(t *testing.T) {
	env, err := GetEnvironment(map[string]any{"timeout": 5}, "local")
	if err != nil || env.(*LocalEnvironment).Config.Timeout != 5 {
		t.Fatalf("got %v %v", env, err)
	}
	vars := env.TemplateVars()
	if vars["system"] == "" || vars["timeout"] != 5 || vars["PATH"] == nil {
		t.Errorf("template vars missing keys: system=%v timeout=%v", vars["system"], vars["timeout"])
	}
	if _, err := GetEnvironment(map[string]any{"environment_class": "nope"}, "local"); err == nil {
		t.Error("expected error for unknown environment")
	}
	if _, err := GetEnvironment(map[string]any{"environment_class": "docker"}, "local"); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("expected missing image error, got %v", err)
	}
}
