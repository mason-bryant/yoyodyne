package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every provider invocation a run makes lands in the cost log as it is made,
// charged to the part of the work it served. This is the whole of what the cost
// log is for, and it is a property of the wiring rather than of the meter: a run
// whose invocations went straight at the backend would record nothing and look
// exactly like a run that cost nothing.
func TestARunRecordsWhatEachOfItsInvocationsSpent(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add a feature",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	priceInvocations(provider, 2.5)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	log := &recordingSpendLog{}
	pipeline.Spend = log
	// The reviewer records into the same log the run does, which is what makes a
	// review's price comparable with the change's.
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel, Spend: log}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if len(log.lines) != 2 {
		t.Fatalf("recorded %d line(s), want one per provider invocation: %#v", len(log.lines), log.lines)
	}

	development, reviewed := log.lines[0], log.lines[1]
	if development.Phase != runstate.SpendPhaseDevelopment || development.Role != domain.RoleDeveloper {
		t.Errorf("first line = %#v, want the developer's first attempt", development)
	}
	if reviewed.Phase != runstate.SpendPhaseReview || reviewed.Role != domain.RoleReviewer {
		t.Errorf("second line = %#v, want the reviewer's invocation", reviewed)
	}
	for _, line := range log.lines {
		// Both invocations were made for one piece of work, and both say so: what an
		// item cost is the join this log has to be able to make.
		if line.RunID != outcome.RunID || line.WorkItemID != tracker.item.ID {
			t.Errorf("line = %#v, want the run and the item it served", line)
		}
		if !line.Known() || line.AmountUSD != 2.5 {
			t.Errorf("line = %#v, want the provider's own figure", line)
		}
		if line.AccountAlias != pipeline.Config.AccountAlias() || line.ConfigRevision != pipeline.Config.Revision() {
			t.Errorf("line = %#v, want the account and configuration the run was set up under", line)
		}
		if line.Backend != domain.BackendClaudeCode || line.Model == "" {
			t.Errorf("line = %#v, want what served the invocation", line)
		}
		if err := line.Validate(); err != nil {
			t.Errorf("recorded line does not satisfy the durable contract: %v", err)
		}
	}
}

// The first developer attempt is the development and every attempt after it is a
// repair. It is read from the run's own repair count rather than from anything
// this process remembered, so a resumed run charges its next attempt to the same
// phase the process before it would have.
func TestADeveloperAttemptAfterTheFirstIsChargedToRepair(t *testing.T) {
	t.Parallel()

	log := &recordingSpendLog{}
	cfg := spendTestConfig()
	run := &activeRun{
		pipeline: Pipeline{Config: cfg, Spend: log},
		state: runstate.State{
			RunID:          pipelineRunID,
			WorkItemID:     "yoyodyne-task",
			AccountAlias:   "default",
			ConfigRevision: cfg.Revision(),
		},
	}
	if phase := run.developmentPhase(); phase != runstate.SpendPhaseDevelopment {
		t.Fatalf("first attempt phase = %q, want the development", phase)
	}
	run.state.RepairAttempts = 1
	if phase := run.developmentPhase(); phase != runstate.SpendPhaseRepair {
		t.Fatalf("second attempt phase = %q, want a repair", phase)
	}
	run.state.RepairAttempts = 2
	if phase := run.developmentPhase(); phase != runstate.SpendPhaseRepair {
		t.Fatalf("third attempt phase = %q, want a repair", phase)
	}

	// The attribution beside it comes from the run's own record rather than from
	// the configuration as it stands now, which is what makes a resumed run's
	// spend attributable to what actually set it up.
	attribution := run.spendAttribution(domain.RoleReviewer, runstate.SpendPhaseReview)
	if attribution.Phase != runstate.SpendPhaseReview || attribution.Agent != "reviewer" {
		t.Errorf("attribution = %#v, want the reviewer's", attribution)
	}
	if attribution.RunID != pipelineRunID || attribution.WorkItemID != "yoyodyne-task" {
		t.Errorf("attribution = %#v, want the run and the item it served", attribution)
	}
	if attribution.AccountAlias != "default" || attribution.ConfigRevision != cfg.Revision() {
		t.Errorf("attribution = %#v, want what the run was set up under", attribution)
	}
	if attribution.Backend != domain.BackendClaudeCode || attribution.ProductID != "yoyodyne" {
		t.Errorf("attribution = %#v, want the configured backend and product", attribution)
	}
}

