package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The developer implements the item and, on the way, finds the design it was
// working from wrong. It says so through the one channel it has: the change is
// recorded for the architect to decide, the run integrates exactly as it would
// have, and no document was touched.
func TestAProposedChangeIsRecordedWithoutChangingWhatTheRunDid(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	design := writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "implemented the work item\n\n" +
		reportBlock(`{"severity":"note","message":"worth knowing"}`) +
		"\n" + amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	recorder := &fakeAmendments{}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = &fakeReports{}
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("a proposal changed what the run did: %#v", outcome)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a readable proposal was reported as a problem: %q", outcome.AmendmentProblem)
	}
	if len(recorder.appended) != 1 || len(outcome.Amendments) != 1 {
		t.Fatalf("recorded = %#v", recorder.appended)
	}
	proposed := recorder.appended[0]
	// The harness resolved who is being asked; the developer only named the
	// document.
	if proposed.Owner != domain.RoleArchitect || proposed.Kind != artifact.KindDesign || proposed.Artifact != "v1-design" {
		t.Fatalf("proposal = %#v", proposed)
	}
	if proposed.Role != domain.RoleDeveloper || proposed.RunID != outcome.RunID || proposed.WorkItemID != tracker.item.ID {
		t.Fatalf("proposal is not attributed to the run that made it: %#v", proposed)
	}
	// Both channels came out of one reply, and neither took the other with it.
	if len(outcome.Reports) != 1 {
		t.Fatalf("reports = %#v", outcome.Reports)
	}
	if outcome.Summary != "implemented the work item" {
		t.Fatalf("summary = %q", outcome.Summary)
	}
	// Nothing an unapproved proposal contains reaches the artifact. The document
	// is byte for byte what it was before the run that argued with it.
	current, err := os.ReadFile(filepath.Join(repository, design))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(current), "The ordering is unspecified.") || strings.Contains(string(current), "say which ordering holds") {
		t.Fatalf("the proposal reached the document:\n%s", current)
	}
}

// The other owner, reached the same way. The architect does not execute yet, so
// the product manager is the only owner a proposal can actually be delivered to
// in conversation, and a mechanism that resolved only the architect's documents
// would leave that half unreachable while every check still passed. The goals
// are the product manager's, they live in the specifications home rather than
// beside the designs, and a developer proposing a change to one has to arrive at
// the product manager.
func TestAChangeProposedToTheProductManagersOwnDocumentReachesTheProductManager(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	goals := writeGoalsArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-goals","change":"say what recovery means for a killed run","why":"the goal cannot be told apart from the one above it"}`)
	recorder := &fakeAmendments{}
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a change to a recorded goals document was not resolvable: %q", outcome.AmendmentProblem)
	}
	if len(recorder.appended) != 1 {
		t.Fatalf("recorded = %#v", recorder.appended)
	}
	proposed := recorder.appended[0]
	if proposed.Owner != domain.RoleProductManager || proposed.Kind != artifact.KindGoals || proposed.Artifact != "v1-goals" {
		t.Fatalf("proposal = %#v", proposed)
	}
	// The conversation delivers to an owner by role, so a proposal that resolved
	// to the product manager is one that conversation would carry.
	if pending := amendment.PendingFor([]amendment.Record{{Proposal: &proposed}}, domain.RoleProductManager); len(pending) != 1 {
		t.Fatalf("the proposal is not pending for the role it names: %#v", pending)
	}
	// And it is still a proposal rather than an edit: the goals say what they said.
	current, err := os.ReadFile(filepath.Join(repository, goals))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(current), "say what recovery means") {
		t.Fatalf("the proposal reached the document:\n%s", current)
	}
}

// A developer that could not be talked out of its argument makes it again on
// every repair attempt. That is one disagreement, and it has to arrive as one
// proposal: minting a fresh id per attempt would put the same argument in the
// queue up to repair_attempts_before_replan times and make whoever decides
// answer it once per copy to clear it.
func TestTheSameArgumentMadeAgainOnARepairAttemptIsOneProposal(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		// The first attempt leaves the check failing, so the developer is asked
		// again and says the same thing about the design both times.
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "worked on it\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	recorder := &fakeAmendments{}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the developer asked twice", outcome.RepairAttempts)
	}
	if len(recorder.appended) != 1 || len(outcome.Amendments) != 1 {
		t.Fatalf("one argument made twice produced %d proposal(s): %#v", len(recorder.appended), recorder.appended)
	}
	// Dropping the repeat is not the same as losing it: the first one is recorded
	// and is the one waiting on the architect.
	if recorder.appended[0].Artifact != "v1-design" || recorder.appended[0].Owner != domain.RoleArchitect {
		t.Fatalf("proposal = %#v", recorder.appended[0])
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a dropped repeat was reported as a lost proposal: %q", outcome.AmendmentProblem)
	}
}

