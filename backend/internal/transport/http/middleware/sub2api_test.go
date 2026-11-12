package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/gin-gonic/gin"
)

func TestSub2APIExchangeRateLimitAlwaysEnforcedByIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &recordingRateLimiter{allow: false}
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.POST("/api/v1/auth/sub2api/exchange", Sub2APIExchangeRateLimit(limiter, config.NewRuntime(config.Config{RateLimitEnabled: false, PublicAuthRateLimitRPM: 7})), okHandler)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sub2api/exchange", nil)
	r.RemoteAddr = "203.0.113.8:12345"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 429 || w.Result().Header.Get("Cache-Control") != "no-store" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("status=%d headers=%v", w.Code, w.Header())
	}
	if len(limiter.fixed) != 1 || limiter.fixed[0].Limit != 7 || !strings.HasPrefix(limiter.fixed[0].Keys[0], "ratelimit:sub2api-exchange:ip:203.0.113.8:minute:") {
		t.Fatalf("limiter calls=%+v", limiter.fixed)
	}
}

type failingExchangeLimiter struct{ recordingRateLimiter }

func (failingExchangeLimiter) AllowFixedWindow(context.Context, []string, int, time.Duration) (bool, error) {
	return false, errors.New("internal cache credentials must not leak")
}
func TestSub2APIExchangeRateLimitFailsClosed(t *testing.T) {
	for _, limiter := range []RateLimiter{nil, &failingExchangeLimiter{}} {
		router := gin.New()
		router.POST("/exchange", Sub2APIExchangeRateLimit(limiter, config.NewRuntime(config.Config{})), okHandler)
		w := performRequest(router, http.MethodPost, "/exchange")
		if w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "cache credentials") {
			t.Fatal(w.Body.String())
		}
	}
}

func TestSub2APIExchangeOriginPolicyRejectsWildcardAndOpaqueOrigins(t *testing.T) {
	for _, tc := range []struct {
		name, origin, fetchSite, allow string
		want                           bool
	}{
		{"same origin", "http://chat.example.com", "same-origin", "", true},
		{"explicit cross origin", "https://web.example.com", "cross-site", "https://web.example.com", true},
		{"wildcard cross origin", "https://attacker.example.com", "cross-site", "*", false},
		{"opaque", "null", "cross-site", "*", false},
		{"originless cross site", "", "cross-site", "*", false},
		{"originless same site", "", "same-site", "*", false},
		{"native", "", "", "", true},
		{"tauri", "tauri://localhost", "", "", true},
		{"origin userinfo", "https://user:pass@chat.example.com", "", "*", false},
		{"origin path", "https://chat.example.com/path", "", "*", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "http://chat.example.com/api/v1/auth/sub2api/exchange", nil)
			c.Request.Header.Set("Origin", tc.origin)
			c.Request.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			if got := Sub2APIExchangeOriginAllowed(c, tc.allow); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
