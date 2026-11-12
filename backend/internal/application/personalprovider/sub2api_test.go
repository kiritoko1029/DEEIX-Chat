package personalprovider

import (
	"context"
	"errors"
	"testing"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestSub2APIImportEncryptsEnablesModelsAndNeverOverwrites(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	input := CandidateInput{Protocol: "openai_responses", BaseURL: "https://sub.example.com/v1", APIKey: "fake-sub2api-api-key"}
	if err := f.service.EnsureSub2API(ctx, 7, "https://sub.example.com", 1, input, true, RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	items, _ := f.repo.ListByOwner(ctx, 7)
	if len(items) != 1 || items[0].APIKeyEnc == input.APIKey || len(items[0].Models) != 2 || items[0].Source != "sub2api" {
		t.Fatal("invalid import")
	}
	name := "用户自定义名称"
	models := []domain.Model{items[0].Models[0]}
	_, _ = f.repo.UpdateByOwner(ctx, 7, items[0].PublicID, repository.PersonalProviderPatch{Name: &name, Models: &models})
	if err := f.service.EnsureSub2API(ctx, 7, "https://sub.example.com", 1, input, false, RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	items, _ = f.repo.ListByOwner(ctx, 7)
	if len(items) != 1 || items[0].Name != name || len(items[0].Models) != 1 || len(f.lister.calls) != 1 {
		t.Fatal("re-login overwrote provider")
	}
	status := domain.StatusSuspended
	_, _ = f.repo.UpdateByOwner(ctx, 7, items[0].PublicID, repository.PersonalProviderPatch{Status: &status})
	if err := f.service.EnsureSub2API(ctx, 7, "https://sub.example.com", 1, input, true, RequestMeta{}); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspension bypass: %v", err)
	}
}

func TestSub2APIImportRespectsPoliciesAndTwoFactorNoMutation(t *testing.T) {
	input := CandidateInput{Protocol: "openai_responses", BaseURL: "https://sub.example.com/v1", APIKey: "fake-sub2api-api-key"}
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
		allow  bool
		want   error
	}{
		{"disabled", func(c *config.Config) { c.PersonalProvidersEnabled = false }, true, ErrFeatureDisabled},
		{"blocked host", func(c *config.Config) { c.PersonalProvidersBlockedHosts = "sub.example.com" }, true, ErrBlockedHost},
		{"2fa missing import", nil, false, ErrModelUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.mutate)
			err := f.service.EnsureSub2API(context.Background(), 7, "https://sub.example.com", 1, input, tc.allow, RequestMeta{})
			if !errors.Is(err, tc.want) || len(f.repo.items) != 0 || len(f.lister.calls) != 0 {
				t.Fatalf("err=%v items=%d", err, len(f.repo.items))
			}
		})
	}
}

func TestSub2APIImportsKeepSameNamedModelsIndependentlySelectable(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	input := Sub2APIImportInput{CandidateInput: CandidateInput{Protocol: "openai_responses", BaseURL: "https://sub.example.com/v1", APIKey: "synthetic-same-key"}, Name: "同名 · #1", AllowChanges: true}
	first, err := f.service.SyncSub2API(ctx, 7, "https://sub.example.com", 1, input, RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "同名 · #2"
	second, err := f.service.SyncSub2API(ctx, 7, "https://sub.example.com", 2, input, RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProviderID == second.ProviderID {
		t.Fatal("different remote IDs merged")
	}
	entries, err := f.service.ListActiveModels(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, entry := range entries {
		if entry.Model != "gpt-4o" {
			continue
		}
		if selected[entry.Ref] || entry.Ref != domain.FormatModelRef(entry.ProviderID, "gpt-4o") {
			t.Fatal("model references merged")
		}
		selected[entry.Ref] = true
		resolved, err := f.service.Resolve(ctx, 7, entry.Ref)
		if err != nil || resolved.Ref != entry.Ref || resolved.APIKey != input.APIKey {
			t.Fatal("selected a different provider", err)
		}
	}
	if len(selected) != 2 {
		t.Fatal("missing independently selectable same-named model")
	}
	// An empty user-selected model set is retained, not re-enabled on login.
	empty := []domain.Model{}
	_, _ = f.repo.UpdateByOwner(ctx, 7, first.ProviderID, repository.PersonalProviderPatch{Models: &empty})
	if _, err := f.service.SyncSub2API(ctx, 7, "https://sub.example.com", 1, input, RequestMeta{}); !errors.Is(err, ErrImportedProviderDisabled) {
		t.Fatal("empty model set restored", err)
	}
}

func TestSub2APIProtocolLookupIsOwnerInstanceAndKeyBoundAndReadOnly(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	input := CandidateInput{Protocol: "openai_chat_completions", BaseURL: "https://sub.example.com/v1", APIKey: "synthetic-key"}
	if err := f.service.EnsureSub2API(ctx, 7, "https://sub.example.com", 1, input, true, RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner    uint
		instance string
		key      int64
		found    bool
	}{
		{7, "https://sub.example.com", 1, true},
		{8, "https://sub.example.com", 1, false},
		{7, "https://other.example.com", 1, false},
		{7, "https://sub.example.com", 2, false},
	} {
		protocol, err := f.service.LookupSub2APIProtocol(ctx, tc.owner, tc.instance, tc.key)
		if tc.found {
			if err != nil || protocol != input.Protocol {
				t.Fatal("missing stored protocol", err)
			}
		} else if !errors.Is(err, repository.ErrNotFound) {
			t.Fatal("cross-boundary protocol lookup", err)
		}
	}
	if len(f.lister.calls) != 1 || len(f.repo.items) != 1 {
		t.Fatal("lookup probed or wrote")
	}
}
