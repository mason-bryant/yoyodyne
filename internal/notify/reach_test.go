package notify

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

var reachMoment = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func noted(kind Kind) Event {
	return Event{Kind: kind, At: reachMoment, Severity: report.SeverityNote}
}

// Every kind says how far it goes, and a kind added without an answer is a kind
// nobody decided about. It matters which way that fails: an unclassified kind
// reaches nothing rather than the channel, so the way to be wrong is to be quiet
// and be corrected rather than loud and be muted — and this is what makes being
// quiet a decision somebody took instead of one nobody noticed.
func TestEveryKindSaysHowFarItGoes(t *testing.T) {
	t.Parallel()

	for _, kind := range Kinds() {
		reach, found := reaches[kind]
		if !found {
			t.Fatalf("%q has no reach, so it would post nowhere; classify it beside the others", kind)
		}
		if !reach.Valid() {
			t.Fatalf("%q reaches %q, which is not one of the three", kind, reach)
		}
	}
}

// The measured baseline, in both directions. Three quarters of 2,250 posts were
// per-event narration and under forty were the kinds somebody has to act on, so
// this pins each side of that: what was drowning the channel is off it, and every
// kind the operator named as important or actionable is still on it.
func TestNarrationLeavesTheChannelAndWhatNeedsSomebodyStaysOnIt(t *testing.T) {
	t.Parallel()

	off := map[Kind]Reach{
		KindReportFiled:    ReachThread,
		KindWatchIdle:      ReachRecord,
		KindWatchStarted:   ReachRecord,
		KindWatchStopped:   ReachRecord,
		KindChecksPassed:   ReachThread,
		KindReviewApproved: ReachThread,
		KindRunStarted:     ReachThread,
		KindPublished:      ReachThread,
		KindMergeCompleted: ReachThread,
		KindPromoted:       ReachThread,
		KindItemAdmitted:   ReachThread,
	}
	for kind, want := range off {
		if got := kind.Reach(); got != want {
			t.Fatalf("%q reaches %q, want %q: it is per-event narration the survey counted", kind, got, want)
		}
	}
	for _, kind := range []Kind{
		KindIntakeHeld, KindWatchBraked, KindUsageLimitExhausted,
		KindRunParked, KindTrackerBlockRefused,
	} {
		if got := kind.Reach(); got != ReachChannel {
			t.Fatalf("%q reaches %q, want the channel: it is one of the kinds that must not be drowned", kind, got)
		}
	}
}

// Severity is importance rather than actionability, in the operator's own
// correction: if something important is broken he wants to know, action needed or
// not. So a critical event is shown where he reads whatever its kind would have
// said on its own.
func TestSomethingAlreadyBrokenReachesTheChannelWhateverItsKind(t *testing.T) {
	t.Parallel()

	topic, err := WorkItem("yoyodyne-ifd.314")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	filed := noted(KindReportFiled)
	if got := reachOf(topic, filed); got != ReachThread {
		t.Fatalf("a note reaches %q, want the thread", got)
	}
	filed.Severity = report.SeverityWarning
	if got := reachOf(topic, filed); got != ReachThread {
		t.Fatalf("a warning reaches %q, want the thread: a risk that has cost nothing yet is not news at the top", got)
	}
	filed.Severity = report.SeverityCritical
	if got := reachOf(topic, filed); got != ReachChannel {
		t.Fatalf("a critical report reaches %q, want the channel", got)
	}
}

// The operator's motivating case: a report that is neither important nor asking
// for anything, filed against no work item. There is no thread to put it in,
// because what is addressed to the product is posted unthreaded — which is the
// channel level — so it would otherwise be pushed to the top of the channel
// precisely because there was less to say about it. It reaches the record and
// nothing posts.
func TestAReportWithNoItemToThreadItInPostsNothing(t *testing.T) {
	t.Parallel()

	filed := Notification{
		Topic:   Product(),
		Speaker: Persona("developer", ""),
		Event:   noted(KindReportFiled),
	}
	filed.Event.Severity = report.SeverityWarning
	filed.Event.Text = "nobody's next on this"
	if got := filed.Reach(); got != ReachRecord {
		t.Fatalf("reach = %q, want the record: there is no thread to say it in", got)
	}
	if filed.Posts() {
		t.Fatalf("%#v posts, want the report store to hold it and the summaries to carry it", filed)
	}
}

// A recorded directive answers from what the record left unsettled rather than
// from its kind, for the reason its whose-move clause does. One that left
// something unresolved pauses the work it affects until somebody settles it; one
// that settled nothing is in force already and stops nothing, and the person who
// typed it is answered by name either way.
func TestOnlyAPausingDirectiveReachesTheChannel(t *testing.T) {
	t.Parallel()

	topic, err := WorkItem("yoyodyne-ifd.314")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	operational := noted(KindDirectiveRecorded)
	if got := reachOf(topic, operational); got != ReachThread {
		t.Fatalf("a directive that stopped nothing reaches %q, want the thread", got)
	}
	pausing := operational
	pausing.Detail.Unresolved = "which branch does this land on?"
	if got := reachOf(topic, pausing); got != ReachChannel {
		t.Fatalf("a directive that paused the work reaches %q, want the channel", got)
	}
}

// The reach is decided by the record and carried on the envelope, so whatever
// posts reads it rather than working it out again. Two surfaces deciding this
// for themselves is the disagreement one derivation exists to prevent.
func TestTheRenderedMessageCarriesTheReach(t *testing.T) {
	t.Parallel()

	topic, err := WorkItem("yoyodyne-ifd.314")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	message, err := Render(topic, Harness(), noted(KindChecksPassed))
	if err != nil {
		t.Fatalf("render checks passing: %v", err)
	}
	if message.Reach != ReachThread {
		t.Fatalf("reach = %q, want the thread", message.Reach)
	}
	parked := noted(KindRunParked)
	parked.Severity = report.SeverityWarning
	parked.Detail.Cause = "the provider's usage window"
	message, err = Render(topic, Harness(), parked)
	if err != nil {
		t.Fatalf("render a parked run: %v", err)
	}
	if message.Reach != ReachChannel {
		t.Fatalf("reach = %q, want the channel", message.Reach)
	}
}

// A notification selection had nothing to say about posts nowhere, exactly as one
// whose reach is the record it came from does. Both advance a cursor; neither
// reaches a reader.
func TestNothingToSayPostsNowhere(t *testing.T) {
	t.Parallel()

	if (Notification{}).Posts() {
		t.Fatal("an empty notification posts, want a cursor advance and nothing said")
	}
	if got := (Notification{}).Reach(); got != ReachRecord {
		t.Fatalf("reach = %q, want the record: no kind is classified as reaching further", got)
	}
}
