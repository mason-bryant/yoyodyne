package orchestrator

import (
	"context"
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
	// The grant covers the file it names and nothing around it.
	if refused := protectedpath.Protect(pipeline.Config).Refused(
		[]string{"docs/designs/another-design.md"},
		protectedpath.Grants(tracker.item.Description),
	); len(refused) != 1 {
		t.Fatalf("the grant admitted more than the path it named: %v", refused)
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
