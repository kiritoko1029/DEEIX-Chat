package auth

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/userview"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/conv"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/apperr"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
	"github.com/google/uuid"
)

type sub2apiClient = port.Client
type sub2apiProviderImporter interface {
	SyncSub2API(context.Context, uint, string, int64, personal.Sub2APIImportInput, personal.RequestMeta) (personal.Sub2APIImportResult, error)
}

func (s *Service) SetSub2API(client port.Client, identities repository.Sub2APIIdentityRepository, providers sub2apiProviderImporter) {
	s.sub2apiClient, s.sub2apiIdentities, s.sub2apiProviders = client, identities, providers
}

func (s *Service) Sub2APIEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Snapshot().Sub2APIEnabled && !s.cfg.Snapshot().LocalMode
}

// ExchangeSub2API uses the remote credential only within this request. No JWT or
// upstream response body is retained. Display-only profile fields are stored separately.
func (s *Service) ExchangeSub2API(ctx context.Context, userID int64, rawToken, requestID string, auditCtx requestmeta.SessionAuditContext) (result *LoginResult, err error) {
	if !s.Sub2APIEnabled() {
		return nil, ErrSub2APIDisabled
	}
	if userID <= 0 || userID > 9007199254740991 || len(rawToken) == 0 || len(rawToken) > 16384 {
		return nil, ErrSub2APIInvalidRequest
	}
	for _, char := range rawToken {
		if char < 0x21 || char > 0x7e {
			return nil, ErrSub2APIInvalidRequest
		}
	}
	authEventCtx := ctx
	defer func() {
		if err != nil {
			s.RecordAuthEvent(authEventCtx, repository.AuthEventInput{RequestID: requestID, EventType: "sub2api_login", Result: "failure", Reason: apperr.Code(err), ClientIP: auditCtx.ClientIP, UserAgent: auditCtx.UserAgent})
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	cfg := s.cfg.Snapshot()
	if !cfg.PersonalProvidersEnabled {
		return nil, personal.ErrFeatureDisabled
	}
	if s.sub2apiClient == nil || s.sub2apiIdentities == nil || s.sub2apiProviders == nil {
		return nil, ErrSub2APIUnavailable
	}
	instance, err := normalizeSub2APIURL(cfg.Sub2APIBaseURL)
	if err != nil {
		return nil, ErrSub2APIUnavailable
	}
	instance = strings.TrimSuffix(instance, "/api/v1")
	snapshot, err := s.sub2apiClient.Fetch(ctx, instance, rawToken, userID, cfg.Sub2APIAPIBaseURL == "")
	if err != nil {
		if errors.Is(err, port.ErrRejected) {
			return nil, &Sub2APIRejectionError{details: port.RejectionDetailsFrom(err)}
		}
		return nil, ErrSub2APIUnavailable
	}
	if snapshot.User.ID != userID {
		return nil, sub2APIRejection(port.StageIdentity, port.ReasonUserIDMismatch)
	}
	if snapshot.User.Status != "active" {
		return nil, sub2APIRejection(port.StageIdentity, port.ReasonUserInactive)
	}
	summary, candidates, err := prepareSub2APIImport(snapshot.Keys, userID, instance, cfg.Sub2APIAPIBaseURL, snapshot.APIBaseURL)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	localUser, err := s.sub2apiIdentities.ResolveSub2APIUser(ctx, instance, userID, repository.CreateWithCredentialInput{
		User:       &domainuser.User{PublicID: conv.NormalizePublicID(uuid.NewString()), Username: "sub2api_" + conv.NormalizePublicID(uuid.NewString()), DisplayName: "sub2api 用户", Role: domainuser.RoleUser, Status: domainuser.StatusActive, Locale: "zh-CN", Timezone: "Etc/UTC", OnboardingCompletedAt: &now},
		Credential: domainuser.Credential{PasswordEnabled: false, PasswordOrigin: domainuser.PasswordOriginSSOPlaceholder},
	})
	if err != nil {
		return nil, ErrSub2APIUnavailable
	}
	if err := ensureProviderLoginUserActive(localUser); err != nil {
		return nil, err
	}
	require2FA, err := s.shouldRequireTwoFactor(ctx, localUser)
	if err != nil {
		return nil, ErrSub2APIUnavailable
	}
	s.importSub2APIKeys(ctx, localUser.ID, instance, candidates, summary, !require2FA, personal.RequestMeta{RequestID: requestID, ClientIP: auditCtx.ClientIP, UserAgent: auditCtx.UserAgent})
	if summary.Imported+summary.Reused == 0 {
		return nil, &Sub2APIImportError{Cause: ErrSub2APIImportFailed, Summary: summary}
	}
	if !require2FA {
		profiles, ok := s.sub2apiIdentities.(repository.Sub2APIProfileRepository)
		if ok {
			if err := profiles.SyncSub2APIProfile(ctx, localUser.ID, instance, userID, sub2APIProfile(snapshot.User)); err != nil {
				return nil, ErrSub2APIUnavailable
			}
		}
	}
	result, err = s.completeProviderLoginForUser(ctx, localUser, "sub2api", strconv.FormatInt(userID, 10), requestID, auditCtx)
	if err != nil {
		return nil, err
	}
	if result.TwoFactorRequired {
		// Do not reveal local account/profile/provider identifiers before the second factor.
		result.User = userview.UserView{}
		for i := range summary.Items {
			summary.Items[i].ProviderID = ""
		}
	}
	result.Sub2APIImport = summary
	return result, nil
}

func sub2APIRejection(stage, reason string) error {
	return &Sub2APIRejectionError{details: port.SafeRejectionDetails(stage, reason)}
}

func normalizeSub2APIURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrSub2APIGateway
	}
	if security.ValidateOutboundHTTPURL(raw, security.NewPublicOnlyOutboundPolicy()) != nil {
		return "", ErrSub2APIGateway
	}
	origin, err := security.HTTPOrigin(raw)
	if err != nil {
		return "", ErrSub2APIGateway
	}
	p := path.Clean("/" + strings.TrimLeft(u.Path, "/"))
	if p == "/" {
		p = ""
	}
	normalized, err := url.Parse(origin)
	if err != nil {
		return "", ErrSub2APIGateway
	}
	normalized.Path = p
	return normalized.String(), nil
}

func sub2APIGatewayURL(instance, configured, advertised, protocol string) (string, error) {
	raw := configured
	if raw == "" {
		raw = advertised
	}
	if raw == "" {
		raw = instance
	}
	base, err := normalizeSub2APIURL(raw)
	if err != nil {
		return "", err
	}
	if configured == "" {
		a, errA := security.HTTPOrigin(instance)
		b, errB := security.HTTPOrigin(base)
		if errA != nil || errB != nil || a != b {
			return "", ErrSub2APIGateway
		}
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, "/v1beta"), "/v1")
	if protocol == "google_generate_content" {
		return base + "/v1beta", nil
	}
	return base + "/v1", nil
}
