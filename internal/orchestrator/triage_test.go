package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// memoryDocket is the durable docket without the disk: it enforces the one
// property the store guarantees, which is that a key is recorded once.
type memoryDocket struct {
	entries []triage.Entry
	failOn  string
}

func (d *memoryDocket) RecordOnce(entry triage.Entry) (bool, error) {
	if d.failOn != "" && entry.Class == triage.Class(d.failOn) {
		return false, errors.New("the docket is unwritable")
	}
	if err := entry.Validate(); err != nil {
		return false, err
	}
	for _, existing := range d.entries {
		if existing.Key == entry.Key {
			return false, nil
		}
	}
	d.entries = append(d.entries, entry)
	return true, nil
}

// List hands back a copy, as the durable store does by decoding the log afresh:
// what a reader joins onto the entries it was given must not travel back into
// the log, which is written once and never revised.
func (d *memoryDocket) List() ([]triage.Entry, error) { return slices.Clone(d.entries), nil }

func (d *memoryDocket) keys() []string {
	keys := make([]string, 0, len(d.entries))
	for _, entry := range d.entries {
		keys = append(keys, entry.Key)
	}
	return keys
}

// recordedRuns is the run evidence a docket is built from.
type recordedRuns struct {
	states []runstate.State
}

func (r recordedRuns) Recorded() ([]runstate.State, error) { return r.states, nil }

// recordedDecisions is the durable triage record the guards spend and refuse
// against, without the disk: what triage has decided about a work item, and what
// the harness has claimed against that item's stoppages.
type recordedDecisions struct {
	counters map[string]runstate.TriageCounters
	claimed  map[string][]runstate.Rerun
	// unreadable is the work item whose record cannot be read, which is the case
	// an entry must never render as an item nothing was ever decided about.
	unreadable string
}

func (d *recordedDecisions) Counters(workItemID string) (runstate.TriageCounters, error) {
	if workItemID == d.unreadable {
		return runstate.TriageCounters{}, errors.New("the triage record is unreadable")
	}
	return d.counters[workItemID], nil
}

func (d *recordedDecisions) Claimed(workItemID string) ([]runstate.Rerun, error) {
	if workItemID == d.unreadable {
		return nil, errors.New("the re-run records are unreadable")
	}
	return d.claimed[workItemID], nil
}

const (
	docketedRunID = "run-0123456789abcdef0123456789abcdef"
	docketedItem  = "yoyodyne-task"
	docketedTitle = "Slack thread headers carry the item's title"
)

var docketedNow = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

// docketClock keeps the moment a docket is built fixed, so what a test is
// measuring is the age of the work rather than the age of the test.
type docketClock struct{}

func (docketClock) Now() time.Time { return docketedNow }

func docketerOver(states []runstate.State, docket *memoryDocket) Docketer {
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {ReviewRounds: 3}},
	}
	return docketerDeciding(states, docket, recorded, recorded)
}

// docketedTriage is the configuration a docket is built against in these tests,
// and docketedCaps the ceilings assembled from it — the same ones the guards are
// refused against, which is the whole point of the docket reporting them.
var (
	docketedTriage = config.Triage{
		StuckMergeAge:       config.Duration(2 * time.Hour),
		ReviewRoundsCap:     4,
		RepairGrantAttempts: 2,
	}
	docketedCaps = TriageCaps(config.Execution{IntegrationRetriesBeforeReconciliation: 1}, docketedTriage)
)

// docketerDeciding is a docket built over one triage record, for the tests that
// are about what the record says rather than about what stopped.
func docketerDeciding(states []runstate.State, docket *memoryDocket, decisions DocketDecisions, claimed DocketReruns) Docketer {
	return Docketer{
		Docket:    docket,
		Runs:      recordedRuns{states: states},
		Decisions: decisions,
		Reruns:    claimed,
		// The caps are assembled the way every other reader of the same record
		// assembles them, so the docket cannot report an item against a ceiling
		// nothing enforces.
		Caps:   docketedCaps,
		Triage: docketedTriage,
		Clock:  docketClock{},
	}
}

