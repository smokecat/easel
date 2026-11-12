package cli

import (
	"strings"
	"testing"
)

// 公网形态：后台就是内容域本身（easel.example.com），原型在 *.easel.example.com。
func TestAppOnContentApex(t *testing.T) {
	s := newTestServerWith(t, "https://easel.example.com", "https://easel.example.com")
	site := t.TempDir()
	writeFiles(t, site, map[string]string{"index.html": "public"})
	_, res, stderr := s.push(site, "--name", "pub")
	preview, ok := dig(res, "urls", "preview").(string)
	if !ok || !strings.HasPrefix(preview, "https://pub-") || !strings.HasSuffix(preview, ".easel.example.com/") {
		t.Fatalf("preview = %v (%s)", res, stderr)
	}
	host := strings.TrimSuffix(strings.TrimPrefix(preview, "https://"), "/")

	if rec := s.get(preview); rec.Code != 200 || rec.Body.String() != "public" {
		t.Fatalf("preview = %d %q", rec.Code, rec.Body.String())
	}
	if rec := s.get(preview); rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("https content should send HSTS")
	}
	if rec := s.get("https://easel.example.com/healthz"); rec.Code != 200 {
		t.Errorf("app on apex: /healthz = %d", rec.Code)
	}
	if rec := s.get("https://a.b.easel.example.com/healthz"); rec.Code != 404 {
		t.Errorf("multi-level subdomain reached the app: %d", rec.Code)
	}

	ask := func(domain string, hdr ...string) int {
		return s.get("http://127.0.0.1:7780/_tls/ask?domain="+domain, hdr...).Code
	}
	tests := []struct {
		domain string
		want   int
	}{
		{"easel.example.com", 200},
		{host, 200},
		{strings.Replace(host, ".easel", "--v1.easel", 1), 200},
		{strings.Replace(host, ".easel", "--v2.easel", 1), 404}, // 还没有第 2 版
		{"pub-zzzzzz.easel.example.com", 404},
		{"a.b.easel.example.com", 404},
		{"evil.example.org", 404},
		{"", 404},
	}
	for _, tt := range tests {
		if got := ask(tt.domain); got != tt.want {
			t.Errorf("ask %q = %d, want %d", tt.domain, got, tt.want)
		}
	}
	// 经 Caddy 转发进来的公网请求不能用它枚举原型
	if got := ask(host, "X-Forwarded-For", "203.0.113.1"); got != 404 {
		t.Errorf("proxied ask = %d, want 404", got)
	}
}

func TestParseHostsRejectsAppUnderContent(t *testing.T) {
	_, err := openInstance(t.Context(), serveConfig{
		DataDir: t.TempDir(), AppURL: "https://app.easel.example.com", ContentURL: "https://easel.example.com",
	})
	if err == nil {
		t.Fatal("app host under the content domain should be rejected")
	}
}
