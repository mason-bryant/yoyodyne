package doctor

// What this repository's own checkout is diagnosed as. The rest of this
// package's tests build an installation to say what a check does; these two say
// what it currently reports here, which is the only place the indexes those
// checks are about actually live.
//
// Both are about files inside the artifact homes, which every developer's diff
// refuses, so what they find is escalated to the role that owns the home rather
// than failing the build for everybody — internal/governeddoc says why, and
// `yoyo artifact check` is where a governed-document defect does fail. What
// stays a failure here is the reader: a load that errored, or a directory that
// yielded no constraint at all, is not something an owner can amend.

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
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
		// An index that is missing or has stopped answering is a file in an
		// artifact home, so it is routed to that home's owner. The finding names
		// every home it is about, so the report is made against the home the check
		// was run over rather than against a path picked out of the prose.
		//
		// This escalating is not the class going unchecked: `yoyo artifact check`
		// reads the same index states and exits non-zero on one that is missing,
		// incomplete, or unreadable. What it does not read is whether an index still
		// matches the text the generator would write, which is artifacthome's own
		// decision — an operator who added a paragraph has not broken their index —
		// so a generator whose wording moved on leaves the doors saying less than it
		// does and nothing reports it.
		detail := "artifact-readmes = " + string(finding.Status) + ": " + finding.Summary + " (" + finding.Detail + ")"
		reported := governeddoc.Defect{Path: resolved.Config.Product.Specifications, Detail: detail}
		governeddoc.Report(resolved.Config, []governeddoc.Defect{reported}, t.Errorf, governeddoc.Escalate)
		return
	}
	// The homes are named rather than counted, because which directory the check
	// read is the whole of what a reader would act on — the invariants home most
	// of all, since it carries its own identity scheme and is still a home this
	// check asks an index of. A home missing from the detail is the check having
	// stopped reading it, which is the reader rather than any document.
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
	defects := make([]governeddoc.Defect, 0, len(artifacts.Problems)+len(artifacts.ReferenceProblems))
	for _, problem := range artifacts.Problems {
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: problem.Reason})
	}
	for _, problem := range artifacts.ReferenceProblems {
		detail := string(problem.Kind) + " — " + problem.Reason
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: detail})
	}
	// An index loaded as an artifact is the exemption having stopped working,
	// which is this package's own code rather than anything the document says.
	for _, recorded := range artifacts.Artifacts {
		if strings.EqualFold(path.Base(recorded.Path), artifacthome.FileName) {
			t.Errorf("%s was loaded as the artifact %q; an index states no intent anything refers to", recorded.Path, recorded.ID)
		}
	}

	invariants, err := (invariant.Store{RepositoryRoot: root, Directory: product.Invariants}).Load()
	if err != nil {
		t.Fatalf("invariant Load() error = %v", err)
	}
	for _, problem := range invariants.Problems {
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: problem.Reason})
	}
	governeddoc.Report(resolved.Config, defects, t.Errorf, governeddoc.Escalate)
	// The constraints themselves still load, so a clean report is the index being
	// skipped rather than the whole directory going unread. A directory that
	// yielded nothing is the reader, and it fails here.
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
