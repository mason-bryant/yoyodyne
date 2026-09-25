package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
)

// verificationBlock is the record as a developer's reply carries it.
func verificationBlock(payload string) string {
	return selfcheck.Fence + "\n" + payload + "\n```\n"
}

// The defect this whole gate comes from: yoyodyne-ifd.149 wrote a guard,
// reported it delivered, and closed its work item with its shell dead the whole
// time and nothing it wrote ever having been run. A change whose developer
// records no execution of its own is now handed back for it, and a run that
// spends its attempts that way stops in front of a person rather than reaching a
// reviewer — which is the step that used to turn an unrun change into an
// integrated one.
func TestAChangeNobodyRanIsHandedBackAndNeverReachesAReviewer(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerRecordsNoExecution = true
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "recorded no execution") {
		t.Fatalf("Run() error = %v, want the run stopped for want of an execution record", err)
	}
	if reviews := provider.requestsForRole(domain.RoleReviewer); len(reviews) != 0 {
		t.Fatalf("a change nobody ran reached a reviewer %d time(s)", len(reviews))
	}
	// It is handed back first, because the record is something the developer can
	// still produce: the budget is spent on attempts rather than on one refusal.
	if developed := provider.requestsForRole(domain.RoleDeveloper); len(developed) != 3 {
		t.Fatalf("developer invocations = %d, want the attempt and both repairs", len(developed))
	}
	attempts := provider.requestsForRole(domain.RoleDeveloper)
	handed := attempts[1].Prompt
	if !strings.Contains(handed, "Execution evidence: repair required") || !strings.Contains(handed, "probe you ran") {
		t.Errorf("the hand-back does not say what is owed: %q", handed)
	}
	if !tracker.blocked {
		t.Fatalf("the item was not blocked for a person; calls = %v", tracker.calls)
	}
	if !strings.Contains(tracker.blockReason, "without recording that it had executed") {
		t.Errorf("the blocker does not say what stopped the run: %q", tracker.blockReason)
	}
	recorded := loadedRun(t, store, outcome.RunID)
	if recorded.Verification == nil || len(recorded.Verification.Owed) == 0 {
		t.Fatalf("the run does not record what the developer still owed: %#v", recorded.Verification)
	}
}

// The other half of the operator's own line, which he drew mechanically rather
// than leaving it to a developer's judgement: a change touching only content no
// declared check reads submits on the probe alone. Demanding a suite run for a
// change the suite never reads prices honesty out and teaches padding.
func TestAChangeNoDeclaredCheckReadsSubmitsOnTheProbeAlone(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "avatar.png"), []byte("not really an image\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "replaced the avatar\n\n" +
		verificationBlock(`{"probe":{"command":"make build","outcome":"passed"}}`)
	// The leeway is available only where the composition ledger is an account of
	// the project being asked about, which means the checks this repository
	// declares. They are not run here — what is under test is the gate in front
	// of them — so the suite is answered by a runner that reports them passing.
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, ledgerChecks)
	pipeline.Checks = passingChecks{}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a change nothing checks was refused for want of a check run: %#v", outcome)
	}
	if attempts := provider.requestsForRole(domain.RoleDeveloper); len(attempts) != 1 {
		t.Fatalf("developer invocations = %d, want the one attempt", len(attempts))
	}
}