// docketerOverStore is a docket over a real run store, reading the same two
// durable records the triage guards spend: the item's counters and the re-runs
// claimed against its stoppages.
func docketerOverStore(docket Docket, store *runstate.Store, cfg config.Config) *Docketer {
	return &Docketer{
		Docket:    docket,
		Runs:      store,
		Decisions: store.Triage(),
		Reruns:    store.Reruns(),
		Caps:      TriageCaps(cfg.Execution, cfg.Triage),
		Triage:    cfg.Triage,
	}
}

// stoppedState is a run that ended on a durable blocker with its work
// preserved, which is what every blocked run leaves behind.
func stoppedState() runstate.State {
	completed := docketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:        runstate.StateSchemaVersion,
		RunID:                docketedRunID,
		ProductID:            "yoyodyne",
		RepositoryID:         "yoyodyne",
		WorkItemID:           docketedItem,
		WorkItemTitle:        docketedTitle,
		Backend:              "claude-code",
		Status:               runstate.StatusFailed,
		Phase:                runstate.PhaseReviewing,
		StartedAt:            completed.Add(-time.Hour),
		UpdatedAt:            completed,
		CompletedAt:          &completed,
		WorktreePath:         "/state/worktrees/task",
		Branch:               "yoyodyne/task/abc",
		BaseCommit:           strings.Repeat("a", 40),
		TargetBranch:         "main",
		RepairAttempts:       2,
		ReviewRounds:         3,
		ReviewSummary:        "the change misses the acceptance criteria",
		ReviewFindings:       1,
		ReviewFindingDetails: []runstate.Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}},
		CheckFailure:         &runstate.CheckFailure{Command: "make test", ExitCode: 1, Output: "FAIL"},
		Blocker:              "Yoyodyne stopped this item: the repair budget was spent.",
	}
}

// publishedState is a run whose approved change was published and whose
// publication the forge has not merged.
func publishedState(approvedAgo time.Duration) runstate.State {
	completed := docketedNow.Add(-approvedAgo)
	commit := strings.Repeat("b", 40)
	return runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          docketedRunID,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		WorkItemID:     docketedItem,
		Backend:        "claude-code",
		Status:         runstate.StatusSucceeded,
		Phase:          runstate.PhaseComplete,
		StartedAt:      completed.Add(-time.Hour),
		UpdatedAt:      completed,
		CompletedAt:    &completed,
		Branch:         "yoyodyne/task/abc",
		ReviewDecision: runstate.ReviewApprove,
		ReviewRounds:   1,
		PullRequest: &runstate.PullRequest{
			Remote: "origin", Branch: "yoyodyne/task/abc", Number: 42,
			URL: "https://forge.invalid/pull/42", HeadCommit: commit, State: "OPEN",
		},
	}
}

// A run that ends on a durable blocker is the event the docket exists for, and
// what it carries is the evidence somebody decides on rather than a summary of
// it.
func TestARunThatStoppedOnABlockerIsDocketedWithItsEvidence(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	created, err := docketerOver(nil, docket).RecordStoppedRun(stoppedState())
	if err != nil || !created {
		t.Fatalf("RecordStoppedRun() = %t, error = %v", created, err)
	}
	entry := docket.entries[0]
	if entry.Class != triage.ClassStoppedRun || entry.WorkItemID != docketedItem {
		t.Fatalf("entry = %#v", entry)
	}
	// An entry outlives the run it came from, so what the item is called travels
	// with it: a development manager reading the docket afterwards has the entry
	// and not the run record it was built from.
	if entry.WorkItemTitle != docketedTitle {
		t.Fatalf("title = %q, want what the run recorded the item as", entry.WorkItemTitle)
	}
	if entry.Blocker != stoppedState().Blocker {
		t.Fatalf("blocker = %q, want the words it was recorded in", entry.Blocker)
	}
	if len(entry.Findings) != 1 || entry.Findings[0].Message != "add the missing file" || entry.Findings[0].File != "feature.txt" {
		t.Fatalf("findings = %#v, want the reviewer's own words", entry.Findings)
	}
	if entry.Check == nil || entry.Check.Command != "make test" || entry.Check.ExitCode != 1 {
		t.Fatalf("check = %#v", entry.Check)
	}
	if entry.Artifacts.Branch != "yoyodyne/task/abc" || entry.Artifacts.WorktreePath != "/state/worktrees/task" {
		t.Fatalf("artifacts = %#v, want the preserved branch and worktree", entry.Artifacts)
	}
	// The counters are the half of an entry a decision is measured against: the
	// rounds and the decisions are the item's durable triage record, and the caps
	// beside each are what will refuse the next decision.
	want := triage.Counters{
		ReviewRounds: 3, ReviewRoundsCap: 4, RepairAttempts: 2, RepairGrantAttempts: 2,
		RepairGrantsCap: 1, RerunsCap: 1, MergeRearmsCap: 1,
	}
	if entry.Counters != want {
		t.Fatalf("counters = %#v, want %#v", entry.Counters, want)
	}
}

