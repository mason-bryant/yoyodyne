package runstate

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const branchReviewID = "run-0123456789abcdef0123456789abcdef"

func newBranchReview() BranchReview {
	return BranchReview{
		SchemaVersion: BranchReviewSchemaVersion,
		ReviewID:      branchReviewID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Branch:        "milestone",
		BaseRef:       "main",
		BaseCommit:    "1111111111111111111111111111111111111111",
		HeadCommit:    "2222222222222222222222222222222222222222",
		Commits:       3,
		ReviewedAt:    time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		SessionID:     "review-session",
		Model:         "opus",
		ResolvedModel: "claude-opus-5",
		Decision:      ReviewApprove,
		Summary:       "the commits agree with one another",
	}
}

func TestBranchReviewStoreKeepsEveryVerdictItWasGiven(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	if reviews, err := store.List(); err != nil || len(reviews) != 0 {
		// A product whose branches have never been reviewed is not a read failure.
		t.Fatalf("List() over an empty store = %v, %v", reviews, err)
	}

	approved := newBranchReview()
	repaired := newBranchReview()
	repaired.Decision = ReviewRepair
	repaired.Summary = "the durable evidence is written in two places"
	repaired.Findings = []Finding{{Severity: SeverityMajor, Message: "one commit writes the record and the next reads another key", File: "reader.go", Line: 8}}
	repaired.Truncated = true
	repaired.CommitsOmitted = 4
	for _, reviewed := range []BranchReview{approved, repaired} {
		if err := store.Append(reviewed); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	reviews, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("List() = %#v", reviews)
	}
	// The session and model evidence a per-item review records survives here too:
	// a recorded verdict nobody can attribute is not audit evidence.
	if reviews[0].SessionID != "review-session" || reviews[0].Model != "opus" || reviews[0].ResolvedModel != "claude-opus-5" {
		t.Errorf("recorded evidence = %#v", reviews[0])
	}
	if !reviews[0].Approved() {
		t.Error("an approving verdict did not read back as approved")
	}
	if reviews[1].Approved() {
		t.Error("a repair verdict read back as an approval")
	}
	if len(reviews[1].Findings) != 1 || reviews[1].Findings[0].File != "reader.go" || reviews[1].CommitsOmitted != 4 {
		t.Errorf("recorded repair = %#v", reviews[1])
	}
	if !strings.HasSuffix(store.Path(), filepath.Join("products", "yoyodyne", "branch-reviews.jsonl")) {
		t.Errorf("Path() = %q", store.Path())
	}
}

func TestBranchReviewStoreRefusesWhatCannotDescribeAReview(t *testing.T) {
	t.Parallel()

	store, err := NewBranchReviewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	for name, mutate := range map[string]func(*BranchReview){
		"another product's review":   func(b *BranchReview) { b.ProductID = "elsewhere" },
		"an unnamed branch":          func(b *BranchReview) { b.Branch = "refs/heads/milestone" },
		"an invalid base commit":     func(b *BranchReview) { b.BaseCommit = "main" },
		"a base that is the head":    func(b *BranchReview) { b.BaseCommit = b.HeadCommit },
		"a branch with no commits":   func(b *BranchReview) { b.Commits = 0 },
		"a decision nothing decided": func(b *BranchReview) { b.Decision = "maybe" },
		"a verdict with no summary":  func(b *BranchReview) { b.Summary = "" },
		"a repair with no finding": func(b *BranchReview) {
			b.Decision = ReviewRepair
			b.Findings = nil
		},
		// A review that answered nothing has to say what stopped it, or it is
		// indistinguishable from a branch nobody reviewed.
		"a silent absent verdict": func(b *BranchReview) {
			b.Decision = ""
			b.Summary = ""
		},
	} {
		reviewed := newBranchReview()
		mutate(&reviewed)
		if err := store.Append(reviewed); err == nil {
			t.Errorf("Append() accepted %s", name)
		}
	}

	// A review that failed is recorded, because "this branch was reviewed and
	// the review did not answer" is itself what an operator needs.
	failed := newBranchReview()
	failed.Decision = ""
	failed.Summary = ""
	failed.Failure = "the reviewer backend failed: claude is not installed"
	if err := store.Append(failed); err != nil {
		t.Fatalf("Append() of a failed review error = %v", err)
	}
	reviews, err := store.List()
	if err != nil || len(reviews) != 1 || reviews[0].Approved() {
		t.Fatalf("List() = %#v, %v", reviews, err)
	}
}
