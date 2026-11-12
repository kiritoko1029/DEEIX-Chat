package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有 HTTP 响应补充通用安全响应头。
func SecurityHeaders(frameOrigins ...string) gin.HandlerFunc {
	// Accept one canonical HTTPS origin, never a wildcard, URL path or header fragment.
	frameOrigin := ""
	if len(frameOrigins) == 1 {
		candidate := frameOrigins[0]
		origin, err := security.HTTPOrigin(candidate)
		if err == nil && origin == candidate && strings.HasPrefix(origin, "https://") && security.ValidateOutboundHTTPURL(origin, security.NewPublicOnlyOutboundPolicy()) == nil {
			frameOrigin = origin
		}
	}
	return func(c *gin.Context) {
		header := c.Writer.Header()
		setHeaderIfEmpty(header, "X-Content-Type-Options", "nosniff")
		if frameOrigin != "" && !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			// CSP replaces X-Frame-Options only for the trusted web shell. File/API
			// handlers keep DENY and may supply an even stricter CSP themselves.
			header.Del("X-Frame-Options")
			header.Set("Content-Security-Policy", "frame-ancestors 'self' "+frameOrigin)
		} else {
			setHeaderIfEmpty(header, "X-Frame-Options", "DENY")
		}
		header.Set("Referrer-Policy", "no-referrer")
		setHeaderIfEmpty(header, "Permissions-Policy", "camera=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), browsing-topics=()")
		if sensitiveSub2APIResponse(c.Request) {
			writer := &noStoreResponseWriter{ResponseWriter: c.Writer}
			writer.protect()
			c.Writer = writer
			defer writer.protect()
		}
		c.Next()
	}
}

func sensitiveSub2APIResponse(r *http.Request) bool {
	if r.URL.Path == "/api/v1/auth/sub2api/exchange" {
		return true
	}
	if r.URL.Path != "/" {
		return false
	}
	// Parse keys separately: even a malformed token value must not make the
	// sensitive entry cacheable by causing URL.Query to discard the pair.
	for _, item := range strings.Split(r.URL.RawQuery, "&") {
		key, _, _ := strings.Cut(item, "=")
		decoded, err := url.QueryUnescape(key)
		if err == nil && (decoded == "user_id" || decoded == "token") {
			return true
		}
	}
	return false
}

// Protect at write/flush time: static files and compression middleware can set
// their own cache headers after SecurityHeaders has called Next.
type noStoreResponseWriter struct{ gin.ResponseWriter }

func (w *noStoreResponseWriter) protect() {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Del("Expires")
}
func (w *noStoreResponseWriter) WriteHeader(status int) {
	w.protect()
	w.ResponseWriter.WriteHeader(status)
}
func (w *noStoreResponseWriter) WriteHeaderNow() { w.protect(); w.ResponseWriter.WriteHeaderNow() }
func (w *noStoreResponseWriter) Write(data []byte) (int, error) {
	w.protect()
	return w.ResponseWriter.Write(data)
}
func (w *noStoreResponseWriter) WriteString(data string) (int, error) {
	w.protect()
	return w.ResponseWriter.WriteString(data)
}
func (w *noStoreResponseWriter) Flush() { w.protect(); w.ResponseWriter.Flush() }

func setHeaderIfEmpty(header http.Header, key string, value string) {
	if header.Get(key) != "" {
		return
	}
	header.Set(key, value)
}
