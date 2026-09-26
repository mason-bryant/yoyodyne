package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// An entry is about an item, and a closed item asks nobody anything. Closing
// the item closes every entry standing for it, with the reason, and leaves the
// entries of every other item where they were.
func TestAnEntryIsClosedWithItsItem(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	other := stoppedState()
	other.RunID = "run-" + strings.Repeat("f", 32)
	other.WorkItemID = "yoyodyne-other"
	recorded := &recordedDecisions{}
	docketer := Docketer{
		Docket:    store,
		Runs:      recordedRuns{states: []runstate.State{stoppedState(), other}},
		Decisions: recorded,
		Reruns:    recorded,
		Caps:      docketedCaps,
		Triage:    docketedTriage,
		Clock:     docketClock{},
	}
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 2 || len(built.Entries) != 2 {
		t.Fatalf("build = %#v, want both stoppages docketed", built)
	}

	const reason = "closed by run run-x, whose change landed"
	closed, err := docketer.SettleClosedItem(docketedItem, reason)
	if err != nil {
		t.Fatalf("SettleClosedItem() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want the one entry standing for the item", closed)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		switch entry.WorkItemID {
		case docketedItem:
			if entry.Closed == nil {
				t.Fatalf("entry of the closed item = %#v, want it closed", entry)
			}
			if entry.Closed.Decision != closedItemDecision || entry.Closed.Reason != reason {
				t.Fatalf("closure = %#v, want %q with the reason %q", entry.Closed, closedItemDecision, reason)
			}
			if !strings.Contains(entry.Closed.DecidedBy, "the harness") {
				t.Fatalf("closed by %q, want the harness named", entry.Closed.DecidedBy)
			}
		case other.WorkItemID:
			if entry.Closed != nil {
				t.Fatalf("entry of an open item = %#v, want it left standing", entry)
			}
		}
	}
	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rebuilt.Added != 0 || len(rebuilt.Entries) != 1 || rebuilt.Entries[0].WorkItemID != other.WorkItemID || rebuilt.Closed != 1 {
		t.Fatalf("rebuild = %#v, want only the open item's entry listed and the closed one counted", rebuilt)
	}
	// Closing it again is nothing to do: the entry is already off the docket.
	if again, err := docketer.SettleClosedItem(docketedItem, reason); err != nil || again != 0 {
		t.Fatalf("SettleClosedItem() again = %d, %v, want nothing closed twice", again, err)
	}

	// The sweep closes from the tracker's closed listing, whatever closed the item.
	swept, err := docketer.SettleClosedItems(ClosedItemReasons([]beads.WorkItem{
		{ID: docketedItem, Status: "closed"},
		{ID: other.WorkItemID, Status: "closed"},
	}, "a reconcile sweep"))
	if err != nil {
		t.Fatalf("SettleClosedItems() error = %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want the one entry still standing", swept)
	}
	entries, err = store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range entries {
		if entry.Closed == nil {
			t.Fatalf("entry = %#v, want every entry closed", entry)
		}
		if entry.WorkItemID == other.WorkItemID && !strings.Contains(entry.Closed.Reason, "the tracker holds yoyodyne-other as closed, as a reconcile sweep read it") {
			t.Fatalf("swept reason = %q, want the tracker's reading named", entry.Closed.Reason)
		}
	}
}

// A decision to wait that has lapsed leaves its entry a question again, and a
// closed item answers it: the entry is closed with the item.
func TestALapsedWaitIsClosedWithItsItem(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stoppedState()}, docket)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	key := docket.entries[0].Key
	docket.waitOn(key, docketedNow.Add(-2*time.Hour), docketedNow.Add(-time.Hour))

	closed, err := docketer.SettleClosedItem(docketedItem, "retired without being done")
	if err != nil || closed != 1 {
		t.Fatalf("SettleClosedItem() = %d, %v, want the lapsed entry closed", closed, err)
	}
	if got := docket.closed[key]; got.Decision != closedItemDecision || got.Reason != "retired without being done" {
		t.Fatalf("closure = %#v", got)
	}
}

// A run that lands closes its item, and with it the entry an earlier run of the
// same item left standing on the docket.
func TestALandingClosesTheEntryItsItemHadStanding(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: docketedItem, Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
	// The stoppage an earlier run of this item left, still on the docket.
	if created, err := pipeline.Docket.RecordStoppedRun(stoppedState()); err != nil || !created {
		t.Fatalf("RecordStoppedRun() = %t, %v", created, err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the item closed by the landing", outcome)
	}
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Closed == nil {
		t.Fatalf("docket = %#v, want the earlier stoppage closed", entries)
	}
	closure := entries[0].Closed
	if closure.Decision != closedItemDecision || !strings.Contains(closure.Reason, "closed by run "+outcome.RunID+", whose change landed") {
		t.Fatalf("closure = %#v, want it closed with the landing named", closure)
	}
	if entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("entry = %#v", entries[0])
	}
}

// An unfinished publication asks about a merge the forge holds, not about the
// item: an item closes as its change is integrated, while the merge can still be
// dropped or stuck afterwards. So its entry stays standing when the item closes,
// beside the stopped run's entry, which does not.
func TestAnUnfinishedPublicationIsNotClosedWithItsItem(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stoppedState()}, docket)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	stopped := docket.entries[0]
	publication := stopped
	publication.Class = triage.ClassPublication
	publication.Key = triage.PublicationKey(stopped.RunID, 7)
	docket.entries = append(docket.entries, publication)

	for _, closer := range []func() (int, error){
		func() (int, error) {
			return docketer.SettleClosedItem(docketedItem, "closed by run run-x, whose change landed")
		},
		func() (int, error) {
			return docketer.SettleClosedItems(ClosedItemReasons([]beads.WorkItem{{ID: docketedItem, Status: "closed"}}, "a reconcile sweep"))
		},
	} {
		if _, err := closer(); err != nil {
			t.Fatalf("closing with the item: %v", err)
		}
	}
	if got, closed := docket.closed[stopped.Key]; !closed || got.Decision != closedItemDecision {
		t.Fatalf("stopped-run closure = %#v, want it closed with its item", got)
	}
	if got, closed := docket.closed[publication.Key]; closed {
		t.Fatalf("publication closure = %#v, want the unfinished publication left standing", got)
	}
}
