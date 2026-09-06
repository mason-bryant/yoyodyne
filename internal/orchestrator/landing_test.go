package orchestrator

// Closure follows the kind of landing a run claimed, not the fact that it
// landed. The case these tests exist for is a real one: a run answered a work
// item it could not do yet with a diagnosis that said so in bold, the diagnosis
// integrated because it was good evidence, and the item closed against it —
// because closure mechanically followed integration and read nothing of what
// had landed. Both kinds are driven through the whole pipeline here rather than
// asserted over a hand-built state, because a test that set the claim itself
// would pass just as happily if nothing ever read one off a developer's reply.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// landingBlock is the claim as a developer's reply carries it.
func landingBlock(payload string) string {
	return landing.Fence + "\n" + payload + "\n```\n"
}

// The diagnosis this whole item comes from, replayed: a developer that finds the
// work is not doable yet lands the evidence for that and says so. The change is
// reviewed and integrated exactly as any other change is, and the item it was
// claimed for goes back to the backlog parked, with the diagnosis as the parking
// reason.
func TestAnHonestNotDoableYetLandingIntegratesAndReParksItsItem(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "The conversion is not doable yet; this change lands the diagnosis.\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the management-conversion design has not landed, so the anchor stays open"}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The evidence landed. That half is unchanged: an honest landing is worth
	// keeping and is promoted like anything else that passes the gate.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("an evidence landing did not integrate: %#v", outcome)
	}
	if outcome.WorkItemClosed {
		t.Fatal("the run closed an item its own landing said it does not discharge")
	}
	if tracker.closed {
		t.Fatalf("the tracker closed the item; calls = %v", tracker.calls)
	}
	if !tracker.reopened {
		t.Fatalf("the item was left claimed by a run that has ended; calls = %v", tracker.calls)
	}
	if tracker.item.Status != "open" {
		t.Errorf("item status = %q, want open", tracker.item.Status)
	}
	// The reason has to be on the item, or the item afterwards reads as work
	// somebody walked away from.
	if !strings.Contains(tracker.reopenReason, "the anchor stays open") {
		t.Errorf("the item does not carry the developer's account: %q", tracker.reopenReason)
	}
	if !strings.Contains(tracker.reopenReason, outcome.RunID) {
		t.Errorf("the item does not name the run whose evidence landed: %q", tracker.reopenReason)
	}
	// And the parking is what keeps the item out of the next pull. Without it the
	// item is back in the queue as ordinary open work, the run that made it is
	// recorded as succeeded so no brake counts it, and the next selection buys
	// another run of the same diagnosis.
	if !tracker.item.Parking.Parked() {
		t.Fatalf("the item went back to the backlog unparked; calls = %v", tracker.calls)
	}
	if !strings.Contains(tracker.item.Parking.Reason(), "the anchor stays open") {
		t.Errorf("the parking reason does not name what would release the item: %q", tracker.item.Parking)
	}
	if len(tracker.blockers) > 0 {
		t.Errorf("a landing that named no impediment made the item wait on one: %v", tracker.blockers)
	}
	// The notes an operator reads must not open by describing a completed item.
	if !strings.Contains(tracker.notes, "the item is parked") {
		t.Errorf("the recorded outcome reads as a discharged item: %q", tracker.notes)
	}
	if outcome.Landing != landing.OutcomeEvidence {
		t.Errorf("outcome landing = %q, want %q", outcome.Landing, landing.OutcomeEvidence)
	}
	// And the claim is durable, because the closure is not always made by the
	// process that read it.
	recorded := onlyRecordedRun(t, store)
	if recorded.Outcome != runstate.OutcomeSucceeded {
		t.Errorf("run outcome = %q, want %q: landing evidence is not a failed run", recorded.Outcome, runstate.OutcomeSucceeded)
	}
}

// The one alternative to the parking. A developer that can name the impediment
// as another work item asks for the item to be left open waiting on it, which is
// a marker selection honours and which releases itself when the impediment
// closes — where the parking waits for a person.
func TestALandingThatNamesItsImpedimentLeavesTheItemOpenWaitingOnIt(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker().holds("yoyodyne-impediment")
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the diagnosis\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the conversion needs yoyodyne-impediment to land first","blocked_by":"yoyodyne-impediment"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("a landing that does not discharge closed its item; calls = %v", tracker.calls)
	}
	if tracker.item.Parking.Parked() {
		t.Errorf("an item that names what it waits on was parked as well: %q", tracker.item.Parking)
	}
	if len(tracker.blockers) != 1 || tracker.blockers[0] != "yoyodyne-impediment" {
		t.Fatalf("the item was not made to wait on the impediment it named: %v", tracker.blockers)
	}
	// The dependency is recorded before the status. Between an item being made
	// open and being made to wait there is a window a watch session polling the
	// queue pulls it in, which is the bare openness this marker replaces.
	blocker := slices.Index(tracker.calls, "blocker")
	reopen := slices.Index(tracker.calls, "reopen")
	if blocker < 0 || reopen < 0 || blocker > reopen {
		t.Errorf("the item was returned to the backlog before it waited on anything: %v", tracker.calls)
	}
	if outcome.LandingBlockedBy != "yoyodyne-impediment" {
		t.Errorf("outcome landing marker = %q, want the item it named", outcome.LandingBlockedBy)
	}
	if !strings.Contains(tracker.notes, "yoyodyne-impediment") {
		t.Errorf("the item's notes do not say what it is waiting for: %q", tracker.notes)
	}
}