// A parked run is owed a continuation. Docketing one would put work in front of
// a development manager that nobody has to decide anything about, and every one
// of these is recorded on a run that is still running.
func TestWorkOwedAContinuationIsNeverDocketed(t *testing.T) {
	t.Parallel()

	resetsAt := docketedNow.Add(time.Hour)
	heldSince := docketedNow.Add(-time.Hour)
	for _, test := range []struct {
		name  string
		state func(runstate.State) runstate.State
	}{
		{
			name: "waiting out an exhausted usage limit",
			state: func(state runstate.State) runstate.State {
				state.UsageLimitResetsAt = &resetsAt
				state.PauseCause = runstate.PauseUsageLimit
				return state
			},
		},
		{
			name: "owed the rest of an attempt the harness stopped on time",
			state: func(state runstate.State) runstate.State {
				state.ProviderStop = runstate.ProviderStopStalled
				return state
			},
		},
		{
			name: "held up by an unresolved directive",
			state: func(state runstate.State) runstate.State {
				state.DirectivePause = &runstate.DirectivePause{DirectiveID: "d1", Kind: "question", Unresolved: "the operator has not answered"}
				return state
			},
		},
		{
			name: "waiting on unfinished work its item depends on",
			state: func(state runstate.State) runstate.State {
				state.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
				return state
			},
		},
		{
			name: "parked because the operator paused the harness",
			state: func(state runstate.State) runstate.State {
				state.OperatorHeldSince = &heldSince
				return state
			},
		},
		{
			name:  "still working",
			state: func(state runstate.State) runstate.State { return state },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// A parked run is a running run, so it carries no terminal record and
			// no blocker: the pauses above are exactly what a run stops short for.
			parked := stoppedState()
			parked.Status = runstate.StatusRunning
			parked.CompletedAt = nil
			parked.Blocker = ""
			parked = test.state(parked)
			if err := parked.Validate(); err != nil {
				t.Fatalf("the parked run is not a state the harness could record: %v", err)
			}

			docket := &memoryDocket{}
			built, err := docketerOver([]runstate.State{parked}, docket).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if built.Added != 0 || len(docket.entries) != 0 {
				t.Fatalf("a run owed a continuation was docketed: %#v", docket.entries)
			}
		})
	}
}

// Most runs end for a reason nobody has to decide about. Docketing those would
// make the docket a run log, which is a channel nobody reads.
func TestARunThatEndedWithNothingToDecideIsNotDocketed(t *testing.T) {
	t.Parallel()

	ended := stoppedState()
	ended.Blocker = ""
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{ended}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Fatalf("a run with no blocker was docketed: %#v", docket.entries)
	}
}

