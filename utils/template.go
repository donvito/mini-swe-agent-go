package utils

import (
	"bytes"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/builtins"
	"github.com/nikolalohinski/gonja/v2/config"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"
)

var templateConfig = func() *config.Config {
	cfg := config.New()
	cfg.StrictUndefined = true
	return cfg
}()

// Render renders a Jinja2 template (with strict undefined variables, like the Python version).
func Render(template string, vars map[string]any) (string, error) {
	loader, err := loaders.NewShiftedLoader("template", bytes.NewReader([]byte(template)), gonja.DefaultLoader)
	if err != nil {
		return "", err
	}
	env := &exec.Environment{
		Context:           exec.EmptyContext().Update(builtins.GlobalFunctions).Update(builtins.GlobalVariables),
		Filters:           builtins.Filters,
		Tests:             builtins.Tests,
		ControlStructures: builtins.ControlStructures,
		Methods:           builtins.Methods,
	}
	tpl, err := exec.NewTemplate("template", templateConfig, loader, env)
	if err != nil {
		return "", err
	}
	return tpl.ExecuteToString(exec.NewContext(vars))
}
