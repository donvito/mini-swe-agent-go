// This is the simplest possible example of how to use mini-swe-agent-go as a library.
// For a more complete example, see cmd/mini-go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/agents"
	"github.com/donvito/mini-swe-agent-go/config"
	"github.com/donvito/mini-swe-agent-go/environments"
	"github.com/donvito/mini-swe-agent-go/models"
)

func main() {
	_ = mswea.LoadGlobalConfig(false)
	task := flag.String("t", "", "Task/problem statement")
	modelName := flag.String("m", os.Getenv("MSWEA_MODEL_NAME"), "Model name (defaults to MSWEA_MODEL_NAME env var)")
	flag.Parse()
	if *task == "" || *modelName == "" {
		fmt.Fprintln(os.Stderr, "usage: hello_world -t TASK [-m MODEL]")
		os.Exit(2)
	}
	slog.SetLogLoggerLevel(slog.LevelDebug)

	content, err := config.ReadConfigFile("default.yaml")
	must(err)
	var cfg struct {
		Agent map[string]any `yaml:"agent"`
	}
	must(yaml.Unmarshal(content, &cfg))

	model, err := models.NewTextbasedModel(map[string]any{"model_name": *modelName})
	must(err)
	env, err := environments.NewLocalEnvironment(nil)
	must(err)
	agent, err := agents.NewDefaultAgent(model, env, cfg.Agent)
	must(err)
	result, err := agent.Run(context.Background(), *task, nil)
	must(err)
	fmt.Println(result)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
