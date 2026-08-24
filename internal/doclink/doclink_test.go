package doclink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestALinkToADocumentThatIsNotThereIsReported(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/guide.md", `# Guide

See [the brief](product/brief.md) and [the design](designs/v1.md).
`)
	write(t, root, "docs/product/brief.md", "# Brief\n")

	problems := check(t, root)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if problems[0].Path != "docs/guide.md" || problems[0].Line != 3 || problems[0].Target != "designs/v1.md" {
		t.Fatalf("problem = %#v", problems[0])
	}
	// The reason names the path as it resolves rather than only as it was
	// written: a relative link is wrong in a way that is invisible until
	// somebody resolves it against the document making it.
	if !strings.Contains(problems[0].Reason, "docs/designs/v1.md") {
		t.Fatalf("reason = %q, want it to name where the link resolves to", problems[0].Reason)
	}
}

func TestALinkToAHeadingTheTargetDoesNotCarryIsReported(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The half of a link nobody can check by eye: the file is right there, and
	// whether it answers to the fragment depends on a slug nothing writes down.
	write(t, root, "README.md", `# Readme

See [the invariants](docs/design.md#design-invariants) and
[what is deferred](docs/design.md#deferred-beyond-v1).
`)
	write(t, root, "docs/design.md", `# Design

## Design invariants

## Deferred beyond v2
`)

	problems := check(t, root)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if problems[0].Target != "docs/design.md#deferred-beyond-v1" || problems[0].Line != 4 {
		t.Fatalf("problem = %#v", problems[0])
	}
}

func TestAnAnchorMovedToAnotherDocumentFailsTheCheck(t *testing.T) {
	t.Parallel()

	// The failure this package exists for, in the shape it actually arrives in: a
	// guide is split, a section moves into the new document, and every link that
	// named it as a `#fragment` of the old one still reads exactly as it did. The
	// link did not change, so the patch that broke it does not contain it — which
	// is why the moved anchor has to fail a check rather than wait for a reader.
	root := newRepository(t)
	write(t, root, "docs/configuration.md", `# Configuration

See [checks](#checks) and [how long a check may take](#how-long-a-check-may-take).

## How long a check may take
`)
	write(t, root, "docs/configuration/runs.md", "# Runs\n\n## Checks\n")

	problems := check(t, root)
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if problems[0].Path != "docs/configuration.md" || problems[0].Line != 3 || problems[0].Target != "#checks" {
		t.Fatalf("problem = %#v", problems[0])
	}
	if !strings.Contains(problems[0].Reason, "carries no heading with that anchor") {
		t.Fatalf("reason = %q", problems[0].Reason)
	}
}

func TestALinkThatResolvesIsNotReported(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "README.md", "# Readme\n\nSee [the guide](docs/guide.md).\n")
	write(t, root, "docs/guide.md", `# Guide

Up to [the readme](../README.md), across to
[a goal](product/goals/v1-goals.md#goals), down to [its own section](#what-this-covers),
from the root at [the readme again](/README.md), at a whole
[directory](product/goals), and a picture: ![a face](../avatar.png).

## What this covers
`)
	write(t, root, "docs/product/goals/v1-goals.md", "# V1 goals\n\n## Goals\n")
	write(t, root, "avatar.png", "not really a picture")

	if problems := check(t, root); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestALinkOutsideTheRepositoryIsNotResolvedAgainstTheFilesystem(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// An absolute URL is somebody else's to keep working, and resolving one would
	// make a deterministic check depend on the network. A relative path that
	// climbs out of the repository is the opposite case and is reported, because
	// what it names is not something this checkout contains.
	write(t, root, "README.md", `# Readme

[The forge](https://github.com/mason-bryant/yoyodyne/pull/53), a
[mail link](mailto:nobody@example.com), and [something above](../elsewhere.md).
`)

	problems := check(t, root)
	if len(problems) != 1 || problems[0].Target != "../elsewhere.md" {
		t.Fatalf("problems = %v", problems)
	}
	if !strings.Contains(problems[0].Reason, "leaves the repository") {
		t.Fatalf("reason = %q", problems[0].Reason)
	}
}

func TestALinkThatIsAnExampleOfALinkIsNotOneToResolve(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A guide that teaches the syntax writes links that point nowhere on purpose.
	// Reporting them would be the check fighting the documentation it is for.
	write(t, root, "docs/guide.md", "# Guide\n\nWrite `[a label](some/path.md)` like this:\n\n"+
		"```markdown\n[the brief](../product/brief.md)\n```\n\nAnd that is all.\n")

	if problems := check(t, root); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestARepeatedHeadingIsReachableByTheAnchorTheForgeGivesIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/guide.md", `# Guide

[The first](#why) and [the second](#why-1), but not [a third](#why-2).

## Why

## Why
`)

	problems := check(t, root)
	if len(problems) != 1 || problems[0].Target != "#why-2" {
		t.Fatalf("problems = %v", problems)
	}
}

func TestAHeadingsMarkupIsNotPartOfTheAnchorItAnswersTo(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The slug is derived from what the heading reads as. An underscore is
	// deliberately kept — the forge keeps one — so a setting named in a heading
	// stays reachable as itself.
	write(t, root, "docs/guide.md", "# Guide\n\n"+
		"[Timeout](#the-check_timeout-setting), [emphasis](#why-this-matters), [a linking heading](#the-brief).\n\n"+
		"## The `check_timeout` setting\n\n"+
		"## Why **this** matters\n\n"+
		"## [The brief](product/brief.md)\n")
	write(t, root, "docs/product/brief.md", "# Brief\n")

	if problems := check(t, root); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestFixturesWrittenToBeBrokenAreNotWalked(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "README.md", "# Readme\n")
	write(t, root, "internal/orchestrator/testdata/fixture/README.md", "# Fixture\n\n[nowhere](nothing.md)\n")

	documents, err := Documents(root)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	if len(documents) != 1 || documents[0] != "README.md" {
		t.Fatalf("Documents() = %v", documents)
	}
	if problems := check(t, root); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

// check runs the checker over a repository the test wrote, failing rather than
// returning when the walk itself could not be done: a walk that could not read a
// document reports nothing, which reads exactly like a repository whose links
// all resolve.
func check(t *testing.T, root string) []Problem {
	t.Helper()

	problems, err := Check(root)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	return problems
}

func newRepository(t *testing.T) string {
	t.Helper()

	return t.TempDir()
}

func write(t *testing.T, root, relative, content string) {
	t.Helper()

	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
