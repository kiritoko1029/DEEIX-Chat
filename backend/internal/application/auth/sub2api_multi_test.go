package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

func TestSub2APIProfileSanitizesAndUpdatesWithoutChangingLocalIdentity(t *testing.T) {
	s, remote, repo, _ := sub2apiFixture(t)
	remote.snapshot.User.Username = "  小\x00明\n\u202e  "
	remote.snapshot.User.Email = "local-owner@example.com"
	result, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if err != nil {
		t.Fatal(err)
	}
	profile := result.User.ExternalProfile
	if profile == nil || profile.Username != "小明" || profile.DisplayName != "小明" || profile.Email != "local-owner@example.com" {
		t.Fatalf("profile=%+v", profile)
	}
	original := *repo.user
	remote.snapshot.User.Username = "更新昵称"
	remote.snapshot.User.Email = "new@example.com"
	result, err = s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if err != nil || result.User.ExternalProfile.DisplayName != "更新昵称" || repo.profileWrites != 2 {
		t.Fatal("profile not refreshed", err)
	}
	if repo.user.ID != original.ID || repo.user.PublicID != original.PublicID || repo.user.Username != original.Username || repo.user.Email != "" || repo.user.EmailVerifiedAt != nil || repo.credential.PasswordEnabled || repo.user.Role != "user" {
		t.Fatal("external profile changed local identity/security")
	}
	for _, email := range []string{"Name <a@example.com>", "a\nb@example.com", strings.Repeat("a", 255) + "@example.com", "not-an-email"} {
		p := sub2APIProfile(port.User{Email: email})
		if p.Email != "" || p.DisplayName != "sub2api 用户" {
			t.Fatalf("invalid email accepted: %+v", p)
		}
	}
	p := sub2APIProfile(port.User{Username: strings.Repeat("名", 500), Email: "fallback@example.com"})
	if len([]rune(p.Username)) != 128 {
		t.Fatal("username not bounded")
	}
	if p := sub2APIProfile(port.User{Email: "fallback@example.com"}); p.DisplayName != "fallback@example.com" {
		t.Fatal("missing email fallback")
	}
	// An unrelated local user has no external profile.
	repo.profile = nil
	view, err := s.buildUserView(context.Background(), domainuser.User{ID: 555, Role: "user", Status: "active"})
	if err != nil || view.ExternalProfile != nil {
		t.Fatal("ordinary account affected", err)
	}
}

func TestSub2APIAllKeysSummaryStablePartialFailuresAndNoMutationBeforeTwoFactor(t *testing.T) {
	s, remote, repo, importer := sub2apiFixture(t)
	base := remote.snapshot.Keys[0]
	remote.snapshot.Keys = nil
	platforms := []string{"openai", "anthropic", "gemini", "openai", "openai", "unsupported", "openai"}
	for i, platform := range platforms {
		key := base
		key.ID = int64(i + 1)
		key.Name = strings.Repeat("同名", 50)
		group := *base.Group
		group.Platform = platform
		group.Name = "同分组"
		key.Group = &group
		if i == 3 {
			key.Status = "disabled"
		}
		if i == 4 {
			past := time.Now().Add(-time.Hour)
			key.ExpiresAt = &past
		}
		remote.snapshot.Keys = append([]port.Key{key}, remote.snapshot.Keys...)
	}
	importer.errorsByID = map[int64]error{7: personal.ErrUpstreamUnavailable}
	result, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if err != nil {
		t.Fatal(err)
	}
	summary := result.Sub2APIImport
	if summary.Total != 7 || summary.Eligible != 4 || summary.Reused != 3 || summary.Skipped != 3 || summary.Failed != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	for i, item := range summary.Items {
		if item.KeyID != int64(i+1) {
			t.Fatal("unstable key ordering")
		}
	}
	if summary.Items[3].Reason != "inactive" || summary.Items[4].Reason != "expired" || summary.Items[5].Reason != "unsupported_platform" || summary.Items[6].Reason != "probe_failed" {
		t.Fatal("wrong classifications")
	}
	for i, protocol := range []string{"openai_responses", "anthropic_messages", "google_generate_content"} {
		input := importer.inputsByID[int64(i+1)]
		if input.Protocol != protocol || len([]rune(input.Name)) > 64 || !strings.HasSuffix(input.Name, "#"+formatSub2APIKeyID(int64(i+1))) {
			t.Fatalf("bad candidate %+v", input)
		}
		if i == 2 && input.BaseURL != "https://sub.example.com/v1beta" {
			t.Fatal("wrong Gemini URL")
		}
	}
	repo.twoFactor = true
	before := repo.profileWrites
	remote.snapshot.User.Username = "must not sync"
	result, err = s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if err != nil || !result.TwoFactorRequired || repo.profileWrites != before || result.User.ID != 0 || result.User.ExternalProfile != nil {
		t.Fatal("2FA mutation or disclosure", err)
	}
	for _, input := range importer.inputsByID {
		if input.AllowChanges {
			t.Fatal("2FA import enabled")
		}
	}
	for _, item := range result.Sub2APIImport.Items {
		if item.ProviderID != "" {
			t.Fatal("local identifier before 2FA")
		}
	}
}

