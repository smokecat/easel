// Package auth 是唯一处理令牌、密码、会话、cookie 的地方：管理员密码与后台会话、API token、
// 分享链接、跨主机的一次性票据、内容主机上的访问会话、限速。
//
// 身份有三种：
//   - 本机：经 unix socket 连进来的请求（能读写 socket 文件就等于管理员，和 Docker socket 同一个模型）
//   - 管理员：后台主机上带有效会话 cookie 的请求
//   - API token：Authorization: Bearer easel_pat_…，按 scope 授权
//
// 后台的会话 cookie 只发给后台主机；内容主机各自有一份只对本主机有效的访问 cookie，由票据或分享链接换来。
// 所有会话、票据、令牌在库里只存 SHA-256，明文只出现在浏览器和 CLI 里。
package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/store"
	"github.com/smokecat/easel/internal/store/db"
)

// 寿命。
const (
	SessionTTL     = 30 * 24 * time.Hour // 后台会话（绝对寿命）
	TicketTTL      = 60 * time.Second    // 一次性票据
	AdminViewTTL   = 12 * time.Hour      // 管理员在内容主机上的访问会话
	ShareViewTTL   = 24 * time.Hour      // 访客的访问会话（不超过分享本身的有效期）
	tokenTouchStep = time.Minute         // last_used_at 至多每分钟写一次
)

// Service 持有鉴权所需的依赖和进程内状态（限速计数）。
type Service struct {
	Store *store.Store
	Hosts config.Hosts
	// Key 是服务端密钥，只用来派生分享令牌。
	Key []byte
	Log *slog.Logger
	// Now 默认 time.Now，测试里替换。
	Now func() time.Time

	loginLimit *limiter // 全局计数：不依赖客户端 IP（经反向代理拿不到可信的 IP）
	shareLimit *limiter // 按分享链接计数

	touchMu sync.Mutex
	touched map[int64]time.Time
}

// New 返回 Service。
func New(st *store.Store, hosts config.Hosts, key []byte, log *slog.Logger) *Service {
	return &Service{
		Store: st, Hosts: hosts, Key: key, Log: log, Now: time.Now,
		loginLimit: newLimiter(10*time.Minute, 10, 5*time.Second, 15*time.Minute),
		shareLimit: newLimiter(10*time.Minute, 10, 5*time.Second, 15*time.Minute),
		touched:    map[int64]time.Time{},
	}
}

func (s *Service) now() time.Time { return s.Now() }
func (s *Service) nowMs() int64   { return s.Now().UnixMilli() }

// Sweep 删除过期的后台会话、票据、访问会话。过期的记录本来就不会被接受，这里只是回收空间。
func (s *Service) Sweep(ctx context.Context) error {
	now := s.nowMs()
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredTickets(ctx, now); err != nil {
			return err
		}
		if err := q.DeleteExpiredViewSessions(ctx, now); err != nil {
			return err
		}
		return q.DeleteExpiredSessions(ctx, now)
	})
}

// Scope 是 API 权限范围。
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeShare Scope = "share"
)

// AllScopes 是可以授予 token 的全部 scope。
var AllScopes = []Scope{ScopeRead, ScopeWrite, ScopeShare}

// Kind 是身份类别。
type Kind string

const (
	KindLocal Kind = "local"
	KindAdmin Kind = "admin"
	KindToken Kind = "token"
)

// Identity 是一个请求的身份。
type Identity struct {
	Kind      Kind
	Scopes    []Scope // 只对 token 有意义；本机和管理员拥有全部权限
	TokenID   int64
	TokenName string

	sessionHash string // 管理员会话
}

func (id *Identity) has(scope Scope) bool {
	if id.Kind != KindToken {
		return true
	}
	for _, s := range id.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

type identityKey struct{}

// identityState 放进 context：身份，或者认证失败的原因（比如 token 过期），供 Check 报给客户端。
type identityState struct {
	id     *Identity
	denied *Denied
}

func withState(r *http.Request, st identityState) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, st))
}

func stateOf(r *http.Request) identityState {
	st, _ := r.Context().Value(identityKey{}).(identityState)
	return st
}

// FromRequest 返回请求的身份；未认证时为 nil。
func FromRequest(r *http.Request) *Identity { return stateOf(r).id }

// Denied 是鉴权失败：Status 是 401 或 403。
type Denied struct {
	Status  int
	Message string
	Hint    string
}

func (d *Denied) Error() string { return d.Message }

var errNoCredentials = &Denied{
	Status:  http.StatusUnauthorized,
	Message: "authentication required",
	Hint:    "run `easel login --server <url>` with an API token, or use easel on the server machine (unix socket)",
}

