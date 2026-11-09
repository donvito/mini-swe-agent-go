package minisweagent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
)

// GlobalConfigDir returns (and creates) the directory holding the global .env file and run outputs.
// It is separate from the Python version of mini-swe-agent, so both can be configured independently.
func GlobalConfigDir() string {
	dir := os.Getenv("MSWEA_GLOBAL_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			base = "."
		}
		dir = filepath.Join(base, "mini-swe-agent-go")
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func GlobalConfigFile() string { return filepath.Join(GlobalConfigDir(), ".env") }

// LoadGlobalConfig loads the global .env file into the process environment.
// Existing environment variables take precedence unless override is set.
func LoadGlobalConfig(override bool) error {
	path := GlobalConfigFile()
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if override {
		return godotenv.Overload(path)
	}
	return godotenv.Load(path)
}

// SetGlobalConfigKey sets a key in the global .env file (and the current process environment).
func SetGlobalConfigKey(key, value string) error {
	quoted := "'" + value + "'"
	if strings.Contains(value, "'") {
		quoted = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	if err := rewriteGlobalConfig(key, key+"="+quoted); err != nil {
		return err
	}
	return os.Setenv(key, value)
}

// UnsetGlobalConfigKey removes a key from the global .env file (and the current process environment).
func UnsetGlobalConfigKey(key string) error {
	if err := rewriteGlobalConfig(key, ""); err != nil {
		return err
	}
	return os.Unsetenv(key)
}

func rewriteGlobalConfig(key, newLine string) error {
	path := GlobalConfigFile()
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	keyRe := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(key) + `\s*=`)
	var lines []string
	replaced := false
	for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
		switch {
		case line == "" && len(lines) == 0:
		case !keyRe.MatchString(line):
			lines = append(lines, line)
		case newLine != "" && !replaced:
			lines, replaced = append(lines, newLine), true
		}
	}
	if newLine != "" && !replaced {
		lines = append(lines, newLine)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