// The marker is developer-written text, and the shape check it passes proves only
// that it is identifier-shaped. Two different things go wrong with a name and
// both end here. A marker the tracker would refuse a dependency for fails the
// settlement of a run whose change is already integrated, leaving the item
// claimed with nothing watching it. A marker the tracker would accept and that
// holds nothing back is worse, because it looks like it worked: the item goes
// back unparked behind a satisfied dependency and the next pull selects it, which
// is the loop this whole item exists to close. Each of them takes the parking,
// which holds the item back without needing anything to be true of the tracker.
func TestAMarkerTheHarnessCannotUseTakesTheParkingInstead(t *testing.T) {
	t.Parallel()

	for _, unusable := range []struct {
		name    string
		marker  func(tracker *fakeTracker) string
		saysWhy string
	}{
		{
			// Nothing waits on itself, and the tracker refuses the dependency as a
			// cycle.
			name:    "a marker naming the item it was claimed on",
			marker:  func(tracker *fakeTracker) string { return tracker.item.ID },
			saysWhy: "nothing waits on itself",
		},
		{
			// The developer cannot open a work item, so a marker naming one that was
			// never admitted is the ordinary way this goes wrong.
			name:    "a marker naming work the tracker does not have",
			marker:  func(*fakeTracker) string { return "yoyodyne-never-admitted" },
			saysWhy: "the tracker did not confirm that item",
		},
		{
			// The dependency would be written and would hold nothing: every gate in
			// the harness reads a closed blocker as no blocker, so the item would sit
			// in the queue unparked behind it.
			name: "a marker naming work that is already closed",
			marker: func(tracker *fakeTracker) string {
				tracker.holdsItem(beads.WorkItem{ID: "yoyodyne-already-done", Title: "Done", Status: "closed"})
				return "yoyodyne-already-done"
			},
			saysWhy: "already closed",
		},
		{
			// A follow-on item that depends on the item it follows makes this shape
			// ordinary rather than exotic, and the tracker refuses the second edge as
			// a cycle.
			name: "a marker naming work that already waits on this item",
			marker: func(tracker *fakeTracker) string {
				tracker.holdsItem(beads.WorkItem{
					ID: "yoyodyne-follows-this", Title: "Follow-on", Status: "open",
					Dependencies: []beads.Dependency{{ID: tracker.item.ID, Type: "blocks"}},
				})
				return "yoyodyne-follows-this"
			},
			saysWhy: "already waits on this item",
		},
	} {
		t.Run(unusable.name, func(t *testing.T) {
			t.Parallel()

			tracker := newOutcomeTracker()
			provider := roleBackend(writeFeature, approveVerdict)
			provider.developerFinalText = "the diagnosis\n\n" +
				landingBlock(`{"outcome":"evidence","why":"not doable yet","blocked_by":"`+unusable.marker(tracker)+`"}`)
			pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			// The run finishes. A marker the harness cannot use must not cost a run
			// whose change has already been promoted.
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
				t.Fatalf("an unusable marker cost the run its integration: %#v", outcome)
			}
			if len(tracker.blockers) > 0 {
				t.Fatalf("the item was made to wait on a marker nothing could use: %v", tracker.blockers)
			}
			if !tracker.item.Parking.Parked() {
				t.Fatalf("a marker that named nothing usable left the item unparked; calls = %v", tracker.calls)
			}
			if outcome.LandingBlockedBy != "" {
				t.Errorf("outcome landing marker = %q, want none", outcome.LandingBlockedBy)
			}
			// The request is recorded rather than dropped: an item parked with no
			// trace of it reads afterwards as a developer that asked for nothing.
			if !strings.Contains(outcome.LandingImpedimentProblem, unusable.saysWhy) {
				t.Errorf("the outcome does not say why the marker was not used: %q", outcome.LandingImpedimentProblem)
			}
			if !strings.Contains(tracker.item.Parking.Reason(), unusable.saysWhy) {
				t.Errorf("the parking reason does not say why the item is parked rather than waiting: %q", tracker.item.Parking)
			}
			if !strings.Contains(tracker.notes, unusable.saysWhy) {
				t.Errorf("the item's notes do not say why the marker was not used: %q", tracker.notes)
			}
		})
	}
}

