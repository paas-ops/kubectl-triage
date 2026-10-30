package main

import (
	"os"
	"runtime/debug"

	"github.com/paas-ops/kubectl-triage/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	v, c, d := resolveBuildInfo(version, commit, date)
	os.Exit(cli.Execute(v, c, d))
}

// resolveBuildInfo falls back to module/VCS build metadata when the binary is
// installed with "go install ...@version" instead of being built with -ldflags.
func resolveBuildInfo(v, c, d string) (string, string, string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c, d
	}
	if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if c == "none" && s.Value != "" {
				c = s.Value
			}
		case "vcs.time":
			if d == "unknown" && s.Value != "" {
				d = s.Value
			}
		}
	}
	return v, c, d
}
