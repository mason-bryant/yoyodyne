package chat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/console"
	"yoyodyne/internal/execution"
)

// testCards is a batch of proposals as the operator is asked about them.
func testCards(count int) []card {
	cards := make([]card, 0, count)
	for i := 1; i <= count; i++ {
		cards = append(cards, card{
			number: i,
			proposal: PendingProposal{
				ID:             "2." + string(rune('0'+i)),
				ConversationID: "conversation-1",
				Turn:           2,
				Proposal: Proposal{
					Title:       "Something to do",
					Description: "What the work is.",
					Rationale:   "Why it follows.",
					Goal:        "Run development nearly autonomously.",
				},
			},
		})
	}
	return cards
}

// An approval has to name what it creates. That is the fail-closed rule the
// batch prompt inherits rather than relaxes: the only approval that names
// nothing is the one answering a single question, where there is exactly one
// item it could mean.
func TestOnlyAnApprovalThatNamesItsItemsCreatesAnything(t *testing.T) {
	t.Parallel()

	t.Run("a bare yes approves the one proposal it is asked about", func(t *testing.T) {
		t.Parallel()

		for _, answer := range []string{"y", "Y", "yes", "YES", " yes ", "approve"} {
			decisions, err := readDecisions(answer, testCards(1))
			if err != nil {
				t.Fatalf("readDecisions(%q) error = %v", answer, err)
			}
			if want := []decision{{proposalID: "2.1", approve: true}}; !reflect.DeepEqual(decisions, want) {
				t.Fatalf("readDecisions(%q) = %#v, want %#v", answer, decisions, want)
			}
		}
	})

	t.Run("a bare yes to several proposals names none of them and creates none", func(t *testing.T) {
		t.Parallel()

		for _, answer := range []string{"y", "yes", "approve", "approve all"} {
			decisions, err := readDecisions(answer, testCards(3))
			if err == nil {
				t.Fatalf("readDecisions(%q) = %#v, want a refusal", answer, decisions)
			}
			if errors.Is(err, errNotADecision) {
				t.Fatalf("readDecisions(%q) declined instead of asking which items were meant", answer)
			}
			if len(decisions) != 0 {
				t.Fatalf("readDecisions(%q) = %#v, want nothing decided", answer, decisions)
			}
		}
	})

	t.Run("an approval naming something that is not there decides nothing at all", func(t *testing.T) {
		t.Parallel()

		// The second clause is perfectly good. It is not carried out either: an
		// answer the harness cannot read whole is one it acts on no part of.
		decisions, err := readDecisions("approve 9 and decline 1 no", testCards(3))
		if err == nil || len(decisions) != 0 {
			t.Fatalf("readDecisions() = %#v, %v, want nothing decided", decisions, err)
		}
		if !strings.Contains(err.Error(), "9 is not one of the proposals") {
			t.Fatalf("readDecisions() error = %v", err)
		}
	})
}

func TestOneAnswerDecidesSeveralProposals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		answer string
		want   []decision
	}{
		{
			name:   "approvals and a decline in one line",
			answer: "approve 1,3 and decline 2 not this quarter",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.2", reason: "not this quarter"},
				{proposalID: "2.3", approve: true},
			},
		},
		{
			name:   "selectors separated however they were typed",
			answer: "approve 1, 3",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.3", approve: true},
			},
		},
		{
			name:   "selectors joined by a word",
			answer: "approve 1 and 2",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.2", approve: true},
			},
		},
		{
			name:   "the proposals named by their own identifiers",
			answer: "approve 2.2",
			want:   []decision{{proposalID: "2.2", approve: true}},
		},
		{
			name:   "everything declined at once, with the reason kept",
			answer: "decline all we are not doing any of this",
			want: []decision{
				{proposalID: "2.1", reason: "we are not doing any of this"},
				{proposalID: "2.2", reason: "we are not doing any of this"},
				{proposalID: "2.3", reason: "we are not doing any of this"},
			},
		},
		{
			name:   "a decline naming nothing declines what is left",
			answer: "approve 2 and decline too speculative",
			want: []decision{
				{proposalID: "2.1", reason: "too speculative"},
				{proposalID: "2.2", approve: true},
				{proposalID: "2.3", reason: "too speculative"},
			},
		},
		{
			// The decisions come back in the order the operator was shown them,
			// whatever order they were written in.
			name:   "decisions ordered by the cards rather than the clauses",
			answer: "approve 3,1",
			want: []decision{
				{proposalID: "2.1", approve: true},
				{proposalID: "2.3", approve: true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decisions, err := readDecisions(test.answer, testCards(3))
			if err != nil {
				t.Fatalf("readDecisions(%q) error = %v", test.answer, err)
			}
			if !reflect.DeepEqual(decisions, test.want) {
				t.Fatalf("readDecisions(%q) = %#v, want %#v", test.answer, decisions, test.want)
			}
		})
	}
}

