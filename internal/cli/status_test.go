package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The failure an operator is chasing is already recorded; what was missing was
// any way to read it back without going through the tracker item by item. So the
// verb has to reach the records a run actually wrote, name the item, and carry
// the reason.
func TestStatusReportsFailedRunsWithTheirReasons(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// records it reads are written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing recorded is an answer rather than an empty listing.
	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "no recorded runs") {
		t.Fatalf("stdout = %q", stdout)
	}

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	failed := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started.Add(time.Hour))
	failed.Phase = runstate.PhaseReviewing
	failed.Failure = "independent review requires repair after 2 of 2 permitted attempt(s):\nthe change does not do what the item asked"
	saveRun(t, store, failed)
	// A failed attempt spent real money, and the listing prices it from the same
	// event log the ledger prices from.
	appendRunCost(t, store, failed.RunID, 8.91)
	succeeded := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.41", started)
	succeeded.Phase = runstate.PhaseComplete
	saveRun(t, store, succeeded)
	appendRunCost(t, store, succeeded.RunID, 19.02)

	stdout, stderr, code = runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"2 of 2 shown",
		failed.RunID,
		"yoyodyne-ifd.2.7",
		"[failed, reviewing]",
		"reason: independent review requires repair",
		"$8.91",
		"yoyodyne-ifd.41",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// A reason written over several lines is folded onto one, so a listing of
	// runs stays a listing.
	folded := "reason: independent review requires repair after 2 of 2 permitted attempt(s): the change does not do what the item asked"
	if !strings.Contains(stdout, folded) {
		t.Fatalf("a multi-line reason was not folded onto one line: %q", stdout)
	}

	// Asking for the failures leaves the successful run out, and reporting a
	// failure is not itself a failure of the command.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--failed")
	if code != 0 {
		t.Fatalf("status --failed code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, succeeded.RunID) {
		t.Fatalf("a succeeded run reached --failed: %q", stdout)
	}
	if !strings.Contains(stdout, "runs that ended without succeeding, 1 of 1 shown") {
		t.Fatalf("stdout = %q", stdout)
	}

	// A script reads the same records, with the whole reason rather than the
	// line the listing shows.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "--failed", "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var decoded statusOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Matched != 1 || decoded.Recorded != 2 || len(decoded.Runs) != 1 {
		t.Fatalf("decoded = %#v", decoded)
	}
	if decoded.Runs[0].Failure != failed.Failure {
		t.Fatalf("JSON reason = %q, want the whole recorded reason", decoded.Runs[0].Failure)
	}
}

