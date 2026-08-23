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

import (
	"testing"
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
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what a reader needs is the
		// file, the line, and the target, and a tally sends them looking.
		t.Errorf("a link in this repository's documentation resolves to nothing: %s", problem)
	}
}
