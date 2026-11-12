package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

type fakeSub2API struct {
	snapshot port.Snapshot
	err      error
	calls    int
}

func TestSub2APIRejectionDetailsPreserveCodeAndDiscardSensitiveContext(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        func(*fakeSub2API)
		stage, reason string
	}{
		{"upstream binding", func(r *fakeSub2API) {
			r.err = fmt.Errorf("synthetic-token-private-url: %w", port.NewRejected(port.StageAuthMe, port.ReasonSessionBindingMismatch))
		}, port.StageAuthMe, port.ReasonSessionBindingMismatch},
		{"upstream forbidden", func(r *fakeSub2API) { r.err = port.NewRejected(port.StageKeys, port.ReasonUpstreamForbidden) }, port.StageKeys, port.ReasonUpstreamForbidden},
		{"plain sentinel", func(r *fakeSub2API) { r.err = fmt.Errorf("synthetic-token-private-url: %w", port.ErrRejected) }, port.StageIdentity, port.ReasonUpstreamUnauthorized},
		{"invalid details", func(r *fakeSub2API) {
			r.err = port.NewRejected("synthetic-token-private-url", "synthetic-token-private-url")
		}, port.StageIdentity, port.ReasonUpstreamUnauthorized},
		{"identity ID", func(r *fakeSub2API) { r.snapshot.User.ID = 999 }, port.StageIdentity, port.ReasonUserIDMismatch},
		{"identity status", func(r *fakeSub2API) { r.snapshot.User.Status = "inactive" }, port.StageIdentity, port.ReasonUserInactive},
		{"key owner", func(r *fakeSub2API) { r.snapshot.Keys[0].UserID = 999 }, port.StageKeys, port.ReasonKeyOwnerMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, repo, importer := sub2apiFixture(t)
			tc.mutate(r)
			_, err := s.ExchangeSub2API(context.Background(), 7, "synthetic-token-private-url", "test", requestmeta.SessionAuditContext{})
			var rejection *Sub2APIRejectionError
			if !errors.Is(err, ErrSub2APIRejected) || !errors.As(err, &rejection) || apperr.Code(err) != "auth.sub2api_rejected" {
				t.Fatalf("error contract=%v", err)
			}
			details := rejection.Details()
			if !strings.Contains(err.Error(), "stage="+details.Stage) || !strings.Contains(err.Error(), "reason="+details.Reason) {
				t.Fatal("missing safe diagnostics in server error")
			}
			if details.Stage != tc.stage || details.Reason != tc.reason {
				t.Fatalf("details=%+v", details)
			}
			encoded, _ := json.Marshal(details)
			if strings.Contains(err.Error()+string(encoded), "synthetic-token-private-url") || repo.calls != 0 || importer.calls != 0 {
				t.Fatal("rejection leaked or provisioned account")
			}
		})
	}
}

func (f *fakeSub2API) Fetch(context.Context, string, string, int64, bool) (port.Snapshot, error) {
	f.calls++
	return f.snapshot, f.err
}

type fakeSub2APIImporter struct {
	mu          sync.Mutex
	errorsByID  map[int64]error
	inputsByID  map[int64]personal.Sub2APIImportInput
	calls       int
	allowCreate bool
	err         error
	input       personal.CandidateInput
}

func (f *fakeSub2APIImporter) SyncSub2API(_ context.Context, _ uint, _ string, id int64, input personal.Sub2APIImportInput, _ personal.RequestMeta) (personal.Sub2APIImportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.allowCreate = input.AllowChanges
	f.input = input.CandidateInput
	if f.inputsByID == nil {
		f.inputsByID = map[int64]personal.Sub2APIImportInput{}
	}
	f.inputsByID[id] = input
	if err := f.errorsByID[id]; err != nil {
		return personal.Sub2APIImportResult{}, err
	}
	return personal.Sub2APIImportResult{Status: "reused", ProviderID: fmt.Sprint(id)}, f.err
}

type fakeSub2APIIdentity struct {
	profile       *domainuser.ExternalProfile
	profileWrites int
	*providerLoginRepo
	user       *domainuser.User
	credential domainuser.Credential
	twoFactor  bool
	calls      int
}

