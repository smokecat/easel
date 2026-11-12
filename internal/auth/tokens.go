package auth

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/store/db"
)

// TokenPrefix 是 API token 的固定前缀，便于密钥扫描工具和日志打码识别。
const TokenPrefix = "easel_pat_"

// Token 是 API token 的元数据（不含明文）。
type Token struct {
	ID         int64
	Name       string
	Hint       string // 明文的前缀部分，如 easel_pat_AbCd
	Scopes     []Scope
	CreatedAt  time.Time
	ExpiresAt  time.Time // 零值表示不过期
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// ParseScopes 解析逗号分隔的 scope 列表，去重并按固定顺序排列。
func ParseScopes(s string) ([]Scope, error) {
	var out []Scope
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sc := Scope(part)
		if !slices.Contains(AllScopes, sc) {
			return nil, fmt.Errorf("unknown scope %q (valid: read, write, share)", part)
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one scope is required")
	}
	slices.SortFunc(out, func(a, b Scope) int { return slices.Index(AllScopes, a) - slices.Index(AllScopes, b) })
	return out, nil
}

func joinScopes(sc []Scope) string {
	parts := make([]string, len(sc))
	for i, s := range sc {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}

func tokenFromRow(t db.ApiToken) Token {
	sc, _ := ParseScopes(t.Scopes)
	return Token{
		ID: t.ID, Name: t.Name, Hint: t.Hint, Scopes: sc,
		CreatedAt: time.UnixMilli(t.CreatedAt), ExpiresAt: msTime(t.ExpiresAt),
		LastUsedAt: msTime(t.LastUsedAt), RevokedAt: msTime(t.RevokedAt),
	}
}

// CreateToken 创建 API token，返回元数据和明文（只此一次）。ttl 为 0 表示不过期。
func (s *Service) CreateToken(ctx context.Context, name string, scopes []Scope, ttl time.Duration) (Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Token{}, "", &Denied{Status: http.StatusBadRequest, Message: "token name must be 1-100 characters"}
	}
	if len(scopes) == 0 {
		return Token{}, "", &Denied{Status: http.StatusBadRequest, Message: "at least one scope is required"}
	}
	if ttl < 0 {
		return Token{}, "", &Denied{Status: http.StatusBadRequest, Message: "expiry must be positive"}
	}
	plain := TokenPrefix + randomToken(32)
	now := s.now()
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}
	row, err := s.Store.W.CreateAPIToken(ctx, db.CreateAPITokenParams{
		Name: name, TokenHash: hashToken(plain), Hint: plain[:len(TokenPrefix)+4], Scopes: joinScopes(scopes),
		CreatedAt: now.UnixMilli(), ExpiresAt: nullMs(exp),
	})
	if err != nil {
		return Token{}, "", err
	}
	return tokenFromRow(row), plain, nil
}

// ListTokens 列出所有 token（含已撤销的）。
func (s *Service) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.Store.Q.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Token, len(rows))
	for i, r := range rows {
		out[i] = tokenFromRow(r)
	}
	return out, nil
}

// RevokeToken 撤销 token，立即生效。返回 false 表示不存在或已撤销。
func (s *Service) RevokeToken(ctx context.Context, id int64) (bool, error) {
	n, err := s.Store.W.RevokeAPIToken(ctx, db.RevokeAPITokenParams{RevokedAt: nullMs(s.now()), ID: id})
	return n > 0, err
}

func (s *Service) authenticateToken(ctx context.Context, plain string) (*Identity, error) {
	invalid := &Denied{Status: http.StatusUnauthorized, Message: "invalid API token",
		Hint: "the token may have been revoked; create a new one with `easel token create` and run `easel login` again"}
	if !strings.HasPrefix(plain, TokenPrefix) {
		return nil, invalid
	}
	row, err := s.Store.Q.GetAPITokenByHash(ctx, hashToken(plain))
	if noRows(err) {
		return nil, invalid
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if row.RevokedAt.Valid {
		return nil, invalid
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Int64 <= now.UnixMilli() {
		return nil, &Denied{Status: http.StatusUnauthorized, Message: "API token expired",
			Hint: "create a new one with `easel token create` and run `easel login` again"}
	}
	s.touchToken(ctx, row.ID, now)
	t := tokenFromRow(row)
	return &Identity{Kind: KindToken, Scopes: t.Scopes, TokenID: t.ID, TokenName: t.Name}, nil
}

// touchToken 更新 last_used_at，同一个 token 至多每分钟写一次库。
func (s *Service) touchToken(ctx context.Context, id int64, now time.Time) {
	s.touchMu.Lock()
	last, ok := s.touched[id]
	if ok && now.Sub(last) < tokenTouchStep {
		s.touchMu.Unlock()
		return
	}
	s.touched[id] = now
	s.touchMu.Unlock()
	if err := s.Store.W.TouchAPIToken(ctx, db.TouchAPITokenParams{LastUsedAt: nullMs(now), ID: id}); err != nil {
		s.Log.Warn("touch token", "err", err)
	}
}
