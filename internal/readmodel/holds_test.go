package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The five stoppages the 2026-09-04 reading named as the ones an audit must pass
// over. Four are runs whose branch and worktree the harness still holds; the
// fifth reached the development manager and has been sitting on her answer. None
// of them has an unfinished dependency, so nothing but this holds them back, and
// releasing any of them starts a fresh run on top of a change that is still
// there.
func TestPreservedWorkAndUndecidedStoppagesAreHeldForAPerson(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	delivered := time.Date(2026, 9, 3, 1, 31, 0, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{
			preservedRun("run-f4fbf60a", "yoyodyne-ifd.148", stopped),
			preservedRun("run-5035c832", "yoyodyne-ifd.153", stopped),
			preservedRun("run-f28ebe44", "yoyodyne-ifd.174", stopped),
			preservedRun("run-031981f8", "yoyodyne-ifd.68.20", stopped),
		},
		[]runstate.Escalation{{
			WorkItemID: "yoyodyne-ifd.241", RunID: "run-ffbfc9d1",
			Attempts: 1, DeliveredAt: &delivered,
		}},
		nothingDecided,
		asRecorded,
	)

	for _, id := range []string{
		"yoyodyne-ifd.148", "yoyodyne-ifd.153", "yoyodyne-ifd.174", "yoyodyne-ifd.68.20",
	} {
		reason := heldReason(t, held, id)
		if !strings.Contains(reason, "its change is preserved") {
			t.Fatalf("%s is held for %q, want the preserved change named", id, reason)
		}
	}
	pending := heldReason(t, held, "yoyodyne-ifd.241")
	if !strings.Contains(pending, "in front of the development manager") {
		t.Fatalf("yoyodyne-ifd.241 is held for %q, want the undelivered decision named", pending)
	}
}

// The other side of the same rule, and the one that ends the idle morning: a run
// that stopped and was cleaned up afterwards holds nothing. Its change is gone,
// so there is nothing for a fresh run to strand, and the item goes back to being
// decided by its dependencies.
func TestAStoppageWhoseWorkWasCleanedUpHoldsNothing(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	swept := preservedRun("run-0aa11bb2", "yoyodyne-ifd.78", stopped)
	swept.BranchRemoved = true
	swept.WorktreeRemoved = true

	held := heldForAPerson(
		[]runstate.State{
			swept,
			// A run that ended without a durable blocker was never handed to anybody,
			// so it is nobody's to decide about however much of it survives.
			func() runstate.State {
				finished := preservedRun("run-3c4d5e6f", "yoyodyne-ifd.243", stopped)
				finished.Blocker = ""
				finished.Status = runstate.StatusSucceeded
				return finished
			}(),
		},
		[]runstate.Escalation{{
			// Answered: triage said what happens to it, so this is not what holds it.
			WorkItemID: "yoyodyne-ifd.78", RunID: "run-0aa11bb2",
			Attempts: 1, DeliveredAt: &decided, Decision: "rerun", Reason: "the ground moved",
		}},
		nothingDecided,
		asRecorded,
	)

	for _, id := range []string{"yoyodyne-ifd.78", "yoyodyne-ifd.243"} {
		if reason, ok := held.Reason(id); ok {
			t.Fatalf("%s was held for %q, want nothing holding it", id, reason)
		}
	}
}