func TestAPublicationIsDocketedOnceItIsStuckAndNotBefore(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		state      func() runstate.State
		wantEntry  bool
		wantInText string
	}{
		{
			// Nothing has happened to it, which is what makes it stuck, so its
			// age is the only thing there is to decide on.
			name:       "approved and unmerged past the configured age",
			state:      func() runstate.State { return publishedState(3 * time.Hour) },
			wantEntry:  true,
			wantInText: "OPEN",
		},
		{
			name:      "approved and unmerged inside the configured age",
			state:     func() runstate.State { return publishedState(time.Hour) },
			wantEntry: false,
		},
		{
			// A merge the forge dropped is not going to happen with time, so it
			// is docketed the moment the harness records it rather than aged.
			name: "an outstanding publication the harness already recorded",
			state: func() runstate.State {
				state := publishedState(time.Minute)
				state.PublishFailure = "the forge dropped the queued merge of pull request 42"
				return state
			},
			wantEntry:  true,
			wantInText: "dropped the queued merge",
		},
		{
			name: "a publication the forge merged",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.PullRequest.Merged = false
				state.PullRequest.State = "MERGED"
				merged := *state.PullRequest
				merged.Merged = true
				state.PullRequest = &merged
				state.Integration = &runstate.Integration{
					TargetBranch: "main", SourceCommit: strings.Repeat("c", 40),
					TargetCommit: strings.Repeat("c", 40), PreviousTargetCommit: strings.Repeat("d", 40),
				}
				return state
			},
			wantEntry: false,
		},
		{
			// The merge happened and the publication still did not finish, which
			// is the one merged publication somebody has to look at.
			name: "a merge the harness could not confirm",
			state: func() runstate.State {
				state := publishedState(time.Minute)
				merged := *state.PullRequest
				merged.Merged = true
				merged.State = "MERGED"
				state.PullRequest = &merged
				state.Integration = &runstate.Integration{
					TargetBranch: "main", SourceCommit: strings.Repeat("c", 40),
					TargetCommit: strings.Repeat("c", 40), PreviousTargetCommit: strings.Repeat("d", 40),
				}
				state.PublishFailure = "confirm the queued merge reached main: the remote does not carry it"
				return state
			},
			wantEntry:  true,
			wantInText: "the harness could not finish the publication",
		},
		{
			// A branch nobody authorized merging is not a stuck publication; the
			// run's own blocker is what says what happened to it.
			name: "a publication no review approved",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.ReviewDecision = runstate.ReviewRepair
				return state
			},
			wantEntry: false,
		},
		{
			name: "a run that is still working on its own publication",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.Status = runstate.StatusRunning
				state.Phase = runstate.PhaseIntegrating
				state.CompletedAt = nil
				return state
			},
			wantEntry: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			docket := &memoryDocket{}
			built, err := docketerOver([]runstate.State{test.state()}, docket).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if !test.wantEntry {
				if built.Added != 0 {
					t.Fatalf("docketed a publication that is not stuck: %#v", docket.entries)
				}
				return
			}
			if built.Added != 1 || len(built.Entries) != 1 {
				t.Fatalf("build = %#v, want one publication entry", built)
			}
			entry := built.Entries[0]
			if entry.Class != triage.ClassPublication || entry.Publication == nil || entry.Publication.Number != 42 {
				t.Fatalf("entry = %#v", entry)
			}
			if entry.Counters.ReviewRoundsCap != 4 || entry.Counters.RepairGrantAttempts != 2 {
				t.Fatalf("counters = %#v, want the configured budgets beside the publication", entry.Counters)
			}
			if rendered := entry.Render(); !strings.Contains(rendered, test.wantInText) {
				t.Fatalf("entry is missing %q:\n%s", test.wantInText, rendered)
			}
		})
	}
}

// The age is measured from when the run ended, so a sweep that walks past a
// stuck publication and writes to the record cannot reset the clock on it.
func TestAPublicationsAgeIsMeasuredFromWhenItsRunEnded(t *testing.T) {
	t.Parallel()

	state := publishedState(3 * time.Hour)
	state.UpdatedAt = docketedNow.Add(-time.Minute)
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{state}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 1 {
		t.Fatalf("a touched record hid a stuck publication: %#v", built)
	}
	if !built.Entries[0].Publication.ApprovedAt.Equal(state.CompletedAt.UTC()) {
		t.Fatalf("approved at = %s, want when the run ended", built.Entries[0].Publication.ApprovedAt)
	}
}

