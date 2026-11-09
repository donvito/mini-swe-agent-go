package models

import (
	"reflect"
	"strings"
	"testing"

	mswea "github.com/donvito/mini-swe-agent-go"
)

func formatErrorContent(t *testing.T, err error) string {
	t.Helper()
	interrupt, ok := mswea.AsInterrupt(err)
	if !ok || interrupt.Kind != mswea.FormatError {
		t.Fatalf("expected format error, got %v", err)
	}
	return interrupt.Messages[0]["content"].(string)
}

func TestParseToolcallActions(t *testing.T) {
	toolCall := func(name, args string) any {
		return map[string]any{"id": "c1", "function": map[string]any{"name": name, "arguments": args}}
	}
	actions, err := ParseToolcallActions([]any{toolCall("bash", `{"command": "ls"}`), toolCall("bash", `{"command": "pwd"}`)}, "{{error}}", nil)
	if err != nil || !reflect.DeepEqual(actions, []mswea.Action{{Command: "ls", ToolCallID: "c1"}, {Command: "pwd", ToolCallID: "c1"}}) {
		t.Fatalf("got %v %v", actions, err)
	}
	for _, tc := range []struct {
		toolCalls []any
		want      string
	}{
		{nil, "No tool calls found"},
		{[]any{toolCall("python", `{"command": "ls"}`)}, "Unknown tool 'python'."},
		{[]any{toolCall("bash", `{"cmd": "ls"}`)}, "Missing 'command' argument"},
		{[]any{toolCall("bash", `not json`)}, "Error parsing tool call arguments"},
	} {
		_, err := ParseToolcallActions(tc.toolCalls, "{{error}}|{{has_tool_calls}}|{{finish_reason}}", map[string]any{"finish_reason": "stop"})
		if content := formatErrorContent(t, err); !strings.Contains(content, tc.want) || !strings.HasSuffix(content, "|stop") {
			t.Errorf("got %q, want %q", content, tc.want)
		}
	}
}

func TestParseRegexActions(t *testing.T) {
	regex := "```mswea_bash_command\\s*\\n(.*?)\\n```"
	actions, err := ParseRegexActions("THOUGHT\n```mswea_bash_command\ncat <<'EOF' > x\na\nEOF\n```\n", regex, "", nil)
	if err != nil || len(actions) != 1 || actions[0].Command != "cat <<'EOF' > x\na\nEOF" {
		t.Fatalf("got %v %v", actions, err)
	}
	tpl := "found {{actions|length}} actions"
	for content, want := range map[string]string{
		"no action": "found 0 actions",
		"```mswea_bash_command\nls\n```\n```mswea_bash_command\npwd\n```": "found 2 actions",
	} {
		_, err := ParseRegexActions(content, regex, tpl, nil)
		if got := formatErrorContent(t, err); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestFormatToolcallObservationMessagesPadsNotExecuted(t *testing.T) {
	actions := []mswea.Action{{Command: "ls", ToolCallID: "a"}, {Command: "pwd", ToolCallID: "b"}, {Command: "human"}}
	msgs, err := FormatToolcallObservationMessages(actions, []mswea.Output{{Output: "x", Extra: map[string]any{"exception_type": "T"}}}, defaultObservationTemplate, nil, "")
	if err != nil || len(msgs) != 3 {
		t.Fatalf("got %v %v", msgs, err)
	}
	if msgs[0]["tool_call_id"] != "a" || msgs[0].Extra()["exception_type"] != "T" || msgs[0].Extra()["raw_output"] != "x" {
		t.Errorf("first = %v", msgs[0])
	}
	if !strings.Contains(msgs[1]["content"].(string), "action was not executed") || msgs[1]["role"] != "tool" {
		t.Errorf("second = %v", msgs[1])
	}
	if msgs[2]["role"] != "user" {
		t.Errorf("human command observation should be a user message: %v", msgs[2])
	}
}

func TestSetCacheControl(t *testing.T) {
	original := []mswea.Message{
		{"role": "user", "content": []any{map[string]any{"type": "text", "text": "a", "cache_control": map[string]any{"type": "ephemeral"}}}},
		{"role": "assistant", "content": nil, "cache_control": map[string]any{"type": "ephemeral"}},
		{"role": "tool", "content": "out", "tool_call_id": "1"},
	}
	msgs, err := SetCacheControl(original, "default_end")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msgs[0]["content"].([]any)[0].(map[string]any)["cache_control"]; ok {
		t.Error("cache control not cleared from earlier message")
	}
	if _, ok := msgs[1]["cache_control"]; ok {
		t.Error("cache control not cleared from assistant message")
	}
	if msgs[2]["cache_control"] == nil || msgs[2]["content"].([]any)[0].(map[string]any)["cache_control"] != nil {
		t.Errorf("tool message should have message-level cache control: %v", msgs[2])
	}
	if _, ok := original[0]["content"].([]any)[0].(map[string]any)["cache_control"]; !ok || original[2]["content"] != "out" {
		t.Error("input messages were modified")
	}
	if _, err := SetCacheControl(original, "bogus"); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestExpandMultimodalContent(t *testing.T) {
	msg := mswea.Message{"role": "user", "content": "see <MSWEA_MULTIMODAL_CONTENT><CONTENT_TYPE>image_url</CONTENT_TYPE>data:image/png;base64,AAA</MSWEA_MULTIMODAL_CONTENT> ok"}
	got, err := ExpandMultimodalContent(msg, DefaultMultimodalRegex)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"type": "text", "text": "see "},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAA"}},
		map[string]any{"type": "text", "text": " ok"},
	}
	if !reflect.DeepEqual(got.(mswea.Message)["content"], want) || msg["content"].(string)[:3] != "see" {
		t.Errorf("got %v", got)
	}
	if got, _ := ExpandMultimodalContent(msg, ""); !reflect.DeepEqual(got, msg) {
		t.Error("empty pattern should disable expansion")
	}
}

func TestGetContentString(t *testing.T) {
	for _, tc := range []struct {
		msg  mswea.Message
		want string
	}{
		{mswea.Message{"content": "plain"}, "plain"},
		{mswea.Message{"content": `{"returncode": 0, "output": "hi"}`}, "<returncode>\n0\n<output>\nhi"},
		{mswea.Message{"content": []any{map[string]any{"type": "text", "text": "a"}}}, "a"},
		{mswea.Message{"content": "thinking", "tool_calls": []any{map[string]any{"function": map[string]any{"arguments": `{"command": "ls"}`}}}}, "thinking\n\n```\nls\n```"},
	} {
		if got := GetContentString(tc.msg); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