// What an undecided stoppage says is which person it is waiting on, and the
// three answers are three different people to go to: the development manager who
// has it and has not answered, the harness that has not finished putting it to
// her, and — once the tries are gone — whoever reads the queue next. Each is
// pinned rather than left to whichever branch a fixture happens to take, because
// the one that reads as quiet and is not, a stoppage nobody was ever asked
// about, is the one that costs a morning.
func TestAnUndecidedStoppageSaysWhichPersonItIsWaitingOn(t *testing.T) {
	t.Parallel()

	const (
		stoppedItem = "yoyodyne-ifd.153"
		stoppedRun  = "run-5035c832"
	)
	delivered := time.Date(2026, 9, 3, 1, 31, 0, 0, time.UTC)

	for _, stoppage := range []struct {
		name       string
		escalation runstate.Escalation
		want       string
	}{
		{
			name: "delivered and unanswered",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: 1, DeliveredAt: &delivered,
			},
			// "is in front of" rather than "in front of": the exhausted case below
			// says "could not be put in front of", so the shorter phrase would pass
			// on either branch and pin neither.
			want: "is in front of the development manager",
		},
		{
			// An attempt failed and there are tries left, so the harness is still
			// going to ask. It is held meanwhile: nobody has decided anything, and a
			// fresh run started under a stoppage still on its way to her is the same
			// mistake as one started under a stoppage she is holding.
			name: "not delivered with attempts left",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: runstate.MaxEscalationAttempts - 1,
				Problem:  "her conversation was busy",
			},
			want: "has not reached the development manager yet",
		},
		{
			name: "delivery attempts exhausted",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: runstate.MaxEscalationAttempts,
				Problem:  "development manager reported failure: cancelled",
			},
			want: "needs a person",
		},
	} {
		t.Run(stoppage.name, func(t *testing.T) {
			t.Parallel()

			held := heldForAPerson(nil, []runstate.Escalation{stoppage.escalation}, nothingDecided, asRecorded)
			reason := heldReason(t, held, stoppedItem)
			if !strings.Contains(reason, stoppage.want) {
				t.Fatalf("the hold says %q, want it to name %q", reason, stoppage.want)
			}
		})
	}
}

// An item that stopped more than once is described by its latest stoppage. An
// older run's account would send somebody after a branch that a later run has
// already superseded.
func TestTheLatestStoppageDescribesAnItemThatStoppedTwice(t *testing.T) {
	t.Parallel()

	first := preservedRun("run-aaaaaaaa", "yoyodyne-ifd.100", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	second := preservedRun("run-bbbbbbbb", "yoyodyne-ifd.100", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))

	for _, order := range [][]runstate.State{{first, second}, {second, first}} {
		reason := heldReason(t, heldForAPerson(order, nil, nothingDecided, asRecorded), "yoyodyne-ifd.100")
		if !strings.Contains(reason, "run-bbbbbbbb") {
			t.Fatalf("the hold names %q, want the later run", reason)
		}
	}
}

// A reading that failed is an error rather than an empty answer, because an
// empty answer is indistinguishable from nothing being held and would release
// exactly the work this holds.
func TestAFailedReadingIsAnErrorRatherThanNoHolds(t *testing.T) {
	t.Parallel()

	unreadable := errors.New("state root is not readable")
	if _, err := HeldForAPerson(context.Background(), failingStoppages{runs: unreadable}, nil, nil); !errors.Is(err, unreadable) {
		t.Fatalf("HeldForAPerson() error = %v, want the run reading's failure", err)
	}
	if _, err := HeldForAPerson(context.Background(), failingStoppages{escalations: unreadable}, nil, nil); !errors.Is(err, unreadable) {
		t.Fatalf("HeldForAPerson() error = %v, want the escalation reading's failure", err)
	}
}

// The yoyodyne-ifd.295 shape: a run that succeeded, integrated its change, and
// left only its publication unfinished. Nothing else holds such an item — the
// run cleaned its own artifacts up, so the preserved-change rule says nothing
// about it — and what that cost was three developer runs and three reviews, each
// pulling the item as ordinary ready work and re-deriving that the change had
// already landed.
func TestAnItemWhoseOnlyOutstandingStateIsAPublicationIsHeldForAPerson(t *testing.T) {
	t.Parallel()

	held := heldForAPerson([]runstate.State{publishedRun("run-55443d4c", "yoyodyne-ifd.295")}, nil, nothingDecided, asRecorded)
	reason := heldReason(t, held, "yoyodyne-ifd.295")
	for _, want := range []string{"run-55443d4c", "the forge merged it", "nothing here to implement"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the hold says %q, want it to name %q", reason, want)
		}
	}
}

