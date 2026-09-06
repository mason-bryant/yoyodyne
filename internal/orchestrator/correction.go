package orchestrator

// Waking the role whose tracker block the harness refused, with nobody standing
// there to do the waking.
//
// A refused block already reaches the role that sent it: the refusal is durable,
// and the harness's own words open that conversation's next turn, so the role can
// re-issue the actions itself rather than waiting to be told. What was missing was
// the turn. Nothing started one, so the correction waited on a person opening the
// conversation — and both of this week's refused batches, the oversized park
// reason and the malformed handle id, needed the operator's assistant to prompt
// the re-issue of actions the product manager could have corrected herself.
//
// So the harness starts it. What that changes is the courier and nothing else: the
// refusal is the one the loud-refusal machinery already wrote, the correction is
// the role's own reply, and the actions happen only if it issues them again.
//
// # One turn per refusal, and then the operator
//
// A refusal is put to the role once. The claim is durable and is taken before the
// turn, so a pass that dies between the two has recorded a wakeup nobody made
// rather than made one nobody recorded — the second is what would fire again on
// the next pass, and on every pass after it. The one wakeup that does not count
// is the one the provider refused for want of capacity: no model saw the message,
// so the attempt is given back and the refusal keeps its turn, bounded and paced
// by the record.
//
// What the second refusal earns is the operator rather than another turn. A role
// handed its refusal back that sends a block refused again has shown that another
// copy of the same message will not fix it, and a harness that kept waking it
// would spend a turn a pass on a conversation that cannot answer. That ending is
// recorded where the refusal is, by the conversation itself, and it is what the
// operator is told; see chat.Session.recordRefusedTrackerBlock.
//
// # The same trigger class as the schedule
//
// This is fired from the pull, beside the recurring tasks and the stopped-work
// delivery, and for the same three reasons. The harness is the only thing that
// invokes a role, so a wakeup living in a cron entry or a launchd job would be a
// second invoker of one. The pull is where the harness is already deciding what to
// do next, so a refusal recorded at any hour is woken for at the next interval
// rather than at the next time somebody looks. And it runs on the non-model side,
// so a provider window that stops turns does not stop the wakeup being scheduled
// or spend it: a turn the window refused is given back and made again once the
// window has had time to clear.
//
// # The pause and not the intake hold
//
// A wakeup is a provider invocation, so the operator's pause covers it exactly as
// it covers a run, a conversation turn, a delivery, and a firing. The intake hold
// is deliberately not read: holding intake stops the harness choosing work, and
// this chooses nothing, claims no work item, and starts no run — what it produces
// is a role putting right its own lost actions, which is usually part of what a
// held queue is waiting on.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// CorrectionConversations is where the refusals are read from. It is read and
// never written here: what a conversation records about a refused block is that
// conversation's own, and this only asks which of them is owed a turn.
//
// It is satisfied by *runstate.ConversationStore.
type CorrectionConversations interface {
	Recorded() ([]runstate.Conversation, error)
}

// CorrectionClaims is where one wakeup is claimed before it is made. It is what
// makes the wakeup at most once per refusal, and a trigger wired without it is
// one that would wake the same conversation on every pull.
//
// It is satisfied by *runstate.ConversationStore.
type CorrectionClaims interface {
	ClaimRefusalWakeup(identity runstate.ConversationIdentity, at time.Time) (runstate.TrackerRefusal, error)
	WithdrawRefusalWakeup(ctx context.Context, identity runstate.ConversationIdentity, turn int) error
}

// ErrProviderWindow reports a wakeup the provider refused for want of capacity.
// It is its own sentinel because it is the one failure that provably put nothing
// in front of the role and provably clears: no model saw the message, and the
// window ends. Every other way a wakeup fails either reached the role or is
// something a person has to change, and neither is worth asking again on a timer.
var ErrProviderWindow = errors.New("the provider refused the wakeup for want of capacity")

// CorrectionRole is a role's conversation as the harness reaches it: one message
// sent into it, and what came back.
//
// Nothing here decides anything on the role's behalf and nothing carries its
// decisions out. The refusal the woken turn answers is the one already waiting in
// that conversation's own record, so what is sent is why it was woken and nothing
// the harness invented about what was wrong.
type CorrectionRole interface {
	Wake(ctx context.Context, identity runstate.ConversationIdentity, message string) (CorrectionTurn, error)
}

