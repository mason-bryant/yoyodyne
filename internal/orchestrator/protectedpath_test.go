package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// trackerExport is the path `yoyo run` declares as the export a worktree is
// given the primary checkout's copy of, which the fixtures here configure too.
const trackerExport = ".beads/issues.jsonl"

// writeTrackerExport puts content where the tracker's export lives, in whichever
// checkout it is given: the primary one, whose copy a new worktree is handed, or
// a worktree, which is where a developer would find it.
func writeTrackerExport(t *testing.T, checkout, content string) {
	t.Helper()
	full := filepath.Join(checkout, filepath.FromSlash(trackerExport))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// exportingRepository is a checkout whose branch carries the tracker's export
// and whose working copy of it is newer, which is the ordinary state of a
// checkout between release cuts: the store moves every time an item is admitted,
// and the export is committed on a cadence of its own.
func exportingRepository(t *testing.T) string {
	t.Helper()
	repository := pipelineRepository(t)
	writeTrackerExport(t, repository, committedExport)
	runPipelineGit(t, repository, "add", trackerExport)
	runPipelineGit(t, repository, "commit", "-m", "export the tracker's items")
	writeTrackerExport(t, repository, refreshedExport)
	return repository
}

const (
	committedExport = "{\"id\":\"yoyodyne-1\"}\n"
	refreshedExport = "{\"id\":\"yoyodyne-1\"}\n{\"id\":\"yoyodyne-2\"}\n"
)

// writeUpstream puts a file where an upstream artifact lives, which is what a
// developer with an editor in its worktree does when it decides the intent it
// was given should say something else.
func writeUpstream(t *testing.T, worktree, relative, content string) error {
	t.Helper()
	full := filepath.Join(worktree, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o600)
}

// A grant of one of these paths is refused at admission, and a run refuses to
// start on an item carrying one however it got there. What neither gate catches
// is the item that describes the work and grants nothing — no marker, so nothing
// to refuse — whose developer would otherwise spend attempts looking for a way
// into a file the provider was never going to allow. The contract is what tells
// that developer, before its first attempt rather than after its last.
func TestTheDeveloperContractNamesEveryPathBeyondAGrant(t *testing.T) {
	t.Parallel()

	for _, entry := range protectedpath.ProviderPaths {
		if !strings.Contains(developerContract, entry.Path) {
			t.Fatalf("the developer contract never names %q, which no grant reaches", entry.Path)
		}
		if !strings.Contains(developerContract, entry.Provider) {
			t.Fatalf("the developer contract never names %q, which is what refuses %q", entry.Provider, entry.Path)
		}
	}
}

// A grant is honoured from an item's design guidance and acceptance criteria as
// well as from its title and description, and the two doors admission holds — a
// proposal and a tracker action — carry neither of those two: no action takes
// them, no creation sets them, and they reach an item through the tracker's own
// command. So the run reads all four itself and refuses to start, which is the
// same refusal one step later and still before anything is claimed or spent.
func TestARunRefusesToStartOnAnItemWhoseDesignGuidanceGrantsAPathNoProviderHonours(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Wire the goal guard into the developer's hook",
		Status: "open",
		Design: protectedpath.GrantMarker + " .claude/settings.json\n",
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	_, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() on an item granting a path no provider honours = nil error, want it refused before it started")
	}
	// The refusal is worth having only if it says whose boundary this is and what
	// to do about it, exactly as the one admission gives does.
	for _, want := range []string{".claude/settings.json", "Claude Code", "operator"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q never names %q", err, want)
		}
	}
	// The point of refusing here is that nothing was spent: this is the budget
	// ifd.153 burned three rounds of against the same wall.
	if developers := len(provider.requestsForRole(domain.RoleDeveloper)); developers != 0 {
		t.Fatalf("developer invocations = %d, want none", developers)
	}
	if tracker.claimed {
		t.Fatal("the item was claimed by a run that could never finish it")
	}
}

