package publish

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/smokecat/easel/internal/store/db"
)

// ExposeRequest 是 PUT /api/v1/prototypes/{name}/upstream 的请求体。
type ExposeRequest struct {
	Port int `json:"port"`
	// HostMode 是 localhost（默认，对上游伪装成 localhost:<port>）或 preserve（保留公网 Host）。
	HostMode string `json:"host_mode,omitempty"`
	Source   Source `json:"source"`
}

// ExposeResult 是注册端口代理的结果。字段只增不改。
type ExposeResult struct {
	Prototype ResultPrototype `json:"prototype"` // kind 固定为 proxy
	Port      int             `json:"port"`
	HostMode  string          `json:"host_mode"`
	URLs      ResultURLs      `json:"urls"` // 只有 preview
	Access    ResultAccess    `json:"access"`
	// Reachable 表示注册时端口上有进程在监听；false 时仍然注册成功（dev server 可能稍后才启动）。
	Reachable bool `json:"reachable"`
	// Resumed 表示这次注册解除了之前的暂停。
	Resumed  bool     `json:"resumed"`
	Warnings []string `json:"warnings"`
}

// Listener 是注册时监听端口的进程（由调用方探测；拿不到时为零值，不做进程身份检查）。
type Listener struct {
	Exe, Cwd string
}

// Expose 把原型注册（或更新）为端口代理。已有的静态原型、被别的原型占用的端口都拒绝；
// 再次注册同一个原型会更新端口、模式和进程身份，并解除暂停。
func (s *Service) Expose(ctx context.Context, name string, req ExposeRequest, reachable bool, l Listener) (*ExposeResult, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "port must be between 1 and 65535")
	}
	switch req.HostMode {
	case "":
		req.HostMode = "localhost"
	case "localhost", "preserve":
	default:
		return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "host_mode must be localhost or preserve")
	}
	now := s.now().UnixMilli()
	res := &ExposeResult{Port: req.Port, HostMode: req.HostMode, Reachable: reachable, Warnings: []string{},
		Access: ResultAccess{PreviewRequiresLogin: s.PreviewRequiresLogin}}
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if other, err := q.GetUpstreamByPort(ctx, int64(req.Port)); err == nil && other.PrototypeName != name {
			return errf(http.StatusConflict, CodePortConflict, "port %d is already exposed as %q", req.Port, other.PrototypeName).
				withHint("remove it with `easel rm " + other.PrototypeName + "`, or expose it under that name")
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		proto, err := q.GetPrototypeByName(ctx, name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			proto, err = q.CreateProxyPrototype(ctx, db.CreateProxyPrototypeParams{
				Name: name, Label: NewLabel(name), SourceFingerprint: req.Source.Fingerprint, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
			res.Prototype.Created = true
		case err != nil:
			return err
		case proto.ServeKind != ServeProxy:
			return errf(http.StatusConflict, CodeKindConflict, "prototype %q holds pushed files; it cannot become a port proxy", name).
				withHint("expose under another --name, or remove it first with `easel rm " + name + "`")
		case req.Source.Inferred && proto.SourceFingerprint != req.Source.Fingerprint:
			return errf(http.StatusConflict, CodeNameConflict, "prototype %q already exists and was exposed from a different source", name).
				withHint("pass --name <slug> to update it anyway, or choose another name")
		default:
			if prev, err := q.GetUpstream(ctx, proto.ID); err == nil {
				res.Resumed = prev.PausedReason != ""
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := q.TouchPrototype(ctx, db.TouchPrototypeParams{UpdatedAt: now, ID: proto.ID}); err != nil {
				return err
			}
		}
		var seen sql.NullInt64
		if reachable {
			seen = sql.NullInt64{Int64: now, Valid: true}
		}
		if err := q.UpsertUpstream(ctx, db.UpsertUpstreamParams{
			PrototypeID: proto.ID, Port: int64(req.Port), HostMode: req.HostMode,
			ProcessExe: l.Exe, ProcessCwd: l.Cwd, RegisteredAt: now, LastSeenAt: seen,
		}); err != nil {
			return err
		}
		res.Prototype.Name, res.Prototype.Kind = proto.Name, ServeProxy
		res.URLs.Preview = s.Hosts.ContentOrigin(proto.Label) + "/"
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !reachable {
		res.Warnings = append(res.Warnings, "nothing is listening on this port yet; the preview shows an error page until the dev server starts")
	}
	return res, nil
}
