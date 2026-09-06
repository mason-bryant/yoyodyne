package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestCleanupFailedCreateRemovesPartialState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "partial.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	cause := errors.New("sync failed")
	if err := cleanupFailedCreate(file, path, cause); !errors.Is(err, cause) {
		t.Fatalf("cleanupFailedCreate() error = %v, want cause", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial state still exists: %v", err)
	}
}

func TestStoreLifecycleAndIncompleteDiscovery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	running := testState(t, StatusRunning)
	if err := store.Create(running); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Create(running); err == nil {
		t.Fatal("second Create() error = nil")
	}

	loaded, err := store.Load(running.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.WorkItemID != running.WorkItemID {
		t.Fatalf("Load() work item = %q", loaded.WorkItemID)
	}

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].RunID != running.RunID {
		t.Fatalf("Incomplete() = %#v", incomplete)
	}

	completed := running
	completed.Status = StatusSucceeded
	completed.UpdatedAt = completed.UpdatedAt.Add(time.Second)
	completedAt := completed.UpdatedAt
	completed.CompletedAt = &completedAt
	if err := store.Save(completed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	incomplete, err = store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %#v, want empty", incomplete)
	}
}

func TestStoreReserveEnforcesCapacityAtomicallyAcrossInstances(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() first error = %v", err)
	}
	second, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() second error = %v", err)
	}
	firstState := testState(t, StatusPending)
	secondState := testState(t, StatusPending)
	secondState.WorkItemID = "yoyodyne-other"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	for _, reservation := range []struct {
		store *Store
		state State
	}{{first, firstState}, {second, secondState}} {
		reservation := reservation
		go func() {
			<-start
			lease, err := reservation.store.Reserve(ctx, reservation.state, 1)
			// The winner keeps its lease for the length of the run; releasing
			// it here is what lets the rest of the test act on the reservation.
			errorsByCall <- errors.Join(err, lease.Release())
		}()
	}
	close(start)
	var succeeded, capacityRejected int
	for range 2 {
		err := <-errorsByCall
		if err == nil {
			succeeded++
			continue
		}
		var capacity CapacityError
		if errors.As(err, &capacity) {
			capacityRejected++
			continue
		}
		t.Fatalf("Reserve() unexpected error = %v", err)
	}
	if succeeded != 1 || capacityRejected != 1 {
		t.Fatalf("Reserve() succeeded = %d, capacity rejected = %d", succeeded, capacityRejected)
	}
	active, err := first.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("Incomplete() = %#v, want exactly one reservation", active)
	}
}

// The size guard is asked with the review summary rather than the failure, which
// the schema now bounds itself: a field the schema refuses never reaches the
// encoder, and what is being measured here is the encoder rather than the
// schema.
func TestStoreRejectsStateItsReaderCannotLoad(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.ReviewSummary = strings.Repeat("x", maxEncodedStateBytes)
	if err := store.Create(state); err == nil || !strings.Contains(err.Error(), "encoded run state is") {
		t.Fatalf("Create() oversized state error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized create left a state file: %v", err)
	}

	state.ReviewSummary = ""
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() valid state error = %v", err)
	}
	state.ReviewSummary = strings.Repeat("x", maxEncodedStateBytes)
	if err := store.Save(state); err == nil || !strings.Contains(err.Error(), "encoded run state is") {
		t.Fatalf("Save() oversized state error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() original state error = %v", err)
	}
	if loaded.ReviewSummary != "" {
		t.Fatalf("oversized save replaced original state: review summary bytes = %d", len(loaded.ReviewSummary))
	}
}

// The bound on the recorded failure is the schema's, not a downstream reader's:
// a reason too long to carry is cut where it is written, and a record that
// somehow reaches the store uncut is refused by name rather than silently kept
// for every reader after it to discover.
func TestAnOversizedFailureIsRefusedBySchemaAndCutAtTheWrite(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusFailed)
	state.Failure = strings.Repeat("x", MaxBlockerBytes+1)
	err := state.Validate()
	if err == nil || !strings.Contains(err.Error(), "failure is") {
		t.Fatalf("Validate() oversized failure error = %v, want the failure named", err)
	}
	// Written the way the harness writes it, the same reason validates and still
	// says what stopped the run.
	state.Failure = RecordFailure("the provider failed: " + strings.Repeat("x", MaxBlockerBytes))
	if err := state.Validate(); err != nil {
		t.Fatalf("a recorded failure does not validate: %v", err)
	}
	if !strings.HasPrefix(state.Failure, "the provider failed: ") ||
		!strings.HasSuffix(state.Failure, "the rest of this failure was not recorded]") {
		t.Fatalf("a cut failure lost its head or did not say it was cut: %q", state.Failure)
	}
}

