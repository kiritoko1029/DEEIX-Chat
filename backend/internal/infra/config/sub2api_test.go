package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSub2API(t *testing.T) {
	cleanupConfigEnv(t)
	for _, key := range []string{"SUB2API_ENABLED", "SUB2API_BASE_URL", "SUB2API_API_BASE_URL", "SUB2API_ALLOW_EMBEDDING"} {
		old, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("sub2api:\n  enabled: true\n  base_url: https://sub.example.test/\n  api_base_url: https://gateway.example.test/v1/\n  allow_embedding: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg := Load()
	if !cfg.Sub2APIEnabled || cfg.Sub2APIBaseURL != "https://sub.example.test" || cfg.Sub2APIAPIBaseURL != "https://gateway.example.test/v1" {
		t.Fatal("sub2api YAML configuration was not loaded")
	}
	if !cfg.Sub2APIAllowEmbedding || cfg.Sub2APIFrameOrigin() != "https://sub.example.test" {
		t.Fatal("explicit embedding configuration was not loaded")
	}
	t.Setenv("SUB2API_ALLOW_EMBEDDING", "false")
	t.Setenv("SUB2API_ENABLED", "false")
	t.Setenv("SUB2API_BASE_URL", "https://override.example.test/")
	t.Setenv("SUB2API_API_BASE_URL", "")
	cfg = Load()
	if cfg.Sub2APIEnabled || cfg.Sub2APIAllowEmbedding || cfg.Sub2APIFrameOrigin() != "" || cfg.Sub2APIBaseURL != "https://override.example.test" || cfg.Sub2APIAPIBaseURL != "" {
		t.Fatal("sub2api environment overrides were not applied")
	}
}

func TestValidateSub2API(t *testing.T) {
	for _, tt := range []struct {
		name, base, api         string
		local, enabled, wantErr bool
	}{
		{name: "disabled by default"},
		{name: "valid", enabled: true, base: "https://sub.example.test"},
		{name: "path prefix and gateway", enabled: true, base: "https://sub.example.test/sub", api: "https://gateway.example.test/v1"},
		{name: "missing base", enabled: true, wantErr: true},
		{name: "http", enabled: true, base: "http://sub.example.test", wantErr: true},
		{name: "credentials", enabled: true, base: "https://user@sub.example.test", wantErr: true},
		{name: "query", enabled: true, base: "https://sub.example.test?token=invalid", wantErr: true},
		{name: "empty query", enabled: true, base: "https://sub.example.test?", wantErr: true},
		{name: "fragment", enabled: true, base: "https://sub.example.test/#x", wantErr: true},
		{name: "loopback", enabled: true, base: "https://127.0.0.1", wantErr: true},
		{name: "private gateway", enabled: true, base: "https://sub.example.test", api: "https://192.168.1.1/v1", wantErr: true},
		{name: "invalid port", enabled: true, base: "https://sub.example.test:99999", wantErr: true},
		{name: "metadata", enabled: true, base: "https://169.254.169.254", wantErr: true},
		{name: "local mode", enabled: true, base: "https://sub.example.test", local: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForEnv("dev")
			cfg.Sub2APIEnabled, cfg.Sub2APIBaseURL, cfg.Sub2APIAPIBaseURL, cfg.LocalMode = tt.enabled, tt.base, tt.api, tt.local
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
