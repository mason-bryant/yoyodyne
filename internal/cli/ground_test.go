package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The conversation asks the repository and the tracker how old its picture is
// through this, which is a compile-time fact worth stating where it is built.
var _ chat.Ground = conversationGround{}

// What has moved is two cheap questions: what the repository holds that the
// picture did not, and what the tracker wrote down after it was taken.
func TestMovementCountsCommitsAgainstTheRecordedCommitAndTrackerChangesAfterTheMoment(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	gathered := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	writeInteractions(t, repository,
		interaction("yoyodyne-ifd.1", gathered.Add(-time.Hour)),
		interaction("yoyodyne-ifd.2", gathered.Add(time.Minute)),
		interaction("yoyodyne-ifd.3", gathered.Add(time.Hour)),
		"{ this line is not an interaction",
	)

	runner := &recordingRunner{stdout: "14\n"}
	ground := conversationGround{runner: runner, repository: repository, gitBinary: "git", timeout: time.Second}
	movement := ground.Movement(context.Background(), chat.Briefing{GatheredAt: gathered, Commit: "a1a1a1a1"})

	if movement.Commits != 14 || movement.RepositoryProblem != "" {
		t.Fatalf("movement = %#v, want the commits the repository reported", movement)
	}
	// The question asked is what HEAD holds that the picture did not, which is
	// the exact question — a commit's own date answers a different one.
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	command := runner.commands[0]
	if command.Name != "git" || command.Dir != repository ||
		strings.Join(command.Args, " ") != "rev-list --count a1a1a1a1..HEAD" {
		t.Fatalf("git command = %#v", command)
	}
	// Only what the tracker recorded after the picture was taken counts, and one
	// unreadable line is not a log that could not be read.
	if movement.TrackerChanges != 2 || movement.TrackerProblem != "" {
		t.Fatalf("movement = %#v, want the two changes after the picture", movement)
	}
}

// A comparison that could not be made is named rather than counted as nothing.
// "0 commits" from a broken comparison is the same confident staleness the
// freshness line exists to end.
func TestMovementReportsWhatItCouldNotCompare(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	ground := conversationGround{
		runner:     &failingRunner{err: errors.New("git is not installed")},
		repository: repository,
		gitBinary:  "git",
		timeout:    time.Second,
	}

	// A picture with no recorded commit cannot be compared against, and says so
	// without running anything.
	movement := ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now()})
	if !strings.Contains(movement.RepositoryProblem, "was not recorded") || movement.Commits != 0 {
		t.Fatalf("movement = %#v", movement)
	}
	// A repository that will not answer is reported the same way.
	movement = ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now(), Commit: "a1a1a1a1"})
	if !strings.Contains(movement.RepositoryProblem, "git is not installed") {
		t.Fatalf("movement = %#v", movement)
	}
	// A repository with no tracker at all has recorded nothing, which is an
	// answer rather than a failure.
	if movement.TrackerChanges != 0 || movement.TrackerProblem != "" {
		t.Fatalf("movement = %#v, want an absent tracker read as no changes", movement)
	}
	// A repository that has a tracker and no exported log is not the same
	// thing, and saying "no tracker changes" there would be false currency.
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	movement = ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now(), Commit: "a1a1a1a1"})
	if !strings.Contains(movement.TrackerProblem, "has not exported its interactions log") {
		t.Fatalf("movement = %#v, want an unexported log reported as unknown", movement)
	}
	// A picture with no recorded moment is a comparison that cannot be made.
	movement = ground.Movement(context.Background(), chat.Briefing{Commit: "a1a1a1a1"})
	if !strings.Contains(movement.TrackerProblem, "was not recorded") {
		t.Fatalf("movement = %#v", movement)
	}
}

