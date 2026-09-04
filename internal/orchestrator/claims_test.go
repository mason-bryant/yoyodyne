package orchestrator

// The audit that gives back a claim with nothing alive behind it, and the pass
// that runs it. The four nights it exists for are the regression cases in
// readmodel; what is here is what the harness does about one — the tracker write,
// the record that keeps it to once, and the placement in the loop that decides
// whether a full machine ever reaches it at all.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// auditMoment is when every audit below is made.
var auditMoment = time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// claimHarness stands in for the tracker, the run store, and the release log.
type claimHarness struct {
	mu sync.Mutex
	// released is every item this harness was asked to give back, with the note
	// the audit wrote onto it.
	released map[string]string
	// order is the identifiers in the order they were released, so a test can say
	// what a sweep of several did.
	order []string
	runs  []runstate.State
	log   []runstate.ReleasedClaim
	// failRelease and failAppend stand in for a tracker that refuses and a log
	// that cannot be written.
	failRelease error
	failAppend  error
	failRuns    error
}

func newClaimHarness(runs ...runstate.State) *claimHarness {
	return &claimHarness{released: map[string]string{}, runs: runs}
}

func (h *claimHarness) Release(_ context.Context, id, reason string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failRelease != nil {
		return beads.WorkItem{}, h.failRelease
	}
	h.released[id] = reason
	h.order = append(h.order, id)
	return beads.WorkItem{ID: id, Status: "open"}, nil
}

func (h *claimHarness) Recorded() ([]runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failRuns != nil {
		return nil, h.failRuns
	}
	return h.runs, nil
}

func (h *claimHarness) Append(released runstate.ReleasedClaim) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failAppend != nil {
		return h.failAppend
	}
	h.log = append(h.log, released)
	return nil
}

func (h *claimHarness) List() ([]runstate.ReleasedClaim, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.log, nil
}

func (h *claimHarness) auditor() ClaimAuditor {
	return ClaimAuditor{
		Tracker:   h,
		Runs:      h,
		Releases:  h,
		ProductID: "yoyodyne",
		Clock:     fixedClock{at: auditMoment},
	}
}

// deadRun is the record a killed process leaves: in flight, and nothing written
// to it since the night before.
func deadRun(runID, workItemID string) runstate.State {
	moment := auditMoment.Add(-9 * time.Hour)
	return runstate.State{
		RunID:      runID,
		WorkItemID: workItemID,
		Status:     runstate.StatusRunning,
		Phase:      runstate.PhaseDeveloping,
		StartedAt:  moment.Add(-time.Hour),
		UpdatedAt:  moment,
	}
}

func claimedItem(id, title string) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: title, Status: "in_progress", Priority: 2}
}

// The whole of it in one pass: a claimed item whose run died is given back to the
// queue, the reason reaches the item's own notes, and the release is on the
// durable log for a surface to say once.
func TestADeadClaimIsGivenBackAndRecorded(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-264", "yoyodyne-ifd.264"))
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.264", "Recoverable failures retry with backoff")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 {
		t.Fatalf("released = %+v, want the dead claim given back", sweep.Released)
	}
	if len(sweep.Problems) != 0 {
		t.Fatalf("problems = %v, want none", sweep.Problems)
	}
	note, gave := harness.released["yoyodyne-ifd.264"]
	if !gave {
		t.Fatal("the tracker was never asked to give the claim back")
	}
	if !strings.Contains(note, "gave this item back") {
		t.Fatalf("the note written onto the item is %q, want it to say the harness gave the item back", note)
	}
	recorded := sweep.Released[0]
	if recorded.WorkItemID != "yoyodyne-ifd.264" || recorded.RunID != "run-264" {
		t.Fatalf("recorded = %+v, want the item and the run that left it", recorded)
	}
	if recorded.WorkItemTitle != "Recoverable failures retry with backoff" {
		t.Fatalf("title = %q, want the item's own title carried onto the record", recorded.WorkItemTitle)
	}
	if !recorded.ReleasedAt.Equal(auditMoment) {
		t.Fatalf("released at %s, want the moment of the audit", recorded.ReleasedAt)
	}
	if len(harness.log) != 1 {
		t.Fatalf("the release log holds %d record(s), want the release written down", len(harness.log))
	}
}