// Check 报告请求是否有 scope 权限，没有时返回 *Denied。
func Check(r *http.Request, scope Scope) error {
	st := stateOf(r)
	if st.id == nil {
		if st.denied != nil {
			return st.denied
		}
		return errNoCredentials
	}
	if !st.id.has(scope) {
		return &Denied{Status: http.StatusForbidden, Message: "this API token lacks the " + string(scope) + " scope",
			Hint: "create a token with `easel token create --scope " + string(scope) + ",…`"}
	}
	return nil
}

// CheckAdmin 报告请求是否来自管理员本人（本机或后台会话）。管理 token 等操作不允许用 token 做。
func CheckAdmin(r *http.Request) error {
	st := stateOf(r)
	switch {
	case st.id == nil && st.denied != nil:
		return st.denied
	case st.id == nil:
		return errNoCredentials
	case st.id.Kind == KindToken:
		return &Denied{Status: http.StatusForbidden, Message: "API tokens cannot do this",
			Hint: "run the command on the server machine (unix socket) or use the admin web UI"}
	}
	return nil
}

// CheckLocal 报告请求是否经本机 unix socket 进来。
func CheckLocal(r *http.Request) error {
	if id := FromRequest(r); id != nil && id.Kind == KindLocal {
		return nil
	}
	return &Denied{Status: http.StatusForbidden, Message: "only available over the local unix socket",
		Hint: "run this command on the machine where `easel serve` runs"}
}

// TrustLocal 把经过 h 的请求标记为本机管理员。只能用在 unix socket 监听器上。
func TrustLocal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, withState(r, identityState{id: &Identity{Kind: KindLocal}}))
	})
}

// Identify 是后台主机（TCP）的中间件：按 Authorization 头或会话 cookie 认出身份，放进 context。
// 不拒绝请求，拒绝由各处理器调 Check 决定。用会话 cookie 的非安全方法请求必须同源（CSRF）。
func (s *Service) Identify(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, withState(r, s.identify(r)))
	})
}

func (s *Service) identify(r *http.Request) identityState {
	if authz := r.Header.Get("Authorization"); authz != "" {
		scheme, tok, _ := strings.Cut(authz, " ")
		if !strings.EqualFold(scheme, "Bearer") || tok == "" {
			return identityState{denied: &Denied{Status: http.StatusUnauthorized, Message: "unsupported Authorization header; use Bearer <token>"}}
		}
		id, err := s.authenticateToken(r.Context(), strings.TrimSpace(tok))
		if err != nil {
			var d *Denied
			if errors.As(err, &d) {
				return identityState{denied: d}
			}
			s.Log.Error("token lookup", "err", err)
			return identityState{denied: &Denied{Status: http.StatusInternalServerError, Message: "token lookup failed"}}
		}
		return identityState{id: id}
	}
	sess, err := s.sessionFromRequest(r)
	if err != nil || sess == nil {
		if err != nil {
			s.Log.Error("session lookup", "err", err)
		}
		return identityState{}
	}
	if !safeMethod(r.Method) && !sameOrigin(r, s.Hosts.AppOrigin()) {
		return identityState{denied: &Denied{Status: http.StatusForbidden, Message: "cross-site request rejected"}}
	}
	return identityState{id: &Identity{Kind: KindAdmin, sessionHash: sess.IDHash}}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin 校验浏览器发来的非安全请求是否来自 origin 本身。现代浏览器对 POST 一定带 Origin；
// 两个头都没有时拒绝（用 cookie 认证的只有浏览器）。
func sameOrigin(r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// cookieName 在 https 下用 __Host- 前缀（必须 Secure、不能带 Domain、Path=/，别的主机种不出同名 cookie）；
// 本机 http 开发时浏览器不接受 __Host- cookie，退回普通名字。
func cookieName(base string, secure bool) string {
	if secure {
		return "__Host-" + base
	}
	return base
}

// newCookie 返回 easel 自己的 cookie：HttpOnly、SameSite=Lax、host-only。maxAge <= 0 表示删除。
// SameSite 用 Lax 而不是 Strict：换票是从别的主机跳过来的顶层导航，Strict 的 cookie 不会带上。
func newCookie(name, value string, secure bool, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name: cookieName(name, secure), Value: value, Path: "/",
		Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(maxAge / time.Second),
	}
	if c.MaxAge <= 0 {
		if value != "" {
			c.MaxAge = 1
		} else {
			c.MaxAge = -1
		}
	}
	return c
}

// noRows 判断查询是否没有结果。
func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func nullMs(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

func msTime(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}
