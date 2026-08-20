package chat

// What survives the process that proposed the work. A proposal made by one
// `yoyo chat --message` is decided by another, so everything a decision needs —
// the proposal itself, the identifier that names it, and the account the agent
// is owed afterwards — has to be in the record rather than in memory.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// oneProposalTurn is a reply that proposes a single work item under a project
// that asks about every one of them, so nothing is created and the operator is
// left with something to decide.
const oneProposalTurn = `We could wait out the limit rather than failing the run.

` + "```yoyodyne-proposal" + `
{"items":[{"title":"Pause on a usage limit","description":"Wait for the window and resume.","rationale":"Capacity is not failure.","goal":"Run development nearly autonomously."}]}
` + "```" + `
`

// The defect: a proposal lived only in the process that made it, so a later one
// had nothing an approval could name. The operator's "y" became ordinary speech
// to a role that cannot create work, and nothing reached the queue.
func TestAProposalOutlivesTheProcessThatMadeItSoALaterOneCanDecideIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}

	proposing := perItemApprovalOptions(t, root, tracker, oneProposalTurn)
	proposed := openTestSession(t, proposing)
	reply, err := proposed.Send(context.Background(), "what should we do about usage limits?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("reply proposed %d item(s), want one to decide", len(reply.Proposals))
	}
	if len(tracker.created) != 0 {
		t.Fatalf("%d item(s) were created before anybody approved anything", len(tracker.created))
	}

	// A second process, holding nothing but the record the first one left.
	deciding := perItemApprovalOptions(t, root, tracker, "It is in the queue now.")
	resumed := openTestSession(t, deciding)
	pending := resumed.Proposals()
	if len(pending) != 1 {
		t.Fatalf("%d proposal(s) came back from the record, want the undecided one", len(pending))
	}
	if pending[0].ID != reply.Proposals[0].ID {
		t.Fatalf("proposal came back as %q, want %q; an approval names it by this", pending[0].ID, reply.Proposals[0].ID)
	}
	if pending[0].Proposal.Title != "Pause on a usage limit" || pending[0].Proposal.Rationale == "" {
		t.Fatalf("proposal came back as %#v, want what the operator was shown", pending[0].Proposal)
	}

	outcomes, decided, err := resumed.Decide(context.Background(), "y")
	if err != nil || !decided {
		t.Fatalf("Decide() = %v, %t, %v; want the bare approval to decide the one proposal on the table", outcomes, decided, err)
	}
	if len(outcomes) != 1 || !outcomes[0].Approved || outcomes[0].WorkItemID == "" {
		t.Fatalf("outcomes = %#v, want one approval that created an item", outcomes)
	}
	if len(tracker.created) != 1 || tracker.created[0].Title != "Pause on a usage limit" {
		t.Fatalf("tracker created %#v, want the proposed work", tracker.created)
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) are still pending after being decided", len(resumed.Proposals()))
	}

	// The decision is on the record, not only in the tracker: what the operator
	// approved and what came of it are both events a later reader can find.
	recorded := countEvents(t, root, resumed)
	for _, wanted := range []execution.EventType{execution.EventProposalRecorded, execution.EventProposalApproved, execution.EventProposalCreated} {
		if recorded[wanted] != 1 {
			t.Fatalf("%s recorded %d time(s), want once; events = %v", wanted, recorded[wanted], recorded)
		}
	}

	// And the agent hears about it. It proposed the work and was never told what
	// became of it, which is how a product manager comes to describe a queue it
	// has not seen.
	if _, err := resumed.Send(context.Background(), "is it in the queue?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	told := deciding.Backend.(*fakeBackend).requests[0].Prompt
	if !strings.Contains(told, "approved proposal") || !strings.Contains(told, "yoyodyne-1") {
		t.Fatalf("the next turn was told %q, want an account of the approval and the item it created", told)
	}
}

