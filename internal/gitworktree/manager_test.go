package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const testRunID = "run-0123456789abcdef0123456789abcdef"

func TestManagerCreateInspectPreserveAndCleanup(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newManager(t, repository, worktreeRoot)
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1.2", BaseRef: "HEAD", TargetBranch: "main"}

	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Branch != "yoyodyne/yoyodyne-1-2/01234567" {
		t.Fatalf("branch = %q", worktree.Branch)
	}
	if !commitPattern.MatchString(worktree.BaseCommit) {
		t.Fatalf("base commit = %q", worktree.BaseCommit)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.Registered || inspection.Dirty || inspection.Branch != worktree.Branch {
		t.Fatalf("Inspect() = %#v", inspection)
	}

	// Cleanup only ever acts on a worktree that really integrated, so this
	// promotes one first rather than passing the base commit off as an
	// integrated one: the base is in the target by construction and would make
	// the containment proof vacuous.
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	dirtyPath := filepath.Join(worktree.Path, "failure.txt")
	if err := os.WriteFile(dirtyPath, []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cleanupRequest := CleanupRequest{Worktree: worktree, TargetBranch: "main", SourceCommit: integration.SourceCommit}
	if _, err := manager.CleanupIntegrated(context.Background(), cleanupRequest); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("CleanupIntegrated() dirty error = %v", err)
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Fatalf("dirty worktree was not preserved: %v", err)
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	cleanup, err := manager.CleanupIntegrated(context.Background(), cleanupRequest)
	if err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v, want both artifacts removed", cleanup)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestManagerCreatesWorktreeFromResolvedBaseCommit(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-base", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var worktreeBase string
	for _, command := range runner.commands {
		for index := 0; index+3 < len(command); index++ {
			if command[index] == "worktree" && command[index+1] == "add" {
				worktreeBase = command[len(command)-1]
			}
		}
	}
	if worktreeBase != worktree.BaseCommit {
		t.Fatalf("git worktree base = %q, want resolved commit %q", worktreeBase, worktree.BaseCommit)
	}
}

func TestManagerReturnsCreatedIdentityWhenPostCreateInspectionFails(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &postCreateFailureRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-partial", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "verify created worktree") {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Path == "" || worktree.Branch == "" || worktree.BaseCommit == "" {
		t.Fatalf("Create() discarded created identity: %#v", worktree)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("created worktree is not recoverable at %s: %v", worktree.Path, err)
	}
}

func TestManagerRejectsReuseAndDirtyPrimaryRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	if _, err := manager.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || (!strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "uncommitted")) {
		t.Fatalf("second Create() error = %v", err)
	}

	repository = newRepository(t)
	manager = newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if err := os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("Create() dirty error = %v", err)
	}
}

