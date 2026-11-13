package sub2api

import (
	"context"
	"time"
)

// PasswordClient authenticates only against the operator-configured instance.
// Implementations must discard credentials and best-effort revoke newly issued
// refresh tokens before returning. Tokens in LoginProof are short-lived inputs.
type PasswordClient interface {
	PasswordSettings(context.Context, string) (PasswordSettings, error)
	PasswordLogin(context.Context, string, PasswordCredentials) (LoginProof, error)
	PasswordTwoFactor(context.Context, string, string, string) (LoginProof, error)
}

type PasswordSettings struct {
	Version           string
	TotpEnabled       bool
	CaptchaProvider   string
	SiteKey           string
	UnavailableReason string
}

type PasswordCredentials struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	TurnstileToken string `json:"turnstile_token,omitempty"`
}

type LoginProof struct {
	RequiresTwoFactor bool
	TempToken         string
	MaskedEmail       string
	AccessToken       string
	UserID            int64
}

// PasswordError contains only fixed classifications, never upstream text.
type PasswordError struct {
	Kind       string
	RetryAfter time.Duration
}

func (e *PasswordError) Error() string { return "sub2api password authentication: " + e.Kind }
