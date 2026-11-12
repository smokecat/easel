package publish

import (
	"archive/tar"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/smokecat/easel/internal/blob"
	"github.com/smokecat/easel/internal/config"
	"github.com/smokecat/easel/internal/store"
	"github.com/smokecat/easel/internal/store/db"
)

// 原型的 serve_kind。
const (
	ServeStatic = "static"
	ServeProxy  = "proxy"
)

// UploadTTL 是三步协议里上传会话的有效期。
const UploadTTL = time.Hour

// Service 是服务端的推送流程。
type Service struct {
	Store  *store.Store
	Blobs  *blob.Store
	Hosts  config.Hosts
	Limits Limits
	// PreviewRequiresLogin 如实告诉客户端预览链接是否需要登录（鉴权落地前为 false）。
	PreviewRequiresLogin bool
	Now                  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// plan 是校验过的一次推送。
type plan struct {
	name   string
	req    CreateRequest
	files  []FileEntry // 按路径排序
	total  int64
	kind   string
	entry  string
	mhash  string
	hashes map[string]int64 // 去重后的哈希 → 大小
}

func (s *Service) newPlan(name string, req CreateRequest) (*plan, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	files, total, err := checkFiles(req.Files, s.Limits)
	if err != nil {
		return nil, err
	}
	if len(req.Note) > 4096 {
		return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "note is too long")
	}
	p := &plan{name: name, req: req, files: files, total: total, mhash: manifestHash(files), hashes: map[string]int64{}}
	p.kind, p.entry = DetectKind(files)
	for _, f := range files {
		p.hashes[f.SHA256] = f.Size
	}
	return p, nil
}

// checkSource 拒绝「推断出的名字命中一个来源不同的已有原型」。显式给名字时不检查。
func checkSource(proto db.Prototype, src Source) error {
	if proto.ServeKind != ServeStatic {
		return errf(http.StatusConflict, CodeKindConflict,
			"prototype %q is a port proxy (easel expose); it cannot receive pushed files", proto.Name).
			withHint("push under another --name, or remove it first with `easel rm " + proto.Name + "`")
	}
	if src.Inferred && proto.SourceFingerprint != src.Fingerprint {
		return errf(http.StatusConflict, CodeNameConflict,
			"prototype %q already exists and was pushed from a different source", proto.Name).
			withHint("pass --name <slug> to push to it anyway, or choose another name")
	}
	return nil
}

// unchanged 判断内容和当前版本是否一致；一致时返回当前版本。
func (s *Service) unchanged(ctx context.Context, q *db.Queries, proto db.Prototype, p *plan) (*db.Revision, error) {
	if !proto.CurrentRevisionID.Valid {
		return nil, nil
	}
	rev, err := q.GetRevision(ctx, proto.CurrentRevisionID.Int64)
	if err != nil {
		return nil, err
	}
	if rev.ManifestHash == p.mhash && (rev.Spa != 0) == p.req.SPA {
		return &rev, nil
	}
	return nil, nil
}