// An answer nobody can be sure of declines, and is kept as the reason. That is
// the contract's own rule, applied to as many proposals as were on the table.
func TestAnAnswerThatDecidesNothingDeclines(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"", "hmm", "not this quarter", "aprove 1", "maybe the first one"} {
		if _, err := readDecisions(answer, testCards(3)); !errors.Is(err, errNotADecision) {
			t.Fatalf("readDecisions(%q) error = %v, want it read as no decision at all", answer, err)
		}
	}
	// What the conversation then does with it: every proposal declined, with the
	// answer itself as the reason.
	declined := declineAll(testCards(2), "not this quarter")
	want := []decision{
		{proposalID: "2.1", reason: "not this quarter"},
		{proposalID: "2.2", reason: "not this quarter"},
	}
	if !reflect.DeepEqual(declined, want) {
		t.Fatalf("declineAll() = %#v, want %#v", declined, want)
	}
}

// A decline's reason is the operator's own words and runs to the end of the
// line, so an approval written after one is never carried out. Nothing is
// created by accident; the proposal it named is simply put again.
func TestADeclineReasonRunsToTheEndOfTheAnswer(t *testing.T) {
	t.Parallel()

	decisions, err := readDecisions("decline 2 no goal and approve 1", testCards(3))
	if err != nil {
		t.Fatalf("readDecisions() error = %v", err)
	}
	want := []decision{{proposalID: "2.2", reason: "no goal and approve 1"}}
	if !reflect.DeepEqual(decisions, want) {
		t.Fatalf("readDecisions() = %#v, want %#v", decisions, want)
	}
}

func TestAnAnswerThatContradictsItselfDecidesNothing(t *testing.T) {
	t.Parallel()

	decisions, err := readDecisions("approve 1 and decline 1 on second thoughts", testCards(3))
	if err == nil || len(decisions) != 0 {
		t.Fatalf("readDecisions() = %#v, %v, want nothing decided", decisions, err)
	}
	if !strings.Contains(err.Error(), "decided twice") {
		t.Fatalf("readDecisions() error = %v", err)
	}
}

// A card is presentation. The frame makes the boundary between two proposals
// findable, and everything that carries meaning survives its removal: the same
// card drawn where nothing may be dressed is the heading and the body it always
// was.
func TestAProposalCardCarriesNoMeaningInItsFrame(t *testing.T) {
	t.Parallel()

	entry := testCards(1)[0]
	entry.proposal.Proposal.Parent = "yoyodyne-ifd.1"
	entry.proposal.Proposal.Dependencies = []string{"yoyodyne-ifd.2"}

	plain := entry.Render(console.Theme{})
	want := strings.Join([]string{
		"1 · proposal 2.1 · Something to do",
		"    What the work is.",
		"    why: Why it follows.",
		"    goal: Run development nearly autonomously.",
		"    parent: yoyodyne-ifd.1",
		"    depends on: yoyodyne-ifd.2",
		"",
	}, "\n")
	if plain != want {
		t.Fatalf("undressed card =\n%q\nwant\n%q", plain, want)
	}

	theme := console.NewTheme(func(name string) string {
		if name == "TERM" {
			return "xterm-256color"
		}
		return ""
	}, func() int { return 60 })
	framed := entry.Render(theme)
	if !strings.Contains(framed, "╭─ 1 · proposal 2.1 · Something to do ") {
		t.Fatalf("framed card =\n%s", framed)
	}
	// Every line of the body is inside the frame, and the frame is the only
	// thing the plain rendering does not have.
	for _, line := range entry.proposal.body() {
		if !strings.Contains(framed, "│ "+line) {
			t.Fatalf("framed card =\n%s\nwant it to carry %q", framed, line)
		}
	}
	if !strings.Contains(framed, "\n╰") {
		t.Fatalf("framed card =\n%s\nwant it closed", framed)
	}
}

