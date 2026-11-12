package auth

import (
	"html/template"
	"net/http"
	"strconv"
	"time"
)

// 登录页、密码页这类小页面由服务端渲染，不依赖管理面板的构建产物；
// 内容主机上的密码页也用它，不能引用任何外部资源。
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}} · easel</title>
<style>
:root { color-scheme: light dark; font-family: system-ui, -apple-system, sans-serif; }
body { display: grid; place-items: center; min-height: 100vh; margin: 0; background: Canvas; color: CanvasText; }
main { width: min(22rem, 90vw); }
h1 { font-size: 1.25rem; margin: 0 0 1rem; }
p { line-height: 1.5; }
form { display: grid; gap: .75rem; }
input[type=password] { font: inherit; padding: .5rem .6rem; border: 1px solid GrayText; border-radius: .4rem; }
button { font: inherit; padding: .5rem; border-radius: .4rem; border: 0; background: #2563eb; color: #fff; cursor: pointer; }
.error { color: #dc2626; }
code { font-size: .9em; }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
{{if .Message}}<p>{{.Message}}</p>{{end}}
{{if .Form}}
<form method="post" action="{{.Action}}">
{{range $k, $v := .Hidden}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<input type="password" name="password" autocomplete="current-password" placeholder="Password" required autofocus aria-label="Password">
<button type="submit">{{.Submit}}</button>
</form>
{{end}}
</main>
</body>
</html>
`))

type page struct {
	Title, Message, Error string
	Form                  bool
	Action, Submit        string
	Hidden                map[string]string
}

func renderPage(w http.ResponseWriter, status int, p page) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, p)
}

func tooManyAttempts(w http.ResponseWriter, wait time.Duration, p page) {
	secs := int(wait.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	p.Error = "Too many failed attempts. Try again in " + (time.Duration(secs) * time.Second).String() + "."
	renderPage(w, http.StatusTooManyRequests, p)
}
