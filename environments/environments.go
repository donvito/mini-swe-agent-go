// Package environments contains the execution environments of mini-swe-agent-go.
package environments

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os/exec"
	"sort"
	"strings"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

var environmentMapping = map[string]func(config map[string]any) (mswea.Environment, error){
	"local":  func(c map[string]any) (mswea.Environment, error) { return NewLocalEnvironment(c) },
	"docker": func(c map[string]any) (mswea.Environment, error) { return NewDockerEnvironment(c) },
}

// GetEnvironment creates an environment from a config dict. The "environment_class" key selects the
// implementation (falling back to defaultType); all other keys are passed to the environment's config.
func GetEnvironment(config map[string]any, defaultType string) (mswea.Environment, error) {
	config = maps.Clone(config)
	spec, _ := config["environment_class"].(string)
	delete(config, "environment_class")
	if spec == "" {
		spec = defaultType
	}
	factory, ok := environmentMapping[spec]
	if !ok {
		return nil, fmt.Errorf("unknown environment type: %q (available: %v)", spec, sortedKeys(environmentMapping))
	}
	return factory(config)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func serialize(env any, config any) map[string]any {
	return map[string]any{"info": map[string]any{"config": map[string]any{
		"environment":      utils.ToMap(config),
		"environment_type": utils.TypeName(env),
	}}}
}

// runCommand runs cmd with combined stdout/stderr. On timeout, the whole process group is killed and
// the partial output is returned with exception info. On context cancellation, ctx.Err() is returned.
func runCommand(ctx context.Context, cmd *exec.Cmd, timeout int) (mswea.Output, error) {
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return errorOutput("", "StartError", err.Error()), nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return mswea.Output{Output: decode(buf.Bytes()), Returncode: cmd.ProcessState.ExitCode()}, nil
	case <-timer.C:
		killProcessGroup(cmd)
		<-done
		msg := fmt.Sprintf("Command '%s' timed out after %d seconds", cmd.Args[len(cmd.Args)-1], timeout)
		return errorOutput(decode(buf.Bytes()), "TimeoutExpired", msg), nil
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		return mswea.Output{}, ctx.Err()
	}
}

func decode(b []byte) string { return strings.ToValidUTF8(string(b), "\uFFFD") }

func errorOutput(output, exceptionType, exception string) mswea.Output {
	return mswea.Output{
		Output:        output,
		Returncode:    -1,
		ExceptionInfo: "An error occurred while executing the command: " + exception,
		Extra:         map[string]any{"exception_type": exceptionType, "exception": exception},
	}
}

// checkFinished returns a Submitted interrupt if the output indicates task completion.
func checkFinished(output mswea.Output) error {
	lines := strings.SplitAfter(strings.TrimLeft(output.Output, " \t\n\r\v\f"), "\n")
	if strings.TrimSpace(lines[0]) == "COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT" && output.Returncode == 0 {
		return mswea.NewInterrupt(mswea.Submitted, mswea.ExitMessage(string(mswea.Submitted), strings.Join(lines[1:], "")))
	}
	return nil
}
