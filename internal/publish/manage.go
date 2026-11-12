package publish

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/smokecat/easel/internal/store/db"
)

// Prototype 是列表里的一个原型。
type Prototype struct {
	Name      string
	Label     string
	Title     string
	ServeKind string // static | proxy
	CreatedAt time.Time
	UpdatedAt time.Time
	// Current 是当前版本；还没有版本（比如只注册了端口代理）时为 nil。
	Current *VersionSummary
	// Versions 是版本总数。
	Versions   int64
	PreviewURL string
	// Upstream 只对端口代理原型有值。
	Upstream *UpstreamSummary
}

// UpstreamSummary 是端口代理的状态。
type UpstreamSummary struct {
	Port         int
	HostMode     string
	PausedReason string    // 空表示分享正常
	LastSeenAt   time.Time // 零值表示从没检测到可达
}

// VersionSummary 是一个版本的概要。
type VersionSummary struct {
	Number    int64
	Kind      string // site | assets
	Note      string
	Files     int64
	Bytes     int64
	CreatedAt time.Time
	URL       string // 固定到这个版本的地址
}

// List 列出所有原型，最近更新的在前。
func (s *Service) List(ctx context.Context) ([]Prototype, error) {
	rows, err := s.Store.Q.ListPrototypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Prototype, len(rows))
	for i, r := range rows {
		p := r.Prototype
		out[i] = Prototype{
			Name: p.Name, Label: p.Label, Title: p.Title, ServeKind: p.ServeKind,
			CreatedAt: time.UnixMilli(p.CreatedAt), UpdatedAt: time.UnixMilli(p.UpdatedAt),
			Versions: r.VersionCount, PreviewURL: s.Hosts.ContentOrigin(p.Label) + "/",
		}
		if r.UpPort.Valid {
			u := &UpstreamSummary{Port: int(r.UpPort.Int64), HostMode: r.UpHostMode.String, PausedReason: r.UpPausedReason.String}
			if r.UpLastSeenAt.Valid {
				u.LastSeenAt = time.UnixMilli(r.UpLastSeenAt.Int64)
			}
			out[i].Upstream = u
		}
		if r.RevNumber.Valid {
			out[i].Current = &VersionSummary{
				Number: r.RevNumber.Int64, Kind: r.RevKind.String, Note: r.RevNote.String,
				Files: r.RevFiles.Int64, Bytes: r.RevBytes.Int64, CreatedAt: time.UnixMilli(r.RevCreatedAt.Int64),
				URL: s.Hosts.ContentOrigin(VersionLabel(p.Label, r.RevNumber.Int64)) + "/",
			}
		}
	}
	return out, nil
}

// Get 返回一个原型；不存在时返回 not_found 错误。
func (s *Service) Get(ctx context.Context, name string) (*Prototype, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == name {
			return &all[i], nil
		}
	}
	return nil, notFound(name)
}

func notFound(name string) *Error {
	return errf(http.StatusNotFound, CodeNotFound, "no prototype named %q", name)
}

// Delete 删除原型：版本、文件清单、分享链接、访问会话随外键级联删除，立即生效。
// 不再被引用的 blob 由 SweepBlobs 回收。
func (s *Service) Delete(ctx context.Context, name string) error {
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		p, err := q.GetPrototypeByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound(name)
		}
		if err != nil {
			return err
		}
		// prototype.current_revision_id 指向 revision，先断开再删，免得级联顺序撞外键
		if err := q.ClearCurrentRevision(ctx, p.ID); err != nil {
			return err
		}
		_, err = q.DeletePrototype(ctx, p.ID)
		return err
	})
}

// BlobGrace 是 blob 回收的宽限期：比它新的未引用 blob 不删，留给还没提交的上传。
const BlobGrace = UploadTTL

// SweepBlobs 删除不再被任何版本、也不被待提交上传引用的 blob，返回删除的个数。
func (s *Service) SweepBlobs(ctx context.Context) (int, error) {
	keep := func(hash string) (bool, error) {
		if ok, err := s.Store.Q.BlobReferenced(ctx, hash); err != nil || ok {
			return ok, err
		}
		// 查询用 instr 在请求 JSON 里找哈希：64 位十六进制不会误撞别的内容（sqlc 解析不了 json_each）。
		// 注意 queries.sql 里的注释不要写中文：sqlc 按字节切查询文本，会切坏
		return s.Store.Q.BlobInPendingUpload(ctx, hash)
	}
	return s.Blobs.Sweep(keep, s.now().Add(-BlobGrace))
}
