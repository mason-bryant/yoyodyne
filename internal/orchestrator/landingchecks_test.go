package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A landing runs the whole suite once, over the commit that landed, after the
// run is over: the run has succeeded, its item has closed, and its worktree is
// gone before the landing checks start. A green landing is recorded on the run
// and said on the item, the checkout the checks ran in is gone afterwards, and
// the checks were told they run the whole module.
func TestALandingRunsTheLandingChecksOverTheIntegratedCommitOnce(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	told := filepath.Join(t.TempDir(), "told.txt")
	ranIn := filepath.Join(t.TempDir(), "ran-in.txt")
	pipeline.Config.LandingChecks = []string{
		"test -f feature.txt",
		`printf '%s' "$YOYODYNE_CHANGED_GO_PACKAGES" > ` + told + ` && pwd > ` + ranIn,
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.closed || !outcome.WorktreeRemoved {
		t.Fatalf("outcome = %#v, closed = %t, want the run succeeded, its item closed and its worktree gone", outcome, tracker.closed)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Finished() || !landed.Green || landed.Red() || landed.Problem != "" {
		t.Fatalf("landing = %#v, want a green landing with nothing wrong around it", landed)
	}
	if landed.Commit != outcome.Integration.TargetCommit || len(landed.Checks) != 2 {
		t.Fatalf("landing = %#v, want both checks recorded over the integrated commit", landed)
	}
	if read, _ := os.ReadFile(told); string(read) != "./..." {
		t.Fatalf("the landing check was told %q, want the whole module", read)
	}
	// The checks ran in a checkout of the commit rather than in the primary
	// checkout, and that checkout is gone once they have.
	directory, _ := os.ReadFile(ranIn)
	if where := strings.TrimSpace(string(directory)); !strings.Contains(where, "landing-") || strings.HasPrefix(where, repository) {
		t.Fatalf("the landing checks ran in %q, want a landing checkout of their own", where)
	}
	if _, err := os.Stat(strings.TrimSpace(string(directory))); !os.IsNotExist(err) {
		t.Fatalf("the landing checkout is still there: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks == nil || !state.LandingChecks.Green || state.Status != runstate.StatusSucceeded {
		t.Fatalf("state = %#v, want the green landing on a succeeded run", state.LandingChecks)
	}
	notes := strings.Join(tracker.noteRecords, "\n")
	if !strings.Contains(notes, "Landing checks: green landing: 2 landing checks passed over") {
		t.Fatalf("item notes do not say the landing was green:\n%s", notes)
	}
}

// A red landing is news about the target branch and not a verdict on the run:
// the run still succeeded and its item is still closed, and what the landing
// does is file its own item — with the failing check, the commit, the run that
// landed it, and the goal the landed item served — and say so on the run and on
// the item. It blocks nothing.
func TestARedLandingFilesItsOwnItemAndBlocksNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "open",
		Notes: "Admitted by the product manager.\n\nGoal served: [reliable-delivery] Run development nearly autonomously.",
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &recordingFiler{}
	pipeline.Filer = filer
	pipeline.Config.LandingChecks = []string{"echo 'a race in package x'; test -f missing.txt", "true"}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a red landing to fail nothing", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.closed || tracker.blocked {
		t.Fatalf("outcome = %#v, closed = %t, blocked = %t, want the run succeeded and its item closed", outcome, tracker.closed, tracker.blocked)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Red() || landed.Green || len(landed.Checks) != 1 {
		t.Fatalf("landing = %#v, want a red landing stopped at its first check", landed)
	}
	if landed.FiledWorkItem != "yoyodyne-red-1" || landed.FilingProblem != "" {
		t.Fatalf("landing = %#v, want the filed item named", landed)
	}
	if len(filer.filed) != 1 {
		t.Fatalf("filed = %d items, want one", len(filer.filed))
	}
	filed := filer.filed[0]
	commit := outcome.Integration.TargetCommit[:12]
	for _, want := range []string{"Red landing on main at " + commit, "exited 1", "after yoyodyne-task integrated"} {
		if !strings.Contains(filed.Title, want) {
			t.Fatalf("filed title = %q, want it to carry %q", filed.Title, want)
		}
	}
	for _, want := range []string{"yoyodyne-task (Task)", "a race in package x", "Reproduce with", outcome.Integration.TargetCommit} {
		if !strings.Contains(filed.Description, want) {
			t.Fatalf("filed description = %q, want it to carry %q", filed.Description, want)
		}
	}
	if !strings.Contains(filed.Notes, "Goal served: [reliable-delivery] Run development nearly autonomously.") || !strings.Contains(filed.Notes, outcome.RunID) {
		t.Fatalf("filed notes = %q, want the landed item's goal and the run named", filed.Notes)
	}
	if filed.Type != "bug" || filed.Priority == nil || *filed.Priority != 1 {
		t.Fatalf("filed as %s at %v, want a bug at the front of the queue", filed.Type, filed.Priority)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks == nil || !state.LandingChecks.Red() || state.LandingChecks.FiledWorkItem != "yoyodyne-red-1" {
		t.Fatalf("state = %#v, want the red landing and its item on the run", state.LandingChecks)
	}
	notes := strings.Join(tracker.noteRecords, "\n")
	if !strings.Contains(notes, "Landing checks: red landing: echo 'a race in package x'; test -f missing.txt exited 1 over "+commit+"; filed as yoyodyne-red-1") {
		t.Fatalf("item notes do not say the landing was red and what it filed:\n%s", notes)
	}
}

// A red landing nothing can file is still a red landing: recorded as red, with
// the record saying no item could be filed and why, on a run that succeeded.
func TestARedLandingNothingCanFileIsRecordedAsSuchAndFailsNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	pipeline.Filer = &recordingFiler{refuse: errors.New("bd is busy")}
	pipeline.Config.LandingChecks = []string{"false"}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want a succeeded run", outcome, err)
	}
	if landed := outcome.LandingChecks; landed == nil || !landed.Red() || landed.FiledWorkItem != "" || landed.FilingProblem != "bd is busy" {
		t.Fatalf("landing = %#v, want a red landing whose filing was refused", landed)
	}
	if notes := strings.Join(tracker.noteRecords, "\n"); !strings.Contains(notes, "no item could be filed: bd is busy") {
		t.Fatalf("item notes do not say the filing was refused:\n%s", notes)
	}
}

// A project that configures no landing checks lands exactly as it did before
// they existed: nothing runs, nothing is recorded, and nothing is said.
func TestAProjectWithNoLandingChecksRecordsNoLanding(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || outcome.LandingChecks != nil {
		t.Fatalf("Run() = %#v, %v, want no landing recorded", outcome.LandingChecks, err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks != nil || strings.Contains(strings.Join(tracker.noteRecords, "\n"), "Landing checks:") {
		t.Fatalf("state = %#v, notes = %v, want nothing said of a landing", state.LandingChecks, tracker.noteRecords)
	}
}

// recordingFiler is a tracker that takes the items a red landing files, or
// refuses them.
type recordingFiler struct {
	filed  []beads.NewWorkItem
	refuse error
}

func (f *recordingFiler) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	if f.refuse != nil {
		return beads.WorkItem{}, f.refuse
	}
	f.filed = append(f.filed, item)
	return beads.WorkItem{ID: "yoyodyne-red-1", Title: item.Title}, nil
}