// Scanning twice must docket once. This is what makes it safe for the sweep and
// every conversation open to build the docket, and what stops a reconcile after
// a crash from docketing a stoppage the run already docketed.
func TestBuildingTheDocketTwiceDocketsEachEventOnce(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	published := publishedState(3 * time.Hour)
	published.RunID = "run-fedcba9876543210fedcba9876543210"
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stopped, published}, docket)

	first, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if first.Added != 2 || len(first.Entries) != 2 {
		t.Fatalf("first build = %#v, want both stoppages docketed", first)
	}
	second, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if second.Added != 0 || len(second.Entries) != 2 {
		t.Fatalf("second build = %#v, want the same docket", second)
	}
	want := []string{
		triage.Key(triage.ClassStoppedRun, stopped.RunID),
		// A publication is keyed to the run and the pull request together, so what
		// the entry is about is two facts a reader can check against the record.
		triage.PublicationKey(published.RunID, published.PullRequest.Number),
	}
	if strings.Join(docket.keys(), ",") != strings.Join(want, ",") {
		t.Fatalf("docket keys = %v, want %v", docket.keys(), want)
	}
}

// One run can have stopped and left a publication nobody merged. They are two
// entries because they are two events, and one of them being docketed must not
// hide the other.
func TestOneRunCanBeDocketedForBothWhatStoppedAndWhatWasNeverMerged(t *testing.T) {
	t.Parallel()

	state := publishedState(3 * time.Hour)
	state.Status = runstate.StatusFailed
	state.Blocker = "Yoyodyne stopped this item: the publication needs a person."
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{state}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 2 {
		t.Fatalf("build = %#v, want the stoppage and the publication both docketed", built)
	}
}

// The development manager's evidence from run-effbd604, replayed: a re-run
// decided and durably recorded where the counters read, a resubmission refused
// as one of one re-runs spent, and a docket that showed no recorded decision at
// all — which is how one authorized recovery nearly got spent twice.
//
// The decision is made after the stoppage is docketed, because that is the only
// order there is: the entry is what the decision is made against.
func TestARecordedDecisionTheGuardWouldRefuseAgainShowsOnTheDocket(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	decisions, err := runstate.NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	claimed, err := runstate.NewRerunStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	docket := &memoryDocket{}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, docket, decisions, claimed)

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].Counters.Reruns != 0 {
		t.Fatalf("build = %#v, want the stoppage docketed with nothing decided about it yet", built)
	}

	// The development manager decides a re-run, which spends the item's re-run
	// budget as it is recorded and before anything acts on it.
	if _, err := decisions.RecordRerun(context.Background(), docketedItem, docketedNow, docketedCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	// The resubmission the development manager made is refused by the guard, and
	// the refusal is the proof the decision is really recorded.
	var refused runstate.TriageCapError
	_, err = decisions.RecordRerun(context.Background(), docketedItem, docketedNow, docketedCaps)
	if !errors.As(err, &refused) || refused.Spent != 1 || refused.Cap != 1 {
		t.Fatalf("second RecordRerun() error = %v, want the re-run budget refusing it", err)
	}

	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 0 || len(rebuilt.Entries) != 1 {
		t.Fatalf("build = %#v, want the same one entry", rebuilt)
	}
	entry := rebuilt.Entries[0]
	if entry.Counters.Reruns != refused.Spent || entry.Counters.RerunsCap != refused.Cap {
		t.Fatalf("counters = %#v, want the %d of %d the guard refused against", entry.Counters, refused.Spent, refused.Cap)
	}
	if entry.Counters.RerunsCarriedOut != 0 || !entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want a decision recorded and not carried out", entry.Counters)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"1 of 1 re-run(s), 0 carried out",
		"already recorded and not yet carried out",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
}

