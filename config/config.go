// Package config provides the builtin config files and utilities to load configs from specs.
package config

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var builtin embed.FS

// ReadConfigFile finds a config file by path or name and returns its content.
// Candidates are tried in order: the path itself, $MSWEA_CONFIG_DIR/<spec>, and the builtin configs.
func ReadConfigFile(spec string) ([]byte, error) {
	if ext := filepath.Ext(spec); ext != ".yaml" {
		spec = strings.TrimSuffix(spec, ext) + ".yaml"
	}
	configDir := os.Getenv("MSWEA_CONFIG_DIR")
	if configDir == "" {
		configDir = "."
	}
	candidates := []string{spec, filepath.Join(configDir, spec)}
	for _, candidate := range candidates {
		if content, err := os.ReadFile(candidate); err == nil {
			return content, nil
		}
	}
	if content, err := builtin.ReadFile(spec); err == nil {
		return content, nil
	}
	return nil, fmt.Errorf("could not find config file for %s (tried: %v and builtin configs %v)", spec, candidates, BuiltinConfigs())
}

// BuiltinConfigs lists the names of the builtin config files.
func BuiltinConfigs() []string {
	entries, _ := builtin.ReadDir(".")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// KeyValueSpecToNestedDict interprets key-value specs from the command line, e.g.
// "model.model_kwargs.temperature=0.5" -> {"model": {"model_kwargs": {"temperature": 0.5}}}
func KeyValueSpecToNestedDict(spec string) (map[string]any, error) {
	key, rawValue, _ := strings.Cut(spec, "=")
	var value any = rawValue
	if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
		value = rawValue
	}
	keys := strings.Split(key, ".")
	for _, k := range keys {
		if k == "" {
			return nil, fmt.Errorf("invalid config spec %q: empty config key", spec)
		}
	}
	result := map[string]any{keys[len(keys)-1]: value}
	for i := len(keys) - 2; i >= 0; i-- {
		result = map[string]any{keys[i]: result}
	}
	return result, nil
}

// GetConfigFromSpec loads a config from a file path/name or a key=value spec.
func GetConfigFromSpec(spec string) (map[string]any, error) {
	if strings.Contains(spec, "=") {
		return KeyValueSpecToNestedDict(spec)
	}
	content, err := ReadConfigFile(spec)
	if err != nil {
		return nil, err
	}
	config := map[string]any{}
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", spec, err)
	}
	return config, nil
}
