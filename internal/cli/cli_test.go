package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Run(context.Background(), &Env{Stdout: &out, Stderr: &errb}, args)
	return code, out.String(), errb.String()
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run("version", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var v VersionInfo
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("invalid json %q: %v", out, err)
	}
	if v.Version == "" || v.Go == "" || v.OS == "" || v.Arch == "" {
		t.Errorf("missing fields: %+v", v)
	}
}

func TestVersionText(t *testing.T) {
	code, out, _ := run("--version")
	if code != ExitOK || !strings.HasPrefix(out, "easel ") {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"nope"}, {"version", "--bogus"}, {"version", "extra"}} {
		code, out, errOut := run(args...)
		if code != ExitUsage {
			t.Errorf("%v: exit = %d, want %d", args, code, ExitUsage)
		}
		if out != "" {
			t.Errorf("%v: stdout should be empty, got %q", args, out)
		}
		if errOut == "" {
			t.Errorf("%v: stderr should explain the error", args)
		}
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := run("help")
	if code != ExitOK || !strings.Contains(out, "serve") {
		t.Fatalf("exit = %d, out = %q", code, out)
	}
	if code, _, _ := run("serve", "-h"); code != ExitOK {
		t.Errorf("serve -h exit = %d", code)
	}
}
