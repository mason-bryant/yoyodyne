package orchestrator

// A run is attributable to the account that served it.
//
// The pieces of this are tested where they live — the pool's rotation and
// budgets in internal/config, the spend and the cursor in internal/runstate, the
// environment on the command in internal/backend/claudecode — and none of that
// says the wiring holds. What makes a run attributable is that one account is
// chosen once, written onto the record, and then read back off that record by
// every invocation the run goes on to make: the first developer attempt, each
// repair, and the review of the change. A pipeline that chose again per
// invocation would pass every one of those other tests and still split one piece
// of work across two subscriptions.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// testStateRoot is where a pooled account's provider home would be. Nothing is
// written there: what is under test is which directory reaches the provider, and
// the fake provider is what receives it.
const testStateRoot = "/state"

func TestAPooledRunIsAttributableToTheAccountThatServedIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)
	// The pool is asked once and answers with the second account, so a run that
	// fell back to the configuration's own answer would be visibly the wrong one:
	// a pooled configuration names no single account.
	served := config.AccountEndpoint{Alias: "two", Directory: filepath.Join(testStateRoot, "accounts", "two")}
	pipeline.Accounts = fixedAccountChooser{endpoint: served}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != served.Alias {
		t.Fatalf("recorded account = %q, want the %q the pool chose", state.AccountAlias, served.Alias)
	}

	// The first verdict sends the change back, so this run made two developer
	// attempts and obtained two verdicts. Both halves matter: a repair is where a
	// second choice would be taken, and the review is the invocation belonging to
	// a different role.
	if attempts := provider.requestsForRole(domain.RoleDeveloper); len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	if reviews := provider.requestsForRole(domain.RoleReviewer); len(reviews) < 2 {
		t.Fatalf("the run obtained %d verdict(s), want one per attempt", len(reviews))
	}
	for index, request := range provider.requests {
		if request.AccountAlias != served.Alias || request.AccountConfigDir != served.Directory {
			t.Fatalf("invocation %d (%s) ran under %q at %q, want the run's own %q at %q",
				index, request.Role, request.AccountAlias, request.AccountConfigDir, served.Alias, served.Directory)
		}
	}
}

// A resumed run is the same run, so it is served by the account its record
// names rather than by whatever the pool would choose now. The pool is asked
// again here and answers differently; the run must ignore it.
func TestAnAccountAlreadyRecordedOutranksWhatThePoolWouldChooseNow(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)
	pipeline.Accounts = fixedAccountChooser{endpoint: config.AccountEndpoint{Alias: "one", Directory: filepath.Join(testStateRoot, "accounts", "one")}}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != "one" {
		t.Fatalf("recorded account = %q, want one", state.AccountAlias)
	}
	// Where the alias authenticates is resolved from the configuration rather
	// than carried on the record, so the record stays a name and the directory
	// stays this machine's business.
	want := filepath.Join(testStateRoot, "accounts", "one")
	for _, request := range provider.requests {
		if request.AccountConfigDir != want {
			t.Fatalf("invocation ran in %q, want the home %q authenticates in", request.AccountConfigDir, want)
		}
	}
}

// A pooled configuration names no single account, so a pipeline with no pool
// wired has nothing to attribute a run to. It refuses before the work item is
// claimed and before a provider is paid, rather than starting a run nothing
// could bill afterwards.
func TestAPooledConfigurationWithNoPoolRefusesBeforeAnythingIsClaimed(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("Run() error = nil, want a pooled configuration with nothing to choose with refused")
	}
	if tracker.claimed {
		t.Fatal("the work item was claimed by a run that could not say which account would serve it")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("a run that could not choose an account still made %d provider invocation(s)", len(provider.requests))
	}
}

// pooled turns a test pipeline into one whose project runs across two accounts,
// which is the arrangement in which the configuration itself no longer answers
// which account a run spent.
func pooled(pipeline Pipeline) Pipeline {
	pipeline.Config.Accounts = map[string]config.Account{"one": {}, "two": {}}
	pipeline.StateRoot = testStateRoot
	return pipeline
}

// fixedAccountChooser is a pool that has already decided. What a real one weighs
// — the rotation, the budgets, the run records behind both — is tested where it
// is implemented; what is under test here is that the pipeline asks once and
// then stops asking.
type fixedAccountChooser struct {
	endpoint config.AccountEndpoint
}

func (c fixedAccountChooser) ChooseAccount() (config.AccountEndpoint, error) {
	return c.endpoint, nil
}