func (r *fakeSub2APIIdentity) ResolveSub2APIUser(_ context.Context, _ string, _ int64, input repository.CreateWithCredentialInput) (*domainuser.User, error) {
	r.calls++
	if r.user == nil {
		r.user = input.User
		r.user.ID = 123
		r.credential = input.Credential
	}
	return r.user, nil
}
func (r *fakeSub2APIIdentity) GetCredentialByUserID(context.Context, uint) (*domainuser.Credential, error) {
	return &r.credential, nil
}
func (r *fakeSub2APIIdentity) GetUserTwoFactorByUserID(context.Context, uint) (*domainuser.UserTwoFactor, error) {
	if r.twoFactor {
		return &domainuser.UserTwoFactor{TOTPEnabled: true, TOTPSecretEncrypted: "fake-encrypted-secret"}, nil
	}
	return nil, repository.ErrNotFound
}

func sub2apiFixture(t *testing.T) (*Service, *fakeSub2API, *fakeSub2APIIdentity, *fakeSub2APIImporter) {
	t.Helper()
	groupID := int64(10)
	remote := &fakeSub2API{snapshot: port.Snapshot{User: port.User{ID: 7, Status: "active"}, Keys: []port.Key{{ID: 2, UserID: 7, Key: "fake-api-key-long-enough", Status: "active", GroupID: &groupID, Group: &port.Group{ID: 10, Platform: "openai", Status: "active"}}}}}
	repo := &fakeSub2APIIdentity{providerLoginRepo: &providerLoginRepo{}}
	importer := &fakeSub2APIImporter{}
	s := newTestService(config.Config{Sub2APIEnabled: true, Sub2APIBaseURL: "https://SUB.example.com:443/", PersonalProvidersEnabled: true, JWTSecret: "test-sub2api-jwt"}, repo, nil)
	s.SetSub2API(remote, repo, importer)
	return s, remote, repo, importer
}

func TestSub2APIExchangeCreatesIsolatedAccountAndSession(t *testing.T) {
	s, _, repo, importer := sub2apiFixture(t)
	result, err := s.ExchangeSub2API(context.Background(), 7, "fake-login-token", "test", requestmeta.SessionAuditContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.TwoFactorRequired {
		t.Fatalf("no session: %+v", result)
	}
	if repo.user.Role != "user" || repo.user.Email != "" || repo.user.OnboardingCompletedAt == nil || repo.credential.PasswordEnabled || result.User.InitialSecurityRequired {
		t.Fatal("unsafe external account defaults")
	}
	if importer.input.BaseURL != "https://sub.example.com/v1" || importer.input.Protocol != "openai_responses" {
		t.Fatalf("candidate=%+v", importer.input)
	}
	result2, err := s.ExchangeSub2API(context.Background(), 7, "fake-login-token", "test", requestmeta.SessionAuditContext{})
	if err != nil || result.User.ID != result2.User.ID {
		t.Fatal("identity changed")
	}
}

func TestSub2APIExchangeFailsBeforeProvisioningOnInvalidRemoteIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fakeSub2API)
		want   error
	}{
		{"user mismatch", func(r *fakeSub2API) { r.snapshot.User.ID = 8 }, ErrSub2APIRejected},
		{"inactive", func(r *fakeSub2API) { r.snapshot.User.Status = "disabled" }, ErrSub2APIRejected},
		{"key mismatch", func(r *fakeSub2API) { r.snapshot.Keys[0].UserID = 8 }, ErrSub2APIRejected},
		{"no keys", func(r *fakeSub2API) { r.snapshot.Keys = nil }, ErrSub2APINoKey},
		{"unsupported", func(r *fakeSub2API) { r.snapshot.Keys[0].Group.Platform = "antigravity" }, ErrSub2APIProtocolRequired},
		{"unsafe gateway", func(r *fakeSub2API) { r.snapshot.APIBaseURL = "https://other.example.com" }, ErrSub2APIGateway},
		{"redacted failure", func(r *fakeSub2API) { r.err = errors.New("fake-login-token upstream details") }, ErrSub2APIUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, repo, importer := sub2apiFixture(t)
			tc.change(r)
			_, err := s.ExchangeSub2API(context.Background(), 7, "fake-login-token", "test", requestmeta.SessionAuditContext{})
			if !errors.Is(err, tc.want) || repo.calls != 0 || importer.calls != 0 {
				t.Fatalf("err=%v identity=%d import=%d", err, repo.calls, importer.calls)
			}
		})
	}
}