// Naming an item reports its runs, and a limited listing has to say what it was
// limited from: a listing that reads as the whole record while being a fifth of
// it is worse than no listing.
func TestStatusNarrowsToOneItemAndSaysWhatALimitLeftOut(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		run := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.2.7", started.Add(time.Duration(index)*time.Hour))
		run.Failure = "developer reported failure: api_error"
		saveRun(t, store, run)
	}
	other := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.41", started)
	other.Failure = "primary checkout is not ready for integration"
	saveRun(t, store, other)

	stdout, stderr, code := runCLI(t, "status", "--config", configPath, "--limit", "2")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "2 of 4 shown") || !strings.Contains(stdout, "2 further run(s) are not listed here") {
		t.Fatalf("stdout = %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.41")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "runs of yoyodyne-ifd.41, 1 of 1 shown") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "api_error") {
		t.Fatalf("another item's run reached a listing of one item: %q", stdout)
	}

	// An item the harness has never run says so against the record it was read
	// out of, rather than reading as though nothing has ever run.
	stdout, stderr, code = runCLI(t, "status", "--config", configPath, "yoyodyne-ifd.99")
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "no runs of yoyodyne-ifd.99, of the 4 run(s) recorded") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// The bookkeeping failures are recorded apart from the run's own so neither can
// masquerade as the other, and the listing has to keep them apart: an
// outstanding cleanup on an integrated run is not a piece of work that failed.
func TestStatusNamesEachRecordedReasonForWhatItIs(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{
		Matched:  2,
		Recorded: 2,
		Runs: []runstate.RunSummary{
			{
				RunID:                      "run-0123456789abcdef0123456789abcdef",
				WorkItemID:                 "yoyodyne-ifd.41",
				Status:                     runstate.StatusSucceeded,
				Phase:                      runstate.PhaseCleaningUp,
				StartedAt:                  completedAt,
				CompletedAt:                &completedAt,
				Integrated:                 true,
				Outstanding:                true,
				CleanupFailure:             "remove worktree: directory is busy",
				PublishFailure:             "push branch: remote rejected",
				CompletionRecordingFailure: "save completed run state after cleanup: disk full",
				CostUSD:                    19.02,
				Invocations:                2,
			},
			{
				RunID:      "run-fedcba9876543210fedcba9876543210",
				WorkItemID: "yoyodyne-ifd.2.7",
				Status:     runstate.StatusRunning,
				Phase:      runstate.PhaseChecking,
				StartedAt:  completedAt,
				// Every run still in flight owes its own remaining steps, so
				// saying so of this one would say nothing.
				Outstanding: true,
				FailingCheck: &runstate.CheckFailure{
					Command:  "make test",
					ExitCode: 2,
					Output:   "FAIL",
				},
				CostUSD: 3.5,
			},
		},
	}, "", false)
	rendered := out.String()
	for _, want := range []string{
		"[succeeded, cleaning_up, integrated, outstanding] $19.02",
		"outstanding publication: push branch: remote rejected",
		"outstanding cleanup: remove worktree: directory is busy",
		"completion recorded late: save completed run state after cleanup: disk full",
		// The marker in the brackets is never left for a reader to interpret:
		// what the run owes is said under it.
		"outstanding: its work is promoted, and cleaning up after it is not recorded as finished",
		// A run still going has not finished spending, so its figure says so.
		"[running, checking] $3.50 so far",
		"failing check: make test exited 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, want it to contain %q", rendered, want)
		}
	}
	// Nothing here failed, so nothing may be printed as the run's own reason.
	if strings.Contains(rendered, "reason:") {
		t.Fatalf("a bookkeeping failure was reported as a run failure: %q", rendered)
	}

	// A run whose evidence is gone is stated as unpriceable rather than as free.
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{
		Matched:  1,
		Recorded: 1,
		Runs: []runstate.RunSummary{{
			RunID:       "run-0123456789abcdef0123456789abcdef",
			WorkItemID:  "yoyodyne-ifd.41",
			Status:      runstate.StatusFailed,
			StartedAt:   completedAt,
			CompletedAt: &completedAt,
			UnknownCost: "the run's event log is no longer recorded",
		}},
	}, "", true)
	if !strings.Contains(out.String(), "cost unknown") {
		t.Fatalf("rendered = %q", out.String())
	}
}

// A run marked outstanding with nothing under it is the "go and read the run's
// JSON" case this verb exists to remove, and the marker has two causes worth
// telling apart: cleanup that never finished, and a merge the forge queued and
// has not performed. The second outlives the run by minutes or hours, so a run
// carrying it looks finished in every other respect.
func TestStatusSaysWhatAnOutstandingRunStillOwes(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	finished := runstate.RunSummary{
		RunID:       "run-0123456789abcdef0123456789abcdef",
		WorkItemID:  "yoyodyne-ifd.41",
		Status:      runstate.StatusSucceeded,
		Phase:       runstate.PhaseComplete,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Integrated:  true,
		Outstanding: true,
		MergeQueued: true,
		CostUSD:     19.02,
	}
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{finished}}, "", false)
	if !strings.Contains(out.String(), "outstanding: the forge queued the merge of its pull request") {
		t.Fatalf("rendered = %q", out.String())
	}
	// Cleanup is finished on this one, so nothing may claim otherwise.
	if strings.Contains(out.String(), "cleaning up after it is not recorded as finished") {
		t.Fatalf("a complete cleanup was reported as unfinished: %q", out.String())
	}

	// A run that owes something the rendering cannot name is still reported as
	// owing something, rather than marked outstanding and left bare.
	unexplained := finished
	unexplained.Integrated = false
	unexplained.MergeQueued = false
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{unexplained}}, "", false)
	if !strings.Contains(out.String(), "outstanding: it still owes a step") {
		t.Fatalf("rendered = %q", out.String())
	}

	// Every run still in flight owes its remaining steps, so saying so of one
	// would be noise rather than news.
	running := finished
	running.Status = runstate.StatusRunning
	running.CompletedAt = nil
	running.Phase = runstate.PhaseDeveloping
	out.Reset()
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{running}}, "", false)
	if strings.Contains(out.String(), "outstanding") {
		t.Fatalf("an in-flight run was reported as outstanding: %q", out.String())
	}
}

