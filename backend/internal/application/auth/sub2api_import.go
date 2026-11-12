package auth

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	personal "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
)

// Sub2APIImportSummary counts every remote key once; Items stay in remote-ID order.
type Sub2APIImportSummary struct {
	Total, Eligible, Imported, Reused, Skipped, Failed int
	Items                                              []Sub2APIImportItem
}
type Sub2APIImportItem struct {
	KeyID                                       int64
	Name, GroupName, ProviderID, Status, Reason string
}
type sub2APICandidate struct {
	index int
	key   port.Key
	input personal.Sub2APIImportInput
}

func formatSub2APIKeyID(id int64) string { return strconv.FormatInt(id, 10) }

func classifySub2APIKey(key port.Key, now time.Time) (string, string) {
	if key.ID <= 0 || key.Key == "" || len(key.Key) > 512 || strings.IndexFunc(key.Key, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return "", "invalid_key"
	}
	if key.Status != "active" {
		return "", "inactive"
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return "", "expired"
	}
	if math.IsNaN(key.Quota) || math.IsInf(key.Quota, 0) || math.IsNaN(key.QuotaUsed) || math.IsInf(key.QuotaUsed, 0) || key.Quota < 0 || key.QuotaUsed < 0 {
		return "", "invalid_key"
	}
	if key.Quota > 0 && key.QuotaUsed >= key.Quota {
		return "", "quota_exhausted"
	}
	if key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID {
		return "", "invalid_group"
	}
	if key.Group.Status != "active" {
		return "", "inactive"
	}
	switch key.Group.Platform {
	case "openai", "deepseek":
		return "openai_responses", ""
	case "anthropic":
		return "anthropic_messages", ""
	case "gemini":
		return "google_generate_content", ""
	default:
		return "", "unsupported_platform"
	}
}

