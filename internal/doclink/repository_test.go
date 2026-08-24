package doclink

// The check itself, run over this repository rather than over a fixture. This
// is the whole point of the package: the fixtures above prove the checker works,
// and this is what makes a broken link in this project's documentation fail a
// declared check instead of costing a reviewer a paragraph saying they could not
// verify it.
//
// It runs under `make test`, which is one of this project's declared checks, so
// it is checked on every verify pass of every run — including each repair — for
// the same reason the artifact and goals gates beside it are. `make links` runs
// this package alone and `make check` puts that first, so a moved anchor is
// answered in a second rather than behind the Go suite; the gate is `make test`
// either way, because that is what the project declares.

import (
	"os"
	"path/filepath"
	"strings"
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

// pinnedAnchors are headings this repository holds in place for a link the check
// above cannot see. Check resolves what Markdown documents write, and a citation
// written as a forge URL is an absolute URL — deliberately somebody else's to
// keep working, because resolving one would put the network in a deterministic
// check. A forge URL pointing back into this repository is the exception that
// costs: it names a heading that is right here, so moving that heading breaks it
// exactly the way a Markdown link breaks, and nothing on either side notices.
//
// The entries are few and they are meant to stay few. Each one is a heading
// somebody has to leave alone, so the field saying who needs it is what lets a
// later reader retire the entry rather than inherit it.
var pinnedAnchors = []struct {
	// Source is the file carrying the link and Document the file carrying the
	// heading, both repository-relative; Fragment is the anchor itself.
	Source   string
	Document string
	Fragment string
	// Why is who is served by the heading staying put, so that an entry whose
	// reason has gone can be removed instead of pinning a heading for nobody.
	Why string
}{
	{
		Source:   "internal/config/scaffold.go",
		Document: "docs/configuration.md",
		Fragment: "checks",
		Why:      "the link `yoyo init` writes into every generated project's configuration, for a project that has no checkout of Yoyodyne to read the guide out of",
	},
}

// The other half of the same guarantee, for the anchors above: that the heading
// is still there, and that whoever needed it still does. Both directions matter.
// A pin nobody needs is a heading somebody is being stopped from moving for no
// reason, and it goes stale silently in exactly the way the dead link would.
func TestThisRepositoryOwnPinnedAnchorsResolve(t *testing.T) {
	t.Parallel()

	for _, pinned := range pinnedAnchors {
		document, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(pinned.Document)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", pinned.Document, err)
		}
		if !anchorsIn(string(document))[pinned.Fragment] {
			t.Errorf("%s carries no heading with the anchor %q, and %s links to it: %s",
				pinned.Document, pinned.Fragment, pinned.Source, pinned.Why)
		}
		source, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(pinned.Source)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", pinned.Source, err)
		}
		if !strings.Contains(string(source), pinned.Document+"#"+pinned.Fragment) {
			t.Errorf("%s no longer links to %s#%s, so nothing needs that heading pinned any more; retire the entry rather than leaving it holding a heading in place for nobody",
				pinned.Source, pinned.Document, pinned.Fragment)
		}
	}
}
