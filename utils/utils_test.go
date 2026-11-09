package utils

import (
	"reflect"
	"strings"
	"testing"
)

func TestRecursiveMerge(t *testing.T) {
	a := map[string]any{"x": 1, "nested": map[string]any{"a": 1, "b": 2}}
	b := map[string]any{"nested": map[string]any{"b": 3, "c": 4}, "y": "s"}
	got := RecursiveMerge(a, nil, b)
	want := map[string]any{"x": 1, "y": "s", "nested": map[string]any{"a": 1, "b": 3, "c": 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	got["nested"].(map[string]any)["a"] = 99
	if a["nested"].(map[string]any)["a"] != 1 {
		t.Error("input was modified")
	}
}

func TestToMapAndDecode(t *testing.T) {
	type inner struct {
		A int `json:"a"`
	}
	type outer struct {
		inner
		B      float64 `json:"b"`
		Hidden string  `json:"-"`
		hidden string
	}
	v := outer{inner: inner{A: 1}, B: 2.0}
	if got := ToMap(&v); !reflect.DeepEqual(got, map[string]any{"a": 1, "b": 2.0}) {
		t.Errorf("ToMap = %v", got)
	}
	cfg := struct {
		A int     `json:"a"`
		B float64 `json:"b"`
	}{A: 5, B: 1.5}
	if err := Decode(map[string]any{"b": 3, "unknown": true}, &cfg); err != nil || cfg.A != 5 || cfg.B != 3 {
		t.Errorf("Decode = %+v %v", cfg, err)
	}
	if err := Decode(map[string]any{"a": "not a number"}, &cfg); err == nil {
		t.Error("expected type error")
	}
}

func TestRender(t *testing.T) {
	output := strings.Repeat("é", 12000)
	got, err := Render(`{{ x | length }} {{ x[:3] }} {{ x | length - 10000 }} {% if y is defined %}y{% else %}n{% endif %} {{ cost }} {{ obj.k | tojson }}`,
		map[string]any{"x": output, "cost": 3.0, "obj": map[string]any{"k": "a\"b"}})
	if err != nil || got != `12000 ééé 2000 n 3.0 "a\"b"` {
		t.Errorf("got %q %v", got, err)
	}
	if _, err := Render("{{ missing }}", nil); err == nil {
		t.Error("expected error for undefined variable")
	}
}
