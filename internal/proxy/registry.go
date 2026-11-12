package proxy

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/smokecat/easel/internal/store"
	"github.com/smokecat/easel/internal/store/db"
)

// 暂停原因。
const (
	PausedProcessChanged = "process_changed"
	PausedUnreachable    = "unreachable"
)

// UnreachableAfter 是上游连续不可达多久后暂停分享。
const UnreachableAfter = 7 * 24 * time.Hour

// Registry 查询和维护上游登记。
type Registry struct {
	Store *store.Store
	Now   func() time.Time
	// Listener 探测监听端口的进程，默认 ListenerProcess；测试里替换。
	Listener func(port int) (Process, bool)
	// Reach 探测端口是否可达，默认 Reachable；测试里替换。
	Reach func(ctx context.Context, port int) bool
}

func (g *Registry) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Lookup 返回内容主机标签对应的上游；标签不是端口代理原型（或带 --vN 后缀）时返回 nil, nil。
func (g *Registry) Lookup(ctx context.Context, label string) (*db.Upstream, error) {
	p, err := g.Store.Q.GetPrototypeByLabel(ctx, label)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.ServeKind != "proxy" {
		return nil, nil
	}
	up, err := g.Store.Q.GetUpstream(ctx, p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &up, nil
}

// StillServing 报告上游登记是否仍和 up 一致（原型没被删、端口没换），供长连接定期校验。
func (g *Registry) StillServing(ctx context.Context, up db.Upstream) bool {
	cur, err := g.Store.Q.GetUpstream(ctx, up.PrototypeID)
	return err == nil && cur.Port == up.Port && cur.HostMode == up.HostMode
}

// Check 拨一遍所有上游：可达的更新最近可达时间，并核对监听进程是否还是注册时那个；
// 进程换了、或者连续 UnreachableAfter 不可达的，暂停分享。返回本轮新暂停的个数。
func (g *Registry) Check(ctx context.Context) (int, error) {
	ups, err := g.Store.Q.ListUpstreams(ctx)
	if err != nil {
		return 0, err
	}
	reach, listener := g.Reach, g.Listener
	if reach == nil {
		reach = Reachable
	}
	if listener == nil {
		listener = ListenerProcess
	}
	paused := 0
	for _, up := range ups {
		if ctx.Err() != nil {
			return paused, ctx.Err()
		}
		now := g.now()
		reason := ""
		if reach(ctx, int(up.Port)) {
			if err := g.Store.W.SetUpstreamSeen(ctx, db.SetUpstreamSeenParams{
				LastSeenAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, PrototypeID: up.PrototypeID,
			}); err != nil {
				return paused, err
			}
			if up.ProcessExe != "" || up.ProcessCwd != "" {
				if p, ok := listener(int(up.Port)); ok && (p.Exe != up.ProcessExe || p.Cwd != up.ProcessCwd) {
					reason = PausedProcessChanged
				}
			}
		} else {
			last := up.RegisteredAt
			if up.LastSeenAt.Valid && up.LastSeenAt.Int64 > last {
				last = up.LastSeenAt.Int64
			}
			if now.Sub(time.UnixMilli(last)) > UnreachableAfter {
				reason = PausedUnreachable
			}
		}
		if reason != "" && up.PausedReason == "" {
			if err := g.Store.W.PauseUpstream(ctx, db.PauseUpstreamParams{
				PausedReason: reason, PausedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, PrototypeID: up.PrototypeID,
			}); err != nil {
				return paused, err
			}
			paused++
		}
	}
	return paused, nil
}