func TestSub2APIImportZeroSuccessReturnsSafeSummaryAndChecksEveryOwner(t *testing.T) {
	s, remote, repo, importer := sub2apiFixture(t)
	importer.err = personal.ErrLimitReached
	_, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	var failure *Sub2APIImportError
	if !errors.As(err, &failure) || failure.Summary.Failed != 1 || failure.Summary.Items[0].Reason != "limit_reached" || repo.profileWrites != 0 {
		t.Fatal("missing all-failed summary", err)
	}
	bad := remote.snapshot.Keys[0]
	bad.ID = 3
	bad.UserID = 999
	remote.snapshot.Keys = append(remote.snapshot.Keys, bad)
	before := importer.calls
	_, err = s.ExchangeSub2API(context.Background(), 7, "synthetic-token", "test", requestmeta.SessionAuditContext{})
	if !errors.Is(err, ErrSub2APIRejected) || importer.calls != before {
		t.Fatal("wrote before all owner checks")
	}
}

type deadlineSub2APIImporter struct {
	mu          sync.Mutex
	active, max int
}

func (f *deadlineSub2APIImporter) SyncSub2API(ctx context.Context, _ uint, _ string, _ int64, _ personal.Sub2APIImportInput, _ personal.RequestMeta) (personal.Sub2APIImportResult, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.max {
		f.max = f.active
	}
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	// Match production fetchModels: context errors are masked before reaching auth.
	return personal.Sub2APIImportResult{}, personal.ErrUpstreamUnavailable
}
func TestSub2APIImportBudgetAndConcurrencyAreBounded(t *testing.T) {
	s, remote, _, _ := sub2apiFixture(t)
	importer := &deadlineSub2APIImporter{}
	s.sub2apiProviders = importer
	base := remote.snapshot.Keys[0]
	keys := make([]port.Key, 20)
	for i := range keys {
		keys[i] = base
		keys[i].ID = int64(i + 1)
	}
	summary, candidates, err := prepareSub2APIImport(keys, 7, "https://sub.example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Reserve five seconds for finalization, leaving only 40ms for these fake probes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second+40*time.Millisecond)
	defer cancel()
	start := time.Now()
	s.importSub2APIKeys(ctx, 123, "https://sub.example.com", candidates, summary, true, personal.RequestMeta{})
	if time.Since(start) > time.Second || summary.Failed != 20 || importer.max > 3 || importer.max < 1 || ctx.Err() != nil {
		t.Fatal("unbounded probes or consumed finalization budget")
	}
	for _, item := range summary.Items {
		if item.Reason != "timeout" {
			t.Fatal("timeout not reported")
		}
	}
}
