package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/internal/console"
	"github.com/donvito/mini-swe-agent-go/models"
	"github.com/donvito/mini-swe-agent-go/utils"
)

// InteractiveAgentConfig extends AgentConfig with settings for putting the user in the loop.
type InteractiveAgentConfig struct {
	AgentConfig
	// "human": commands issued by the user are executed immediately.
	// "confirm": commands issued by the LM but not whitelisted are confirmed by the user.
	// "yolo": commands issued by the LM are executed immediately without confirmation.
	Mode string `json:"mode"`
	// Never confirm actions that match these regular expressions (matched at the start of the command).
	WhitelistActions []string `json:"whitelist_actions"`
	// If the agent wants to finish, ask the user for confirmation.
	ConfirmExit bool `json:"confirm_exit"`
}

var modeCommands = map[string]string{"/u": "human", "/c": "confirm", "/y": "yolo"}

// InteractiveAgent is a small generalization of the default agent that puts the user in the loop.
type InteractiveAgent struct {
	*DefaultAgent
	Config *InteractiveAgentConfig
	Input  *console.LineReader
	Output io.Writer
	// stopSpinner stops the "waiting for the LM" spinner (it must be stopped before printing messages).
	stopSpinner func()
}

func NewInteractiveAgent(model mswea.Model, env mswea.Environment, config map[string]any) (*InteractiveAgent, error) {
	cfg := &InteractiveAgentConfig{AgentConfig: defaultAgentConfig(), Mode: "confirm", ConfirmExit: true}
	if err := utils.Decode(config, cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if _, ok := map[string]bool{"human": true, "confirm": true, "yolo": true}[cfg.Mode]; !ok {
		return nil, fmt.Errorf("invalid mode %q (must be human, confirm or yolo)", cfg.Mode)
	}
	a := &InteractiveAgent{Config: cfg, Input: console.Stdin, Output: os.Stdout}
	a.DefaultAgent = newDefaultAgent(model, env, &cfg.AgentConfig, cfg)
	a.self = a
	return a, nil
}

func (a *InteractiveAgent) interrupt(content, interruptType string) error {
	return mswea.NewInterrupt(mswea.UserInterruption, mswea.Message{"role": "user", "content": content, "extra": map[string]any{"interrupt_type": interruptType}})
}

// AddMessages prints the messages before adding them.
func (a *InteractiveAgent) AddMessages(messages ...mswea.Message) []mswea.Message {
	if a.stopSpinner != nil {
		a.stopSpinner()
	}
	for _, msg := range messages {
		role := msg.Role()
		if role == "" {
			role, _ = msg["type"].(string)
		}
		if role == "assistant" {
			fmt.Fprintf(a.Output, "\n%s\n", console.BoldRed(fmt.Sprintf("mini-swe-agent-go (step %d, %s):", a.NCalls, formatCost(a.Cost))))
		} else {
			if role == "" {
				role = "unknown"
			}
			fmt.Fprintf(a.Output, "\n%s\n", console.BoldGreen(strings.ToUpper(role[:1])+strings.ToLower(role[1:])+":"))
		}
		fmt.Fprintln(a.Output, models.GetContentString(msg))
	}
	return a.DefaultAgent.AddMessages(messages...)
}

// Query handles human mode and lets the user raise exceeded limits.
func (a *InteractiveAgent) Query(ctx context.Context) (mswea.Message, error) {
	if a.Config.Mode == "human" {
		command, err := a.promptAndHandleSlashCommands(ctx, console.BoldYellow("> "), false)
		if err != nil {
			return nil, err
		}
		if command != "/y" && command != "/c" {
			msg := mswea.Message{
				"role":    "user",
				"content": "User command: \n```bash\n" + command + "\n```",
				"extra":   map[string]any{"actions": []any{map[string]any{"command": command}}},
			}
			a.AddMessages(msg)
			return msg, nil
		}
	}
	a.stopSpinner = console.Spinner(a.Output, "Waiting for the LM to respond...")
	message, err := a.DefaultAgent.Query(ctx)
	a.stopSpinner()
	// A wall-clock limit can't be lifted by raising the step/cost limits, and without a terminal we can't prompt.
	if interrupt, ok := mswea.AsInterrupt(err); !ok || interrupt.Kind != mswea.LimitsExceeded || !console.IsTerminal(os.Stdin) || a.Input != console.Stdin {
		return message, err
	}
	fmt.Fprintf(a.Output, "Limits exceeded. Limits: %d steps, $%v.\nCurrent spend: %d steps, %s.\n", a.Config.StepLimit, a.Config.CostLimit, a.NCalls, formatCost(a.Cost))
	if a.Config.StepLimit, err = promptNumber(ctx, a, "New step limit: ", strconv.Atoi); err != nil {
		return nil, err
	}
	if a.Config.CostLimit, err = promptNumber(ctx, a, "New cost limit: ", func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }); err != nil {
		return nil, err
	}
	return a.DefaultAgent.Query(ctx)
}

