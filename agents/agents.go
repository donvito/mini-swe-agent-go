package agents

import (
	"fmt"
	"maps"

	mswea "github.com/donvito/mini-swe-agent-go"
)

var agentMapping = map[string]func(mswea.Model, mswea.Environment, map[string]any) (mswea.Agent, error){
	"default": func(m mswea.Model, e mswea.Environment, c map[string]any) (mswea.Agent, error) {
		return NewDefaultAgent(m, e, c)
	},
	"interactive": func(m mswea.Model, e mswea.Environment, c map[string]any) (mswea.Agent, error) {
		return NewInteractiveAgent(m, e, c)
	},
}

// GetAgent creates an agent from a config dict. The "agent_class" key selects the implementation
// (falling back to defaultType); all other keys are passed to the agent's config.
func GetAgent(model mswea.Model, env mswea.Environment, config map[string]any, defaultType string) (mswea.Agent, error) {
	config = maps.Clone(config)
	spec, _ := config["agent_class"].(string)
	delete(config, "agent_class")
	if spec == "" {
		spec = defaultType
	}
	factory, ok := agentMapping[spec]
	if !ok {
		return nil, fmt.Errorf("unknown agent type: %q (available: default, interactive)", spec)
	}
	return factory(model, env, config)
}
