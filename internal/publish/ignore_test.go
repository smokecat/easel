package publish

import "testing"

func TestIgnore(t *testing.T) {
	ig, err := NewIgnore("*.map", "/drafts/", "docs/**/private", "!keep.pem")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path  string
		dir   bool
		match bool
	}{
		{"index.html", false, false},
		{".env", false, true},
		{".env.local", false, true},
		{"sub/.git", true, true},
		{".well-known", true, false},
		{".well-known/security.txt", false, false},
		{"node_modules", true, true},
		{"a/node_modules", true, true},
		{"node_modules", false, false}, // 只匹配目录
		{"certs/server.pem", false, true},
		{"keep.pem", false, false},
		{"id_rsa.pub", false, true},
		{"app.js.map", false, true},
		{"drafts", true, true},
		{"sub/drafts", true, false}, // 锚定在根
		{"docs/private", false, true},
		{"docs/a/b/private", false, true},
		{"docs/privates", false, false},
	}
	for _, tt := range tests {
		if got := ig.Match(tt.path, tt.dir); got != tt.match {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", tt.path, tt.dir, got, tt.match)
		}
	}
}

func TestIgnoreInclude(t *testing.T) {
	ig, _ := NewIgnore()
	if !ig.Match(".nojekyll", false) {
		t.Fatal("dotfile should be ignored by default")
	}
	_ = ig.Include(".nojekyll")
	if ig.Match(".nojekyll", false) {
		t.Fatal("--include should bring it back")
	}
}

func TestGlobClass(t *testing.T) {
	ig := &Ignore{}
	_ = ig.Add("file[0-9].txt", "x[!a].txt")
	for path, want := range map[string]bool{"file1.txt": true, "filea.txt": false, "xb.txt": true, "xa.txt": false} {
		if got := ig.Match(path, false); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}
