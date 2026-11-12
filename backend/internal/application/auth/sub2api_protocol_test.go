package auth

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

func TestSub2APIProtocolChoiceCannotBypassEligibility(t *testing.T) {
	for _, platform := range []string{"openai", "deepseek", "anthropic", "gemini", "unknown"} {
		for _, tc := range []struct {
			name   string
			mutate func(*port.Key)
		}{
			{"inactive", func(k *port.Key) { k.Status = "disabled" }},
			{"unknown status", func(k *port.Key) { k.Status = "unknown" }},
			{"expired", func(k *port.Key) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past }},
			{"missing group", func(k *port.Key) { k.Group = nil }},
			{"inactive group", func(k *port.Key) { k.Group.Status = "disabled" }},
			{"missing metadata", func(k *port.Key) { k.Group.Status = "" }},
			{"quota", func(k *port.Key) { k.Quota = 1; k.QuotaUsed = 1 }},
			{"invalid quota", func(k *port.Key) { k.Quota = math.NaN() }},
			{"invalid key", func(k *port.Key) { k.Key = "" }},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				s, remote, repo, importer := sub2apiFixture(t)
				remote.snapshot.Keys[0].Group.Platform = platform
				tc.mutate(&remote.snapshot.Keys[0])
				_, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{}, Sub2APIExchangeOptions{ProtocolSelections: []Sub2APIProtocolChoice{{KeyID: 2, Protocol: "openai_chat_completions"}}})
				if !errors.Is(err, ErrSub2APINoKey) || repo.calls != 0 || repo.profileWrites != 0 || repo.createSessionCount != 0 || importer.calls != 0 {
					t.Fatal("selection bypassed eligibility", err)
				}
			})
		}
	}
}

func TestSub2APIProtocolPreflightHasNoWritesAndUsesCanonicalInstance(t *testing.T) {
	s, remote, repo, importer := sub2apiFixture(t)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	result, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if !errors.Is(err, ErrSub2APIProtocolRequired) || result != nil || repo.calls != 0 || repo.profileWrites != 0 || repo.createSessionCount != 0 || importer.calls != 0 {
		t.Fatal("preflight wrote state", err)
	}
	// Known platforms also accept explicit choices from the same narrow allowlist.
	remote.snapshot.Keys[0].Group.Platform = "openai"
	result, err = s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{}, Sub2APIExchangeOptions{ProtocolSelections: []Sub2APIProtocolChoice{{KeyID: 2, Protocol: "openai_chat_completions"}}})
	if err != nil || result == nil || importer.input.Protocol != "openai_chat_completions" || importer.input.BaseURL != "https://sub.example.com/v1" {
		t.Fatal("explicit known override", err)
	}
}
