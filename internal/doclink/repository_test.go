package doclink

// The check itself, run over this repository rather than over a fixture. This
// is the whole point of the package: the fixtures above prove the checker works,
// and this is what makes a broken link in this project's documentation fail a
// declared check instead of costing a reviewer a paragraph saying they could not
// verify it.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is checked on every verify pass of every run — including each repair — for
// the same reason the artifact and goals gates beside it are.
//
// Where the broken link is decides what happens about it. Most of this
// repository's Markdown is a developer's to edit, and a link broken there still
// fails here. A link written inside an artifact home is in a document every
// developer's diff refuses, so it is escalated to the role that owns that home
// and fails `yoyo artifact check` instead — see internal/governeddoc.

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The documents are read where they actually live rather than from a
// copy, because a copy is exactly what cannot have the link that is broken.
const repositoryRoot = "../.."

func TestThisRepositoryOwnDocumentationLinksResolve(t *testing.T) {
	t.Parallel()

	documents, err := Documents(repositoryRoot)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	// A walk that found nothing would report no broken links, which is the one
	// way this gate can pass while checking nothing at all.
	if len(documents) == 0 {
		t.Fatal("no Markdown documents were found in this repository; the walk is looking in the wrong place")
	}
	problems, err := Check(repositoryRoot)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	// Reported one at a time rather than as a count: what a reader needs is the
	// file, the line, and the target, and a tally sends them looking.
	defects := make([]governeddoc.Defect, 0, len(problems))
	for _, problem := range problems {
		detail := fmt.Sprintf("line %d: %s", problem.Line, problem.Reason)
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: detail})
	}
	cfg, read := repositoryConfiguration()
	if !read {
		// Nothing here then knows which directories are artifact homes, so every
		// broken link is reported as one to fix here. That is the stricter of the
		// two answers, which is the right way to be wrong: a link this gate failed
		// on and could have escalated costs one message, and the reverse costs a
		// broken link nobody fails on. The configuration is itself a protected path,
		// so it is not this check's to diagnose either.
		t.Log("this repository's configuration could not be read; every broken link below is reported as one to fix here")
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)
}

// repositoryConfiguration is this repository's own configuration, discovered the
// way the commands discover it. It is read rather than assumed for the reason
// every other gate reads it: which directories are artifact homes is the
// project's to say, and a hardcoded list would route a moved home to nobody.
func repositoryConfiguration() (config.Config, bool) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return config.Config{}, false
	}
	discovered, err := config.Discover(root)
	if err != nil {
		return config.Config{}, false
	}
	loaded, err := config.Load(discovered)
	if err != nil {
		return config.Config{}, false
	}
	return loaded, true
}
