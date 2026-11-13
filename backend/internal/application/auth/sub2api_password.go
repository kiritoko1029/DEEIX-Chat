package auth

import (
	"context"
	"errors"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

type Sub2APIPasswordRateError struct{ RetryAfter time.Duration }

func (*Sub2APIPasswordRateError) Error() string { return "sub2api login rate limited" }
func (*Sub2APIPasswordRateError) Unwrap() error { return ErrSub2APIPasswordRateLimited }

type Sub2APIPasswordCaptcha struct {
	Provider string
	Token    string
}

type Sub2APIPasswordOptions struct {
	Enabled                bool
	PasswordLoginAvailable bool
	BrowserOnly            bool
	UnavailableReason      *string
	UpstreamVersion        string
	RemoteTotpEnabled      bool
	Captcha                struct {
		Provider string
		SiteKey  string
	}
	FallbackURL string
}

type Sub2APIPasswordProgress struct {
	Stage             string
	FlowID            string
	ExpiresAt         time.Time
	MaskedEmail       string
	ProtocolSelection *Sub2APIProtocolSelection
	Login             *LoginResult
}

func (s *Service) Sub2APIPasswordEnabled() bool {
	return s.Sub2APIEnabled() && s.cfg.Snapshot().Sub2APIPasswordLoginEnabled && s.cfg.Snapshot().PersonalProvidersEnabled
}

func (s *Service) passwordConfiguration() (string, string, error) {
	if !s.Sub2APIPasswordEnabled() {
		s.passwordState().invalidateConfiguration("")
		return "", "", ErrSub2APIPasswordDisabled
	}
	cfg := s.cfg.Snapshot()
	instance, err := normalizeSub2APIURL(cfg.Sub2APIBaseURL)
	if err != nil {
		return "", "", ErrSub2APIPasswordUnavailable
	}
	instance = strings.TrimSuffix(instance, "/api/v1")
	configuration := instance + "\n" + cfg.Sub2APIAPIBaseURL
	s.passwordState().invalidateConfiguration(configuration)
	return instance, configuration, nil
}

func (s *Service) passwordClient() (port.PasswordClient, error) {
	client, ok := s.sub2apiClient.(port.PasswordClient)
	if !ok {
		return nil, ErrSub2APIPasswordUnavailable
	}
	return client, nil
}

func (s *Service) passwordSettings(ctx context.Context, instance string) (port.PasswordSettings, error) {
	state := s.passwordState()
	state.mu.Lock()
	if state.settingsInstance == instance && state.now().Before(state.settingsUntil) {
		settings := state.settings
		state.mu.Unlock()
		return settings, nil
	}
	if state.settingsBusy {
		state.mu.Unlock()
		return port.PasswordSettings{}, ErrSub2APIPasswordUnavailable
	}
	state.settingsBusy = true
	state.mu.Unlock()
	defer func() { state.mu.Lock(); state.settingsBusy = false; state.mu.Unlock() }()
	if err := state.allow("settings:"+instance, 10); err != nil {
		return port.PasswordSettings{}, err
	}
	client, err := s.passwordClient()
	if err != nil {
		return port.PasswordSettings{}, err
	}
	settings, err := client.PasswordSettings(ctx, instance)
	if err != nil {
		return port.PasswordSettings{}, s.passwordRemoteError(err)
	}
	state.mu.Lock()
	state.settings = settings
	state.settingsInstance = instance
	state.settingsUntil = state.now().Add(30 * time.Second)
	state.mu.Unlock()
	return settings, nil
}

func (s *Service) Sub2APIPasswordLoginOptions(ctx context.Context) Sub2APIPasswordOptions {
	out := Sub2APIPasswordOptions{BrowserOnly: true}
	out.Captcha.Provider = "none"
	disabled := "disabled"
	out.UnavailableReason = &disabled
	if s != nil && s.cfg != nil {
		if instance, err := normalizeSub2APIURL(s.cfg.Snapshot().Sub2APIBaseURL); err == nil {
			out.FallbackURL = strings.TrimSuffix(instance, "/api/v1")
		}
	}
	instance, _, err := s.passwordConfiguration()
	if err != nil {
		return out
	}
	out.Enabled = true
	reason := "upstream_unavailable"
	out.UnavailableReason = &reason
	settings, err := s.passwordSettings(ctx, instance)
	if err != nil {
		return out
	}
	out.UpstreamVersion = settings.Version
	out.RemoteTotpEnabled = settings.TotpEnabled
	out.Captcha.Provider = settings.CaptchaProvider
	out.Captcha.SiteKey = settings.SiteKey
	if settings.UnavailableReason != "" {
		reason = settings.UnavailableReason
		return out
	}
	out.PasswordLoginAvailable = true
	out.UnavailableReason = nil
	return out
}

func (s *Service) passwordRemoteError(err error) error {
	var remote *port.PasswordError
	if errors.As(err, &remote) {
		switch remote.Kind {
		case "invalid_credentials":
			return ErrSub2APIPasswordCredentials
		case "captcha_failed":
			return ErrSub2APIPasswordCaptcha
		case "two_factor_invalid":
			return ErrSub2APIPasswordTwoFactor
		case "flow_expired":
			return ErrSub2APIPasswordFlowExpired
		case "rate_limited":
			retry := min(max(remote.RetryAfter, time.Second), 15*time.Minute)
			state := s.passwordState()
			state.mu.Lock()
			state.blockedUntil = state.now().Add(retry)
			state.mu.Unlock()
			return &Sub2APIPasswordRateError{RetryAfter: retry}
		}
	}
	if errors.Is(err, port.ErrRejected) {
		return &Sub2APIRejectionError{details: port.RejectionDetailsFrom(err)}
	}
	return ErrSub2APIPasswordUnavailable
}

func validateSub2APIPassword(email, password string, captcha *Sub2APIPasswordCaptcha) error {
	if len(email) == 0 || len(email) > 254 || strings.TrimSpace(email) != email || len(password) == 0 || len(password) > 1024 || !utf8.ValidString(password) {
		return ErrSub2APIPasswordInvalidRequest
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return ErrSub2APIPasswordInvalidRequest
	}
	if captcha != nil && (len(captcha.Token) > 2048 || (captcha.Provider != "none" && captcha.Provider != "turnstile")) {
		return ErrSub2APIPasswordInvalidRequest
	}
	return nil
}

func (s *Service) StartSub2APIPassword(ctx context.Context, email, password string, captcha *Sub2APIPasswordCaptcha, binding PasswordFlowBinding, requestID string) (*Sub2APIPasswordProgress, error) {
	instance, configuration, err := s.passwordConfiguration()
	if err != nil {
		return nil, err
	}
	if err = validateSub2APIPassword(email, password, captcha); err != nil {
		return nil, err
	}
	hash, err := passwordBindingHash(binding)
	if err != nil {
		return nil, err
	}
	state := s.passwordState()
	if err = state.allow("email:"+instance+":"+strings.ToLower(email), 5); err != nil {
		return nil, err
	}
	if err = state.allow("login-ip:"+binding.Audit.ClientIP, 20); err != nil {
		return nil, err
	}
	settings, err := s.passwordSettings(ctx, instance)
	if err != nil {
		return nil, err
	}
	if settings.UnavailableReason != "" {
		return nil, ErrSub2APIPasswordUnavailable
	}
	proof := ""
	switch settings.CaptchaProvider {
	case "none":
		if captcha != nil && (captcha.Provider != "none" || captcha.Token != "") {
			return nil, ErrSub2APIPasswordInvalidRequest
		}
	case "turnstile":
		if captcha == nil || captcha.Provider != "turnstile" || captcha.Token == "" {
			return nil, ErrSub2APIPasswordCaptcha
		}
		proof = captcha.Token
	default:
		return nil, ErrSub2APIPasswordUnavailable
	}
	client, err := s.passwordClient()
	if err != nil {
		return nil, err
	}
	if err = state.allow("outbound:"+instance, 18); err != nil {
		return nil, err
	}
	id, flow, operationCtx, err := state.create(ctx, hash, instance, configuration)
	if err != nil {
		return nil, err
	}
	remote, err := client.PasswordLogin(operationCtx, instance, port.PasswordCredentials{Email: email, Password: password, TurnstileToken: proof})
	password = ""
	if err != nil {
		state.finish(id, flow, false)
		return nil, s.passwordRemoteError(err)
	}
	return s.completeSub2APIPassword(operationCtx, id, flow, remote, binding, requestID)
}

func passwordProgress(id string, flow *sub2APIPasswordFlow) *Sub2APIPasswordProgress {
	return &Sub2APIPasswordProgress{Stage: flow.stage, FlowID: id, ExpiresAt: flow.expiresAt, MaskedEmail: flow.maskedEmail}
}

func (s *Service) completeSub2APIPassword(ctx context.Context, id string, flow *sub2APIPasswordFlow, remote port.LoginProof, binding PasswordFlowBinding, requestID string) (*Sub2APIPasswordProgress, error) {
	state := s.passwordState()
	if ctx.Err() != nil {
		state.finish(id, flow, false)
		return nil, ErrSub2APIPasswordFlowExpired
	}
	_, configuration, err := s.passwordConfiguration()
	if err != nil || configuration != flow.configuration {
		state.finish(id, flow, false)
		return nil, ErrSub2APIPasswordFlowExpired
	}
	if remote.RequiresTwoFactor {
		flow.stage = "remote_2fa"
		flow.tempToken = remote.TempToken
		flow.maskedEmail = remote.MaskedEmail
		progress := passwordProgress(id, flow)
		if !state.finish(id, flow, true) {
			return nil, ErrSub2APIPasswordFlowExpired
		}
		return progress, nil
	}
	flow.tempToken = ""
	flow.accessToken = remote.AccessToken
	flow.remoteID = remote.UserID
	result, err := s.exchangeSub2API(ctx, flow.remoteID, flow.accessToken, requestID, binding.Audit, flow.configuration, flow.choices)
	var selection *Sub2APIProtocolRequiredError
	if errors.As(err, &selection) {
		flow.stage = "protocol_selection"
		progress := passwordProgress(id, flow)
		progress.ProtocolSelection = &selection.Selection
		if !state.finish(id, flow, true) {
			return nil, ErrSub2APIPasswordFlowExpired
		}
		return progress, err
	}
	cancelled := ctx.Err() != nil
	_, currentConfiguration, configErr := s.passwordConfiguration()
	valid := state.finish(id, flow, false)
	if !valid || cancelled || configErr != nil || currentConfiguration != flow.configuration {
		return nil, ErrSub2APIPasswordFlowExpired
	}
	if err != nil {
		var remote *port.PasswordError
		if errors.As(err, &remote) {
			return nil, s.passwordRemoteError(err)
		}
		return nil, err
	}
	stage := "authenticated"
	if result.TwoFactorRequired {
		stage = "local_2fa"
	}
	return &Sub2APIPasswordProgress{Stage: stage, Login: result}, nil
}

func (s *Service) VerifySub2APIPasswordTwoFactor(ctx context.Context, id, code string, binding PasswordFlowBinding, requestID string) (*Sub2APIPasswordProgress, error) {
	if len(code) != 6 {
		return nil, ErrSub2APIPasswordInvalidRequest
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return nil, ErrSub2APIPasswordInvalidRequest
		}
	}
	instance, configuration, err := s.passwordConfiguration()
	if err != nil {
		return nil, err
	}
	hash, err := passwordBindingHash(binding)
	if err != nil {
		return nil, ErrSub2APIPasswordFlowExpired
	}
	state := s.passwordState()
	flow, operationCtx, err := state.acquire(ctx, id, hash, configuration, "remote_2fa")
	if err != nil {
		return nil, err
	}
	client, err := s.passwordClient()
	if err != nil {
		state.finish(id, flow, false)
		return nil, err
	}
	if err = state.allow("outbound:"+instance, 18); err != nil {
		state.finish(id, flow, false)
		return nil, err
	}
	flow.attempts++
	remote, err := client.PasswordTwoFactor(operationCtx, instance, flow.tempToken, code)
	if err != nil {
		err = s.passwordRemoteError(err)
		if errors.Is(err, ErrSub2APIPasswordTwoFactor) && flow.attempts < 5 {
			progress := passwordProgress(id, flow)
			if !state.finish(id, flow, true) {
				return nil, ErrSub2APIPasswordFlowExpired
			}
			return progress, err
		}
		state.finish(id, flow, false)
		if errors.Is(err, ErrSub2APIPasswordTwoFactor) {
			return nil, &Sub2APIPasswordRateError{RetryAfter: 15 * time.Minute}
		}
		return nil, err
	}
	return s.completeSub2APIPassword(operationCtx, id, flow, remote, binding, requestID)
}