// The failure this work item exists for: five proposals in one turn used to be
// five prompts in a row. They are decided in one answer now, and every decision
// in it is recorded exactly as a serial answer records it — the approval before
// the creation it authorized, the decline with the operator's own reason.
func TestSeveralProposalsAreDecidedInOneAnswer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: proposalReply(
			"Four things follow from that.",
			`{"title":"Pause on a usage limit","description":"Wait and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}`,
			`{"title":"Rewrite the CLI in Rust","description":"Port everything.","rationale":"It would be faster.","goal":"Support development in any language."}`,
			`{"title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule.","goal":"Run development nearly autonomously."}`,
			`{"title":"Publish a pull request","description":"Push the branch.","rationale":"You asked for review on the forge.","goal":"Run development nearly autonomously."}`,
		),
	}}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	// Two approved, two declined, in one line, and the conversation carries on
	// rather than asking again.
	input := strings.NewReader("what next?\napprove 1,3 and decline 2,4 not this quarter\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	created := make([]string, 0, len(tracker.created))
	for _, item := range tracker.created {
		created = append(created, item.Title)
	}
	if want := []string{"Pause on a usage limit", "Add a retry budget"}; !reflect.DeepEqual(created, want) {
		t.Fatalf("created work items = %#v, want %#v", created, want)
	}
	if pending := session.Proposals(); len(pending) != 0 {
		t.Fatalf("awaiting decision = %#v, want the whole batch decided", pending)
	}

	transcript := out.String()
	for _, required := range []string{
		"proposes 4 work item(s)",
		// The cards are numbered, and the numbers are what the answer used.
		"1 · proposal 1.1 · Pause on a usage limit",
		"4 · proposal 1.4 · Publish a pull request",
		"decide 4 proposals?",
		"created yoyodyne-1",
		"declined 1.2",
		"created yoyodyne-2",
		"declined 1.4",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// One prompt decided all four: the operator was not interrogated.
	if asked := strings.Count(transcript, "decide 4 proposals?"); asked != 1 {
		t.Fatalf("the operator was asked %d times, want once", asked)
	}

	// Each decision is its own record, in the order the operator was shown them,
	// and each approval precedes the creation it authorized.
	counted := countEvents(t, root, session)
	if counted[execution.EventProposalApproved] != 2 ||
		counted[execution.EventProposalCreated] != 2 ||
		counted[execution.EventProposalRejected] != 2 {
		t.Fatalf("recorded proposal events = %#v", counted)
	}
	var order []execution.EventType
	for _, event := range loadTestEvents(t, root, session) {
		switch event.Type {
		case execution.EventProposalApproved, execution.EventProposalCreated:
			order = append(order, event.Type)
		}
	}
	want := []execution.EventType{
		execution.EventProposalApproved, execution.EventProposalCreated,
		execution.EventProposalApproved, execution.EventProposalCreated,
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("recorded order = %#v, want each approval before what it created", order)
	}
}

// Batching changes the prompt and not the contract. An answer nobody can be
// sure of creates nothing, whether it is nonsense, an approval that names no
// item, or an approval of something that is not on the table.
func TestABatchAnswerNeverCreatesWhatItDidNotName(t *testing.T) {
	t.Parallel()

	twoProposals := func(t *testing.T) (*fakeTracker, *Session, *strings.Builder) {
		t.Helper()

		tracker := &fakeTracker{}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1",
			FinalText: proposalReply(
				"Two things.",
				`{"title":"Pause on a usage limit","description":"Wait and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}`,
				`{"title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule.","goal":"Run development nearly autonomously."}`,
			),
		}}})
		options.Tracker = tracker
		return tracker, openTestSession(t, options), &strings.Builder{}
	}

	t.Run("a bare yes to a batch creates nothing and asks again", func(t *testing.T) {
		t.Parallel()

		tracker, session, out := twoProposals(t)
		if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\ny\napprove 2\n"), out)); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		// The bare yes named neither item, so it created neither; the answer
		// after it named one, and that is the one that exists.
		if len(tracker.created) != 1 || tracker.created[0].Title != "Add a retry budget" {
			t.Fatalf("created work items = %#v", tracker.created)
		}
		if !strings.Contains(out.String(), "an approval has to name what it creates") {
			t.Fatalf("transcript = %q", out.String())
		}
		// The one it did not decide is still waiting rather than declined.
		if pending := session.Proposals(); len(pending) != 1 || pending[0].ID != "1.1" {
			t.Fatalf("awaiting decision = %#v", pending)
		}
	})

	t.Run("an answer that is not a decision declines the batch and is kept as the reason", func(t *testing.T) {
		t.Parallel()

		tracker, session, out := twoProposals(t)
		if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\nnot this quarter\n/exit\n"), out)); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		if len(tracker.created) != 0 {
			t.Fatalf("an answer that decided nothing created %#v", tracker.created)
		}
		if pending := session.Proposals(); len(pending) != 0 {
			t.Fatalf("awaiting decision = %#v, want the batch declined", pending)
		}
		transcript := out.String()
		for _, required := range []string{"declined 1.1", "declined 1.2"} {
			if !strings.Contains(transcript, required) {
				t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
			}
		}
	})
}
