package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

type passwordRemote struct {
	*fakeSub2API
	settings                            port.PasswordSettings
	proof                               port.LoginProof
	loginError                          error
	otpError                            error
	loginCalls, otpCalls, settingsCalls int
	entered                             chan struct{}
	release                             chan struct{}
}

func (r *passwordRemote) PasswordSettings(context.Context, string) (port.PasswordSettings, error) {
	r.settingsCalls++
	return r.settings, nil
}
func (r *passwordRemote) PasswordLogin(_ context.Context, _ string, input port.PasswordCredentials) (port.LoginProof, error) {
	r.loginCalls++
	return r.proof, r.loginError
}
func (r *passwordRemote) PasswordTwoFactor(context.Context, string, string, string) (port.LoginProof, error) {
	r.otpCalls++
	if r.entered != nil {
		close(r.entered)
		<-r.release
	}
	return port.LoginProof{AccessToken: "synthetic-access", UserID: 7}, r.otpError
}
func passwordFixture(t *testing.T) (*Service, *passwordRemote, *fakeSub2APIIdentity, *fakeSub2APIImporter, PasswordFlowBinding) {
	t.Helper()
	s, r, repo, importer := sub2apiFixture(t)
	cfg := s.cfg.Snapshot()
	cfg.Sub2APIPasswordLoginEnabled = true
	s.cfg.Store(cfg)
	remote := &passwordRemote{fakeSub2API: r, settings: port.PasswordSettings{Version: "0.2.21", CaptchaProvider: "none"}, proof: port.LoginProof{AccessToken: "synthetic-access", UserID: 7}}
	s.SetSub2API(remote, repo, importer)
	t.Cleanup(func() { s.passwordState().invalidateConfiguration("") })
	binding := PasswordFlowBinding{Cookie: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Audit: requestmeta.SessionAuditContext{ClientIP: "203.0.113.10", UserAgent: "browser"}}
	return s, remote, repo, importer, binding
}
func passwordStart(t *testing.T, s *Service, binding PasswordFlowBinding) *Sub2APIPasswordProgress {
	t.Helper()
	p, err := s.StartSub2APIPassword(context.Background(), "same@example.test", " 密码 ", nil, binding, "test")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPasswordLoginUsesImmutableIdentityAndNeverClaimsEmail(t *testing.T) {
	s, r, repo, _, binding := passwordFixture(t)
	repo.usersByEmail = map[string]*domainuser.User{"same@example.test": {ID: 99, Role: "admin"}}
	existing, err := s.ExchangeSub2API(context.Background(), 7, "existing-jwt", "test", binding.Audit)
	if err != nil {
		t.Fatal(err)
	}
	p := passwordStart(t, s, binding)
	if p.Stage != "authenticated" || p.Login.User.ID != existing.User.ID || p.Login.User.ID == 99 || repo.user.Role != "user" || repo.credential.PasswordEnabled {
		t.Fatal("identity or role boundary violated")
	}
	r.loginError = &port.PasswordError{Kind: "invalid_credentials"}
	if _, err = s.StartSub2APIPassword(context.Background(), "same@example.test", "old-password", nil, binding, "test"); !errors.Is(err, ErrSub2APIPasswordCredentials) {
		t.Fatal(err)
	}
	if len(s.passwordState().flows) != 0 {
		t.Fatal("terminal credentials retained")
	}
}

func TestPasswordRemoteAndLocalTwoFactorRemainIndependent(t *testing.T) {
	s, r, repo, importer, binding := passwordFixture(t)
	r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "synthetic-temp", MaskedEmail: "a***"}
	p := passwordStart(t, s, binding)
	if p.Stage != "remote_2fa" || repo.calls != 0 || importer.calls != 0 || r.calls != 0 {
		t.Fatal("provisioned before remote 2fa")
	}
	repo.twoFactor = true
	result, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test")
	if err != nil || result.Stage != "local_2fa" || !result.Login.TwoFactorRequired || result.Login.AccessToken != "" || importer.allowCreate {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err = s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test"); !errors.Is(err, ErrSub2APIPasswordFlowExpired) {
		t.Fatal("replayed remote 2fa")
	}
}

