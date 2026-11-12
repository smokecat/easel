package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServer(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>easel</title>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
		"assets/nested/.x": {Data: []byte("")},
	}
	h := New(Options{Assets: assets})

	tests := []struct {
		name, method, path string
		wantCode           int
		wantBody           string
		wantCache          string
	}{
		{"health", "GET", "/healthz", 200, "ok", ""},
		{"index", "GET", "/", 200, "<title>easel</title>", "no-cache"},
		{"spa fallback", "GET", "/prototypes/abc", 200, "<title>easel</title>", "no-cache"},
		{"directory falls back", "GET", "/assets/", 200, "<title>easel</title>", "no-cache"},
		{"hashed asset", "GET", "/assets/app-1.js", 200, "console.log", "immutable"},
		{"root file", "GET", "/favicon.svg", 200, "<svg/>", ""},
		{"head", "HEAD", "/", 200, "", "no-cache"},
		{"post rejected", "POST", "/", 405, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want contains %q", rec.Body.String(), tt.wantBody)
			}
			if !strings.Contains(rec.Header().Get("Cache-Control"), tt.wantCache) {
				t.Errorf("Cache-Control = %q, want contains %q", rec.Header().Get("Cache-Control"), tt.wantCache)
			}
		})
	}
}

func TestServerWithoutBuiltUI(t *testing.T) {
	h := New(Options{Assets: fstest.MapFS{".gitkeep": {}}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}
