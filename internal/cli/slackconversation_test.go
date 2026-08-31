package cli

// The Slack door as a client of the one durable conversation.
//
// What is tested here is the property the whole item exists for: a conversation
// begun in `yoyo chat` carries on in Slack and back, without losing state,
// because both are clients of one durable record rather than two conversations
// that would have to be reconciled. The two clients are two sessions over one
// state root, which is what two processes talking to one recorded conversation
// actually is.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
)

// A question asked at a terminal, answered from a channel, and carried back to
// the terminal: one conversation throughout, with the turns accumulating on it
// and the provider session resumed rather than restarted.
func TestAConversationBegunAtTheTerminalCarriesOnInSlackAndBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}

	// The terminal opens it and takes the first turn.
	terminal := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-1", FinalText: "The brief names no audience."},
	}, tracker)
	began := terminal.Evidence().ConversationID
	if terminal.Resumed() {
		t.Fatalf("the first client resumed something, and there was nothing to resume")
	}
	if _, err := terminal.Send(context.Background(), "what is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Slack picks up the same conversation, from a different process, and takes
	// the next turn through the door's own dispatch.
	fromSlack := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-2", FinalText: "Who it is for, and what finished looks like."},
	}, tracker)
	if !fromSlack.Resumed() {
		t.Fatalf("the Slack client started a new conversation instead of continuing the terminal's")
	}
	answer, err := sayToConversation(context.Background(), fromSlack, "say more about the audience", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if answer.ConversationID != began {
		t.Fatalf("conversation = %q, want the one the terminal began, %q", answer.ConversationID, began)
	}
	if !strings.Contains(answer.Text, "Who it is for") {
		t.Fatalf("answer = %q, want what the product manager said", answer.Text)
	}
	if answer.Turns != 2 {
		t.Fatalf("turns = %d, want the terminal's turn and this one on the same conversation", answer.Turns)
	}
	if answer.Harness {
		t.Fatalf("answer = %#v, want something the product manager said rather than the harness's own", answer)
	}

	// And back to the terminal, which finds the Slack turn already on the record
	// rather than a conversation that forked while it was away.
	returned := openTestChatSession(t, root, &recordingChatBackend{
		result: backendapi.RunResult{SessionID: "session-3", FinalText: "Understood."},
	}, tracker)
	evidence := returned.Evidence()
	if !returned.Resumed() || evidence.ConversationID != began {
		t.Fatalf("evidence = %#v, want the terminal back in the conversation it began, %q", evidence, began)
	}
	if evidence.Turns != 2 {
		t.Fatalf("turns = %d, want the turn taken from Slack to be on the record the terminal reads", evidence.Turns)
	}
	// The provider session the Slack turn recorded is what the terminal resumes,
	// which is the whole of why it is one conversation rather than two.
	if evidence.SessionID != "session-2" {
		t.Fatalf("session = %q, want the one the turn taken from Slack recorded", evidence.SessionID)
	}
}

// A proposal made in one client and approved from the other. It is the sharpest
// case of one conversation with two clients — the operator's "y" has to decide
// the proposal rather than be said to the product manager as speech — and the
// door dispatches it exactly as `yoyo chat --message` does, because a
// conversation whose two clients dispatched differently would be one where the
// same word did different things.
func TestAProposalMadeAtTheTerminalIsApprovedFromSlack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &recordingChatTracker{}

	proposing := &recordingChatBackend{result: backendapi.RunResult{SessionID: "session-1", FinalText: proposalReply}}
	terminal := openTestChatSession(t, root, proposing, tracker)
	if _, err := terminal.Send(context.Background(), "what should we do about usage limits?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(terminal.Proposals()) != 1 {
		t.Fatalf("proposals = %#v, want the one the turn proposed", terminal.Proposals())
	}

	// The approval arrives through the Slack door, in another process.
	answering := &recordingChatBackend{}
	fromSlack := openTestChatSession(t, root, answering, tracker)
	answer, err := sayToConversation(context.Background(), fromSlack, "y", discardLog)
	if err != nil {
		t.Fatalf("sayToConversation() error = %v", err)
	}
	if !answer.Harness {
		t.Fatalf("answer = %#v, want a decision to read as the harness's own rather than as speech", answer)
	}
	if !strings.Contains(answer.Text, "You decided 1 proposal(s)") {
		t.Fatalf("answer = %q, want what the decision did", answer.Text)
	}
	if answering.turns != 0 {
		t.Fatalf("the approval was said to the product manager %d time(s); it is a decision the harness carries out", answering.turns)
	}
	if len(tracker.creations) != 1 {
		t.Fatalf("creations = %#v, want the approved proposal to have reached the queue", tracker.creations)
	}
	// And the proposal is no longer waiting on anybody, in either client.
	if pending := fromSlack.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want the decided proposal off the table", pending)
	}
	back := openTestChatSession(t, root, &recordingChatBackend{}, tracker)
	if pending := back.Proposals(); len(pending) != 0 {
		t.Fatalf("pending = %#v, want the terminal to find the proposal decided rather than still waiting", pending)
	}
}

// Everything a turn said travels back even when the turn then failed, because a
// partial answer is worth reading and the door posts it ahead of the account of
// the failure.
func TestAFailedTurnStillCarriesBackWhatItManagedToSay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// A provider with no answer to give fails the turn, which is what a refusal
	// for want of capacity looks like from here.
	session := openTestChatSession(t, root, &recordingChatBackend{}, &recordingChatTracker{})
	answer, err := sayToConversation(context.Background(), session, "what should we build next?", discardLog)
	if err == nil {
		t.Fatalf("sayToConversation() error = nil, want the refused turn reported")
	}
	if answer.ConversationID == "" {
		t.Fatalf("answer = %#v, want the conversation named even where the turn failed", answer)
	}
}

// discardLog is the sink's log for a test that is not reading it.
func discardLog(string, ...any) {}