// Development is parallel, so several runs can be given a worktree at the same
// instant — which is this repository's own configuration. Git's bookkeeping
// under .git/worktrees has no lock of its own: an add registers the new entry
// and then fills it in, so two adds at that instant can have one read the
// other's half-written entry and exit outright, losing a run to nothing but
// timing. Creation queues now, and every run that asked for a worktree gets one.
//
// The overlap is what this asserts rather than only the eight successes. Git's
// own window is a few microseconds wide and a machine that happens not to hit it
// would pass this test with nothing serializing anything at all, which is
// precisely the reassurance that let the race reach production in the first
// place.
func TestManagerCreatesConcurrentWorktreesWithoutLosingAny(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &creationWitness{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const concurrent = 8
	// Every creation is released at once rather than one happening to be finished
	// before the next starts, because the window this is about is only open while
	// an add is in flight.
	start := make(chan struct{})
	worktrees := make([]Worktree, concurrent)
	failures := make([]error, concurrent)
	var creating sync.WaitGroup
	for index := range concurrent {
		creating.Add(1)
		go func() {
			defer creating.Done()
			<-start
			worktrees[index], failures[index] = manager.Create(context.Background(), CreateRequest{
				RunID:      fmt.Sprintf("run-%08x%s", index, strings.Repeat("0", 24)),
				WorkItemID: fmt.Sprintf("yoyodyne-concurrent-%d", index),
				BaseRef:    "HEAD",
			})
		}()
	}
	close(start)
	creating.Wait()

	registered := gitOutput(t, repository, "worktree", "list", "--porcelain")
	for index := range concurrent {
		if failures[index] != nil {
			t.Fatalf("Create(%d) error = %v", index, failures[index])
		}
		if !strings.Contains(registered, worktrees[index].Path) {
			t.Fatalf("worktree %d at %s is not registered:\n%s", index, worktrees[index].Path, registered)
		}
	}
	adds, overlaps := runner.observed()
	if adds != concurrent {
		t.Fatalf("git worktree add ran %d time(s), want one per creation", adds)
	}
	if overlaps != 0 {
		t.Fatalf("%d worktree creation(s) started while another was still in flight", overlaps)
	}
}

// What two runs race over is the repository's bookkeeping rather than the
// configuration that points at it, so creations queue across managers and not
// only within one. Two products aimed at one repository have separate worktree
// roots and one worktrees/ directory between them, and two `yoyo run`
// invocations have separate processes and the same directory; a queue held in a
// manager would serialize neither.
func TestManagerCreationQueuesAcrossManagersOnOneRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &creationWitness{delegate: execution.OSProcessRunner{}}
	const managers, each = 2, 3
	start := make(chan struct{})
	var creating sync.WaitGroup
	failures := make([]error, managers*each)
	for group := range managers {
		manager, err := New(Options{
			Runner:         runner,
			RepositoryRoot: repository,
			WorktreeRoot:   filepath.Join(t.TempDir(), fmt.Sprintf("worktrees-%d", group)),
		})
		if err != nil {
			t.Fatalf("New(%d) error = %v", group, err)
		}
		for index := range each {
			creating.Add(1)
			go func() {
				defer creating.Done()
				<-start
				_, failures[group*each+index] = manager.Create(context.Background(), CreateRequest{
					RunID:      fmt.Sprintf("run-%08x%s", group*each+index, strings.Repeat("0", 24)),
					WorkItemID: fmt.Sprintf("yoyodyne-shared-%d-%d", group, index),
					BaseRef:    "HEAD",
				})
			}()
		}
	}
	close(start)
	creating.Wait()

	for index, failure := range failures {
		if failure != nil {
			t.Fatalf("Create(%d) error = %v", index, failure)
		}
	}
	adds, overlaps := runner.observed()
	if adds != managers*each {
		t.Fatalf("git worktree add ran %d time(s), want one per creation", adds)
	}
	if overlaps != 0 {
		t.Fatalf("%d worktree creation(s) started while another manager's was still in flight", overlaps)
	}
}

// creationWitness records what is true at the moment a worktree is registered:
// whether another registration is already in flight. It watches the Git command
// itself rather than the manager's method, because the bookkeeping two runs
// race over is written by that command and by nothing else.
type creationWitness struct {
	delegate execution.ProcessRunner
	mu       sync.Mutex
	adding   int
	adds     int
	overlaps int
}

func (w *creationWitness) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if !containsArguments(command.Args, "worktree", "add") {
		return w.delegate.Run(ctx, command, observer)
	}
	w.started()
	defer w.finished()
	return w.delegate.Run(ctx, command, observer)
}

func (w *creationWitness) started() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.adds++
	if w.adding > 0 {
		w.overlaps++
	}
	w.adding++
}

func (w *creationWitness) finished() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.adding--
}

func (w *creationWitness) observed() (adds, overlaps int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.adds, w.overlaps
}

