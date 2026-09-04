package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// noon is the moment every reading below is taken at.
var noon = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func claimed(id, title string) Claim { return Claim{WorkItemID: id, Title: title} }

// inFlight is a run whose record says it is still going and which last wrote to
// itself `ago` before noon. It is what every killed process leaves behind.
func inFlight(runID, workItemID string, ago time.Duration) runstate.State {
	moment := noon.Add(-ago)
	return runstate.State{
		RunID:      runID,
		WorkItemID: workItemID,
		Status:     runstate.StatusRunning,
		Phase:      runstate.PhaseDeveloping,
		StartedAt:  moment.Add(-time.Hour),
		UpdatedAt:  moment,
	}
}

// ended is a run that reached a terminal status `ago` before noon without giving
// its claim back, which is what an in-process failure leaves behind.
func ended(runID, workItemID string, status runstate.Status, ago time.Duration) runstate.State {
	moment := noon.Add(-ago)
	return runstate.State{
		RunID:       runID,
		WorkItemID:  workItemID,
		Status:      status,
		Phase:       runstate.PhaseDeveloping,
		StartedAt:   moment.Add(-time.Hour),
		UpdatedAt:   moment,
		CompletedAt: &moment,
	}
}

// The four nights this exists for, each as the records actually held it. Every
// one of them is a work item the tracker called in progress with no process
// anywhere working on it, and every existing derivation reads all four as a
// machine that is busy.
func TestTheFourNightsAreAllDeadClaims(t *testing.T) {
	t.Parallel()

	nights := map[string]struct {
		claim Claim
		runs  []runstate.State
	}{
		// yoyodyne-ifd.211, killed by the backend and left claimed for two days.
		"a backend kill two days ago": {
			claim: claimed("yoyodyne-ifd.211", "The two-day one"),
			runs:  []runstate.State{inFlight("run-211", "yoyodyne-ifd.211", 48*time.Hour)},
		},
		// yoyodyne-ifd.209.6, which died in its bootstrap twice: the run was
		// reserved and the item claimed, and then the process was gone before it
		// wrote anything else.
		"a bootstrap death that never got past its reservation": {
			claim: claimed("yoyodyne-ifd.209.6", "The one that died starting"),
			runs: []runstate.State{{
				RunID:      "run-209-6",
				WorkItemID: "yoyodyne-ifd.209.6",
				Status:     runstate.StatusPending,
				StartedAt:  noon.Add(-6 * time.Hour),
				UpdatedAt:  noon.Add(-6 * time.Hour),
			}},
		},
		// yoyodyne-ifd.209.7, one of the two that went inside an hour on the last
		// night, killed on the network.
		"a network kill last night": {
			claim: claimed("yoyodyne-ifd.209.7", "The first of the pair"),
			runs:  []runstate.State{inFlight("run-209-7", "yoyodyne-ifd.209.7", 9*time.Hour)},
		},
		// yoyodyne-ifd.264, the other of the pair, whose run recorded its own
		// ending and left the claim standing anyway.
		"a run that ended and did not give the claim back": {
			claim: claimed("yoyodyne-ifd.264", "The second of the pair"),
			runs:  []runstate.State{ended("run-264", "yoyodyne-ifd.264", runstate.StatusFailed, 9*time.Hour)},
		},
	}
	for name, night := range nights {
		dead := DeadClaims([]Claim{night.claim}, night.runs, noon, 0, 0)
		if len(dead) != 1 {
			t.Fatalf("%s: DeadClaims() = %+v, want the claim read as dead", name, dead)
		}
		if dead[0].WorkItemID != night.claim.WorkItemID {
			t.Fatalf("%s: dead claim names %q, want %q", name, dead[0].WorkItemID, night.claim.WorkItemID)
		}
		if dead[0].Since.IsZero() {
			t.Fatalf("%s: dead claim carries no moment to measure from", name)
		}
		if strings.TrimSpace(dead[0].Because) == "" {
			t.Fatalf("%s: dead claim says nothing about what became of the run", name)
		}
		if dead[0].RunID == "" {
			t.Fatalf("%s: dead claim names no run, so nobody can go and look at it", name)
		}
	}
}

// A run that is genuinely working keeps its claim, whatever else is true. This
// is the reading that must not be wrong in this direction: releasing an item a
// developer is halfway through would put a second one on it.
func TestAClaimWithALiveRunBehindItIsLeftAlone(t *testing.T) {
	t.Parallel()

	live := []runstate.State{inFlight("run-live", "yoyodyne-ifd.9", time.Minute)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Working")}, live, noon, 0, 0); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a live run to keep its claim", dead)
	}
	// And an item whose newest run is alive keeps it even though an earlier
	// attempt at the same item ended hours ago.
	together := []runstate.State{
		ended("run-first", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour),
		inFlight("run-second", "yoyodyne-ifd.9", time.Minute),
	}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Working")}, together, noon, 0, 0); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a retried item with a live run to keep its claim", dead)
	}
}