func TestAChangeTouchingAnUngrantedProtectedPathIsRefusedBeforeAnythingJudgesIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		// The first attempt also rewrites the product's own brief; the repair
		// attempt takes it back out.
		if attempts == 1 {
			return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n")
		}
		return os.RemoveAll(filepath.Join(request.WorkingDirectory, "docs", "product"))
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || !tracker.closed || tracker.blocked {
		t.Fatalf("the repaired change was not integrated: %#v, closed = %t, blocked = %t", outcome.Integration, tracker.closed, tracker.blocked)
	}
	// The refusal spent one attempt from the same budget the other two kinds of
	// repair draw on.
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the refusal to have spent one", outcome.RepairAttempts)
	}
	developerRequests := provider.requestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	// This is the whole point of the gate: no reviewer was asked about the
	// attempt that reached outside its item, so the class costs nothing to catch.
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that stayed inside its scope", reviews)
	}
	// The rule is declared before the first attempt as well as in the refusal.
	// A gate a developer only meets by tripping it costs an attempt to learn.
	if !strings.Contains(developerRequests[0].Prompt, protectedpath.GrantMarker) {
		t.Fatalf("the first attempt was not told the rule:\n%s", developerRequests[0].Prompt)
	}
	repair := developerRequests[1]
	if repair.SessionID != provider.developerSession || repair.WorkingDirectory != outcome.WorktreePath {
		t.Fatalf("the refusal did not go back to the same developer in the same worktree: %#v", repair)
	}
	for _, want := range []string{
		"repair attempt 1 of 2",
		"docs/product/brief.md",
		"Granted by this work item: nothing",
		// A developer that genuinely needs the path has to be told how one is
		// granted, or the gate is something to work around rather than to raise.
		protectedpath.GrantMarker,
	} {
		if !strings.Contains(repair.Prompt, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, repair.Prompt)
		}
	}
	// An integrated run carries no outstanding refusal: the change that was
	// promoted is the one that touched nothing it was not granted.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
	if _, err := attemptPipelineGit(repository, "show", "main:docs/product/brief.md"); err == nil {
		t.Fatal("the refused edit reached the target branch")
	}
}

// The refreshed export is kept out of a run's change by Git's skip-worktree bit,
// which lives in the worktree's index under `.git` — a directory a developer's
// sandbox grants writes to. So the bit is where the hold is kept rather than what
// enforces it: one `git update-index --no-skip-worktree` turns the copy the
// harness put there into a modification the harness would otherwise commit,
// promote, and conflict every other run against. What refuses it is this gate,
// which reads the change rather than the index, and it is the case the read-only
// posture already takes seriously — an agent following injected instructions is
// exactly who would flip the bit.
func TestARefreshedExportInTheChangeIsRefusedHoweverTheIndexBitEnded(t *testing.T) {
	t.Parallel()

	repository := exportingRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		if attempts == 1 {
			// The whole of the attack: the file's content is the harness's own
			// refresh, and lifting the hold is what makes it this run's change.
			if output, err := attemptPipelineGit(request.WorkingDirectory, "update-index", "--no-skip-worktree", "--", trackerExport); err != nil {
				return fmt.Errorf("lift the hold on %s: %v: %s", trackerExport, err, output)
			}
			return nil
		}
		// The repair takes it back out by putting the file at what the base commit
		// carries, which is what the refusal asks for.
		writeTrackerExport(t, request.WorkingDirectory, committedExport)
		return nil
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("the repaired change was not integrated on one refusal: %#v", outcome)
	}
	// No reviewer was asked about the attempt carrying the export, and no
	// promotion carried it: the export on the target is the one the branch had.
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviews = %d, want only the attempt that carried no export", reviews)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+trackerExport); promoted != committedExport {
		t.Fatalf("the promoted export = %q, want the committed copy %q", promoted, committedExport)
	}
	developerRequests := provider.requestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and its repair", len(developerRequests))
	}
	// A developer refused for a file it never wrote can act on the refusal only
	// if the refusal says what the file is and how it got there.
	for _, want := range []string{
		trackerExport,
		"Held out of every run's change by the harness: " + trackerExport,
		"derived from a store outside Git",
		"the hold was lifted",
	} {
		if !strings.Contains(developerRequests[1].Prompt, want) {
			t.Fatalf("the refusal is missing %q:\n%s", want, developerRequests[1].Prompt)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
}

// The other half of the same rule: the export a run leaves held is not part of
// its change, so a run that reads it and writes its own work costs nothing. A
// gate that refused the ordinary case would refuse every run this project makes.
func TestAnExportLeftHeldIsNotRefusedThoughTheWorktreeCarriesTheRefreshedCopy(t *testing.T) {
	t.Parallel()

	repository := exportingRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	var read string
	provider := roleBackend(func(request backend.RunRequest) error {
		content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, filepath.FromSlash(trackerExport)))
		if err != nil {
			return err
		}
		read = string(content)
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 0 {
		t.Fatalf("a run that only read the export was refused: %#v", outcome)
	}
	// It read the current export rather than the copy its base commit carried,
	// which is the reason the refresh exists at all.
	if read != refreshedExport {
		t.Fatalf("the developer read %q, want the refreshed export %q", read, refreshedExport)
	}
	if promoted := gitOutput(t, repository, "show", "main:"+trackerExport); promoted != committedExport {
		t.Fatalf("the promoted export = %q, want the committed copy %q", promoted, committedExport)
	}
}

func TestAGrantInTheWorkItemAdmitsThePathForEveryAttemptTheItemMakes(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	// The precedent this has to cover: an item whose own work is to move a
	// design document. The grant is in the item, written and reviewed before the
	// run started, which is what makes it somebody's decision rather than the
	// developer's.
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:          "yoyodyne-task",
		Title:       "Move the design document into its home",
		Description: "The design has to move.\n\nProtected-path grant: docs/designs/v1-harness-design.md\n",
		Status:      "open",
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/designs/v1-harness-design.md", "the design, moved\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f docs/designs/v1-harness-design.md"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 0 {
		t.Fatalf("a granted path did not pass the gate: %#v", outcome)
	}
	if invocations := len(provider.requestsForRole(domain.RoleDeveloper)); invocations != 1 {
		t.Fatalf("developer invocations = %d, want the granted change to stand on its first attempt", invocations)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("a granted path was refused: %#v", state.PathRefusal)
	}
	// The gate admitted the path; whether anybody decided what went into it is
	// the other half of the same mechanism, and that half is the reviewer's. So
	// the review this run asked for carries both the instruction to look for the
	// decision behind the grant and the item text the grant is written in.
	reviews := provider.requestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviews = %d, want the granted change judged once", len(reviews))
	}
	if !strings.Contains(reviews[0].SystemPrompt, "read the item for the decided change named behind each grant") {
		t.Fatalf("the reviewer was not asked what decided the granted edit:\n%s", reviews[0].SystemPrompt)
	}
	if !strings.Contains(strings.ToLower(reviews[0].Prompt), protectedpath.GrantMarker) {
		t.Fatalf("the granting item text never reached the reviewer:\n%s", reviews[0].Prompt)
	}
	// The grant covers the file it names and nothing around it.
	if refused := protectedpath.Protect(pipeline.Config).Refused(
		[]string{"docs/designs/another-design.md"},
		protectedpath.Grants(tracker.item.Description),
	); len(refused) != 1 {
		t.Fatalf("the grant admitted more than the path it named: %v", refused)
	}
}

