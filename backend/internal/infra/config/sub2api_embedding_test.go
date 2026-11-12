package config

import "testing"

func TestSub2APIEmbeddingIsExplicitAndFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name, base, want          string
		enabled, embedding, local bool
	}{
		{name: "default deny", base: "https://sub.example.test", enabled: true},
		{name: "disabled integration", base: "https://sub.example.test", embedding: true},
		{name: "trusted origin only", base: "https://SUB.example.test/panel", enabled: true, embedding: true, want: "https://sub.example.test"},
		{name: "no local mode", base: "https://sub.example.test", enabled: true, embedding: true, local: true},
		{name: "no wildcard", base: "*", enabled: true, embedding: true},
		{name: "no injected directive", base: "https://sub.example.test; frame-src *", enabled: true, embedding: true},
		{name: "no private origin", base: "https://127.0.0.1", enabled: true, embedding: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Sub2APIEnabled: tt.enabled, Sub2APIAllowEmbedding: tt.embedding, Sub2APIBaseURL: tt.base, LocalMode: tt.local}
			if got := cfg.Sub2APIFrameOrigin(); got != tt.want {
				t.Fatalf("origin=%q want=%q", got, tt.want)
			}
		})
	}
}