// A different argument on a later attempt is a different proposal, so the
// deduplication cannot swallow something new the developer found.
func TestADifferentChangeOnARepairAttemptIsItsOwnProposal(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"worked on it\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"say which ordering holds","why":"the item cannot satisfy both"}`),
		"worked on it\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"say what happens when the queue is empty","why":"nothing here answers it"}`),
	}
	recorder := &fakeAmendments{}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(recorder.appended) != 2 || len(outcome.Amendments) != 2 {
		t.Fatalf("two different arguments produced %d proposal(s): %#v", len(recorder.appended), recorder.appended)
	}
	if recorder.appended[0].Change == recorder.appended[1].Change {
		t.Fatalf("the same change was recorded twice: %#v", recorder.appended)
	}
}

// A proposal the harness cannot read, cannot resolve, or cannot keep is named on
// the outcome and costs the run nothing. A run that failed because an agent
// argued with the design would teach every agent to stop arguing with it.
func TestAProposalThatCannotBeRecordedNeverFailsTheRun(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		reply    string
		recorder *fakeAmendments
		want     string
	}{
		"unreadable block": {
			reply:    "implemented the work item\n\n" + amendment.Fence + "\n{\"proposals\":[]}\n```\n",
			recorder: &fakeAmendments{},
			want:     "at least one change",
		},
		"document nobody records": {
			reply:    "implemented the work item\n\n" + amendmentBlock(`{"artifact":"invented","change":"x","why":"y"}`),
			recorder: &fakeAmendments{},
			want:     "no owner to decide",
		},
		"log refuses the write": {
			reply:    "implemented the work item\n\n" + amendmentBlock(`{"artifact":"v1-design","change":"x","why":"y"}`),
			recorder: &fakeAmendments{err: errors.New("the amendment log is read-only")},
			want:     "read-only",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			writeDesignArtifact(t, repository)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := roleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			provider.developerFinalText = fixture.reply
			pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
			pipeline.Amendments = fixture.recorder

			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
				t.Fatalf("a lost proposal changed what the run did: %#v", outcome)
			}
			if len(outcome.Amendments) != 0 || !strings.Contains(outcome.AmendmentProblem, fixture.want) {
				t.Fatalf("outcome proposal evidence = %#v, %q", outcome.Amendments, outcome.AmendmentProblem)
			}
			// The account of the work survives whichever way the proposal went.
			if !strings.HasPrefix(outcome.Summary, "implemented the work item") {
				t.Fatalf("summary = %q", outcome.Summary)
			}
		})
	}
}

