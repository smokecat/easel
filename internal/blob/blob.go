// Package blob 是按 SHA-256 内容寻址的文件库，只管「哈希 → 内容」，不知道原型和版本。
//
// 布局：<root>/sha256/ab/cd/<hash>；写入先落到 <root>/tmp，边写边算哈希，校验通过后原子 rename。
// 请求路径永远不拼进文件系统路径，只用来查 manifest，所以这里不存在路径穿越的问题。
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrHashMismatch 表示写入内容的哈希与声明的不一致。
var ErrHashMismatch = errors.New("blob: content does not match hash")

// ErrTooLarge 表示写入内容超过了调用方给的上限。
var ErrTooLarge = errors.New("blob: content too large")

// Store 是一个 blob 库。
type Store struct{ root string }

// Open 打开（必要时创建）位于 root 的 blob 库。
func Open(root string) (*Store, error) {
	for _, d := range []string{"sha256", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

// ValidHash 判断 h 是否是 64 位小写十六进制的 SHA-256。
func ValidHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(hash string) string {
	return filepath.Join(s.root, "sha256", hash[0:2], hash[2:4], hash)
}

// Has 报告库里是否已有该哈希的内容。
func (s *Store) Has(hash string) bool {
	if !ValidHash(hash) {
		return false
	}
	_, err := os.Stat(s.path(hash))
	return err == nil
}

// Open 打开一个 blob 供读取；不存在时返回 fs.ErrNotExist。
func (s *Store) Open(hash string) (*os.File, error) {
	if !ValidHash(hash) {
		return nil, fs.ErrNotExist
	}
	return os.Open(s.path(hash))
}

// Put 从 r 读入内容，校验哈希等于 want 后入库，返回读入的字节数。已存在时不覆盖。
// limit > 0 时内容超过 limit 字节返回 ErrTooLarge。
func (s *Store) Put(r io.Reader, want string, limit int64) (int64, error) {
	if !ValidHash(want) {
		return 0, fmt.Errorf("blob: invalid hash %q", want)
	}
	_, n, _, err := s.write(r, limit, want)
	return n, err
}

// Ingest 从 r 读入内容并入库，哈希由内容算出；用于不知道哈希的整包上传。
// created 表示这份内容此前不在库里。
func (s *Store) Ingest(r io.Reader, limit int64) (hash string, n int64, created bool, err error) {
	return s.write(r, limit, "")
}

// write 先写临时文件、边写边算哈希，校验通过后原子 rename；want 为空时不校验。
func (s *Store) write(r io.Reader, limit int64, want string) (string, int64, bool, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "put-*")
	if err != nil {
		return "", 0, false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // rename 成功后是无害的 ENOENT

	h := sha256.New()
	src := r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", n, false, err
	}
	if limit > 0 && n > limit {
		return "", n, false, ErrTooLarge
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if want != "" && hash != want {
		return "", n, false, ErrHashMismatch
	}
	if s.Has(hash) {
		return hash, n, false, nil
	}
	dst := s.path(hash)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", n, false, err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return "", n, false, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", n, false, err
	}
	return hash, n, true, nil
}

// Sweep 做标记-清除：删除 keep 返回 false、且修改时间早于 before 的 blob，返回删除的个数。
// before 给「已上传、版本还没提交」的窗口留出宽限期。
func (s *Store) Sweep(keep func(hash string) (bool, error), before time.Time) (int, error) {
	removed := 0
	err := filepath.WalkDir(filepath.Join(s.root, "sha256"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !ValidHash(d.Name()) {
			return err
		}
		info, err := d.Info()
		if err != nil || !info.ModTime().Before(before) {
			return err
		}
		ok, err := keep(d.Name())
		if err != nil || ok {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
