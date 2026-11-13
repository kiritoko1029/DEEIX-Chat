package sub2api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

const passwordSettingsJSON = `{"version":"0.2.21","totp_enabled":true,"turnstile_enabled":false,"turnstile_site_key":"","tencent_captcha_enabled":false,"aliyun_captcha_enabled":false,"login_agreement_enabled":false,"backend_mode_enabled":false}`
const passwordSuccessJSON = `{"code":0,"data":{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","token_type":"Bearer","user":{"id":7,"status":"active","role":"admin","email":"same@example.test"}}}`

func TestPasswordSettingsFailClosed(t *testing.T) {
	for _, tc := range []struct{ name, body, provider, reason string }{
		{"none", passwordSettingsJSON, "none", ""},
		{"turnstile", strings.Replace(strings.Replace(passwordSettingsJSON, `"turnstile_enabled":false`, `"turnstile_enabled":true`, 1), `"turnstile_site_key":""`, `"turnstile_site_key":"public-site-key"`, 1), "turnstile", ""},
		{"missing key", strings.Replace(passwordSettingsJSON, `"turnstile_enabled":false`, `"turnstile_enabled":true`, 1), "turnstile", "captcha_not_configured"},
		{"tencent", strings.Replace(passwordSettingsJSON, `"tencent_captcha_enabled":false`, `"tencent_captcha_enabled":true`, 1), "none", "captcha_unsupported"},
		{"aliyun", strings.Replace(passwordSettingsJSON, `"aliyun_captcha_enabled":false`, `"aliyun_captcha_enabled":true`, 1), "none", "captcha_unsupported"},
		{"agreement", strings.Replace(passwordSettingsJSON, `"login_agreement_enabled":false`, `"login_agreement_enabled":true`, 1), "none", "login_agreement_required"},
		{"unknown", `{"version":"0.2.21"}`, "none", "settings_unknown"},
		{"backend", strings.Replace(passwordSettingsJSON, `"backend_mode_enabled":false`, `"backend_mode_enabled":true`, 1), "none", "backend_mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return fakeResponse(r, 200, `{"code":0,"data":`+tc.body+`}`), nil
			})
			got, err := c.PasswordSettings(context.Background(), "https://sub.example.com")
			if err != nil || got.CaptchaProvider != tc.provider || got.UnavailableReason != tc.reason {
				t.Fatalf("settings=%+v err=%v", got, err)
			}
		})
	}
}

func TestPasswordProofPreservesBytesCleansRefreshAndUsesConsistentUA(t *testing.T) {
	c := New()
	var paths []string
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("User-Agent") != integrationUserAgent || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("unsafe outbound identity")
		}
		var body map[string]string
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		switch r.URL.Path {
		case "/api/v1/auth/login":
			if body["password"] != " 密码 with spaces \t" || body["turnstile_token"] != "proof" {
				t.Fatal("credential bytes changed")
			}
			return fakeResponse(r, 200, passwordSuccessJSON), nil
		case "/api/v1/auth/logout":
			if body["refresh_token"] != "synthetic-refresh" || len(body) != 1 || r.Header.Get("Authorization") != "" {
				t.Fatal("incorrect refresh cleanup")
			}
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 2*time.Second {
				t.Fatal("unbounded cleanup")
			}
			return fakeResponse(r, 500, `{"code":500,"message":"synthetic-private"}`), nil
		case "/api/v1/auth/me":
			return fakeResponse(r, 200, `{"code":0,"data":{"id":7,"status":"active"}}`), nil
		case "/api/v1/keys":
			return fakeResponse(r, 200, `{"code":0,"data":{"items":[],"total":0,"page":1,"pages":1,"page_size":100}}`), nil
		default:
			t.Fatal("unexpected endpoint")
			return nil, nil
		}
	})
	proof, err := c.PasswordLogin(context.Background(), "https://sub.example.com", port.PasswordCredentials{Email: "a@example.test", Password: " 密码 with spaces \t", TurnstileToken: "proof"})
	if err != nil || proof.AccessToken != "synthetic-access" || proof.UserID != 7 {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
	if _, err = c.Fetch(context.Background(), "https://sub.example.com", proof.AccessToken, proof.UserID, false); err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/api/v1/auth/login,/api/v1/auth/logout,/api/v1/auth/me,/api/v1/keys" {
		t.Fatal(paths)
	}
}

func TestPasswordRemoteErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
	}{
		{"bad password", `{"code":401,"reason":"INVALID_CREDENTIALS","message":"synthetic-private"}`, "invalid_credentials", 401},
		{"captcha", `{"code":400,"reason":"TURNSTILE_VERIFICATION_FAILED"}`, "captcha_failed", 400},
		{"forced captcha missing", `{"code":503,"reason":"TURNSTILE_NOT_CONFIGURED"}`, "", 503},
		{"conflict", `{"code":503,"reason":"CAPTCHA_PROVIDER_CONFLICT"}`, "", 503},
		{"rate", `{"error":"rate limit exceeded"}`, "rate_limited", 429},
		{"redirect", `{"code":0,"data":{}}`, "", 307},
		{"oversized", strings.Repeat("x", passwordResponseLimit+1), "", 200},
		{"contradictory", `{"code":0,"data":{"requires_2fa":true,"temp_token":"temp","access_token":"access"}}`, "", 200},
		{"malformed", `{"code":0,"data":`, "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := New()
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := fakeResponse(r, tc.status, tc.body)
				resp.Header.Set("Retry-After", "999999999")
				resp.Header.Set("Location", "https://evil.example.com")
				return resp, nil
			})
			_, err := c.PasswordLogin(context.Background(), "https://sub.example.com", port.PasswordCredentials{Email: "a@example.test", Password: "synthetic-private"})
			if err == nil || strings.Contains(err.Error(), "synthetic-private") || calls != 1 {
				t.Fatalf("unsafe error=%v calls=%d", err, calls)
			}
			var remote *port.PasswordError
			if tc.kind != "" && (!errors.As(err, &remote) || remote.Kind != tc.kind) {
				t.Fatalf("error=%v", err)
			}
			if tc.kind == "rate_limited" && remote.RetryAfter != 15*time.Minute {
				t.Fatal("retry unbounded")
			}
		})
	}
}

func TestPasswordTwoFactorAndAccessOnly(t *testing.T) {
	c := New()
	calls := 0
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(r.URL.Path, "/login") {
			return fakeResponse(r, 200, `{"code":0,"data":{"requires_2fa":true,"temp_token":"synthetic-temp","user_email_masked":"a***@example.test"}}`), nil
		}
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path != "/api/v1/auth/login/2fa" || len(payload) != 2 || payload["temp_token"] != "synthetic-temp" || payload["totp_code"] != "123456" {
			t.Fatal("incorrect 2fa request")
		}
		return fakeResponse(r, 200, strings.Replace(passwordSuccessJSON, `"refresh_token":"synthetic-refresh",`, "", 1)), nil
	})
	proof, err := c.PasswordLogin(context.Background(), "https://sub.example.com", port.PasswordCredentials{})
	if err != nil || !proof.RequiresTwoFactor || proof.AccessToken != "" {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
	proof, err = c.PasswordTwoFactor(context.Background(), "https://sub.example.com", proof.TempToken, "123456")
	if err != nil || proof.AccessToken == "" || calls != 2 {
		t.Fatal("access-only response rejected")
	}
}

func TestPasswordSSRFAndTransportPolicy(t *testing.T) {
	c := New()
	transport := c.http.Transport.(*http.Transport)
	if transport.Proxy != nil || c.http.Jar != nil {
		t.Fatal("unsafe transport")
	}
	for _, base := range []string{"http://sub.example.com", "https://127.0.0.1", "https://10.0.0.1", "https://user@sub.example.com", "https://sub.example.com?x=1"} {
		if _, err := c.PasswordLogin(context.Background(), base, port.PasswordCredentials{}); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
}