// A pipeline with nowhere to record says so once rather than losing the
// proposals silently, which is what a developer telling nobody looks like from
// the outside.
func TestAPipelineWithNowhereToRecordSaysTheProposalWasLost(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"x","why":"y"}`)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outcome.AmendmentProblem, "nothing records proposed amendments") {
		t.Fatalf("amendment problem = %q", outcome.AmendmentProblem)
	}
}

// The proposal has to still be there when somebody decides it, which is in
// another process and long after this run's own state is gone. Every other test
// here records through a fake, so the one seam that decides whether the channel
// works at all — the collected proposal reaching the log on disk, and a later
// reader finding it through the same calls `yoyo amendment list` makes — is the
// seam none of them cross. This crosses it with a real store, a real file, and a
// second reader opened over the same root the way a separate process opens it.
func TestAProposedChangeIsOnDiskForALaterProcessToList(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// Named once and used both to write the block and to check what comes back,
	// so the assertion cannot drift into agreeing with its own copy of the text.
	const (
		change = "say which ordering holds"
		why    = "the item cannot satisfy both"
	)
	provider.developerFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"v1-design","change":"`+change+`","why":"`+why+`"}`)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Reports = &fakeReports{}

	stateRoot := t.TempDir()
	// Keyed to the pipeline's own product rather than to a literal, because a
	// store built for a different product refuses every proposal the run makes
	// while the run carries on succeeding — a silent drop nothing else here
	// would catch.
	recorder, err := runstate.NewAmendmentStore(stateRoot, pipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	pipeline.Amendments = recorder

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.AmendmentProblem != "" {
		t.Fatalf("a readable proposal was reported as a problem: %q", outcome.AmendmentProblem)
	}
	if len(outcome.Amendments) != 1 {
		t.Fatalf("the run reported %d proposal(s): %#v", len(outcome.Amendments), outcome.Amendments)
	}

	// A second store over the same root is what another process is: nothing this
	// run held in memory carries into it.
	later, err := runstate.NewAmendmentStore(stateRoot, pipeline.Config.Product.ID)
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	records, err := later.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	// Read the way the operator's command reads it, so a proposal that is on disk
	// but that `yoyo amendment list` would not show still fails here.
	waiting := amendment.Pending(records)
	if len(waiting) != 1 {
		t.Fatalf("a proposal the run recorded is not waiting to be decided: %#v", records)
	}
	proposed := waiting[0]
	if proposed.Artifact != "v1-design" || proposed.Kind != artifact.KindDesign || proposed.Owner != domain.RoleArchitect {
		t.Fatalf("proposal = %#v", proposed)
	}
	if proposed.Role != domain.RoleDeveloper || proposed.RunID != outcome.RunID || proposed.WorkItemID != tracker.item.ID {
		t.Fatalf("proposal is not attributed to the run that made it: %#v", proposed)
	}
	// Identity and attribution say which proposal this is and who made it, but
	// the argument itself is the whole of what the architect decides. A defect
	// that wrote those two fields away would leave everything above this passing
	// while the operator reads an empty proposal — the same silent drop, one
	// layer in.
	if proposed.Change != change || proposed.Why != why {
		t.Fatalf("the proposal's argument did not survive the disk round trip: change = %q, why = %q", proposed.Change, proposed.Why)
	}
	// The proposal the operator will decide is the one the run said it made, so
	// an outcome that reports a proposal the log does not hold cannot pass.
	if proposed.ID != outcome.Amendments[0].ID {
		t.Fatalf("recorded proposal %q is not the one the run reported, %q", proposed.ID, outcome.Amendments[0].ID)
	}
}

func TestTheDeveloperContractSaysHowToProposeAChange(t *testing.T) {
	t.Parallel()

	// A developer told to propose upstream changes and given no mechanism has
	// only two moves left, both bad, so the mechanism is stated in the contract
	// where a persona cannot weaken it — and repeated on a repair attempt for the
	// same reason the rest of the contract is.
	for name, prompt := range map[string]string{
		"first attempt": developerPrompt("", "", "# Assigned work item\n", "/scratch"),
		"check repair":  checkRepairPrompt("", "/scratch", runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}, 1, 2),
	} {
		for _, required := range []string{amendment.Fence, "A proposal is not an edit you have written in advance"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("%s prompt is missing %q", name, required)
			}
		}
	}
}

