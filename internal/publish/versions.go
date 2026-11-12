package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/smokecat/easel/internal/store/db"
)

// Version 是版本历史里的一项。
type Version struct {
	VersionSummary
	Origin  string // upload | rollback | build
	Current bool
}

// Versions 列出原型的全部版本，最新的在前。
func (s *Service) Versions(ctx context.Context, name string) ([]Version, error) {
	p, err := s.Store.Q.GetPrototypeByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound(name)
	}
	if err != nil {
		return nil, err
	}
	revs, err := s.Store.Q.ListRevisions(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Version, len(revs))
	for i, r := range revs {
		out[i] = Version{
			VersionSummary: s.summary(p.Label, r),
			Origin:         r.Origin,
			Current:        p.CurrentRevisionID.Valid && p.CurrentRevisionID.Int64 == r.ID,
		}
	}
	return out, nil
}

func (s *Service) summary(label string, r db.Revision) VersionSummary {
	return VersionSummary{
		Number: r.Number, Kind: r.Kind, Note: r.Note, Files: r.FileCount, Bytes: r.TotalBytes,
		CreatedAt: time.UnixMilli(r.CreatedAt), URL: s.Hosts.ContentOrigin(VersionLabel(label, r.Number)) + "/",
	}
}

// Rollback 把原型恢复到第 number 版：新建一个 origin=rollback 的版本，内容和第 number 版相同。
// 历史保持线性、不可变；目标就是当前版本时不产生新版本（Unchanged）。
func (s *Service) Rollback(ctx context.Context, name string, number int64) (*Result, error) {
	var res Result
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		p, err := q.GetPrototypeByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound(name)
		}
		if err != nil {
			return err
		}
		if p.ServeKind != ServeStatic {
			return errf(http.StatusConflict, CodeKindConflict, "prototype %q is a port proxy; it has no versions", name)
		}
		target, err := q.GetRevisionByNumber(ctx, db.GetRevisionByNumberParams{PrototypeID: p.ID, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return errf(http.StatusNotFound, CodeNotFound, "prototype %q has no version %d", name, number)
		}
		if err != nil {
			return err
		}
		if p.CurrentRevisionID.Valid && p.CurrentRevisionID.Int64 == target.ID {
			res = s.result(p, target, false, true)
			return nil
		}
		next, err := q.NextRevisionNumber(ctx, p.ID)
		if err != nil {
			return err
		}
		now := s.now().UnixMilli()
		rev, err := q.CreateRevision(ctx, db.CreateRevisionParams{
			PrototypeID: p.ID, Number: next, Origin: "rollback", Kind: target.Kind, Entry: target.Entry, Spa: target.Spa,
			Note: fmt.Sprintf("rollback to v%d", number), ManifestHash: target.ManifestHash,
			FileCount: target.FileCount, TotalBytes: target.TotalBytes, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := q.CopyArtifacts(ctx, db.CopyArtifactsParams{ToRevision: rev.ID, FromRevision: target.ID}); err != nil {
			return err
		}
		if err := q.SetCurrentRevision(ctx, db.SetCurrentRevisionParams{
			CurrentRevisionID: sql.NullInt64{Int64: rev.ID, Valid: true}, UpdatedAt: now, ID: p.ID,
		}); err != nil {
			return err
		}
		res = s.result(p, rev, false, false)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}
