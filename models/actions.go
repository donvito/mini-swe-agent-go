package models

import (
	"encoding/json"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"

	mswea "github.com/donvito/mini-swe-agent-go"
	"github.com/donvito/mini-swe-agent-go/utils"
)

// BashTool is the only tool offered to the LM in toolcall mode.
var BashTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "bash",
		"description": "Execute a bash command",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The bash command to execute"},
			},
			"required": []any{"command"},
		},
	},
}

const defaultObservationTemplate = "{% if output.exception_info %}<exception>{{output.exception_info}}</exception>\n{% endif %}" +
	"<returncode>{{output.returncode}}</returncode>\n<output>\n{{output.output}}</output>"

func formatError(template string, vars map[string]any, extra map[string]any) error {
	content, err := utils.Render(template, vars)
	if err != nil {
		return err
	}
	extra["interrupt_type"] = string(mswea.FormatError)
	return mswea.NewInterrupt(mswea.FormatError, mswea.Message{"role": "user", "content": content, "extra": extra})
}

// ParseToolcallActions parses OpenAI-style tool calls. Returns a FormatError interrupt for missing or
// invalid tool calls. templateVars are extra variables exposed to formatErrorTemplate (e.g., finish_reason).
func ParseToolcallActions(toolCalls []any, formatErrorTemplate string, templateVars map[string]any) ([]mswea.Action, error) {
	vars := maps.Clone(templateVars)
	if vars == nil {
		vars = map[string]any{}
	}
	vars["actions"] = []any{}
	if len(toolCalls) == 0 {
		vars["error"] = "No tool calls found in the response. Every response MUST include at least one tool call."
		vars["has_tool_calls"] = false
		return nil, formatError(formatErrorTemplate, vars, map[string]any{})
	}
	var actions []mswea.Action
	for _, raw := range toolCalls {
		toolCall, _ := raw.(map[string]any)
		function, _ := toolCall["function"].(map[string]any)
		name, _ := function["name"].(string)
		arguments, _ := function["arguments"].(string)
		id, _ := toolCall["id"].(string)
		errorMsg := ""
		var args map[string]any
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			errorMsg = "Error parsing tool call arguments: " + err.Error() + "."
		}
		if name != "bash" {
			errorMsg += "Unknown tool '" + name + "'."
		}
		command, ok := args["command"].(string)
		if !ok {
			errorMsg += "Missing 'command' argument in bash tool call."
		}
		if errorMsg != "" {
			vars["error"] = strings.TrimSpace(errorMsg)
			vars["has_tool_calls"] = true
			return nil, formatError(formatErrorTemplate, vars, map[string]any{})
		}
		actions = append(actions, mswea.Action{Command: command, ToolCallID: id})
	}
	return actions, nil
}

// ParseRegexActions parses exactly one action from text content using a regex (with "dot matches newline").
func ParseRegexActions(content, actionRegex, formatErrorTemplate string, templateVars map[string]any) ([]mswea.Action, error) {
	re, err := regexp.Compile("(?s)" + actionRegex)
	if err != nil {
		return nil, err
	}
	var found []any
	for _, match := range re.FindAllStringSubmatch(content, -1) {
		found = append(found, strings.TrimSpace(match[min(1, len(match)-1)]))
	}
	if len(found) != 1 {
		vars := maps.Clone(templateVars)
		if vars == nil {
			vars = map[string]any{}
		}
		vars["actions"] = append([]any{}, found...)
		vars["error"] = "Expected exactly 1 action, found " + strconv.Itoa(len(found)) + "."
		return nil, formatError(formatErrorTemplate, vars, map[string]any{"n_actions": len(found), "model_response": content})
	}
	return []mswea.Action{{Command: found[0].(string)}}, nil
}

func renderObservation(output mswea.Output, observationTemplate string, templateVars map[string]any, multimodalRegex string) (mswea.Message, error) {
	vars := maps.Clone(templateVars)
	if vars == nil {
		vars = map[string]any{}
	}
	vars["output"] = output.Map()
	content, err := utils.Render(observationTemplate, vars)
	if err != nil {
		return nil, err
	}
	extra := map[string]any{
		"raw_output":     output.Output,
		"returncode":     output.Returncode,
		"timestamp":      now(),
		"exception_info": output.ExceptionInfo,
	}
	maps.Copy(extra, output.Extra)
	msg := mswea.Message{"role": "user", "content": content, "extra": extra}
	if multimodalRegex != "" {
		expanded, err := ExpandMultimodalContent(msg, multimodalRegex)
		if err != nil {
			return nil, err
		}
		msg = expanded.(mswea.Message)
	}
	return msg, nil
}

// FormatToolcallObservationMessages formats execution outputs into tool result messages
// (one per action; actions that were not executed are reported as such).
func FormatToolcallObservationMessages(actions []mswea.Action, outputs []mswea.Output, observationTemplate string, templateVars map[string]any, multimodalRegex string) ([]mswea.Message, error) {
	var results []mswea.Message
	for i, action := range actions {
		output := mswea.Output{Returncode: -1, ExceptionInfo: "action was not executed"}
		if i < len(outputs) {
			output = outputs[i]
		}
		msg, err := renderObservation(output, observationTemplate, templateVars, multimodalRegex)
		if err != nil {
			return nil, err
		}
		if action.ToolCallID != "" {
			msg["role"], msg["tool_call_id"] = "tool", action.ToolCallID
		}
		results = append(results, msg)
	}
	return results, nil
}

// FormatObservationMessages formats execution outputs into user observation messages (textbased mode).
func FormatObservationMessages(outputs []mswea.Output, observationTemplate string, templateVars map[string]any, multimodalRegex string) ([]mswea.Message, error) {
	var results []mswea.Message
	for _, output := range outputs {
		msg, err := renderObservation(output, observationTemplate, templateVars, multimodalRegex)
		if err != nil {
			return nil, err
		}
		results = append(results, msg)
	}
	return results, nil
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }
