package slack

// The product manager's own conversation, reached from this channel.
//
// The door in mention.go answers where things stand and, before this, said of
// everything else that it could not do it yet. This is what it does instead: an
// operator says something to this app and the product manager answers, in the
// thread they said it in and in its own voice.
//
// It creates no conversation of its own, and that is the whole of why a channel
// is allowed to hold one at all. What it speaks into is the durable conversation
// `yoyo chat` opens, keyed by the agent that holds it, recorded in the same state
// root and resumed from the same provider session — so a question asked at a
// terminal is answered from a phone and carried back to the terminal, because
// there is one conversation and two clients of it rather than two conversations
// that have to be reconciled. Nothing here keeps a transcript, a session, or a
// turn count: what this sink stores about a turn is what it always stored about
// a message, which is that it has already acted on it.
//
// Four rules hold the channel to that.
//
// Authority is the thread reply's, unchanged. Talking to the product manager
// admits work, reorders the queue, and spends the operator's money, so it is for
// the humans the project granted direct-work who bound a Slack member id and for
// nobody else. Somebody the project recognizes and granted nothing is told which
// grant they are missing; a stranger is stranger.go's, and neither is answered
// with silence.
//
// The wait is bounded, and bounded here rather than trusted to the callee. A
// conversation turn can take many minutes, and steering work from inside one can
// wait on a provider's usage limit for hours — which is right at a terminal,
// where somebody chose to wait, and wrong in a channel, where the only thing
// distinguishable from a slow answer is a dead sink. So one turn gets
// DefaultConversationDeadline and then the thread is told what happened, whether
// that is capacity, the conversation being held elsewhere, or anything else.
//
// One turn at a time, said out loud. The durable conversation admits one holder,
// so a second turn started while one is running would be refused by the lease
// rather than queued; this refuses it a moment earlier and in words, which is the
// same answer with somebody actually told.
//
// And it runs off the connection. The connection is a read loop Slack expects to
// keep reading, so a turn taken on it would hold every other message in the
// channel behind one operator's question.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Conversation is the durable operator conversation this door speaks into: one
// thing said, one answer back.
//
// It is deliberately this one method. Opening the conversation, holding it,
// resuming its provider session, and releasing it are the caller's, because they
// are what makes this the same conversation as the terminal's rather than a
// second one — and a sink that could hold it open between messages would be
// holding the lease against the operator's own `yoyo chat` for as long as the
// sink ran.
type Conversation interface {
	Say(ctx context.Context, said string) (Answer, error)
}

// Answer is one reply from the conversation, as this channel posts it.
//
// The prose is what goes in the thread. Everything else a turn did — work it
// admitted, items it reprioritized, a question it put to another role — reaches
// this channel through the ordinary feed, which reads the conversation's own
// durable log, so nothing here repeats it and nothing here is the only account
// of it.
//
// ConversationID and Turns say which conversation answered and where it has got
// to. They are not posted: an operator reading a thread wants the answer, and
// which durable record it came out of is the sink's log's business.
//
// Harness marks an answer nothing said — a decision the harness carried out on a
// proposal the conversation was waiting on, which spends no turn and is not the
// product manager speaking. It decides whose name and face the answer is posted
// under, because a message in a persona's voice that the persona did not say is
// attribution nobody can check.
type Answer struct {
	Text           string
	ConversationID string
	Turns          int
	Harness        bool
}

// DefaultConversationDeadline bounds one turn taken from this channel.
//
// It is generous against a turn and mean against a wait. One provider invocation
// is allowed a quarter of an hour and a single message may take several rounds of
// them, so a long answer can genuinely be cut off here — and that is the right
// trade, because the turn is recorded as it goes and the conversation is where it
// was left, while an operator watching a thread for an hour has been told
// nothing. What must never happen is the unbounded case: a turn that steers work
// can wait on an exhausted usage limit for as long as the configuration allows a
// run to, which is hours.
const DefaultConversationDeadline = 10 * time.Minute

// What the thread is told when a turn does not produce an answer. Each names
// where the conversation actually is, because the whole point of it being one
// conversation is that a client that could not answer is not the end of it.
const (
	conversationBusy = "The product manager is already answering something else in this conversation — say it again once that lands, or carry on at `yoyo chat`."
	conversationHeld = "The product manager's conversation is held by another client right now, which is usually a `yoyo chat` open at a terminal. Nothing was said to it; ask again once that one is closed."
	// conversationOverdue is the bounded wait running out. It says the turn may
	// yet land because that is true: the deadline stops this client waiting, and
	// what the conversation recorded of the turn before it was cut is in the
	// record either way.
	conversationOverdue = "I waited %s for the product manager and it had not answered, so I stopped waiting rather than leave you with nothing. `yoyo chat` continues the same conversation and will show where it got to."
	// conversationFailed carries the reason rather than summarizing it. A
	// provider out of capacity says so in its own words, and the durable record it
	// leaves is reported to this channel at warning severity by the feed, so this
	// is the person who asked being told as well as the channel.
	conversationFailed = "The product manager could not answer: %v"
	// conversationSilent is a turn that came back with nothing to say. It is an
	// answer rather than an absence for the reason every other line here is one.
	conversationSilent = "The product manager answered without saying anything. `yoyo chat` continues the same conversation."
	// conversationElsewhere is where the whole of a cut answer is. It is the
	// conversation rather than a command's output, because that is what holds it.
	conversationElsewhere = "`yoyo chat` continues the same conversation and shows the whole of it"
)