func TestPasswordTwoFactorBindingStageAndAttempts(t *testing.T) {
	for _, change := range []string{"cookie", "IP", "UA", "instance", "wrong-stage", "expiry", "restart"} {
		t.Run(change, func(t *testing.T) {
			s, r, _, _, binding := passwordFixture(t)
			r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "temp"}
			p := passwordStart(t, s, binding)
			switch change {
			case "cookie":
				binding.Cookie = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
			case "IP":
				binding.Audit.ClientIP = "203.0.113.11"
			case "UA":
				binding.Audit.UserAgent = "other"
			case "instance":
				cfg := s.cfg.Snapshot()
				cfg.Sub2APIBaseURL = "https://other.example.com"
				s.cfg.Store(cfg)
			case "expiry":
				s.passwordState().now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			case "restart":
				s.sub2apiPassword.flows = make(map[[32]byte]*sub2APIPasswordFlow)
			case "wrong-stage":
				_, err := s.ContinueSub2APIPassword(context.Background(), p.FlowID, Sub2APIExchangeOptions{}, binding, "test")
				if !errors.Is(err, ErrSub2APIPasswordInvalidRequest) || r.otpCalls != 0 {
					t.Fatal("stage bypass")
				}
				return
			}
			if _, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test"); !errors.Is(err, ErrSub2APIPasswordFlowExpired) || r.otpCalls != 0 {
				t.Fatalf("binding bypass %v", err)
			}
		})
	}
	s, r, _, _, binding := passwordFixture(t)
	r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "temp"}
	r.otpError = &port.PasswordError{Kind: "two_factor_invalid"}
	p := passwordStart(t, s, binding)
	for n := 1; n <= 5; n++ {
		retry, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test")
		if n < 5 && (!errors.Is(err, ErrSub2APIPasswordTwoFactor) || retry == nil || !retry.ExpiresAt.Equal(p.ExpiresAt)) {
			t.Fatal("retry contract")
		}
		if n == 5 && (!errors.Is(err, ErrSub2APIPasswordRateLimited) || retry != nil) {
			t.Fatal("attempt cap")
		}
	}
	if _, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test"); !errors.Is(err, ErrSub2APIPasswordFlowExpired) || r.otpCalls != 5 {
		t.Fatal("attempt cap bypass")
	}
}

func TestPasswordCancellationAndConcurrency(t *testing.T) {
	s, r, repo, importer, binding := passwordFixture(t)
	r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "temp"}
	p := passwordStart(t, s, binding)
	r.entered = make(chan struct{})
	r.release = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		progress, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test")
		if progress != nil {
			result <- errors.New("cancelled flow returned credentials")
			return
		}
		result <- err
	}()
	<-r.entered
	if _, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test"); !errors.Is(err, ErrSub2APIPasswordFlowBusy) {
		t.Fatal(err)
	}
	s.CancelSub2APIPasswordFlow(p.FlowID, binding)
	close(r.release)
	if err := <-result; !errors.Is(err, ErrSub2APIPasswordFlowExpired) {
		t.Fatal(err)
	}
	if r.otpCalls != 1 || repo.calls != 0 || importer.calls != 0 {
		t.Fatal("cancelled authentication wrote local identity")
	}
}

