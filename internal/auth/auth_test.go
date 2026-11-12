package auth

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h := hashPassword("hunter2 hunter2")
	if ok, err := verifyPassword(h, "hunter2 hunter2"); !ok || err != nil {
		t.Fatalf("verify right = %v %v", ok, err)
	}
	if ok, _ := verifyPassword(h, "hunter2"); ok {
		t.Fatal("wrong password accepted")
	}
	if h2 := hashPassword("hunter2 hunter2"); h2 == h {
		t.Fatal("salt not random")
	}
	for _, bad := range []string{"", "$argon2id$v=19$m=1,t=1$x$y", "$bcrypt$...", "$argon2id$v=19$m=99999999,t=1,p=1$AAAA$AAAA"} {
		if _, err := verifyPassword(bad, "x"); err == nil {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
	if err := CheckPasswordPolicy("short"); err == nil {
		t.Error("short password accepted")
	}
}

func TestSafeReturn(t *testing.T) {
	tests := map[string]string{
		"":                      "/",
		"/":                     "/",
		"/a/b?x=1":              "/a/b?x=1",
		"//evil.com/":           "/",
		"/\\evil.com":           "/",
		"https://evil.com/":     "/",
		"evil.com":              "/",
		"/a\r\nSet-Cookie: x=1": "/",
		"/%2F%2Fevil.com":       "/%2F%2Fevil.com",
		"javascript:alert(1)":   "/",
		"/ok#frag":              "/ok",
	}
	for in, want := range tests {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(10*time.Minute, 3, time.Second, 4*time.Second)
	now := time.Unix(1000, 0)
	for i := range 3 {
		if w := l.begin("k", now); w != 0 {
			t.Fatalf("attempt %d locked too early: %v", i, w)
		}
	}
	if w := l.begin("k", now); w != time.Second {
		t.Fatalf("first lock = %v", w)
	}
	// 每次锁定结束后再失败，锁定时长翻倍，封顶 4 秒
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 4 * time.Second} {
		now = now.Add(5 * time.Second)
		if w := l.begin("k", now); w != 0 {
			t.Fatalf("attempt after lock = %v", w)
		}
		if w := l.begin("k", now); w != want {
			t.Fatalf("lock = %v, want %v", w, want)
		}
	}
	if w := l.begin("other", now); w != 0 {
		t.Fatal("keys not independent")
	}
	l.reset("k")
	if w := l.begin("k", now); w != 0 {
		t.Fatal("reset did not clear")
	}
	// 窗口过后重新计数
	l.begin("k", now)
	later := now.Add(11 * time.Minute)
	l.begin("k", later)
	if w := l.begin("k", later); w != 0 {
		t.Fatalf("window not reset: %v", w)
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l := newLimiter(10*time.Minute, 3, time.Minute, time.Hour)
	now := time.Unix(1000, 0)
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for range 50 {
		wg.Go(func() {
			if l.begin("k", now) == 0 {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 3 {
		t.Fatalf("%d concurrent attempts allowed, want 3", n)
	}
}

func TestParseScopes(t *testing.T) {
	sc, err := ParseScopes(" share,read, read ")
	if err != nil || len(sc) != 2 || sc[0] != ScopeRead || sc[1] != ScopeShare {
		t.Fatalf("ParseScopes = %v %v", sc, err)
	}
	if _, err := ParseScopes("admin"); err == nil {
		t.Error("unknown scope accepted")
	}
	if _, err := ParseScopes(" , "); err == nil {
		t.Error("empty scopes accepted")
	}
}

func TestStripCookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cookie", "a=1; easel_view=secret; b=2")
	stripCookie(r, "easel_view")
	if got := r.Header.Get("Cookie"); got != "a=1; b=2" {
		t.Errorf("Cookie = %q", got)
	}
	r.Header.Set("Cookie", "easel_view=secret")
	stripCookie(r, "easel_view")
	if _, ok := r.Header["Cookie"]; ok {
		t.Error("empty Cookie header left behind")
	}
}

func TestKeyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.key")
	k1, err := LoadOrCreateKey(p)
	if err != nil || len(k1) != keySize {
		t.Fatalf("create = %v %v", k1, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode())
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || string(k1) != string(k2) {
		t.Fatal("key not stable across loads")
	}
	if deriveShareToken(k1, "n") == deriveShareToken(k1, "m") || deriveShareToken(k1, "n") != deriveShareToken(k2, "n") {
		t.Error("share token derivation")
	}
}
