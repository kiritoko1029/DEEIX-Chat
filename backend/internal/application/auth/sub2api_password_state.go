package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"
	"sync"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/requestmeta"
)

const sub2APIPasswordTTL = 5 * time.Minute
const sub2APIPasswordFlowCapacity = 1024
const sub2APIPasswordRateCapacity = 4096

// PasswordFlowBinding comes only from the HTTP cookie and trusted request audit
// context. Neither it nor the remote credential is accepted in JSON input.
type PasswordFlowBinding struct {
	Cookie string
	Audit  requestmeta.SessionAuditContext
}

func passwordBindingHash(binding PasswordFlowBinding) ([32]byte, error) {
	if len(binding.Cookie) != 43 {
		return [32]byte{}, ErrSub2APIPasswordInvalidRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(binding.Cookie)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, ErrSub2APIPasswordInvalidRequest
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(binding.Audit.ClientIP))
	if err != nil {
		return [32]byte{}, ErrSub2APIPasswordInvalidRequest
	}
	ua := strings.TrimSpace(binding.Audit.UserAgent)
	if len(ua) > 1024 {
		return [32]byte{}, ErrSub2APIPasswordInvalidRequest
	}
	return sha256.Sum256([]byte(binding.Cookie + "\n" + ip.Unmap().String() + "\n" + ua)), nil
}

type sub2APIPasswordFlow struct {
	binding       [32]byte
	instance      string
	configuration string
	stage         string
	expiresAt     time.Time
	busy          bool
	cancel        context.CancelFunc
	timer         *time.Timer
	attempts      int
	tempToken     string
	maskedEmail   string
	accessToken   string
	remoteID      int64
	choices       Sub2APIExchangeOptions
}

type passwordRateBucket struct {
	count     int
	expiresAt time.Time
}

type sub2APIPasswordState struct {
	mu               sync.Mutex
	flows            map[[32]byte]*sub2APIPasswordFlow
	rates            map[[32]byte]passwordRateBucket
	key              [32]byte
	ready            bool
	capacity         int
	now              func() time.Time
	settings         port.PasswordSettings
	settingsInstance string
	settingsUntil    time.Time
	settingsBusy     bool
	blockedUntil     time.Time
}

func (s *Service) passwordState() *sub2APIPasswordState {
	s.sub2apiPasswordOnce.Do(func() {
		state := &sub2APIPasswordState{flows: make(map[[32]byte]*sub2APIPasswordFlow), rates: make(map[[32]byte]passwordRateBucket), capacity: sub2APIPasswordFlowCapacity, now: time.Now}
		_, err := rand.Read(state.key[:])
		state.ready = err == nil
		s.sub2apiPassword = state
	})
	return s.sub2apiPassword
}

func (s *sub2APIPasswordState) pruneLocked() {
	now := s.now()
	for key, flow := range s.flows {
		if !now.Before(flow.expiresAt) {
			if flow.cancel != nil {
				flow.cancel()
			}
			if flow.timer != nil {
				flow.timer.Stop()
			}
			delete(s.flows, key)
		}
	}
	for key, bucket := range s.rates {
		if !now.Before(bucket.expiresAt) {
			delete(s.rates, key)
		}
	}
}

func (s *sub2APIPasswordState) rateLocked(scope string, limit int) error {
	if !s.ready {
		return ErrSub2APIPasswordUnavailable
	}
	now := s.now()
	if now.Before(s.blockedUntil) {
		return &Sub2APIPasswordRateError{RetryAfter: s.blockedUntil.Sub(now)}
	}
	digest := hmac.New(sha256.New, s.key[:])
	_, _ = digest.Write([]byte(scope))
	var key [32]byte
	copy(key[:], digest.Sum(nil))
	bucket, exists := s.rates[key]
	if !exists {
		if len(s.rates) >= sub2APIPasswordRateCapacity {
			return ErrSub2APIPasswordUnavailable
		}
		bucket.expiresAt = now.Add(time.Minute)
	}
	if bucket.count >= limit {
		return &Sub2APIPasswordRateError{RetryAfter: bucket.expiresAt.Sub(now)}
	}
	bucket.count++
	s.rates[key] = bucket
	return nil
}