// The creation lease serializes writers against each other, and nothing
// serializes a reader against them: a creation holds the lease while it
// verifies what it made, which is itself a listing, so a listing that waited
// for the lease would wait for itself. That leaves `git worktree list` able to
// cross an entry another run has registered and not yet filled in, where it
// fails the whole command rather than skipping the one entry — observed as
// `failed to read .git/worktrees/<other run>/commondir` in a run doing nothing
// but summarizing its own change. So the listing is read again, and a run is
// not lost to an instant that has already passed.
func TestManagerReadsTheWorktreeListingAgainWhenItCrossesACreation(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-crossed",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want the half-written listing to have been read again", err)
	}
	if listings, refused := runner.observed(); refused != 1 || listings < 2 {
		t.Fatalf("listings = %d after %d refusal(s), want the refusal to have been followed by another read", listings, refused)
	}
	if _, err := manager.Inspect(context.Background(), worktree); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
}

// A listing that keeps failing is still a failure, reported with what Git said.
// The retry is for the instant that passes; a repository Git cannot describe at
// all must not be waited on forever or reported as empty.
func TestManagerReportsAWorktreeListingThatKeepsFailing(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: worktreeListAttempts}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-unreadable",
		BaseRef:    "HEAD",
	})
	if err == nil {
		t.Fatal("Create() succeeded with a listing Git never answered")
	}
	if !strings.Contains(err.Error(), "list worktrees failed with exit code 128") || !strings.Contains(err.Error(), "commondir") {
		t.Fatalf("Create() error = %v, want it to carry what Git said", err)
	}
	if listings, _ := runner.observed(); listings != worktreeListAttempts {
		t.Fatalf("listings = %d, want %d attempts and no more", listings, worktreeListAttempts)
	}
}

// A listing that timed out is not the passing instant, and it has already spent
// the command's whole budget. Reading it again would make a slow repository
// three times slower to fail, so the retry is for a Git that ran and refused
// and for nothing else.
func TestManagerDoesNotReadAWorktreeListingAgainAfterATimeout(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &listRefusingRunner{delegate: execution.OSProcessRunner{}, refusals: 1, status: execution.ProcessTimedOut}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-slow",
		BaseRef:    "HEAD",
	})
	if err == nil {
		t.Fatal("Create() succeeded with a listing that never answered")
	}
	if listings, _ := runner.observed(); listings != 1 {
		t.Fatalf("listings = %d, want the timeout to have been believed the first time", listings)
	}
}

// listRefusingRunner fails the first refusals listings the way Git fails one
// that crossed a creation: the whole command exits 128 over a single entry it
// could not read. status is what the refusal is reported as, so a refusal and a
// listing that never answered can be told apart. Everything else runs for real,
// so what is being tested is the manager's own reading of the bookkeeping rather
// than a simulation of Git.
type listRefusingRunner struct {
	delegate execution.ProcessRunner
	status   execution.ProcessStatus
	mu       sync.Mutex
	refusals int
	refused  int
	listings int
}

func (r *listRefusingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if !containsArguments(command.Args, "worktree", "list") {
		return r.delegate.Run(ctx, command, observer)
	}
	r.mu.Lock()
	r.listings++
	refuse := r.refused < r.refusals
	if refuse {
		r.refused++
	}
	r.mu.Unlock()
	if refuse {
		status := r.status
		if status == "" {
			status = execution.ProcessFailed
		}
		return execution.ProcessResult{
			Status:   status,
			ExitCode: 128,
			Stderr:   "fatal: failed to read .git/worktrees/yoyodyne-other-2113a23c/commondir: Result too large\n",
		}, nil
	}
	return r.delegate.Run(ctx, command, observer)
}

func (r *listRefusingRunner) observed() (listings, refused int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listings, r.refused
}

// Git prunes worktree registrations during automatic maintenance, and it judges
// one stale by whether its gitdir file is there — which is precisely what an add
// still filling its entry in has not written yet. A prune reaching that window
// deletes the registration out from under the add, and the run is lost. The
// registry lease cannot queue a prune Git starts for itself, so no command the
// harness runs is allowed to start one; that is a property of every command
// rather than of the few that write, because maintenance is triggered by
// ordinary writing commands and prunes the whole repository when it runs.
func TestManagerGitCommandsNeverStartAutomaticMaintenance(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The whole lifecycle is driven, because the assertion is about every command
	// the harness issues and the writing ones are what start maintenance.
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-maintenance",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if _, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	}); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}

	if len(runner.commands) == 0 {
		t.Fatal("no Git commands were recorded")
	}
	for _, command := range runner.commands {
		// The settings have to lead: Git reads -c only before the subcommand, so
		// one that arrived after it would be an argument to the subcommand and
		// disable nothing.
		if len(command) < len(maintenanceOptions) || !reflect.DeepEqual(command[:len(maintenanceOptions)], maintenanceOptions) {
			t.Fatalf("git %v does not disable automatic maintenance first", command)
		}
	}
}

