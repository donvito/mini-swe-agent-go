// Command mini-go runs mini-swe-agent-go in your local environment.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/agents"
	"github.com/donvito/mini-swe-agent-go/config"
	"github.com/donvito/mini-swe-agent-go/environments"
	"github.com/donvito/mini-swe-agent-go/internal/cli"
	"github.com/donvito/mini-swe-agent-go/internal/console"
	"github.com/donvito/mini-swe-agent-go/models"
	"github.com/donvito/mini-swe-agent-go/utils"
)

const configSpecHelp = `Path to config files, filenames, or key-value pairs.
IMPORTANT: If you set this option, the default config file will not be used.
So you need to explicitly set it e.g., with -c mini.yaml <other options>.
Multiple configs will be recursively merged. Examples:
  -c mini.yaml -c model.model_kwargs.temperature=0.5
  -c mini.yaml -c agent.mode=yolo`

type options struct {
	modelName, modelClass, agentClass, environmentClass, task, output string
	yolo, exitImmediately                                             bool
	costLimit                                                         float64
	configSpecs                                                       []string
}

func newCommand() *cobra.Command {
	defaultConfig := os.Getenv("MSWEA_MINI_CONFIG_PATH")
	if defaultConfig == "" {
		defaultConfig = "mini.yaml"
	}
	var opts options
	cmd := &cobra.Command{
		Use:          "mini-go",
		Short:        "Run mini-swe-agent-go in your local environment.",
		Long:         "Run mini-swe-agent-go in your local environment.\n\nA Go port of mini-swe-agent (https://mini-swe-agent.com).",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), opts, cmd.Flags().Changed("cost-limit"), defaultConfig)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.modelName, "model", "m", "", "Model to use")
	f.StringVar(&opts.modelClass, "model-class", "", "Model class to use (e.g., 'openai' or 'textbased')")
	f.StringVar(&opts.agentClass, "agent-class", "", "Agent class to use (e.g., 'interactive' or 'default')")
	f.StringVar(&opts.environmentClass, "environment-class", "", "Environment class to use (e.g., 'local' or 'docker')")
	f.StringVarP(&opts.task, "task", "t", "", "Task/problem statement")
	f.BoolVarP(&opts.yolo, "yolo", "y", false, "Run without confirmation")
	f.Float64VarP(&opts.costLimit, "cost-limit", "l", 0, "Cost limit. Set to 0 to disable.")
	f.StringArrayVarP(&opts.configSpecs, "config", "c", []string{defaultConfig}, configSpecHelp)
	f.StringVarP(&opts.output, "output", "o", filepath.Join(mswea.GlobalConfigDir(), "last_mini_run.traj.json"), "Output trajectory file")
	f.BoolVar(&opts.exitImmediately, "exit-immediately", false, "Exit immediately when the agent wants to finish instead of prompting.")
	return cmd
}

// setIf sets dict[key] = value if the option was given.
func setIf(dict map[string]any, key string, value any, given bool) {
	if given {
		dict[key] = value
	}
}

func section(config map[string]any, key string) map[string]any {
	s, _ := config[key].(map[string]any)
	return s
}

func run(ctx context.Context, opts options, costLimitGiven bool, defaultConfig string) error {
	if err := cli.ConfigureIfFirstTime(ctx); err != nil {
		return err
	}
	fmt.Printf("Building agent config from specs: %s\n", console.BoldGreen(fmt.Sprint(opts.configSpecs)))
	var configs []map[string]any
	for _, spec := range opts.configSpecs {
		c, err := config.GetConfigFromSpec(spec)
		if err != nil {
			return err
		}
		configs = append(configs, c)
	}
	runOverrides, agentOverrides, modelOverrides, envOverrides := map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}
	setIf(runOverrides, "task", opts.task, opts.task != "")
	setIf(agentOverrides, "agent_class", opts.agentClass, opts.agentClass != "")
	setIf(agentOverrides, "mode", "yolo", opts.yolo)
	setIf(agentOverrides, "cost_limit", opts.costLimit, costLimitGiven)
	setIf(agentOverrides, "confirm_exit", false, opts.exitImmediately)
	setIf(agentOverrides, "output_path", opts.output, opts.output != "")
	setIf(modelOverrides, "model_class", opts.modelClass, opts.modelClass != "")
	setIf(modelOverrides, "model_name", opts.modelName, opts.modelName != "")
	setIf(envOverrides, "environment_class", opts.environmentClass, opts.environmentClass != "")
	cfg := utils.RecursiveMerge(append(configs, map[string]any{
		"run": runOverrides, "agent": agentOverrides, "model": modelOverrides, "environment": envOverrides,
	})...)
	if _, ok := section(cfg, "agent")["system_template"]; !ok {
		return fmt.Errorf("the config has no prompt templates (agent.system_template). Passing -c replaces the default "+
			"config, so put a full config first, e.g.: mini-go -c %s -c %s", defaultConfig, strings.Join(opts.configSpecs, " -c "))
	}

	task, ok := section(cfg, "run")["task"].(string)
	if !ok {
		fmt.Println(console.BoldYellow("What do you want to do?") + console.Dim(" (finish with Ctrl-D on an empty line)"))
		var err error
		if task, err = console.Stdin.Multiline(ctx); err != nil {
			return fmt.Errorf("reading task: %w", err)
		}
		fmt.Println(console.BoldGreen("Got that, thanks!"))
	}

	model, err := models.GetModel("", section(cfg, "model"))
	if err != nil {
		return err
	}
	env, err := environments.GetEnvironment(section(cfg, "environment"), "local")
	if err != nil {
		return err
	}
	if closer, ok := env.(io.Closer); ok {
		defer closer.Close()
	}
	agent, err := agents.GetAgent(model, env, section(cfg, "agent"), "interactive")
	if err != nil {
		return err
	}
	_, err = agent.Run(ctx, task, nil)
	if outputPath, _ := section(cfg, "agent")["output_path"].(string); outputPath != "" {
		fmt.Printf("Saved trajectory to %s\n", console.BoldGreen("'"+outputPath+"'"))
	}
	return err
}

func main() {
	cli.Startup()
	if err := newCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