// converse carries one thing somebody said to the product manager, on a
// goroutine of its own, and answers in the thread they said it in.
//
// It reports nothing to its caller and stops nothing when it fails, which is the
// rule the whole inbound half follows: the connection goes on reading, and every
// path out of the turn either answers in the thread or says in the sink's log why
// it could not.
func (s *steering) converse(ctx context.Context, message inboundMessage, said string) {
	if !s.begin() {
		s.answerOnce(ctx, message, conversationBusy, "a turn already in flight")
		return
	}
	s.sink.log("this app was addressed by %s outside its own threads, saying %q, and is taking it to the product manager",
		message.user, singleLine(message.text, maxAskedBytes))
	s.turns.Add(1)
	go func() {
		defer s.turns.Done()
		defer s.finish()
		s.take(ctx, message, said)
	}()
}

// take is one turn: the thinking face while it runs, and then whatever came back
// posted in the product manager's own voice.
func (s *steering) take(ctx context.Context, message inboundMessage, said string) {
	// The mark goes on before the turn starts, because the minutes between
	// somebody typing and an answer landing are exactly where silence reads as
	// not-listening. It is never a gate: a workspace that will not take a mark
	// costs the message its mark and nothing else.
	s.mark(ctx, message.ts, notify.ReceiptUnderConsideration)
	bounded, stop := context.WithTimeout(ctx, s.deadline)
	defer stop()
	answer, err := s.conversation.Say(bounded, said)
	// What the turn managed to say before it failed is worth reading, so it goes
	// into the thread ahead of the account of the failure rather than behind it.
	if text := strings.TrimSpace(answer.Text); text != "" {
		s.reply(ctx, message, answer, text)
	}
	if err != nil {
		s.failed(ctx, message, bounded, err)
		return
	}
	if strings.TrimSpace(answer.Text) == "" {
		s.answerOnce(ctx, message, conversationSilent, "a turn that said nothing")
		return
	}
	s.sink.log("%s was answered from conversation %s, which stands at %d turn(s)",
		message.user, answer.ConversationID, answer.Turns)
	s.mark(ctx, message.ts, notify.ReceiptSettled)
}

// reply posts what came back, in the product manager's own name and face where
// the product manager is what said it.
func (s *steering) reply(ctx context.Context, message inboundMessage, answer Answer, text string) {
	speaker := notify.Persona(domain.RoleProductManager, "")
	if answer.Harness {
		speaker = notify.Harness()
	}
	if err := s.sink.answerAs(ctx, speaker, message, text, conversationElsewhere); err != nil {
		if ctx.Err() != nil {
			return
		}
		// The turn happened and is recorded whichever way this went, so what failed
		// is the account of it rather than the act. The line below is what points
		// whoever is watching this log at where the answer actually is.
		s.sink.log("%s was answered and the answer could not be posted, so it reads as silence there; `yoyo chat` continues conversation %s: %v",
			message.user, answer.ConversationID, err)
	}
}

// failed says in the thread why a turn produced no answer, telling the three
// cases apart because they are three different things for the operator to do.
func (s *steering) failed(ctx context.Context, message inboundMessage, bounded context.Context, err error) {
	switch {
	case errors.Is(bounded.Err(), context.DeadlineExceeded):
		s.answerOnce(ctx, message, fmt.Sprintf(conversationOverdue, s.deadline), "the wait running out")
	case errors.Is(err, runstate.ErrConversationHeld):
		s.answerOnce(ctx, message, conversationHeld, "the conversation being held elsewhere")
	default:
		s.answerOnce(ctx, message, fmt.Sprintf(conversationFailed, err), "why it could not answer")
	}
	s.mark(ctx, message.ts, notify.ReceiptRefused)
}

// begin takes the one turn this sink runs at a time, reporting whether it got it.
func (s *steering) begin() bool {
	s.turn.Lock()
	defer s.turn.Unlock()
	if s.taking {
		return false
	}
	s.taking = true
	return true
}

// finish gives it back.
func (s *steering) finish() {
	s.turn.Lock()
	defer s.turn.Unlock()
	s.taking = false
}

// settle waits for a turn the connection set off to finish. A sink that returned
// while one was still running would leave a goroutine writing to a log its
// process is done with, and would exit before the thread was told anything.
func (s *steering) settle() {
	s.turns.Wait()
}