func mergePasswordChoices(old, incoming Sub2APIExchangeOptions) (Sub2APIExchangeOptions, error) {
	selection, err := sub2APIOptions([]Sub2APIExchangeOptions{incoming})
	if err != nil {
		return Sub2APIExchangeOptions{}, ErrSub2APIPasswordInvalidRequest
	}
	previous, err := sub2APIOptions([]Sub2APIExchangeOptions{old})
	if err != nil {
		return Sub2APIExchangeOptions{}, ErrSub2APIPasswordInvalidRequest
	}
	for id, protocol := range selection.protocols {
		previous.protocols[id] = protocol
		delete(previous.skips, id)
	}
	for id := range selection.skips {
		previous.skips[id] = true
		delete(previous.protocols, id)
	}
	if len(previous.protocols)+len(previous.skips) > 2000 {
		return Sub2APIExchangeOptions{}, ErrSub2APIPasswordInvalidRequest
	}
	result := Sub2APIExchangeOptions{}
	for id, protocol := range previous.protocols {
		result.ProtocolSelections = append(result.ProtocolSelections, Sub2APIProtocolChoice{KeyID: id, Protocol: protocol})
	}
	for id := range previous.skips {
		result.SkipKeyIDs = append(result.SkipKeyIDs, id)
	}
	sort.Slice(result.ProtocolSelections, func(i, j int) bool { return result.ProtocolSelections[i].KeyID < result.ProtocolSelections[j].KeyID })
	sort.Slice(result.SkipKeyIDs, func(i, j int) bool { return result.SkipKeyIDs[i] < result.SkipKeyIDs[j] })
	return result, nil
}

func (s *Service) ContinueSub2APIPassword(ctx context.Context, id string, choices Sub2APIExchangeOptions, binding PasswordFlowBinding, requestID string) (*Sub2APIPasswordProgress, error) {
	instance, configuration, err := s.passwordConfiguration()
	if err != nil {
		return nil, err
	}
	hash, err := passwordBindingHash(binding)
	if err != nil {
		return nil, ErrSub2APIPasswordFlowExpired
	}
	state := s.passwordState()
	flow, operationCtx, err := state.acquire(ctx, id, hash, configuration, "protocol_selection")
	if err != nil {
		return nil, err
	}
	merged, err := mergePasswordChoices(flow.choices, choices)
	if err != nil {
		state.finish(id, flow, true)
		return nil, err
	}
	if err = state.allow("outbound:"+instance, 18); err != nil {
		state.finish(id, flow, false)
		return nil, err
	}
	flow.choices = merged
	return s.completeSub2APIPassword(operationCtx, id, flow, port.LoginProof{AccessToken: flow.accessToken, UserID: flow.remoteID}, binding, requestID)
}