// The same split, driven through a whole run rather than by setting the counter
// by hand: a failing check sends the change back, and the attempt that answers
// it is recorded as a repair.
//
// This is the half the unit test above cannot reach. developmentPhase() reads
// the repair count at the moment the invocation is made, so which phase a real
// repair lands in is decided by whether the run increments that counter before
// or after it invokes the developer. It increments before, in activeRun.repair,
// and nothing in that function mentions the cost log — so an edit that moved the
// increment after the invocation would write every first repair as development,
// would make docs/reporting.md's new section false, and would pass every other
// test in this file.
func TestARepairAttemptIsChargedToRepairThroughAWholeRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt leaves the check failing; the repair attempt makes it
		// pass in the same worktree.
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	priceInvocations(provider, 1.5)
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	log := &recordingSpendLog{}
	pipeline.Spend = log
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel, Spend: log}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 || outcome.Integration == nil {
		t.Fatalf("the run did not repair and then integrate: %#v", outcome)
	}

	// The first attempt, the attempt that answered the failing check, and the
	// review of what passed — one line each, in the order they were made. The
	// reviewer only ever saw the change whose checks passed, so there is exactly
	// one review.
	want := []runstate.SpendPhase{
		runstate.SpendPhaseDevelopment,
		runstate.SpendPhaseRepair,
		runstate.SpendPhaseReview,
	}
	recorded := make([]runstate.SpendPhase, 0, len(log.lines))
	for _, line := range log.lines {
		recorded = append(recorded, line.Phase)
	}
	if len(recorded) != len(want) {
		t.Fatalf("recorded %v, want one line per invocation: %v", recorded, want)
	}
	for index, phase := range want {
		if recorded[index] != phase {
			t.Fatalf("recorded %v, want %v", recorded, want)
		}
	}

	// The repair is the same developer asked again for the same item, and it is
	// its own line rather than an amendment to the first attempt's.
	repair := log.lines[1]
	if repair.Role != domain.RoleDeveloper || repair.Agent != "developer" {
		t.Errorf("repair line = %#v, want the developer's", repair)
	}
	if repair.RunID != outcome.RunID || repair.WorkItemID != tracker.item.ID {
		t.Errorf("repair line = %#v, want the run and the item it served", repair)
	}
	if !repair.Known() || repair.AmountUSD != 1.5 {
		t.Errorf("repair line = %#v, want the provider's own figure", repair)
	}
	if err := repair.Validate(); err != nil {
		t.Errorf("recorded line does not satisfy the durable contract: %v", err)
	}
}

// A branch review is a provider invocation with neither a run nor a work item
// behind it, so it is charged to the review itself. Nothing else in the harness
// would ever say what one cost.
func TestABranchReviewRecordsWhatItSpentAgainstTheReview(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"the commits agree with one another"}`)
	priceInvocations(provider, 1.25)
	reviewer, _, _ := newBranchReviewer(t, repository, provider)
	log := &recordingSpendLog{}
	reviewer.Reviewer = review.Reviewer{
		Backend: provider,
		Model:   testReviewerModel,
		Clock:   fixedBranchClock{},
		Spend:   log,
	}

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want one for the review's one invocation: %#v", len(log.lines), log.lines)
	}
	line := log.lines[0]
	if line.Phase != runstate.SpendPhaseReview || line.Role != domain.RoleReviewer {
		t.Errorf("line = %#v, want a review", line)
	}
	// The review is what it belongs to, in the field that means a branch review.
	// Putting it in the run id would read as a run to whatever joins these lines
	// to run records, and there is no such run.
	if line.BranchReviewID != outcome.ReviewID {
		t.Errorf("line = %#v, want the review it was made for", line)
	}
	if line.RunID != "" || line.WorkItemID != "" {
		t.Errorf("line = %#v, want no run and no work item behind it", line)
	}
	if !line.Known() || line.AmountUSD != 1.25 {
		t.Errorf("line = %#v, want the provider's own figure", line)
	}
	if err := line.Validate(); err != nil {
		t.Errorf("recorded line does not satisfy the durable contract: %v", err)
	}
}

// priceInvocations makes a fake provider report what each invocation cost, the
// way a real one does on its terminal. Without it every line would be classified
// unknown, which is a true answer about a provider that said nothing and not the
// property these tests are about.
func priceInvocations(provider *fakeBackend, amount float64) {
	inner := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil {
			return result, err
		}
		result.CostUSD = amount
		result.CostReported = true
		return result, nil
	}
}

// spendTestConfig is the least a configuration has to say for a run's spend to
// be attributable: which agent fills each role, and on what.
func spendTestConfig() config.Config {
	return config.Config{
		Version: config.CurrentVersion,
		Product: config.Product{ID: "yoyodyne", RepositoryID: "yoyodyne"},
		Agents: map[string]config.AgentConfig{
			"developer": {Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: testDeveloperModel, Instances: 1},
			"reviewer":  {Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: testReviewerModel, Instances: 1},
		},
	}
}

// recordingSpendLog is the cost log as a test reads it back.
type recordingSpendLog struct {
	lines []runstate.Spend
}

func (l *recordingSpendLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return nil
}