// The picture records what it was taken against, because a comparison made
// later has nothing else to go on.
func TestGatherRecordsWhenAndWhatItWasTakenAgainst(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	specifications := filepath.Join(repository, "docs", "product")
	if err := os.MkdirAll(specifications, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(specifications, "brief.md"),
		[]byte("# Brief\n\nWhat this is for.\n\n## Goals\n\n- ship bounded work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	taken := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	ground := conversationGround{
		// The tracker answers with no items and Git answers with a commit; both
		// are the same runner, which is what the conversation actually has.
		runner:         &scriptedRunner{outputs: map[string]string{"bd": "[]", "git": "a1a1a1a1a1a1\n"}},
		repository:     repository,
		specifications: "docs/product",
		gitBinary:      "git",
		clock:          stoppedClock{at: taken},
		timeout:        time.Second,
	}
	briefing, err := ground.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if !briefing.GatheredAt.Equal(taken) || briefing.Commit != "a1a1a1a1a1a1" {
		t.Fatalf("briefing = %#v, want what it was taken against", briefing)
	}
	if !strings.Contains(briefing.Text, "What this is for.") {
		t.Fatalf("briefing text = %q, want the specification in it", briefing.Text)
	}
	if len(briefing.Problems) != 0 {
		t.Fatalf("problems = %#v, want none", briefing.Problems)
	}
}

// A specification nobody can read, and a tracker that will not answer, are
// reported rather than failing the picture: a conversation with a partial
// picture that says so is worth more than no conversation.
func TestGatherReportsWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	ground := conversationGround{
		runner:         &failingRunner{err: errors.New("bd is not installed")},
		repository:     t.TempDir(),
		specifications: "docs/product",
		gitBinary:      "git",
		timeout:        time.Second,
	}
	briefing, err := ground.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	problems := strings.Join(briefing.Problems, "\n")
	if !strings.Contains(problems, "Beads state is unavailable") || !strings.Contains(problems, "no specification was found") {
		t.Fatalf("problems = %#v", briefing.Problems)
	}
	if !strings.Contains(briefing.Text, "Beads state is unavailable") {
		t.Fatalf("briefing text = %q, want the tracker named as unread rather than empty", briefing.Text)
	}
	// A repository that will not name its commit still yields a usable picture;
	// the comparison that needs the commit reports itself as unknown later.
	if briefing.Commit != "" {
		t.Fatalf("briefing commit = %q", briefing.Commit)
	}
}

// A log too large to read is a comparison that was not made. Stopping at the
// bound would drop the newest entries — the only ones being counted — and
// render as "nothing has moved in the tracker", which is the one answer this
// must never give without having earned it.
func TestATrackerLogLargerThanTheBoundIsReportedRatherThanTruncated(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	gathered := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	recent := interaction("yoyodyne-ifd.2", gathered.Add(time.Hour))
	writeInteractions(t, repository,
		interaction("yoyodyne-ifd.1", gathered.Add(-time.Hour)),
		recent,
	)

	// Read to a bound the log fits inside, and the newest entry is counted.
	changes, err := trackerChangesSince(repository, gathered, 1<<20)
	if err != nil || changes != 1 {
		t.Fatalf("trackerChangesSince() = %d, %v, want the one change after the picture", changes, err)
	}
	// Read to a bound it does not fit inside, and the count is refused rather
	// than answered from the part that fitted.
	changes, err = trackerChangesSince(repository, gathered, len(recent))
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("trackerChangesSince() = %d, %v, want the bound reported", changes, err)
	}
	if changes != 0 {
		t.Fatalf("trackerChangesSince() = %d, want no count beside the failure", changes)
	}
}

// The counting is checked against a real repository as well as a scripted one,
// because what is being claimed is about Git rather than about how the harness
// spells a command: a picture taken two commits ago is two commits old.
func TestMovementCountsCommitsInARealRepository(t *testing.T) {
	t.Parallel()

	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	git(t, repository, "init", "-b", "main")
	git(t, repository, "config", "user.name", "Yoyodyne Test")
	git(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, repository, "first")

	ground := conversationGround{
		runner:     execution.OSProcessRunner{},
		repository: repository,
		gitBinary:  "git",
		timeout:    30 * time.Second,
	}
	head, err := ground.head(context.Background())
	if err != nil {
		t.Fatalf("head() error = %v", err)
	}

	if movement := ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now(), Commit: head}); movement.Commits != 0 {
		t.Fatalf("movement = %#v, want nothing moved yet", movement)
	}
	commit(t, repository, "second")
	commit(t, repository, "third")
	movement := ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now(), Commit: head})
	if movement.Commits != 2 || movement.RepositoryProblem != "" {
		t.Fatalf("movement = %#v, want the two commits since the picture", movement)
	}

	// A commit the repository has never heard of is a comparison that cannot be
	// made, and is reported rather than counted as nothing.
	gone := ground.Movement(context.Background(), chat.Briefing{GatheredAt: time.Now(), Commit: "0123456789abcdef0123456789abcdef01234567"})
	if gone.RepositoryProblem == "" {
		t.Fatalf("movement = %#v, want a comparison it could not make", gone)
	}
}

