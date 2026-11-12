package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

func TestSub2APIDeepSeekPreparationUsesResponsesAndNormalizedGateway(t *testing.T) {
	for _, tc := range []struct {
		name, configured, advertised, want string
	}{
		{"instance", "", "", "https://sub.example.com/v1"},
		{"advertised v1", "", "https://sub.example.com/v1/", "https://sub.example.com/v1"},
		{"advertised proxy", "", "https://sub.example.com/proxy/v1", "https://sub.example.com/proxy/v1"},
		{"configured v1", "https://api.example.com/proxy/v1/", "", "https://api.example.com/proxy/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, remote, _, _ := sub2apiFixture(t)
			remote.snapshot.Keys[0].Group.Platform = "deepseek"
			summary, candidates, err := prepareSub2APIImport(remote.snapshot.Keys, 7, "https://sub.example.com", tc.configured, tc.advertised)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Total != 1 || summary.Eligible != 1 || summary.Skipped != 0 || len(candidates) != 1 {
				t.Fatalf("summary=%+v candidates=%d", summary, len(candidates))
			}
			input := candidates[0].input
			if input.Protocol != "openai_responses" || input.BaseURL != tc.want || input.APIKey != remote.snapshot.Keys[0].Key {
				t.Fatalf("protocol=%q baseURL=%q", input.Protocol, input.BaseURL)
			}
		})
	}
}

func TestSub2APIDeepSeekRetainsEligibilityChecks(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name, reason string
		mutate       func(*port.Key)
	}{
		{"inactive key", "inactive", func(k *port.Key) { k.Status = "disabled" }},
		{"expired key", "expired", func(k *port.Key) { k.ExpiresAt = &past }},
		{"inactive group", "inactive", func(k *port.Key) { k.Group.Status = "disabled" }},
		{"invalid credential", "invalid_key", func(k *port.Key) { k.Key = "" }},
		{"quota exhausted", "quota_exhausted", func(k *port.Key) { k.Quota = 1; k.QuotaUsed = 1 }},
		{"invalid group", "invalid_group", func(k *port.Key) { k.Group.ID++ }},
		{"unknown platform with suggestive names", "unsupported_platform", func(k *port.Key) {
			k.Group.Platform = "unknown"
			k.Group.Name = "DeepSeek OpenAI Responses"
			k.Name = "deepseek-chat"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, remote, _, _ := sub2apiFixture(t)
			remote.snapshot.Keys[0].Group.Platform = "deepseek"
			tc.mutate(&remote.snapshot.Keys[0])
			summary, candidates, err := prepareSub2APIImport(remote.snapshot.Keys, 7, "https://sub.example.com", "", "")
			if tc.reason == "unsupported_platform" {
				if !errors.Is(err, ErrSub2APIProtocolRequired) || len(candidates) != 0 {
					t.Fatal("unknown platform must require explicit protocol", err)
				}
				return
			}
			if !errors.Is(err, ErrSub2APINoKey) || summary == nil || len(candidates) != 0 {
				t.Fatalf("err=%v summary=%+v candidates=%d", err, summary, len(candidates))
			}
			if summary.Eligible != 0 || summary.Skipped != 1 || summary.Items[0].Status != "skipped" || summary.Items[0].Reason != tc.reason {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestSub2APIDeepSeekMixedKeysStayIndependent(t *testing.T) {
	s, remote, _, importer := sub2apiFixture(t)
	base := remote.snapshot.Keys[0]
	remote.snapshot.Keys = nil
	for i, platform := range []string{"deepseek", "openai", "anthropic", "gemini", "deepseek", "unknown"} {
		key := base
		key.ID = int64(i + 1)
		key.Key = fmt.Sprintf("synthetic-mixed-key-%d", i)
		key.Name = "same name"
		group := *base.Group
		group.Platform = platform
		group.Name = "DeepSeek"
		key.Group = &group
		remote.snapshot.Keys = append([]port.Key{key}, remote.snapshot.Keys...)
	}
	result, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{}, Sub2APIExchangeOptions{SkipKeyIDs: []int64{6}})
	if err != nil {
		t.Fatal(err)
	}
	summary := result.Sub2APIImport
	if summary == nil || summary.Total != 6 || summary.Eligible != 5 || summary.Reused != 5 || summary.Skipped != 1 || summary.Failed != 0 || importer.calls != 5 || len(importer.inputsByID) != 5 {
		t.Fatalf("summary=%+v calls=%d", summary, importer.calls)
	}
	for i, protocol := range []string{"openai_responses", "openai_responses", "anthropic_messages", "google_generate_content", "openai_responses"} {
		id := int64(i + 1)
		input, ok := importer.inputsByID[id]
		baseURL := "https://sub.example.com/v1"
		if protocol == "google_generate_content" {
			baseURL += "beta"
		}
		if !ok || input.Protocol != protocol || input.BaseURL != baseURL || input.APIKey != fmt.Sprintf("synthetic-mixed-key-%d", i) {
			t.Fatalf("key=%d protocol=%q baseURL=%q", id, input.Protocol, input.BaseURL)
		}
		item := summary.Items[i]
		if item.KeyID != id || item.ProviderID != formatSub2APIKeyID(id) || item.Status != "reused" || item.Reason != "" {
			t.Fatalf("item=%+v", item)
		}
	}
	if summary.Items[5].KeyID != 6 || summary.Items[5].Reason != "user_skipped" {
		t.Fatalf("unknown platform item=%+v", summary.Items[5])
	}
}

func TestSub2APIDeepSeekOwnerMismatchStillRejectsBeforeWrites(t *testing.T) {
	s, remote, repo, importer := sub2apiFixture(t)
	remote.snapshot.Keys[0].Group.Platform = "deepseek"
	remote.snapshot.Keys[0].UserID = 999
	_, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if !errors.Is(err, ErrSub2APIRejected) || repo.calls != 0 || importer.calls != 0 {
		t.Fatalf("err=%v identity=%d import=%d", err, repo.calls, importer.calls)
	}
}