// A removal writes the same bookkeeping an add does, and Git guards neither, so
// a creation that reached a half-deleted entry would exit rather than create
// anything. Removal therefore queues on the registry lease. The lease is held
// here from outside the removal, so what this observes is the removal waiting
// for it rather than two commands that happened not to overlap.
func TestManagerRemovalQueuesOnTheWorktreeRegistryLease(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &removalWitness{delegate: execution.OSProcessRunner{}, removing: make(chan struct{}, 1)}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-removal-queue",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	cleaned := make(chan error, 1)
	go func() {
		_, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
			Worktree:     worktree,
			TargetBranch: worktree.TargetBranch,
			SourceCommit: integration.SourceCommit,
		})
		cleaned <- err
	}()

	// Nothing here waits for the removal to be attempted, because the assertion
	// is that it is not. A machine slow enough to make this window uninformative
	// still cannot fail it wrongly: only a removal that actually ran does that.
	select {
	case <-runner.removing:
		t.Fatal("the worktree was unregistered while the registry lease was held elsewhere")
	case err := <-cleaned:
		t.Fatalf("CleanupIntegrated() finished without waiting for the lease: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := lease.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	if err := <-cleaned; err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
		t.Fatalf("worktree registration survived cleanup: %q", registrations)
	}
}

// removalWitness announces the moment the harness unregisters a worktree, which
// is the one step of a cleanup the registry lease has to hold back.
type removalWitness struct {
	delegate execution.ProcessRunner
	removing chan struct{}
}

func (w *removalWitness) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if containsArguments(command.Args, "worktree", "remove") {
		select {
		case w.removing <- struct{}{}:
		default:
		}
	}
	return w.delegate.Run(ctx, command, observer)
}

func TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "interactions.jsonl"), []byte("base control state\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() base control state error = %v", err)
	}
	runGit(t, repository, "add", ".beads/interactions.jsonl")
	runGit(t, repository, "commit", "-m", "add control state")
	for _, name := range []string{"interactions.jsonl", "issues.jsonl"} {
		if err := os.WriteFile(filepath.Join(repository, ".beads", name), []byte("control state\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := manager.ValidateReady(context.Background()); err != nil {
		t.Fatalf("ValidateReady() allowed changes error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "product.go"), []byte("product change\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() product error = %v", err)
	}
	if err := manager.ValidateReady(context.Background()); err == nil || !strings.Contains(err.Error(), "product.go") {
		t.Fatalf("ValidateReady() product error = %v", err)
	}
}

func TestManagerSummarizeChangesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-task",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "README.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() tracked error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() untracked error = %v", err)
	}

	summary, err := manager.SummarizeChanges(context.Background(), worktree)
	if err != nil {
		t.Fatalf("SummarizeChanges() error = %v", err)
	}
	if !strings.Contains(summary.Status, "M README.txt") || !strings.Contains(summary.Status, "?? new.txt") {
		t.Fatalf("status = %q", summary.Status)
	}
	if !strings.Contains(summary.DiffStat, "README.txt") {
		t.Fatalf("diff stat = %q", summary.DiffStat)
	}

	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.SummarizeChanges(context.Background(), worktree); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("SummarizeChanges() committed HEAD error = %v", err)
	}
}

