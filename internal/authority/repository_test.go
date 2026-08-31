package authority

// The check itself, run over this repository rather than over a fixture. This is
// the point of the package: the fixtures beside it prove the checker works, and
// this is what makes an authorization site nobody listed fail a declared check
// instead of waiting for somebody to notice the inventory has gone stale.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is applied on every verify pass of every run — including each repair — the
// same way the link, terms, and artifact gates beside it are.

import (
	"testing"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory. The code is read where it actually lives rather than from a copy,
// because a copy is exactly what cannot carry the check that moved.
const repositoryRoot = "../.."

func TestThisRepositoryOwnAuthorityChecksAreListed(t *testing.T) {
	t.Parallel()

	entries, exemptions, err := Inventory(repositoryRoot)
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s lists no checks; an inventory nothing is in pins nothing", InventoryPath)
	}
	if len(exemptions) == 0 {
		t.Fatalf("%s excuses nothing, and the sweep finds sites this repository deliberately does not list; the second table is being read from somewhere it is not", InventoryPath)
	}
	// A sweep that found nothing would report no unlisted site, which is the one
	// way this gate can pass while reading nothing at all.
	sites, err := Sites(repositoryRoot)
	if err != nil {
		t.Fatalf("Sites() error = %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("the sweep found no authorization sites in this repository; it is looking in the wrong place")
	}
	problems, err := Check(repositoryRoot)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what a reader needs is the
		// file, the declaration, and what to write.
		t.Errorf("%s", problem)
	}
}
