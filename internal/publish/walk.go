package publish

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// LocalFile 是本地待推送的一个文件。
type LocalFile struct {
	FileEntry
	Abs string
}

// Collected 是遍历的结果。
type Collected struct {
	Files []LocalFile
	// Skipped 是被忽略规则排除的路径；目录以 / 结尾，不再列出其中的文件。
	Skipped []string
	// Warnings 是给人看的提示（如跳过了指向根目录之外的符号链接）。
	Warnings []string
}

// Collect 遍历 root（目录或单个文件），按 ig 过滤，不计算哈希。
// 符号链接只跟随指向 root 之内的文件；指向目录或 root 之外的都跳过并给出警告。
func Collect(root string, ig *Ignore) (*Collected, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	out := &Collected{}
	if !info.IsDir() {
		name := filepath.Base(root)
		if ig.Match(name, false) {
			return nil, fmt.Errorf("%s is excluded by the ignore rules; pass --include %q to push it anyway", root, name)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file", root)
		}
		out.Files = append(out.Files, LocalFile{FileEntry: FileEntry{Path: name, Size: info.Size()}, Abs: root})
		return out, nil
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.Type()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(p)
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("skipped broken symlink %s", rel))
				return nil
			}
			if !within(realRoot, target) {
				out.Warnings = append(out.Warnings, fmt.Sprintf("skipped symlink %s: it points outside the pushed directory", rel))
				return nil
			}
			ti, err := os.Stat(target)
			if err != nil {
				return err
			}
			if ti.IsDir() {
				out.Warnings = append(out.Warnings, fmt.Sprintf("skipped symlink %s: links to directories are not followed", rel))
				return nil
			}
			if ig.Match(rel, false) {
				out.Skipped = append(out.Skipped, rel)
				return nil
			}
			out.Files = append(out.Files, LocalFile{FileEntry: FileEntry{Path: rel, Size: ti.Size()}, Abs: target})
			return nil
		}

		if d.IsDir() {
			if ig.Match(rel, true) {
				out.Skipped = append(out.Skipped, rel+"/")
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // 设备、管道、socket 之类不推送
		}
		if ig.Match(rel, false) {
			out.Skipped = append(out.Skipped, rel)
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out.Files = append(out.Files, LocalFile{FileEntry: FileEntry{Path: rel, Size: fi.Size()}, Abs: p})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// HashFiles 并行计算每个文件的 SHA-256，并以实际读到的大小为准。
func HashFiles(files []LocalFile) error {
	workers := min(runtime.NumCPU(), 8, max(len(files), 1))
	jobs := make(chan int)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				hash, size, err := hashFile(files[i].Abs)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", files[i].Path, err)
					for range jobs {
					}
					return
				}
				files[i].SHA256, files[i].Size = hash, size
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	return <-errs
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