func TestPasswordProtocolSelectionAccumulates(t *testing.T) {
	s, r, _, _, binding := passwordFixture(t)
	r.snapshot.Keys[0].Group.Platform = "unknown"
	second := r.snapshot.Keys[0]
	second.ID = 3
	r.snapshot.Keys = append(r.snapshot.Keys, second)
	p, err := s.StartSub2APIPassword(context.Background(), "same@example.test", "pwd", nil, binding, "test")
	if !errors.Is(err, ErrSub2APIProtocolRequired) || p.Stage != "protocol_selection" {
		t.Fatal(err)
	}
	p, err = s.ContinueSub2APIPassword(context.Background(), p.FlowID, Sub2APIExchangeOptions{ProtocolSelections: []Sub2APIProtocolChoice{{KeyID: 2, Protocol: "openai_responses"}}}, binding, "test")
	if !errors.Is(err, ErrSub2APIProtocolRequired) || len(p.ProtocolSelection.Items) != 1 {
		t.Fatal("partial selection lost")
	}
	result, err := s.ContinueSub2APIPassword(context.Background(), p.FlowID, Sub2APIExchangeOptions{ProtocolSelections: []Sub2APIProtocolChoice{{KeyID: 2, Protocol: "openai_responses"}, {KeyID: 3, Protocol: "openai_chat_completions"}}}, binding, "test")
	if err != nil || result.Stage != "authenticated" || r.loginCalls != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestPasswordCapabilitiesDefaultDisabledCacheAndLimits(t *testing.T) {
	s, r, _, _, binding := passwordFixture(t)
	cfg := s.cfg.Snapshot()
	cfg.Sub2APIPasswordLoginEnabled = false
	s.cfg.Store(cfg)
	if options := s.Sub2APIPasswordLoginOptions(context.Background()); options.Enabled || options.PasswordLoginAvailable || r.settingsCalls != 0 {
		t.Fatal("disabled fetched upstream")
	}
	cfg.Sub2APIPasswordLoginEnabled = true
	s.cfg.Store(cfg)
	for range 2 {
		if !s.Sub2APIPasswordLoginOptions(context.Background()).PasswordLoginAvailable {
			t.Fatal("unavailable")
		}
	}
	if r.settingsCalls != 1 {
		t.Fatal("settings not cached")
	}
	s.passwordState().capacity = 0
	if _, err := s.StartSub2APIPassword(context.Background(), "same@example.test", "pwd", nil, binding, "test"); !errors.Is(err, ErrSub2APIPasswordUnavailable) || r.loginCalls != 0 {
		t.Fatal("capacity ignored")
	}
	s.passwordState().capacity = 10
	r.loginError = &port.PasswordError{Kind: "rate_limited", RetryAfter: time.Minute}
	_, err := s.StartSub2APIPassword(context.Background(), "same@example.test", "pwd", nil, binding, "test")
	if !errors.Is(err, ErrSub2APIPasswordRateLimited) {
		t.Fatal(err)
	}
	_, err = s.StartSub2APIPassword(context.Background(), "different@example.test", "pwd", nil, binding, "test")
	if !errors.Is(err, ErrSub2APIPasswordRateLimited) || r.loginCalls != 1 {
		t.Fatal("upstream backoff ignored")
	}
}

func TestPasswordValidationAndCaptchaBeforeRemoteCall(t *testing.T) {
	s, r, _, _, binding := passwordFixture(t)
	r.settings.CaptchaProvider = "turnstile"
	r.settings.SiteKey = "public"
	if _, err := s.StartSub2APIPassword(context.Background(), "same@example.test", "pwd", nil, binding, "test"); !errors.Is(err, ErrSub2APIPasswordCaptcha) || r.loginCalls != 0 {
		t.Fatal("captcha bypass")
	}
	for _, password := range []string{"", strings.Repeat("x", 1025)} {
		if _, err := s.StartSub2APIPassword(context.Background(), "same@example.test", password, nil, binding, "test"); !errors.Is(err, ErrSub2APIPasswordInvalidRequest) {
			t.Fatal("password bound")
		}
	}
}

func TestPasswordFlowTimersAndBoundedRates(t *testing.T) {
	s, r, _, _, binding := passwordFixture(t)
	r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "synthetic-temp"}
	p := passwordStart(t, s, binding)
	state := s.passwordState()
	state.mu.Lock()
	for _, flow := range state.flows {
		flow.timer.Reset(time.Millisecond)
	}
	state.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		state.mu.Lock()
		remaining := len(state.flows)
		state.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expiry requires another incoming request")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.VerifySub2APIPasswordTwoFactor(context.Background(), p.FlowID, "123456", binding, "test"); !errors.Is(err, ErrSub2APIPasswordFlowExpired) {
		t.Fatal("timer expiry not enforced")
	}
	state.mu.Lock()
	for len(state.rates) < sub2APIPasswordRateCapacity {
		key := [32]byte{}
		key[0] = byte(len(state.rates))
		key[1] = byte(len(state.rates) >> 8)
		state.rates[key] = passwordRateBucket{expiresAt: time.Now().Add(time.Minute)}
	}
	state.mu.Unlock()
	if err := state.allow("new-email-scope", 5); !errors.Is(err, ErrSub2APIPasswordUnavailable) {
		t.Fatal("rate store capacity ignored")
	}
}

func TestPasswordForeignCancelCannotDestroyFlowAndConfigInvalidatesAll(t *testing.T) {
	s, r, _, _, binding := passwordFixture(t)
	r.proof = port.LoginProof{RequiresTwoFactor: true, TempToken: "temp"}
	p := passwordStart(t, s, binding)
	foreign := binding
	foreign.Audit.ClientIP = "203.0.113.55"
	s.CancelSub2APIPasswordFlow(p.FlowID, foreign)
	if len(s.passwordState().flows) != 1 {
		t.Fatal("foreign cancellation removed flow")
	}
	cfg := s.cfg.Snapshot()
	cfg.Sub2APIBaseURL = "https://other.example.com"
	s.cfg.Store(cfg)
	_ = s.Sub2APIPasswordLoginOptions(context.Background())
	if len(s.passwordState().flows) != 0 {
		t.Fatal("trust change retained remote credentials")
	}
}