// The failure this ends, from run-6ff896ba: a developer named a document the
// repository does not record, the refusal reached the operator and nobody else,
// and the developer went on to write into a checked-in file that it had raised a
// proposal nothing was holding. So the refusal is put in front of that developer
// before it can be asked anything again — and it is spent by the reply to that
// invocation, because a refusal repeated on every attempt is one the developer
// answers over and over.
func TestARefusedProposalIsPutInFrontOfTheDeveloperThatMadeIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		// Two failing checks, so the developer is invoked three times: the attempt
		// that proposes, the one that is told, and one more that must not be told
		// again.
		if attempts < 3 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"worked on it\n\n" + amendmentBlock(`{"artifact":"invented","change":"say which ordering holds","why":"the item cannot satisfy both"}`),
		"worked on it",
	}
	command := `test -f feature.txt || { echo "feature.txt is missing" >&2; exit 3; }`
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{command})
	pipeline.Amendments = &fakeAmendments{}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The refusal still costs the run nothing, which is the property the carry-back
	// must not have quietly changed.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("a refused proposal changed what the run did: %#v", outcome)
	}
	requests := provider.requestsForRole(domain.RoleDeveloper)
	if len(requests) != 3 {
		t.Fatalf("developer invocations = %d, want the attempt that proposed and two repairs", len(requests))
	}
	// Nothing had been refused yet when the proposing attempt was asked.
	if strings.Contains(requests[0].Prompt, "A change you proposed was not recorded") {
		t.Fatalf("the first attempt was told about a refusal that had not happened:\n%s", requests[0].Prompt)
	}
	// The next one opens with it, in the harness's own words rather than a
	// paraphrase, and is told not to leave the claim behind in the change.
	told := requests[1].Prompt
	if !strings.HasPrefix(told, "# A change you proposed was not recorded") {
		t.Fatalf("the next invocation does not open with the refusal:\n%s", told)
	}
	for _, want := range []string{
		"no artifact answers to that id",
		"Do not describe the proposal as raised",
		"do not write into your change",
	} {
		if !strings.Contains(told, want) {
			t.Fatalf("the carried refusal is missing %q:\n%s", want, told)
		}
	}
	// And the contract the invocation still carries under it, so the developer is
	// asked for the work as well as told about the refusal.
	if !strings.Contains(told, "Failing check: repair required") {
		t.Fatalf("the carried refusal replaced the repair the attempt was for:\n%s", told)
	}
	// Told once. The reply to that invocation spends it.
	if strings.Contains(requests[2].Prompt, "A change you proposed was not recorded") {
		t.Fatalf("the refusal was carried a second time:\n%s", requests[2].Prompt)
	}
}

// The carry-back has to survive the process, not just the loop. A run stopped
// after the reply that was refused is continued by triage in another process
// entirely, and the developer it hands the work back to is the one that has still
// never been told.
func TestARefusedProposalIsDurableForAnInvocationInAnotherProcess(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	writeDesignArtifact(t, repository)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	provider.developerFinalText = "implemented the work item\n\n" +
		amendmentBlock(`{"artifact":"invented","change":"say which ordering holds","why":"the item cannot satisfy both"}`)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Amendments = &fakeAmendments{}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Read back through the store, the way a later process reads it: the words are
	// on disk rather than only in the prompt this run happened to build.
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.RefusedAmendments) != 1 || !strings.Contains(recorded.RefusedAmendments[0].Problem, "no artifact answers to that id") {
		t.Fatalf("the refusal did not survive the run: %#v", recorded.RefusedAmendments)
	}
	// Tagged with who proposed it, which is what decides who is shown it.
	if recorded.RefusedAmendments[0].Role != domain.RoleDeveloper {
		t.Fatalf("the refusal is not attributed to the role that earned it: %#v", recorded.RefusedAmendments[0])
	}
}

// A refusal is shown to the role that earned it and to nobody else. Only the
// developer's reply is scanned for proposals today, so only a developer can earn
// one — but what makes that safe has to be the attribution rather than the single
// caller, because a refusal shown to the wrong agent is that agent told not to
// claim something it never said while the agent that did say it goes on believing
// its proposal landed. That is the failure this item exists to end, one role over.
func TestARefusalIsNotShownToARoleThatDidNotEarnIt(t *testing.T) {
	t.Parallel()

	const problem = "a change the reviewer proposed was not recorded: invented: no artifact answers to that id"
	carried := []runstate.AmendmentRefusal{{Role: domain.RoleReviewer, Problem: problem}}

	// The developer is shown nothing, because none of it is the developer's.
	if section := carriedAmendmentRefusals(domain.RoleDeveloper, carried); section != "" {
		t.Fatalf("another role's refusal was put in front of the developer:\n%s", section)
	}
	// The role that earned it still is.
	section := carriedAmendmentRefusals(domain.RoleReviewer, carried)
	if !strings.Contains(section, problem) {
		t.Fatalf("the proposing role was not shown its own refusal:\n%s", section)
	}

	// And spending one role's refusals leaves every other role's where they are: a
	// developer reply says the developer was told, and nothing about anybody else.
	run := &activeRun{state: runstate.State{RefusedAmendments: append(
		[]runstate.AmendmentRefusal{{Role: domain.RoleDeveloper, Problem: "a change the developer proposed was not recorded: invented: no artifact answers to that id"}},
		carried...)}}
	run.clearCarriedAmendmentRefusals(domain.RoleDeveloper)
	if len(run.state.RefusedAmendments) != 1 || run.state.RefusedAmendments[0].Role != domain.RoleReviewer {
		t.Fatalf("clearing one role's refusals took another role's with them: %#v", run.state.RefusedAmendments)
	}
}

