package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

const sub2APIPasswordCookie = "deeix_sub2api_attempt"

type sub2APIPasswordProgressResponse struct {
	Stage             string                            `json:"stage"`
	FlowID            string                            `json:"flowID,omitempty"`
	ExpiresAt         *time.Time                        `json:"expiresAt,omitempty"`
	MaskedEmail       *string                           `json:"maskedEmail,omitempty"`
	ProtocolSelection *Sub2APIProtocolSelectionResponse `json:"protocolSelection,omitempty"`
	Login             *LoginResponse                    `json:"login,omitempty"`
}

func passwordWebClient(c *gin.Context) bool {
	platform := strings.ToLower(strings.TrimSpace(c.GetHeader(ClientPlatformHeader)))
	return platform != "desktop" && platform != "mobile" && (c.GetHeader("Origin") == "" || isWebOrigin(c.GetHeader("Origin")))
}

func (h *Handler) sub2APIPasswordBoundary(allowedOrigins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			response.ErrorFrom(c, http.StatusUnsupportedMediaType, appauth.ErrSub2APIPasswordInvalidRequest)
			c.Abort()
			return
		}
		if !passwordWebClient(c) || !isWebOrigin(c.GetHeader("Origin")) || !middleware.Sub2APIExchangeOriginAllowed(c, allowedOrigins) {
			response.ErrorFrom(c, http.StatusForbidden, appauth.ErrSub2APIPasswordDisabled)
			c.Abort()
			return
		}
		// Cleanup of an obsolete flow is not an account switch. Its timer or
		// queued request may run after another login has issued a fresh cookie.
		if c.FullPath() != "/api/v1/auth/sub2api/login/cancel" {
			h.clearRefreshTokenCookie(c)
		}
		if !h.service.Sub2APIPasswordEnabled() && c.FullPath() != "/api/v1/auth/sub2api/login/cancel" {
			h.writeSub2APIPasswordResult(c, nil, appauth.ErrSub2APIPasswordDisabled)
			c.Abort()
			return
		}
		c.Next()
	}
}

// This limiter remains mandatory when optional public authentication limits are disabled.
func (h *Handler) sub2APIPasswordRateLimit(limiter middleware.RateLimiter, cfg *config.Runtime) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter == nil {
			h.writeSub2APIPasswordResult(c, nil, appauth.ErrSub2APIPasswordUnavailable)
			c.Abort()
			return
		}
		limit := cfg.Snapshot().PublicAuthRateLimitRPM
		if limit <= 0 {
			limit = 30
		}
		key := fmt.Sprintf("ratelimit:sub2api-password:ip:%s:minute:%d", c.ClientIP(), time.Now().Unix()/60)
		allowed, err := limiter.AllowFixedWindow(c.Request.Context(), []string{key}, limit, time.Minute)
		if err != nil {
			h.writeSub2APIPasswordResult(c, nil, appauth.ErrSub2APIPasswordUnavailable)
			c.Abort()
			return
		}
		if !allowed {
			h.writeSub2APIPasswordResult(c, nil, &appauth.Sub2APIPasswordRateError{RetryAfter: time.Minute})
			c.Abort()
			return
		}
		c.Next()
	}
}

func decodeSub2APIPassword(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dst)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = appauth.ErrSub2APIPasswordInvalidRequest
		}
	}
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		response.ErrorFrom(c, status, appauth.ErrSub2APIPasswordInvalidRequest)
		return false
	}
	return true
}

func (h *Handler) passwordFlowBinding(c *gin.Context, create bool) (appauth.PasswordFlowBinding, error) {
	cookie, _ := c.Cookie(sub2APIPasswordCookie)
	raw, err := base64.RawURLEncoding.DecodeString(cookie)
	if err != nil || len(raw) != 32 || len(cookie) != 43 {
		if !create {
			return appauth.PasswordFlowBinding{}, appauth.ErrSub2APIPasswordFlowExpired
		}
		raw = make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return appauth.PasswordFlowBinding{}, appauth.ErrSub2APIPasswordUnavailable
		}
		cookie = base64.RawURLEncoding.EncodeToString(raw)
	}
	if create {
		http.SetCookie(c.Writer, &http.Cookie{Name: sub2APIPasswordCookie, Value: cookie, Path: "/api/v1/auth/sub2api", HttpOnly: true, Secure: h.shouldUseSecureCookie(c), SameSite: http.SameSiteStrictMode, MaxAge: 300})
	}
	return appauth.PasswordFlowBinding{Cookie: cookie, Audit: middleware.ResolveSessionAuditContext(c)}, nil
}