func TestManagerUnifiedChangesCoversTrackedAndUntrackedWorkWithoutMutating(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-diff", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, "new.txt", "brand new\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "should not be reviewed\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	for _, want := range []string{
		"diff --git a/README.txt b/README.txt",
		"+edited",
		"diff --git a/new.txt b/new.txt",
		"+brand new",
		"diff --git a/sub/odd name.txt b/sub/odd name.txt",
		"+nested",
	} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch is missing %q:\n%s", want, changes.Patch)
		}
	}
	if strings.Contains(changes.Patch, "should not be reviewed") {
		t.Errorf("patch includes an ignored file:\n%s", changes.Patch)
	}
	wantUntracked := []string{".gitignore", "new.txt", "sub/odd name.txt"}
	if !reflect.DeepEqual(changes.UntrackedFiles, wantUntracked) {
		t.Errorf("untracked files = %#v, want %#v", changes.UntrackedFiles, wantUntracked)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Errorf("changes = %#v, want an untruncated change", changes)
	}
	if !strings.Contains(changes.Status, "?? new.txt") || !strings.Contains(changes.DiffStat, "README.txt") {
		t.Errorf("changes summary = %#v", changes)
	}

	// Inspecting a change must never stage or otherwise alter what the
	// developer left behind.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
	if staged := gitOutput(t, worktree.Path, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("inspection staged files: %q", staged)
	}
}

func TestManagerChangedPathsNamesBothSidesOfEveryChange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-paths", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A tracked file moved somewhere else. A caller deciding what a change was
	// allowed to touch has to see the path it left as well as the one it
	// arrived at, because moving a file out of a directory is as much of an
	// edit to that directory as writing in it.
	if err := os.Rename(filepath.Join(worktree.Path, "README.txt"), filepath.Join(worktree.Path, "MOVED.txt")); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "should not be gated\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	changed, err := manager.ChangedPaths(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	want := []string{".gitignore", "MOVED.txt", "README.txt", "sub/odd name.txt"}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("ChangedPaths() = %#v, want %#v", changed, want)
	}
	// Inspecting a change never alters it, here for the same reason the unified
	// diff never does.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
}

func TestManagerChangedPathsSeesWhatAnAttemptAlreadyCommitted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-committed", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	// Publishing commits each developer attempt, so a listing built from
	// uncommitted status would report a published change as touching nothing.
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "-m", "yoyodyne: published attempt")
	worktree.HarnessCommit = gitLine(t, worktree.Path, "rev-parse", "HEAD")

	changed, err := manager.ChangedPaths(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"feature.txt"}) {
		t.Fatalf("ChangedPaths() = %#v, want the committed change", changed)
	}
}

func TestManagerUnifiedChangesEnforcesDiffBounds(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "big.txt", strings.Repeat("a line of new content\n", 200))
	writeFile(t, worktree.Path, "small.txt", "small\n")

	perFile, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFileBytes: 64})
	if err != nil {
		t.Fatalf("UnifiedChanges() per-file error = %v", err)
	}
	if !perFile.Truncated || !reflect.DeepEqual(perFile.OmittedFiles, []string{"big.txt"}) {
		t.Fatalf("per-file bound = %#v", perFile)
	}
	if !reflect.DeepEqual(perFile.UntrackedFiles, []string{"small.txt"}) {
		t.Fatalf("per-file untracked = %#v", perFile.UntrackedFiles)
	}
	if strings.Contains(perFile.Patch, "a line of new content") {
		t.Fatalf("patch included an oversized file:\n%s", perFile.Patch)
	}

	counted, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: 1})
	if err != nil {
		t.Fatalf("UnifiedChanges() file-count error = %v", err)
	}
	if len(counted.UntrackedFiles) != 1 || !counted.Truncated || len(counted.OmittedFiles) != 1 {
		t.Fatalf("file-count bound = %#v", counted)
	}

	// A total bound clamps to whole lines rather than cutting a diff mid-line.
	total, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() total error = %v", err)
	}
	if !total.Truncated {
		t.Fatalf("total bound = %#v", total)
	}
	if len(total.Patch) > 200 {
		t.Fatalf("patch is %d bytes, want at most 200", len(total.Patch))
	}
	if total.Patch != "" && !strings.HasSuffix(total.Patch, "\n") {
		t.Fatalf("clamped patch ends mid-line: %q", total.Patch)
	}

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: -1}); err == nil {
		t.Fatal("UnifiedChanges() negative limit error = nil")
	}
}

