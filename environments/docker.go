package environments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

type DockerEnvironmentConfig struct {
	Image string `json:"image"`
	// Working directory in which to execute commands.
	Cwd string `json:"cwd"`
	// Environment variables to set in the container.
	Env map[string]string `json:"env"`
	// Environment variables to forward to the container (only if set on the host).
	// In case of conflict with Env, Env takes precedence.
	ForwardEnv []string `json:"forward_env"`
	// Timeout (seconds) for executing commands in the container.
	Timeout int `json:"timeout"`
	// Path to the docker/container executable.
	Executable string `json:"executable"`
	// Additional arguments to pass to `docker run`. Default ["--rm"] removes the container after it exits.
	RunArgs []string `json:"run_args"`
	// Max duration to keep the container running. Uses the same format as the sleep command.
	ContainerTimeout string `json:"container_timeout"`
	// Timeout (seconds) for starting the container (which might include pulling the image).
	PullTimeout int `json:"pull_timeout"`
	// Interpreter used to execute commands. The command is appended as the last argument.
	Interpreter []string `json:"interpreter"`
}

// DockerEnvironment executes bash commands in a Docker container using direct docker commands.
type DockerEnvironment struct {
	Config      DockerEnvironmentConfig
	ContainerID string
}

func NewDockerEnvironment(config map[string]any) (*DockerEnvironment, error) {
	executable := os.Getenv("MSWEA_DOCKER_EXECUTABLE")
	if executable == "" {
		executable = "docker"
	}
	e := &DockerEnvironment{Config: DockerEnvironmentConfig{
		Cwd: "/", Env: map[string]string{}, Timeout: 30, Executable: executable, RunArgs: []string{"--rm"},
		ContainerTimeout: "2h", PullTimeout: 120, Interpreter: []string{"bash", "-lc"},
	}}
	if err := utils.Decode(config, &e.Config); err != nil {
		return nil, err
	}
	if e.Config.Image == "" {
		return nil, fmt.Errorf("docker environment: 'image' is required")
	}
	return e, e.startContainer()
}

func (e *DockerEnvironment) startContainer() error {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "minisweagent-" + hex.EncodeToString(suffix)
	args := append([]string{"run", "-d", "--name", name, "-w", e.Config.Cwd}, e.Config.RunArgs...)
	args = append(args, e.Config.Image, "sleep", e.Config.ContainerTimeout)
	slog.Debug("Starting container", "command", e.Config.Executable+" "+strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(e.Config.PullTimeout)*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, e.Config.Executable, args...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("starting container: %w: %s", err, exitErr.Stderr)
		}
		return fmt.Errorf("starting container: %w", err)
	}
	e.ContainerID = strings.TrimSpace(string(out))
	slog.Info("Started container", "name", name, "id", e.ContainerID)
	return nil
}

func (e *DockerEnvironment) Execute(ctx context.Context, action mswea.Action, cwd string) (mswea.Output, error) {
	if cwd == "" {
		cwd = e.Config.Cwd
	}
	args := []string{"exec", "-w", cwd}
	for _, key := range e.Config.ForwardEnv {
		if value, ok := os.LookupEnv(key); ok {
			args = append(args, "-e", key+"="+value)
		}
	}
	for key, value := range e.Config.Env {
		args = append(args, "-e", key+"="+value)
	}
	args = append(append(append(args, e.ContainerID), e.Config.Interpreter...), action.Command)
	output, err := runCommand(ctx, exec.Command(e.Config.Executable, args...), e.Config.Timeout)
	if err != nil {
		return output, err
	}
	return output, checkFinished(output)
}

func (e *DockerEnvironment) TemplateVars() map[string]any {
	return utils.RecursiveMerge(utils.ToMap(e.Config), utils.Uname())
}

func (e *DockerEnvironment) Serialize() map[string]any { return serialize(e, e.Config) }

// Close stops and removes the container.
func (e *DockerEnvironment) Close() error {
	if e.ContainerID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, e.Config.Executable, "rm", "-f", e.ContainerID).Run()
}

var _ mswea.Environment = (*DockerEnvironment)(nil)
