package orchestrator

// Delivery is the whole point of an invariant: one that nobody puts in front of
// a developer constrains nothing, and one that only reached a change because
// somebody transcribed it into the bead constrains that bead alone. These tests
// are about the delivery rather than about the artifact, which internal/invariant
// covers on its own.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestRelevantInvariantsReachTheDeveloperAndTheReviewerWithoutBeingTranscribed(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	recordInvariant(t, repository, invariant.Draft{
		ID:            "harness-owns-git",
		Title:         "Every Git write is the harness's own",
		Statement:     "No role commits, pushes, or merges; the harness performs every Git write.",
		Rationale:     "A role that could merge is a role that can be talked into merging.",
		EstablishedBy: []string{"yoyodyne-ifd.2.4"},
		Reason:        "extracted from the decision that made integration automatic",
	})
	recordInvariant(t, repository, invariant.Draft{
		ID:            "one-writer-per-item",
		Title:         "One process at a time acts on an in-flight item",
		Statement:     "Every entry into an in-flight run takes the run's exclusive lease first.",
		Rationale:     "The lease is the only thing keeping two developers off one work item.",
		EstablishedBy: []string{"yoyodyne-ifd.2.7"},
		Scope:         []string{"internal/runstate"},
		Reason:        "extracted from the decision that added the reservation",
	})
	// A file that is not an invariant is a gap in the set rather than a run
	// failure, and it has to be visible as one.
	writeRepositoryFile(t, repository, filepath.Join("docs", "decisions", "invariants", "half-written.md"), "## Must hold\n\nSomething.\n")
	commitRepository(t, repository)

	// The work item says nothing about invariants and nothing about the package
	// the scoped one constrains. Nothing here was transcribed by hand.
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add a feature",
		Description:        "Add the feature the acceptance criteria describe.",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
	}}
	// The change touches the scoped invariant's code, which the work item never
	// named. That is the case a developer's own reading of its work cannot catch.
	provider := roleBackend(func(request backend.RunRequest) error {
		if err := os.MkdirAll(filepath.Join(request.WorkingDirectory, "internal", "runstate"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "internal", "runstate", "resume.go"), []byte("package runstate\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v", outcome)
	}

	developerPrompt := provider.requestsForRole(domain.RoleDeveloper)[0].Prompt
	if !strings.Contains(developerPrompt, "harness-owns-git") {
		t.Fatalf("the repository-wide invariant did not reach the developer:\n%s", developerPrompt)
	}
	// A scoped invariant the work item never mentioned is deliberately not in the
	// developer's context: every invariant in every prompt is both expensive and
	// ignorable.
	if strings.Contains(developerPrompt, "one-writer-per-item") {
		t.Fatalf("an invariant scoped elsewhere reached the developer:\n%s", developerPrompt)
	}
	if !strings.Contains(developerPrompt, "half-written.md") {
		t.Fatalf("the unreadable invariant was not named as a gap:\n%s", developerPrompt)
	}

	// The reviewer's evidence is selected against the change as well, so the
	// invariant the change actually walked into reaches the gate that judges it.
	reviewerPrompt := provider.requestsForRole(domain.RoleReviewer)[0].Prompt
	for _, required := range []string{"harness-owns-git", "one-writer-per-item", "Must hold:"} {
		if !strings.Contains(reviewerPrompt, required) {
			t.Fatalf("the reviewer's evidence is missing %q:\n%s", required, reviewerPrompt)
		}
	}
	// The invariants are the harness's own, delivered ahead of everything the
	// developer produced, so a change cannot present itself as its own constraint.
	invariants := strings.Index(reviewerPrompt, "# Architectural invariants")
	untrusted := strings.Index(reviewerPrompt, "# Untrusted review evidence")
	if invariants < 0 || untrusted < 0 || invariants > untrusted {
		t.Fatalf("invariants = %d, untrusted evidence = %d\n%s", invariants, untrusted, reviewerPrompt)
	}

	// What constrained the change is recorded, and so is what the delivered set
	// was missing.
	if got := strings.Join(outcome.Invariants, ","); got != "harness-owns-git,one-writer-per-item" {
		t.Fatalf("recorded invariants = %q", got)
	}
	if len(outcome.InvariantProblems) != 1 || !strings.Contains(outcome.InvariantProblems[0], "half-written.md") {
		t.Fatalf("recorded invariant problems = %#v", outcome.InvariantProblems)
	}
	if !strings.Contains(tracker.notes, "Invariants delivered: harness-owns-git, one-writer-per-item") {
		t.Fatalf("the tracker does not record what constrained the change:\n%s", tracker.notes)
	}
	if !strings.Contains(tracker.notes, "Invariant not delivered:") {
		t.Fatalf("the tracker does not record the gap in the set:\n%s", tracker.notes)
	}
}

func TestEveryRepairAttemptCarriesTheInvariantsToo(t *testing.T) {
	t.Parallel()

	// A repair attempt is where the ifd.2.8 failure happened: work that fixes
	// what it was asked about while breaking a constraint nobody restated. So the
	// constraints go back with every attempt, whichever kind of failure triggered
	// it.
	repository := pipelineRepository(t)
	recordInvariant(t, repository, invariant.Draft{
		ID:            "one-resume-path",
		Title:         "One resume path per run",
		Statement:     "A run is resumed through the path the harness already has, not a second one beside it.",
		Rationale:     "A second path breaks the first one's contract without appearing to.",
		EstablishedBy: []string{"yoyodyne-ifd.2.7"},
		Reason:        "extracted from the decision that built the resume path",
	})
	commitRepository(t, repository)

	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	requests := provider.requestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and one repair", len(requests))
	}
	for index, request := range requests {
		if !strings.Contains(request.Prompt, "one-resume-path") {
			t.Fatalf("developer attempt %d lost the invariants:\n%s", index+1, request.Prompt)
		}
	}
	if !strings.Contains(requests[1].Prompt, "Failing check: repair required") {
		t.Fatalf("the second attempt was not a check repair:\n%s", requests[1].Prompt)
	}
}

func TestARunRefusesToStartWhenTheInvariantsCannotBeRead(t *testing.T) {
	t.Parallel()

	// Delivering nothing is indistinguishable from a repository with no
	// constraints, so a directory that cannot be read stops the run before
	// anything is claimed rather than quietly leaving the change unconstrained.
	repository := pipelineRepository(t)
	writeRepositoryFile(t, repository, filepath.Join("docs", "decisions", "invariants"), "not a directory\n")
	commitRepository(t, repository)

	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	_, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "load architectural invariants") {
		t.Fatalf("Run() error = %v", err)
	}
	if tracker.claimed || len(provider.requests) != 0 {
		t.Fatalf("work started without its constraints: claimed = %t, requests = %d", tracker.claimed, len(provider.requests))
	}
}

// recordInvariant records one invariant the way the architect does, so what the
// pipeline reads is what the owning role's own lifecycle produces.
func recordInvariant(t *testing.T, repository string, draft invariant.Draft) {
	t.Helper()
	store := invariant.Store{RepositoryRoot: repository, Directory: "docs/decisions/invariants"}
	if _, err := store.Create(domain.RoleArchitect, draft, time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("record invariant %q: %v", draft.ID, err)
	}
}

func writeRepositoryFile(t *testing.T, repository, relative, content string) {
	t.Helper()
	path := filepath.Join(repository, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// commitRepository commits what a test added, because a run refuses to start in a
// repository with uncommitted changes.
func commitRepository(t *testing.T, repository string) {
	t.Helper()
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "record invariants")
}
