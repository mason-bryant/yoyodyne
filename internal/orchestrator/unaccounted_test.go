package orchestrator

// A developer invocation that ends on an interim progress line used to be
// accepted as the run's reply: the terminal said completed, the process had not
// failed, and the summary, the landing claim, the reports and the proposals were
// all silently empty. The regression case here is run-9ad1799e, both of whose
// attempts ended with "the check is running; I'll report when it lands", and
// which the harness recorded as an account of the work.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The run-9ad1799e line, and everything around it that must not be mistaken for
// it. The false negatives matter as much as the true positive: refusing a real
// account costs a whole developer invocation to ask for something the run
// already had.
func TestAReplyThatAccountsForNothingIsToldApartFromOneThatDoes(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		summary        string
		landingOutcome string
		landingProblem string
		want           bool
	}{
		"the regression case": {
			summary: "the check is running; I'll report when it lands",
			want:    true,
		},
		"a reply that said nothing at all": {
			summary: "   \n\t",
			want:    true,
		},
		"a promise with the provider's own apostrophe": {
			summary: "I’ll report back once the suite finishes.",
			want:    true,
		},
		"a bare acknowledgement": {
			summary: "On it.",
			want:    true,
		},
		"an account of the work": {
			summary: "Added the missing guard in parser.go and extended parser_test.go; go test ./internal/backend/... passes.",
			want:    false,
		},
		"an account that mentions a check it left running": {
			summary: "Added the missing guard in parser.go. The full suite is still running; I'll report when it lands.",
			want:    false,
		},
		"an account that reports a check it ran": {
			summary: "Reworked the parser so the terminal is read once. make check exits 0.",
			want:    false,
		},
		"an interim line under a landing claim": {
			summary:        "the check is running; I'll report when it lands",
			landingOutcome: string(runstate.LandingEvidence),
			want:           false,
		},
		"an interim line under a claim nobody could read": {
			summary:        "the check is running; I'll report when it lands",
			landingProblem: "the landing block is not valid JSON",
			want:           false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reason, nothing := accountedForNothing(fixture.summary, fixture.landingOutcome, fixture.landingProblem)
			if nothing != fixture.want {
				t.Fatalf("accountedForNothing(%q) = %v (%q), want %v", fixture.summary, nothing, reason, fixture.want)
			}
			if nothing && reason == "" {
				t.Fatal("a reply that accounts for nothing was refused without saying why")
			}
		})
	}
}

// The developer ends its first invocation on the interim line and is asked for
// the account rather than having the line filed as one. It answers, and the run
// proceeds exactly as a run whose developer accounted for itself the first time:
// the account is the summary, the change is reviewed and integrated, and no
// repair attempt was spent on a change nothing was wrong with.
func TestADeveloperThatEndedOnInterimProgressIsAskedForTheAccount(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"the check is running; I'll report when it lands",
		"Added feature.txt and ran the configured check, which passes. No risk outstanding.",
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("a re-asked developer did not finish its run: %#v", outcome)
	}
	if outcome.Summary != "Added feature.txt and ran the configured check, which passes. No risk outstanding." {
		t.Fatalf("summary = %q, want the account rather than the interim line", outcome.Summary)
	}
	requests := provider.requestsForRole(domain.RoleDeveloper)
	if len(requests) != 2 {
		t.Fatalf("the developer was invoked %d time(s), want the first attempt and one re-ask", len(requests))
	}
	// The re-ask says what is missing, says the work itself is not, and continues
	// the session that did the work: a developer told only that its reply was
	// refused would start the change over.
	reasked := requests[1]
	if !strings.Contains(reasked.Prompt, "did not account for the work") ||
		!strings.Contains(reasked.Prompt, "interim progress") {
		t.Fatalf("the re-ask does not say what was missing:\n%s", reasked.Prompt)
	}
	if !strings.Contains(reasked.Prompt, "The work you already did is untouched") {
		t.Fatalf("the re-ask does not say the change is not what was refused:\n%s", reasked.Prompt)
	}
	if reasked.SessionID != provider.developerSession {
		t.Fatalf("the re-ask ran in session %q, want the session that did the work", reasked.SessionID)
	}
	// A missing account is not something a repair attempt fixes, and spending one
	// would take the budget from the failures repairs exist for.
	if outcome.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a re-ask that spends none", outcome.RepairAttempts)
	}
}

// The developer says the same thing twice. The run ends naming that, rather than
// completing with an interim line recorded as its account: a run that says "I
// will report later" and then vanishes is unaccounted work wearing a completed
// status, which is what every downstream judgement reads.
func TestARunEndsWhenItsDeveloperNeverAccountsForTheWork(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the check is running; I'll report when it lands"
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "without accounting for the work") {
		t.Fatalf("Run() error = %v, want a run that failed naming the unaccounted reply", err)
	}
	if outcome.Status != runstate.StatusFailed {
		t.Fatalf("outcome = %#v, want a failed run", outcome)
	}
	if outcome.Summary != "" {
		t.Fatalf("summary = %q, want an interim line recorded as no account at all", outcome.Summary)
	}
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("an item closed against work nothing accounted for; calls = %v", tracker.calls)
	}
	// Asked once and no more: the third invocation would be the largest thing the
	// harness buys, spent on the same question.
	if invocations := len(provider.requestsForRole(domain.RoleDeveloper)); invocations != 2 {
		t.Fatalf("the developer was invoked %d time(s), want the attempt and one re-ask", invocations)
	}
	// And what a person reads afterwards says which failure this was.
	if !strings.Contains(tracker.notes, "accounting for the work") {
		t.Fatalf("the recorded failure does not name the unaccounted reply: %q", tracker.notes)
	}
	recorded := onlyRecordedRun(t, store)
	if recorded.Outcome != runstate.OutcomeFailed {
		t.Fatalf("run outcome = %q, want %q", recorded.Outcome, runstate.OutcomeFailed)
	}
}