func git(t *testing.T, repository string, args ...string) {
	t.Helper()

	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

func commit(t *testing.T, repository, name string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(repository, name+".txt"), []byte(name+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	git(t, repository, "add", name+".txt")
	git(t, repository, "commit", "-m", name)
}

func interaction(id string, at time.Time) string {
	return fmt.Sprintf(`{"id":"int-%s","kind":"field_change","created_at":%q,"issue_id":%q}`,
		strings.ReplaceAll(id, ".", ""), at.Format(time.RFC3339Nano), id)
}

func writeInteractions(t *testing.T, repository string, lines ...string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, interactionsLog),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// failingRunner is a command that cannot be run at all, which is how a missing
// Git or bd behaves.
type failingRunner struct{ err error }

func (r *failingRunner) Run(context.Context, execution.Command, execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{}, r.err
}

// scriptedRunner answers each command by name, so one runner can stand in for
// both the tracker and Git exactly as the real one does.
type scriptedRunner struct{ outputs map[string]string }

func (r *scriptedRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.outputs[command.Name]}, nil
}

type stoppedClock struct{ at time.Time }

func (c stoppedClock) Now() time.Time { return c.at }

// The docket reaches the development manager by being gathered with everything
// else the conversation opens with. Nobody has to carry it there, which is the
// whole of what the goal this serves asks for.
func TestGatherCarriesTheTriageDocketToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	stopped := stoppedRunState(t)
	ground := conversationGround{
		runner:         &scriptedRunner{outputs: map[string]string{"bd": "[]", "git": "a1a1a1a1a1a1\n"}},
		repository:     t.TempDir(),
		specifications: "docs/product",
		docket:         docketerOverRuns(t, stopped),
		gitBinary:      "git",
		timeout:        time.Second,
	}
	briefing, err := ground.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, required := range []string{
		"## Triage docket",
		"[stopped run]",
		"the repair budget was spent",
		"Branch (preserved): yoyodyne/task/abc",
	} {
		if !strings.Contains(briefing.Text, required) {
			t.Fatalf("briefing is missing %q:\n%s", required, briefing.Text)
		}
	}
}

// What the development manager decided is what takes the stoppage out of the
// next conversation she opens. The docket is rebuilt from the same durable run
// records every time it is gathered, so without the closure the same entry is
// gathered again for ever — and a decision that spends no budget leaves nothing
// else the harness can read.
func TestADecisionTakesTheStoppageOffTheDocketTheNextConversationGathers(t *testing.T) {
	t.Parallel()

	runs := stoppedRunState(t)
	store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	docketer := docketerOverDocket(runs, store)
	built, err := docketer.Build()
	if err != nil || len(built.Entries) != 1 {
		t.Fatalf("Build() = %#v, error = %v, want the stoppage docketed", built, err)
	}

	closer := conversationDocketLog{store: store, clock: stoppedClock{at: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)}}
	decided := chat.DocketClosure{
		RunID:     built.Entries[0].RunID,
		Decision:  "escalate",
		Reason:    "the findings dispute the item's criteria",
		DecidedBy: "the development manager in conversation chat-0123456789abcdef",
	}
	// A wait answers a publication the forge has not finished, and this run has
	// none: closing on it would take a live question off the docket.
	waited := decided
	waited.Decision = "wait"
	waited.Classes = []triage.Class{triage.ClassPublication}
	if closed, err := closer.Close(context.Background(), waited); err != nil || closed != 0 {
		t.Fatalf("Close() = %d, error = %v, want nothing closed by a decision this stoppage is not", closed, err)
	}

	decided.Classes = []triage.Class{triage.ClassStoppedRun}
	closed, err := closer.Close(context.Background(), decided)
	if err != nil || closed != 1 {
		t.Fatalf("Close() = %d, error = %v, want the stopped run's entry closed", closed, err)
	}

	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if len(rebuilt.Entries) != 0 || rebuilt.Added != 0 || rebuilt.Closed != 1 {
		t.Fatalf("second build = %#v, want the decided stoppage closed and nothing docketed again", rebuilt)
	}
	ground := conversationGround{
		runner:         &scriptedRunner{outputs: map[string]string{"bd": "[]", "git": "a1a1a1a1a1a1\n"}},
		repository:     t.TempDir(),
		specifications: "docs/product",
		docket:         docketer,
		gitBinary:      "git",
		timeout:        time.Second,
	}
	briefing, err := ground.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	// A docket with nothing undecided on it renders no section at all, which is
	// what a product where nothing has stopped renders: either way there is
	// nothing waiting on her.
	if strings.Contains(briefing.Text, "Triage docket") || strings.Contains(briefing.Text, "the repair budget was spent") {
		t.Fatalf("the decided stoppage is still in the context:\n%s", briefing.Text)
	}
}