// A probe the developer could not start at all is the environment refusing
// rather than the work failing, so the run ends on it naming what refused
// instead of spending its repair budget, its reviewer, and the rest of its
// context against a wall that was already named in the first reply. This is the
// 2026-08-23 shape, where every shell a run could start died on the operating
// system's argument limit and the run found out at first use, an hour in.
func TestAProbeTheEnvironmentRefusedEndsTheRunNamingIt(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	provider.developerFinalText = "I cannot run anything in this worktree.\n\n" +
		verificationBlock(`{"probe":{"command":"make build","outcome":"refused",`+
			`"detail":"could not start /bin/zsh: the command line plus environment exceed the OS exec argument limit"}}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "argument limit") {
		t.Fatalf("Run() error = %v, want the run ended naming what refused", err)
	}
	if attempts := provider.requestsForRole(domain.RoleDeveloper); len(attempts) != 1 {
		t.Fatalf("developer invocations = %d, want the run to have stopped on the first reply", len(attempts))
	}
	if reviews := provider.requestsForRole(domain.RoleReviewer); len(reviews) != 0 {
		t.Fatalf("a run whose environment cannot execute bought %d review(s)", len(reviews))
	}
	recorded := loadedRun(t, store, outcome.RunID)
	if recorded.Environmental == nil || recorded.Environmental.Cause != runstate.CauseSandboxSpawnFailure {
		t.Fatalf("environmental refusal = %#v, want the spawn failure recorded", recorded.Environmental)
	}
	if !strings.Contains(recorded.Environmental.Detail, "argument limit") {
		t.Errorf("the refusal does not say what refused: %q", recorded.Environmental.Detail)
	}
}

// A probe that ran and came back red is the opposite finding, and the harness
// must not file it as a broken sandbox: the environment executed exactly as the
// probe asks it to prove, and what is red is the commit the run was cut from.
// Recording that as an environmental refusal would end the run, charge it
// nothing, and point triage at a machine that is working.
func TestAProbeThatRanAndFailedIsNotAnEnvironmentalRefusal(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the base commit is red; my own change is below\n\n" +
		verificationBlock(`{"probe":{"command":"make test","outcome":"failed",`+
			`"detail":"TestSomethingElse fails on the base commit: want 2, got 3"},`+
			`"checks":[{"command":"make test ./internal/orchestrator/...","outcome":"passed"}]}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Environmental != nil {
		t.Fatalf("a probe that ran was filed as an environmental refusal: %#v", outcome.Environmental)
	}
	recorded := loadedRun(t, store, outcome.RunID)
	if recorded.Environmental != nil {
		t.Fatalf("the run records an environmental cause it never had: %#v", recorded.Environmental)
	}
	// The run carries on exactly as one whose probe passed: the record is kept,
	// the change is checked, and the failure the probe saw is the reviewer's to
	// read rather than the harness's to classify.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("the run did not carry on past a probe that ran: %#v", outcome)
	}
	if recorded.Verification == nil || recorded.Verification.Probe == nil ||
		recorded.Verification.Probe.Outcome != runstate.VerificationFailed {
		t.Fatalf("verification = %#v, want the failing probe kept as it was recorded", recorded.Verification)
	}
	reviews := provider.requestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 || !strings.Contains(reviews[0].Prompt, "ran and failed") {
		t.Errorf("the reviewer was not shown that the probe ran and failed")
	}
}

// What the developer executed reaches the reviewer beside the change, because a
// reviewer judges evidence: a change whose author never ran it is a change
// offered on a claim, and nothing else in the request says which of the two is
// in front of it.
func TestWhatTheDeveloperExecutedReachesTheReviewer(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "implemented the work item\n\n" +
		verificationBlock(`{"probe":{"command":"make build","outcome":"passed"},`+
			`"checks":[{"command":"make test ./internal/orchestrator/...","outcome":"passed"}]}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.requestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("review invocations = %d, want one", len(reviews))
	}
	for _, shown := range []string{"What the developer executed", "make build", "make test ./internal/orchestrator/..."} {
		if !strings.Contains(reviews[0].Prompt, shown) {
			t.Errorf("the reviewer was not shown %q", shown)
		}
	}
	// And the block itself is not left in the summary as well as in the record,
	// which is what keeps the account of the work readable as prose.
	if strings.Contains(reviews[0].Prompt, selfcheck.Fence) {
		t.Errorf("the raw block reached the reviewer's evidence: %q", reviews[0].Prompt)
	}
}

// A landing claim the harness cannot read takes everything after its own fence
// with it, so the record is read out of the whole reply rather than out of what
// the claim left. A developer that ran its checks and then wrote one malformed
// claim has still run them, and handing it back for evidence it recorded would
// be a repair round spent on the harness's own reading.
func TestAnUnreadableLandingClaimDoesNotCostTheExecutionRecord(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "worked on it\n\n" +
		landingBlock(`{"outcome":"partly","why":"some of it"}`) +
		verificationBlock(`{"probe":{"command":"make build","outcome":"passed"},`+
			`"checks":[{"command":"make test","outcome":"passed"}]}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("the change did not integrate: %#v", outcome)
	}
	if attempts := provider.requestsForRole(domain.RoleDeveloper); len(attempts) != 1 {
		t.Fatalf("developer invocations = %d, want no repair spent on a record it had written", len(attempts))
	}
	recorded := loadedRun(t, store, outcome.RunID)
	if recorded.Verification == nil || recorded.Verification.Probe == nil || len(recorded.Verification.Owed) != 0 {
		t.Fatalf("verification = %#v, want the record the developer wrote", recorded.Verification)
	}
	// The unreadable claim still withholds the closure, which is the other half
	// and is unchanged by this.
	if outcome.WorkItemClosed {
		t.Fatal("the item closed on a claim nobody could read")
	}
}