// A merge the forge dropped is the other publication that leaves an item
// outstanding, and it is held too: the change is on the local target branch,
// which is the authoritative one, so a run against it would redo work that has
// landed and re-arming the merge is a triage decision. What it must not say is
// that the forge merged anything. Nothing did, something the base branch
// required went unmet, and a hold asserting the merge happened would be a false
// statement in the one derivation the scheduler and the docket both quote.
func TestADroppedMergeIsHeldWithoutClaimingTheForgeMergedIt(t *testing.T) {
	t.Parallel()

	dropped := publishedRun("run-8f31ca02", "yoyodyne-ifd.288")
	dropped.PullRequest.State = "OPEN"
	dropped.PullRequest.Merged = false
	dropped.PublishFailure = "the forge dropped the queued merge of pull request 84: it is open and has no merge queued for it"

	reason := heldReason(t, heldForAPerson([]runstate.State{dropped}, nil, nothingDecided, asRecorded), "yoyodyne-ifd.288")
	if !strings.Contains(reason, "the forge has not merged it") {
		t.Errorf("the hold says %q, want the unmerged publication named", reason)
	}
	if strings.Contains(reason, "the forge merged it") {
		t.Errorf("the hold says %q, which claims a merge the forge dropped", reason)
	}
}

// The other side of it. A publication that finished holds nothing, and neither
// does a run still in flight, which owns its own publication and has not
// finished asking.
func TestAFinishedOrInFlightPublicationHoldsNothing(t *testing.T) {
	t.Parallel()

	settled := publishedRun("run-9c1f2ab3", "yoyodyne-ifd.300")
	settled.PublishFailure = ""
	inFlight := publishedRun("run-7d2e4cc1", "yoyodyne-ifd.302")
	inFlight.Status = runstate.StatusRunning

	held := heldForAPerson([]runstate.State{settled, inFlight}, nil, nothingDecided, asRecorded)
	for _, id := range []string{"yoyodyne-ifd.300", "yoyodyne-ifd.302"} {
		if reason, ok := held.Reason(id); ok {
			t.Fatalf("%s was held for %q, want nothing holding it", id, reason)
		}
	}
}

// An item that is both — a change still on a branch and a publication that did
// not finish — is described by whichever of the two a reader can act on, and
// which that is depends on the merge. A confirmed merge makes the branch debris:
// the change is everywhere it was going, so the publication is what is left to
// say. A merge nobody made does not, so the branch is still a decision and the
// preserved-change reason is the one that sends triage to it.
func TestABranchLeftBehindReadsAsThePublicationOnlyWhereTheMergeIsConfirmed(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 2, 0, 0, 0, time.UTC)
	merged := preservedRun("run-1b782eeb", "yoyodyne-ifd.295", stopped)
	merged.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: "bb8ec09", TargetCommit: "bb8ec09"}
	merged.PullRequest = &runstate.PullRequest{Number: 424, Branch: merged.Branch, Merged: true, MergeCommit: "262372c"}
	merged.PublishFailure = "delete the merged remote branch: Connection reset by peer"

	unmerged := preservedRun("run-8f31ca02", "yoyodyne-ifd.288", stopped)
	unmerged.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: "0d392c4", TargetCommit: "0d392c4"}
	unmerged.PullRequest = &runstate.PullRequest{Number: 84, Branch: unmerged.Branch, State: "OPEN"}
	unmerged.PublishFailure = "the forge dropped the queued merge of pull request 84"

	held := heldForAPerson([]runstate.State{merged, unmerged}, nil, nothingDecided, asRecorded)
	if reason := heldReason(t, held, "yoyodyne-ifd.295"); !strings.Contains(reason, "the forge merged it") {
		t.Errorf("the merged item is held for %q, want the publication rather than the preserved change", reason)
	}
	if reason := heldReason(t, held, "yoyodyne-ifd.288"); !strings.Contains(reason, "its change is preserved") {
		t.Errorf("the unmerged item is held for %q, want the branch it left behind named", reason)
	}
}

