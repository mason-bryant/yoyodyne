package terms

// The check itself, run over this repository rather than over a fixture. This is
// the point of the package: the fixtures above prove the checker works, and this
// is what makes a coinage nothing defines fail a declared check instead of
// waiting for a reviewer to notice it, or for an operator to meet a word with
// nowhere to look it up.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is applied on every verify pass of every run — including each repair — the
// same way the link, goals, and artifact gates beside it are.

import (
	"testing"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The documents are read where they actually live rather than from a
// copy, because a copy is exactly what cannot carry the word that came back.
const repositoryRoot = "../.."

func TestThisRepositoryOwnCoinedTermsAreRegistered(t *testing.T) {
	t.Parallel()

	documents, err := Documents(repositoryRoot)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	// A walk that found nothing would report no coinage, which is the one way this
	// gate can pass while reading nothing at all.
	if len(documents) == 0 {
		t.Fatal("no documents were found in this repository's artifact homes; the walk is looking in the wrong place")
	}
	entries, err := Register(repositoryRoot)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s carries no entries; a register nothing is in permits nothing and proves nothing", RegisterPath)
	}
	problems, err := Check(repositoryRoot)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what a reader needs is the
		// file, the line, and what to write instead.
		t.Errorf("%s", problem)
	}
}