func TestPasswordRateLimitsEmailAndInstance(t *testing.T) {
	s, _, _, _, _ := passwordFixture(t)
	state := s.passwordState()
	for range 5 {
		if err := state.allow("email:normalized", 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.allow("email:normalized", 5); !errors.Is(err, ErrSub2APIPasswordRateLimited) {
		t.Fatal("email limit absent")
	}
	for range 18 {
		if err := state.allow("outbound:instance", 18); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.allow("outbound:instance", 18); !errors.Is(err, ErrSub2APIPasswordRateLimited) {
		t.Fatal("instance limit absent")
	}
	state.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := state.allow("email:normalized", 5); err != nil {
		t.Fatal("expired counters not pruned")
	}
}

// This calls the bound Exchange after the outer flow precheck has succeeded.
// It covers the gap between that precheck and Exchange's own configuration read.
func TestPasswordExchangeRejectsChangedSnapshotBeforeSendingCredentials(t *testing.T) {
	for _, change := range []string{"management-host", "gateway-only"} {
		t.Run(change, func(t *testing.T) {
			s, remote, repo, importer, binding := passwordFixture(t)
			_, expected, err := s.passwordConfiguration()
			if err != nil {
				t.Fatal(err)
			}
			cfg := s.cfg.Snapshot()
			if change == "management-host" {
				cfg.Sub2APIBaseURL = "https://new-source.example.com"
			} else {
				cfg.Sub2APIAPIBaseURL = "https://new-gateway.example.com/v1"
			}
			s.cfg.Store(cfg)
			result, err := s.exchangeSub2API(context.Background(), 7, "synthetic-old-source-token", "test", binding.Audit, expected)
			if !errors.Is(err, ErrSub2APIPasswordFlowExpired) || result != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if remote.calls != 0 || repo.calls != 0 || importer.calls != 0 {
				t.Fatalf("credentials crossed configuration boundary: fetch=%d identity=%d import=%d", remote.calls, repo.calls, importer.calls)
			}
		})
	}
}

type passwordSnapshotRemote struct {
	*passwordRemote
	service  *Service
	seenBase string
}

func (r *passwordSnapshotRemote) Fetch(ctx context.Context, base, token string, userID int64, readSettings bool) (port.Snapshot, error) {
	r.seenBase = base
	// Once Exchange has selected its snapshot, later changes must not redirect
	// keys to a different gateway while processing the old source's response.
	cfg := r.service.cfg.Snapshot()
	cfg.Sub2APIBaseURL = "https://later-source.example.com"
	cfg.Sub2APIAPIBaseURL = "https://later-gateway.example.com/v1"
	r.service.cfg.Store(cfg)
	return r.passwordRemote.Fetch(ctx, base, token, userID, readSettings)
}

func TestPasswordExchangeUsesOneSnapshotForIdentityAndGateway(t *testing.T) {
	s, remote, repo, importer, binding := passwordFixture(t)
	cfg := s.cfg.Snapshot()
	cfg.Sub2APIAPIBaseURL = "https://original-gateway.example.com/v1"
	s.cfg.Store(cfg)
	instance, expected, err := s.passwordConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	switched := &passwordSnapshotRemote{passwordRemote: remote, service: s}
	s.SetSub2API(switched, repo, importer)
	result, err := s.exchangeSub2API(context.Background(), 7, "synthetic-old-source-token", "test", binding.Audit, expected)
	if err != nil || result == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if switched.seenBase != instance || importer.input.BaseURL != "https://original-gateway.example.com/v1" {
		t.Fatalf("snapshot mixed: fetch=%s gateway=%s", switched.seenBase, importer.input.BaseURL)
	}
}

func TestPasswordOptionsUseConfiguredInstanceAndNoneCapabilities(t *testing.T) {
	s, _, _, _, binding := passwordFixture(t)
	cfg := s.cfg.Snapshot()
	cfg.Sub2APIBaseURL = "https://sub.vme5.de"
	s.cfg.Store(cfg)
	options := s.Sub2APIPasswordLoginOptions(context.Background())
	if !options.PasswordLoginAvailable || options.FallbackURL != "https://sub.vme5.de" || options.Captcha.Provider != "none" || options.RemoteTotpEnabled {
		t.Fatalf("dynamic options=%+v", options)
	}
	result := passwordStart(t, s, binding)
	if result.Stage != "authenticated" {
		t.Fatalf("none capability login stage=%s", result.Stage)
	}
}
