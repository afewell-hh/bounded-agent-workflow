// Command baw is the Bounded Agent Workflow executable.
package main

import (
	"os"

	"github.com/afewell-hh/bounded-agent-workflow/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
