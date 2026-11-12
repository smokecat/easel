package content

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 素材集（没有入口 HTML 的版本）的首页：图片、视频、音频直接内嵌，其余列成链接。
// 在内容主机上渲染，文件名经 html/template 转义。Markdown 渲染留给后续任务。
var listingTmpl = template.Must(template.New("listing").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  body { font: 14px/1.5 system-ui, sans-serif; margin: 0; padding: 24px; background: #fafafa; color: #222; }
  h1 { font-size: 18px; margin: 0 0 16px; }
  ul { list-style: none; padding: 0; display: grid; gap: 16px; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
  li { background: #fff; border: 1px solid #e5e5e5; border-radius: 8px; padding: 12px; overflow: hidden; }
  img, video { display: block; max-width: 100%; max-height: 360px; margin: 0 auto 8px; }
  audio { width: 100%; margin-bottom: 8px; }
  a { color: #0b57d0; word-break: break-all; }
  .size { color: #777; font-size: 12px; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<ul>
{{- range .Items}}
  <li>
    {{- if eq .Media "image"}}<a href="{{.Href}}"><img src="{{.Href}}" alt="{{.Path}}" loading="lazy"></a>
    {{- else if eq .Media "video"}}<video src="{{.Href}}" controls preload="metadata"></video>
    {{- else if eq .Media "audio"}}<audio src="{{.Href}}" controls preload="none"></audio>
    {{- end}}
    <a href="{{.Href}}">{{.Path}}</a> <span class="size">{{.Size}}</span>
  </li>
{{- end}}
</ul>
</body>
</html>
`))

type listingItem struct {
	Path, Href, Media, Size string
}

func (h *Handler) serveListing(w http.ResponseWriter, r *http.Request, m *manifest) {
	data := struct {
		Title string
		Items []listingItem
	}{Title: strconv.Itoa(len(m.list)) + " 个文件"}
	for _, a := range m.list {
		media := ""
		switch {
		case strings.HasPrefix(a.ContentType, "image/"):
			media = "image"
		case strings.HasPrefix(a.ContentType, "video/"):
			media = "video"
		case strings.HasPrefix(a.ContentType, "audio/"):
			media = "audio"
		}
		data.Items = append(data.Items, listingItem{
			Path: a.Path, Href: (&url.URL{Path: "/" + a.Path}).String(), Media: media, Size: humanBytes(a.Size),
		})
	}
	var buf bytes.Buffer
	if err := listingTmpl.Execute(&buf, data); err != nil {
		h.Log.Error("render listing", "err", err)
		h.plainError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buf.Bytes())
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}