// publishedRun is a run that finished, integrated its change, and could not
// finish publishing it: the forge merged, and deleting the branch that merge
// consumed failed on a reset connection.
func publishedRun(runID, workItemID string) runstate.State {
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusSucceeded,
		UpdatedAt:    time.Date(2026, 9, 6, 3, 2, 59, 0, time.UTC),
		Branch:       "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath: "/state/worktrees/" + runID,
		// An integrated run removes both, which is why nothing else holds this.
		BranchRemoved:   true,
		WorktreeRemoved: true,
		Integration: &runstate.Integration{
			TargetBranch: "main",
			SourceCommit: "b206ca1",
			TargetCommit: "b206ca1",
		},
		PullRequest: &runstate.PullRequest{
			Number:      428,
			Branch:      "yoyodyne/" + workItemID + "/" + runID,
			HeadCommit:  "b206ca1",
			State:       "MERGED",
			Merged:      true,
			MergeCommit: "f382df4",
		},
		PublishFailure: "delete the merged remote branch: resolve " + workItemID +
			" on origin failed with exit code 128: Read from remote host ssh.github.com: Connection reset by peer",
	}
}

func preservedRun(runID, workItemID string, stopped time.Time) runstate.State {
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusFailed,
		UpdatedAt:    stopped,
		Branch:       "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath: "/state/worktrees/" + runID,
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

func heldReason(t *testing.T, held backlog.Holds, id string) string {
	t.Helper()
	reason, ok := held.Reason(id)
	if !ok {
		t.Fatalf("%s is not held, want it held for a person", id)
	}
	return reason
}

type failingStoppages struct {
	runs        error
	escalations error
}

func (f failingStoppages) Recorded() ([]runstate.State, error) { return nil, f.runs }

func (f failingStoppages) Escalated() ([]runstate.Escalation, error) { return nil, f.escalations }

// The 2026-09-07 shape. The development manager had decided every one of the
// thirty-three stoppages that were reading as work she owed a decision on, and
// what was missing was the harness carrying those decisions out. A hold that
// cannot say which of the two it is sends the operator to the role that has
// already done its job.
func TestAStoppageWithADecisionRecordedAwaitsItsCarryOutRatherThanADecision(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{
			preservedRun("run-aaaa1111", "yoyodyne-ifd.150", stopped),
			preservedRun("run-bbbb2222", "yoyodyne-ifd.151", stopped),
		},
		nil,
		decisions(map[string]runstate.TriageCounters{
			"yoyodyne-ifd.150": {Decisions: []runstate.TriageDecision{{
				Decision: runstate.TriageDecisionRerun, RunID: "run-aaaa1111",
			}}},
		}),
		asRecorded,
	)

	decided := heldReason(t, held, "yoyodyne-ifd.150")
	if !strings.Contains(decided, "already decided") || !strings.Contains(decided, "carrying that decision out") {
		t.Errorf("the decided stoppage is held for %q, want the carry-out named as what is outstanding", decided)
	}
	undecided := heldReason(t, held, "yoyodyne-ifd.151")
	if !strings.Contains(undecided, "the development manager decides what happens to it") {
		t.Errorf("the undecided stoppage is held for %q, want her decision named", undecided)
	}
	if !held.Decided("yoyodyne-ifd.150") || held.Decided("yoyodyne-ifd.151") {
		t.Errorf("the holds report the wrong movers: %#v", held)
	}
}

