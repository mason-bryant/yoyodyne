package orchestrator

import (
	"context"
	"errors"
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

func TestTheDeveloperContractSaysHowToProposeAChange(t *testing.T) {
	t.Parallel()

	// A developer told to propose upstream changes and given no mechanism has
	// only two moves left, both bad, so the mechanism is stated in the contract
	// where a persona cannot weaken it — and repeated on a repair attempt for the
	// same reason the rest of the contract is.
	for name, prompt := range map[string]string{
		"first attempt": developerPrompt("", "", "# Assigned work item\n"),
		"check repair":  checkRepairPrompt("", runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}, 1, 2),
	} {
		for _, required := range []string{amendment.Fence, "A proposal is not an edit you have written in advance"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("%s prompt is missing %q", name, required)
			}
		}
	}
}

// amendmentBlock renders what an agent writes when it proposes a change, the way
// the contract asks for it: after everything else it says.
func amendmentBlock(proposals ...string) string {
	return amendment.Fence + "\n{\"proposals\":[" + strings.Join(proposals, ",") + "]}\n```\n"
}

// writeDesignArtifact gives the fixture repository one governed document to
// propose a change to, and returns where it is. It is committed because the
// developer's worktree is created from the repository, and an uncommitted file
// in the primary checkout is a change the harness refuses to work around.
func writeDesignArtifact(t *testing.T, repository string) string {
	t.Helper()
	relative := filepath.Join("docs", "designs", "v1-design.md")
	path := filepath.Join(repository, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := `---
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
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "record a design")
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
