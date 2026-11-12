package api

// 鉴权和分享相关 API 的请求、响应类型，服务端和 CLI 共用。字段只增不改。
// 时间一律是 RFC 3339（UTC）；可空的时间为 null。

// WhoAmI 是 GET /api/v1/whoami 的响应。
type WhoAmI struct {
	Kind   string     `json:"kind"` // local | admin | token
	Scopes []string   `json:"scopes"`
	Token  *TokenInfo `json:"token,omitempty"`
	Server ServerInfo `json:"server"`
}

// ServerInfo 是服务端的公开地址。
type ServerInfo struct {
	AppURL     string `json:"app_url"`
	ContentURL string `json:"content_url"`
}

// SetPasswordRequest 是 PUT /api/v1/admin/password 的请求体。
type SetPasswordRequest struct {
	Password string `json:"password"`
}

// CreateTokenRequest 是 POST /api/v1/tokens 的请求体。
type CreateTokenRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresIn 是有效秒数，0 表示不过期。
	ExpiresIn int64 `json:"expires_in_seconds,omitempty"`
}

// TokenInfo 是一个 API token。Token（明文）只在创建的响应里出现一次。
type TokenInfo struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Hint       string   `json:"hint"`
	Scopes     []string `json:"scopes"`
	Status     string   `json:"status"` // active | expired | revoked
	CreatedAt  string   `json:"created_at"`
	ExpiresAt  *string  `json:"expires_at"`
	LastUsedAt *string  `json:"last_used_at"`
	RevokedAt  *string  `json:"revoked_at"`
	Token      string   `json:"token,omitempty"`
}

// TokenList 是 GET /api/v1/tokens 的响应。
type TokenList struct {
	Tokens []TokenInfo `json:"tokens"`
}

// CreateShareRequest 是 POST /api/v1/prototypes/{name}/shares 的请求体。
type CreateShareRequest struct {
	Note     string `json:"note,omitempty"`
	Password string `json:"password,omitempty"`
	// ExpiresIn 是有效秒数，0 表示不过期。
	ExpiresIn int64 `json:"expires_in_seconds,omitempty"`
}

// ShareInfo 是一个分享链接。URL 随时可以再取。
type ShareInfo struct {
	ID          int64   `json:"id"`
	Prototype   string  `json:"prototype"`
	URL         string  `json:"url"`
	Note        string  `json:"note"`
	HasPassword bool    `json:"has_password"`
	Status      string  `json:"status"` // active | expired | revoked
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
	RevokedAt   *string `json:"revoked_at"`
}

// ShareList 是 GET /api/v1/shares 的响应。
type ShareList struct {
	Shares []ShareInfo `json:"shares"`
}
