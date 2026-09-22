package doctor

// What this repository's own checkout is diagnosed as. The rest of this
// package's tests build an installation to say what a check does; these two say
// what it currently reports here, which is the only place the indexes those
// checks are about actually live.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

func TestThisRepositoryReportsItsOwnArtifactHomesDocumented(t *testing.T) {
	t.Parallel()

	// The item this covers is done when `yoyo doctor` says this repository's homes
	// are documented, so the assertion is the check itself against this checkout
	// rather than a fixture that would keep passing while the real indexes were
	// missing.
	root, resolved := thisRepository(t)
	finding := (&diagnosis{}).checkArtifactHomes(root, root, resolved)
	if finding.Status != StatusOK {
		t.Fatalf("artifact-readmes = %s: %s (%s)", finding.Status, finding.Summary, finding.Detail)
	}
	// The homes are named rather than counted, because which directory the check
	// read is the whole of what a reader would act on — the invariants home most
	// of all, since it carries its own identity scheme and is still a home this
	// check asks an index of.
	product := resolved.Config.Product
	for _, home := range []string{
		product.Specifications,
		product.Specifications + "/goals",
		product.Designs,
		product.Decisions,
		product.Invariants,
	} {
		if !strings.Contains(finding.Detail, home+"/"+artifacthome.FileName) {
			t.Errorf("detail = %q, want %s among the homes it read", finding.Detail, home)
		}
	}
}

// The Node finding is asked only of a product carrying the dashboard page's
// render script, so a script that moved or was renamed would stand the finding
// down without a word: the one product that ships the dashboard would stop being
// asked whether it can draw it. The path is a constant three things share, and
// this is what holds that constant against the checkout it is about.
func TestThisRepositoryIsAskedAboutTheNodeItsDashboardNeeds(t *testing.T) {
	t.Parallel()

	root, _ := thisRepository(t)
	script := filepath.Join(root, filepath.FromSlash(dashboard.RenderScript))
	if info, err := os.Stat(script); err != nil || info.IsDir() {
		t.Fatalf("%s is not a file in this checkout (%v), so nothing here is asked about node and the page's renders go uncompared with nothing saying so", dashboard.RenderScript, err)
	}
	// The real machine, because what is being held is the gate rather than the
	// answer: this checkout produces a node finding whether or not the machine
	// running the suite has Node.
	findings := (&diagnosis{}).checkNode(root)
	if len(findings) != 1 || findings[0].Check != "node" {
		t.Fatalf("checkNode() = %+v, want the one node finding a product shipping the dashboard is given", findings)
	}
}

func TestTheIndexesThisRepositoryCarriesAreNotReadAsGovernedDocuments(t *testing.T) {
	t.Parallel()

	// An index is a file inside a home that is not a document of the kind filed
	// there, so every loader that walks a home has to skip it by name. This says
	// the ones this repository now carries are skipped rather than reported, for
	// the artifact homes and for the invariants directory both.
	root, resolved := thisRepository(t)
	product := resolved.Config.Product

	artifacts, err := artifact.StoreFor(root, product).Load()
	if err != nil {
		t.Fatalf("artifact Load() error = %v", err)
	}
	if len(artifacts.Problems) != 0 || len(artifacts.ReferenceProblems) != 0 {
		t.Fatalf("problems = %v, reference problems = %v", artifacts.Problems, artifacts.ReferenceProblems)
	}
	for _, recorded := range artifacts.Artifacts {
		if strings.EqualFold(path.Base(recorded.Path), artifacthome.FileName) {
			t.Errorf("%s was loaded as the artifact %q; an index states no intent anything refers to", recorded.Path, recorded.ID)
		}
	}

	invariants, err := (invariant.Store{RepositoryRoot: root, Directory: product.Invariants}).Load()
	if err != nil {
		t.Fatalf("invariant Load() error = %v", err)
	}
	if len(invariants.Problems) != 0 {
		t.Fatalf("invariant problems = %v", invariants.Problems)
	}
	// The constraints themselves still load, so a clean report is the index being
	// skipped rather than the whole directory going unread.
	if len(invariants.Active) == 0 {
		t.Fatal("no invariant loaded, so a clean problem list says nothing about the index")
	}
}

// thisRepository is the checkout these tests run in, read through the same
// discovery the commands use so a home that moves in the configuration moves
// here too.
func thisRepository(t *testing.T) (string, config.Resolved) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	discovered, err := config.Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	loaded, err := config.Load(discovered)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return root, config.Resolved{Config: loaded, Path: discovered}
}