// Create 是第 1 步：校验清单，返回缺失的哈希和上传会话。
func (s *Service) Create(ctx context.Context, name string, req CreateRequest) (*CreateResponse, error) {
	p, err := s.newPlan(name, req)
	if err != nil {
		return nil, err
	}
	proto, err := s.Store.Q.GetPrototypeByName(ctx, name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		if err := checkSource(proto, req.Source); err != nil {
			return nil, err
		}
		rev, err := s.unchanged(ctx, s.Store.Q, proto, p)
		if err != nil {
			return nil, err
		}
		if rev != nil {
			res := s.result(proto, *rev, false, true)
			return &CreateResponse{Unchanged: true, Missing: []string{}, Result: &res}, nil
		}
	}

	resp := &CreateResponse{Missing: []string{}}
	for h, size := range p.hashes {
		if !s.Blobs.Has(h) {
			resp.Missing = append(resp.Missing, h)
			resp.MissingBytes += size
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	now := s.now()
	resp.UploadID = NewID()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredUploads(ctx, now.UnixMilli()); err != nil {
			return err
		}
		return q.CreateUpload(ctx, db.CreateUploadParams{
			ID: resp.UploadID, Name: name, Request: string(body),
			CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(UploadTTL).UnixMilli(),
		})
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) loadUpload(ctx context.Context, id string) (*plan, error) {
	up, err := s.Store.Q.GetUpload(ctx, db.GetUploadParams{ID: id, ExpiresAt: s.now().UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errf(http.StatusNotFound, CodeNotFound, "upload %q not found or expired", id).
			withHint("start the push again")
	}
	if err != nil {
		return nil, err
	}
	var req CreateRequest
	if err := json.Unmarshal([]byte(up.Request), &req); err != nil {
		return nil, fmt.Errorf("upload %s: %w", id, err)
	}
	return s.newPlan(up.Name, req)
}

// PutBlobs 是第 2 步：从 tar 流里收文件，条目名是哈希，只接受本次清单里出现过的哈希。
func (s *Service) PutBlobs(ctx context.Context, uploadID string, r io.Reader) (*BlobsResponse, error) {
	p, err := s.loadUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	resp := &BlobsResponse{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return resp, nil
		}
		if err != nil {
			return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "read tar stream: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		size, ok := p.hashes[hdr.Name]
		if !ok {
			return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "blob %q is not part of this upload", hdr.Name)
		}
		if hdr.Size != size {
			return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "blob %s: size %d does not match manifest (%d)", hdr.Name, hdr.Size, size)
		}
		n, err := s.Blobs.Put(tr, hdr.Name, size)
		if errors.Is(err, blob.ErrHashMismatch) || errors.Is(err, blob.ErrTooLarge) {
			return nil, errf(http.StatusBadRequest, CodeInvalidRequest, "blob %s: %v", hdr.Name, err)
		}
		if err != nil {
			return nil, err
		}
		resp.Received++
		resp.ReceivedBytes += n
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// Commit 是第 3 步：确认 blob 齐全后原子地生成新版本。
func (s *Service) Commit(ctx context.Context, uploadID string) (*Result, error) {
	p, err := s.loadUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	res, err := s.commit(ctx, p, "upload")
	if err != nil {
		return nil, err
	}
	_ = s.Store.Tx(ctx, func(q *db.Queries) error { return q.DeleteUpload(ctx, uploadID) })
	return res, nil
}

func (s *Service) commit(ctx context.Context, p *plan, origin string) (*Result, error) {
	var missing int
	for h := range p.hashes {
		if !s.Blobs.Has(h) {
			missing++
		}
	}
	if missing > 0 {
		return nil, errf(http.StatusConflict, CodeMissingBlobs, "%d file(s) have not been uploaded yet", missing)
	}

	var res Result
	now := s.now().UnixMilli()
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		created := false
		proto, err := q.GetPrototypeByName(ctx, p.name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			proto, err = q.CreatePrototype(ctx, db.CreatePrototypeParams{
				Name: p.name, Label: NewLabel(p.name), SourceFingerprint: p.req.Source.Fingerprint,
				CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		default:
			if err := checkSource(proto, p.req.Source); err != nil {
				return err
			}
			// 两次推送并发提交同样的内容时，后一次不再产生新版本
			if rev, err := s.unchanged(ctx, q, proto, p); err != nil || rev != nil {
				if rev != nil {
					res = s.result(proto, *rev, false, true)
				}
				return err
			}
		}

		number, err := q.NextRevisionNumber(ctx, proto.ID)
		if err != nil {
			return err
		}
		spa := int64(0)
		if p.req.SPA {
			spa = 1
		}
		rev, err := q.CreateRevision(ctx, db.CreateRevisionParams{
			PrototypeID: proto.ID, Number: number, Origin: origin, Kind: p.kind, Entry: p.entry, Spa: spa,
			Note: p.req.Note, ManifestHash: p.mhash, FileCount: int64(len(p.files)), TotalBytes: p.total, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		for _, f := range p.files {
			if err := q.CreateArtifact(ctx, db.CreateArtifactParams{
				RevisionID: rev.ID, Path: f.Path, BlobHash: f.SHA256, Size: f.Size, ContentType: ContentType(f.Path),
			}); err != nil {
				return err
			}
		}
		if err := q.SetCurrentRevision(ctx, db.SetCurrentRevisionParams{
			CurrentRevisionID: sql.NullInt64{Int64: rev.ID, Valid: true}, UpdatedAt: now, ID: proto.ID,
		}); err != nil {
			return err
		}
		res = s.result(proto, rev, created, false)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// VersionLabel 是固定某个版本的内容主机标签：<label>--v<N>。
func VersionLabel(label string, number int64) string {
	return fmt.Sprintf("%s--v%d", label, number)
}

func (s *Service) result(proto db.Prototype, rev db.Revision, created, unchanged bool) Result {
	return Result{
		Prototype: ResultPrototype{Name: proto.Name, Kind: rev.Kind, Created: created},
		Version:   ResultVersion{Number: rev.Number, Unchanged: unchanged, Note: rev.Note},
		URLs: ResultURLs{
			Preview: s.Hosts.ContentOrigin(proto.Label) + "/",
			Version: s.Hosts.ContentOrigin(VersionLabel(proto.Label, rev.Number)) + "/",
		},
		Access:   ResultAccess{PreviewRequiresLogin: s.PreviewRequiresLogin},
		Stats:    ResultStats{Files: int(rev.FileCount), Bytes: rev.TotalBytes, Skipped: []string{}},
		Warnings: []string{},
	}
}
