package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestKeyValueSpecToNestedDict(t *testing.T) {
	for spec, want := range map[string]map[string]any{
		"model.model_kwargs.temperature=0.5": {"model": map[string]any{"model_kwargs": map[string]any{"temperature": 0.5}}},
		"agent.mode=yolo":                    {"agent": map[string]any{"mode": "yolo"}},
		"a=[1, \"x\"]":                       {"a": []any{1.0, "x"}},
		"model.model_name=a/b=c":             {"model": map[string]any{"model_name": "a/b=c"}},
	} {
		if got, err := KeyValueSpecToNestedDict(spec); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v %v", spec, got, err)
		}
	}
	if _, err := KeyValueSpecToNestedDict("a..b=1"); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestGetConfigFromSpec(t *testing.T) {
	for _, name := range []string{"mini", "mini.yaml", "default.yaml", "mini_textbased"} {
		cfg, err := GetConfigFromSpec(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := cfg["agent"].(map[string]any)["system_template"].(string); !ok {
			t.Errorf("%s: missing agent.system_template", name)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte("agent:\n  step_limit: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSWEA_CONFIG_DIR", dir)
	if cfg, err := GetConfigFromSpec("custom"); err != nil || cfg["agent"].(map[string]any)["step_limit"] != 7 {
		t.Errorf("got %v %v", cfg, err)
	}
	if _, err := GetConfigFromSpec("does-not-exist.yaml"); err == nil {
		t.Error("expected error for missing config")
	}
}