func (h *Handler) Sub2APIPasswordLoginOptions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if !passwordWebClient(c) {
		response.ErrorFrom(c, http.StatusForbidden, appauth.ErrSub2APIPasswordDisabled)
		return
	}
	options := h.service.Sub2APIPasswordLoginOptions(c.Request.Context())
	response.Success(c, gin.H{
		"enabled": options.Enabled, "passwordLoginAvailable": options.PasswordLoginAvailable,
		"browserOnly": options.BrowserOnly, "unavailableReason": options.UnavailableReason,
		"upstreamVersion": options.UpstreamVersion, "remoteTotpEnabled": options.RemoteTotpEnabled,
		"captcha":     gin.H{"provider": options.Captcha.Provider, "siteKey": options.Captcha.SiteKey},
		"fallbackURL": options.FallbackURL,
	})
}

func (h *Handler) LoginSub2APIPassword(c *gin.Context) {
	if !h.service.Sub2APIPasswordEnabled() {
		h.writeSub2APIPasswordResult(c, nil, appauth.ErrSub2APIPasswordDisabled)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Captcha  *struct {
			Provider string `json:"provider"`
			Token    string `json:"token,omitempty"`
		} `json:"captcha,omitempty"`
	}
	if !decodeSub2APIPassword(c, &req) {
		return
	}
	binding, err := h.passwordFlowBinding(c, true)
	if err != nil {
		h.writeSub2APIPasswordResult(c, nil, err)
		return
	}
	var captcha *appauth.Sub2APIPasswordCaptcha
	if req.Captcha != nil {
		captcha = &appauth.Sub2APIPasswordCaptcha{Provider: req.Captcha.Provider, Token: req.Captcha.Token}
	}
	result, err := h.service.StartSub2APIPassword(c.Request.Context(), req.Email, req.Password, captcha, binding, middleware.MustRequestID(c))
	req.Password = ""
	h.writeSub2APIPasswordResult(c, result, err)
}

func (h *Handler) VerifySub2APIPasswordTwoFactor(c *gin.Context) {
	var req struct {
		FlowID string `json:"flowID"`
		Code   string `json:"code"`
	}
	if !decodeSub2APIPassword(c, &req) {
		return
	}
	binding, err := h.passwordFlowBinding(c, false)
	if err != nil {
		h.writeSub2APIPasswordResult(c, nil, err)
		return
	}
	result, err := h.service.VerifySub2APIPasswordTwoFactor(c.Request.Context(), req.FlowID, req.Code, binding, middleware.MustRequestID(c))
	req.Code = ""
	h.writeSub2APIPasswordResult(c, result, err)
}

func (h *Handler) ContinueSub2APIPassword(c *gin.Context) {
	var req struct {
		FlowID             string                         `json:"flowID"`
		ProtocolSelections []Sub2APIProtocolChoiceRequest `json:"protocolSelections"`
		SkipKeyIDs         []int64                        `json:"skipKeyIDs"`
	}
	if !decodeSub2APIPassword(c, &req) {
		return
	}
	binding, err := h.passwordFlowBinding(c, false)
	if err != nil {
		h.writeSub2APIPasswordResult(c, nil, err)
		return
	}
	options := Sub2APIExchangeRequest{ProtocolSelections: req.ProtocolSelections, SkipKeyIDs: req.SkipKeyIDs}.options()
	result, err := h.service.ContinueSub2APIPassword(c.Request.Context(), req.FlowID, options, binding, middleware.MustRequestID(c))
	h.writeSub2APIPasswordResult(c, result, err)
}

func (h *Handler) CancelSub2APIPassword(c *gin.Context) {
	var req struct {
		FlowID string `json:"flowID"`
	}
	if !decodeSub2APIPassword(c, &req) {
		return
	}
	if binding, err := h.passwordFlowBinding(c, false); err == nil {
		h.service.CancelSub2APIPasswordFlow(req.FlowID, binding)
	}
	response.Success(c, struct {
		Cancelled bool `json:"cancelled"`
	}{true})
}