func (s *sub2APIPasswordState) allow(scope string, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	return s.rateLocked(scope, limit)
}

func (s *sub2APIPasswordState) create(ctx context.Context, binding [32]byte, instance, configuration string) (string, *sub2APIPasswordFlow, context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if !s.ready || len(s.flows) >= s.capacity {
		return "", nil, nil, ErrSub2APIPasswordUnavailable
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, nil, ErrSub2APIPasswordUnavailable
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])
	flow := &sub2APIPasswordFlow{binding: binding, instance: instance, configuration: configuration, stage: "password", expiresAt: s.now().Add(sub2APIPasswordTTL), busy: true}
	operationCtx, cancel := context.WithDeadline(ctx, flow.expiresAt)
	flow.cancel = cancel
	key := sha256.Sum256([]byte(id))
	s.flows[key] = flow
	flow.timer = time.AfterFunc(sub2APIPasswordTTL, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.flows[key] == flow {
			if flow.cancel != nil {
				flow.cancel()
			}
			if flow.timer != nil {
				flow.timer.Stop()
			}
			delete(s.flows, key)
		}
	})
	return id, flow, operationCtx, nil
}

func (s *sub2APIPasswordState) acquire(ctx context.Context, id string, binding [32]byte, configuration, stage string) (*sub2APIPasswordFlow, context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	key := sha256.Sum256([]byte(id))
	flow := s.flows[key]
	if len(id) != 43 || flow == nil || flow.binding != binding {
		return nil, nil, ErrSub2APIPasswordFlowExpired
	}
	if flow.configuration != configuration {
		if flow.cancel != nil {
			flow.cancel()
		}
		if flow.timer != nil {
			flow.timer.Stop()
		}
		delete(s.flows, key)
		return nil, nil, ErrSub2APIPasswordFlowExpired
	}
	if flow.busy {
		return nil, nil, ErrSub2APIPasswordFlowBusy
	}
	if flow.stage != stage {
		return nil, nil, ErrSub2APIPasswordInvalidRequest
	}
	flow.busy = true
	operationCtx, cancel := context.WithDeadline(ctx, flow.expiresAt)
	flow.cancel = cancel
	return flow, operationCtx, nil
}

// finish is the completion linearization point. Cancellation which wins this
// lock prevents any local login credentials from being returned by the service.
func (s *sub2APIPasswordState) finish(id string, flow *sub2APIPasswordFlow, keep bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(id))
	valid := s.flows[key] == flow && s.now().Before(flow.expiresAt)
	if flow.cancel != nil {
		flow.cancel()
		flow.cancel = nil
	}
	if !valid {
		if s.flows[key] == flow {
			if flow.timer != nil {
				flow.timer.Stop()
			}
			delete(s.flows, key)
		}
		return false
	}
	if keep {
		flow.busy = false
	} else {
		if flow.timer != nil {
			flow.timer.Stop()
		}
		delete(s.flows, key)
	}
	return true
}

func (s *Service) CancelSub2APIPasswordFlow(id string, binding PasswordFlowBinding) {
	hash, err := passwordBindingHash(binding)
	if err != nil {
		return
	}
	state := s.passwordState()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.pruneLocked()
	key := sha256.Sum256([]byte(id))
	flow := state.flows[key]
	if flow != nil && flow.binding == hash {
		if flow.cancel != nil {
			flow.cancel()
		}
		if flow.timer != nil {
			flow.timer.Stop()
		}
		delete(state.flows, key)
	}
}

// A deployment trust change invalidates every pending flow, including operations
// currently waiting for upstream. The worker still owns its stack until it exits.
func (s *sub2APIPasswordState) invalidateConfiguration(configuration string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for key, flow := range s.flows {
		if flow.configuration != configuration {
			if flow.cancel != nil {
				flow.cancel()
			}
			if flow.timer != nil {
				flow.timer.Stop()
			}
			delete(s.flows, key)
		}
	}
}