// A run that stopped short and is owed a continuation keeps its claim however
// long it has been quiet. Each of these returns and lets its process exit, so its
// record goes as still as a killed one's — and its item is claimed on purpose,
// with the worktree and the developer session that continuation needs. Giving one
// of these back would put a second run on work that is waiting to be picked up.
func TestARunWaitingToBeContinuedKeepsItsClaimHoweverQuietItIs(t *testing.T) {
	t.Parallel()

	waited := noon.Add(-30 * time.Hour)
	held := noon.Add(-30 * time.Hour)
	for name, park := range map[string]func(*runstate.State){
		"waiting out a provider usage limit": func(s *runstate.State) { s.UsageLimitResetsAt = &waited },
		"waiting out a named refusal":        func(s *runstate.State) { s.PauseCause = "overloaded" },
		"stopped on a provider that stalled": func(s *runstate.State) { s.ProviderStop = runstate.ProviderStopStalled },
		"held up by a directive": func(s *runstate.State) {
			s.DirectivePause = &runstate.DirectivePause{DirectiveID: "directive-1", Unresolved: "which artifact?"}
		},
		"waiting on work it depends on": func(s *runstate.State) {
			s.DependencyPause = &runstate.DependencyPause{}
		},
		"parked by the operator's hold": func(s *runstate.State) { s.OperatorHeldSince = &held },
	} {
		run := inFlight("run-parked", "yoyodyne-ifd.9", 30*time.Hour)
		park(&run)
		dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Parked")}, []runstate.State{run}, noon, 0, 0)
		if len(dead) != 0 {
			t.Fatalf("%s: DeadClaims() = %+v, want a run owed a continuation to keep its claim", name, dead)
		}
	}
}

// The threshold, which is what separates a dead claim from a run that ended a
// moment ago with the tracker not yet caught up.
func TestAClaimIsNotDeadUntilItHasBeenQuietPastTheThreshold(t *testing.T) {
	t.Parallel()

	runs := []runstate.State{ended("run-fresh", "yoyodyne-ifd.9", runstate.StatusFailed, time.Minute)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Just ended")}, runs, noon, 0, 0); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a run that ended a minute ago to be left alone", dead)
	}
	past := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Just ended")}, runs, noon, 30*time.Second, 0)
	if len(past) != 1 {
		t.Fatalf("DeadClaims() = %+v, want the same claim dead once the threshold is shorter than the silence", past)
	}
}

// A killed run that had already been round a repair is a dead claim like any
// other. The count of rounds it took is history rather than a wait: it never goes
// back down, so reading it as a continuation being owed would exempt every
// long-running item from every audit for the rest of the product's life — which
// is the shape of the item that sat claimed for two days.
func TestARunKilledAfterARepairRoundIsStillADeadClaim(t *testing.T) {
	t.Parallel()

	repaired := inFlight("run-repaired", "yoyodyne-ifd.264", 9*time.Hour)
	repaired.RepairAttempts = 2
	repaired.Phase = runstate.PhaseReviewing
	dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.264", "Round once already")}, []runstate.State{repaired}, noon, 0, 0)
	if len(dead) != 1 {
		t.Fatalf("DeadClaims() = %+v, want a killed run read as dead however many repair rounds it took", dead)
	}
}

// An item whose change already landed is not work to be started again. The run
// promoted it and then stopped somewhere after the promotion, so what the item
// wants is closing rather than developing a second time — and giving the claim
// back would buy the same change twice.
func TestAClaimOverWorkThatAlreadyLandedIsNotGivenBack(t *testing.T) {
	t.Parallel()

	promoted := ended("run-landed", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour)
	promoted.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: "abc1234"}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Landed")}, []runstate.State{promoted}, noon, 0, 0); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want an item whose change landed left for reconciliation to close", dead)
	}
}

// A claim the harness never made is not the harness's to give back. The harness
// reserves a run before it claims anything, so a claim with no run behind it is
// a person's — and taking work back off somebody is not this reading's to do.
func TestAClaimWithNoRunBehindItIsNobodysToGiveBack(t *testing.T) {
	t.Parallel()

	other := []runstate.State{ended("run-elsewhere", "yoyodyne-ifd.8", runstate.StatusSucceeded, 9*time.Hour)}
	if dead := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Somebody's own")}, other, noon, 0, 0); len(dead) != 0 {
		t.Fatalf("DeadClaims() = %+v, want a claim with no recorded run left alone", dead)
	}
}

// The two histories are said apart, because a reader acts on the difference: a
// run that ended without giving its claim back is a hole in the pipeline, and a
// run still recorded as in flight is a process something killed.
func TestWhatBecameOfTheRunTellsTheTwoDeathsApart(t *testing.T) {
	t.Parallel()

	over := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Ended")},
		[]runstate.State{ended("run-over", "yoyodyne-ifd.9", runstate.StatusFailed, 9*time.Hour)}, noon, 0, 0)
	if len(over) != 1 || !strings.Contains(over[0].Because, "ended") {
		t.Fatalf("Because = %q, want a run that ended said as one", because(over))
	}
	killed := DeadClaims([]Claim{claimed("yoyodyne-ifd.9", "Killed")},
		[]runstate.State{inFlight("run-killed", "yoyodyne-ifd.9", 9*time.Hour)}, noon, 0, 0)
	if len(killed) != 1 || !strings.Contains(killed[0].Because, "the process holding it is gone") {
		t.Fatalf("Because = %q, want a killed process said as one", because(killed))
	}
	// Neither clause hands anybody a chore: the audit settles the run and gives the
	// item back itself, so a message that named a command would be telling somebody
	// to do what has already been done.
	for _, dead := range append(over, killed...) {
		if strings.Contains(dead.Because, "yoyo reconcile") {
			t.Fatalf("Because = %q, want no command in a clause about work already put right", dead.Because)
		}
	}
}

func because(dead []DeadClaim) string {
	if len(dead) == 0 {
		return ""
	}
	return dead[0].Because
}
