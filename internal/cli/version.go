package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
)

// 由构建时 -ldflags "-X github.com/smokecat/easel/internal/cli.version=..." 注入。
var (
	version = ""
	commit  = ""
)

// VersionInfo 是 `easel version --json` 的输出，字段保持稳定。
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func versionInfo() VersionInfo {
	v := VersionInfo{Version: version, Commit: commit, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v.Version = bi.Main.Version // go install ...@vX.Y.Z
		}
		if v.Commit == "" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					v.Commit = s.Value
				}
			}
		}
	}
	if v.Version == "" {
		v.Version = "dev"
	}
	return v
}

func runVersion(_ context.Context, env *Env, args []string) error {
	fset := newFlagSet(env, "version")
	asJSON := fset.Bool("json", false, "以 JSON 输出")
	if err := parseFlags(fset, args); err != nil {
		return err
	}
	v := versionInfo()
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		return enc.Encode(v)
	}
	commit := v.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		commit = "unknown"
	}
	_, err := fmt.Fprintf(env.Stdout, "easel %s (%s, %s, %s/%s)\n", v.Version, commit, v.Go, v.OS, v.Arch)
	return err
}
