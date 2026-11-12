package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
	"github.com/gin-gonic/gin"
)

// Sub2APIExchangeOriginAllowed requires same origin or an explicit CORS entry.
// Wildcard CORS cannot authorize credential-changing requests. Browser requests
// without Origin are rejected when Fetch Metadata identifies another site.
func Sub2APIExchangeOriginAllowed(c *gin.Context, configured string) bool {
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin == "" {
		site := c.GetHeader("Sec-Fetch-Site")
		return site == "" || site == "same-origin" || site == "none"
	}
	if origin == "null" {
		return false
	}
	for _, desktopOrigin := range desktopWebviewOrigins {
		if origin == desktopOrigin {
			return true
		}
	}
	normalized, err := security.HTTPOrigin(origin)
	// Origin contains only scheme/authority, never a path/query/userinfo.
	if err != nil || !strings.EqualFold(strings.TrimRight(origin, "/"), normalized) {
		return false
	}
	scheme := "http"
	if c.Request.TLS != nil || (requestCameFromTrustedProxy(c) && c.GetHeader("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	if own, err := security.HTTPOrigin(scheme + "://" + c.Request.Host); err == nil && own == normalized {
		return true
	}
	for _, allowed := range strings.Split(configured, ",") {
		if trusted, err := security.HTTPOrigin(strings.TrimSpace(allowed)); err == nil && trusted == normalized {
			return true
		}
	}
	return false
}

// Sub2APIExchangeRateLimit is always enforced for this costly public exchange,
// regardless of the optional global limiter switch, user identity or role.
func Sub2APIExchangeRateLimit(limiter RateLimiter, cfg *config.Runtime) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		if limiter == nil {
			response.ErrorFrom(c, http.StatusServiceUnavailable, errRateLimitUnavailable)
			c.Abort()
			return
		}
		clientIP := c.ClientIP()
		if clientIP == "" {
			clientIP = "unknown"
		}
		policy := newRateLimitPolicy("sub2api_exchange", publicAuthRateLimitRPM(cfg), errAuthRateLimitExceeded)
		// Fixed-window stores use TTL for cleanup; an explicit minute bucket
		// keeps the configured RPM independent of sliding Redis expiration.
		key := fmt.Sprintf("ratelimit:sub2api-exchange:ip:%s:minute:%d", clientIP, time.Now().Unix()/60)
		allowed, err := limiter.AllowFixedWindow(c.Request.Context(), []string{key}, policy.Limit, policy.TTL)
		if err != nil {
			response.ErrorFrom(c, http.StatusServiceUnavailable, errRateLimitUnavailable)
			c.Abort()
			return
		}
		if !allowed {
			writeRateLimitError(c, policy)
			return
		}
		c.Next()
	}
}