// formatCost shows sub-dollar costs with more precision, so that cheap models don't always show $0.00.
func formatCost(cost float64) string {
	if cost < 1 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func promptNumber[T any](ctx context.Context, a *InteractiveAgent, prompt string, parse func(string) (T, error)) (T, error) {
	fmt.Fprint(a.Output, prompt)
	line, err := a.Input.Line(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return parse(strings.TrimSpace(line))
}

// interruptible returns a context that is cancelled when the user presses Ctrl+C.
func interruptible(ctx context.Context) (context.Context, func() bool, func()) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	var interrupted atomic.Bool
	go func() {
		select {
		case <-signals:
			interrupted.Store(true)
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, interrupted.Load, func() { signal.Stop(signals); cancel() }
}

// Step handles user interruptions (Ctrl+C).
func (a *InteractiveAgent) Step(ctx context.Context) ([]mswea.Message, error) {
	console.Rule(a.Output)
	stepCtx, interrupted, stop := interruptible(ctx)
	messages, err := a.DefaultAgent.Step(stepCtx)
	stop()
	if !interrupted() {
		return messages, err
	}
	promptCtx, _, stopPrompt := interruptible(ctx)
	defer stopPrompt()
	message, err := a.promptAndHandleSlashCommands(promptCtx, "\n\n"+console.BoldYellow("Interrupted.")+" "+
		console.Green("Type a comment/command")+" (/h for available commands)\n"+console.BoldYellow("> "), false)
	if err != nil {
		return nil, err
	}
	if message = strings.TrimSpace(message); message == "" || modeCommands[message] != "" {
		message = "Temporary interruption caught."
	}
	return nil, a.interrupt("Interrupted by user: "+message, "UserInterruption")
}

// ExecuteActions asks for confirmation and handles confirm_exit. Observations of the actions are always
// added (even for partially executed or rejected actions).
func (a *InteractiveAgent) ExecuteActions(ctx context.Context, message mswea.Message) ([]mswea.Message, error) {
	actions := message.Actions()
	var outputs []mswea.Output
	err := func() error {
		commands := make([]string, len(actions))
		for i, action := range actions {
			commands[i] = action.Command
		}
		if err := a.askConfirmationOrInterrupt(ctx, commands); err != nil {
			return err
		}
		for _, action := range actions {
			output, err := a.Env.Execute(ctx, action, "")
			if interrupt, ok := mswea.AsInterrupt(err); ok && interrupt.Kind == mswea.Submitted {
				return a.checkForNewTaskOrSubmit(ctx, interrupt)
			}
			if err != nil {
				return err
			}
			outputs = append(outputs, output)
		}
		return nil
	}()
	observations, formatErr := a.Model.FormatObservationMessages(message, outputs, a.TemplateVars(nil))
	if formatErr != nil {
		return nil, errors.Join(err, formatErr)
	}
	return a.AddMessages(observations...), err
}

func (a *InteractiveAgent) checkForNewTaskOrSubmit(ctx context.Context, submitted *mswea.Interrupt) error {
	if !a.Config.ConfirmExit {
		return submitted
	}
	input, err := a.promptAndHandleSlashCommands(ctx, console.BoldYellow("Agent wants to finish.")+" "+
		console.BoldGreen("Type new task")+" or "+console.Bold("Enter")+" to quit ("+console.Bold("/h")+" for commands)\n"+console.BoldYellow("> "), false)
	if err != nil {
		return err
	}
	switch input = strings.TrimSpace(input); {
	case input == "/u":
		return a.interrupt("Switched to human mode.", "UserInterruption")
	case modeCommands[input] != "":
		return a.checkForNewTaskOrSubmit(ctx, submitted)
	case input != "":
		return a.interrupt("The user added a new task: "+input, "UserNewTask")
	}
	return submitted
}

func (a *InteractiveAgent) shouldAskConfirmation(command string) bool {
	if a.Config.Mode != "confirm" {
		return false
	}
	for _, pattern := range a.Config.WhitelistActions {
		if matched, _ := regexp.MatchString("^(?:"+pattern+")", command); matched {
			return false
		}
	}
	return true
}

func (a *InteractiveAgent) askConfirmationOrInterrupt(ctx context.Context, commands []string) error {
	ask := false
	for _, c := range commands {
		ask = ask || a.shouldAskConfirmation(c)
	}
	if !ask {
		return nil
	}
	prompt := console.BoldYellow(fmt.Sprintf("Execute %d action(s)?", len(commands))) + " " + console.Green(console.Bold("Enter")+" to confirm") +
		", " + console.Red("type "+console.Bold("comment")+" to reject") + ", or " + console.BoldBlue("/h") + " to show available commands\n" + console.BoldYellow("> ")
	input, err := a.promptAndHandleSlashCommands(ctx, prompt, false)
	if err != nil {
		return err
	}
	switch input = strings.TrimSpace(input); input {
	case "", "/y":
		return nil
	case "/u":
		return a.interrupt("Commands not executed. Switching to human mode", "UserRejection")
	}
	return a.interrupt("Commands not executed. The user rejected your commands with the following message: "+input, "UserRejection")
}

// promptAndHandleSlashCommands prompts the user, handles /h and /m, and switches modes. Returns the user input.
func (a *InteractiveAgent) promptAndHandleSlashCommands(ctx context.Context, prompt string, multiline bool) (string, error) {
	fmt.Fprint(a.Output, prompt)
	if multiline {
		fmt.Fprintln(a.Output, console.Dim("(multiline input: finish with Ctrl-D on an empty line)"))
		return a.Input.Multiline(ctx)
	}
	input, err := a.Input.Line(ctx)
	if err != nil {
		return "", err
	}
	switch mode := modeCommands[input]; {
	case input == "/m":
		return a.promptAndHandleSlashCommands(ctx, prompt, true)
	case input == "/h":
		fmt.Fprintf(a.Output, "Current mode: %s\n"+
			"%s to switch to %s mode (execute LM commands without confirmation)\n"+
			"%s to switch to %s mode (ask for confirmation before executing LM commands)\n"+
			"%s to switch to %s mode (execute commands issued by the user)\n"+
			"%s to enter multiline comment\n",
			console.BoldGreen(a.Config.Mode), console.BoldGreen("/y"), console.BoldYellow("yolo"), console.BoldGreen("/c"),
			console.BoldYellow("confirmation"), console.BoldGreen("/u"), console.BoldYellow("human"), console.BoldGreen("/m"))
		return a.promptAndHandleSlashCommands(ctx, prompt, false)
	case mode != "" && mode == a.Config.Mode:
		return a.promptAndHandleSlashCommands(ctx, console.BoldRed("Already in "+mode+" mode.")+"\n"+prompt, false)
	case mode != "":
		a.Config.Mode = mode
		fmt.Fprintf(a.Output, "Switched to %s mode.\n", console.BoldGreen(mode))
	}
	return input, nil
}

var _ mswea.Agent = (*InteractiveAgent)(nil)