// The third recurrence of the same divergence, replayed: a repair grant the
// round cap cut from two rounds to one, reported back with that detail as it was
// recorded, and a docket entry that carried a bare count of it — so the same
// decision was made again and refused by the budget rather than by the docket it
// was read off.
//
// What the entry has to carry is every figure the guard refused against, which
// is more than the count: what the grant came to, that the cap cut it, and what
// the item now stands committed to. The rounds counted alone say three of four
// used, which reads as room the guard does not have.
func TestAGrantTheRoundCapCutShowsOnTheDocketWithWhatARepeatWouldMeet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	decisions, err := runstate.NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	claimed, err := runstate.NewRerunStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, decisions, claimed)

	// The item has cost three of the four rounds its cap permits, one for each
	// developer attempt its runs were judged on.
	for _, attempt := range []string{"attempt-1", "attempt-2", "attempt-3"} {
		if _, err := decisions.RecordReviewRound(context.Background(), docketedItem, attempt, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound(%s) error = %v", attempt, err)
		}
	}
	// The development manager decides a repair. The configured grant is two
	// rounds and the cap has room for one, so the harness records the cut and
	// says so — which is the sentence the next docket contradicted.
	granted, err := decisions.GrantRepair(context.Background(), docketedItem,
		TriageRepairGrantRounds(docketedTriage), docketedNow, docketedCaps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if !granted.Truncated || granted.Requested != 2 || granted.Rounds != 1 {
		t.Fatalf("grant = %#v, want the round cap cutting two rounds to one", granted)
	}
	// The resubmission is refused by the guard, which is the proof the decision is
	// really recorded and the round-trip the docket is meant to save.
	var refused runstate.TriageCapError
	_, err = decisions.GrantRepair(context.Background(), docketedItem,
		TriageRepairGrantRounds(docketedTriage), docketedNow, docketedCaps)
	if !errors.As(err, &refused) || refused.Spent != 1 || refused.Cap != 1 {
		t.Fatalf("second GrantRepair() error = %v, want the repair grant budget refusing it", err)
	}

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the one stoppage docketed", built)
	}
	entry := built.Entries[0]
	if entry.Counters.RepairGrants != refused.Spent || entry.Counters.RepairGrantsCap != refused.Cap {
		t.Fatalf("counters = %#v, want the %d of %d the guard refused against", entry.Counters, refused.Spent, refused.Cap)
	}
	if entry.Counters.GrantedRounds != 1 || entry.Counters.TruncatedGrants != 1 {
		t.Fatalf("counters = %#v, want the one round the cap cut the grant down to", entry.Counters)
	}
	if entry.Counters.CommittedRounds != 4 || entry.Counters.RoundsUncommitted() != 0 || !entry.Counters.Exhausted() {
		t.Fatalf("counters = %#v, want the whole cap committed, as the guard reads it", entry.Counters)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"1 of 1 repair grant(s) worth 1 review round(s), 1 of them cut down to the room the cap still had",
		"4 of 4 are committed by a grant not yet spent",
		"A further repair grant for " + docketedItem + " is refused: 1 of 1 permitted grant(s) are already recorded",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
}

// A stoppage the harness has already run again is the other half of the same
// gate: the decision is spent and so is the one re-run this entry gets, and an
// entry that still read as re-runnable is an entry somebody acts on and the
// harness refuses.
func TestAStoppageAlreadyReRunSaysSoRatherThanReadingAsAvailable(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	key := triage.Key(triage.ClassStoppedRun, stopped.RunID)
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 1}},
		claimed: map[string][]runstate.Rerun{docketedItem: {{
			DocketKey:  key,
			PriorRunID: stopped.RunID,
			WorkItemID: docketedItem,
			ClaimedAt:  docketedNow.Add(-time.Minute),
			RunID:      "run-fedcba9876543210fedcba9876543210",
		}}},
	}
	built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entry := built.Entries[0]
	if entry.Rerun == nil || entry.Rerun.RunID != "run-fedcba9876543210fedcba9876543210" {
		t.Fatalf("rerun = %#v, want the fresh run the claim started", entry.Rerun)
	}
	if entry.Counters.RerunsCarriedOut != 1 || entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want the decision recorded as carried out", entry.Counters)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "already re-run as run run-fedcba9876543210fedcba9876543210") {
		t.Fatalf("the entry does not say this stoppage was re-run:\n%s", rendered)
	}
}

