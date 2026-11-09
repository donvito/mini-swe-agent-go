package models

import (
	"encoding/json"
	"fmt"
	"strings"

	mswea "github.com/donvito/mini-swe-agent-go"
)

func formatToolCall(arguments string) string {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) == nil {
		if command, ok := args["command"]; ok {
			return fmt.Sprintf("```\n%v\n```", command)
		}
	}
	return "```\n" + arguments + "\n```"
}

// formatObservation formats an observation JSON as key-value pairs.
func formatObservation(content string) string {
	var data map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &data) != nil || data["returncode"] == nil {
		return content
	}
	// Preserve the original key order.
	dec := json.NewDecoder(strings.NewReader(content))
	_, _ = dec.Token()
	var lines []string
	for dec.More() {
		key, _ := dec.Token()
		var value any
		_ = dec.Decode(&value)
		lines = append(lines, fmt.Sprintf("<%v>", key), fmt.Sprint(value))
	}
	return strings.Join(lines, "\n")
}

// GetContentString extracts the text content from a message for display, including tool calls.
func GetContentString(msg mswea.Message) string {
	var texts []string
	switch content := msg["content"].(type) {
	case string:
		texts = append(texts, formatObservation(content))
	case []any:
		for _, raw := range content {
			item, _ := raw.(map[string]any)
			switch item["type"] {
			case "tool_use":
				b, _ := json.Marshal(item["input"])
				texts = append(texts, formatToolCall(string(b)))
			case "tool_result":
				if rc, ok := item["content"].(string); ok {
					texts = append(texts, formatObservation(rc))
				}
			default:
				if text, ok := item["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
	}
	toolCalls, _ := msg["tool_calls"].([]any)
	for _, raw := range toolCalls {
		tc, _ := raw.(map[string]any)
		if function, ok := tc["function"].(map[string]any); ok {
			arguments, ok := function["arguments"].(string)
			if !ok {
				arguments = "{}"
			}
			texts = append(texts, formatToolCall(arguments))
		}
	}
	var nonEmpty []string
	for _, t := range texts {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}
