package publish

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/smokecat/easel/internal/blob"
)

// MaxNameLen 是原型名的最大长度。主机标签是 <name>-<6 位后缀>，加版本固定的 --v<N>，要留在 63 字符以内。
const MaxNameLen = 40

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidName 校验原型名：小写字母、数字、短横线，不以短横线开头结尾，不含 --（留给版本固定主机）。
func ValidName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLen || !nameRe.MatchString(name) || strings.Contains(name, "--") {
		return errf(http.StatusBadRequest, CodeInvalidName,
			"invalid prototype name %q: use 1-%d lowercase letters, digits and single dashes", name, MaxNameLen)
	}
	return nil
}

// Slugify 把任意字符串转成合法的原型名；转不出来时返回空串。
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > MaxNameLen {
		out = strings.TrimRight(out[:MaxNameLen], "-")
	}
	return out
}

var labelEnc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewLabel 生成内容主机的标签：<name>-<6 位随机后缀>。
// 主机名会经 DNS、SNI、证书透明日志泄漏，所以后缀只防枚举和撞名，不当秘密用。
func NewLabel(name string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return name + "-" + labelEnc.EncodeToString(b[:])[:6]
}

// NewID 生成 128 bit 随机 ID（十六进制）。
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ReservedPrefix 是内容主机上留给 easel 自己的路径（换票、输密码、错误页）。
const ReservedPrefix = "_easel"

// ValidPath 校验 manifest 里的路径。
func ValidPath(p string) error {
	bad := func(why string) error {
		return errf(http.StatusBadRequest, CodeInvalidRequest, "invalid file path %q: %s", p, why)
	}
	switch {
	case len(p) > 1024:
		return bad("too long")
	case !fs.ValidPath(p) || p == ".":
		return bad("must be a relative path without . or .. segments")
	case strings.ContainsRune(p, '\\'):
		return bad("backslash is not allowed")
	case strings.IndexFunc(p, unicode.IsControl) >= 0:
		return bad("control characters are not allowed")
	case p == ReservedPrefix || strings.HasPrefix(p, ReservedPrefix+"/"):
		return bad("/" + ReservedPrefix + "/ is reserved by easel")
	}
	return nil
}

// Limits 是单次推送的上限，在第 1 步就检查，不会传到一半才失败。
type Limits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFiles      int
}

// DefaultLimits 是保守的默认上限。
var DefaultLimits = Limits{MaxFileBytes: 500 << 20, MaxTotalBytes: 2 << 30, MaxFiles: 20000}

// checkFiles 校验文件清单，返回排好序的副本和总大小。
func checkFiles(files []FileEntry, lim Limits) ([]FileEntry, int64, error) {
	if len(files) == 0 {
		return nil, 0, errf(http.StatusBadRequest, CodeInvalidRequest, "no files to publish")
	}
	if len(files) > lim.MaxFiles {
		return nil, 0, errf(http.StatusRequestEntityTooLarge, CodeTooLarge,
			"%d files exceeds the limit of %d", len(files), lim.MaxFiles).withHint("exclude files with --exclude or .easelignore")
	}
	out := slices.Clone(files)
	slices.SortFunc(out, func(a, b FileEntry) int { return strings.Compare(a.Path, b.Path) })
	var total int64
	for i, f := range out {
		if err := ValidPath(f.Path); err != nil {
			return nil, 0, err
		}
		if i > 0 && out[i-1].Path == f.Path {
			return nil, 0, errf(http.StatusBadRequest, CodeInvalidRequest, "duplicate file path %q", f.Path)
		}
		if !blob.ValidHash(f.SHA256) {
			return nil, 0, errf(http.StatusBadRequest, CodeInvalidRequest, "invalid sha256 for %q", f.Path)
		}
		if f.Size < 0 {
			return nil, 0, errf(http.StatusBadRequest, CodeInvalidRequest, "invalid size for %q", f.Path)
		}
		if f.Size > lim.MaxFileBytes {
			return nil, 0, errf(http.StatusRequestEntityTooLarge, CodeTooLarge,
				"%q is %d bytes, over the per-file limit of %d", f.Path, f.Size, lim.MaxFileBytes)
		}
		total += f.Size
	}
	if total > lim.MaxTotalBytes {
		return nil, 0, errf(http.StatusRequestEntityTooLarge, CodeTooLarge,
			"%d bytes in total exceeds the limit of %d", total, lim.MaxTotalBytes)
	}
	// 一个路径不能既是文件又是目录（a 和 a/b 同时存在）
	for i := 1; i < len(out); i++ {
		if strings.HasPrefix(out[i].Path, out[i-1].Path+"/") {
			return nil, 0, errf(http.StatusBadRequest, CodeInvalidRequest, "%q is both a file and a directory", out[i-1].Path)
		}
	}
	return out, total, nil
}

// manifestHash 是排好序的清单的规范化哈希，用来判断内容是否和上一版一致。
func manifestHash(sorted []FileEntry) string {
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", f.Path, f.SHA256, strconv.FormatInt(f.Size, 10))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// 版本的形态。
const (
	KindSite   = "site"
	KindAssets = "assets"
)

// DetectKind 按文件判断形态和入口：有 index.html 就是站点；根目录恰好一个 HTML 时以它为入口；其余是素材集。
func DetectKind(sorted []FileEntry) (kind, entry string) {
	var rootHTML []string
	for _, f := range sorted {
		if f.Path == "index.html" {
			return KindSite, "index.html"
		}
		if !strings.Contains(f.Path, "/") && isHTML(f.Path) {
			rootHTML = append(rootHTML, f.Path)
		}
	}
	if len(rootHTML) == 1 {
		return KindSite, rootHTML[0]
	}
	return KindAssets, ""
}

func isHTML(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// contentTypes 是固定的白名单：Content-Type 只按扩展名定，不采信客户端声明，也不嗅探内容。
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8", ".map": "application/json; charset=utf-8",
	".webmanifest": "application/manifest+json; charset=utf-8",
	".txt":         "text/plain; charset=utf-8", ".md": "text/markdown; charset=utf-8", ".csv": "text/csv; charset=utf-8",
	".xml": "application/xml; charset=utf-8", ".svg": "image/svg+xml",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".ico": "image/x-icon", ".bmp": "image/bmp",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".wav": "audio/wav", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".flac": "audio/flac",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
	".wasm": "application/wasm", ".pdf": "application/pdf",
	".glb": "model/gltf-binary", ".gltf": "model/gltf+json",
}

// OctetStream 是白名单之外的类型，内容主机以附件形式提供。
const OctetStream = "application/octet-stream"

// ContentType 按扩展名查白名单。
func ContentType(p string) string {
	if ct, ok := contentTypes[strings.ToLower(path.Ext(p))]; ok {
		return ct
	}
	return OctetStream
}