// A granted repair is asked of the grant rather than of the decision, because a
// repair continues the run it was granted for and the same run stops again
// carrying the same decision. A grant whose rounds have all been spent has been
// carried out, so what the stoppage after it waits on is a fresh decision.
func TestAGrantThatHasBeenSpentIsNotAStoppageAwaitingCarryOut(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repair := func(committed, spent int) runstate.TriageCounters {
		return runstate.TriageCounters{
			RepairGrants: 1, CommittedRounds: committed, ReviewRounds: spent,
			Decisions: []runstate.TriageDecision{{
				Decision: runstate.TriageDecisionRepair, RunID: "run-cccc3333",
			}},
		}
	}
	for _, granted := range []struct {
		name     string
		counters runstate.TriageCounters
		want     string
	}{
		{name: "unspent", counters: repair(3, 2), want: "carrying that decision out"},
		{name: "spent", counters: repair(3, 3), want: "the development manager decides what happens to it"},
	} {
		t.Run(granted.name, func(t *testing.T) {
			t.Parallel()

			held := heldForAPerson(
				[]runstate.State{preservedRun("run-cccc3333", "yoyodyne-ifd.152", stopped)},
				nil,
				decisions(map[string]runstate.TriageCounters{"yoyodyne-ifd.152": granted.counters}),
				asRecorded,
			)
			if reason := heldReason(t, held, "yoyodyne-ifd.152"); !strings.Contains(reason, granted.want) {
				t.Fatalf("the hold says %q, want it to name %q", reason, granted.want)
			}
		})
	}
}

// A wait, a re-scope and an escalation leave the harness nothing to do, so an
// item still held under one of them is held by what was decided rather than by
// anything outstanding. Naming the harness as its next mover would send an
// operator to watch for a run nothing is going to start.
func TestADecisionTheHarnessDoesNotCarryOutLeavesTheStoppageWaitingOnHer(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, word := range []string{
		runstate.TriageDecisionWait,
		runstate.TriageDecisionRescope,
		runstate.TriageDecisionEscalate,
	} {
		t.Run(word, func(t *testing.T) {
			t.Parallel()

			held := heldForAPerson(
				[]runstate.State{preservedRun("run-dddd4444", "yoyodyne-ifd.154", stopped)},
				nil,
				decisions(map[string]runstate.TriageCounters{"yoyodyne-ifd.154": {
					Decisions: []runstate.TriageDecision{{Decision: word, RunID: "run-dddd4444"}},
				}}),
				asRecorded,
			)
			if held.Decided("yoyodyne-ifd.154") {
				t.Fatalf("a %q decision reads as one the harness has still to carry out", word)
			}
		})
	}
}

// A triage record nobody can open costs the hold nothing: the item is held
// exactly as it was, and what the reading could not say it says rather than
// guessing. One unreadable file must not make a whole queue unpullable, and it
// must not claim the harness owes a carry-out nothing established.
func TestATriageRecordThatCouldNotBeReadHoldsTheItemAndSaysSo(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{preservedRun("run-eeee5555", "yoyodyne-ifd.155", stopped)},
		nil,
		standingDecisions(failingDecisions{errors.New("open triage counters: permission denied")}),
		asRecorded,
	)
	reason := heldReason(t, held, "yoyodyne-ifd.155")
	for _, want := range []string{"its change is preserved", "could not be read", "permission denied"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the hold says %q, want it to name %q", reason, want)
		}
	}
	if held.Decided("yoyodyne-ifd.155") {
		t.Errorf("a hold nothing could be read about claims a carry-out is outstanding")
	}
}

// A pull wired without the record is the same case as one that could not read
// it: the item is held and stated as one nobody has decided about, which is
// where the answer went before the two were told apart.
func TestAHoldReadWithoutTheTriageRecordSaysNothingWasWiredToReadIt(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{preservedRun("run-ffff6666", "yoyodyne-ifd.156", stopped)},
		nil,
		standingDecisions(nil),
		asRecorded,
	)
	if reason := heldReason(t, held, "yoyodyne-ifd.156"); !strings.Contains(reason, "nothing was wired to read") {
		t.Fatalf("the hold says %q, want the missing record named", reason)
	}
}

// nothingDecided is the reading of an item nobody has decided anything about,
// which is every item in the fixtures that predate the two holds being told
// apart.
func nothingDecided(string, string) (bool, string) { return false, "" }

// decisions is a triage record readable for the items it names, and empty for
// every other — which is what an item nothing has been decided about actually
// reads as.
func decisions(recorded map[string]runstate.TriageCounters) standing {
	return standingDecisions(recordedDecisions(recorded))
}