func TestManagerUnifiedChangesMarksBinaryContentIncomplete(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-binary", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "changed\x00binary\n")
	writeFile(t, worktree.Path, "new.bin", "new\x00binary\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if !changes.Truncated {
		t.Fatalf("binary changes were reported as complete: %#v", changes)
	}
	if !reflect.DeepEqual(changes.OmittedFiles, []string{"new.bin"}) {
		t.Fatalf("omitted files = %#v, want new.bin", changes.OmittedFiles)
	}
	if !strings.Contains(changes.Patch, "Binary files a/README.txt and b/README.txt differ") {
		t.Fatalf("tracked binary change is not disclosed in patch:\n%s", changes.Patch)
	}
	if strings.Contains(changes.Patch, "new.bin") {
		t.Fatalf("unreviewable untracked binary was included in patch:\n%s", changes.Patch)
	}
}

func TestManagerUnifiedChangesRejectsAgentOwnedCommits(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-commit", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "new.txt", "new\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{}); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("UnifiedChanges() committed HEAD error = %v", err)
	}
}

func TestManagerRejectsUnsafeRootsAndTamperedOwnership(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := execution.OSProcessRunner{}
	for _, root := range []string{string(filepath.Separator), filepath.Join(repository, "worktrees"), filepath.Dir(repository)} {
		if _, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: root}); err == nil {
			t.Errorf("New() root %q error = nil", root)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tampered := worktree
	tampered.Path = repository
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned path") {
		t.Fatalf("Inspect() tampered error = %v", err)
	}
	tampered = worktree
	tampered.Branch = "main"
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned branch") {
		t.Fatalf("Inspect() branch error = %v", err)
	}
}

// A test's own teardown is part of what it asserts: a run that passes every
// check and then fails deleting its temporary directory is still a red required
// check, and one that is usually spurious is one people stop reading. Both
// halves of that are guarded here, because both are a line someone can delete
// without any test noticing.
func TestRepositoryUnderTestLeavesNothingForTempDirToRace(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	// Writing commands otherwise detach "git maintenance run --auto", which
	// outlives them and is still working inside .git when TempDir deletes it.
	for _, setting := range []struct{ name, want string }{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
	} {
		value, err := attemptGit(repository, "config", "--get", setting.name)
		if err != nil || strings.TrimSpace(value) != setting.want {
			t.Errorf("%s = %q (%v), want %q", setting.name, strings.TrimSpace(value), err, setting.want)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-teardown", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "left behind\n")

	// Calling the cleanup here proves it removes a dirty worktree that was never
	// integrated. TempDir deletes directories and unregisters nothing, so an
	// unregistered repository is the state this has to reach.
	removeLinkedWorktrees(t, repository)
	if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
		t.Errorf("worktree registration survived cleanup: %q", registrations)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory survived cleanup: %v", err)
	}

	// A second worktree is left registered on purpose, so the cleanup
	// newRepository registered has something real to remove rather than an
	// already-empty repository. Its own assertions fail this test if any
	// registration survives into TempDir's removal, which is the case that
	// every other test in this package relies on.
	survivor, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-teardown-survivor", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() survivor error = %v", err)
	}
	writeFile(t, survivor.Path, "work.txt", "left for teardown\n")
}

func newManager(t *testing.T, repository, worktreeRoot string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}

func newRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	disableBackgroundMaintenance(t, repository)
	// Registered after the first TempDir call above and therefore run before
	// TempDir's removal, so the repository is idle by the time Go deletes it.
	t.Cleanup(func() { removeLinkedWorktrees(t, repository) })
	if err := os.WriteFile(filepath.Join(repository, "README.txt"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "initial")
	return repository
}

