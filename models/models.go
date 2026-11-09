// Package models contains the LM interfaces of mini-swe-agent-go and convenience functions for selecting models.
package models

import (
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	mswea "github.com/donvito/mini-swe-agent-go"
)

// GlobalModelStats tracks the cost and number of calls of all models, with optional global limits
// (set via MSWEA_GLOBAL_COST_LIMIT and MSWEA_GLOBAL_CALL_LIMIT).
type GlobalModelStats struct {
	mu        sync.Mutex
	once      sync.Once
	cost      float64
	nCalls    int
	CostLimit float64
	CallLimit int
}

var GlobalStats = &GlobalModelStats{}

func (s *GlobalModelStats) init() {
	s.once.Do(func() {
		s.CostLimit, _ = strconv.ParseFloat(os.Getenv("MSWEA_GLOBAL_COST_LIMIT"), 64)
		s.CallLimit, _ = strconv.Atoi(os.Getenv("MSWEA_GLOBAL_CALL_LIMIT"))
		if (s.CostLimit > 0 || s.CallLimit > 0) && os.Getenv("MSWEA_SILENT_STARTUP") == "" {
			fmt.Printf("Global cost/call limit: $%.4f / %d\n", s.CostLimit, s.CallLimit)
		}
	})
}

// Add records a model call with its cost and returns an error if a global limit is exceeded.
func (s *GlobalModelStats) Add(cost float64) error {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cost += cost
	s.nCalls++
	if (s.CostLimit > 0 && s.CostLimit < s.cost) || (s.CallLimit > 0 && s.CallLimit < s.nCalls+1) {
		return fmt.Errorf("global cost/call limit exceeded: $%.4f / %d", s.cost, s.nCalls)
	}
	return nil
}

func (s *GlobalModelStats) Cost() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cost
}

func (s *GlobalModelStats) NCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nCalls
}

// Reset zeroes the stats (useful in tests).
func (s *GlobalModelStats) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cost, s.nCalls = 0, 0
}

var modelClassMapping = map[string]func(config map[string]any) (mswea.Model, error){
	"openai":               func(c map[string]any) (mswea.Model, error) { return NewOpenAIModel(c) },
	"textbased":            func(c map[string]any) (mswea.Model, error) { return NewTextbasedModel(c) },
	"openrouter":           func(c map[string]any) (mswea.Model, error) { return NewOpenAIModel(withProvider(c, "openrouter")) },
	"openrouter_textbased": func(c map[string]any) (mswea.Model, error) { return NewTextbasedModel(withProvider(c, "openrouter")) },
	"deterministic":        func(c map[string]any) (mswea.Model, error) { return NewDeterministicModel(c, false) },
	"deterministic_toolcall": func(c map[string]any) (mswea.Model, error) {
		return NewDeterministicModel(c, true)
	},
}

// Aliases so that configs written for the Python version keep working.
var modelClassAliases = map[string]string{"litellm": "openai", "litellm_textbased": "textbased", "openai_textbased": "textbased"}

func withProvider(config map[string]any, provider string) map[string]any {
	if _, ok := config["provider"]; !ok {
		config = maps.Clone(config)
		config["provider"] = provider
	}
	return config
}

// GetModel returns an initialized model from any kind of user input or settings.
// The "model_class" key of the config selects the implementation (default: "openai").
func GetModel(inputModelName string, config map[string]any) (mswea.Model, error) {
	name, err := GetModelName(inputModelName, config)
	if err != nil {
		return nil, err
	}
	config = maps.Clone(config)
	if config == nil {
		config = map[string]any{}
	}
	config["model_name"] = name
	class, _ := config["model_class"].(string)
	delete(config, "model_class")
	if alias, ok := modelClassAliases[class]; ok {
		class = alias
	}
	if class == "" {
		class = "openai"
	}
	factory, ok := modelClassMapping[class]
	if !ok {
		keys := make([]string, 0, len(modelClassMapping))
		for k := range modelClassMapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unknown model class: %q (available: %v)", class, keys)
	}
	return factory(config)
}

// GetModelName returns the model name from the input, the config, or $MSWEA_MODEL_NAME (in that order).
func GetModelName(inputModelName string, config map[string]any) (string, error) {
	if inputModelName != "" {
		return inputModelName, nil
	}
	if fromConfig, _ := config["model_name"].(string); fromConfig != "" {
		return fromConfig, nil
	}
	if fromEnv := os.Getenv("MSWEA_MODEL_NAME"); fromEnv != "" {
		return fromEnv, nil
	}
	return "", fmt.Errorf("no default model set. Please run `mini-go-extra config setup` to set one")
}

func isAnthropicModel(name string) bool {
	name = strings.ToLower(name)
	for _, s := range []string{"anthropic", "sonnet", "opus", "claude"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}