func passwordProgressResponse(progress *appauth.Sub2APIPasswordProgress) *sub2APIPasswordProgressResponse {
	if progress == nil {
		return nil
	}
	out := &sub2APIPasswordProgressResponse{Stage: progress.Stage, FlowID: progress.FlowID}
	if !progress.ExpiresAt.IsZero() {
		out.ExpiresAt = &progress.ExpiresAt
	}
	if progress.Stage == "remote_2fa" {
		out.MaskedEmail = &progress.MaskedEmail
	}
	if progress.ProtocolSelection != nil {
		selection := toSub2APIProtocolSelectionResponse(*progress.ProtocolSelection)
		out.ProtocolSelection = &selection
	}
	if progress.Login != nil {
		login := toLoginResponse(progress.Login)
		login.RefreshToken = ""
		out.Login = &login
	}
	return out
}

func (h *Handler) writeSub2APIPasswordResult(c *gin.Context, progress *appauth.Sub2APIPasswordProgress, err error) {
	if err == nil {
		if progress != nil && progress.Login != nil && !progress.Login.TwoFactorRequired {
			removeSub2APIRefreshCookieHeader(c)
			h.writeRefreshTokenCookie(c, progress.Login)
			h.recordAudit(c, progress.Login.User.ID, "login", "user", progress.Login.User.PublicID, map[string]string{"event": "sub2api_password_login"})
		}
		response.Success(c, passwordProgressResponse(progress))
		return
	}
	status := http.StatusBadGateway
	var details any
	var rejected *appauth.Sub2APIRejectionError
	var importErr *appauth.Sub2APIImportError
	var limited *appauth.Sub2APIPasswordRateError
	switch {
	case errors.As(err, &limited):
		status = http.StatusTooManyRequests
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
	case errors.Is(err, appauth.ErrSub2APIPasswordDisabled):
		status = http.StatusForbidden
	case errors.Is(err, appauth.ErrSub2APIPasswordUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, appauth.ErrSub2APIPasswordFlowExpired):
		status = http.StatusGone
	case errors.Is(err, appauth.ErrSub2APIPasswordFlowBusy):
		status = http.StatusConflict
	case errors.Is(err, appauth.ErrSub2APIPasswordInvalidRequest), errors.Is(err, appauth.ErrSub2APIInvalidRequest), errors.Is(err, appauth.ErrSub2APIPasswordCaptcha):
		status = http.StatusBadRequest
	case errors.Is(err, appauth.ErrSub2APIPasswordCredentials):
		status = http.StatusUnauthorized
	case errors.Is(err, appauth.ErrSub2APIPasswordTwoFactor):
		status = http.StatusBadRequest
		details = passwordProgressResponse(progress)
	case errors.Is(err, appauth.ErrSub2APIProtocolRequired):
		status = http.StatusConflict
		details = passwordProgressResponse(progress)
	case errors.As(err, &rejected):
		status = http.StatusUnauthorized
		details = rejected.Details()
	case errors.Is(err, appauth.ErrInvalidCredentials), errors.Is(err, appauth.ErrSub2APIRejected):
		status = http.StatusUnauthorized
	case errors.Is(err, appauth.ErrSub2APIUnavailable):
		status = http.StatusBadGateway
	case errors.As(err, &importErr):
		status = http.StatusForbidden
		details = map[string]any{"sub2apiImport": toSub2APIImportResponse(importErr.Summary)}
	case errors.Is(err, appauth.ErrAccountLocked):
		writeAccountLockedResponse(c, err)
		return
	case errors.Is(err, appauth.ErrSub2APINoKey), errors.Is(err, appauth.ErrSub2APIGateway), strings.HasPrefix(apperr.Code(err), "personal_provider."):
		status = http.StatusForbidden
	default:
		err = appauth.ErrSub2APIPasswordUnavailable
		status = http.StatusServiceUnavailable
	}
	description := response.Describe(status, err)
	response.RecordError(c, err)
	response.RecordErrorCode(c, description.Code)
	c.JSON(description.Status, response.Envelope{ErrorMsg: description.Message, ErrorCode: description.Code, Details: details, RequestID: middleware.MustRequestID(c)})
}