// A claim taken against another stoppage of the same item is not this entry's.
// The docket key is what the guard refuses on, so it is what the view reads:
// counting the item's claims alone would show a stoppage as spent that the
// harness would run again, and matching on the item would hide the one that is.
func TestAClaimAgainstAnotherStoppageIsNotReadAsThisOnes(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 2}},
		claimed: map[string][]runstate.Rerun{docketedItem: {{
			DocketKey:  triage.Key(triage.ClassStoppedRun, "run-99999999999999999999999999999999"),
			PriorRunID: "run-99999999999999999999999999999999",
			WorkItemID: docketedItem,
			ClaimedAt:  docketedNow.Add(-time.Hour),
			RunID:      "run-fedcba9876543210fedcba9876543210",
		}}},
	}
	built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entry := built.Entries[0]
	if entry.Rerun != nil {
		t.Fatalf("rerun = %#v, want no claim against this stoppage", entry.Rerun)
	}
	if entry.Counters.RerunsCarriedOut != 1 || !entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want one of the two decisions carried out", entry.Counters)
	}
}

// A triage record that cannot be read is said out loud on the entry. Rendering
// it as zeros would describe an item nobody has decided anything about, which is
// the one reading that turns an unreadable record into a decision made twice.
func TestATriageRecordThatCannotBeReadIsStatedRatherThanShownAsNothingDecided(t *testing.T) {
	t.Parallel()

	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 1}},
	}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, recorded, recorded)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	recorded.unreadable = docketedItem
	built, err := docketer.Build()
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("Build() error = %v, want the unreadable record reported", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the entry it had docketed", built)
	}
	entry := built.Entries[0]
	if entry.CountersProblem == "" {
		t.Fatalf("entry = %#v, want the unreadable record named on it", entry)
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "Triage decisions could not be read") ||
		!strings.Contains(rendered, "read the record before deciding") {
		t.Fatalf("the entry does not say the record could not be read:\n%s", rendered)
	}
	if strings.Contains(rendered, "Triage decisions recorded") {
		t.Fatalf("an unreadable record was rendered as decisions:\n%s", rendered)
	}
}

// Reading a docket without what has been carried out would report every
// decision as still standing, which is exactly the state a reader would spend
// twice. It refuses instead.
func TestReadingTheDocketWithoutWhatWasCarriedOutRefuses(t *testing.T) {
	t.Parallel()

	docketer := docketerOver([]runstate.State{stoppedState()}, &memoryDocket{})
	docketer.Reruns = nil
	if _, err := docketer.Build(); err == nil {
		t.Fatalf("Build() read a docket without the re-runs already carried out")
	}
}

// A publication docketed before the pull request joined the key is still on the
// docket, and a build that read only the current key would docket it a second
// time. The log is append-only, so both keys are what a reader has to answer to.
func TestAPublicationDocketedUnderTheOlderKeyIsNotDocketedAgain(t *testing.T) {
	t.Parallel()

	published := publishedState(3 * time.Hour)
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{published}, docket)
	entry, err := docketer.publicationEntry(published, docketedNow)
	if err != nil {
		t.Fatalf("publicationEntry() error = %v", err)
	}
	// The key an entry made before this change carries: the run alone.
	entry.Key = triage.Key(triage.ClassPublication, published.RunID)
	if _, err := docket.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 || len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the publication left as the one entry it already is", built)
	}
}

// A docket that cannot be built in full still delivers what it found: the
// entries beside the broken one are exactly the ones somebody needs.
func TestABuildThatCannotDocketEverythingStillReturnsWhatItFound(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	published := publishedState(3 * time.Hour)
	published.RunID = "run-fedcba9876543210fedcba9876543210"
	docket := &memoryDocket{failOn: string(triage.ClassPublication)}
	built, err := docketerOver([]runstate.State{stopped, published}, docket).Build()
	if err == nil || !strings.Contains(err.Error(), "unwritable") {
		t.Fatalf("Build() error = %v, want the refusal named", err)
	}
	if built.Added != 1 || len(built.Entries) != 1 || built.Entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("build = %#v, want the stoppage it could docket", built)
	}
}

func TestADocketerWithNothingWiredRefusesRatherThanReportingAnEmptyDocket(t *testing.T) {
	t.Parallel()

	if _, err := (Docketer{}).Build(); err == nil {
		t.Fatalf("Build() reported an empty docket without one being wired")
	}
	if _, err := (Docketer{}).RecordStoppedRun(stoppedState()); err == nil {
		t.Fatalf("RecordStoppedRun() reported a docketed run without a docket")
	}
}