// The gate cannot ask who typed a grant — the tracker records no authorship it
// could check — so it relies on when instead: the fields a grant is read from
// exist before the run and the harness never writes to them. The notes are the
// one field it does write to, and what it appends there includes the reviewer's
// own summary and findings. A grant read from the notes could therefore be an
// agent's prose admitting a path to the next run of the same item, which is the
// thing this gate exists to stop.
func TestAGrantInTheItemsNotesDoesNotAdmitAPath(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Task",
		Status: "open",
		// Exactly the shape a previous run's recorded outcome leaves behind: the
		// reviewer's own words, appended to the item by the harness.
		Notes: "Review summary: the change is fine.\nProtected-path grant: docs/product\n",
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "a brief the run preferred\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 0

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused") {
		t.Fatalf("Run() error = %v, want the notes to have granted nothing", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal == nil || len(state.PathRefusal.Grants) != 0 {
		t.Fatalf("durable refusal = %#v, want the notes to have granted nothing", state.PathRefusal)
	}
	// The same words in a field somebody authored do admit the path, so what is
	// being tested is where the grant was read from rather than how it was
	// written.
	tracker.item.Design = tracker.item.Notes
	if granted := protectedpath.Grants(grantEvidence(tracker.item)...); len(granted) != 1 || granted[0] != "docs/product" {
		t.Fatalf("Grants() from an authored field = %v, want the path admitted", granted)
	}
}

func TestARefusedChangeBlocksTheItemWhenTheRepairBudgetIsSpent(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, "docs/decisions/invariants/new-invariant.md", "an invariant this run wrote for itself\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	wantFailure := "protected paths refused after 2 of 2 permitted attempt(s)"
	if err == nil || !strings.Contains(err.Error(), wantFailure) {
		t.Fatalf("Run() error = %v, want %q", err, wantFailure)
	}
	if invocations := len(provider.requestsForRole(domain.RoleDeveloper)); invocations != 3 {
		t.Fatalf("developer invocations = %d, want the first attempt and both repairs", invocations)
	}
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 0 {
		t.Fatalf("reviews = %d, want none while the change reaches outside its item", reviews)
	}
	if outcome.Integration != nil || tracker.closed {
		t.Fatalf("a refused change reached integration: %#v, closed = %t", outcome.Integration, tracker.closed)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved with a refused change: %q, want %q", head, before)
	}
	if !tracker.blocked || !outcome.Blocked {
		t.Fatalf("the spent budget did not block the item: tracker = %t, outcome = %t", tracker.blocked, outcome.Blocked)
	}
	// What a person has to decide is which of the two is wrong, so the note says
	// both and settles neither.
	for _, want := range []string{
		"Repair attempts: 2 of 2 permitted",
		"Refused paths: docs/decisions/invariants/new-invariant.md",
		"Granted by this work item: nothing",
		"missing a grant it should have had",
		outcome.WorktreePath,
		outcome.Branch,
	} {
		if !strings.Contains(tracker.blockReason, want) {
			t.Fatalf("blocker is missing %q:\n%s", want, tracker.blockReason)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.RepairAttempts != 2 || state.PathRefusal == nil {
		t.Fatalf("state = %#v", state)
	}
	if len(state.PathRefusal.Paths) != 1 || state.PathRefusal.Paths[0] != "docs/decisions/invariants/new-invariant.md" {
		t.Fatalf("durable refusal = %#v", state.PathRefusal)
	}
}

// A refusal is the gate's decision about the change in front of it, so the
// evidence an earlier attempt collected must not survive beside it: a check that
// passed on a change this one has moved past would otherwise read as a gate this
// attempt cleared.
func TestARefusalReplacesWhateverEarlierEvidenceTheRunWasCarrying(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt fails its check; the second passes it and reaches
		// outside the item instead.
		if attempts == 1 {
			return nil
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return err
		}
		return writeUpstream(t, request.WorkingDirectory, "docs/product/goals/v1-goals.md", "goals this run preferred\n")
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "protected paths refused after 1 of 1 permitted attempt(s)") {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal == nil {
		t.Fatal("no refusal was recorded")
	}
	if state.CheckFailure != nil {
		t.Fatalf("the refusal left an earlier attempt's failing check to compete with it: %#v", state.CheckFailure)
	}
}

func TestAResumedRunIsHandedBackTheRefusalItWasRecordedWith(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The first process keeps rewriting the configuration and is interrupted
	// once its second attempt is already recorded. What survives is an attempt
	// counted against the budget together with the refusal that triggered it.
	interrupted := &interruptedStore{StateStore: store, atAttempt: 2, allowSaves: 1}
	first := roleBackend(func(request backend.RunRequest) error {
		return writeUpstream(t, request.WorkingDirectory, ".yoyodyne/config.yaml", "checks: []\n")
	}, approveVerdict)
	firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first)
	firstOutcome, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !interrupted.stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
	}
	interruptedState, err := store.Load(firstOutcome.RunID)
	if err != nil {
		t.Fatalf("Load() interrupted state error = %v", err)
	}
	if interruptedState.Status.Terminal() || interruptedState.RepairAttempts != 2 || interruptedState.Phase != runstate.PhaseDeveloping {
		t.Fatalf("interrupted state = %#v, want 2 attempts in the developing phase", interruptedState)
	}
	if interruptedState.PathRefusal == nil || interruptedState.PathRefusal.Paths[0] != ".yoyodyne/config.yaml" {
		t.Fatalf("interrupted state lost the refusal: %#v", interruptedState.PathRefusal)
	}

	// The second process rebuilds the interrupted attempt from durable state
	// rather than from a gate it re-ran to discover, and this attempt takes the
	// path back out.
	second := roleBackend(func(request backend.RunRequest) error {
		if err := os.RemoveAll(filepath.Join(request.WorkingDirectory, ".yoyodyne")); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != firstOutcome.RunID || outcome.RepairAttempts != 2 || outcome.Integration == nil {
		t.Fatalf("resumed run did not finish at the recorded attempt: %#v", outcome)
	}
	developerRequests := second.requestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("resumed developer invocations = %d, want the one recorded attempt reissued", len(developerRequests))
	}
	for _, want := range []string{"repair attempt 2 of 2", ".yoyodyne/config.yaml", protectedpath.GrantMarker} {
		if !strings.Contains(developerRequests[0].Prompt, want) {
			t.Fatalf("resumed refusal is missing %q:\n%s", want, developerRequests[0].Prompt)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.PathRefusal != nil {
		t.Fatalf("integrated run kept the refusal it repaired: %#v", state.PathRefusal)
	}
}
