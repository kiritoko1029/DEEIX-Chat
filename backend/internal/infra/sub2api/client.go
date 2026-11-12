// Package sub2api 实现限流量、禁止重定向且仅允许公网的只读协议客户端。
package sub2api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2api"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
)

const (
	maxResponseBytes         = 2 << 20
	maxPages                 = 20
	pageSize                 = 100
	maxRejectedResponseBytes = 16 << 10
)

type Client struct{ http *http.Client }

func New() *Client {
	c := security.NewOutboundHTTPClient(security.NewPublicOnlyOutboundPolicy(), 10*time.Second)
	// Login credentials never use an environment proxy whose resolver could
	// bypass the connect-time public-IP check.
	transport := c.Transport.(*http.Transport)
	transport.Proxy = nil
	transport.DialContext = security.NewOutboundDialContext(security.NewPublicOnlyOutboundPolicy(), 5*time.Second, 30*time.Second)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: c}
}

func (c *Client) Fetch(ctx context.Context, baseURL, token string, userID int64, readSettings bool) (port.Snapshot, error) {
	var out port.Snapshot
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return out, port.ErrUnavailable
	}
	if security.ValidateOutboundHTTPURL(baseURL, security.NewPublicOnlyOutboundPolicy()) != nil {
		return out, port.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/api/v1") {
		base += "/api/v1"
	}
	if err := c.get(ctx, base+"/auth/me", token, port.StageAuthMe, &out.User); err != nil {
		return port.Snapshot{}, err
	}
	if out.User.ID != userID || out.User.ID <= 0 {
		return port.Snapshot{}, port.NewRejected(port.StageAuthMe, port.ReasonUserIDMismatch)
	}
	if out.User.Status != "active" {
		return port.Snapshot{}, port.NewRejected(port.StageAuthMe, port.ReasonUserInactive)
	}
	seen := map[int64]bool{}
	finished := false
	for page := 1; page <= maxPages; page++ {
		var data struct {
			Items    []port.Key `json:"items"`
			Total    *int       `json:"total"`
			Page     int        `json:"page"`
			Pages    int        `json:"pages"`
			PageSize int        `json:"page_size"`
		}
		endpoint := base + "/keys?page=" + strconv.Itoa(page) + "&page_size=100&sort_by=id&sort_order=asc"
		if err := c.get(ctx, endpoint, token, port.StageKeys, &data); err != nil {
			return port.Snapshot{}, err
		}
		if data.Total == nil || *data.Total < 0 || *data.Total > maxPages*pageSize || data.Page != page || data.Pages < 1 || data.Pages > maxPages || len(data.Items) > pageSize || data.PageSize != pageSize {
			return port.Snapshot{}, port.ErrUnavailable
		}
		for _, key := range data.Items {
			// Even disabled or unsupported keys must belong to the verified user.
			if key.UserID != userID {
				return port.Snapshot{}, port.NewRejected(port.StageKeys, port.ReasonKeyOwnerMismatch)
			}
			if key.ID <= 0 || seen[key.ID] {
				return port.Snapshot{}, port.ErrUnavailable
			}
			seen[key.ID] = true
			out.Keys = append(out.Keys, key)
		}
		if page == data.Pages {
			if len(out.Keys) != *data.Total {
				return port.Snapshot{}, port.ErrUnavailable
			}
			finished = true
			break
		}
		if len(data.Items) == 0 {
			return port.Snapshot{}, port.ErrUnavailable
		}
	}
	if !finished {
		return port.Snapshot{}, port.ErrUnavailable
	}
	if readSettings {
		var settings struct {
			APIBaseURL string `json:"api_base_url"`
		}
		if err := c.get(ctx, base+"/settings/public", "", port.StageIdentity, &settings); err != nil {
			return port.Snapshot{}, err
		}
		out.APIBaseURL = settings.APIBaseURL
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, target, token, stage string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return port.ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return port.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return rejectedResponse(resp, stage)
	}
	if resp.StatusCode != http.StatusOK {
		return port.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return port.ErrUnavailable
	}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return port.ErrUnavailable
	}
	if json.Unmarshal(envelope.Data, result) != nil {
		return port.ErrUnavailable
	}
	return nil
}

// Only recognized top-level reason/code enums from sub2api's JWT middleware
// are mapped. Older deployments return a string code rather than reason.
// Malformed, oversized or unknown bodies retain the generic HTTP class.
func rejectedResponse(resp *http.Response, stage string) error {
	reason := port.ReasonUpstreamUnauthorized
	if resp.StatusCode == http.StatusForbidden {
		reason = port.ReasonUpstreamForbidden
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRejectedResponseBytes+1))
	if err == nil && len(body) <= maxRejectedResponseBytes {
		var envelope struct {
			Reason string          `json:"reason"`
			Code   json.RawMessage `json:"code"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			enum := envelope.Reason
			if enum == "" {
				// Numeric HTTP-style codes stay generic; only string enums can match.
				_ = json.Unmarshal(envelope.Code, &enum)
			}
			switch enum {
			case "SESSION_BINDING_MISMATCH":
				reason = port.ReasonSessionBindingMismatch
			case "TOKEN_EXPIRED":
				reason = port.ReasonTokenExpired
			case "INVALID_TOKEN", "TOKEN_REVOKED", "EMPTY_TOKEN", "INVALID_AUTH_HEADER":
				reason = port.ReasonTokenInvalid
			case "USER_INACTIVE":
				reason = port.ReasonUserInactive
			}
		}
	}
	return port.NewRejected(stage, reason)
}
