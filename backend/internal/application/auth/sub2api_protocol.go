package auth

import (
	"context"
	"errors"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type Sub2APIProtocolChoice struct {
	KeyID    int64
	Protocol string
}
type Sub2APIExchangeOptions struct {
	ProtocolSelections []Sub2APIProtocolChoice
	SkipKeyIDs         []int64
}
type Sub2APIProtocolItem struct {
	KeyID     int64
	Name      string
	GroupName string
	Platform  string
}
type Sub2APIProtocolSelection struct {
	Items     []Sub2APIProtocolItem
	Protocols []string
}
type Sub2APIProtocolRequiredError struct{ Selection Sub2APIProtocolSelection }

// Error deliberately excludes remote display metadata from audit/log output.
func (*Sub2APIProtocolRequiredError) Error() string { return "auth.sub2api_protocol_required" }
func (*Sub2APIProtocolRequiredError) Unwrap() error { return ErrSub2APIProtocolRequired }

func sub2APIProtocols() []string {
	return []string{"openai_responses", "anthropic_messages", "openai_chat_completions"}
}
func validSub2APIProtocol(protocol string) bool {
	switch protocol {
	case "openai_responses", "anthropic_messages", "openai_chat_completions":
		return true
	default:
		return false
	}
}

type sub2APISelection struct {
	protocols map[int64]string
	skips     map[int64]bool
}

func sub2APIOptions(options []Sub2APIExchangeOptions) (sub2APISelection, error) {
	result := sub2APISelection{protocols: map[int64]string{}, skips: map[int64]bool{}}
	if len(options) == 0 {
		return result, nil
	}
	if len(options) != 1 {
		return result, ErrSub2APIInvalidRequest
	}
	input := options[0]
	if len(input.ProtocolSelections)+len(input.SkipKeyIDs) > 2000 {
		return result, ErrSub2APIInvalidRequest
	}
	for _, choice := range input.ProtocolSelections {
		if choice.KeyID <= 0 || choice.KeyID > 9007199254740991 || !validSub2APIProtocol(choice.Protocol) || result.protocols[choice.KeyID] != "" {
			return result, ErrSub2APIInvalidRequest
		}
		result.protocols[choice.KeyID] = choice.Protocol
	}
	for _, id := range input.SkipKeyIDs {
		if id <= 0 || id > 9007199254740991 || result.skips[id] || result.protocols[id] != "" {
			return result, ErrSub2APIInvalidRequest
		}
		result.skips[id] = true
	}
	return result, nil
}
func (selection sub2APISelection) validateReferences(keys []port.Key) error {
	ids := make(map[int64]bool, len(keys))
	for _, key := range keys {
		ids[key.ID] = true
	}
	for id := range selection.protocols {
		if !ids[id] {
			return ErrSub2APIInvalidRequest
		}
	}
	for id := range selection.skips {
		if !ids[id] {
			return ErrSub2APIInvalidRequest
		}
	}
	return nil
}

type sub2APIProtocolLookup interface {
	LookupSub2APIProtocol(context.Context, uint, string, int64) (string, error)
}

func (s *Service) rememberSub2APIProtocols(ctx context.Context, instance string, remoteID int64, keys []port.Key, selection *sub2APISelection) error {
	var unresolved []int64
	for _, key := range keys {
		_, reason := classifySub2APIKey(key, time.Now())
		// An explicit choice may differ from a known platform's default, and
		// upstream platform labels can change. Keep the owned provider's saved
		// choice for every otherwise-eligible Key, not only unknown platforms.
		if (reason == "" || reason == "unsupported_platform") && !selection.skips[key.ID] && selection.protocols[key.ID] == "" {
			unresolved = append(unresolved, key.ID)
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	identities, ok := s.sub2apiIdentities.(repository.Sub2APIIdentityLookup)
	providers, canLookup := s.sub2apiProviders.(sub2APIProtocolLookup)
	if !ok || !canLookup {
		return nil
	}
	owner, err := identities.LookupSub2APIUser(ctx, instance, remoteID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil || owner == nil || owner.ID == 0 {
		return ErrSub2APIUnavailable
	}
	for _, keyID := range unresolved {
		protocol, err := providers.LookupSub2APIProtocol(ctx, owner.ID, instance, keyID)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return ErrSub2APIUnavailable
		}
		if validSub2APIProtocol(protocol) {
			selection.protocols[keyID] = protocol
		}
	}
	return nil
}
