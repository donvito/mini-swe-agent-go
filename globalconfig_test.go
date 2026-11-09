package minisweagent

import (
	"os"
	"testing"
)

func TestGlobalConfigKeys(t *testing.T) {
	t.Setenv("MSWEA_GLOBAL_CONFIG_DIR", t.TempDir())
	t.Setenv("MSWEA_TEST_A", "")
	t.Setenv("MSWEA_TEST_B", "")
	if err := os.WriteFile(GlobalConfigFile(), []byte("# comment\nMSWEA_TEST_A='old'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"MSWEA_TEST_A", "new"}, {"MSWEA_TEST_B", "it's"}} {
		if err := SetGlobalConfigKey(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := os.ReadFile(GlobalConfigFile())
	if want := "# comment\nMSWEA_TEST_A='new'\nMSWEA_TEST_B=\"it's\"\n"; string(content) != want {
		t.Errorf("got %q, want %q", content, want)
	}
	os.Unsetenv("MSWEA_TEST_A")
	os.Unsetenv("MSWEA_TEST_B")
	if err := LoadGlobalConfig(false); err != nil || os.Getenv("MSWEA_TEST_A") != "new" || os.Getenv("MSWEA_TEST_B") != "it's" {
		t.Errorf("load: %v A=%q B=%q", err, os.Getenv("MSWEA_TEST_A"), os.Getenv("MSWEA_TEST_B"))
	}
	if err := UnsetGlobalConfigKey("MSWEA_TEST_A"); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(GlobalConfigFile())
	if want := "# comment\nMSWEA_TEST_B=\"it's\"\n"; string(content) != want || os.Getenv("MSWEA_TEST_A") != "" {
		t.Errorf("got %q", content)
	}
}
