package proxy

import (
	"net"
	"os"
	"testing"
)

func TestRewriteLocation(t *testing.T) {
	const pub = "https://app-x.easel.example.com"
	tests := map[string]string{
		"http://localhost:5173/login":       pub + "/login",
		"https://localhost:5173/a?b=1":      pub + "/a?b=1", // Next 按 X-Forwarded-Proto 拼出来的
		"http://127.0.0.1:5173":             pub,
		"http://[::1]:5173/x":               pub + "/x",
		"http://localhost:5173?next=/":      pub + "?next=/",
		"http://localhost:51730/x":          "http://localhost:51730/x", // 别的端口不动
		"http://localhost:5174/x":           "http://localhost:5174/x",
		"https://accounts.example.com/auth": "https://accounts.example.com/auth",
		"/relative":                         "/relative",
	}
	for in, want := range tests {
		if got := rewriteLocation(in, 5173, pub); got != want {
			t.Errorf("rewriteLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterCookie(t *testing.T) {
	tests := map[string]string{
		"sid=1; Path=/; Domain=localhost; HttpOnly": "sid=1; Path=/; HttpOnly",
		"a=b; domain=.easel.example.com":            "a=b",
		"theme=dark":                                "theme=dark",
		"__Host-easel_session=x; Path=/; Secure":    "",
		"easel_view=x":                              "",
		"__Secure-easel_x=1":                        "",
	}
	for in, want := range tests {
		if got := filterCookie(in); got != want {
			t.Errorf("filterCookie(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListenerProcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	p, ok := ListenerProcess(port)
	if !ok {
		t.Skip("process identity is not available on this platform")
	}
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	if p.Exe != exe || p.Cwd != wd {
		t.Errorf("ListenerProcess = %+v, want exe %s cwd %s", p, exe, wd)
	}
}
