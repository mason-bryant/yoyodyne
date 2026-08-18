package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

func TestReviewRequiresTheBaseItIsMeasuredAgainst(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"review"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "review requires --base") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"review", "--base", "main", "milestone"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() with a positional argument code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// The outcome of a branch-scope repair verdict is that the branch is not
// approved. Nothing already integrated is touched by it, and the exit code is
// what stops anything downstream reading a repair, a failed review, or an
// approval of a change nobody could see in full as an approval of the branch.
func TestReviewFailsUnlessTheAccumulatedChangeWasApproved(t *testing.T) {
	t.Parallel()

	approved := orchestrator.BranchReviewOutcome{
		ReviewID: "run-abcdef0123456789abcdef0123456789", Branch: "milestone", BaseRef: "main",
		BaseCommit: "1111111111111111111111111111111111111111",
		HeadCommit: "2222222222222222222222222222222222222222",
		Commits:    3, Decision: review.DecisionApprove, Summary: "the commits agree",
		SessionID: "branch-review-session", Model: "opus",
	}
	repaired := approved
	repaired.Decision = review.DecisionRepair
	repaired.Summary = "the two halves disagree about the key they use"
	repaired.Findings = []review.Finding{{
		Severity: review.SeverityMajor,
		Message:  "store.go writes under key and reader.go reads under other",
		Location: &review.Location{File: "reader.go", Line: 3},
	}}
	unanswered := approved
	unanswered.Decision = ""
	unanswered.Summary = ""

	for _, test := range []struct {
		name    string
		outcome orchestrator.BranchReviewOutcome
		err     error
		code    int
		want    string
	}{
		{name: "approved", outcome: approved, code: 0, want: "review: approve (session branch-review-session, model opus)"},
		{name: "repair", outcome: repaired, code: 1, want: "major [reader.go:3]"},
		{name: "no verdict", outcome: unanswered, err: errors.New("the reviewer never answered"), code: 1, want: "reviewed milestone against main"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			if code := reportBranchReview(&stdout, &stderr, false, test.outcome, test.err); code != test.code {
				t.Fatalf("reportBranchReview() code = %d, want %d (stderr %q)", code, test.code, stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Fatalf("output = %q, want it to contain %q", stdout.String()+stderr.String(), test.want)
			}
		})
	}

	// A repair verdict says plainly that the work stays integrated, because the
	// harness does not undo promotions on a second opinion.
	var stdout, stderr bytes.Buffer
	reportBranchReview(&stdout, &stderr, false, repaired, nil)
	if !strings.Contains(stderr.String(), "stays integrated") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	// The JSON form answers the same question, and carries the verdict with it.
	stdout.Reset()
	stderr.Reset()
	if code := reportBranchReview(&stdout, &stderr, true, repaired, nil); code != 1 {
		t.Fatalf("reportBranchReview(--json) code = %d, want 1", code)
	}
	var decoded branchReviewOutput
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Review == nil || decoded.Review.Decision != review.DecisionRepair || len(decoded.Review.Findings) != 1 {
		t.Fatalf("decoded review = %#v", decoded.Review)
	}
}