// The whole point of the docket is that it arrives without an operator
// carrying it: the run that stops on a blocker is what puts itself on it,
// carrying the evidence it recorded on the way.
func TestARunThatSpendsItsRepairBudgetDocketsItselfAsItStops(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, runErr := pipeline.Run(context.Background(), tracker.item.ID)
	if runErr == nil || !outcome.Blocked {
		t.Fatalf("Run() error = %v, blocked = %t, want a run that spent its budget", runErr, outcome.Blocked)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the stopped run", entries)
	}
	entry := entries[0]
	if entry.Class != triage.ClassStoppedRun || entry.RunID != outcome.RunID || entry.WorkItemID != tracker.item.ID {
		t.Fatalf("entry = %#v", entry)
	}
	// The blocker on the entry is the blocker on the item, in the same words:
	// an entry that paraphrased it would be a second account of one stoppage.
	if entry.Blocker != strings.TrimRight(tracker.blockReason, "\n") {
		t.Fatalf("docketed blocker is not the one recorded on the item:\n%s\n---\n%s", entry.Blocker, tracker.blockReason)
	}
	if len(entry.Findings) != 1 || entry.Findings[0].Message != "add the missing file" {
		t.Fatalf("findings = %#v, want the reviewer's own words", entry.Findings)
	}
	if entry.Artifacts.WorktreePath != outcome.WorktreePath || entry.Artifacts.Branch != outcome.Branch {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entry.Artifacts)
	}
	// Two verdicts were obtained — the first one and the one after the repair —
	// against a cap of four, and that is what a grant would be decided against.
	// The rounds are the item's own triage record rather than a second count made
	// from the runs, because that record is what the grant is truncated against.
	want := triage.Counters{
		ReviewRounds: 2, ReviewRoundsCap: 4, RepairAttempts: 1, RepairGrantAttempts: 2,
		RepairGrantsCap: 1, RerunsCap: 1, MergeRearmsCap: 2,
	}
	if entry.Counters != want {
		t.Fatalf("counters = %#v, want %#v", entry.Counters, want)
	}
	// The rounds are durable, so the next run of this item is measured against
	// what the last one already spent rather than starting the argument over.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewRounds != 2 || state.Blocker == "" {
		t.Fatalf("state = %#v, want the rounds and the blocker recorded", state)
	}
}

// A run whose process died never docketed itself. The sweep that settles it is
// what dockets the stoppage, and doing that twice — the run and then the sweep,
// or two sweeps — must leave one entry.
func TestASweepDocketsARunItStopsAndNeverDocketsItTwice(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseReviewing}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}

	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	docketer := docketerOverStore(docket, store, pipeline.Config)
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketer,
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked", results)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != pipelineRunID {
		t.Fatalf("docket = %#v, want the run the sweep stopped", entries)
	}
	if !strings.Contains(entries[0].Blocker, "No developer was restarted") {
		t.Fatalf("docketed blocker is not the one the sweep recorded:\n%s", entries[0].Blocker)
	}
	if entries[0].Artifacts.WorktreePath == "" || entries[0].Artifacts.Branch == "" {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entries[0].Artifacts)
	}

	// A later scan finds the same stoppage on the same run and dockets nothing:
	// the entry is keyed to the stoppage rather than to the noticing.
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 || len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the one entry already docketed", built)
	}
}

// A stoppage that could not be docketed is a delivery that did not happen, not
// a run that has to be settled again. Reporting it as a failed settlement would
// describe a settled run as outstanding, and nothing would ever correct that.
func TestASweepThatCannotDocketStillSettlesTheRun(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseReviewing}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}

	docket := &memoryDocket{failOn: string(triage.ClassStoppedRun)}
	results, err := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
	}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled as blocked", results)
	}
	if !strings.Contains(results[0].DocketProblem, "unwritable") {
		t.Fatalf("docket problem = %q, want the refusal named", results[0].DocketProblem)
	}
	// The blocker is durable on the run, so the delivery that failed here is
	// made by the next build rather than lost with the sweep.
	settled, err := store.Load(results[0].RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Blocker == "" {
		t.Fatalf("settled run = %#v, want the blocker recorded on it", settled)
	}
}
