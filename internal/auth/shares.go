package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/store/db"
)

// SharePathPrefix 是内容主机上分享入口的路径前缀：<内容源>/_easel/s/<token>。
const SharePathPrefix = "/_easel/s/"

// Share 是分享链接的元数据。URL 由服务端密钥重新派生，可以随时再次显示。
type Share struct {
	ID          int64
	Prototype   string // 原型名
	URL         string
	Note        string
	HasPassword bool
	CreatedAt   time.Time
	ExpiresAt   time.Time // 零值表示不过期
	RevokedAt   time.Time
}

// Status 返回 active / expired / revoked。
func (sh Share) Status(now time.Time) string {
	switch {
	case !sh.RevokedAt.IsZero():
		return "revoked"
	case !sh.ExpiresAt.IsZero() && !now.Before(sh.ExpiresAt):
		return "expired"
	}
	return "active"
}

// ShareOptions 是创建分享链接的参数。
type ShareOptions struct {
	Note     string
	Password string        // 空表示不需要密码
	TTL      time.Duration // 0 表示不过期
}

func (s *Service) shareURL(label, nonce string) string {
	return s.Hosts.ContentOrigin(label) + SharePathPrefix + deriveShareToken(s.Key, nonce)
}

func (s *Service) shareFromRow(row db.Share, name, label string) Share {
	return Share{
		ID: row.ID, Prototype: name, URL: s.shareURL(label, row.Nonce), Note: row.Note,
		HasPassword: row.PasswordHash != "", CreatedAt: time.UnixMilli(row.CreatedAt),
		ExpiresAt: msTime(row.ExpiresAt), RevokedAt: msTime(row.RevokedAt),
	}
}

// CreateShare 给原型创建分享链接。分享授予的是原型的主地址（跟随最新版本）。
func (s *Service) CreateShare(ctx context.Context, prototype string, opts ShareOptions) (Share, error) {
	if len(opts.Note) > 200 {
		return Share{}, &Denied{Status: http.StatusBadRequest, Message: "note must be at most 200 characters"}
	}
	if opts.TTL < 0 {
		return Share{}, &Denied{Status: http.StatusBadRequest, Message: "expiry must be positive"}
	}
	var pwHash string
	if opts.Password != "" {
		if err := CheckPasswordPolicy(opts.Password); err != nil {
			return Share{}, &Denied{Status: http.StatusBadRequest, Message: "share " + err.Error()}
		}
		pwHash = hashPassword(opts.Password)
	}
	proto, err := s.Store.Q.GetPrototypeByName(ctx, prototype)
	if err != nil {
		return Share{}, err // 由调用方把 sql.ErrNoRows 转成 404
	}
	nonce := randomToken(16)
	now := s.now()
	var exp time.Time
	if opts.TTL > 0 {
		exp = now.Add(opts.TTL)
	}
	var row db.Share
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.CreateShare(ctx, db.CreateShareParams{
			PrototypeID: proto.ID, Nonce: nonce, TokenHash: hashToken(deriveShareToken(s.Key, nonce)),
			Note: strings.TrimSpace(opts.Note), PasswordHash: pwHash, CreatedAt: now.UnixMilli(), ExpiresAt: nullMs(exp),
		})
		return err
	})
	if err != nil {
		return Share{}, err
	}
	return s.shareFromRow(row, proto.Name, proto.Label), nil
}

// ListShares 列出分享链接；prototype 为空时列出全部。
func (s *Service) ListShares(ctx context.Context, prototype string) ([]Share, error) {
	var filter sql.NullInt64
	if prototype != "" {
		proto, err := s.Store.Q.GetPrototypeByName(ctx, prototype)
		if err != nil {
			return nil, err
		}
		filter = sql.NullInt64{Int64: proto.ID, Valid: true}
	}
	rows, err := s.Store.Q.ListShares(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]Share, len(rows))
	for i, r := range rows {
		out[i] = s.shareFromRow(r.Share, r.PrototypeName, r.PrototypeLabel)
	}
	return out, nil
}

// RevokeShare 撤销分享链接并删除它的访问会话，立即生效。返回 false 表示不存在或已撤销。
func (s *Service) RevokeShare(ctx context.Context, id int64) (bool, error) {
	var n int64
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		if n, err = q.RevokeShare(ctx, db.RevokeShareParams{RevokedAt: nullMs(s.now()), ID: id}); err != nil {
			return err
		}
		return q.DeleteShareViewSessions(ctx, sql.NullInt64{Int64: id, Valid: true})
	})
	return n > 0, err
}

// shareActive 报告分享是否仍然有效（未撤销、未过期）。
func shareActive(sh db.Share, now time.Time) bool {
	return !sh.RevokedAt.Valid && (!sh.ExpiresAt.Valid || sh.ExpiresAt.Int64 > now.UnixMilli())
}
