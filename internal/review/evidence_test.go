package review

// The evidence a reviewer's judgements actually require. Four reviews on record
// judged with less than their findings claimed: one could not see the blocking
// dependency the item was correctly refused on, one twice asserted a repository
// file did not exist because no binary appeared in the diff, and three in a row
// lacked the developer's change summary that the item's own done criterion named
// as the evidence to judge against. Each of these is one of those.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

func TestReviewCarriesTheDevelopersOwnAccountOfTheChange(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"approve","summary":"fine"}`}
	request := newRequest(nil)
	request.DeveloperSummary = "Added subtract(a, b); make test passes. I did not touch the parser, which the criteria said to leave alone."

	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	for _, want := range []string{
		"## The developer's account of this change",
		"I did not touch the parser",
		// It is the party whose work is being judged talking, and the reviewer is
		// told so rather than left to weigh it as though the harness had said it.
		"a claim about the change rather than evidence of it",
	} {
		if !strings.Contains(provider.request.Prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

// A developer that said nothing contributes no heading, rather than an empty one
// that reads as a summary somebody wrote and that says nothing.
func TestReviewOmitsTheDevelopersAccountWhenThereIsNone(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"approve","summary":"fine"}`}
	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), newRequest(nil)); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(provider.request.Prompt, "The developer's account of this change") {
		t.Fatalf("prompt invented a developer account: %s", provider.request.Prompt)
	}
}

func TestReviewCarriesWhatTheRepositoryHoldsAndSaysWhatItSettles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		listing RepositoryListing
		want    []string
	}{
		{
			name:    "a complete listing settles both ways",
			listing: RepositoryListing{Commit: "abc123", Files: []string{"README.md", "testdata/fixture.bin"}},
			want: []string{
				"Every path the repository holds at commit abc123",
				"testdata/fixture.bin",
				"A path here is in the repository whether or not the patch mentions it",
			},
		},
		{
			name:    "a bounded listing settles presence only",
			listing: RepositoryListing{Commit: "abc123", Files: []string{"README.md"}, Omitted: 7},
			want: []string{
				"7 further path(s) are not in it",
				"you may not report a path as missing",
			},
		},
		{
			name:    "a listing that could not be taken settles nothing",
			listing: RepositoryListing{Unavailable: "git ls-tree failed with exit code 128"},
			want: []string{
				"No listing of the repository could be taken (git ls-tree failed with exit code 128)",
				"you may not report one as missing",
			},
		},
		{
			name:    "no listing at all is stated rather than passed over",
			listing: RepositoryListing{},
			want: []string{
				"No listing of the repository was supplied with this review",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeBackend{finalText: `{"decision":"approve","summary":"fine"}`}
			request := newRequest(nil)
			request.Repository = test.listing
			if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request); err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			for _, want := range test.want {
				if !strings.Contains(provider.request.Prompt, want) {
					t.Errorf("prompt is missing %q", want)
				}
			}
		})
	}
}

// The claim the two recorded cases got wrong: a file absent from the diff read as
// a file absent from the repository. The listing is what refutes it, and the
// refutation is a refusal rather than a correction — a finding resting on a false
// reading carries an instruction built on one, and both of those directed an edit
// that would have introduced the defect the item existed to fix.
func TestReviewRefusesAnAbsenceClaimItsEvidenceDoesNotSupport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		listing RepositoryListing
		verdict string
		wantErr string
	}{
		{
			name:    "the repository holds what the finding says is missing",
			listing: RepositoryListing{Commit: "abc123", Files: []string{"testdata/fixture.bin"}},
			verdict: `{"decision":"repair","summary":"missing fixture","findings":[{"severity":"blocker","message":"add the fixture","absent":"testdata/fixture.bin"}]}`,
			wantErr: "the repository holds it at abc123",
		},
		{
			name:    "the listing was bounded, so nothing could check",
			listing: RepositoryListing{Commit: "abc123", Files: []string{"README.md"}, Omitted: 3},
			verdict: `{"decision":"repair","summary":"missing fixture","findings":[{"severity":"blocker","message":"add the fixture","absent":"testdata/fixture.bin"}]}`,
			wantErr: "3 path(s) were left out of it",
		},
		{
			name:    "no listing could be taken, so nothing could check",
			listing: RepositoryListing{Unavailable: "the worktree would not answer"},
			verdict: `{"decision":"repair","summary":"missing fixture","findings":[{"severity":"blocker","message":"add the fixture","absent":"testdata/fixture.bin"}]}`,
			wantErr: "the worktree would not answer",
		},
		{
			// An approval carrying the same false reading is refused too: what is
			// wrong is the finding rather than the decision beside it.
			name:    "an approval carrying the claim is refused as readily",
			listing: RepositoryListing{Commit: "abc123", Files: []string{"README.md"}},
			verdict: `{"decision":"approve","summary":"fine","findings":[{"severity":"minor","message":"no readme","absent":"README.md"}]}`,
			wantErr: "the repository holds it at abc123",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeBackend{finalText: test.verdict}
			request := newRequest(nil)
			request.Repository = test.listing
			result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Review() error = %v, want it to contain %q", err, test.wantErr)
			}
			if result.Decision != "" {
				t.Fatalf("a refused verdict still resolved to %q", result.Decision)
			}
			// The verdict itself travels with the refusal, because what the reviewer
			// said is evidence about the reviewer whatever became of it.
			if result.Verdict.Summary == "" {
				t.Fatalf("the refused verdict was not carried: %#v", result)
			}
		})
	}
}