func TestSub2APIRespectsLocalStatusAndSecondFactor(t *testing.T) {
	for _, status := range []string{"suspended", "locked", "active"} {
		t.Run(status, func(t *testing.T) {
			s, _, repo, importer := sub2apiFixture(t)
			repo.user = &domainuser.User{ID: 123, Username: "existing", Role: "user", Status: status}
			repo.twoFactor = true
			result, err := s.ExchangeSub2API(context.Background(), 7, "fake-login-token", "test", requestmeta.SessionAuditContext{})
			if status != "active" {
				if err == nil || importer.calls != 0 {
					t.Fatal("disabled user reached import")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !result.TwoFactorRequired || result.TwoFactorChallengeToken == "" || result.AccessToken != "" || result.RefreshToken != "" || repo.createSessionCount != 0 || importer.allowCreate {
				t.Fatal("2FA bypassed or mutated account")
			}
		})
	}
}

func TestSub2APIKeyPreparationIsStableAndFiltersExpiredQuotaAndUnsupported(t *testing.T) {
	_, r, _, _ := sub2apiFixture(t)
	valid := r.snapshot.Keys[0]
	expired := valid
	expired.ID = 1
	past := time.Now().Add(-time.Hour)
	expired.ExpiresAt = &past
	exhausted := valid
	exhausted.ID = 3
	exhausted.Quota = 1
	exhausted.QuotaUsed = 1
	unsupported := valid
	unsupported.ID = 4
	group := *valid.Group
	group.Platform = "unsupported"
	unsupported.Group = &group
	summary, candidates, err := prepareSub2APIImport([]port.Key{unsupported, valid, expired, exhausted}, 7, "https://sub.example.com", "", "", sub2APISelection{skips: map[int64]bool{4: true}})
	if err != nil || len(candidates) != 1 || candidates[0].key.ID != 2 || candidates[0].input.Protocol != "openai_responses" {
		t.Fatalf("unexpected candidates or error: %v", err)
	}
	if summary.Skipped != 3 || summary.Items[0].Reason != "expired" || summary.Items[2].Reason != "quota_exhausted" || summary.Items[3].Reason != "user_skipped" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestSub2APIGatewayPathsAndOriginPolicy(t *testing.T) {
	for _, tc := range []struct{ configured, advertised, protocol, want string }{
		{"", "", "openai_responses", "https://sub.example.com/v1"},
		{"", "https://sub.example.com/proxy/v1", "anthropic_messages", "https://sub.example.com/proxy/v1"},
		{"", "https://sub.example.com/proxy/v1", "google_generate_content", "https://sub.example.com/proxy/v1beta"},
		{"https://api.example.com/proxy/v1beta", "https://evil.example.com", "openai_responses", "https://api.example.com/proxy/v1"},
	} {
		got, err := sub2APIGatewayURL("https://sub.example.com", tc.configured, tc.advertised, tc.protocol)
		if err != nil || got != tc.want {
			t.Fatalf("got %q/%v want %q", got, err, tc.want)
		}
	}
	for _, bad := range []string{"https://evil.example.com", "https://127.0.0.1", "http://sub.example.com", "https://sub.example.com?secret=x"} {
		if _, err := sub2APIGatewayURL("https://sub.example.com", "", bad, "openai_responses"); err == nil {
			t.Fatal(bad)
		}
	}
}

func (r *fakeSub2APIIdentity) GetSub2APIProfile(context.Context, uint) (*domainuser.ExternalProfile, error) {
	if r.profile == nil {
		return nil, repository.ErrNotFound
	}
	return r.profile, nil
}
func (r *fakeSub2APIIdentity) SyncSub2APIProfile(_ context.Context, _ uint, _ string, _ int64, profile domainuser.ExternalProfile) error {
	r.profile = &profile
	r.profileWrites++
	return nil
}