type recordedDecisions map[string]runstate.TriageCounters

func (r recordedDecisions) Counters(workItemID string) (runstate.TriageCounters, error) {
	return r[workItemID], nil
}

type failingDecisions struct{ err error }

func (f failingDecisions) Counters(string) (runstate.TriageCounters, error) {
	return runstate.TriageCounters{}, f.err
}

// asRecorded looks at nothing and answers from each run's own record, which is
// what every fixture above that predates the look was written against. The
// tests below are the ones about the look itself.
func asRecorded(run runstate.State) survival {
	recorded := run.Artifacts()
	return survival{Found: gitworktree.Survival{
		BranchExists:    recorded.Branch != "" && !recorded.BranchRemoved,
		WorktreePresent: recorded.WorktreePath != "" && !recorded.WorktreeRemoved,
	}}
}

// remainsOf is a repository that holds exactly the artifacts it lists, by run,
// and it keeps what it was asked about so a test can see the run's own
// identifiers were what the look was made with.
type remainsOf struct {
	survives map[string]gitworktree.Survival
	asked    []gitworktree.Worktree
}

func (r *remainsOf) Survives(_ context.Context, worktree gitworktree.Worktree) (gitworktree.Survival, error) {
	r.asked = append(r.asked, worktree)
	return r.survives[worktree.RunID], nil
}

type failingRemains struct{ err error }

func (f failingRemains) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return gitworktree.Survival{}, f.err
}

// The yoyodyne-ifd.372 shape, 2026-09-19. The run stopped with its change
// preserved, the item's own notes said the branch and worktree were checked and
// there, and the product manager's repair cleared the blocked status as "no
// longer held behind a preserved run" — because the hold was read off the run's
// removal flags and never looked. Whether a stopped run's change is still there
// is the repository's answer: a run whose branch or worktree exists is held
// whatever its flags say, with the run named and what was found stated, and a
// run whose flags say preserved over artifacts that are gone holds nothing.
func TestAStoppedRunIsHeldOnWhatTheRepositoryHoldsRatherThanItsRecord(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 16, 10, 5, 41, 0, time.UTC)
	sweptOnRecord := preservedRun("run-192522d8", "yoyodyne-ifd.372", stopped)
	sweptOnRecord.BaseCommit = "449b375812b5f92a3d64efc33f7fcf26b41584e3"
	sweptOnRecord.BranchRemoved = true
	sweptOnRecord.WorktreeRemoved = true
	preservedOnRecord := preservedRun("run-48216ea9", "yoyodyne-ifd.275", stopped)

	repository := &remainsOf{survives: map[string]gitworktree.Survival{
		"run-192522d8": {BranchExists: true, WorktreePresent: true},
		"run-48216ea9": {},
	}}
	held := heldForAPerson(
		[]runstate.State{sweptOnRecord, preservedOnRecord},
		nil,
		nothingDecided,
		lookingFor(context.Background(), repository),
	)

	reason := heldReason(t, held, "yoyodyne-ifd.372")
	for _, want := range []string{
		"run-192522d8", "its change is preserved", "(branch and worktree checked and there)",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("yoyodyne-ifd.372 is held for %q, want it to say %q", reason, want)
		}
	}
	if reason, ok := held.Reason("yoyodyne-ifd.275"); ok {
		t.Errorf("yoyodyne-ifd.275 is held for %q, want nothing holding it: its record says preserved and the repository holds neither artifact", reason)
	}
	// The look was made with the run's own identifiers, which is what proves the
	// answer is about this run's branch and directory rather than any other's.
	if len(repository.asked) != 2 {
		t.Fatalf("the repository was asked about %d run(s), want both stopped runs", len(repository.asked))
	}
	for _, asked := range repository.asked {
		if asked.RunID != "run-192522d8" {
			continue
		}
		if asked.WorkItemID != "yoyodyne-ifd.372" || asked.Branch != sweptOnRecord.Branch || asked.Path != sweptOnRecord.WorktreePath || asked.BaseCommit != sweptOnRecord.BaseCommit {
			t.Errorf("the look was made with %#v, want the run's own identifiers", asked)
		}
	}
}

