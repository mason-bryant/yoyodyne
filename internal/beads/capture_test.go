package beads

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// capturedListing is `bd list --all --id=yoyodyne-ifd.121,yoyodyne-ifd.121.2
// --json` as this project's own tracker answered it, kept verbatim: nothing in
// it was written here, which is the whole of what it is for.
//
// It was taken with bd 1.1.2 on 2026-09-04, against a copy of the store made in
// the run's scratch directory rather than against the store itself — a
// developer run's sandbox refuses that one, and a copy answers the same
// question without writing to what the harness and the product manager share.
const capturedListing = "bd-list-parent-child.json"

// TestACapturedListingStatesParentageAsAnEdgeAttributedToTheChild pins the one
// assumption the epic-coverage guard rests on to output the tracker actually
// produced, rather than to a payload written alongside the reading of it. Every
// other check of the direction is a scripted runner replaying what it was
// handed, so it agrees with itself whichever way bd answers; this one disagrees
// if bd answers the other way.
//
// The pair is the one the guard was written for. yoyodyne-ifd.121 was started as
// two developer runs of one scope — the epic and the child carrying its
// execution — and it is the discriminating case for the direction besides: it is
// a child of the root epic as well as the parent of yoyodyne-ifd.121.2, so it
// carries a parent-child edge of its own that points up. A reading that took an
// edge's ends the other way round would report it as broken out of its own
// child, defer the child, and run the epic.
func TestACapturedListingStatesParentageAsAnEdgeAttributedToTheChild(t *testing.T) {
	t.Parallel()

	const (
		child = "yoyodyne-ifd.121.2"
		epic  = "yoyodyne-ifd.121"
		root  = "yoyodyne-ifd"
	)
	byID := make(map[string]WorkItem)
	var held []string
	for _, item := range decodeCapturedListing(t) {
		byID[item.ID] = item
		held = append(held, item.ID)
	}
	decomposed, hasChild := byID[child]
	whole, hasEpic := byID[epic]
	if !hasChild || !hasEpic {
		t.Fatalf("the captured listing holds %v, want both %s and %s", held, epic, child)
	}

	// The direction itself, read off the capture: the edge is attributed to the
	// child and names the parent.
	if named := parentEdgesOf(decomposed); !slices.Equal(named, []string{epic}) {
		t.Fatalf("the child's own parent-child edges name %v, want just the epic %s; the tracker attributes the edge to "+
			"the child and points it at the parent", named, epic)
	}
	// And the epic's own edge points at what it was broken out of rather than at
	// what was broken out of it, which is the half a mirrored reading gets wrong.
	if named := parentEdgesOf(whole); !slices.Equal(named, []string{root}) {
		t.Fatalf("the epic's own parent-child edges name %v, want just its own parent %s; an edge read in the other "+
			"direction makes an epic read as work broken out of its own child", named, root)
	}

	if got := decomposed.DecomposedFrom(); got != epic {
		t.Fatalf("the child's DecomposedFrom() = %q, want the epic %s", got, epic)
	}
	if got := whole.DecomposedFrom(); got == child {
		t.Fatalf("the epic's DecomposedFrom() = %q, the child it was never broken out of", got)
	}

	// The listing states parentage both ways, so the field answers on its own and
	// the reading above would be right whatever the edges said. The edge reading
	// is what a store stating only the edge is left with — the tracker's own
	// export is one, carrying no parent field on any item in it — so it is checked
	// with the field taken away rather than only alongside it.
	edgeOnly := decomposed
	edgeOnly.Parent = ""
	if got := edgeOnly.DecomposedFrom(); got != epic {
		t.Fatalf("the child's DecomposedFrom() = %q from its edges alone, want the epic %s", got, epic)
	}
	wholeEdgeOnly := whole
	wholeEdgeOnly.Parent = ""
	if got := wholeEdgeOnly.DecomposedFrom(); got == child {
		t.Fatalf("the epic's DecomposedFrom() = %q from its edges alone, the child it was never broken out of", got)
	}
	// What makes that a second reading rather than the same one twice.
	if decomposed.Parent != epic {
		t.Fatalf("the child's parent field = %q, want %s; the capture is what says the listing states parentage both "+
			"ways, and a reading of the field alone is only sound while it does", decomposed.Parent, epic)
	}
}

func decodeCapturedListing(t *testing.T) []WorkItem {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", capturedListing))
	if err != nil {
		t.Fatalf("read the captured listing: %v", err)
	}
	items, err := decodeWorkItems(data)
	if err != nil {
		t.Fatalf("decode the captured listing: %v; the client must read what the tracker actually answers", err)
	}
	return items
}
