package models

import (
	"fmt"
	"regexp"
	"strings"

	mswea "github.com/donvito/mini-swe-agent-go"
)

const DefaultMultimodalRegex = `(?s)<MSWEA_MULTIMODAL_CONTENT><CONTENT_TYPE>(.+?)</CONTENT_TYPE>(.+?)</MSWEA_MULTIMODAL_CONTENT>`

// ExpandMultimodalContent recursively replaces multimodal tags in message content with structured
// OpenAI-style content parts. Returns a copy; the input is not modified.
func ExpandMultimodalContent(content any, pattern string) (any, error) {
	if pattern == "" {
		return content, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return expandMultimodal(content, re), nil
}

func expandMultimodal(content any, re *regexp.Regexp) any {
	switch x := content.(type) {
	case string:
		return expandContentString(x, re)
	case []any:
		result := make([]any, len(x))
		for i, item := range x {
			result[i] = expandMultimodal(item, re)
		}
		return result
	case mswea.Message:
		return mswea.Message(expandMultimodal(map[string]any(x), re).(map[string]any))
	case map[string]any:
		result := deepCopy(x).(map[string]any)
		if c, ok := x["content"]; ok {
			result["content"] = expandMultimodal(c, re)
		}
		return result
	case nil:
		return nil
	}
	return fmt.Sprint(content)
}

func expandContentString(content string, re *regexp.Regexp) []any {
	matches := re.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return []any{map[string]any{"type": "text", "text": content}}
	}
	var result []any
	lastEnd := 0
	for _, m := range matches {
		if before := content[lastEnd:m[0]]; before != "" {
			result = append(result, map[string]any{"type": "text", "text": before})
		}
		if strings.TrimSpace(content[m[2]:m[3]]) == "image_url" {
			url := strings.TrimSpace(content[m[4]:m[5]])
			result = append(result, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
		lastEnd = m[1]
	}
	if after := content[lastEnd:]; after != "" {
		result = append(result, map[string]any{"type": "text", "text": after})
	}
	return result
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case mswea.Message:
		return mswea.Message(deepCopy(map[string]any(x)).(map[string]any))
	case map[string]any:
		result := make(map[string]any, len(x))
		for k, val := range x {
			result[k] = deepCopy(val)
		}
		return result
	case []any:
		result := make([]any, len(x))
		for i, val := range x {
			result[i] = deepCopy(val)
		}
		return result
	}
	return v
}
