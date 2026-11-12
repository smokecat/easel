package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPutOpenHas(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := hashOf("hello")
	if s.Has(h) {
		t.Fatal("empty store has blob")
	}
	if n, err := s.Put(strings.NewReader("hello"), h, 0); err != nil || n != 5 {
		t.Fatalf("Put = %d, %v", n, err)
	}
	if !s.Has(h) {
		t.Fatal("Has = false after Put")
	}
	// 再写一次是幂等的
	if _, err := s.Put(strings.NewReader("hello"), h, 0); err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(h)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
}

func TestPutRejects(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.Put(strings.NewReader("hello"), hashOf("other"), 0); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("mismatch: err = %v", err)
	}
	if s.Has(hashOf("other")) || s.Has(hashOf("hello")) {
		t.Error("rejected content was stored")
	}
	if _, err := s.Put(strings.NewReader("hello"), hashOf("hello"), 4); !errors.Is(err, ErrTooLarge) {
		t.Errorf("limit: err = %v", err)
	}
	if _, err := s.Put(strings.NewReader("x"), "../../etc/passwd", 0); err == nil {
		t.Error("invalid hash accepted")
	}
	if _, err := s.Open("../x"); err == nil {
		t.Error("Open accepted invalid hash")
	}
}

func TestSweep(t *testing.T) {
	s, _ := Open(t.TempDir())
	keep, drop := hashOf("keep"), hashOf("drop")
	_, _ = s.Put(strings.NewReader("keep"), keep, 0)
	_, _ = s.Put(strings.NewReader("drop"), drop, 0)

	isKept := func(h string) (bool, error) { return h == keep, nil }
	// 宽限期内的不删
	if n, err := s.Sweep(isKept, time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("Sweep in grace = %d, %v", n, err)
	}
	if n, err := s.Sweep(isKept, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if !s.Has(keep) || s.Has(drop) {
		t.Error("wrong blob removed")
	}
}
