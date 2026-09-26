package backlog

// A re-scope's child under a parent whose change never landed. The tracker will
// not link a child to wait on its own parent, so what holds one is the child's
// own statement that it builds on the parent's change, read here at every pull,
// and what releases it is that change landing — however it lands.

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

const (
	unlandedParent = "yoyodyne-ifd.429.13"
	buildingChild  = "yoyodyne-ifd.429.13.1"
	supersedingOne = "yoyodyne-ifd.429.13.2"
)

// where is the account the read model gives of the parent's change.
const where = "the change run run-1 made for yoyodyne-ifd.429.13 never reached main, and is on yoyodyne/ifd-429-13 at commit abc, published as pull request #757"

func substrateItems(parentStatus string) []beads.WorkItem {
	items := []beads.WorkItem{
		{ID: buildingChild, Title: "carry the rest", Description: "Builds on the parent's change: assumes its two new files.", Status: "open", Priority: 1, Parent: unlandedParent},
		{ID: supersedingOne, Title: "rewrite it from main", Description: "Supersedes pull request 757.", Status: "open", Priority: 1, Parent: unlandedParent},
	}
	if parentStatus != "" {
		items = append(items, beads.WorkItem{ID: unlandedParent, Title: "the re-scoped item", Status: parentStatus, Priority: 2})
	}
	return items
}

func entryFor(t *testing.T, queue Queue, id string) Entry {
	t.Helper()
	for _, entry := range queue.Entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("%s is not in the queue: %#v", id, queue.Entries)
	return Entry{}
}

// The child that says it builds on the parent's change waits for it, as a wait
// and not a hold for a person; the child that supersedes it is pulled.
func TestAChildBuildingOnAnUnlandedParentIsHeldAndOneSupersedingItIsNot(t *testing.T) {
	t.Parallel()

	held := ReadHolds(nil).OnUnlandedParents(map[string]string{unlandedParent: where})
	queue := Order(substrateItems("open"), []string{buildingChild, supersedingOne, unlandedParent}, held)

	building := entryFor(t, queue, buildingChild)
	if building.Ready {
		t.Fatalf("the child that builds on the unlanded change is ready: %#v", building)
	}
	if !building.AwaitingLanding || building.HoldKind() != HeldWaitingOn {
		t.Fatalf("the child is held as %q (landing %v), want a wait on the parent's change", building.HoldKind(), building.AwaitingLanding)
	}
	if isHeld, _ := building.Awaits(); isHeld {
		t.Fatal("a wait on the parent's change was counted as held for a person")
	}
	for _, want := range []string{"builds on " + unlandedParent + "'s change", "yoyodyne/ifd-429-13", "pull request #757"} {
		if !strings.Contains(building.Hold(), want) {
			t.Fatalf("the hold is missing %q: %s", want, building.Hold())
		}
	}
	if superseding := entryFor(t, queue, supersedingOne); !superseding.Ready {
		t.Fatalf("the child superseding the parent's change is held: %s", superseding.Hold())
	}
}

// Every route the change can land by releases the child without anybody
// touching it: the records no longer saying it is unlanded, or the parent
// leaving the backlog closed.
func TestAChildBuildingOnItsParentIsReleasedWhenTheChangeLands(t *testing.T) {
	t.Parallel()

	for name, read := range map[string]struct {
		items []beads.WorkItem
		held  Holds
	}{
		"a later run promoted the change": {
			items: substrateItems("open"),
			held:  ReadHolds(nil).OnUnlandedParents(map[string]string{}),
		},
		"the parent closed and left the backlog": {
			items: substrateItems(""),
			held:  ReadHolds(nil).OnUnlandedParents(map[string]string{unlandedParent: where}),
		},
		"the parent's edge says it is closed": {
			items: func() []beads.WorkItem {
				items := substrateItems("")
				items[0].Parent = ""
				items[0].Dependencies = []beads.Dependency{{ID: unlandedParent, Type: "parent-child", Status: "closed"}}
				return items
			}(),
			held: ReadHolds(nil).OnUnlandedParents(map[string]string{unlandedParent: where}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			queue := Order(read.items, []string{buildingChild, supersedingOne}, read.held)
			if entry := entryFor(t, queue, buildingChild); !entry.Ready {
				t.Fatalf("the child is still held: %s", entry.Hold())
			}
		})
	}
}

// A parent that is being run again has not landed anything yet, and its edge
// says so, so the child keeps waiting.
func TestAChildWaitsWhileItsParentIsStillUnfinishedOutsideTheBacklog(t *testing.T) {
	t.Parallel()

	items := substrateItems("")
	items[0].Dependencies = []beads.Dependency{{ID: unlandedParent, Type: "parent-child", Status: "in_progress"}}
	queue := Order(items, []string{buildingChild, supersedingOne}, ReadHolds(nil).OnUnlandedParents(map[string]string{unlandedParent: where}))
	if entry := entryFor(t, queue, buildingChild); entry.Ready {
		t.Fatal("the child was released while its parent is still being worked")
	}
}

// A stoppage of the child's own is somebody's to release, and it is what the
// child is told, because it would still refuse the child once the parent landed.
func TestAChildsOwnStoppageAnswersAheadOfItsParentsChange(t *testing.T) {
	t.Parallel()

	held := ReadHolds(map[string]Hold{buildingChild: {Reason: "run run-2 stopped on it"}}).
		OnUnlandedParents(map[string]string{unlandedParent: where})
	entry := entryFor(t, Order(substrateItems("open"), []string{buildingChild}, held), buildingChild)
	if entry.AwaitingLanding || entry.HoldKind() != HeldForAPerson {
		t.Fatalf("the child's own stoppage was shadowed: %q, landing %v", entry.Hold(), entry.AwaitingLanding)
	}
}

// The phrasing is narrow: it names the parent, or says "the parent", and it
// is read from what somebody authored rather than the notes.
func TestBuildsOnParentReadsOnlyTheChildsOwnStatementAboutItsParent(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]bool{
		"Builds on the parent's files.":                      true,
		"builds on yoyodyne-ifd.429.13's branch":             true,
		"This builds on the parent’s change.":                true,
		"builds on yoyodyne-ifd.7's change":                  false,
		"builds on the existing machinery":                   false,
		"Supersedes the parent's change on pull request 757": false,
	} {
		if got := BuildsOnParent(beads.WorkItem{Description: text}, unlandedParent); got != want {
			t.Errorf("BuildsOnParent(%q) = %v, want %v", text, got, want)
		}
	}
	if BuildsOnParent(beads.WorkItem{Notes: "Builds on the parent's change."}, unlandedParent) {
		t.Error("the notes were read as the child's own statement")
	}
}