// The claim that checks out is not refused, which is the half that keeps the rule
// from simply banning the finding.
func TestReviewAcceptsAnAbsenceClaimTheListingBearsOut(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"repair","summary":"the fixture is not there","findings":[{"severity":"blocker","message":"add testdata/fixture.bin","absent":"testdata/fixture.bin"}]}`}
	request := newRequest(nil)
	request.Repository = RepositoryListing{Commit: "abc123", Files: []string{"README.md", "runner.go"}}

	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRepair || len(result.Verdict.Findings) != 1 {
		t.Fatalf("Review() = %#v", result)
	}
	if result.Verdict.Findings[0].Absent != "testdata/fixture.bin" {
		t.Fatalf("the absence claim was not carried: %#v", result.Verdict.Findings[0])
	}
}

// An absence claim is checked against a listing of repository-relative paths, so
// one written any other way could not be checked at all — and a claim that
// silently could not be checked is what the field exists to end.
func TestAnAbsenceClaimMustBeARepositoryRelativePath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		absent  string
		wantErr string
	}{
		{name: "absolute", absent: "/etc/passwd", wantErr: "must be repository-relative"},
		{name: "outside the repository", absent: "../other/file.go", wantErr: "must be inside the repository"},
		{name: "blank", absent: "   ", wantErr: "path is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			finding := Finding{Severity: SeverityBlocker, Message: "add it", Absent: test.absent}
			err := finding.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}

	valid := Finding{Severity: SeverityBlocker, Message: "add it", Absent: "testdata/fixture.bin"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() of a repository-relative claim error = %v", err)
	}
}

// A change that is a reasoned refusal to implement is a terminal outcome the
// review can accept. Repairing it is repair pressure toward a design nobody has
// decided, which is the exact outcome the block that stopped the work exists to
// prevent.
func TestReviewUpholdsAReasonedRefusalAtWorkItemScope(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"refusal_upheld","summary":"the item waits on an architect decision that does not exist; stopping was right"}`}
	request := newRequest(nil)
	request.DeveloperSummary = "I did not implement this: it waits on yoyodyne-100, whose design nobody has decided."

	result, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if result.Decision != DecisionRefusalUpheld {
		t.Fatalf("Review() decision = %q, want %q", result.Decision, DecisionRefusalUpheld)
	}
	for _, want := range []string{
		"approve, repair, or refusal_upheld",
		"A change may be a reasoned refusal to implement",
	} {
		if !strings.Contains(provider.request.SystemPrompt, want) {
			t.Errorf("contract is missing %q", want)
		}
	}
}

// An upheld refusal carrying work the change still has to do is two answers
// rather than one, and is refused on the rule an approval is held to.
func TestReviewRefusesAnUpheldRefusalThatStillDemandsWork(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"refusal_upheld","summary":"stopped correctly","findings":[{"severity":"blocker","message":"implement it anyway"}]}`}
	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), newRequest(nil)); err == nil ||
		!strings.Contains(err.Error(), "contradictory review verdict") {
		t.Fatalf("Review() error = %v", err)
	}
}

// A branch review judges commits already written and integrated, so there is no
// refusal in front of it to uphold. The vocabulary is withheld from the contract
// and the verdict is refused if it reaches for it regardless.
func TestBranchReviewHasNoRefusalToUphold(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{finalText: `{"decision":"refusal_upheld","summary":"nothing to do"}`}
	request := newRequest(nil)
	request.Scope = ScopeBranch
	request.WorkItemID = ""
	request.Branch = BranchScope{
		Name:       "milestone",
		BaseCommit: strings.Repeat("a", 40),
		HeadCommit: strings.Repeat("b", 40),
		Commits:    []gitworktree.Commit{{Commit: strings.Repeat("b", 40), Subject: "one"}},
	}
	request.Repository = RepositoryListing{Commit: strings.Repeat("b", 40), Files: []string{"README.md"}}

	if _, err := (Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel}).Review(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "has no refusal to uphold") {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(provider.request.SystemPrompt, "refusal_upheld") {
		t.Fatalf("branch contract offers a verdict the scope cannot reach: %s", provider.request.SystemPrompt)
	}
}