// The refusal the resolution above cannot see. The marker names work the tracker
// has, that is open, and that does not already wait on this item, and the tracker
// refuses the dependency anyway — a cycle further round the graph than the one
// read that resolution makes. The item is parked, and the whole record has to say
// so: the disposition every surface prints is derived from the run's own landing
// fields, and the outcome notes an operator reads are written from them before the
// item's status is settled at all.
func TestADependencyRefusedAtTheSettlementSaysParkedOnEverySurface(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker().holds("yoyodyne-impediment")
	tracker.blockerErr = errors.New("bd: adding this dependency would create a cycle")
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the diagnosis\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the conversion needs yoyodyne-impediment to land first","blocked_by":"yoyodyne-impediment"}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	// The run finishes. A dependency the tracker will not write must not cost a run
	// whose change has already been promoted.
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a refused dependency cost the run its integration: %#v", outcome)
	}
	// Where the item actually went.
	if !tracker.item.Parking.Parked() {
		t.Fatalf("the item went back unparked behind a dependency the tracker refused; calls = %v", tracker.calls)
	}
	// And what every surface says about it. `yoyo status` and the conversation both
	// derive the disposition from these two fields, so a run that kept the marker
	// tells them the item stays open waiting on work it is not waiting on.
	if outcome.LandingBlockedBy != "" {
		t.Errorf("outcome landing marker = %q, want none: the tracker refused the dependency", outcome.LandingBlockedBy)
	}
	if !strings.Contains(outcome.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the outcome does not say why the item was parked instead: %q", outcome.LandingImpedimentProblem)
	}
	if got := outcome.UndischargedDisposition(); !strings.Contains(got, "parked") {
		t.Errorf("UndischargedDisposition() = %q, want the parking the item actually got", got)
	}
	// The notes are recorded before the item's status is settled, so this is what
	// asserts the arrangement happens ahead of them rather than after.
	if strings.Contains(tracker.notes, "stays open waiting on") {
		t.Errorf("the recorded outcome names a disposition the item did not get: %q", tracker.notes)
	}
	if !strings.Contains(tracker.notes, "the item is parked") {
		t.Errorf("the recorded outcome does not say the item was parked: %q", tracker.notes)
	}
	// And the durable record, because the run that made the claim is not always the
	// process that describes it afterwards.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.LandingBlockedBy != "" {
		t.Errorf("the run's record still carries the marker the tracker refused: %q", recorded.LandingBlockedBy)
	}
	if !strings.Contains(recorded.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the run's record does not say why the item was parked: %q", recorded.LandingImpedimentProblem)
	}
}