// The carried list is bounded, so a run whose every reply is an unreadable block
// cannot fill durable state or the next prompt with refusals. Past the bound the
// refusal is still named on the outcome, which is the half that reaches the
// operator.
func TestCarriedAmendmentRefusalsStopAtTheBound(t *testing.T) {
	t.Parallel()

	run := &activeRun{}
	for i := 0; i < runstate.MaxCarriedAmendmentRefusals+3; i++ {
		run.noteAmendmentProblem(domain.RoleDeveloper, fmt.Errorf("refusal %d", i))
	}
	if len(run.state.RefusedAmendments) != runstate.MaxCarriedAmendmentRefusals {
		t.Fatalf("carried %d refusals, want the bound of %d", len(run.state.RefusedAmendments), runstate.MaxCarriedAmendmentRefusals)
	}
	// What was dropped from the carry is still on the outcome for the operator.
	if !strings.Contains(run.outcome.AmendmentProblem, "refusal 12") {
		t.Fatalf("a refusal past the bound was lost to the operator too: %q", run.outcome.AmendmentProblem)
	}
	// And every one of them is in a shape the store will take, since a refusal the
	// state refuses is a refusal nobody is ever told.
	for index, refused := range run.state.RefusedAmendments {
		if err := refused.Validate(); err != nil {
			t.Fatalf("carried refusal %d is not storable: %v", index, err)
		}
	}
}

// One refusal longer than the bound is folded to it rather than left for the
// store to refuse, because a refusal the state will not take is a refusal nobody
// is ever told. Today's callers fold to a shorter bound of their own before they
// get here, so this asks the unit whose guarantee it is rather than going through
// them: what must hold is that nothing this appends is a shape the state rejects,
// whatever the caller hands it.
func TestALongAmendmentRefusalIsFoldedToWhatTheStateWillTake(t *testing.T) {
	t.Parallel()

	run := &activeRun{}
	run.carryAmendmentRefusal(domain.RoleDeveloper, strings.Repeat("x", runstate.MaxAmendmentRefusalBytes*2))
	if len(run.state.RefusedAmendments) != 1 {
		t.Fatalf("carried = %#v", run.state.RefusedAmendments)
	}
	if err := run.state.RefusedAmendments[0].Validate(); err != nil {
		t.Fatalf("an over-long refusal was carried in a shape the store refuses: %v", err)
	}
}

// amendmentBlock renders what an agent writes when it proposes a change, the way
// the contract asks for it: after everything else it says.
func amendmentBlock(proposals ...string) string {
	return amendment.Fence + "\n{\"proposals\":[" + strings.Join(proposals, ",") + "]}\n```\n"
}

// writeDesignArtifact gives the fixture repository one of the architect's
// documents to propose a change to, and writeGoalsArtifact one of the product
// manager's — in the home each kind actually lives in, since which home a
// document is loaded from is what decides whether it can be proposed against at
// all. Both return where the document is.
func writeDesignArtifact(t *testing.T, repository string) string {
	t.Helper()
	return writeArtifact(t, repository, filepath.Join("docs", "designs", "v1-design.md"), `---
id: v1-design
kind: design
title: V1 design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: recorded when identity arrived
---

The ordering is unspecified.
`)
}

func writeGoalsArtifact(t *testing.T, repository string) string {
	t.Helper()
	return writeArtifact(t, repository, filepath.Join("docs", "product", "v1-goals.md"), `---
id: v1-goals
kind: goals
title: V1 goals
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: recorded when identity arrived
---

# V1 goals

What the product is for.

## Goals

- A run that cannot finish leaves its work recoverable.
`)
}

// writeArtifact commits the document, because the developer's worktree is
// created from the repository and an uncommitted file in the primary checkout is
// a change the harness refuses to work around.
func writeArtifact(t *testing.T, repository, relative, content string) string {
	t.Helper()
	path := filepath.Join(repository, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "record an artifact")
	return relative
}

// fakeAmendments is the durable log without a filesystem. err refuses every
// append, which is how a run that cannot keep a proposal is tested.
type fakeAmendments struct {
	appended []amendment.Proposal
	err      error
}

func (f *fakeAmendments) Append(proposal amendment.Proposal) error {
	if f.err != nil {
		return f.err
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	f.appended = append(f.appended, proposal)
	return nil
}
