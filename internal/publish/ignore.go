package publish

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// DefaultIgnore 是默认排除的规则（gitignore 语法）。不读 .gitignore：被推送的通常正是被 gitignore 掉的构建产物。
// 点文件一律排除（.well-known/ 例外），一条规则覆盖 .git、.env*、.npmrc、.ssh 等。
var DefaultIgnore = []string{
	".*",
	"!.well-known/",
	"node_modules/",
	"*.pem", "*.key", "*.p12", "*.pfx", "*.kdbx",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	"credentials.json",
}

// Ignore 是一组按顺序生效的规则，最后一条命中的规则决定结果。
type Ignore struct{ rules []rule }

type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
	src     string
}

// NewIgnore 用 DefaultIgnore 加上 extra 构造规则。
func NewIgnore(extra ...string) (*Ignore, error) {
	ig := &Ignore{}
	if err := ig.Add(DefaultIgnore...); err != nil {
		return nil, err
	}
	return ig, ig.Add(extra...)
}

// Add 追加规则；空行和 # 开头的行忽略。
func (ig *Ignore) Add(patterns ...string) error {
	for _, p := range patterns {
		r, ok, err := compileRule(p)
		if err != nil {
			return err
		}
		if ok {
			ig.rules = append(ig.rules, r)
		}
	}
	return nil
}

// Include 追加「加回来」的规则（相当于 !pattern）。
func (ig *Ignore) Include(patterns ...string) error {
	for _, p := range patterns {
		if err := ig.Add("!" + strings.TrimPrefix(p, "!")); err != nil {
			return err
		}
	}
	return nil
}

// AddFile 读 gitignore 格式的文件内容。
func (ig *Ignore) AddFile(r io.Reader) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if err := ig.Add(sc.Text()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Match 报告相对路径 rel（用 / 分隔）是否被排除。目录被排除时调用方不应再进入它。
func (ig *Ignore) Match(rel string, isDir bool) bool {
	ignored := false
	for _, r := range ig.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

func compileRule(line string) (rule, bool, error) {
	line = strings.TrimRight(line, "\r")
	// 行尾空格除非被转义否则忽略
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = line[:len(line)-1]
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return rule{}, false, nil
	}
	r := rule{src: line}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return rule{}, false, nil
	}
	// 中间或开头带 / 的模式相对根目录；否则匹配任意层级的名字
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	body, err := globToRegexp(line)
	if err != nil {
		return rule{}, false, fmt.Errorf("ignore pattern %q: %w", r.src, err)
	}
	prefix := "^"
	if !anchored {
		prefix = "^(?:.*/)?"
	}
	r.re, err = regexp.Compile(prefix + body + "$")
	if err != nil {
		return rule{}, false, fmt.Errorf("ignore pattern %q: %w", r.src, err)
	}
	return r, true, nil
}

func globToRegexp(g string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				// **/ 匹配零或多级目录；结尾的 /** 匹配目录下的一切；其他位置的 ** 当 * 处理
				atStart := i == 0 || g[i-1] == '/'
				switch {
				case atStart && i+2 < len(g) && g[i+2] == '/':
					b.WriteString("(?:.*/)?")
					i += 2
					continue
				case atStart && i+2 == len(g):
					b.WriteString(".*")
					i++
					continue
				}
				i++
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(g[i+1:], ']')
			if j < 0 {
				return "", fmt.Errorf("unterminated character class")
			}
			class := g[i+1 : i+1+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += j + 1
		case '\\':
			if i+1 < len(g) {
				i++
				b.WriteString(regexp.QuoteMeta(string(g[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String(), nil
}
