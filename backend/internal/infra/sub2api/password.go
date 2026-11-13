package sub2api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

const integrationUserAgent = "DEEIX-Chat-Sub2API/1.0"
const passwordResponseLimit = 256 << 10

func passwordEndpoint(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", port.ErrUnavailable
	}
	if security.ValidateOutboundHTTPURL(base, security.NewPublicOnlyOutboundPolicy()) != nil {
		return "", port.ErrUnavailable
	}
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/api/v1") + "/api/v1/" + path, nil
}

func (c *Client) passwordRequest(ctx context.Context, base, path string, input any, output any) error {
	endpoint, err := passwordEndpoint(base, path)
	if err != nil {
		return err
	}
	method := http.MethodGet
	var payload []byte
	if input != nil {
		method = http.MethodPost
		payload, err = json.Marshal(input)
		if err != nil {
			return port.ErrUnavailable
		}
		defer clear(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return port.ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", integrationUserAgent)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return port.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return passwordRateLimitError(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, passwordResponseLimit+1))
	if err != nil || len(raw) > passwordResponseLimit {
		return port.ErrUnavailable
	}
	defer clear(raw)
	var envelope struct {
		Code   *int            `json:"code"`
		Reason string          `json:"reason"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return port.ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK || envelope.Code == nil || *envelope.Code != 0 {
		switch envelope.Reason {
		case "INVALID_CREDENTIALS":
			return &port.PasswordError{Kind: "invalid_credentials"}
		case "TURNSTILE_VERIFICATION_FAILED":
			return &port.PasswordError{Kind: "captcha_failed"}
		case "TOTP_INVALID_CODE":
			return &port.PasswordError{Kind: "two_factor_invalid"}
		case "TOTP_TOO_MANY_ATTEMPTS":
			return &port.PasswordError{Kind: "rate_limited", RetryAfter: 15 * time.Minute}
		case "SESSION_BINDING_MISMATCH":
			return port.NewRejected(port.StageAuthMe, port.ReasonSessionBindingMismatch)
		case "USER_NOT_ACTIVE", "USER_INACTIVE":
			return port.NewRejected(port.StageIdentity, port.ReasonUserInactive)
		}
		if path == "auth/login/2fa" && resp.StatusCode == http.StatusBadRequest {
			return &port.PasswordError{Kind: "flow_expired"}
		}
		return port.ErrUnavailable
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, output) != nil {
		return port.ErrUnavailable
	}
	return nil
}

func (c *Client) PasswordSettings(ctx context.Context, base string) (port.PasswordSettings, error) {
	var data struct {
		Version     string `json:"version"`
		Totp        *bool  `json:"totp_enabled"`
		Turnstile   *bool  `json:"turnstile_enabled"`
		SiteKey     string `json:"turnstile_site_key"`
		Tencent     *bool  `json:"tencent_captcha_enabled"`
		Aliyun      *bool  `json:"aliyun_captcha_enabled"`
		Agreement   *bool  `json:"login_agreement_enabled"`
		BackendMode *bool  `json:"backend_mode_enabled"`
	}
	if err := c.passwordRequest(ctx, base, "settings/public", nil, &data); err != nil {
		return port.PasswordSettings{}, err
	}
	out := port.PasswordSettings{Version: data.Version, CaptchaProvider: "none"}
	if data.Version == "" || len(data.Version) > 64 || data.Totp == nil || data.Turnstile == nil || data.Tencent == nil || data.Aliyun == nil || data.Agreement == nil || data.BackendMode == nil {
		out.UnavailableReason = "settings_unknown"
		return out, nil
	}
	out.TotpEnabled = *data.Totp
	switch {
	case *data.Agreement:
		out.UnavailableReason = "login_agreement_required"
	case *data.BackendMode:
		out.UnavailableReason = "backend_mode"
	case *data.Tencent || *data.Aliyun:
		out.UnavailableReason = "captcha_unsupported"
	case *data.Turnstile:
		out.CaptchaProvider = "turnstile"
		out.SiteKey = data.SiteKey
		if len(data.SiteKey) == 0 || len(data.SiteKey) > 256 || strings.TrimSpace(data.SiteKey) != data.SiteKey {
			out.UnavailableReason = "captcha_not_configured"
		}
	}
	return out, nil
}

type passwordResponse struct {
	RequiresTwoFactor bool      `json:"requires_2fa"`
	TempToken         string    `json:"temp_token"`
	MaskedEmail       string    `json:"user_email_masked"`
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	TokenType         string    `json:"token_type"`
	User              port.User `json:"user"`
}

func validRemoteCredential(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit {
		return false
	}
	for _, ch := range value {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}

func (c *Client) passwordProof(ctx context.Context, base, path string, input any) (port.LoginProof, error) {
	var data passwordResponse
	requestErr := c.passwordRequest(ctx, base, path, input, &data)
	// Cleanup has a separate, tightly bounded lifetime even if the browser cancels.
	// Logout is best effort: upstream intentionally hides Redis deletion errors.
	if validRemoteCredential(data.RefreshToken, 4096) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		var ignored struct {
			Message string `json:"message"`
		}
		cleanupErr := c.passwordRequest(cleanupCtx, base, "auth/logout", struct {
			RefreshToken string `json:"refresh_token"`
		}{data.RefreshToken}, &ignored)
		cancel()
		var rateError *port.PasswordError
		if errors.As(cleanupErr, &rateError) && rateError.Kind == "rate_limited" {
			requestErr = cleanupErr
		}
	}
	data.RefreshToken = ""
	if requestErr != nil {
		return port.LoginProof{}, requestErr
	}
	if data.RequiresTwoFactor {
		if path != "auth/login" || !validRemoteCredential(data.TempToken, 4096) || data.AccessToken != "" || len(data.MaskedEmail) > 512 {
			return port.LoginProof{}, port.ErrUnavailable
		}
		return port.LoginProof{RequiresTwoFactor: true, TempToken: data.TempToken, MaskedEmail: data.MaskedEmail}, nil
	}
	if !validRemoteCredential(data.AccessToken, 16384) || data.TempToken != "" || data.TokenType != "Bearer" || data.User.ID <= 0 || data.User.ID > 9007199254740991 || data.User.Status != "active" {
		return port.LoginProof{}, port.ErrUnavailable
	}
	return port.LoginProof{AccessToken: data.AccessToken, UserID: data.User.ID}, nil
}

func (c *Client) PasswordLogin(ctx context.Context, base string, input port.PasswordCredentials) (port.LoginProof, error) {
	return c.passwordProof(ctx, base, "auth/login", input)
}
func (c *Client) PasswordTwoFactor(ctx context.Context, base, tempToken, code string) (port.LoginProof, error) {
	input := struct {
		TempToken string `json:"temp_token"`
		Code      string `json:"totp_code"`
	}{tempToken, code}
	return c.passwordProof(ctx, base, "auth/login/2fa", input)
}

func passwordRateLimitError(resp *http.Response) error {
	retry := time.Minute
	value := resp.Header.Get("Retry-After")
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		retry = time.Duration(min(seconds, 900)) * time.Second
	} else if deadline, err := http.ParseTime(value); err == nil {
		retry = min(max(time.Until(deadline), time.Second), 15*time.Minute)
	}
	return &port.PasswordError{Kind: "rate_limited", RetryAfter: retry}
}
