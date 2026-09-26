package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A decision is either attempted or written down as not attempted, and never
// neither. yoyodyne-ifd.428.39 is the week this was not so: the re-runs decided
// for yoyodyne-ifd.192 and .187 on 2026-09-19 fired nothing and recorded nothing,
// because nothing ever handed them to an action for a gate to refuse.

// laterCaps are the caps after the development manager crossed the re-run cap,
// which is what let her record a second re-run of the same item.
var laterCaps = runstate.TriageCaps{ReviewRounds: 7, RepairGrants: 1, Reruns: 2, MergeRearms: 2}

// The 192 and 187 case: a re-run recorded again about a stoppage whose one
// re-run was already claimed. The sweep used to read the old claim as the new
// decision carried out and pass it over on every pull. It is offered now, the
// action refuses it, and the refusal is on the item saying what clears it.
func TestARerunDecidedAgainAfterItsStoppageWasRerunIsAttemptedAndRefusedOnTheRecord(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() of the first decision error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the first decision fired", harness.started)
	}

	// A week on, the development manager crosses the cap and records a re-run
	// again — against the same stoppage, whose one re-run is spent.
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, docketedRunID), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	later := harness.carryOutAt(2 * time.Hour)
	task := theOneOutstanding(t, later)
	if task.RunID != docketedRunID || task.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("task = %#v, want the re-run recorded again offered to the pass", task)
	}
	carried, _, err := later.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported as a gate", err)
	}
	if carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want nothing fired a second time for one stoppage", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateBudget {
		t.Fatalf("gate = %q, want the stoppage's spent re-run named", carried.Gate)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(docketedRunID)
	if !found || recorded.Unattempted || recorded.Attempts != 1 {
		t.Fatalf("finding = %#v (found %t), want the refusal of an attempt written onto the item", recorded, found)
	}
	if !strings.Contains(recorded.Clears, "latest run") {
		t.Fatalf("clears = %q, want it to say the decision belongs on the latest stoppage", recorded.Clears)
	}
	if refused, unattempted := counters.CarryOutFindings(); refused != 1 || unattempted != 0 {
		t.Fatalf("findings = %d refused, %d unattempted, want the one refusal counted", refused, unattempted)
	}
}

// A decision re-recorded about a spent stoppage is offered only while it is the
// item's latest. One she has since decided past is not offered again, so a
// stoppage she re-decided and then moved on from is not refused forever.
func TestARerunDecidedPastIsNotOfferedAgain(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, docketedRunID), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	// And then a decision about a later stoppage of the same item.
	laterRun := "run-22222222222222222222222222222222"
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionWait, laterRun), docketedNow.Add(2*time.Hour)); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	outstanding, err := harness.carryOutAt(3 * time.Hour).Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	for _, task := range outstanding {
		if task.RunID == docketedRunID {
			t.Fatalf("outstanding = %#v, want the decision she moved past left alone", outstanding)
		}
	}
}