// CorrectionTurn is what one woken turn came to.
type CorrectionTurn struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// CostUSD is what the provider charged for the turn, as it reported it. It is
	// carried back because a wakeup is a spend the caller made rather than one a
	// run made, so a session counting what it has spent has no other way to see
	// it. A turn that failed carries what it cost too: the provider charged for it
	// exactly as it charges for one that answered.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Actions is how many tracker actions the woken turn got carried out, which is
	// the whole of what says the correction landed. A turn that answered in prose
	// re-issued nothing and reports none.
	Actions int `json:"actions,omitempty"`
	// Refused is the refusal the woken turn's own block earned, where it earned
	// one. It is not a failed turn — the role answered — and it is the ending that
	// goes to the operator rather than earning another wakeup.
	Refused string `json:"refused,omitempty"`
}

// Corrected is one refusal this pass woke a role for, and what came back. It
// reports a wakeup that did not happen as carefully as one that did.
type Corrected struct {
	ConversationID string           `json:"conversation_id,omitempty"`
	Role           domain.AgentRole `json:"role"`
	// Agent is which of a role's conversations was woken, because a project may
	// configure two agents for one role and the refusal belongs to one of them.
	Agent string `json:"agent,omitempty"`
	// Turn is the conversation turn whose block was refused, so what this pass says
	// names the loss rather than only the correction.
	Turn int `json:"turn"`
	// Woken reports a turn actually taken. A wakeup that never reached the role is
	// never reported as one that did.
	Woken bool `json:"woken"`
	// Actions is how many the woken turn got carried out, and CostUSD what the turn
	// cost.
	Actions int     `json:"actions,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Problem is what stopped or spoiled the wakeup, including the woken turn's own
	// block being refused again.
	Problem string `json:"problem,omitempty"`
}

// CorrectionSweep is what one pass did about the refusals. A pass that found none
// reports nothing, which is almost every pass.
type CorrectionSweep struct {
	Corrected []Corrected `json:"corrected,omitempty"`
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and every refusal keeps the wakeup it is owed.
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
}

// Corrector wakes roles whose tracker blocks were refused. It has no tracker, no
// worktree access, and no forge access, and it starts nothing: what it does is
// put a role in front of a refusal that is already in its own conversation, and
// write down that it did.
type Corrector struct {
	// Conversations is where the refusals are read from. Required.
	Conversations CorrectionConversations
	// Claims is what makes the wakeup at most once per refusal. Required.
	Claims CorrectionClaims
	// Roles is how the harness reaches a role's conversation. Required.
	Roles CorrectionRole
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and a corrector wired without one is one nothing can pause, which
	// is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// Correct wakes the role whose refusal has waited longest, and reports what came
// of it.
//
// The order is the order the guarantees need. The pause is read before anything
// is claimed, so a paused harness costs no refusal its wakeup; the wakeup is
// claimed before the turn is taken, so a process that dies between the two has
// recorded a wakeup that produced nothing rather than made one nothing paces.
//
// One per pass, for the reason the delivery and the firing beside it are bounded
// the same way: a wakeup is a conversation turn, and a pass that woke four
// conversations would hold the queue closed for as long as all four took. The next
// pass takes the next refusal, and on a poll loop that is an interval later.
func (c Corrector) Correct(ctx context.Context) (CorrectionSweep, error) {
	if err := c.validate(); err != nil {
		return CorrectionSweep{}, err
	}
	hold, held, err := c.paused()
	if err != nil {
		return CorrectionSweep{}, err
	}
	if held {
		return CorrectionSweep{Paused: &hold}, nil
	}
	conversations, err := c.Conversations.Recorded()
	if err != nil {
		return CorrectionSweep{}, fmt.Errorf("read the recorded conversations: %w", err)
	}
	owed := awaitingCorrection(conversations, c.now())
	if len(owed) == 0 {
		return CorrectionSweep{}, nil
	}
	var problems []error
	for _, conversation := range owed {
		corrected, woken, err := c.wake(ctx, conversation)
		if err != nil {
			problems = append(problems, err)
			// A claim this pass could not make is this pass's one wakeup spent on
			// finding that out: trying the next conversation would be a pass that woke
			// two roles because the first attempt failed, which is the bound this is
			// under going away exactly when something is already wrong.
			break
		}
		if !woken {
			// Another process claimed this refusal between the reading and the claim,
			// which is the record doing its job. The next one is this pass's to look at.
			continue
		}
		return CorrectionSweep{Corrected: []Corrected{corrected}}, errors.Join(problems...)
	}
	return CorrectionSweep{}, errors.Join(problems...)
}

// awaitingCorrection is every conversation owed a wakeup, oldest refusal first.
//
// The order is by when the block was refused rather than by which conversation it
// is, because what the harness owes is a correction and the one that has been
// unanswered longest is the one whose actions have been missing from the queue
// longest.
func awaitingCorrection(conversations []runstate.Conversation, now time.Time) []runstate.Conversation {
	var owed []runstate.Conversation
	for _, conversation := range conversations {
		if conversation.RefusedBlock == nil || !conversation.RefusedBlock.AwaitingWakeup(now) {
			continue
		}
		owed = append(owed, conversation)
	}
	sort.SliceStable(owed, func(i, j int) bool {
		return owed[i].RefusedBlock.RefusedAt.Before(owed[j].RefusedBlock.RefusedAt)
	})
	return owed
}

// wake claims one refusal's turn and takes it, reporting whether a claim was made
// at all so a refusal another process took leaves this pass looking at the next
// one rather than reporting a wakeup nobody made.
//
// The claim is kept for every ending but one, which is deliberately narrower than
// the stopped-work delivery's give-back. A conversation nothing can open — no
// agent configured for the role, a provider nobody has signed in — is something a
// person has to change, so retrying it every pass would be a refused wakeup a
// minute and this pass's one turn taken from every refusal behind it. What such a
// refusal falls back to is what it had before this existed: the harness's own
// words opening the role's next turn, whenever one happens.
//
// The exception is the provider refusing the turn for want of capacity. That one
// provably put nothing in front of the role and provably clears on its own, and
// the wakeup running on the non-model side is worth nothing if a window silently
// spends it — so the attempt is given back and the refusal keeps the turn it is
// owed, bounded by MaxRefusalWakeups and paced by RefusalWakeupRetryDelay.
func (c Corrector) wake(ctx context.Context, conversation runstate.Conversation) (Corrected, bool, error) {
	identity := conversation.Identity()
	corrected := Corrected{
		ConversationID: conversation.ConversationID,
		Role:           conversation.Role,
		Agent:          identity.Agent,
		Turn:           conversation.RefusedBlock.Turn,
	}
	claimed, err := c.Claims.ClaimRefusalWakeup(identity, c.now())
	if err != nil {
		// A refusal another process woke for between the reading above and this
		// claim, and a conversation somebody is mid-turn with, mean the same thing
		// here: this is not the pass that wakes it. Neither is a failure to report.
		if errors.Is(err, runstate.ErrNoRefusalAwaitingWakeup) || errors.Is(err, runstate.ErrConversationHeld) {
			return Corrected{}, false, nil
		}
		return Corrected{}, false, fmt.Errorf("claim the wakeup owed to %s for the tracker block refused on turn %d: %w",
			identity, conversation.RefusedBlock.Turn, err)
	}
	turn, wakeErr := c.Roles.Wake(ctx, identity, correctionMessage(claimed))
	// What the turn cost is carried whichever way it went, because the provider
	// charges for a turn that failed exactly as for one that answered.
	corrected.CostUSD = turn.CostUSD
	if conversationID := strings.TrimSpace(turn.ConversationID); conversationID != "" {
		corrected.ConversationID = conversationID
	}
	if wakeErr != nil {
		corrected.Problem = describeFailedWakeup(identity, claimed, wakeErr)
		if errors.Is(wakeErr, ErrProviderWindow) {
			c.giveBack(ctx, identity, claimed, &corrected)
		}
		return corrected, true, nil
	}
	corrected.Woken = true
	corrected.Actions = turn.Actions
	switch {
	case turn.Refused != "":
		corrected.Problem = fmt.Sprintf(
			"the %s was woken to re-issue the tracker block refused on turn %d and the block it sent back was refused too, so the actions are still lost and the operator has it: %s",
			identity, claimed.Turn, turn.Refused)
	case turn.Actions == 0:
		// A turn that answered and asked for nothing is not a failure and is not a
		// correction either. It is said out loud because the two look identical from
		// outside: a pass that woke a role and reported only that would read as a
		// refusal put right.
		corrected.Problem = fmt.Sprintf(
			"the %s was woken to re-issue the tracker block refused on turn %d and answered without asking for any tracker action, so nothing it lost has been put back",
			identity, claimed.Turn)
	}
	return corrected, true, nil
}

// giveBack returns a wakeup that reached the role with nothing, so the refusal
// keeps the turn it is owed.
//
// It is written under a context detached from the wakeup's own, for the reason
// the stopped-work delivery detaches its own records: a shutdown cancels the very
// context the wakeup ran under, and it lands between the failure and this write —
// which is the largest class of the deaths a give-back exists for. One that
// failed is said beside the wakeup rather than swallowed, because what it leaves
// behind is an attempt spent on a turn nobody was asked.
func (c Corrector) giveBack(ctx context.Context, identity runstate.ConversationIdentity, claimed runstate.TrackerRefusal, corrected *Corrected) {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if err := c.Claims.WithdrawRefusalWakeup(write, identity, claimed.Turn); err != nil {
		corrected.Problem = fmt.Sprintf("%s; and the wakeup it never used could not be given back, so it has spent one of %d on a turn nobody was asked: %v",
			corrected.Problem, runstate.MaxRefusalWakeups, err)
	}
}

// describeFailedWakeup says what became of a turn that did not answer, in the
// words each failure earns. The three are different facts about the same wakeup:
// a provider with no capacity put nothing in front of the role and will have
// capacity again, a conversation that could never be opened asked the role
// nothing and waits on somebody changing something, and a turn that started and
// failed inside is neither.
func describeFailedWakeup(identity runstate.ConversationIdentity, claimed runstate.TrackerRefusal, err error) string {
	switch {
	case errors.Is(err, ErrProviderWindow):
		return fmt.Sprintf("the provider had no capacity for the turn waking the %s to re-issue the tracker block refused on turn %d, so nothing was asked and it will be woken again once %s has passed: %v",
			identity, claimed.Turn, runstate.RefusalWakeupRetryDelay, err)
	case errors.Is(err, ErrRoleUnreachable):
		return fmt.Sprintf("the %s could not be woken to re-issue the tracker block refused on turn %d, so nothing was asked and the refusal waits on its own conversation's next turn: %v",
			identity, claimed.Turn, err)
	default:
		return fmt.Sprintf("the turn waking the %s to re-issue the tracker block refused on turn %d failed: %v",
			identity, claimed.Turn, err)
	}
}

// correctionMessage is what the harness says when it wakes a role to correct a
// refused block.
//
// It deliberately does not restate what was wrong. The refusal is already in this
// conversation's own record, in the harness's own words, and it opens the turn
// this message arrives at the end of — written there by the same machinery that
// refused the block, and written in the same save that recorded the refusal this
// wakeup was claimed against, so a wakeup exists only where those words do. A
// second copy worded here would be the harness paraphrasing its own refusal, and
// a paraphrase is exactly what a role correcting a block must not be given.
//
// What it does say is the three things the refusal cannot: that the harness woke
// this turn rather than a person, that being woken grants no authority, and that
// this is the only turn the harness will start for it.
func correctionMessage(refused runstate.TrackerRefusal) string {
	return strings.Join([]string{
		fmt.Sprintf("The harness woke you because the tracker block in your reply on turn %d was refused whole and nothing in it happened. Nobody is waiting at a terminal for this.", refused.Turn),
		"The refusal is above this message, in the harness's own words. Answer it: issue the actions you still want again, in a block that fixes what it says was wrong with the one before it.",
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and being woken to correct something widens nothing about what you may decide or change.",
		"This is the one turn the harness starts for this refusal. A block refused again goes to the operator rather than earning another.",
	}, "\n")
}

// paused reports the operator's pause over everything the harness spends. A pause
// that cannot be read refuses the pass rather than being spent through, exactly as
// it does everywhere else it is read.
func (c Corrector) paused() (runstate.OperatorHold, bool, error) {
	if c.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := c.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (c Corrector) validate() error {
	var problems []error
	if c.Conversations == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the recorded conversations the refusals are on"))
	}
	if c.Claims == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the durable claim that bounds it to one wakeup per refusal"))
	}
	if c.Roles == nil {
		problems = append(problems, errors.New("waking a role to correct a refused tracker block requires the role's conversation to wake"))
	}
	return errors.Join(problems...)
}

func (c Corrector) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what one pass did about the refusals, for whoever asked.
func (s CorrectionSweep) Render() string {
	var rendered strings.Builder
	if s.Paused != nil {
		fmt.Fprintf(&rendered, "PAUSED: no role was woken to correct a refused tracker block, since %s\n",
			s.Paused.HeldAt.UTC().Format(time.RFC3339))
	}
	for _, corrected := range s.Corrected {
		switch {
		case !corrected.Woken:
			fmt.Fprintf(&rendered, "the %s was not woken for the tracker block refused on turn %d\n", corrected.Role, corrected.Turn)
		case corrected.Actions > 0:
			fmt.Fprintf(&rendered, "the %s was woken for the tracker block refused on turn %d and re-issued %d action(s)\n",
				corrected.Role, corrected.Turn, corrected.Actions)
		default:
			fmt.Fprintf(&rendered, "the %s was woken for the tracker block refused on turn %d\n", corrected.Role, corrected.Turn)
		}
		if corrected.Problem != "" {
			fmt.Fprintf(&rendered, "  %s\n", corrected.Problem)
		}
	}
	return rendered.String()
}
