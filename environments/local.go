package environments

import (
	"context"
	"os"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

type LocalEnvironmentConfig struct {
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
	Timeout int               `json:"timeout"`
}

// LocalEnvironment executes bash commands directly on the local machine.
type LocalEnvironment struct {
	Config LocalEnvironmentConfig
}

func NewLocalEnvironment(config map[string]any) (*LocalEnvironment, error) {
	e := &LocalEnvironment{Config: LocalEnvironmentConfig{Env: map[string]string{}, Timeout: 30}}
	return e, utils.Decode(config, &e.Config)
}

func (e *LocalEnvironment) Execute(ctx context.Context, action mswea.Action, cwd string) (mswea.Output, error) {
	if cwd == "" {
		cwd = e.Config.Cwd
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cmd := shellCommand(action.Command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range e.Config.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	output, err := runCommand(ctx, cmd, e.Config.Timeout)
	if err != nil {
		return output, err
	}
	return output, checkFinished(output)
}

func (e *LocalEnvironment) TemplateVars() map[string]any {
	return utils.RecursiveMerge(utils.ToMap(e.Config), utils.Uname(), utils.EnvMap(os.Environ()))
}

func (e *LocalEnvironment) Serialize() map[string]any { return serialize(e, e.Config) }

var _ mswea.Environment = (*LocalEnvironment)(nil)
