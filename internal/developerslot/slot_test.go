package developerslot

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

var (
	dashboard = domain.DeveloperSlot{Prefer: []string{"dashboard"}}
	nothing   = domain.DeveloperSlot{}
	at        = time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
)

func run(id string, started time.Duration, labels ...string) Run {
	return Run{RunID: "run-" + id, WorkItemID: id, Labels: labels, StartedAt: at.Add(started)}
}

func TestAssignPutsLabelledWorkInThePreferringSlotWhicheverStartedFirst(t *testing.T) {
	assignment := Assign(2, []domain.DeveloperSlot{dashboard, nothing}, []Run{
		run("plain", 0),
		run("panel", time.Minute, "dashboard"),
	})
	if got := assignment.Slots[0].WorkItemID; got != "panel" {
		t.Fatalf("slot 1 holds %q, want the dashboard item", got)
	}
	if got := assignment.Slots[1].WorkItemID; got != "plain" {
		t.Fatalf("slot 2 holds %q, want the unlabelled item", got)
	}
	if free := assignment.Free(); len(free) != 0 {
		t.Fatalf("free slots = %v, want none", free)
	}
}

func TestAssignFillsTheUnpreferringSlotBeforeAPreferringSlotFallsBack(t *testing.T) {
	assignment := Assign(2, []domain.DeveloperSlot{dashboard, nothing}, []Run{run("plain", 0)})
	if !assignment.Slots[0].Free() {
		t.Fatalf("slot 1 holds %q, want it free for dashboard work", assignment.Slots[0].WorkItemID)
	}
	if got := assignment.Slots[1].WorkItemID; got != "plain" {
		t.Fatalf("slot 2 holds %q, want the unlabelled item", got)
	}
	free := assignment.Free()
	if len(free) != 1 || free[0].Number != 1 || !free[0].Preferring() {
		t.Fatalf("free slots = %v, want slot 1 preferring dashboard", free)
	}
}

func TestAssignLetsAPreferringSlotFallBackOnceTheRestAreFull(t *testing.T) {
	assignment := Assign(2, []domain.DeveloperSlot{dashboard, nothing}, []Run{
		run("first", 0),
		run("second", time.Minute),
	})
	if got := assignment.Slots[1].WorkItemID; got != "first" {
		t.Fatalf("slot 2 holds %q, want the older unlabelled run", got)
	}
	if got := assignment.Slots[0].WorkItemID; got != "second" {
		t.Fatalf("slot 1 holds %q, want the run the preferring slot fell back to", got)
	}
}

func TestAssignNamesRunsBeyondTheCapacityAsOverflow(t *testing.T) {
	assignment := Assign(1, []domain.DeveloperSlot{dashboard}, []Run{
		run("plain", 0),
		run("panel", time.Minute, "dashboard"),
	})
	if got := assignment.Slots[0].WorkItemID; got != "panel" {
		t.Fatalf("slot 1 holds %q, want the labelled run ahead of the older unlabelled one", got)
	}
	if len(assignment.Overflow) != 1 || assignment.Overflow[0].WorkItemID != "plain" {
		t.Fatalf("overflow = %v, want the unlabelled run", assignment.Overflow)
	}
}

func TestFreeListsPreferringSlotsFirstInConfiguredOrder(t *testing.T) {
	reliability := domain.DeveloperSlot{Prefer: []string{"reliability"}}
	assignment := Assign(4, []domain.DeveloperSlot{nothing, dashboard, nothing, reliability}, nil)
	var numbers []int
	for _, slot := range assignment.Free() {
		numbers = append(numbers, slot.Number)
	}
	want := []int{2, 4, 1, 3}
	for index := range want {
		if index >= len(numbers) || numbers[index] != want[index] {
			t.Fatalf("free order = %v, want %v", numbers, want)
		}
	}
}

func TestAPreferenceListShorterThanTheCapacityLeavesTheRestPreferringNothing(t *testing.T) {
	assignment := Assign(3, []domain.DeveloperSlot{dashboard}, nil)
	if len(assignment.Slots) != 3 {
		t.Fatalf("%d slots, want 3", len(assignment.Slots))
	}
	if !assignment.Slots[0].Preferring() || assignment.Slots[1].Preferring() || assignment.Slots[2].Preferring() {
		t.Fatalf("preferences = %v, want only slot 1 preferring", assignment.Slots)
	}
}

func TestSaysNamesTheSlotAndItsPreference(t *testing.T) {
	cases := map[string]Slot{
		"developer slot 2": {Number: 2},
		"developer slot 1 (prefers the dashboard label)":      {Number: 1, Preferred: []string{"dashboard"}},
		"developer slot 3 (prefers the docs or tests labels)": {Number: 3, Preferred: []string{"docs", "tests"}},
		"developer slot 3 (prefers the a, b or c labels)":     {Number: 3, Preferred: []string{"a", "b", "c"}},
	}
	for want, slot := range cases {
		if got := slot.Says(); got != want {
			t.Errorf("Says() = %q, want %q", got, want)
		}
	}
}

func TestPreferringReportsWhetherAnySlotPrefersALabel(t *testing.T) {
	if Preferring([]domain.DeveloperSlot{nothing, nothing}) {
		t.Fatal("two slots preferring nothing reported as preferring")
	}
	if !Preferring([]domain.DeveloperSlot{nothing, dashboard}) {
		t.Fatal("a slot preferring dashboard was not reported")
	}
}