// A third process must not be able to spend the same approval twice. The
// decision left the pending set when it was made, so what a later conversation
// resumes is a conversation with nothing on the table.
func TestADecidedProposalDoesNotComeBackFromTheRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	deciding := openTestSession(t, perItemApprovalOptions(t, root, tracker, "declined"))
	if _, _, err := deciding.Decide(context.Background(), "no thanks, we do this already"); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}

	third := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	if pending := third.Proposals(); len(pending) != 0 {
		t.Fatalf("a declined proposal came back as pending: %#v", pending)
	}
	outcomes, decided, err := third.Decide(context.Background(), "y")
	if decided || err != nil || len(outcomes) != 0 {
		t.Fatalf("Decide() = %v, %t, %v; want nothing on the table to decide", outcomes, decided, err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("a declined proposal created %d item(s)", len(tracker.created))
	}
}

// An answer that is not a decision decides nothing. A prompt may read anything
// it cannot understand as a decline, because it asked the question; a message
// was never asked, and reading it that way would turn work down that the
// operator never mentioned.
func TestAMessageThatDecidesNothingLeavesEveryProposalWhereItWas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "two of them are about capacity"))
	outcomes, decided, err := resumed.Decide(context.Background(), "what else is open?")
	if decided || err != nil || len(outcomes) != 0 {
		t.Fatalf("Decide() = %v, %t, %v; want a question to be a question", outcomes, decided, err)
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the undecided one exactly where it was", len(resumed.Proposals()))
	}
}

// A decision the harness cannot carry out whole carries out none of it, and is
// reported as a decision rather than passed on as speech: an approval naming a
// proposal that is not there is exactly the answer that must not go missing.
func TestADecisionNamingAProposalThatIsNotThereFailsRatherThanDecidingNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	outcomes, decided, err := resumed.Decide(context.Background(), "approve 9.9")
	if !decided {
		t.Fatalf("Decide() reported nothing decided, want an approval that failed rather than speech")
	}
	if err == nil || !strings.Contains(err.Error(), "9.9") {
		t.Fatalf("Decide() error = %v, want it to name what could not be decided", err)
	}
	if len(outcomes) != 0 || len(tracker.created) != 0 {
		t.Fatalf("outcomes = %#v and %d creation(s); want none of it carried out", outcomes, len(tracker.created))
	}
	if len(resumed.Proposals()) != 1 {
		t.Fatalf("%d proposal(s) are pending, want the real one still waiting", len(resumed.Proposals()))
	}
}

// perItemApprovalOptions is a conversation under the gate the harness starts
// with: the operator is asked about every work item, so a proposal is something
// to decide rather than something already admitted. The store is the caller's,
// so two of these are two processes talking to one recorded conversation.
func perItemApprovalOptions(t *testing.T, root string, tracker Tracker, reply string) Options {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: reply}}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Admission = Admission{WorkItems: domain.ApprovalHuman}
	return options
}

// A conversation that opens with a proposal still waiting puts it to the
// operator rather than carrying it silently to the end and naming it as
// undecided a second time. It is the interactive half of the same fix: a
// proposal made by a single message is decidable wherever the operator turns up
// next.
func TestAConversationOpensByPuttingWhatIsStillWaitingToTheOperator(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	proposed := openTestSession(t, perItemApprovalOptions(t, root, tracker, oneProposalTurn))
	if _, err := proposed.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	resumed := openTestSession(t, perItemApprovalOptions(t, root, tracker, "nothing to say"))
	var out strings.Builder
	// One line in: the approval of what was already waiting, and then the input
	// ends, so no turn is ever taken.
	if err := resumed.Converse(context.Background(), testConsole(strings.NewReader("y\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if !strings.Contains(transcript, "still waiting on you") || !strings.Contains(transcript, "Pause on a usage limit") {
		t.Fatalf("transcript = %q, want the waiting proposal put to the operator", transcript)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v, want the approved item", tracker.created)
	}
	if len(resumed.Proposals()) != 0 {
		t.Fatalf("%d proposal(s) still pending after being approved", len(resumed.Proposals()))
	}
}