// The whole point of the marker, replayed through the derivation selection
// actually reads: an item a run did not discharge is offered by no pull until
// somebody releases it, or until the impediment it names closes. Before this it
// went back as ordinary open work, and the next pull bought another run and
// another diagnosis of the same impediment.
func TestSelectionNeverPicksAnUndischargedItemUntilItIsReleased(t *testing.T) {
	t.Parallel()

	// pulls is how many readings of the queue the item has to survive. One would
	// prove it; a handful is what a watch session actually makes while nothing
	// about the item changes.
	const pulls = 3

	t.Run("the parking default", func(t *testing.T) {
		t.Parallel()

		tracker := newOutcomeTracker()
		provider := roleBackend(writeFeature, approveVerdict)
		provider.developerFinalText = "the diagnosis\n\n" +
			landingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`)
		pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
		if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// The tracker reports the item as ready, because it is open and nothing
		// blocks it — which is exactly why the parking has to be the thing that
		// holds it back. Selection is asked about the axis the tracker does not know.
		settled := tracker.item
		for pull := 1; pull <= pulls; pull++ {
			queue := backlog.Order([]beads.WorkItem{settled}, []string{settled.ID}, backlog.ReadHolds(nil))
			if next, ok := queue.Next(); ok {
				t.Fatalf("pull %d selected the item its own landing parked: %s", pull, next.ID)
			}
			if queue.Parked() != 1 {
				t.Fatalf("pull %d does not report the item as parked: %+v", pull, queue.Entries)
			}
		}
		// And it is a parking rather than a disappearance: releasing it is one act,
		// and the item is pulled the next time the queue is read.
		released := settled
		released.Parking = ""
		queue := backlog.Order([]beads.WorkItem{released}, []string{released.ID}, backlog.ReadHolds(nil))
		if next, ok := queue.Next(); !ok || next.ID != released.ID {
			t.Fatalf("the released item was not pulled: %+v", queue.Entries)
		}
	})

	t.Run("the impediment a landing named", func(t *testing.T) {
		t.Parallel()

		tracker := newOutcomeTracker().holds("yoyodyne-impediment")
		provider := roleBackend(writeFeature, approveVerdict)
		provider.developerFinalText = "the diagnosis\n\n" +
			landingBlock(`{"outcome":"evidence","why":"it needs yoyodyne-impediment first","blocked_by":"yoyodyne-impediment"}`)
		pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
		if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// This branch is asked of the harness's own dependency gate rather than of
		// the queue. An open item's readiness is the tracker's answer and the queue
		// only relays it, so a test that assembled that answer itself would be
		// asserting its own bookkeeping; the gate every run passes through on the way
		// in is this repository's code and reads the same relation.
		calls := len(tracker.calls)
		for pull := 1; pull <= pulls; pull++ {
			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			if err != nil {
				t.Fatalf("pull %d error = %v", pull, err)
			}
			if !outcome.Paused || outcome.PausedByDependency == nil {
				t.Fatalf("pull %d started work on an item waiting for its impediment: %#v", pull, outcome)
			}
			if !slices.Contains(outcome.PausedByDependency.Blockers, "yoyodyne-impediment") {
				t.Errorf("pull %d does not name what the item waits for: %v", pull, outcome.PausedByDependency.Blockers)
			}
		}
		// Nothing was claimed and no developer ran, which is what picking it zero
		// times has to mean: a refusal that still spent a run is the cost this exists
		// to avoid.
		if extra := tracker.calls[calls:]; len(extra) > 0 {
			t.Errorf("the refused pulls still wrote to the tracker: %v", extra)
		}
		// The release nobody has to remember to make: the impediment closes and the
		// same gate lets the work through, which is what the marker buys over the
		// parking.
		for index := range tracker.item.Dependencies {
			tracker.item.Dependencies[index].Status = "closed"
		}
		// A fresh pipeline over the same tracker, because a run identifier is spent
		// once and this fixture's is fixed. What is being asked is the gate, and the
		// gate decides from the item rather than from the run store.
		released, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
		outcome, err := released.Run(context.Background(), tracker.item.ID)
		if err != nil {
			t.Fatalf("Run() after the impediment closed error = %v", err)
		}
		if outcome.Paused {
			t.Fatalf("the item was still held back after its impediment closed: %#v", outcome)
		}
		if !tracker.claimed {
			t.Error("the released item was never claimed, so nothing actually picked it up")
		}
	})
}

// The settlement is re-runnable, which is not a nicety: the reopen is retried
// where the tracker was busy, and a sweep re-runs the whole settlement of a run
// whose reopen never landed. A second run of it must not add the dependency
// again, because the tracker refuses a duplicate and the settlement it would fail
// is that of a run whose change is already integrated.
func TestSettlingAnUndischargedItemTwiceMakesItWaitOnce(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
			t.Fatalf("settleUndischarged() attempt %d error = %v", attempt, err)
		}
	}
	if len(tracker.blockers) != 1 {
		t.Fatalf("the item was made to wait %d times: %v", len(tracker.blockers), tracker.blockers)
	}
	if tracker.item.Parking.Parked() {
		t.Errorf("an item waiting on its impediment was parked as well: %q", tracker.item.Parking)
	}
}

// Every refusal the harness can see is decided when the claim is read, and one it
// cannot is a cycle further round the graph than the one read it makes. So a
// dependency the tracker refuses anyway takes the parking rather than failing the
// settlement: the change is already promoted, the run cannot be retried into a
// better state, and failing here leaves the item claimed with nothing watching
// it — which is the outcome this settlement exists to prevent.
func TestADependencyTheTrackerRefusesParksTheItemRatherThanFailingTheSettlement(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	tracker.blockerErr = errors.New("bd: adding this dependency would create a cycle")
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	settled, err := settleUndischarged(context.Background(), tracker, state)
	if err != nil {
		t.Fatalf("settleUndischarged() error = %v, want the parking fallback rather than a failure", err)
	}
	if tracker.item.Status != "open" {
		t.Fatalf("the item was left claimed by a run that has ended: status = %q", tracker.item.Status)
	}
	if !tracker.item.Parking.Parked() {
		t.Fatalf("the item went back unparked behind a dependency that was refused; calls = %v", tracker.calls)
	}
	// The settlement answers with the run's landing fields as they now stand. A
	// caller that recorded the state it went in with would say the item stays open
	// waiting on work the item was in fact parked for.
	if settled.LandingBlockedBy != "" {
		t.Errorf("the settled state still carries the marker the tracker refused: %q", settled.LandingBlockedBy)
	}
	if !strings.Contains(settled.LandingImpedimentProblem, "would not make this item wait") {
		t.Errorf("the settled state does not say why the item was parked instead: %q", settled.LandingImpedimentProblem)
	}
	// What the tracker would not do is on the item, because that is where somebody
	// reads it.
	if !strings.Contains(tracker.item.Parking.Reason(), "would not make this item wait") {
		t.Errorf("the parking reason does not say the dependency was refused: %q", tracker.item.Parking)
	}
	if !strings.Contains(tracker.notes, "would not make this item wait") {
		t.Errorf("the item's notes do not say the dependency was refused: %q", tracker.notes)
	}
}

// Deciding where an undischarged item goes reads the item, and a tracker that
// could not be read is not the run's failure: a run whose change is promoted
// leaves the settlement to ask again under the recovery it always had. So the
// read is reported rather than swallowed — a decision made from an item nobody
// saw would write a dependency the item may already carry and lift a parking
// somebody placed.
func TestDecidingWhereAnUndischargedItemGoesReportsATrackerItCouldNotRead(t *testing.T) {
	t.Parallel()

	// A tracker that holds some other work, so the item this run served is one it
	// cannot answer for.
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-other", Title: "Another", Status: "open"}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       "yoyodyne-task",
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	if _, _, err := arrangeUndischarged(context.Background(), tracker, state); err == nil {
		t.Fatal("arrangeUndischarged() returned no error for an item it could not read")
	}
	if len(tracker.blockers) > 0 {
		t.Errorf("a dependency was written for an item that was never read: %v", tracker.blockers)
	}
	// The parking needs nothing of the read but the reason it supersedes, so it
	// settles anyway: a sentence lost there must not cost the settlement of a
	// change that is already promoted.
	parked := state
	parked.LandingBlockedBy = ""
	if _, err := settleUndischarged(context.Background(), tracker, parked); err != nil {
		t.Fatalf("settleUndischarged() error = %v, want the parking to settle without the read", err)
	}
}

// A settlement adds a dependency; it never lifts a parking. An item an operator
// parked and then named for a run anyway comes back still parked, because the
// operator's decision is not this run's to retire on the way past — and once the
// impediment closes, the parking is still what keeps the item out of the queue.
func TestSettlingAnItemLeftWaitingKeepsTheParkingItAlreadyCarried(t *testing.T) {
	t.Parallel()

	operatorParked := domain.WorkItemParking("deferred by the scope decision until the quarter turns")
	tracker := &fakeTracker{item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "in_progress", Parking: operatorParked,
	}}
	state := runstate.State{
		RunID:            "run-abcdef0123456789abcdef0123456789",
		WorkItemID:       tracker.item.ID,
		TargetBranch:     "main",
		LandingOutcome:   runstate.LandingEvidence,
		LandingReason:    "it needs yoyodyne-impediment first",
		LandingBlockedBy: "yoyodyne-impediment",
	}
	if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if tracker.item.Parking != operatorParked {
		t.Errorf("the settlement rewrote a parking somebody else placed: %q", tracker.item.Parking)
	}
	if len(tracker.blockers) != 1 {
		t.Errorf("the item was not made to wait on its impediment: %v", tracker.blockers)
	}
}

// The parking default does replace the reason — both are parkings, and the run's
// names what would release the item now — so the one it superseded goes into the
// notes. A decision somebody took has to stay readable off the item; the parking
// field holds one value, and without this the only copy of theirs is whatever the
// tracker keeps of its own history.
func TestTheParkingDefaultRecordsTheReasonItSuperseded(t *testing.T) {
	t.Parallel()

	operatorParked := domain.WorkItemParking("deferred by the scope decision until the quarter turns")
	tracker := &fakeTracker{item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "in_progress", Parking: operatorParked,
	}}
	state := runstate.State{
		RunID:          "run-abcdef0123456789abcdef0123456789",
		WorkItemID:     tracker.item.ID,
		TargetBranch:   "main",
		LandingOutcome: runstate.LandingEvidence,
		LandingReason:  "the design this needs has not landed",
	}
	if _, err := settleUndischarged(context.Background(), tracker, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if !strings.Contains(tracker.item.Parking.Reason(), "the design this needs has not landed") {
		t.Errorf("the run's account did not become the parking reason: %q", tracker.item.Parking)
	}
	if !strings.Contains(tracker.notes, operatorParked.Reason()) {
		t.Errorf("the superseded parking reason was lost: %q", tracker.notes)
	}
	// An item nobody had parked has nothing to supersede, and a note saying so
	// would be a sentence about a decision nobody took.
	unparked := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "in_progress"}}
	if _, err := settleUndischarged(context.Background(), unparked, state); err != nil {
		t.Fatalf("settleUndischarged() error = %v", err)
	}
	if strings.Contains(unparked.notes, "replaces the parking reason") {
		t.Errorf("the notes claim a parking reason that never existed: %q", unparked.notes)
	}
}

// A developer's account may run to the whole of what a claim can carry, and the
// parking reason has to stay one value the tracker will hold on one line. A
// reason the tracker refuses fails the settlement of a run whose change is
// already integrated, and the account is on the item as notes in full either way,
// so cutting it here costs a reader nothing.
func TestTheParkingReasonStaysInsideWhatTheTrackerHolds(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID:          "run-abcdef0123456789abcdef0123456789",
		WorkItemID:     "yoyodyne-task",
		LandingOutcome: runstate.LandingEvidence,
		LandingReason:  strings.Repeat("the design this needs has not landed\n", 200),
	}
	parking := undischargedParking(state)
	if !parking.Parked() {
		t.Fatal("a landing that parks produced no parking reason")
	}
	// The tracker's own two rules, asserted here because this is what writes one.
	if len(parking.Reason()) > domain.MaxWorkItemParkingBytes {
		t.Errorf("parking reason is %d bytes, past the %d the tracker holds", len(parking.Reason()), domain.MaxWorkItemParkingBytes)
	}
	if strings.ContainsAny(parking.Reason(), "\r\n") {
		t.Errorf("parking reason spans lines: %q", parking.Reason())
	}
	if !strings.Contains(parking.Reason(), state.RunID) {
		t.Errorf("parking reason does not name the run that parked the item: %q", parking.Reason())
	}
	// A landing that named its impediment is held back by that instead, so parking
	// it as well would take an item out of reach for a wait that releases itself.
	waiting := state
	waiting.LandingBlockedBy = "yoyodyne-impediment"
	if got := undischargedParking(waiting); got.Parked() {
		t.Errorf("a landing that named its impediment was parked as well: %q", got)
	}
}

// The other kind, and the one nearly every run is. A developer that says nothing
// about its landing has discharged the item, and so has one that says so.
func TestADischargingLandingClosesItsItem(t *testing.T) {
	t.Parallel()

	for _, claimed := range []struct {
		name  string
		reply string
	}{
		{name: "a reply that claims nothing", reply: "implemented the work item"},
		{
			name: "a reply that claims the discharge",
			reply: "implemented the work item\n\n" +
				landingBlock(`{"outcome":"discharged","why":"the acceptance criteria are met and the suite covers them"}`),
		},
	} {
		t.Run(claimed.name, func(t *testing.T) {
			t.Parallel()

			tracker := newOutcomeTracker()
			provider := roleBackend(writeFeature, approveVerdict)
			provider.developerFinalText = claimed.reply
			pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !outcome.WorkItemClosed || !tracker.closed || tracker.item.Status != "closed" {
				t.Fatalf("a discharging landing did not close its item: outcome = %#v, calls = %v", outcome, tracker.calls)
			}
			if tracker.reopened {
				t.Errorf("a discharging landing put its item back in the backlog; calls = %v", tracker.calls)
			}
		})
	}
}

// A claim that arrived and could not be read is not a claim that was never made.
// The developer wrote a block, which means it was trying to say something about
// whether the item closes, and the safe reading of an unreadable one is the
// recoverable direction: an item left open is something a person can settle, and
// a false closure is the thing nobody sees.
func TestAnUnreadableLandingClaimWithholdsTheClosure(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "worked on it\n\n" + landingBlock(`{"outcome":"partly","why":"some of it"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("an unreadable claim cost the run its integration: %#v", outcome)
	}
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("the item closed on a claim nobody could read; calls = %v", tracker.calls)
	}
	if outcome.LandingProblem == "" {
		t.Fatal("an unreadable claim left no trace on the outcome")
	}
	if !tracker.reopened || !strings.Contains(tracker.reopenReason, "could not be read") {
		t.Errorf("the item does not say why it was not closed: %q", tracker.reopenReason)
	}
	// The prose the developer wrote is the run's evidence, and a refused claim
	// must not take it.
	if !strings.Contains(outcome.Summary, "worked on it") {
		t.Errorf("a refused claim cost the run its summary: %q", outcome.Summary)
	}
}

// A repair round that finishes the work must not be closed against the previous
// attempt's evidence claim. The claim describes the change as it now stands.
func TestALaterAttemptReplacesTheLandingTheEarlierOneClaimed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, repairVerdict, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"not doable yet\n\n" + landingBlock(`{"outcome":"evidence","why":"the dependency has not landed"}`),
		"the reviewer was right, and it was doable after all",
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Landing != "" || outcome.LandingReason != "" {
		t.Errorf("the second attempt inherited the first one's claim: %q / %q", outcome.Landing, outcome.LandingReason)
	}
	if !outcome.WorkItemClosed || tracker.reopened {
		t.Fatalf("the finished work was not closed: outcome = %#v, calls = %v", outcome, tracker.calls)
	}
}