// A reason is prose somebody wrote, and a reviewer's verdict runs to
// paragraphs. The listing bounds it so a run stays one row, and cuts on a rune
// boundary: this repository's own reasons are full of em dashes, and half a
// rune is not a shorter reason but a broken one.
func TestStatusBoundsAReasonWithoutBreakingARune(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	// Em dashes laid so that the byte the bound falls on is inside one.
	verdict := "independent review requires repair " + strings.Repeat("—", 200)
	var out bytes.Buffer
	printRunHistory(&out, runstate.RunHistory{Matched: 1, Recorded: 1, Runs: []runstate.RunSummary{{
		RunID:       "run-0123456789abcdef0123456789abcdef",
		WorkItemID:  "yoyodyne-ifd.41",
		Status:      runstate.StatusFailed,
		StartedAt:   completedAt,
		CompletedAt: &completedAt,
		Failure:     verdict,
	}}}, "", true)
	rendered := out.String()
	if !utf8.ValidString(rendered) {
		t.Fatalf("the listing is not valid UTF-8: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "reason:") {
			continue
		}
		if !strings.HasSuffix(line, "...") {
			t.Fatalf("an unbounded reason was listed: %q", line)
		}
		if len(line) > maxSingleLineBytes+32 {
			t.Fatalf("the listed reason is %d bytes: %q", len(line), line)
		}
	}
}

func TestStatusRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	_, stderr, code := runCLI(t, "status", "yoyodyne-ifd.41", "yoyodyne-ifd.42")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "at most one Beads work item id") {
		t.Fatalf("stderr = %q", stderr)
	}

	_, stderr, code = runCLI(t, "status", "--limit", "-1")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "limit cannot be negative") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// The verb reads the records without building the repository-facing components,
// so that the two still address one store is asserted rather than assumed. A
// status verb pointed at a different root would report "no recorded runs" over a
// directory full of them, which is the one failure it cannot afford: it is the
// surface an operator reaches for when something has gone wrong.
func TestStatusReadsTheSameStoreRunsAreRecordedIn(t *testing.T) {
	// Not parallel: the state root both paths resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	store, err := recordedRunStore(configPath)
	if err != nil {
		t.Fatalf("recordedRunStore() error = %v", err)
	}
	if store.Root() != parts.store.Root() {
		t.Fatalf("status reads %s, but runs are recorded in %s", store.Root(), parts.store.Root())
	}
}

// recordedRun creates one run record, so a test can then set what it is about
// and save it.
func recordedRun(t *testing.T, store *runstate.Store, status runstate.Status, workItemID string, startedAt time.Time) runstate.State {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     startedAt,
		UpdatedAt:     startedAt,
	}
	if status.Terminal() {
		completedAt := startedAt
		state.CompletedAt = &completedAt
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return state
}

// appendRunCost records what one provider invocation of a run cost, which is
// the evidence the listing prices a run from.
func appendRunCost(t *testing.T, store *runstate.Store, runID string, cost float64) {
	t.Helper()
	event, err := execution.NewEvent(runID, 1, time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC),
		execution.EventRunCompleted, "claude-code", map[string]any{
			"session_id":     "session-developer",
			"total_cost_usd": cost,
		})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func saveRun(t *testing.T, store *runstate.Store, state runstate.State) {
	t.Helper()
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}