// Once means once. The second audit sees an item the tracker still calls claimed
// — a tracker that has not caught up, or a release that came straight back — and
// says nothing, because a second record is a second message about one stuck item.
func TestAClaimIsGivenBackOnceEvenIfTheTrackerStillCallsItClaimed(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-211", "yoyodyne-ifd.211"))
	auditor := harness.auditor()
	claimed := []beads.WorkItem{claimedItem("yoyodyne-ifd.211", "The two-day one")}
	if _, err := auditor.Audit(context.Background(), claimed); err != nil {
		t.Fatalf("first Audit() error = %v", err)
	}
	sweep, err := auditor.Audit(context.Background(), claimed)
	if err != nil {
		t.Fatalf("second Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 {
		t.Fatalf("released = %+v, want the second audit to say nothing", sweep.Released)
	}
	if len(harness.order) != 1 {
		t.Fatalf("the tracker was asked %d time(s), want the claim given back once", len(harness.order))
	}
	if len(harness.log) != 1 {
		t.Fatalf("the release log holds %d record(s), want one", len(harness.log))
	}
}

// A fresh run that dies in its turn is a fresh thing to say. The dedup is against
// the run's own last word rather than against the item, so an item released in
// the morning and stuck again by the evening is reported both times.
func TestASecondDeathOfTheSameItemIsSaidAgain(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-first", "yoyodyne-ifd.9"))
	auditor := harness.auditor()
	claimed := []beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Twice unlucky")}
	if _, err := auditor.Audit(context.Background(), claimed); err != nil {
		t.Fatalf("first Audit() error = %v", err)
	}
	// A second run, started after the release and dead in its turn.
	second := deadRun("run-second", "yoyodyne-ifd.9")
	second.StartedAt = auditMoment.Add(-2 * time.Hour)
	second.UpdatedAt = auditMoment.Add(-90 * time.Minute)
	harness.runs = append(harness.runs, second)
	sweep, err := auditor.Audit(context.Background(), claimed)
	if err != nil {
		t.Fatalf("second Audit() error = %v", err)
	}
	if len(sweep.Released) != 1 || sweep.Released[0].RunID != "run-second" {
		t.Fatalf("released = %+v, want the second death said as its own", sweep.Released)
	}
}

// A claim the tracker will not give back is an item nothing will ever pull, which
// is exactly what must not go unsaid — and it must not take the rest of the sweep
// down with it, because the pass this matters in is the one where several claims
// died at once.
func TestAClaimTheTrackerRefusesIsNamedAndDoesNotStopTheSweep(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(
		deadRun("run-209-7", "yoyodyne-ifd.209.7"),
		deadRun("run-264", "yoyodyne-ifd.264"),
	)
	harness.failRelease = errors.New("the tracker is locked")
	sweep, err := harness.auditor().Audit(context.Background(), []beads.WorkItem{
		claimedItem("yoyodyne-ifd.209.7", "The first of the pair"),
		claimedItem("yoyodyne-ifd.264", "The second of the pair"),
	})
	if err != nil {
		t.Fatalf("Audit() error = %v, want one refused claim reported rather than raised", err)
	}
	if len(sweep.Problems) != 2 {
		t.Fatalf("problems = %v, want both claims named", sweep.Problems)
	}
	for _, problem := range sweep.Problems {
		if !strings.Contains(problem, "nothing will pull it") {
			t.Fatalf("problem = %q, want it to say what the failure costs", problem)
		}
	}
	if len(harness.log) != 0 {
		t.Fatalf("the release log holds %d record(s), want nothing recorded for a release that did not happen", len(harness.log))
	}
}

// A release the tracker took and the log would not record is reported rather than
// swallowed: the item is back in the queue and nobody will be told why, which is
// a second run for it with nothing accounting for the first.
func TestAReleaseNobodyCouldRecordIsReported(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness(deadRun("run-9", "yoyodyne-ifd.9"))
	harness.failAppend = errors.New("the log is read-only")
	sweep, err := harness.auditor().Audit(context.Background(),
		[]beads.WorkItem{claimedItem("yoyodyne-ifd.9", "Unrecorded")})
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 {
		t.Fatalf("released = %+v, want a release nobody recorded reported rather than claimed", sweep.Released)
	}
	if len(sweep.Problems) != 1 || !strings.Contains(sweep.Problems[0], "nobody will be told") {
		t.Fatalf("problems = %v, want the unrecorded release named", sweep.Problems)
	}
}

// Nothing claimed is answered without reading anything at all, which is the
// ordinary case on every pull of a quiet product.
func TestAnAuditWithNothingClaimedReadsNothing(t *testing.T) {
	t.Parallel()

	harness := newClaimHarness()
	harness.failRuns = errors.New("the run store would have been read")
	sweep, err := harness.auditor().Audit(context.Background(), nil)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	if len(sweep.Released) != 0 || len(sweep.Problems) != 0 {
		t.Fatalf("sweep = %+v, want nothing at all", sweep)
	}
}

// An auditor missing any of its three parts refuses rather than reporting a sweep
// it never made.
func TestAnAuditorWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	if _, err := (ClaimAuditor{}).Audit(context.Background(), []beads.WorkItem{claimedItem("yoyodyne-ifd.9", "x")}); err == nil {
		t.Fatal("Audit() error = nil, want a refusal from an auditor with nothing wired")
	}
}