// A block nobody could read is not the same thing as no block, and the developer
// that wrote one is told which it was. Both fail the gate — a record the harness
// cannot read is a record it cannot gate on — and the difference is what the
// hand-back has to say, or the developer is told to write something it already
// wrote.
func TestAnUnreadableVerificationBlockSaysSoRatherThanReadingAsNoRecord(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerRecordsNoExecution = true
	provider.developerFinalTextByAttempt = []string{
		"implemented the work item\n\n" + verificationBlock(`{"probe":{"command":"make build","outcome":"ran"}}`),
		"implemented the work item\n\n" +
			verificationBlock(`{"probe":{"command":"make build","outcome":"passed"},`+
				`"checks":[{"command":"make test","outcome":"passed"}]}`),
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("the second attempt's readable record did not carry the change through: %#v", outcome)
	}
	attempts := provider.requestsForRole(domain.RoleDeveloper)
	if len(attempts) != 2 {
		t.Fatalf("developer invocations = %d, want the attempt and one hand-back", len(attempts))
	}
	if !strings.Contains(attempts[1].Prompt, "could not be read") {
		t.Errorf("the hand-back does not say the block was unreadable: %q", attempts[1].Prompt)
	}
}

// Every attempt re-earns the record. A repair attempt that altered the change
// must not be able to stand on the evidence the attempt before it recorded,
// because that evidence was about a change this one has since edited.
func TestEachAttemptRecordsItsOwnExecutionsRatherThanInheritingThem(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerRecordsNoExecution = true
	provider.developerFinalTextByAttempt = []string{
		"implemented the work item\n\n" +
			verificationBlock(`{"probe":{"command":"make build","outcome":"passed"},`+
				`"checks":[{"command":"make test","outcome":"passed"}]}`),
		"tidied it up",
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The first attempt's record carried the run through the gate.
	if reviews := provider.requestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("review invocations = %d, want the one the first attempt earned", len(reviews))
	}

	// And a second run whose first attempt records nothing does not inherit
	// anything either, which is the same statement from the other side.
	second := newOutcomeTracker()
	bare := roleBackend(writeFeature, approveVerdict)
	bare.developerRecordsNoExecution = true
	barePipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), second, bare, []string{"exit 0"})
	if _, err := barePipeline.Run(context.Background(), second.item.ID); err == nil {
		t.Fatal("a run that recorded nothing at all was let through")
	}
}

// loadedRun reads the durable record back, which is where the verification and
// the environmental refusal live: the listing a surface reads is a summary of
// it, and what these are about is what the run wrote down.
func loadedRun(t *testing.T, store *runstate.Store, runID string) runstate.State {
	t.Helper()

	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

// ledgerChecks are the checks this repository declares, which is what makes the
// composition ledger an account of the project a test drives: the leeway for
// content nothing reads is granted only where the two agree.
var ledgerChecks = []string{"make fmtcheck", "make test", "make race", "make vet"}

// passingChecks answers a suite without running it, for a test about the gate in
// front of the checks rather than about the checks.
type passingChecks struct{}

func (passingChecks) Run(_ context.Context, _, _ string, commands []string, lastSequence uint64, _ func(execution.Event) error) ([]checks.Result, uint64, error) {
	results := make([]checks.Result, 0, len(commands))
	for _, command := range commands {
		results = append(results, checks.Result{
			Command: command,
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		})
	}
	return results, lastSequence, nil
}

// A field stored short is cut on a rune boundary, so what the record keeps of a
// check's detail is still text.
func TestABoundedSelfCheckFieldIsCutOnARuneBoundary(t *testing.T) {
	t.Parallel()

	const limit = 64
	cut := bounded("x"+strings.Repeat("é", limit), limit)
	if !utf8.ValidString(cut) || len(cut) > limit || len(cut) < limit-1 {
		t.Fatalf("bounded() = %q (%d bytes), want valid text within %d bytes and no shorter than a rune under it", cut, len(cut), limit)
	}
}