// A decision the pass does not attempt is written onto the item once it has
// stood a poll interval, with why, and not again while nothing about it changes.
func TestADecisionNoPassAttemptedIsWrittenOntoTheItemWithWhy(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// Something else is running the item, so the sweep holds the decision back.
	going := stoppedState()
	going.RunID = "run-11111111111111111111111111111111"
	going.Status = runstate.StatusRunning
	going.Phase = runstate.PhaseDeveloping
	going.CompletedAt = nil
	going.Blocker = ""
	going.CheckFailure = nil
	going.ReviewFindingDetails = nil
	going.ReviewFindings = 0
	going.ReviewSummary = ""
	if err := harness.runs.Create(going); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if outstanding, err := harness.carryOut().Outstanding(); err != nil || len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, %v, want nothing offered while the item has a run going", outstanding, err)
	}

	// Within the poll interval nothing is said yet: the next pull may reach it.
	written, err := harness.carryOutAt(30*time.Second).RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil || len(written) != 0 {
		t.Fatalf("written = %#v, %v, want nothing written inside the poll interval", written, err)
	}

	written, err = harness.carryOutAt(2*time.Minute).RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	if len(written) != 1 || !strings.Contains(written[0].Problem, going.RunID) {
		t.Fatalf("written = %#v, want the decision said to be unattempted, naming the run in the way", written)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(docketedRunID)
	if !found || !recorded.Unattempted || recorded.Attempts != 0 || !strings.Contains(recorded.Refusal, going.RunID) {
		t.Fatalf("finding = %#v (found %t), want an unattempted record naming why", recorded, found)
	}
	if recorded.Cooling(docketedNow.Add(2 * time.Minute)) {
		t.Fatalf("finding = %#v, want a decision nobody attempted never paced", recorded)
	}
	if refused, unattempted := counters.CarryOutFindings(); refused != 0 || unattempted != 1 {
		t.Fatalf("findings = %d refused, %d unattempted, want the one unattempted decision counted", refused, unattempted)
	}

	// Said once: a pull that finds the same thing writes nothing more.
	written, err = harness.carryOutAt(3*time.Minute).RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil || len(written) != 0 {
		t.Fatalf("written = %#v, %v, want the standing record left as it is", written, err)
	}

	// The docket entry the development manager reads carries it, and puts it in
	// front of her walk rather than behind it.
	entries := harness.docket.entries
	if len(entries) == 0 {
		t.Fatalf("docket = %#v, want the stoppage docketed", entries)
	}
	entry := entries[0]
	entry.CarryOut = docketedCarryOut(entry, counters)
	if entry.CarryOut == nil || !entry.CarryOut.Unattempted {
		t.Fatalf("carry-out = %#v, want the docket entry to carry the unattempted decision", entry.CarryOut)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "No pass has attempted") {
		t.Fatalf("rendered = %q, want the entry to say no pass attempted the decision", rendered)
	}
}

// An offered decision the pull passed over is written down too, with the
// pull's own account of why; one it attempted is not.
func TestAnOfferedDecisionThePullPassedOverIsWrittenDownWithThePullsReason(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOutAt(2 * time.Minute)
	task := theOneOutstanding(t, carrying)

	written, err := carrying.RecordUnattempted(context.Background(), time.Minute, nil)
	if err != nil || len(written) != 0 {
		t.Fatalf("written = %#v, %v, want an offered decision the pull did not name treated as attempted", written, err)
	}
	why := "every developer slot this pull had was spent on 2 decision(s) ahead of it on the docket"
	written, err = carrying.RecordUnattempted(context.Background(), time.Minute, map[string]string{task.RunID: why})
	if err != nil {
		t.Fatalf("RecordUnattempted() error = %v", err)
	}
	if len(written) != 1 || written[0].Gate != runstate.TriageGateCapacity {
		t.Fatalf("written = %#v, want the passed-over decision written down against capacity", written)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if recorded, found := counters.CarryOutOf(task.RunID); !found || !recorded.Unattempted || recorded.Refusal != why {
		t.Fatalf("finding = %#v (found %t), want the pull's reason on the item", recorded, found)
	}
	// And it is still offered: an unattempted record paces nothing.
	if again := theOneOutstanding(t, carrying); again.RunID != task.RunID {
		t.Fatalf("outstanding = %#v, want the decision still offered", again)
	}
}

// A decision about a run no stopped-run entry stands for is still reached: the
// sweep reads each docketed item's latest decision whatever run it names.
func TestAnItemsLatestDecisionIsReachedWhereNoEntryLeadsToIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	first := harness.carryOut()
	if _, _, err := first.Carry(context.Background(), theOneOutstanding(t, first)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	// The fresh run was cancelled on its way out and never docketed, and the
	// development manager decided a re-run of it.
	undocketed := "run-33333333333333333333333333333333"
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRerun, undocketed), docketedNow.Add(time.Hour), laterCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	task := theOneOutstanding(t, harness.carryOutAt(2*time.Hour))
	if task.RunID != undocketed || task.DocketKey != triage.Key(triage.ClassStoppedRun, undocketed) {
		t.Fatalf("task = %#v, want the decision about the undocketed run offered to the pass", task)
	}
}