// A save the durable schema refuses names the field it refused, and leaves the
// record exactly as it was. Both halves are the contract a caller acts on: the
// process now holds a state nothing durable carries, so it is the only thing that
// can still say so, and the name of the field is the whole of what it has to go
// on. Everything that reads the record after that process exits reads what is
// here rather than what the process believed.
func TestARefusedSaveNamesTheFieldAndLeavesTheRecordAsItWas(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.ReviewDecision = ReviewRepair
	state.ReviewFindings = 1
	state.ReviewFindingDetails = []Finding{{Severity: "advisory", Message: "this reads oddly"}}
	err := store.Save(state)
	want := `review_finding_details[0]: severity "advisory" must be "blocker", "major", or "minor"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Save() refused state error = %v, want %q named", err, want)
	}
	// The refusal is classified as well as worded. A caller has to be able to tell
	// it from a store that could not be written, because only one of the two is
	// still true the next time anything tries: an unavailable store leaves an
	// interrupted run something comes back for, and this leaves a record no later
	// write ever catches up with.
	var refused RefusedStateError
	if !errors.As(err, &refused) {
		t.Fatalf("Save() error = %v, want a refusal a caller can tell from an unavailable store", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ReviewDecision != "" || len(loaded.ReviewFindingDetails) != 0 {
		t.Fatalf("the refused save reached the record: %#v", loaded)
	}
}

func TestStoreRejectsStateFromAnotherRunFile(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	otherRunID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	source, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() source error = %v", err)
	}
	target, err := store.statePath(otherRunID)
	if err != nil {
		t.Fatalf("statePath() target error = %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(otherRunID); err == nil || !strings.Contains(err.Error(), "belongs to run") {
		t.Fatalf("Load() mismatched run error = %v", err)
	}
}

func TestStoreEventsAndMalformedDiscovery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	event, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventRunStarted, "harness", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	events, err := store.LoadEvents(state.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != execution.EventRunStarted {
		t.Fatalf("LoadEvents() = %#v", events)
	}

	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("malformed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.LoadEvents(state.RunID); err == nil {
		t.Fatal("LoadEvents() malformed error = nil")
	}
}

func TestStoreRejectsEventsItsReaderCannotLoad(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	baseEvent, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": "",
	})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	baseEncoded, err := encodeEvent(baseEvent)
	if err != nil {
		t.Fatalf("encodeEvent() error = %v", err)
	}
	maxTextBytes := maxEncodedEventBytes - len(baseEncoded)
	boundaryEvent, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": strings.Repeat("x", maxTextBytes),
	})
	if err != nil {
		t.Fatalf("NewEvent() boundary error = %v", err)
	}
	if err := store.AppendEvent(boundaryEvent); err != nil {
		t.Fatalf("AppendEvent() boundary error = %v", err)
	}
	if events, err := store.LoadEvents(state.RunID); err != nil || len(events) != 1 {
		t.Fatalf("LoadEvents() boundary = %d events, error %v", len(events), err)
	}

	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() before oversized append error = %v", err)
	}
	oversizedEvent, err := execution.NewEvent(state.RunID, 2, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": strings.Repeat("x", maxTextBytes+1),
	})
	if err != nil {
		t.Fatalf("NewEvent() oversized error = %v", err)
	}
	if err := store.AppendEvent(oversizedEvent); err == nil || !strings.Contains(err.Error(), "encoded event is") {
		t.Fatalf("AppendEvent() oversized error = %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() after oversized append error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("oversized append changed event log size from %d to %d", before.Size(), after.Size())
	}
}

func TestStoreDoesNotPersistEnvironmentSecrets(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), "OPENAI_API_KEY") || strings.Contains(string(data), "ANTHROPIC_API_KEY") {
		t.Fatalf("state file contains credential environment names: %s", data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, exists := decoded["environment"]; exists {
		t.Fatal("state file contains environment")
	}
}

func TestStateRequiresCompleteWorktreeIdentity(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	state.WorktreePath = "/state/worktree"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "recorded together") {
		t.Fatalf("Validate() incomplete worktree error = %v", err)
	}
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() complete worktree error = %v", err)
	}
}

// A run may name no build, because every run recorded before the harness pinned
// one names none and because a binary carrying no revision of its own leaves it
// empty. What it may not do is name something no repository could resolve: the
// whole use of the field is that a reader hands it to Git, and a value that
// could never be one reads as an answer to "which harness did this" while being
// nothing of the kind.
func TestARunRecordsABuildThatCouldBeOneOrNoneAtAll(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() with no build error = %v", err)
	}
	state.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() with a revision error = %v", err)
	}
	state.Build = "the one from Tuesday"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "build is not a revision") {
		t.Fatalf("Validate() invented build error = %v", err)
	}
}

func TestStateRequiresCoherentReviewAndIntegrationEvidence(t *testing.T) {
	t.Parallel()

	approved := func(t *testing.T) State {
		t.Helper()
		state := testState(t, StatusRunning)
		state.WorktreePath = "/state/worktree"
		state.Branch = "yoyodyne/task/01234567"
		state.BaseCommit = strings.Repeat("a", 40)
		state.Phase = PhaseIntegrating
		state.ProviderSessionID = "developer-session"
		state.ProviderModel = "opus"
		state.ReviewSessionID = "reviewer-session"
		state.ReviewModel = "opus"
		state.ReviewDecision = ReviewApprove
		return state
	}
	integration := Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("b", 40),
		TargetCommit:         strings.Repeat("b", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}

	for _, test := range []struct {
		name    string
		mutate  func(*State)
		problem string
	}{
		{name: "approved and integrated", mutate: func(state *State) { state.Integration = &integration }},
		{
			name:    "invalid phase",
			mutate:  func(state *State) { state.Phase = "reticulating" },
			problem: "phase is invalid",
		},
		{
			name:    "invalid decision",
			mutate:  func(state *State) { state.ReviewDecision = "maybe" },
			problem: "review_decision is invalid",
		},
		{
			name: "finding with no severity the developer can act on",
			mutate: func(state *State) {
				state.ReviewFindings = 1
				state.ReviewFindingDetails = []Finding{{Message: "fix it"}}
			},
			problem: `review_finding_details[0]: severity "" must be`,
		},
		{
			name: "finding with nothing to act on",
			mutate: func(state *State) {
				state.ReviewFindings = 1
				state.ReviewFindingDetails = []Finding{{Severity: SeverityBlocker}}
			},
			problem: "review_finding_details[0]: message is required",
		},
		{
			name: "finding anchored to a line in no file",
			mutate: func(state *State) {
				state.ReviewFindings = 1
				state.ReviewFindingDetails = []Finding{{Severity: SeverityMajor, Message: "fix it", Line: 4}}
			},
			problem: "review_finding_details[0]: line requires a file",
		},
		{
			name: "a count that contradicts the findings it counts",
			mutate: func(state *State) {
				state.ReviewFindings = 3
				state.ReviewFindingDetails = []Finding{{Severity: SeverityMinor, Message: "fix it"}}
			},
			problem: "review_findings is 3 but 1 review_finding_details are recorded",
		},
		{
			name:    "negative repair attempts",
			mutate:  func(state *State) { state.RepairAttempts = -1 },
			problem: "repair_attempts cannot be negative",
		},
		{
			name:    "negative transient relaunches",
			mutate:  func(state *State) { state.TransientRelaunches = -1 },
			problem: "transient_relaunches cannot be negative",
		},
		{
			name:    "a failing check the developer cannot re-run",
			mutate:  func(state *State) { state.CheckFailure = &CheckFailure{ExitCode: 3} },
			problem: "check_failure: command is required",
		},
		{
			name: "a failing check that carries more output than the bound allows",
			mutate: func(state *State) {
				state.CheckFailure = &CheckFailure{Command: "go test ./...", ExitCode: 1, Output: strings.Repeat("x", MaxCheckOutputBytes+1)}
			},
			problem: "check_failure: output is",
		},
		{
			name: "integration alongside a check that still fails",
			mutate: func(state *State) {
				state.CheckFailure = &CheckFailure{Command: "go test ./...", ExitCode: 1}
				state.Integration = &integration
			},
			problem: "integration requires no recorded failing check",
		},
		{
			name:    "a refusal that names no path",
			mutate:  func(state *State) { state.PathRefusal = &PathRefusal{Grants: []string{"docs/designs"}} },
			problem: "path_refusal: at least one refused path is required",
		},
		{
			name: "a refusal that carries more paths than the bound allows",
			mutate: func(state *State) {
				paths := make([]string, MaxRefusedPaths+1)
				for index := range paths {
					paths[index] = "docs/product/goal.md"
				}
				state.PathRefusal = &PathRefusal{Paths: paths}
			},
			problem: "path_refusal: 51 refused paths are recorded",
		},
		{
			name: "integration alongside a path the gate still refuses",
			mutate: func(state *State) {
				state.PathRefusal = &PathRefusal{Paths: []string{"docs/product/brief.md"}}
				state.Integration = &integration
			},
			problem: "integration requires no recorded protected-path refusal",
		},
		{
			name:    "integration target that is not a local branch",
			mutate:  func(state *State) { state.TargetBranch = "refs/heads/main" },
			problem: "target_branch must be a local branch name",
		},
		{
			name: "integration into a branch the run was not written against",
			mutate: func(state *State) {
				state.TargetBranch = "release"
				state.Integration = &integration
			},
			problem: `integration target_branch "main" does not match the recorded target_branch "release"`,
		},
		{
			name: "integration without an approval",
			mutate: func(state *State) {
				state.ReviewDecision = ReviewRepair
				state.Integration = &integration
			},
			problem: "integration requires an approving review decision",
		},
		{
			name: "integration recorded before the integrating phase",
			mutate: func(state *State) {
				state.Phase = PhaseReviewing
				state.Integration = &integration
			},
			problem: "integration requires the integrating phase or later",
		},
		{
			name: "integration onto a fully qualified ref",
			mutate: func(state *State) {
				qualified := integration
				qualified.TargetBranch = "refs/heads/main"
				state.Integration = &qualified
			},
			problem: "integration target_branch must be a local branch name",
		},
		{
			name: "integration onto HEAD",
			mutate: func(state *State) {
				detached := integration
				detached.TargetBranch = "HEAD"
				state.Integration = &detached
			},
			problem: "integration target_branch must be a local branch name",
		},
		{
			name: "integration that did not move the target",
			mutate: func(state *State) {
				stalled := integration
				stalled.PreviousTargetCommit = stalled.SourceCommit
				state.Integration = &stalled
			},
			problem: "integration did not move the target",
		},
		{
			name: "integration with an invalid commit",
			mutate: func(state *State) {
				malformed := integration
				malformed.TargetCommit = "HEAD"
				state.Integration = &malformed
			},
			problem: "integration target_commit is invalid",
		},
		{
			// Integration is fast-forward only, so any other pair of commits
			// describes something this harness never does.
			name: "integration whose target is not the source commit",
			mutate: func(state *State) {
				diverged := integration
				diverged.TargetCommit = strings.Repeat("c", 40)
				state.Integration = &diverged
			},
			problem: "target_commit must equal the fast-forwarded source_commit",
		},
		{
			name: "integration without a developer session",
			mutate: func(state *State) {
				state.ProviderSessionID = ""
				state.Integration = &integration
			},
			problem: "requires recorded developer and reviewer session identifiers",
		},
		{
			name: "integration with a reused session",
			mutate: func(state *State) {
				state.ReviewSessionID = state.ProviderSessionID
				state.Integration = &integration
			},
			problem: "requires distinct developer and reviewer session identifiers",
		},
		{
			// Two identifiers that differ only in surrounding whitespace are one
			// session, and must not read as an independent second invocation.
			name: "integration with a whitespace-variant session",
			mutate: func(state *State) {
				state.ReviewSessionID = "  " + state.ProviderSessionID + "\t"
				state.Integration = &integration
			},
			problem: "requires distinct developer and reviewer session identifiers",
		},
		{
			name: "complete phase with cleanup outstanding",
			mutate: func(state *State) {
				state.Integration = &integration
				state.Phase = PhaseComplete
				state.WorktreeRemoved = true
			},
			problem: "complete phase requires both the worktree and branch to be removed",
		},
		{
			name: "integration without a reviewer model selector",
			mutate: func(state *State) {
				state.ReviewModel = ""
				state.Integration = &integration
			},
			problem: "requires recorded developer and reviewer model selectors",
		},
		{
			name:    "worktree removal claimed without integration",
			mutate:  func(state *State) { state.Integration = nil; state.WorktreeRemoved = true },
			problem: "removed artifacts require recorded integration",
		},
		{
			name:    "branch removal claimed without integration",
			mutate:  func(state *State) { state.Integration = nil; state.BranchRemoved = true },
			problem: "removed artifacts require recorded integration",
		},
		{
			// The second way a removal is earned: this run promoted nothing, and
			// the run that superseded it is what its artifacts were retired for.
			name: "removal earned by the run that superseded this one",
			mutate: func(state *State) {
				state.Integration = nil
				state.WorktreeRemoved = true
				state.BranchRemoved = true
				state.ArtifactsRetiredBy = "run-fedcba9876543210fedcba9876543210"
			},
		},
		{
			name: "a retirement that removed nothing",
			mutate: func(state *State) {
				state.ArtifactsRetiredBy = "run-fedcba9876543210fedcba9876543210"
			},
			problem: "names what it removed",
		},
		{
			// The third way a removal is earned: the convergence sweep retired an
			// old stoppage's empty checkout to keep the machine's worktree
			// registrations bounded.
			name: "checkout removal earned by the convergence sweep",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeRemoved = true
				state.WorktreeSweptAt = &swept
			},
		},
		{
			// It earns the checkout and nothing else, because the sweep never
			// touches a branch.
			name: "a branch removal claimed on the checkout sweep",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeRemoved = true
				state.BranchRemoved = true
				state.WorktreeSweptAt = &swept
			},
			problem: "removed artifacts require recorded integration",
		},
		{
			// The fourth way a removal is earned: the same sweep deleted a branch
			// whose work the target provably carries. Recording it is what stops the
			// run going on reading as one whose change is preserved on a branch that
			// is gone.
			name: "branch removal earned by the convergence sweep",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.BranchRemoved = true
				state.BranchSweptAt = &swept
			},
		},
		{
			// It earns the branch and nothing else, because the branch sweep never
			// touches a checkout.
			name: "a checkout removal claimed on the branch sweep",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeRemoved = true
				state.BranchRemoved = true
				state.BranchSweptAt = &swept
			},
			problem: "removed artifacts require recorded integration",
		},
		{
			name: "a branch sweep that deleted nothing",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.BranchSweptAt = &swept
			},
			problem: "a recorded branch sweep names a branch that was deleted",
		},
		{
			name: "a checkout sweep that removed nothing",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeSweptAt = &swept
			},
			problem: "a recorded checkout sweep names a checkout that was removed",
		},
		{
			// Where the retired checkout's uncommitted work went, which is the only
			// record connecting that ref back to the item it belonged to.
			name: "a checkout sweep that preserved the work in it",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeRemoved = true
				state.WorktreeSweptAt = &swept
				state.PreservedWorkRef = "refs/yoyodyne/preserved-work/" + state.RunID
			},
		},
		{
			name: "preserved work with no sweep that recorded it",
			mutate: func(state *State) {
				state.Integration = nil
				state.PreservedWorkRef = "refs/yoyodyne/preserved-work/" + state.RunID
			},
			problem: "preserved_work_ref requires the checkout sweep that recorded it",
		},
		{
			// A branch would be swept by the branch sweep and answer the containment
			// proofs the harness makes about run branches, which is the whole reason
			// the capture is kept out of refs/heads.
			name: "preserved work recorded as a branch",
			mutate: func(state *State) {
				swept := state.UpdatedAt
				state.Integration = nil
				state.WorktreeRemoved = true
				state.WorktreeSweptAt = &swept
				state.PreservedWorkRef = "refs/heads/" + state.RunID
			},
			problem: "must be a ref outside refs/heads",
		},
		{
			name: "a run recorded as superseding itself",
			mutate: func(state *State) {
				state.Integration = nil
				state.WorktreeRemoved = true
				state.ArtifactsRetiredBy = state.RunID
			},
			problem: "does not supersede itself",
		},
		{
			name: "a retirement by something that is not a run",
			mutate: func(state *State) {
				state.Integration = nil
				state.BranchRemoved = true
				state.ArtifactsRetiredBy = "somebody"
			},
			problem: "is not a run identifier",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := approved(t)
			test.mutate(&state)
			err := state.Validate()
			if test.problem == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("Validate() error = %v, want %q", err, test.problem)
			}
		})
	}
}

func TestStoreRoundTripsReviewAndIntegrationEvidence(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.Phase = PhaseCleaningUp
	// A partial cleanup is the state most worth proving survives a round trip.
	state.WorktreeRemoved = true
	state.CleanupFailure = "delete integrated branch failed"
	state.ProviderSessionID = "developer-session"
	state.ProviderModel = "opus"
	state.ProviderResolvedModel = "claude-opus-5"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewModel = "opus"
	state.ReviewResolvedModel = "claude-opus-5"
	state.ReviewDecision = ReviewApprove
	state.ReviewSummary = "matches the acceptance criteria"
	state.ReviewFindings = 2
	state.ReviewFindingDetails = []Finding{
		{Severity: SeverityMinor, Message: "the comment above this is stale", File: "internal/run.go", Line: 12},
		{Severity: SeverityMinor, Message: "this name reads as a verb"},
	}
	state.RepairAttempts = 1
	// The relaunch budget is durable so that a crash cannot refill it, which is
	// worth nothing if the count does not survive the file it is written to.
	state.TransientRelaunches = 2
	state.TargetBranch = "main"
	state.Integration = &Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("b", 40),
		TargetCommit:         strings.Repeat("b", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, state) {
		t.Fatalf("Load() = %#v, want %#v", loaded, state)
	}
}

// TestStoreRoundTripsTheFailingCheckARepairAttemptWasHanded proves the durable
// repair input survives the process that recorded it: an interrupted attempt is
// reissued from exactly this, so a command, exit code, or output lost here is an
// attempt that cannot be rebuilt.
func TestStoreRoundTripsTheFailingCheckARepairAttemptWasHanded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Phase = PhaseDeveloping
	state.ProviderSessionID = "developer-session"
	state.RepairAttempts = 1
	state.CheckFailure = &CheckFailure{
		Command:  "go test ./...",
		ExitCode: 1,
		Output:   "--- FAIL: TestThing\n    thing_test.go:12: got 2, want 3\nFAIL",
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, state) {
		t.Fatalf("Load() = %#v, want %#v", loaded, state)
	}
}

// TestStoreRoundTripsTheProtectedPathsARefusedAttemptWasHanded proves the same
// for the gate in front of the checks. It matters more here than it does for a
// check: a resumed run rebuilds the attempt from this rather than by re-reading
// the worktree, and nothing reconstructs it for a reader afterwards, because the
// worktree it describes is removed when the run is cleaned up.
func TestStoreRoundTripsTheProtectedPathsARefusedAttemptWasHanded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Phase = PhaseDeveloping
	state.ProviderSessionID = "developer-session"
	state.RepairAttempts = 1
	state.PathRefusal = &PathRefusal{
		Paths:   []string{".yoyodyne/config.yaml", "docs/product/brief.md"},
		Omitted: 2,
		Grants:  []string{"docs/designs/v1-harness-design.md"},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, state) {
		t.Fatalf("Load() = %#v, want %#v", loaded, state)
	}
}

// TestStoreLoadsStateWrittenBeforeTheRepairLoopExisted holds the schema to the
// only bar that matters: a file the previous release actually wrote still
// loads. The document below is the shape found in a real run state directory,
// including `review_findings` as the integer count it has always been, and the
// scans that run before every run have to survive it.
func TestStoreLoadsStateWrittenBeforeTheRepairLoopExisted(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runID := "run-22ce30530a5cd7ac66414f2095dcede1"
	existing := `{
  "schema_version": 1,
  "run_id": "` + runID + `",
  "product_id": "yoyodyne",
  "repository_id": "yoyodyne",
  "work_item_id": "yoyodyne-ifd.2.5",
  "backend": "claude-code",
  "provider_session_id": "developer-session",
  "provider_model": "opus",
  "provider_resolved_model": "claude-opus-5",
  "status": "succeeded",
  "phase": "complete",
  "last_sequence": 128,
  "started_at": "2026-08-15T22:47:57.459654Z",
  "updated_at": "2026-08-15T22:50:45.953777Z",
  "completed_at": "2026-08-15T22:50:45.836592Z",
  "worktree_path": "/state/worktrees/yoyodyne-ifd-2-5-22ce3053",
  "branch": "yoyodyne/yoyodyne-ifd-2-5/22ce3053",
  "base_commit": "d5cac8a79bda4dd90146b1d4283812469c55bf03",
  "worktree_removed": true,
  "branch_removed": true,
  "review_session_id": "reviewer-session",
  "review_model": "opus",
  "review_resolved_model": "claude-opus-5",
  "review_decision": "approve",
  "review_summary": "matches the acceptance criteria",
  "review_findings": 2,
  "integration": {
    "target_branch": "main",
    "source_commit": "34533d24ab1cdaa677219d7d582332e2c55ace2d",
    "target_commit": "34533d24ab1cdaa677219d7d582332e2c55ace2d",
    "previous_target_commit": "d5cac8a79bda4dd90146b1d4283812469c55bf03"
  }
}
`
	if err := os.WriteFile(filepath.Join(store.Root(), runID+".json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ReviewFindings != 2 || len(loaded.ReviewFindingDetails) != 0 || loaded.CheckFailure != nil {
		t.Fatalf("Load() repair evidence = %d counted, %#v recorded, check %#v", loaded.ReviewFindings, loaded.ReviewFindingDetails, loaded.CheckFailure)
	}
	// Every run scans this directory before it does anything, so a file it
	// cannot read would stop every later run rather than only its own.
	if _, err := store.Incomplete(); err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lease, err := store.Reserve(ctx, testState(t, StatusPending), 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, _, err := store.Adopt(ctx, "yoyodyne-ifd.2.5"); !errors.Is(err, ErrNoRunInFlight) {
		t.Fatalf("Adopt() of a completed run error = %v, want ErrNoRunInFlight", err)
	}
}

// TestStoreAdoptGivesOneHolderTheRunInFlight proves the resume path is as
// exclusive as the reservation path: a second caller is refused while the first
// still holds the run, and gets it once the holder lets go.
func TestStoreAdoptGivesOneHolderTheRunInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() first error = %v", err)
	}
	second, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() second error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state := testState(t, StatusPending)
	reservation, err := first.Reserve(ctx, state, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	// The reserving process still holds its run, so nothing else may enter it.
	var existing ExistingWorkItemError
	if _, _, err := second.Adopt(ctx, state.WorkItemID); !errors.As(err, &existing) {
		t.Fatalf("Adopt() of a held run error = %v, want ExistingWorkItemError", err)
	}
	if existing.State.RunID != state.RunID {
		t.Fatalf("Adopt() reported run %q, want %q", existing.State.RunID, state.RunID)
	}
	if err := reservation.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	// Once the holder is gone the run is adoptable, and adopting it holds it in
	// exactly the same way.
	adopted, lease, err := second.Adopt(ctx, state.WorkItemID)
	if err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	if adopted.RunID != state.RunID {
		t.Fatalf("Adopt() = %q, want %q", adopted.RunID, state.RunID)
	}
	if _, _, err := first.Adopt(ctx, state.WorkItemID); !errors.As(err, &existing) {
		t.Fatalf("Adopt() of an adopted run error = %v, want ExistingWorkItemError", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// Releasing twice is a no-op so a caller can defer it unconditionally.
	if err := lease.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}

// A lease can keep answering "held" for a moment after its holder is gone,
// because the lock belongs to the open file description and a process forked
// while the lease was open shares it until that child execs. The harness forks
// Git and check processes constantly, so believing that answer would refuse to
// resume a run whose process no longer exists. Adoption waits it out instead.
func TestAdoptWaitsOutALeaseNobodyHoldsAnyMore(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.leasePath(state.RunID)
	if err != nil {
		t.Fatalf("leasePath() error = %v", err)
	}
	// A second descriptor on the lease file stands in for the one a forked
	// child briefly shares: the lock is real while it is open and gone when it
	// is closed, which is exactly the window being waited out.
	inherited, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	held, err := tryLockStateFile(inherited)
	if err != nil || !held {
		t.Fatalf("tryLockStateFile() = %t, %v", held, err)
	}
	go func() {
		time.Sleep(leaseGrace / 5)
		inherited.Close()
	}()

	adopted, lease, err := store.Adopt(ctx, state.WorkItemID)
	if err != nil {
		t.Fatalf("Adopt() error = %v, want the run adopted once the phantom lock went away", err)
	}
	if adopted.RunID != state.RunID {
		t.Fatalf("Adopt() = %q, want %q", adopted.RunID, state.RunID)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A lock that outlasts the grace belongs to somebody. Adoption reports that
// rather than queueing behind a holder that may work for minutes.
func TestAdoptRefusesALeaseThatOutlastsTheGrace(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.leasePath(state.RunID)
	if err != nil {
		t.Fatalf("leasePath() error = %v", err)
	}
	holder, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer holder.Close()
	if held, err := tryLockStateFile(holder); err != nil || !held {
		t.Fatalf("tryLockStateFile() = %t, %v", held, err)
	}

	started := time.Now()
	var existing ExistingWorkItemError
	if _, _, err := store.Adopt(ctx, state.WorkItemID); !errors.As(err, &existing) {
		t.Fatalf("Adopt() error = %v, want ExistingWorkItemError", err)
	}
	if waited := time.Since(started); waited < leaseGrace {
		t.Fatalf("Adopt() refused after %s, want at least the %s grace", waited, leaseGrace)
	}
	if _, _, err := store.AdoptRun(ctx, state.RunID); !errors.Is(err, ErrRunHeld) {
		t.Fatalf("AdoptRun() error = %v, want ErrRunHeld", err)
	}
}

// A run that reached a terminal status can still owe the cleanup its
// integration scheduled. Reconciliation has to find those, and nothing else: a
// terminal run with nothing integrated is finished, and its artifacts are
// preserved deliberately.
func TestOutstandingFindsTerminalRunsThatStillOweCleanup(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	live := testState(t, StatusRunning)
	preserved := testState(t, StatusFailed)
	preserved.WorkItemID = "yoyodyne-preserved"
	preserved.Phase = PhaseDeveloping
	uncleaned := integratedState(t, PhaseCleaningUp)
	finished := integratedState(t, PhaseComplete)
	finished.WorktreeRemoved = true
	finished.BranchRemoved = true
	for _, state := range []State{live, preserved, uncleaned, finished} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create(%s) error = %v", state.RunID, err)
		}
	}

	outstanding, err := store.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	found := make(map[string]bool, len(outstanding))
	for _, state := range outstanding {
		found[state.RunID] = true
	}
	if len(found) != 2 || !found[live.RunID] || !found[uncleaned.RunID] {
		t.Fatalf("Outstanding() = %#v, want the live run and the uncleaned one", outstanding)
	}
	// The incomplete set stays what it was: the uncleaned run is new to
	// Outstanding precisely because it is already terminal.
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].RunID != live.RunID {
		t.Fatalf("Incomplete() = %#v, want only the live run", incomplete)
	}
}

// AdoptRun is how reconciliation enters a run no work item lookup would find.
// It has to hold that run as exclusively as a reservation does.
func TestAdoptRunHoldsATerminalRunThatStillOwesCleanup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() first error = %v", err)
	}
	second, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() second error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state := integratedState(t, PhaseCleaningUp)
	if err := first.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The run is terminal, so the work item path cannot reach it at all.
	if _, _, err := first.Adopt(ctx, state.WorkItemID); !errors.Is(err, ErrNoRunInFlight) {
		t.Fatalf("Adopt() error = %v, want ErrNoRunInFlight", err)
	}

	adopted, lease, err := first.AdoptRun(ctx, state.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	if adopted.RunID != state.RunID || adopted.Integration == nil {
		t.Fatalf("AdoptRun() = %#v, want the recorded run", adopted)
	}
	if _, _, err := second.AdoptRun(ctx, state.RunID); !errors.Is(err, ErrRunHeld) {
		t.Fatalf("AdoptRun() of a held run error = %v, want ErrRunHeld", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	released, releasedLease, err := second.AdoptRun(ctx, state.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() after release error = %v", err)
	}
	if released.RunID != state.RunID {
		t.Fatalf("AdoptRun() = %q, want %q", released.RunID, state.RunID)
	}
	if err := releasedLease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// integratedState is a succeeded run whose work is promoted, recorded in the
// phase it was interrupted in.
func integratedState(t *testing.T, phase Phase) State {
	t.Helper()
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-integrated-" + string(phase)
	state.Phase = phase
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.ProviderSessionID = "developer-session"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewModel = "opus"
	state.ReviewDecision = ReviewApprove
	state.Integration = &Integration{
		TargetBranch:         "main",
		SourceCommit:         strings.Repeat("b", 40),
		TargetCommit:         strings.Repeat("b", 40),
		PreviousTargetCommit: strings.Repeat("a", 40),
	}
	return state
}

// The moment a merge stopped being expected is a fact about the run, so it has
// to outlive the process that found it out — which is usually a reconcile sweep
// somebody ran once and will not run again for hours.
func TestStoreRoundTripsADroppedMerge(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := integratedState(t, PhaseCleaningUp)
	state.PullRequest = &PullRequest{
		Remote: "origin", Branch: state.Branch, Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("b", 40),
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	dropped := state.StartedAt.Add(time.Hour).UTC()
	state.PublishFailure = "the forge dropped the queued merge of pull request 84"
	state.MergeDrop = &MergeDrop{At: dropped, Reason: state.PublishFailure}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.MergeDrop == nil || !loaded.MergeDrop.At.Equal(dropped) || loaded.MergeDrop.Reason != state.PublishFailure {
		t.Fatalf("Load() = %#v, want the recorded drop", loaded.MergeDrop)
	}
	// The publication is what is still waiting, and it goes on being counted
	// whatever the sweep did to the run: a drop stops the harness owing a step
	// and leaves the change published nowhere.
	if !loaded.AwaitingForge() {
		t.Fatalf("a promotion whose merge was dropped is not awaiting the forge: %#v", loaded)
	}
}

// A drop is a drop of something. Without the request it was going to merge, the
// record says a publication needs a person and nothing says which one.
func TestStateRejectsADroppedMergeWithNoPublication(t *testing.T) {
	t.Parallel()

	state := integratedState(t, PhaseCleaningUp)
	state.MergeDrop = &MergeDrop{At: state.StartedAt, Reason: "the forge dropped it"}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "requires the pull request") {
		t.Fatalf("Validate() error = %v, want a drop with no publication refused", err)
	}
}

// What AwaitingForge answers and Outstanding does not: a run the harness owes
// nothing on can still have a promotion nobody has published.
func TestAMergedPublicationIsNotAwaitingTheForge(t *testing.T) {
	t.Parallel()

	state := integratedState(t, PhaseComplete)
	state.PullRequest = &PullRequest{
		Remote: "origin", Branch: state.Branch, Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("b", 40),
		MergeMethod: "merge", Merged: true,
	}
	if state.Outstanding() {
		t.Fatalf("a settled run still owes a step: %#v", state)
	}
	if state.AwaitingForge() {
		t.Fatalf("a merged publication is still waiting on the forge: %#v", state.PullRequest)
	}
	unmerged := *state.PullRequest
	unmerged.Merged = false
	state.PullRequest = &unmerged
	if !state.AwaitingForge() {
		t.Fatalf("an unmerged publication of a promoted change is not waiting on the forge: %#v", state.PullRequest)
	}
}

func TestIncompleteRejectsCorruptStateRatherThanDuplicatingIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runID := "run-0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(store.Root(), runID+".json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Incomplete(); err == nil || !strings.Contains(err.Error(), "discover incomplete runs") {
		t.Fatalf("Incomplete() error = %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	}
	root, err := DefaultRoot(getenv, func() (string, error) { return "/home/test", nil }, "linux")
	if err != nil {
		t.Fatalf("DefaultRoot() error = %v", err)
	}
	if root != filepath.Join("/state", "yoyodyne") {
		t.Fatalf("DefaultRoot() = %q", root)
	}
	if _, err := DefaultRoot(func(string) string { return "relative" }, func() (string, error) { return "/home/test", nil }, "linux"); err == nil {
		t.Fatal("DefaultRoot() relative override error = nil")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func testState(t *testing.T, status Status) State {
	t.Helper()
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	state := State{
		SchemaVersion: StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-test",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if status.Terminal() {
		state.CompletedAt = &now
	}
	return state
}

// The pause deadline has to survive the process that wrote it: that is the
// whole point of recording it before the wait begins.
func TestStoreRoundTripsAUsageLimitPause(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Phase = PhaseDeveloping
	resetsAt := state.StartedAt.Add(2 * time.Hour).UTC()
	state.UsageLimitResetsAt = &resetsAt
	state.UsageLimitKind = "five_hour"
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.UsageLimitResetsAt == nil || !loaded.UsageLimitResetsAt.Equal(resetsAt) || loaded.UsageLimitKind != "five_hour" {
		t.Fatalf("Load() = %#v, want the recorded pause", loaded)
	}
	// A paused run is in flight, so every entry point that looks for work to
	// resume has to see it.
	if !loaded.Outstanding() {
		t.Fatalf("a paused run is not outstanding: %#v", loaded)
	}
}

// A pause is an instruction to resume later. Recorded on a terminal run it would
// promise a continuation nothing will ever make, so it must not be storable.
func TestStateRejectsAPauseOnARunThatCannotResume(t *testing.T) {
	t.Parallel()

	completedAt := time.Now().UTC()
	terminal := testState(t, StatusFailed)
	terminal.CompletedAt = &completedAt
	resetsAt := completedAt.Add(time.Hour)
	terminal.UsageLimitResetsAt = &resetsAt
	if err := terminal.Validate(); err == nil || !strings.Contains(err.Error(), "requires a run that is still in flight") {
		t.Fatalf("Validate() error = %v, want a terminal run to refuse a pause", err)
	}

	zeroed := testState(t, StatusRunning)
	zeroed.UsageLimitResetsAt = &time.Time{}
	if err := zeroed.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be the zero time") {
		t.Fatalf("Validate() error = %v, want the zero time refused", err)
	}

	// The operator's hold is the same kind of instruction and is held to the same
	// rule: a run nothing can carry on must not carry a park that promises it.
	parked := testState(t, StatusFailed)
	parked.CompletedAt = &completedAt
	heldSince := completedAt.Add(-time.Hour)
	parked.OperatorHeldSince = &heldSince
	if err := parked.Validate(); err == nil || !strings.Contains(err.Error(), "requires a run that is still in flight") {
		t.Fatalf("Validate() error = %v, want a terminal run to refuse an operator hold", err)
	}
	// What a hold cost a run outlives the park itself: it is the ledger's account
	// of why time passed, and a run that finished still spent it.
	held := testState(t, StatusRunning)
	held.OperatorHeldSeconds = int64((2 * time.Hour) / time.Second)
	if err := held.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want recorded held time to be storable", err)
	}
	if held.OperatorHeld() != 2*time.Hour {
		t.Fatalf("OperatorHeld() = %s, want the two hours recorded", held.OperatorHeld())
	}
}

// The published pull request is durable evidence like the integration it
// accompanies: a run that stops after publishing still names the work it put
// somewhere other people can see.
func TestStoreRoundTripsThePublishedPullRequest(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Phase = PhaseDeveloping
	state.PullRequest = &PullRequest{
		Remote:     "origin",
		Branch:     "yoyodyne/task/01234567",
		Number:     12,
		URL:        "https://example.invalid/pull/12",
		HeadCommit: strings.Repeat("b", 40),
		State:      "OPEN",
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, state) {
		t.Fatalf("Load() = %#v, want %#v", loaded, state)
	}
}

func TestStateRejectsIncoherentPublishedEvidence(t *testing.T) {
	t.Parallel()

	published := func(t *testing.T) State {
		t.Helper()
		state := testState(t, StatusRunning)
		state.WorktreePath = "/state/worktree"
		state.Branch = "yoyodyne/task/01234567"
		state.BaseCommit = strings.Repeat("a", 40)
		state.TargetBranch = "main"
		state.Phase = PhaseDeveloping
		state.PullRequest = &PullRequest{
			Remote:     "origin",
			Branch:     "yoyodyne/task/01234567",
			Number:     12,
			URL:        "https://example.invalid/pull/12",
			HeadCommit: strings.Repeat("b", 40),
		}
		return state
	}

	for _, test := range []struct {
		name    string
		mutate  func(*State)
		problem string
	}{
		{name: "published and open"},
		{
			name:    "no number to address it by",
			mutate:  func(state *State) { state.PullRequest.Number = 0 },
			problem: "pull_request number must be positive",
		},
		{
			name:    "nowhere to find it",
			mutate:  func(state *State) { state.PullRequest.URL = "" },
			problem: "pull_request url is required",
		},
		{
			name:    "a branch this run never published",
			mutate:  func(state *State) { state.PullRequest.Branch = "yoyodyne/other/01234567" },
			problem: "does not match the run branch",
		},
		{
			// The forge is only asked to merge once the promotion it carries has
			// been made, so a merge this run asked for cannot be recorded before
			// it is. The recorded method is what says the run asked.
			name: "merged by a request this run made, with nothing integrated",
			mutate: func(state *State) {
				state.PullRequest.Merged = true
				state.PullRequest.MergeMethod = "merge"
			},
			problem: "merged pull request the run asked the forge for requires recorded integration",
		},
		{
			// A merge nobody here asked for is what somebody merging the request
			// on the forge after the run was over looks like. Recording it says
			// what the forge did, not that this run promoted anything, and
			// refusing to store it is what left such a record stale for good.
			name:   "merged on the forge by somebody else, with nothing integrated",
			mutate: func(state *State) { state.PullRequest.Merged = true },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state := published(t)
			if test.mutate != nil {
				test.mutate(&state)
			}
			err := state.Validate()
			if test.problem == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("Validate() error = %v, want one mentioning %q", err, test.problem)
			}
		})
	}
}

// The commit the harness made is what permits a run's worktree HEAD to have
// moved, so it has to survive the process that made it: a resumed run that
// could not name it would either refuse its own published work or have to
// accept whatever HEAD it finds.
func TestStateHoldsTheHarnessCommitThatPermitsAMovedHead(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.HarnessCommit = strings.Repeat("b", 40)
	state.Phase = PhaseDeveloping
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.HarnessCommit != state.HarnessCommit {
		t.Fatalf("loaded harness commit = %q, want %q", loaded.HarnessCommit, state.HarnessCommit)
	}

	for _, test := range []struct {
		name    string
		mutate  func(*State)
		problem string
	}{
		{
			name:    "not a commit",
			mutate:  func(state *State) { state.HarnessCommit = "HEAD" },
			problem: "harness_commit is invalid",
		},
		{
			// The base is in the worktree by construction, so accepting it would
			// permit a HEAD that proves no harness commit was ever made.
			name:    "the base commit, which proves nothing",
			mutate:  func(state *State) { state.HarnessCommit = state.BaseCommit },
			problem: "harness_commit cannot be the base commit",
		},
		{
			name: "recorded without the worktree that produced it",
			mutate: func(state *State) {
				state.WorktreePath = ""
				state.Branch = ""
				state.BaseCommit = ""
			},
			problem: "harness_commit requires the worktree that produced it",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			broken := state
			test.mutate(&broken)
			if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("Validate() error = %v, want one mentioning %q", err, test.problem)
			}
		})
	}
}

// A stopped provider has to survive the process that recorded it for the same
// reason a pause deadline does: it is what tells the next invocation the run is
// owed a continuation rather than a fresh start.
func TestStoreRoundTripsAStoppedProviderAndRefusesItOnATerminalRun(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.WorktreePath = "/state/worktree"
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Phase = PhaseDeveloping
	state.ProviderSessionID = "developer-session"
	state.ProviderStop = ProviderStopStalled
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ProviderStop != ProviderStopStalled || !loaded.Outstanding() {
		t.Fatalf("Load() = %#v, want the recorded stop on an outstanding run", loaded)
	}

	// A stop is an instruction to continue later, so a terminal run must not be
	// able to carry one: it would promise a continuation nothing will make.
	terminal := loaded
	terminal.Status = StatusFailed
	completedAt := terminal.UpdatedAt
	terminal.CompletedAt = &completedAt
	if err := terminal.Validate(); err == nil || !strings.Contains(err.Error(), "provider_stop requires a run that is still in flight") {
		t.Fatalf("Validate() error = %v, want a terminal run carrying a stop to be refused", err)
	}
	unknown := loaded
	unknown.ProviderStop = "wandered off"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "provider_stop is invalid") {
		t.Fatalf("Validate() error = %v, want an unknown stop reason to be refused", err)
	}
}

// What a run changed has to outlive the worktree it changed it in: cleanup
// removes the tree and the branch, so a summary nobody recorded is one nobody
// can be shown afterwards.
func TestStoreRoundTripsWhatARunChanged(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	state.Changes = RecordChanges("M internal/chat/chat.go\nA internal/chat/decision.go", " 2 files changed, 40 insertions(+)")
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Changes == nil || loaded.Changes.Files != state.Changes.Files || loaded.Changes.DiffStat != state.Changes.DiffStat {
		t.Fatalf("recorded changes = %#v, want %#v", loaded.Changes, state.Changes)
	}
	// A run that summarized nothing records nothing, so an absent account is
	// never mistaken for an empty change.
	if empty := RecordChanges("", "   "); empty != nil {
		t.Fatalf("RecordChanges() = %#v, want nothing recorded", empty)
	}
}

// A very large change must not be able to fill the state file with its own
// listing. What the bound cuts is the tail of a summary, and it says that it
// cut it rather than leaving a clamped listing looking complete.
func TestRecordedChangesAreBoundedAndSayWhenTheyWereCut(t *testing.T) {
	t.Parallel()

	changes := RecordChanges(strings.Repeat("M some/long/path/to/a/file.go\n", 2000), "")
	if changes == nil {
		t.Fatal("RecordChanges() recorded nothing at all")
	}
	if len(changes.Files) > MaxChangeRecordBytes {
		t.Fatalf("recorded %d bytes, limit is %d", len(changes.Files), MaxChangeRecordBytes)
	}
	if !strings.HasSuffix(changes.Files, changeRecordCutNote) {
		t.Fatalf("a cut listing does not say it was cut: %q", changes.Files[len(changes.Files)-80:])
	}
	if err := changes.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	// The bound is enforced on the way in as well, so a record assembled by hand
	// cannot smuggle an unbounded listing into the state file.
	oversized := Changes{DiffStat: strings.Repeat("x", MaxChangeRecordBytes+1)}
	if err := oversized.Validate(); err == nil {
		t.Fatal("Validate() accepted a summary past the bound")
	}
}

// Asking what the harness last did to a work item is a read. It answers for a
// terminal run as readily as an in-flight one, and it decides nothing about
// either: nothing is held, adopted, or claimed by looking.
func TestLatestReportsTheMostRecentRunOfAWorkItem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	older := testState(t, StatusSucceeded)
	older.WorkItemID = "yoyodyne-ifd.39"
	newer := testState(t, StatusRunning)
	newer.WorkItemID = "yoyodyne-ifd.39"
	newer.StartedAt = older.StartedAt.Add(time.Hour)
	newer.UpdatedAt = newer.StartedAt
	other := testState(t, StatusSucceeded)
	other.WorkItemID = "yoyodyne-ifd.38"
	other.StartedAt = newer.StartedAt.Add(time.Hour)
	other.UpdatedAt = other.StartedAt
	other.CompletedAt = &other.StartedAt
	for _, state := range []State{older, newer, other} {
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	latest, err := store.Latest("yoyodyne-ifd.39")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.RunID != newer.RunID {
		t.Fatalf("Latest() = %s, want the most recently started run %s", latest.RunID, newer.RunID)
	}
	if _, err := store.Latest("yoyodyne-ifd.99"); !errors.Is(err, ErrNoRecordedRun) {
		t.Fatalf("Latest() error = %v, want a plain answer that there is no run", err)
	}
	if _, err := store.Latest("  "); err == nil {
		t.Fatal("Latest() accepted no work item at all")
	}
}

// The cap triage measures against is a total for the work item, so the rounds
// are summed across every run ever made for it. A count kept per run would let
// an item argue with a reviewer indefinitely by being run again.
func TestReviewRoundsAreSummedAcrossEveryRunOfOneItem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	for _, rounds := range []int{2, 3} {
		state := testState(t, StatusFailed)
		state.WorkItemID = "yoyodyne-task"
		state.ReviewRounds = rounds
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	// Another item's rounds are not this item's, however many runs it took.
	elsewhere := testState(t, StatusFailed)
	elsewhere.WorkItemID = "yoyodyne-other"
	elsewhere.ReviewRounds = 4
	if err := store.Create(elsewhere); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rounds, err := store.ReviewRounds("yoyodyne-task")
	if err != nil {
		t.Fatalf("ReviewRounds() error = %v", err)
	}
	if rounds != 5 {
		t.Fatalf("ReviewRounds() = %d, want 5", rounds)
	}
	// An item nothing has ever reviewed has taken no rounds, which is an answer
	// rather than a failure to look.
	unrun, err := store.ReviewRounds("yoyodyne-unrun")
	if err != nil || unrun != 0 {
		t.Fatalf("ReviewRounds() = %d, error = %v, want no rounds", unrun, err)
	}
}