// The reviewer is the only reader that sees the claim beside the change, which
// is what lets a diagnosis be judged as a diagnosis rather than as a missing
// implementation. It reaches the reviewer as untrusted evidence, because the
// developer wrote it.
func TestTheReviewerIsShownWhichLandingWasClaimed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the diagnosis\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`)
	var reviewerPrompt string
	underlying := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompt = request.Prompt
		}
		return underlying(request)
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(reviewerPrompt, "Claimed landing outcome") {
		t.Fatal("the reviewer was not shown the claim at all")
	}
	if !strings.Contains(reviewerPrompt, "does not discharge the work item") {
		t.Errorf("the reviewer was not told which landing was claimed: %q", reviewerPrompt)
	}
	if !strings.Contains(reviewerPrompt, "the design this needs has not landed") {
		t.Errorf("the reviewer was not told why: %q", reviewerPrompt)
	}
	// The claim is the developer's, so it belongs under the untrusted heading
	// rather than beside the invariants the harness supplied.
	claimAt := strings.Index(reviewerPrompt, "Claimed landing outcome")
	untrustedAt := strings.Index(reviewerPrompt, "# Untrusted review evidence")
	if untrustedAt < 0 || claimAt < untrustedAt {
		t.Error("the claim was presented outside the untrusted evidence the developer produced")
	}
}

// yoyodyne-ifd.284 replayed. A developer files a diagnosis saying the machinery
// its item needs has not landed, and writes no landing block — so the ordinary
// discharging landing is in force and the item closes on a document saying in its
// own words that the work was not done. That is what happened, after the claim
// channel shipped, because the claim nobody writes was shown to nobody: the
// reviewer approved a diagnosis without ever being told that approving it closed
// the item.
//
// The default is a claim like any other now, and the reviewer is shown it. A
// diagnosis offered under it is a change that is not the work, the review sends
// it back, and the repair round is where the developer claims what it actually
// landed — which leaves the item parked rather than closed.
func TestADiagnosisWithNoClaimIsSentBackRatherThanClosingItsItem(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, repairVerdict, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"the recurring-task machinery this item needs is on a branch that has not landed; this change is the diagnosis",
		"the diagnosis stands\n\n" +
			landingBlock(`{"outcome":"evidence","why":"the machinery this item is written against has not landed"}`),
	}
	var reviewerPrompts []string
	underlying := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompts = append(reviewerPrompts, request.Prompt)
		}
		return underlying(request)
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The first review is the one that mattered: the change in front of it was a
	// diagnosis, and it has to be told that approving it closes the item.
	if len(reviewerPrompts) == 0 {
		t.Fatal("the change was never reviewed")
	}
	if !strings.Contains(reviewerPrompts[0], "claimed no landing outcome") {
		t.Errorf("the reviewer was not told the default claim was in force: %q", reviewerPrompts[0])
	}
	if !strings.Contains(reviewerPrompts[0], "approve it only if it is the work the item asked for") {
		t.Errorf("the reviewer was not told what approving the default does: %q", reviewerPrompts[0])
	}
	// And the item is where the evidence says it belongs, not closed against it.
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("the item closed against a diagnosis; calls = %v", tracker.calls)
	}
	if !tracker.reopened || !tracker.item.Parking.Parked() {
		t.Fatalf("the item was not put back parked; calls = %v", tracker.calls)
	}
	if !strings.Contains(tracker.item.Parking.Reason(), "has not landed") {
		t.Errorf("the parking reason does not name what would release the item: %q", tracker.item.Parking)
	}
}

// A sweep settling a run somebody killed decides the same way the run would
// have. It has to: a run whose merge the forge only queued is settled by a later
// process entirely, and a sweep reading the promotion alone is exactly the
// closure this record exists to stop.
func TestReconciliationSettlesAnInterruptedRunByTheLandingItClaimed(t *testing.T) {
	t.Parallel()

	for _, settled := range []struct {
		name       string
		reply      string
		wantClosed bool
		wantParked bool
		wantWaits  string
	}{
		{
			name:       "a landing that discharges the item",
			reply:      "implemented the work item",
			wantClosed: true,
		},
		{
			name: "a landing that does not",
			reply: "the diagnosis\n\n" +
				landingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`),
			wantParked: true,
		},
		{
			// The sweep decides where the item goes as well as whether it closes: a
			// sweep that read only the promotion would park an item whose own landing
			// asked for the dependency, and one that read only the outcome would put
			// it back bare.
			name: "a landing that named the impediment it waits on",
			reply: "the diagnosis\n\n" +
				landingBlock(`{"outcome":"evidence","why":"it needs yoyodyne-impediment first","blocked_by":"yoyodyne-impediment"}`),
			wantWaits: "yoyodyne-impediment",
		},
	} {
		t.Run(settled.name, func(t *testing.T) {
			t.Parallel()

			repository, worktreeRoot, store := restartableFixture(t)
			tracker := (&fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}).
				holds("yoyodyne-impediment")
			provider := roleBackend(writeFeature, approveVerdict)
			provider.developerFinalText = settled.reply
			// Killed after the promotion and before the item was settled, which is
			// the boundary reconciliation exists for.
			halting := &haltingStore{StateStore: store, at: runstate.PhaseCompleting}
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
			if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
				t.Fatal("interrupted Run() error = nil")
			}
			if tracker.closed || tracker.reopened {
				t.Fatalf("the interrupted run settled the item itself; calls = %v", tracker.calls)
			}

			results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 {
				t.Fatalf("reconciled %d runs, want the one this test made", len(results))
			}
			if tracker.closed != settled.wantClosed {
				t.Errorf("closed = %t, want %t; calls = %v", tracker.closed, settled.wantClosed, tracker.calls)
			}
			if tracker.reopened == settled.wantClosed {
				t.Errorf("reopened = %t, want %t; calls = %v", tracker.reopened, !settled.wantClosed, tracker.calls)
			}
			if tracker.item.Parking.Parked() != settled.wantParked {
				t.Errorf("parked = %q, want parked = %t", tracker.item.Parking, settled.wantParked)
			}
			if waits := strings.Join(tracker.blockers, ","); waits != settled.wantWaits {
				t.Errorf("the item waits on %q, want %q", waits, settled.wantWaits)
			}
			// Whichever way it went, settling it twice settles it once: the second
			// sweep finds the item already in the state this run's landing calls for.
			calls := len(tracker.calls)
			reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if extra := tracker.calls[calls:]; countCalls(extra, "complete")+countCalls(extra, "reopen")+countCalls(extra, "blocker") > 0 {
				t.Errorf("a second sweep settled the item again: %v", extra)
			}
		})
	}
}

