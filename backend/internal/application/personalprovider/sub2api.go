package personalprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

// Sub2APIImportInput never comes directly from a public provider-create request.
type Sub2APIImportInput struct {
	CandidateInput
	Name         string
	AllowChanges bool
}

type Sub2APIImportResult struct {
	ProviderID string
	Status     string
	Reason     string
}

type importedNameSyncer interface {
	SyncImportedName(context.Context, uint, string, string, string, string) (bool, error)
}

// EnsureSub2API preserves the previous internal API for integrations/tests.
func (s *Service) EnsureSub2API(ctx context.Context, userID uint, instance string, keyID int64, input CandidateInput, allow bool, meta RequestMeta) error {
	result, err := s.SyncSub2API(ctx, userID, instance, keyID, Sub2APIImportInput{CandidateInput: input, Name: "sub2api", AllowChanges: allow}, meta)
	if err == nil && result.Status == "skipped" {
		return ErrModelUnavailable
	}
	return err
}

// SyncSub2API keeps each remote key in its own immutable provider identity.
func (s *Service) SyncSub2API(ctx context.Context, userID uint, instance string, keyID int64, input Sub2APIImportInput, meta RequestMeta) (Sub2APIImportResult, error) {
	if err := s.requireEnabled(userID, s.policy()); err != nil {
		return Sub2APIImportResult{}, err
	}
	candidate, err := s.normalizeCandidate(input.CandidateInput)
	if err != nil {
		return Sub2APIImportResult{}, err
	}
	name, err := normalizeName(input.Name, "sub2api")
	if err != nil {
		return Sub2APIImportResult{}, err
	}
	sum := sha256.Sum256([]byte("sub2api\x00" + instance + "\x00" + strconv.FormatUint(uint64(userID), 10) + "\x00" + strconv.FormatInt(keyID, 10)))
	publicID := hex.EncodeToString(sum[:16])
	reuse := func() (Sub2APIImportResult, error) {
		current, err := s.repo.GetByOwner(ctx, userID, publicID)
		if err != nil {
			return Sub2APIImportResult{}, err
		}
		if current.Source != "sub2api" {
			return Sub2APIImportResult{}, ErrDuplicate
		}
		if current.Status == domain.StatusSuspended {
			return Sub2APIImportResult{}, ErrSuspended
		}
		if current.Status != domain.StatusActive || len(current.Models) == 0 {
			return Sub2APIImportResult{}, ErrImportedProviderDisabled
		}
		key, err := s.decryptKey(current)
		if err != nil || current.BaseURL != candidate.endpoint.BaseURL || current.Protocol != candidate.protocol || key != candidate.apiKey {
			return Sub2APIImportResult{}, ErrImportedCredentialChanged
		}
		autoNamed := (current.LastSyncedName != "" && current.Name == current.LastSyncedName) || (current.LastSyncedName == "" && current.Name == "sub2api")
		if input.AllowChanges && autoNamed && (current.Name != name || current.LastSyncedName != name) {
			if repo, ok := s.repo.(importedNameSyncer); ok {
				changed, err := repo.SyncImportedName(ctx, userID, publicID, current.Name, current.LastSyncedName, name)
				if err != nil {
					return Sub2APIImportResult{}, err
				}
				if changed {
					s.audit(ctx, userID, meta, "personal_provider.sub2api_name_sync", current, nil)
				}
			}
		}
		return Sub2APIImportResult{ProviderID: publicID, Status: "reused"}, nil
	}
	if result, err := reuse(); !errors.Is(err, repository.ErrNotFound) {
		return result, err
	}
	if !input.AllowChanges {
		return Sub2APIImportResult{Status: "skipped", Reason: "two_factor_required"}, nil
	}
	_, err = s.create(ctx, userID, CreateInput{Name: name, Protocol: input.Protocol, BaseURL: input.BaseURL, APIKey: input.APIKey}, meta, publicID, "sub2api")
	if err != nil {
		if result, reuseErr := reuse(); !errors.Is(reuseErr, repository.ErrNotFound) {
			return result, reuseErr
		}
		return Sub2APIImportResult{}, err
	}
	return Sub2APIImportResult{ProviderID: publicID, Status: "imported"}, nil
}

type limitedCreator interface {
	CreateLimited(context.Context, *domain.Provider, int) (*domain.Provider, error)
}

func (s *Service) createLimited(ctx context.Context, item *domain.Provider, limit int) (*domain.Provider, error) {
	if repo, ok := s.repo.(limitedCreator); ok {
		created, err := repo.CreateLimited(ctx, item, limit)
		if errors.Is(err, repository.ErrPersonalProviderLimitReached) {
			return nil, ErrLimitReached
		}
		return created, err
	}
	return s.repo.Create(ctx, item)
}