// A look that failed is not a look that found nothing. The run is held as if
// its change were there, and the reason says what stopped the look, because
// releasing on an answer nobody got is the same mistake as releasing on a flag.
func TestAStoppedRunWhoseChangeCouldNotBeLookedForIsHeldAsPreserved(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 16, 10, 5, 41, 0, time.UTC)
	swept := preservedRun("run-192522d8", "yoyodyne-ifd.372", stopped)
	swept.BranchRemoved = true
	swept.WorktreeRemoved = true

	held := heldForAPerson(
		[]runstate.State{swept},
		nil,
		nothingDecided,
		lookingFor(context.Background(), failingRemains{errors.New("worktree base commit is invalid")}),
	)
	reason := heldReason(t, held, "yoyodyne-ifd.372")
	for _, want := range []string{"run-192522d8", "could not be checked", "worktree base commit is invalid", "may still be there"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the hold says %q, want it to name %q", reason, want)
		}
	}
}

// A reading with nothing wired to look answers from the record and says so,
// which is the one case the flags still decide: the surfaces that look and the
// surface that cannot have to agree wherever the record is right, and the
// reason has to say which of the two this was.
func TestAReadingWithNothingWiredToLookAnswersFromTheRecordAndSaysSo(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 16, 10, 5, 41, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{preservedRun("run-48216ea9", "yoyodyne-ifd.275", stopped)},
		nil,
		nothingDecided,
		lookingFor(context.Background(), nil),
	)
	reason := heldReason(t, held, "yoyodyne-ifd.275")
	for _, want := range []string{"its change is preserved", "as its record says", "nothing having been wired to look"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the hold says %q, want it to name %q", reason, want)
		}
	}
}

// The other half of the 372 repair. Once the development manager had recorded a
// repair continuation on the stopped run, the item was held by that decision
// whatever became of the run's artifacts: what the grant continues is the run,
// in the session it preserved, and a fresh pull meanwhile would start over
// beside it. So a stopped run about which a decision stands that the harness
// has still to carry out is held with nothing of it surviving, and the hold
// names the carry-out as what is outstanding.
func TestAStoppedRunWithARecordedContinuationIsHeldWithNothingSurviving(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 16, 10, 5, 41, 0, time.UTC)
	swept := preservedRun("run-192522d8", "yoyodyne-ifd.372", stopped)
	swept.BranchRemoved = true
	swept.WorktreeRemoved = true
	gone := &remainsOf{survives: map[string]gitworktree.Survival{}}

	continued := decisions(map[string]runstate.TriageCounters{"yoyodyne-ifd.372": {
		RepairGrants: 1, CommittedRounds: 2, ReviewRounds: 0,
		Decisions: []runstate.TriageDecision{{
			Decision: runstate.TriageDecisionRepair, RunID: "run-192522d8",
		}},
	}})
	held := heldForAPerson([]runstate.State{swept}, nil, continued, lookingFor(context.Background(), gone))
	reason := heldReason(t, held, "yoyodyne-ifd.372")
	for _, want := range []string{"run-192522d8", "not yet carried out", "carrying that decision out"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the hold says %q, want it to name %q", reason, want)
		}
	}
	if !held.Decided("yoyodyne-ifd.372") {
		t.Errorf("the hold does not name the harness as the next mover: %#v", held)
	}

	// And the same run with nothing decided about it holds nothing, which is what
	// separates a continuation from a stoppage whose change is simply gone.
	released := heldForAPerson([]runstate.State{swept}, nil, nothingDecided, lookingFor(context.Background(), gone))
	if reason, ok := released.Reason("yoyodyne-ifd.372"); ok {
		t.Errorf("with nothing decided and nothing surviving the item is held for %q, want nothing holding it", reason)
	}
}