// Every other role gathers no docket at all: deciding what becomes of stopped
// work belongs to one role, and a section the reader cannot act on is one every
// conversation pays for and reads past.
func TestGatherCarriesNoDocketForARoleThatCannotActOnIt(t *testing.T) {
	t.Parallel()

	if docketer := conversationDocket(components{}, domain.RoleProductManager); docketer != nil {
		t.Fatalf("the product manager was wired a triage docket")
	}
	// Nor the log itself: a role that cannot decide about a stoppage must not be
	// able to close one either.
	if entries := conversationDocketEntries(components{}, domain.RoleProductManager); entries != nil {
		t.Fatalf("the product manager was wired the docket to close entries on")
	}
	ground := conversationGround{
		runner:         &scriptedRunner{outputs: map[string]string{"bd": "[]", "git": "a1a1a1a1a1a1\n"}},
		repository:     t.TempDir(),
		specifications: "docs/product",
		gitBinary:      "git",
		timeout:        time.Second,
	}
	briefing, err := ground.Gather(context.Background())
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if strings.Contains(briefing.Text, "Triage docket") {
		t.Fatalf("a role with no docket was given one:\n%s", briefing.Text)
	}
}

// The durable budget a triage decision spends is wired for the role that spends
// it, over the product's own record and the configured caps. A conversation
// wired without one refuses every decision that spends a budget, which is a
// failure only the real binary would show: every conversation test supplies its
// own budget, so a mistake here leaves the suite green and the development
// manager unable to grant a repair.
func TestTheDevelopmentManagerIsWiredTheProductsTriageBudget(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	parts, err := buildComponents(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}

	// Deciding what becomes of stopped work is one role's, and so is spending
	// what the decision costs.
	for _, role := range []domain.AgentRole{
		domain.RoleProductManager, domain.RoleArchitect, domain.RoleDeveloper, domain.RoleReviewer,
	} {
		if budgets := conversationTriage(parts, role); budgets != nil {
			t.Fatalf("the %s was wired a triage budget", role)
		}
	}

	budgets := conversationTriage(parts, domain.RoleDevelopmentManager)
	if budgets == nil {
		t.Fatal("the development manager was wired no triage budget, so every decision that spends one would be refused")
	}
	// Spending through it is what says it is wired to something usable: the
	// right store, caps that permit the decision, and a grant of the configured
	// size rather than of zero.
	granted, err := budgets.GrantRepair(context.Background(), "yoyodyne-ifd.90")
	if err != nil {
		t.Fatalf("GrantRepair() through the wired budget error = %v", err)
	}
	if granted.Rounds != orchestrator.TriageRepairGrantRounds(parts.config.Triage) || granted.Truncated {
		t.Fatalf("granted = %+v, want the configured grant in full", granted)
	}
	if _, err := budgets.RecordRerun(context.Background(), "yoyodyne-ifd.90"); err != nil {
		t.Fatalf("RecordRerun() through the wired budget error = %v", err)
	}
	if _, err := budgets.RecordMergeRearm(context.Background(), "yoyodyne-ifd.90"); err != nil {
		t.Fatalf("RecordMergeRearm() through the wired budget error = %v", err)
	}

	// It is the product's own record under the state root, which is what
	// `yoyo status` reads back: a budget wired to a store of its own would bound
	// nothing across conversations.
	counters, err := parts.store.Triage().Counters("yoyodyne-ifd.90")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RepairGrants != 1 || counters.Reruns != 1 || counters.MergeRearms != 1 {
		t.Fatalf("counters after spending through the wired budget = %+v", counters)
	}
	// And the caps it enforces are the assembled ones, so the second of each is
	// refused rather than counted.
	if _, err := budgets.GrantRepair(context.Background(), "yoyodyne-ifd.90"); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("a second grant through the wired budget error = %v, want a cap refusal", err)
	}
	if _, err := budgets.RecordRerun(context.Background(), "yoyodyne-ifd.90"); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("a second re-run through the wired budget error = %v, want a cap refusal", err)
	}
}

