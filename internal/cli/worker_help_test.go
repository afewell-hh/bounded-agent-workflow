package cli

import (
	"bytes"
	"strings"
	"testing"
)

// Every supported global help form lists the worker command once and states
// its meaning; expectations are written out by hand, not taken from Usage.
func TestWorkerHelpDiscoverability(t *testing.T) {
	const line = "\n  baw run worker --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]\n"
	const para = "\n\nRun worker records one native Claude Code worker attempt with full frozen project instructions.\n" +
		"Its completion is an observation, not tests, review, acceptance or authority; it never retries or recovers.\n"
	forms := [][]string{
		{"--help"}, {"-h"}, {"help"},
		{"inspect", "--help"}, {"inspect", "-h"}, {"inspect", "help"},
		{"run", "--help"}, {"run", "create", "--help"}, {"status", "--help"}, {"run", "diagnose", "--help"},
	}
	for _, args := range forms {
		var out, errb bytes.Buffer
		code := Run(args, &out, &errb)
		got := out.String()
		if code != 0 || errb.Len() != 0 {
			t.Errorf("%q: %d %q", args, code, errb.String())
			continue
		}
		if strings.Count(got, "baw run worker ") != 1 || strings.Count(got, line) != 1 {
			t.Errorf("%q: worker syntax line not present exactly once", args)
		}
		if !strings.Contains(got, "[--json]"+line+"  baw --help\n") {
			t.Errorf("%q: worker syntax line not before baw --help", args)
		}
		if !strings.HasSuffix(got, para) {
			t.Errorf("%q: worker meaning paragraph missing", args)
		}
		if len(got) > 4096 {
			t.Errorf("%q: help is %d bytes, over 4096", args, len(got))
		}
	}
}
