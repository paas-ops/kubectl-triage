package main

import (
	"os"

	"github.com/bavocado/kubectl-triage/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(version, commit, date))
}
