// Package utils contains small helpers shared by agents, environments and models.
package utils

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// RecursiveMerge merges multiple dicts recursively. Later dicts take precedence over earlier ones.
// Nested dicts are merged recursively (and copied, so the inputs are never modified).
func RecursiveMerge(dicts ...map[string]any) map[string]any {
	result := map[string]any{}
	for _, d := range dicts {
		for key, value := range d {
			valueMap, valueIsMap := value.(map[string]any)
			if existing, ok := result[key].(map[string]any); ok && valueIsMap {
				result[key] = RecursiveMerge(existing, valueMap)
			} else if valueIsMap {
				result[key] = RecursiveMerge(valueMap)
			} else {
				result[key] = value
			}
		}
	}
	return result
}

// Decode populates the config struct pointed to by target from a dict (e.g., parsed from YAML).
// Fields not present in the dict keep their current (default) values; unknown keys are ignored.
func Decode(dict map[string]any, target any) error {
	b, err := json.Marshal(dict)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

// ToMap converts a (config) struct to a dict keyed by its json field names, keeping native value types
// so that templates render e.g. floats and ints the same way as the Python version.
// Embedded structs are flattened.
func ToMap(v any) map[string]any { return toMap(reflect.Indirect(reflect.ValueOf(v))) }

func toMap(rv reflect.Value) map[string]any {
	result := map[string]any{}
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			for k, val := range toMap(rv.Field(i)) {
				result[k] = val
			}
			continue
		}
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		result[name] = rv.Field(i).Interface()
	}
	return result
}

// TypeName returns the type name of v (e.g., "models.OpenAIModel") for serialization.
func TypeName(v any) string { return strings.TrimPrefix(fmt.Sprintf("%T", v), "*") }

// EnvMap returns the process environment as a dict.
func EnvMap(environ []string) map[string]any {
	result := map[string]any{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			result[k] = v
		}
	}
	return result
}
