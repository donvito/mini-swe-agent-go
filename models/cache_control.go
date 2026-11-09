package models

import (
	"fmt"

	mswea "github.com/donvito/mini-swe-agent-go"
)

// SetCacheControl adds an explicit cache control marker to the last message (mostly for Anthropic models)
// and removes all others. mode is "" (disabled) or "default_end". Returns a copy of the messages.
func SetCacheControl(messages []mswea.Message, mode string) ([]mswea.Message, error) {
	if mode == "" {
		return messages, nil
	}
	if mode != "default_end" {
		return nil, fmt.Errorf("invalid cache control mode: %q", mode)
	}
	result := make([]mswea.Message, len(messages))
	for i, msg := range messages {
		entry := deepCopy(msg).(mswea.Message)
		clearCacheControl(entry)
		if i == len(messages)-1 {
			setCacheControl(entry)
		}
		result[i] = entry
	}
	return result, nil
}

func clearCacheControl(entry mswea.Message) {
	if content, ok := entry["content"].([]any); ok {
		for _, part := range content {
			if p, ok := part.(map[string]any); ok {
				delete(p, "cache_control")
			}
		}
	}
	delete(entry, "cache_control")
}

func setCacheControl(entry mswea.Message) {
	ephemeral := map[string]any{"type": "ephemeral"}
	switch content := entry["content"].(type) {
	case nil:
		entry["cache_control"] = ephemeral
		return
	case string:
		entry["content"] = []any{map[string]any{"type": "text", "text": content, "cache_control": ephemeral}}
	case []any:
		if len(content) > 0 {
			if last, ok := content[len(content)-1].(map[string]any); ok {
				last["cache_control"] = ephemeral
			}
		}
	}
	if entry.Role() == "tool" {
		clearCacheControl(entry)
		entry["cache_control"] = ephemeral
	}
}