// The budgets a decision spends and the docket that same conversation reads are
// one record. A development manager who records a re-run and then finds the
// docket showing nothing decided is how one authorized recovery is nearly spent
// twice, once by them and once by whoever is helping them.
func TestTheDocketReportsWhatTheWiredBudgetSpent(t *testing.T) {
	// Not parallel: the state root the components read is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	parts, err := buildComponents(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	if err := parts.store.Create(stoppedRunOf("yoyodyne-ifd.90")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	budgets := conversationTriage(parts, domain.RoleDevelopmentManager)
	if _, err := budgets.RecordRerun(context.Background(), "yoyodyne-ifd.90"); err != nil {
		t.Fatalf("RecordRerun() through the wired budget error = %v", err)
	}

	built, err := docketerFrom(parts).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("docket = %#v, want the stopped run", built.Entries)
	}
	entry := built.Entries[0]
	if entry.Counters.Reruns != 1 || entry.Counters.RerunsCap != 1 || entry.Counters.RerunsCarriedOut != 0 {
		t.Fatalf("counters = %#v, want the decision the budget just recorded", entry.Counters)
	}
	if !strings.Contains(entry.Render(), "already recorded and not yet carried out") {
		t.Fatalf("the entry does not show the decision as authorized:\n%s", entry.Render())
	}
	// The guard refuses a second against the same record the entry just showed,
	// which is the whole of what makes what it showed worth reading.
	if _, err := budgets.RecordRerun(context.Background(), "yoyodyne-ifd.90"); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("a second re-run through the wired budget error = %v, want a cap refusal", err)
	}
}

// stoppedRunState records one run that ended on a durable blocker, which is
// what a docket build has to find.
func stoppedRunState(t *testing.T) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := store.Create(stoppedRunOf("yoyodyne-task")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store
}

// stoppedRunOf is one run of a work item that ended on a durable blocker.
func stoppedRunOf(workItemID string) runstate.State {
	completed := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseReviewing,
		StartedAt:     completed.Add(-time.Hour),
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		WorktreePath:  "/state/worktrees/task",
		Branch:        "yoyodyne/task/abc",
		BaseCommit:    strings.Repeat("a", 40),
		ReviewRounds:  3,
		Blocker:       "Yoyodyne stopped this item: the repair budget was spent.",
	}
}

func docketerOverRuns(t *testing.T, runs *runstate.Store) *orchestrator.Docketer {
	t.Helper()
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewDocketStore() error = %v", err)
	}
	return docketerOverDocket(runs, docket)
}

func docketerOverDocket(runs *runstate.Store, docket *runstate.DocketStore) *orchestrator.Docketer {
	triage := config.Triage{StuckMergeAge: config.Duration(2 * time.Hour), ReviewRoundsCap: 4, RepairGrantAttempts: 2}
	return &orchestrator.Docketer{
		Docket: docket,
		Runs:   runs,
		// The same durable records the conversation's triage budgets spend, so the
		// docket the development manager reads and the guards that refuse a
		// decision are one record rather than two.
		Decisions: runs.Triage(),
		Reruns:    runs.Reruns(),
		Caps:      orchestrator.TriageCaps(config.Execution{IntegrationRetriesBeforeReconciliation: 1}, triage),
		Triage:    triage,
	}
}
