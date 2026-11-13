package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSub2APIPasswordFlagDefaultsAndSources(t *testing.T) {
	cleanupConfigEnv(t)
	t.Setenv("SUB2API_PASSWORD_LOGIN_ENABLED", "")
	chdir(t, t.TempDir())
	if Load().Sub2APIPasswordLoginEnabled {
		t.Fatal("password login enabled by default")
	}
	path := filepath.Join(t.TempDir(), "test-config.yaml")
	if err := os.WriteFile(path, []byte("sub2api:\n  password_login_enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	if !Load().Sub2APIPasswordLoginEnabled {
		t.Fatal("YAML flag not loaded")
	}
	t.Setenv("SUB2API_PASSWORD_LOGIN_ENABLED", "false")
	if Load().Sub2APIPasswordLoginEnabled {
		t.Fatal("environment override ignored")
	}
}