// The placement in the loop, which is the half of this that decides whether the
// failure it exists for is ever reached. Both developer slots are held by runs
// that died, so the pass never gets as far as the queue — and the audit still
// runs, because it is asked before the machine's own capacity is consulted.
func TestAFullMachineOfDeadRunsStillAuditsItsClaims(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	harness.inFlight["yoyodyne-ifd.209.7"] = deadRun("run-209-7", "yoyodyne-ifd.209.7")
	harness.inFlight["yoyodyne-ifd.264"] = deadRun("run-264", "yoyodyne-ifd.264")
	harness.items = []beads.WorkItem{
		claimedItem("yoyodyne-ifd.209.7", "The first of the pair"),
		claimedItem("yoyodyne-ifd.264", "The second of the pair"),
	}
	var audited [][]string
	harness.audit = func(_ *scheduleHarness, claimed []beads.WorkItem) (ClaimSweep, error) {
		ids := make([]string, 0, len(claimed))
		for _, item := range claimed {
			ids = append(ids, item.ID)
		}
		audited = append(audited, ids)
		return ClaimSweep{Released: []runstate.ReleasedClaim{{
			SchemaVersion: runstate.ReleasedClaimSchemaVersion,
			ProductID:     "yoyodyne",
			WorkItemID:    "yoyodyne-ifd.264",
			Because:       "its run run-264 is recorded as still in flight",
			ReleasedAt:    auditMoment,
		}}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleCapacityFull {
		t.Fatalf("stopped = %q, want the pass to have found the machine full", schedule.Stopped)
	}
	if len(audited) != 1 {
		t.Fatalf("the claims were audited %d time(s), want once on the pull that found the machine full", len(audited))
	}
	if len(audited[0]) != 2 {
		t.Fatalf("audited %v, want both claimed items handed to the audit", audited[0])
	}
	if len(schedule.Released) != 1 || schedule.Released[0].WorkItemID != "yoyodyne-ifd.264" {
		t.Fatalf("released = %+v, want what the audit gave back on the schedule", schedule.Released)
	}
	if !strings.Contains(schedule.Render(), "given back to the queue") {
		t.Fatalf("the rendered pass does not say what it gave back:\n%s", schedule.Render())
	}
}

// An audit that fails costs the pass nothing it was doing. The line goes on
// choosing work and the failure is reported beside it, because a session that
// stopped pulling because it could not read a claim would be a worse failure than
// the stuck item it was looking for.
func TestAnAuditThatFailsDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-alpha")...)
	harness.audit = func(*scheduleHarness, []beads.WorkItem) (ClaimSweep, error) {
		return ClaimSweep{}, errors.New("the run store could not be read")
	}
	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d run(s), want the pass to have carried on choosing work", len(schedule.Started))
	}
	if !strings.Contains(schedule.ClaimProblem, "could not be audited") {
		t.Fatalf("claim problem = %q, want the failed audit reported beside the pass", schedule.ClaimProblem)
	}
}

// The default clock is the real one, so an auditor a caller wired without one
// still stamps its records rather than writing the zero time.
func TestAnAuditorWithoutAClockUsesTheWallClock(t *testing.T) {
	t.Parallel()

	auditor := ClaimAuditor{Tracker: &claimHarness{}, Runs: &claimHarness{}, Releases: &claimHarness{}, ProductID: "yoyodyne"}
	if _, ok := auditor.clock().(execution.RealClock); !ok {
		t.Fatalf("clock() = %T, want the wall clock", auditor.clock())
	}
}
