// Package auth 是唯一处理令牌、密码、会话的地方：管理员会话、API token、分享链接、跨子域票据、限速。
//
// 目前只有「本机受信」一种身份：经 unix socket 连进来的请求视为本机管理员（能读写 socket 文件就等于有写权限，
// 和 Docker socket 同一个模型）。API token 和后台登录落地前，TCP 上的写 API 一律拒绝。
package auth

import (
	"context"
	"net/http"
)

type localKey struct{}

// TrustLocal 把经过 h 的请求标记为本机受信。只能用在 unix socket 监听器上。
func TrustLocal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localKey{}, true)))
	})
}

// Scope 是 API 权限范围。
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeShare Scope = "share"
)

// Allowed 报告请求是否有 scope 权限。
func Allowed(r *http.Request, _ Scope) bool {
	local, _ := r.Context().Value(localKey{}).(bool)
	return local
}
