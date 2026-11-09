// Package cli contains helpers shared by the command line tools.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/internal/console"
)

// Startup prints the banner and loads the global config file into the environment.
func Startup() {
	if os.Getenv("MSWEA_SILENT_STARTUP") == "" {
		fmt.Printf("This is %s version %s.\nLoading global config from %s\n",
			console.BoldGreen("mini-swe-agent-go"), console.BoldGreen(mswea.Version), console.BoldGreen("'"+mswea.GlobalConfigFile()+"'"))
	}
	if err := mswea.LoadGlobalConfig(false); err != nil {
		fmt.Fprintf(os.Stderr, "Could not load global config: %v\n", err)
	}
}

func prompt(ctx context.Context, message, defaultValue string) (string, error) {
	if defaultValue != "" {
		message += console.Dim("[" + defaultValue + "] ")
	}
	fmt.Print(message)
	line, err := console.Stdin.Line(ctx)
	if line = strings.TrimSpace(line); line == "" {
		line = defaultValue
	}
	return line, err
}

const setupHelp = `To get started, we need to set up your global config file.

You can edit it manually or use the %s or %s commands.

This setup will ask you for your model and an API key.

Here's a few popular models and the required API keys:

%s (%s)
%s or %s (%s)
%s (%s)
%s (%s)

%s

%s

More information at https://mini-swe-agent.com/latest/quickstart/
To find the best model, check the leaderboard at https://swebench.com/
`

// ConfigureIfFirstTime runs the setup unless MSWEA_CONFIGURED is set (and stdin is a terminal).
func ConfigureIfFirstTime(ctx context.Context) error {
	if os.Getenv("MSWEA_CONFIGURED") != "" || !console.IsTerminal(os.Stdin) {
		return nil
	}
	console.Rule(os.Stdout)
	err := Setup(ctx)
	console.Rule(os.Stdout)
	return err
}

// Setup asks for the default model and an API key and writes them to the global config file.
func Setup(ctx context.Context) error {
	g := console.BoldGreen
	fmt.Printf(setupHelp, g("mini-go-extra config set"), g("mini-go-extra config edit"),
		g("anthropic/claude-opus-4-6-20260205"), g("ANTHROPIC_API_KEY"),
		g("openai/gpt-5.4"), g("openai/gpt-5.4-mini"), g("OPENAI_API_KEY"),
		g("gemini/gemini-3-pro-preview"), g("GEMINI_API_KEY"),
		g("openrouter/anthropic/claude-opus-4.6"), g("OPENROUTER_API_KEY"),
		console.Bold(`Note: Please always include the provider (e.g., "openai/") in the model name.`),
		console.BoldYellow("You can leave any setting blank to skip it."))
	model, err := prompt(ctx, "Enter your default model (e.g., anthropic/claude-opus-4-6-20260205): ", os.Getenv("MSWEA_MODEL_NAME"))
	if err != nil {
		return err
	}
	if model != "" {
		if err := mswea.SetGlobalConfigKey("MSWEA_MODEL_NAME", model); err != nil {
			return err
		}
	}
	fmt.Println(console.BoldYellow("If you already have your API keys set as environment variables, you can ignore the next question."))
	keyName, err := prompt(ctx, "Enter your API key name (e.g., ANTHROPIC_API_KEY): ", "")
	if err != nil {
		return err
	}
	keyValue := ""
	if keyName != "" {
		if keyValue, err = prompt(ctx, "Enter your API key value (e.g., sk-1234567890): ", ""); err != nil {
			return err
		}
		if keyValue != "" {
			if err := mswea.SetGlobalConfigKey(keyName, keyValue); err != nil {
				return err
			}
		}
	}
	if keyValue == "" {
		fmt.Println(console.BoldRed("API key setup not completed.") + " Totally fine if you have your keys as environment variables.")
	}
	if err := mswea.SetGlobalConfigKey("MSWEA_CONFIGURED", "true"); err != nil {
		return err
	}
	fmt.Printf("\n%s If you want to revisit it, run %s.\n", console.BoldYellow("Config finished."), g("mini-go-extra config setup"))
	return nil
}

func argOrPrompt(cmd *cobra.Command, args []string, i int, message string) (string, error) {
	if len(args) > i {
		return args[i], nil
	}
	return prompt(cmd.Context(), message, "")
}

// NewConfigCmd returns the `config` command to manage the global config file.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the global config file",
		Long: "Utility to manage the global config file.\n\nYou can also directly edit the .env file in the config directory.\n\n" +
			"It is located at " + mswea.GlobalConfigFile(),
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "setup",
			Short: "Setup the global config file",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return Setup(cmd.Context()) },
		},
		&cobra.Command{
			Use:   "set [KEY] [VALUE]",
			Short: "Set a key in the global config file",
			Args:  cobra.MaximumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				key, err := argOrPrompt(cmd, args, 0, "Enter the key to set: ")
				if err != nil {
					return err
				}
				value, err := argOrPrompt(cmd, args, 1, fmt.Sprintf("Enter the value for %s: ", key))
				if err != nil {
					return err
				}
				return mswea.SetGlobalConfigKey(key, value)
			},
		},
		&cobra.Command{
			Use:   "unset [KEY]",
			Short: "Unset a key in the global config file",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				key, err := argOrPrompt(cmd, args, 0, "Enter the key to unset: ")
				if err != nil {
					return err
				}
				return mswea.UnsetGlobalConfigKey(key)
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Edit the global config file",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "nano"
				}
				c := exec.Command(editor, mswea.GlobalConfigFile())
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
				return c.Run()
			},
		},
	)
	return cmd
}
