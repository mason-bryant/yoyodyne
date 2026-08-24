package orchestrator

// What the harness actually hands a reviewer, and what it does with the one
// verdict that ends a run without approving it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The reviewer is handed the developer's own account of the change and a listing
// of what the repository holds. Three consecutive reviews on one item judged
// against a summary they were never shown, while that item's done criterion named
// the summary as the evidence; and two findings asserted a repository file did
// not exist because no binary appeared in the diff.
func TestTheReviewerIsHandedTheDevelopersAccountAndWhatTheRepositoryHolds(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, `{"decision":"approve","summary":"the change matches the acceptance criteria"}`)
	provider.developerFinalText = "Added feature.txt. I left docs/design.md alone, which the criteria asked for."
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	reviews := provider.requestsForRole(domain.RoleReviewer)
	if len(reviews) != 1 {
		t.Fatalf("reviewer invocations = %d, want 1", len(reviews))
	}
	for _, want := range []string{
		"The developer's account of this change",
		"I left docs/design.md alone",
		"Every path the repository holds at commit",
		// The fixture is committed before the run starts, so its own files are what
		// the listing has to name: a file the change never touched is exactly the
		// one the patch says nothing about.
		"docs/design.md",
	} {
		if !strings.Contains(reviews[0].Prompt, want) {
			t.Errorf("review evidence is missing %q", want)
		}
	}

	// The account is durable, because the process that reviews a change is
	// routinely not the one that developed it.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(state.DeveloperSummary, "I left docs/design.md alone") {
		t.Fatalf("the developer's account was not made durable: %#v", state)
	}
}

// A change that is a reasoned refusal to implement is terminal. Handing it back
// as findings is repair pressure toward a design nobody has decided, which is the
// outcome the upstream block exists to prevent; so the run ends, the item is
// recorded as waiting on that decision, and no further attempt is bought.
func TestAnUpheldRefusalEndsTheRunWithoutRepairingOrIntegrating(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "notes.md"), []byte("why I stopped\n"), 0o600)
	}, `{"decision":"refusal_upheld","summary":"the item waits on a design nobody has decided; stopping was right"}`)
	provider.developerFinalText = "I did not implement this: the design it needs has not been decided."
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	before := gitLine(t, repository, "rev-parse", "refs/heads/main")

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "upheld the refusal") {
		t.Fatalf("Run() error = %v, want the upheld refusal reported", err)
	}
	if outcome.Integration != nil || tracker.closed {
		t.Fatalf("an upheld refusal integrated something: %#v, closed = %t", outcome.Integration, tracker.closed)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != before {
		t.Fatalf("main moved on a verdict that approved nothing: %q, want %q", head, before)
	}
	// One developer attempt and one review: the refusal buys no repair round.
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Fatalf("developer invocations = %d, want the refusal not handed back", developed)
	}
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want none spent on an upheld refusal", outcome.RepairAttempts)
	}
	if !tracker.blocked {
		t.Fatalf("the item was not recorded as waiting on the upstream decision: %#v", tracker)
	}
	for _, want := range []string{
		"upheld the refusal",
		"This is not a defect in the change",
		"the design it needs has not been decided",
	} {
		if !strings.Contains(tracker.blockReason, want) {
			t.Errorf("blocker is missing %q: %q", want, tracker.blockReason)
		}
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewDecision != runstate.ReviewRefusalUpheld {
		t.Fatalf("durable review decision = %q, want %q", state.ReviewDecision, runstate.ReviewRefusalUpheld)
	}
	// Nothing downstream treats it as an approval: every gate asks for that one
	// word, and this is not it.
	if outcome.ReviewDecision == review.DecisionApprove {
		t.Fatalf("an upheld refusal was reported as an approval: %#v", outcome)
	}
}
