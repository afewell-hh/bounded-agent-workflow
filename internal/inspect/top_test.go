package inspect

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// InspectTop returns exactly Inspect's packet or error, plus the physical
// top level, for both real object formats, subdirectories and symlinked
// aliases.
func TestInspectTopMatchesInspect(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, repo := filepath.Join(base, "home"), filepath.Join(base, "repo")
			os.Mkdir(home, 0o700)
			if err := tf.Init(home, repo, format); err != nil {
				t.Fatalf("%s fixture mandatory: %v", format, err)
			}
			os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
			tf.Write(filepath.Join(repo, "sub", "f.txt"), "x\n")
			head, err := tf.CommitSources(home, repo)
			if err != nil {
				t.Fatal(err)
			}
			if len(head) != map[string]int{"sha1": 40, "sha256": 64}[format] {
				t.Fatalf("head %q", head)
			}
			alias := filepath.Join(base, "alias")
			if err := os.Symlink(repo, alias); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{repo, filepath.Join(repo, "sub"), alias, filepath.Join(alias, "sub")} {
				opts := Options{Repo: dir, Limits: DefaultLimits}
				want, werr := Inspect(opts)
				got, top, gerr := InspectTop(opts)
				if werr != nil || gerr != nil {
					t.Fatalf("%s: %v / %v", dir, werr, gerr)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: packets differ", dir)
				}
				if top != repo {
					t.Fatalf("%s: top %q want %q", dir, top, repo)
				}
			}
			// Failure: identical error, no top level.
			notRepo := filepath.Join(base, "home")
			_, werr := Inspect(Options{Repo: notRepo, Limits: DefaultLimits})
			p, top, gerr := InspectTop(Options{Repo: notRepo, Limits: DefaultLimits})
			if werr == nil || gerr == nil || werr.Error() != gerr.Error() || p != nil || top != "" {
				t.Fatalf("failure: %v / %v %q", werr, gerr, top)
			}
		})
	}
}
