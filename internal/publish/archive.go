package publish

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/smokecat/easel/internal/blob"
)

// ArchiveOptions 是整包推送的参数（来自 query string）。
type ArchiveOptions struct {
	Note string
	SPA  bool
	// TempDir 用来暂存 zip（zip 需要随机读取）；为空时用系统临时目录。
	TempDir string
}

// PushArchive 是整包端点：接受 tar、tar.gz 或 zip，解包入库后直接生成版本。
// 默认忽略规则同样生效（防止误传 .env），被跳过的文件在结果的 stats.skipped 里列出。
func (s *Service) PushArchive(ctx context.Context, name string, body io.Reader, opts ArchiveOptions) (*Result, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	ig, err := NewIgnore()
	if err != nil {
		return nil, err
	}
	// skipped 初始化成空切片：结果里的 stats.skipped 约定是数组，不能是 null
	in := &ingester{s: s, ig: ig, seen: map[string]bool{}, skipped: []string{}}

	br := bufio.NewReader(body)
	magic, _ := br.Peek(4)
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, badArchive(err)
		}
		err = in.tar(ctx, gz)
		if err != nil {
			return nil, err
		}
	case bytes.Equal(magic, []byte("PK\x03\x04")), bytes.Equal(magic, []byte("PK\x05\x06")):
		if err := in.zip(ctx, br, opts.TempDir); err != nil {
			return nil, err
		}
	default:
		if err := in.tar(ctx, br); err != nil {
			return nil, err
		}
	}

	req := CreateRequest{Files: in.files, Note: opts.Note, SPA: opts.SPA}
	p, err := s.newPlan(name, req)
	if err != nil {
		return nil, err
	}
	proto, err := s.Store.Q.GetPrototypeByName(ctx, name)
	if err == nil {
		if rev, err := s.unchanged(ctx, s.Store.Q, proto, p); err != nil {
			return nil, err
		} else if rev != nil {
			res := s.result(proto, *rev, false, true)
			res.Stats.Skipped = in.skipped
			return &res, nil
		}
	}
	res, err := s.commit(ctx, p, "upload")
	if err != nil {
		return nil, err
	}
	res.Stats.UploadedFiles, res.Stats.UploadedBytes = in.newFiles, in.newBytes
	res.Stats.Skipped = in.skipped
	return res, nil
}

func badArchive(err error) error {
	return errf(http.StatusBadRequest, CodeInvalidRequest, "read archive: %v", err).
		withHint("send a tar, tar.gz or zip file")
}

type ingester struct {
	s        *Service
	ig       *Ignore
	files    []FileEntry
	skipped  []string
	seen     map[string]bool
	total    int64
	newFiles int
	newBytes int64
}

// add 处理压缩包里的一个普通文件。路径先规整（去掉前导 ./ 和 /），再走和清单相同的校验。
func (in *ingester) add(name string, size int64, r io.Reader) error {
	p := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, `\`, "/")), "/")
	if p != strings.TrimPrefix(strings.TrimPrefix(name, "./"), "/") {
		// 规整前后不一致说明带了 .. 之类的段，直接拒绝而不是悄悄改写
		return errf(http.StatusBadRequest, CodeInvalidRequest, "archive entry %q has an unsafe path", name)
	}
	if err := ValidPath(p); err != nil {
		return err
	}
	if in.ignored(p) {
		in.skipped = append(in.skipped, p)
		return nil
	}
	if in.seen[p] {
		return errf(http.StatusBadRequest, CodeInvalidRequest, "archive has duplicate entry %q", p)
	}
	lim := in.s.Limits
	if len(in.files) >= lim.MaxFiles {
		return errf(http.StatusRequestEntityTooLarge, CodeTooLarge, "archive has more than %d files", lim.MaxFiles)
	}
	limit := min(lim.MaxFileBytes, lim.MaxTotalBytes-in.total)
	if size > limit {
		return errf(http.StatusRequestEntityTooLarge, CodeTooLarge, "archive exceeds the size limits at %q", p)
	}
	hash, n, created, err := in.s.Blobs.Ingest(r, limit)
	if errors.Is(err, blob.ErrTooLarge) {
		return errf(http.StatusRequestEntityTooLarge, CodeTooLarge, "archive exceeds the size limits at %q", p)
	}
	if err != nil {
		return err
	}
	if created {
		in.newFiles++
		in.newBytes += n
	}
	in.seen[p] = true
	in.total += n
	in.files = append(in.files, FileEntry{Path: p, SHA256: hash, Size: n})
	return nil
}

// ignored 对路径的每一级目录和文件本身应用忽略规则。
func (in *ingester) ignored(p string) bool {
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		if in.ig.Match(strings.Join(parts[:i], "/"), true) {
			return true
		}
	}
	return in.ig.Match(p, false)
}

func (in *ingester) tar(ctx context.Context, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return badArchive(err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			if err := in.add(hdr.Name, hdr.Size, tr); err != nil {
				return err
			}
		case tar.TypeDir, tar.TypeXGlobalHeader:
		default:
			in.skipped = append(in.skipped, hdr.Name) // 符号链接、硬链接等不解
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (in *ingester) zip(ctx context.Context, r io.Reader, tempDir string) error {
	tmp, err := os.CreateTemp(tempDir, "easel-zip-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(r, in.s.Limits.MaxTotalBytes+1))
	if err != nil {
		return err
	}
	if n > in.s.Limits.MaxTotalBytes {
		return errf(http.StatusRequestEntityTooLarge, CodeTooLarge, "archive is larger than %d bytes", in.s.Limits.MaxTotalBytes)
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return badArchive(err)
	}
	for _, f := range zr.File {
		mode := f.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			in.skipped = append(in.skipped, f.Name)
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return badArchive(err)
		}
		// UncompressedSize64 由压缩包自己声明，不可信；真正的上限在 Ingest 里按实际读到的字节数检查
		err = in.add(f.Name, int64(min(f.UncompressedSize64, 1<<62)), rc)
		_ = rc.Close()
		if err != nil {
			var zerr *Error
			if !errors.As(err, &zerr) && (errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat)) {
				return badArchive(err)
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}
