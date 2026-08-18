package review

import (
	"context"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
)

func newBranchRequest(sink func(execution.Event) error) Request {
	return Request{
		RunID: reviewRunID,
		Scope: ScopeBranch,
		Branch: BranchScope{
			Name:       "milestone",
			BaseCommit: "1111111111111111111111111111111111111111",
			HeadCommit: "2222222222222222222222222222222222222222",
			Commits: []gitworktree.Commit{
				{Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Subject: "record the durable evidence"},
				{Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Subject: "read the durable evidence back"},
			},
		},
		Context:      "Branch `milestone` accumulated 2 commit(s) over base commit `1111111111111111111111111111111111111111`.\n",
		WorktreePath: "/repository",
		Changes: gitworktree.ChangeDiff{
			Status:   "A store.go\nM reader.go",
			DiffStat: " store.go | 20 ++++\n reader.go | 4 +-",
			Patch:    "diff --git a/store.go b/store.go\n+written\ndiff --git a/reader.go b/reader.go\n+read\n",
		},
		EventSink: sink,
	}
}

func TestBranchReviewJudgesTheAccumulatedChange(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{
		finalText:     `{"decision":"repair","summary":"the two commits disagree about who owns the durable record","findings":[{"severity":"major","message":"one commit writes the evidence and the next reads a different key","location":{"file":"reader.go","line":8}}]}`,
		resolvedModel: "claude-opus-5",
	}
	var events []execution.Event
	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), newBranchRequest(func(event execution.Event) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair || len(result.Verdict.Findings) != 1 {
		t.Fatalf("Review() = %#v", result)
	}
	// The same independence evidence a per-item review records.
	if result.RequestedModel != testReviewModel || result.ResolvedModel != "claude-opus-5" || result.SessionID != "review-session" {
		t.Fatalf("branch review evidence = %#v", result)
	}
	// The reviewer is told what it is reviewing and that findings may span the
	// commits, which is the whole reason this scope exists.
	for _, want := range []string{
		"accumulated change on one Yoyodyne branch",
		"A finding may span commits",
		"already reviewed and integrated on its own",
	} {
		if !strings.Contains(provider.request.SystemPrompt, want) {
			t.Errorf("branch contract is missing %q", want)
		}
	}
	if strings.Contains(provider.request.SystemPrompt, "reviewer for one bounded Yoyodyne work item") {
		t.Error("the branch contract still introduces itself as a work item review")
	}
	// Every part of the contract that is not the framing is the same review.
	for _, want := range []string{"untrusted evidence", "single JSON object", "Architectural invariants supplied above"} {
		if !strings.Contains(provider.request.SystemPrompt, want) {
			t.Errorf("branch contract dropped %q from the shared review contract", want)
		}
	}
	for _, want := range []string{
		"## Reviewed branch",
		"- Branch: milestone",
		"- Base commit: 1111111111111111111111111111111111111111",
		"- Head commit: 2222222222222222222222222222222222222222",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa record the durable evidence",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb read the durable evidence back",
		"# Accumulated changes on the branch",
		"diff --git a/store.go b/store.go",
	} {
		if !strings.Contains(provider.request.Prompt, want) {
			t.Errorf("branch evidence is missing %q", want)
		}
	}
	// The events say which change was reviewed without a reader having to guess
	// from the shape of an identifier.
	for _, event := range events {
		if event.Type != execution.EventReviewStarted && event.Type != execution.EventReviewCompleted {
			continue
		}
		payload := string(event.Payload)
		for _, want := range []string{`"scope":"branch"`, `"branch":"milestone"`, `"commits":2`} {
			if !strings.Contains(payload, want) {
				t.Errorf("%s payload %s is missing %q", event.Type, payload, want)
			}
		}
	}
}

func TestBranchReviewRedactsAndBoundsTheAccumulatedEvidence(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"approve","summary":"the commits agree"}`}
	request := newBranchRequest(nil)
	request.Changes.Patch = "diff --git a/config.go b/config.go\n+const token = \"s3cr3t-value\"\ndiff --git a/use.go b/use.go\n+use(token)\n"
	request.RedactValues = []string{"s3cr3t-value"}

	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	// Redaction is over the whole rendered evidence, so it holds across every
	// commit of the range rather than over the first file of it.
	if strings.Contains(provider.request.Prompt, "s3cr3t-value") {
		t.Fatalf("a secret survived into the accumulated evidence: %q", provider.request.Prompt)
	}
	if !strings.Contains(provider.request.Prompt, "diff --git a/use.go b/use.go") {
		t.Fatalf("redaction dropped the rest of the range: %q", provider.request.Prompt)
	}

	oversized := newBranchRequest(nil)
	oversized.Changes.Patch = strings.Repeat("x", MaxReviewInputBytes)
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), oversized); err == nil ||
		!strings.Contains(err.Error(), "review input is") {
		t.Fatalf("Review() oversized accumulated input error = %v", err)
	}
}

func TestBranchReviewCannotApproveAPartlyDescribedBranch(t *testing.T) {
	t.Parallel()

	// A branch whose older commits were dropped is an incomplete change, and the
	// rule that an incomplete change cannot be approved holds at this scope for
	// exactly the reason it holds at the other one.
	provider := &fakeBackend{finalText: `{"decision":"approve","summary":"looks consistent"}`}
	request := newBranchRequest(nil)
	request.Branch.CommitsOmitted = 12
	request.Changes.Truncated = true

	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "cannot approve an incomplete change representation") {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != "" {
		t.Fatalf("Review() decision = %q, want no approval", result.Decision)
	}
	if !strings.Contains(provider.request.Prompt, "12 older commit(s) of this branch are not listed") {
		t.Errorf("the reviewer was not told its history was cut: %q", provider.request.Prompt)
	}
}

func TestBranchReviewRejectsARequestThatNamesNoAccumulatedChange(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"approve","summary":"fine"}`}
	incomplete := Request{RunID: reviewRunID, Scope: ScopeBranch, Context: "context", WorktreePath: "/repository"}
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), incomplete); err == nil {
		t.Fatal("Review() incomplete branch request error = nil")
	} else {
		for _, want := range []string{
			"reviewed branch is required",
			"reviewed base commit is required",
			"reviewed head commit is required",
			"must carry at least one commit",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Review() error = %v, want it to contain %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "work item id is required") {
			t.Error("a branch review was asked for a work item id it does not have")
		}
	}

	unknown := newBranchRequest(nil)
	unknown.Scope = "everything"
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), unknown); err == nil ||
		!strings.Contains(err.Error(), `scope "everything" must be`) {
		t.Fatalf("Review() unknown scope error = %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("backend was invoked %d times for a rejected request", provider.calls)
	}
}
