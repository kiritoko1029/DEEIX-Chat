package auth

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	appauth "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/auth"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type Sub2APIExchangeRequest struct {
	UserID int64  `json:"userID" binding:"required" minimum:"1" maximum:"9007199254740991"`
	Token  string `json:"token" binding:"required" minLength:"1" maxLength:"16384"`
}

// ExchangeSub2API godoc
// @Summary 使用可信 sub2api 登录凭证换取 DEEIX Chat 会话
// @Description 仅部署方开启时可用；校验远端身份并逐项复用或导入全部兼容 Key，保留本地 2FA
// @Tags auth
// @Accept json
// @Produce json
// @Param body body Sub2APIExchangeRequest true "sub2api 交换请求"
// @Success 200 {object} LoginResponseDoc
// @Failure 400 {object} ErrorDoc
// @Failure 401 {object} ErrorDoc
// @Failure 403 {object} ErrorDoc
// @Failure 404 {object} ErrorDoc
// @Failure 413 {object} ErrorDoc
// @Failure 415 {object} ErrorDoc
// @Failure 423 {object} ErrorDoc
// @Failure 429 {object} ErrorDoc
// @Failure 502 {object} ErrorDoc
// @Failure 503 {object} ErrorDoc
// @Router /auth/sub2api/exchange [post]
func (h *Handler) ExchangeSub2API(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if !h.service.Sub2APIEnabled() {
		response.ErrorFrom(c, http.StatusNotFound, appauth.ErrSub2APIDisabled)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 20<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var req Sub2APIExchangeRequest
	err := decoder.Decode(&req)
	if err == nil {
		var extra any
		if trailingErr := decoder.Decode(&extra); trailingErr != io.EOF {
			err = trailingErr
			if err == nil {
				err = appauth.ErrSub2APIInvalidRequest
			}
		}
	}
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		response.ErrorFrom(c, status, appauth.ErrSub2APIInvalidRequest)
		return
	}
	auditCtx := middleware.ResolveSessionAuditContext(c)
	result, err := h.service.ExchangeSub2API(c.Request.Context(), req.UserID, req.Token, middleware.MustRequestID(c), auditCtx)
	if err != nil {
		var importErr *appauth.Sub2APIImportError
		if errors.As(err, &importErr) && importErr != nil {
			description := response.Describe(http.StatusForbidden, err)
			response.RecordError(c, err)
			response.RecordErrorCode(c, description.Code)
			c.JSON(description.Status, response.Envelope{ErrorMsg: description.Message, ErrorCode: description.Code, Details: map[string]any{"sub2apiImport": toSub2APIImportResponse(importErr.Summary)}, RequestID: middleware.MustRequestID(c)})
			return
		}
		var rejected *appauth.Sub2APIRejectionError
		if errors.As(err, &rejected) && rejected != nil {
			// The shared response package does not currently expose typed-error
			// details. Keep its apperr description, audit hooks and envelope;
			// Details() revalidates both values against the fixed allowlist.
			description := response.Describe(http.StatusUnauthorized, err)
			response.RecordError(c, err)
			response.RecordErrorCode(c, description.Code)
			c.JSON(description.Status, response.Envelope{ErrorMsg: description.Message, ErrorCode: description.Code, Details: rejected.Details(), RequestID: middleware.MustRequestID(c)})
			return
		}
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, appauth.ErrSub2APIInvalidRequest):
			status = http.StatusBadRequest
		case errors.Is(err, appauth.ErrSub2APIDisabled):
			status = http.StatusNotFound
		case errors.Is(err, appauth.ErrSub2APIRejected), errors.Is(err, appauth.ErrInvalidCredentials):
			status = http.StatusUnauthorized
		case errors.Is(err, appauth.ErrAccountLocked):
			writeAccountLockedResponse(c, err)
			return
		case errors.Is(err, appauth.ErrSub2APINoKey), errors.Is(err, appauth.ErrSub2APIGateway):
			status = http.StatusForbidden
		case strings.HasPrefix(apperr.Code(err), "personal_provider."):
			status = http.StatusForbidden
		default:
			err = appauth.ErrSub2APIUnavailable
		}
		response.ErrorFrom(c, status, err)
		return
	}
	if !result.TwoFactorRequired {
		h.recordAudit(c, result.User.ID, "login", "user", result.User.PublicID, map[string]string{"event": "sub2api_login"})
		// Replace the boundary's deletion header, rather than relying on browser
		// ordering of two Set-Cookie headers for the same cookie.
		if !isNativeClient(c) {
			removeSub2APIRefreshCookieHeader(c)
		}
	}
	h.respondWithSession(c, result)
}

// sub2APIExchangeBoundary runs before parsing, feature checks and rate limiting.
// Untrusted cross-site or simple-form requests must never clear a browser cookie.
func (h *Handler) sub2APIExchangeBoundary(allowedOrigins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			response.ErrorFrom(c, http.StatusUnsupportedMediaType, appauth.ErrSub2APIInvalidRequest)
			c.Abort()
			return
		}
		if !middleware.Sub2APIExchangeOriginAllowed(c, allowedOrigins) {
			response.ErrorWithCode(c, http.StatusForbidden, "cors.origin_forbidden")
			c.Abort()
			return
		}
		h.clearRefreshTokenCookie(c)
		if !h.service.Sub2APIEnabled() {
			response.ErrorFrom(c, http.StatusNotFound, appauth.ErrSub2APIDisabled)
			c.Abort()
			return
		}
		c.Next()
	}
}

func removeSub2APIRefreshCookieHeader(c *gin.Context) {
	values := c.Writer.Header().Values("Set-Cookie")
	c.Writer.Header().Del("Set-Cookie")
	for _, value := range values {
		if !strings.HasPrefix(strings.TrimSpace(value), refreshTokenCookieName+"=") {
			c.Writer.Header().Add("Set-Cookie", value)
		}
	}
}
