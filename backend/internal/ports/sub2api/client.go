// Package sub2api 定义可信 sub2api 实例的只读认证接口。
package sub2api

import (
	"context"
	"errors"
	"time"
)

var (
	ErrRejected    = errors.New("sub2api authentication rejected")
	ErrUnavailable = errors.New("sub2api unavailable")
)

type User struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	ID       int64  `json:"id"`
	Status   string `json:"status"`
}

type Key struct {
	Name      string     `json:"name"`
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	Key       string     `json:"key"`
	Status    string     `json:"status"`
	Quota     float64    `json:"quota"`
	QuotaUsed float64    `json:"quota_used"`
	ExpiresAt *time.Time `json:"expires_at"`
	GroupID   *int64     `json:"group_id"`
	Group     *Group     `json:"group"`
}

type Group struct {
	Name     string `json:"name"`
	ID       int64  `json:"id"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

type Snapshot struct {
	User       User
	Keys       []Key
	APIBaseURL string
}

// Client 不保存登录 token，不接受请求方指定的主机或转发头。
type Client interface {
	Fetch(ctx context.Context, baseURL, token string, userID int64, readSettings bool) (Snapshot, error)
}