// The derivation the closure reads is one derivation, so a run cannot be closed
// by the process that made it and left open by the sweep that settles it.
func TestTheClosureDerivationReadsTheSameRecordEverywhere(t *testing.T) {
	t.Parallel()

	for _, recorded := range []struct {
		name       string
		state      runstate.State
		discharges bool
		parks      bool
	}{
		{name: "a run that claimed nothing", state: runstate.State{}, discharges: true},
		{
			name:       "a run recorded before the channel existed",
			state:      runstate.State{Status: runstate.StatusSucceeded},
			discharges: true,
		},
		{
			name:       "a claimed discharge",
			state:      runstate.State{LandingOutcome: runstate.LandingDischarged, LandingReason: "done"},
			discharges: true,
		},
		{
			name:       "a claimed evidence landing",
			state:      runstate.State{LandingOutcome: runstate.LandingEvidence, LandingReason: "not yet"},
			discharges: false,
			parks:      true,
		},
		{
			name: "a claimed evidence landing that named its impediment",
			state: runstate.State{
				WorkItemID:       "yoyodyne-task",
				LandingOutcome:   runstate.LandingEvidence,
				LandingReason:    "not yet",
				LandingBlockedBy: "yoyodyne-impediment",
			},
			discharges: false,
		},
		{
			// The tracker refuses a self-dependency as a cycle, so a marker naming
			// the item it was claimed on holds nothing back. It takes the parking,
			// which is the disposition that does.
			name: "a claimed evidence landing that named its own item",
			state: runstate.State{
				WorkItemID:       "yoyodyne-task",
				LandingOutcome:   runstate.LandingEvidence,
				LandingReason:    "not yet",
				LandingBlockedBy: "yoyodyne-task",
			},
			discharges: false,
			parks:      true,
		},
		{
			name:       "a claim that could not be read",
			state:      runstate.State{LandingProblem: "decode landing claim: unexpected trailing content"},
			discharges: false,
			parks:      true,
		},
	} {
		t.Run(recorded.name, func(t *testing.T) {
			t.Parallel()

			if got := recorded.state.LandingDischarges(); got != recorded.discharges {
				t.Fatalf("LandingDischarges() = %t, want %t", got, recorded.discharges)
			}
			// Where the item is left is the other half of the same derivation, and it
			// is read by both settlement sites for the same reason: an item parked by
			// the run and reopened bare by the sweep is an item selection picks up
			// again the moment the sweep runs.
			if got := recorded.state.LandingParks(); got != recorded.parks {
				t.Fatalf("LandingParks() = %t, want %t", got, recorded.parks)
			}
			// An item already in the state the landing calls for is settled, which
			// is what makes settling one twice settle it once.
			for status, want := range map[string]bool{"closed": true, "open": !recorded.discharges, "in_progress": false} {
				if got := landingSettled(recorded.state, status); got != want {
					t.Errorf("landingSettled(%q) = %t, want %t", status, got, want)
				}
			}
		})
	}
}

