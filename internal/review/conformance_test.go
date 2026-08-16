package review

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/backend/claudecode"
	"yoyodyne/internal/checks"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
)

// TestLocalReviewConformance checks the one part of documentation
// reconciliation no fake provider can demonstrate: that a real reviewer, given
// a change whose evidence contains the document it falsifies, actually reports
// the contradiction instead of approving. It is opt-in because it spends a
// provider invocation, like the backend conformance test.
func TestLocalReviewConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := claudecode.Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}

	// The work item asks only for the behavior change. Nothing in it mentions
	// the README, so an approval here would mean the contract's documentation
	// instruction did not carry.
	request := Request{
		RunID:      reviewRunID,
		WorkItemID: "yoyodyne-conformance",
		Context: "# Assigned work item\n\nID: yoyodyne-conformance\nTitle: Integrate an approved change automatically\n\n" +
			"## Acceptance criteria\n\nAn approved change is fast-forwarded into the target branch by the harness.\n",
		WorktreePath: t.TempDir(),
		Changes: gitworktree.ChangeDiff{
			Status:   " M integrate.go",
			DiffStat: " integrate.go | 6 ++++++",
			Patch: "diff --git a/integrate.go b/integrate.go\n" +
				"--- a/integrate.go\n+++ b/integrate.go\n" +
				"@@ -1,3 +1,9 @@\n package harness\n" +
				"+\n+// Integrate fast-forwards an approved change into the target branch and\n" +
				"+// closes its work item.\n+func Integrate(change Change) error {\n" +
				"+\treturn fastForward(change)\n+}\n" +
				"diff --git a/README.md b/README.md\n" +
				"--- a/README.md\n+++ b/README.md\n" +
				"@@ -36,4 +36,5 @@\n" +
				" On success, the JSON result reports the run ID, branch, and change summary.\n" +
				" The harness does not yet commit, integrate, or close the item automatically.\n" +
				"+The JSON result also reports the base commit.\n",
		},
		Checks: []checks.Result{{
			Command: "go test ./...",
			Passed:  true,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded},
		}},
	}

	result, err := (Reviewer{Backend: provider, Model: "opus", Timeout: 5 * time.Minute}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair {
		t.Fatalf("Review() decision = %q with summary %q, want repair over the falsified README claim", result.Decision, result.Verdict.Summary)
	}
	for _, finding := range result.Verdict.Findings {
		haystack := strings.ToLower(finding.Message)
		if finding.Location != nil {
			haystack += " " + strings.ToLower(finding.Location.File)
		}
		if strings.Contains(haystack, "readme") || strings.Contains(haystack, "document") {
			return
		}
	}
	t.Fatalf("no finding named the documentation the change falsified: %#v", result.Verdict)
}