// disableBackgroundMaintenance stops Git from handing this repository to a
// process that outlives the command which started it. Writing commands
// otherwise start "git maintenance run --auto --detach", which daemonizes into
// its own session: it escapes both the caller's wait and the process group the
// runner kills, and it is still working inside .git when a test's TempDir
// cleanup deletes that directory. A directory Go has already emptied and that
// then gains an entry again is exactly what makes the removal fail. Nothing
// under test needs maintenance, so none is started.
func disableBackgroundMaintenance(t *testing.T, repository string) {
	t.Helper()
	runGit(t, repository, "config", "maintenance.auto", "false")
	runGit(t, repository, "config", "gc.auto", "0")
}

// removeLinkedWorktrees makes a test responsible for the worktrees it
// registered. TempDir deletes directories and unregisters nothing, so a
// registration inside .git outlives the checkout it names and leaves the
// repository holding state no test still describes.
func removeLinkedWorktrees(t *testing.T, repository string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repository, ".git")); err != nil {
		return
	}
	for _, path := range linkedWorktreePaths(t, repository) {
		// A worktree whose directory a test deleted on purpose cannot be
		// removed, only pruned. One that is still on disk must come off here.
		if output, err := attemptGit(repository, "worktree", "remove", "--force", path); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("cleanup could not remove worktree %s: %v: %s", path, err, output)
			}
		}
	}
	if output, err := attemptGit(repository, "worktree", "prune"); err != nil {
		t.Errorf("cleanup could not prune worktree registrations: %v: %s", err, output)
	}
	if remaining := linkedWorktreePaths(t, repository); len(remaining) > 0 {
		t.Errorf("cleanup left worktree registrations behind: %v", remaining)
	}
}

// linkedWorktreePaths names every worktree registered against repository apart
// from the primary checkout, which is the repository itself.
func linkedWorktreePaths(t *testing.T, repository string) []string {
	t.Helper()
	listing, err := attemptGit(repository, "worktree", "list", "--porcelain")
	if err != nil {
		t.Errorf("cleanup could not list worktrees: %v: %s", err, listing)
		return nil
	}
	var paths []string
	for _, line := range strings.Split(listing, "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || samePath(path, repository) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	if output, err := attemptGit(repository, args...); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

// attemptGit runs a Git command whose failure is the caller's to interpret,
// rather than a test failure at the point of the call.
func attemptGit(repository string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func gitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v error = %v", args, err)
	}
	return string(output)
}

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", relative, err)
	}
}

type recordingProcessRunner struct {
	delegate execution.ProcessRunner
	commands [][]string
	// bounds keeps every command as it was issued, so a test can hold Git to the
	// flat deadline that is exactly right for it.
	bounds []execution.Command
}

func (r *recordingProcessRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	r.bounds = append(r.bounds, command)
	return r.delegate.Run(ctx, command, observer)
}

type postCreateFailureRunner struct {
	delegate execution.ProcessRunner
	created  bool
}

func (r *postCreateFailureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.created && containsArguments(command.Args, "worktree", "list") {
		return execution.ProcessResult{}, errors.New("injected post-create inspection failure")
	}
	result, err := r.delegate.Run(ctx, command, observer)
	if err == nil && result.Status == execution.ProcessSucceeded && containsArguments(command.Args, "worktree", "add") {
		r.created = true
	}
	return result, err
}

func containsArguments(arguments []string, first, second string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == first && arguments[index+1] == second {
			return true
		}
	}
	return false
}

// A Git command's duration is known and short, so a flat deadline is exactly
// the right bound for it. The activity bound that keeps a working provider
// alive would only mistake a quiet Git command for a stalled one.
func TestManagerBoundsGitCommandsByAFlatDeadlineOnly(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "main"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(runner.bounds) == 0 {
		t.Fatal("no Git commands were recorded")
	}
	for _, command := range runner.bounds {
		if command.Timeout <= 0 {
			t.Fatalf("git %v carries no deadline", command.Args)
		}
		if command.IdleTimeout != 0 {
			t.Fatalf("git %v carries an idle bound of %s", command.Args, command.IdleTimeout)
		}
	}
}