// A claim with no account of itself is refused by the durable schema, because
// the account is the half of the record anybody reads afterwards: an item left
// open for a reason nobody wrote down is the false closure's quieter cousin.
func TestTheDurableSchemaRefusesALandingClaimWithNoReason(t *testing.T) {
	t.Parallel()

	now := execution.RealClock{}.Now()
	state := runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          "run-abcdef0123456789abcdef0123456789",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		WorkItemID:     "yoyodyne-task",
		Backend:        "claude-code",
		Status:         runstate.StatusRunning,
		StartedAt:      now,
		UpdatedAt:      now,
		LandingOutcome: runstate.LandingEvidence,
	}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "landing_reason") {
		t.Fatalf("Validate() error = %v, want a refusal naming the missing reason", err)
	}
	state.LandingReason = "the design this needs has not landed"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a complete claim: %v", err)
	}
	// The marker only means anything on a landing that leaves the item open, and
	// the settlement reads it: a record carrying one against a closure would have
	// the item closed and waiting at once.
	state.LandingBlockedBy = "yoyodyne-impediment"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a marker on the landing it belongs to: %v", err)
	}
	state.LandingOutcome = runstate.LandingDischarged
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "landing_blocked_by") {
		t.Fatalf("Validate() error = %v, want a refusal naming the marker on a discharge", err)
	}
	// The marker the schema stores is one the harness resolved, so the two ways it
	// could be unresolvable are refused rather than stored and acted on: nothing
	// waits on itself, and a marker and the reason a marker could not be used are
	// two different settlements to record at once.
	state.LandingOutcome = runstate.LandingEvidence
	state.LandingBlockedBy = state.WorkItemID
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "cannot name the work item it was claimed on") {
		t.Fatalf("Validate() error = %v, want a refusal of a marker naming its own item", err)
	}
	state.LandingBlockedBy = "yoyodyne-impediment"
	state.LandingImpedimentProblem = "the tracker did not confirm that item"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "cannot both be recorded") {
		t.Fatalf("Validate() error = %v, want a refusal of a marker recorded beside its own problem", err)
	}
	state.LandingBlockedBy = ""
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a landing parked because its marker was unusable: %v", err)
	}
}