func prepareSub2APIImport(keys []port.Key, userID int64, instance, configured, advertised string, selections ...sub2APISelection) (*Sub2APIImportSummary, []sub2APICandidate, error) {
	if err := validateSub2APIKeys(keys, userID); err != nil {
		return nil, nil, err
	}
	selection := sub2APISelection{}
	if len(selections) > 0 {
		selection = selections[0]
	}
	required := &Sub2APIProtocolRequiredError{Selection: Sub2APIProtocolSelection{Protocols: sub2APIProtocols()}}
	keys = append([]port.Key(nil), keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	summary := &Sub2APIImportSummary{Total: len(keys), Items: make([]Sub2APIImportItem, len(keys))}
	candidates := make([]sub2APICandidate, 0, len(keys))
	gatewayFailed := false
	for i, key := range keys {
		item := Sub2APIImportItem{KeyID: key.ID, Name: sub2APIText(key.Name, 128), Status: "skipped"}
		if key.Group != nil {
			item.GroupName = sub2APIText(key.Group.Name, 128)
		}
		protocol, reason := classifySub2APIKey(key, time.Now())
		if selection.skips[key.ID] {
			reason = "user_skipped"
		} else if reason == "" || reason == "unsupported_platform" {
			if chosen := selection.protocols[key.ID]; chosen != "" {
				protocol, reason = chosen, ""
			} else if reason == "unsupported_platform" {
				required.Selection.Items = append(required.Selection.Items, Sub2APIProtocolItem{KeyID: key.ID, Name: item.Name, GroupName: item.GroupName, Platform: sub2APIText(key.Group.Platform, 128)})
			}
		}
		item.Reason = reason
		if reason != "" {
			summary.Skipped++
			summary.Items[i] = item
			continue
		}
		summary.Eligible++
		base, err := sub2APIGatewayURL(instance, configured, advertised, protocol)
		if err != nil {
			gatewayFailed = true
			item.Status = "failed"
			item.Reason = "gateway_not_allowed"
			summary.Failed++
		} else {
			candidates = append(candidates, sub2APICandidate{index: i, key: key, input: personal.Sub2APIImportInput{CandidateInput: personal.CandidateInput{Protocol: protocol, BaseURL: base, APIKey: key.Key}, Name: sub2APIProviderName(key)}})
		}
		summary.Items[i] = item
	}
	if len(required.Selection.Items) > 0 {
		return nil, nil, required
	}
	if summary.Eligible == 0 {
		return summary, nil, &Sub2APIImportError{Cause: ErrSub2APINoKey, Summary: summary}
	}
	if gatewayFailed {
		return summary, nil, &Sub2APIImportError{Cause: ErrSub2APIGateway, Summary: summary}
	}
	return summary, candidates, nil
}

func (s *Service) importSub2APIKeys(ctx context.Context, userID uint, instance string, candidates []sub2APICandidate, summary *Sub2APIImportSummary, allow bool, meta personal.RequestMeta) {
	// Three bounded probes share a 25-second budget; reserve time to issue a session
	// and return partial results even when one upstream stalls.
	budget := 25 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) - 5*time.Second; remaining < budget {
			budget = remaining
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	jobs := make(chan sub2APICandidate)
	var wg sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				item := &summary.Items[job.index]
				if probeCtx.Err() != nil {
					item.Status = "failed"
					item.Reason = "timeout"
					continue
				}
				job.input.AllowChanges = allow
				result, err := s.sub2apiProviders.SyncSub2API(probeCtx, userID, instance, job.key.ID, job.input, meta)
				if err != nil {
					item.Status = "failed"
					item.Reason = sub2APIImportReason(err)
					// Personal-provider probes intentionally mask upstream errors;
					// the shared context still identifies an exhausted import budget.
					if probeCtx.Err() != nil {
						item.Reason = "timeout"
					}
					continue
				}
				switch result.Status {
				case "imported", "reused":
					item.Status = result.Status
					item.ProviderID = result.ProviderID
				case "skipped":
					item.Status = "skipped"
					item.Reason = "two_factor_required"
				default:
					item.Status = "failed"
					item.Reason = "import_failed"
				}
			}
		}()
	}
	for _, candidate := range candidates {
		jobs <- candidate
	}
	close(jobs)
	wg.Wait()
	for _, candidate := range candidates {
		switch summary.Items[candidate.index].Status {
		case "imported":
			summary.Imported++
		case "reused":
			summary.Reused++
		case "skipped":
			summary.Skipped++
		case "failed":
			summary.Failed++
		}
	}
}

func sub2APIImportReason(err error) string {
	switch {
	case errors.Is(err, personal.ErrLimitReached):
		return "limit_reached"
	case errors.Is(err, personal.ErrSuspended):
		return "provider_suspended"
	case errors.Is(err, personal.ErrImportedProviderDisabled):
		return "provider_disabled"
	case errors.Is(err, personal.ErrImportedCredentialChanged):
		return "credential_changed"
	case errors.Is(err, personal.ErrDuplicate):
		return "duplicate"
	case errors.Is(err, personal.ErrUpstreamRejected), errors.Is(err, personal.ErrUpstreamUnavailable), errors.Is(err, personal.ErrInvalidModels):
		return "probe_failed"
	case errors.Is(err, personal.ErrBlockedHost):
		return "blocked_host"
	case errors.Is(err, personal.ErrFeatureDisabled):
		return "feature_disabled"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "import_failed"
	}
}

func validateSub2APIKeys(keys []port.Key, userID int64) error {
	if len(keys) > 2000 {
		return ErrSub2APIUnavailable
	}
	seen := make(map[int64]bool, len(keys))
	// Validate every owner before any identity/provider/profile write.
	for _, key := range keys {
		if key.UserID != userID {
			return sub2APIRejection(port.StageKeys, port.ReasonKeyOwnerMismatch)
		}
		if key.ID <= 0 || key.ID > 9007199254740991 || seen[key.ID] {
			return ErrSub2APIUnavailable
		}
		seen[key.ID] = true
	}
	return nil
}
