package orchestrator

// Putting a stopped run in front of the development manager, with nobody
// carrying it there.
//
// A run that fails independent review after every permitted attempt is docketed
// as it stops, and the docket is delivered into the development manager's
// conversation when she opens one. Between those two facts sat a person. The
// stoppage was durable and the docket was assembled and her authority to decide
// was wired, and none of it moved until somebody opened her conversation and
// told her a run had stopped — which is the operator standing in as the
// harness's messenger, in the one workflow that exists so they do not have to
// stand in as its eyes.
//
// So the harness delivers it. What that changes is the courier and nothing else:
// the evidence is the docket entry she would have read anyway, the decision is
// hers and is recorded where every triage decision is recorded, the caps still
// refuse what they refused, and nothing here carries a decision out. The verbs
// that act on one — `yoyo triage repair`, `yoyo triage rerun` — are unchanged
// and still read the intake hold and prove the stoppage is over before they
// spend anything.
//
// # What is delivered, and what is not
//
// Only the 2-of-2 review stoppage: a run that ended on a durable blocker with
// its reviewer still requiring repair. It is the stoppage this was asked for and
// the one whose next step is a judgment rather than a fix — a failing check, a
// refused path, and a replay conflict are all stoppages too, and each is a
// different question. They stay on the docket for her to read, exactly as
// before, rather than being delivered by a rule nobody has argued for yet.
//
// Which stoppage a docket entry describes is read from the run's own record
// rather than from the entry's prose, for the reason every other triage action
// reads it there: the entry says what was true when it was written, and a
// classification made by matching words is one that changes when somebody
// rewrites a sentence.
//
// # One at a time
//
// A pass delivers one stoppage. A delivery is a conversation turn, and a pass
// that delivered a backlog of them at once would hold the queue closed for as
// long as the development manager took to answer all of them — so the oldest
// goes first, and the next pass takes the next. What that costs is that a
// morning with five stopped runs reaches her over five passes, which is the
// right trade for a loop that goes on choosing work while she reads.
//
// # Why the spending pause and not the intake hold
//
// A delivery is a provider invocation, so the operator's pause covers it exactly
// as it covers a run and a conversation turn: a paused harness delivers nothing,
// and nothing is claimed, so the stoppage keeps its delivery for after the pause
// is lifted.
//
// The intake hold is deliberately not read here, and the difference is what each
// switch is for. Holding intake stops the harness choosing work; this chooses
// nothing, claims nothing, and starts nothing. What it produces is the
// development manager's judgment about work that has already stopped — which is
// the thing a held queue is usually waiting on, and holding it back would be the
// hold answering a question nobody asked it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// EscalationDocket is the docket the stoppages are read from. It is read and
// never written: an entry stands as the record that work stopped however many
// times it is delivered, and delivering it settles nothing about it.
type EscalationDocket interface {
	List() ([]triage.Entry, error)
}

// EscalationRuns is the durable run state one delivery reads. It reads and
// never writes: what a stopped run's record says is evidence about the
// stoppage, and nothing here acts on the run.
type EscalationRuns interface {
	Load(runID string) (runstate.State, error)
}

// EscalationRecords is where the delivery of one docketed stoppage is claimed
// and settled. It is what makes the delivery at most once: a stoppage put in
// front of the development manager twice is the same evidence in front of her
// twice, which is how one authorized recovery becomes two decisions.
//
// It is satisfied by runstate.EscalationStore.
type EscalationRecords interface {
	Attempt(ctx context.Context, escalation runstate.Escalation) (runstate.Escalation, error)
	Settle(ctx context.Context, docketKey string, delivery runstate.Delivery) (runstate.Escalation, error)
	Withdraw(ctx context.Context, docketKey string) error
	Find(docketKey string) (runstate.Escalation, bool, error)
}

// EscalationReruns is what the harness has already carried out against one
// docketed stoppage. It is read to leave alone a stoppage somebody has already
// acted on: a re-run claimed against this entry is a decision made and carried
// out, and asking her to judge it again would be asking about a stoppage whose
// one re-run the guards would now refuse.
//
// The other carry-out needs nothing here. A repair continues the stopped run
// itself and clears its blocker as it does, so a repaired stoppage stops being
// one this delivers by the same rule that decides every other run.
//
// It is optional: an escalator wired without one delivers a stoppage that was
// re-run as though nobody had looked at it, which is a turn spent and no harm
// past that.
type EscalationReruns interface {
	Find(docketKey string) (runstate.Rerun, bool, error)
}

// DevelopmentManager is her conversation as the harness reaches it: one stoppage
// delivered into it, and what she recorded about it in answer.
//
// Nothing here decides anything on her behalf and nothing carries her decision
// out. What comes back is read from what she actually recorded, so a delivery
// she answered in prose and decided nothing about reports exactly that.
type DevelopmentManager interface {
	Judge(ctx context.Context, entry triage.Entry) (Judgment, error)
}

// Judgment is what came back from one delivery: her conversation, and the triage
// decision she recorded about the stoppage, where she recorded one.
type Judgment struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// Decision is the triage decision she recorded about this stoppage, in the
	// vocabulary the conversation records decisions in, and Reason the reasoning
	// she gave for it. Both are empty where she decided nothing, which is an
	// answer rather than a failure.
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ErrConversationUnreachable reports a delivery that failed before the
// development manager was asked anything: her conversation could not be opened
// at all. It is its own error because it is the one failure whose attempt is
// provably worth giving back — nothing was said to her, and nothing was spent.
var ErrConversationUnreachable = errors.New("the development manager's conversation could not be opened")

// Escalator delivers docketed stoppages to the development manager. It has no
// tracker, no worktree access, and no forge access, and it starts nothing: what
// it does is put evidence somebody already recorded in front of the role whose
// judgment it is, and write down that it did.
type Escalator struct {
	Docket EscalationDocket
	Runs   EscalationRuns
	// Records is what makes a delivery at most once. Required: a delivery nothing
	// records is one every pass makes again.
	Records EscalationRecords
	// Reruns is what has already been carried out against a stoppage. Optional;
	// see EscalationReruns.
	Reruns  EscalationReruns
	Manager DevelopmentManager
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and an escalator wired without one is one nothing can pause,
	// which is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// Escalated is one stoppage this pass put in front of the development manager,
// and what came back. It reports a delivery that did not happen as carefully as
// one that did: a stoppage nobody heard about is the state this exists to end,
// so it is never reported as a pass that did nothing.
type Escalated struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Delivered  bool   `json:"delivered"`
	// Decision is what she recorded about the stoppage, and is empty where she
	// recorded nothing.
	Decision string `json:"decision,omitempty"`
	// Problem is what stopped a delivery, and says whether the stoppage will be
	// tried again.
	Problem string `json:"problem,omitempty"`
}

// EscalationSweep is what one pass did. A pass that found nothing to deliver
// reports nothing, which is every pass on a harness whose work is landing.
type EscalationSweep struct {
	Escalated []Escalated `json:"escalated,omitempty"`
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and the stoppage keeps its delivery.
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
}

// Escalate delivers the oldest stoppage the development manager has not been
// shown, and reports what came of it.
//
// The order is the order the guarantees need. The pause is read before anything
// is claimed, so a paused harness costs the stoppage nothing; the attempt is
// recorded before the turn is taken, so a process that dies between the two has
// recorded a delivery nobody made rather than made one nobody recorded; and what
// came back is written down after, because until she has answered there is
// nothing to write.
//
// A run whose record cannot be read is skipped rather than failing the pass: a
// sweep that refuses to run because one run is odd is a sweep that delivers
// nothing, and the stoppages beside it are exactly the ones somebody needs.
func (e Escalator) Escalate(ctx context.Context) (EscalationSweep, error) {
	if err := e.validate(); err != nil {
		return EscalationSweep{}, err
	}
	hold, held, err := e.paused()
	if err != nil {
		return EscalationSweep{}, err
	}
	if held {
		return EscalationSweep{Paused: &hold}, nil
	}
	entries, err := e.Docket.List()
	if err != nil {
		return EscalationSweep{}, fmt.Errorf("read the triage docket: %w", err)
	}
	var problems []error
	for _, entry := range entries {
		deliver, err := e.awaitingJudgment(entry)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !deliver {
			continue
		}
		escalated, attempted, err := e.deliver(ctx, entry)
		if err != nil {
			problems = append(problems, err)
			// A stoppage the store would not record is not one to deliver: the
			// delivery this pass would make is the one the next pass would make
			// again, and a turn spent on a record nothing bounds is exactly what the
			// record exists to prevent. The next entry is left for the next pass,
			// which reads the same store and meets the same failure or does not.
			break
		}
		if !attempted {
			// Another process delivered this stoppage between the reading and the
			// claim, which is the record doing its job. The next entry is this pass's
			// to look at.
			continue
		}
		return EscalationSweep{Escalated: []Escalated{escalated}}, errors.Join(problems...)
	}
	return EscalationSweep{}, errors.Join(problems...)
}

// awaitingJudgment reports one docket entry being a stoppage the development
// manager has not been shown and the harness will still deliver.
//
// It asks the run's own record rather than the entry, for the reason every
// triage action does: the entry says what was true when it was written, and what
// decides whether this is the 2-of-2 stoppage is what the run recorded about how
// it ended.
func (e Escalator) awaitingJudgment(entry triage.Entry) (bool, error) {
	if entry.Class != triage.ClassStoppedRun {
		return false, nil
	}
	recorded, found, err := e.Records.Find(entry.Key)
	if err != nil {
		return false, fmt.Errorf("read whether the stoppage of run %s has been put to the development manager: %w", entry.RunID, err)
	}
	if found && recorded.Spent() {
		return false, nil
	}
	state, err := e.Runs.Load(entry.RunID)
	if err != nil {
		return false, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	if !reviewRepairStoppage(state) {
		return false, nil
	}
	return e.notActedOn(entry)
}

// notActedOn reports a stoppage nobody has carried anything out against. A
// stoppage that was re-run has had a decision made and acted on, and the one
// re-run it gets is spent, so putting it in front of her would be asking about
// work whose recovery has already happened.
func (e Escalator) notActedOn(entry triage.Entry) (bool, error) {
	if e.Reruns == nil {
		return true, nil
	}
	_, found, err := e.Reruns.Find(entry.Key)
	if err != nil {
		return false, fmt.Errorf("read whether the stoppage of run %s has already been run again: %w", entry.RunID, err)
	}
	return !found, nil
}

// reviewRepairStoppage reports the stoppage this delivers: a run that ended on a
// durable blocker with its independent reviewer still requiring repair after
// every permitted attempt.
//
// The last two conditions are what keep it to that stoppage. A run handed back a
// failing check or a refused path never reached a reviewer on the change it
// stopped with, and its record says so — the check failure and the path refusal
// each clear the review evidence beside them as they are recorded, so a run
// carrying either stopped on something other than a reviewer's judgment however
// many reviews it had before.
func reviewRepairStoppage(state runstate.State) bool {
	return stoppedRun(state) &&
		state.ReviewDecision == runstate.ReviewRepair &&
		state.CheckFailure == nil &&
		state.PathRefusal == nil
}

// deliver puts one stoppage in front of the development manager and records what
// came back.
//
// A conversation that could not be opened gives the attempt back: nothing was
// asked of her, so the stoppage keeps the delivery it is owed and the next pass
// makes it. Every other failure keeps the attempt, because a turn that may have
// been taken is one this cannot claim was not — and the record is bounded, so a
// delivery that goes on failing stops rather than spending every pass on it.
// It reports whether an attempt was made at all, so a stoppage another process
// claimed between the reading and the claim leaves this pass looking at the next
// one rather than reporting a delivery nobody made.
func (e Escalator) deliver(ctx context.Context, entry triage.Entry) (Escalated, bool, error) {
	escalated := Escalated{WorkItemID: entry.WorkItemID, RunID: entry.RunID, DocketKey: entry.Key}
	attempted, err := e.Records.Attempt(ctx, runstate.Escalation{
		DocketKey:        entry.Key,
		RunID:            entry.RunID,
		WorkItemID:       entry.WorkItemID,
		FirstAttemptedAt: e.now(),
	})
	if err != nil {
		// A stoppage another process delivered between the reading above and this
		// claim is not a failure of either: one of them delivered it, which is what
		// the record is for.
		if errors.Is(err, runstate.ErrEscalationSpent) {
			return Escalated{}, false, nil
		}
		return Escalated{}, false, fmt.Errorf("record that the stoppage of run %s is being put to the development manager: %w", entry.RunID, err)
	}
	judgment, judgeErr := e.Manager.Judge(ctx, entry)
	if errors.Is(judgeErr, ErrConversationUnreachable) {
		escalated.Problem = fmt.Sprintf("the stoppage of run %s was not put to the development manager and will be at the next pass: %v", entry.RunID, judgeErr)
		if err := e.Records.Withdraw(ctx, entry.Key); err != nil {
			escalated.Problem = fmt.Sprintf("the stoppage of run %s was not put to the development manager, and the attempt taken for it could not be given back, so it has spent one of %d on a turn nobody took: %v",
				entry.RunID, runstate.MaxEscalationAttempts, err)
		}
		return escalated, true, nil
	}
	delivery := runstate.Delivery{At: e.now(), ConversationID: judgment.ConversationID, Decision: judgment.Decision, Reason: judgment.Reason}
	if judgeErr != nil {
		// The turn may have been taken and may have cost money, and nothing here
		// can tell. So the attempt stands and the delivery does not: the record
		// says the development manager may not have been asked, which is the state
		// somebody has to be able to find, and the bounded retry is what stops that
		// honesty from becoming a loop.
		delivery = runstate.Delivery{Problem: judgeErr.Error()}
		escalated.Problem = fmt.Sprintf("putting the stoppage of run %s to the development manager failed on attempt %d of %d: %v",
			entry.RunID, attempted.Attempts, runstate.MaxEscalationAttempts, judgeErr)
	} else {
		escalated.Delivered = true
		escalated.Decision = judgment.Decision
	}
	if _, err := e.Records.Settle(ctx, entry.Key, delivery); err != nil {
		escalated.Problem = strings.TrimSpace(escalated.Problem + fmt.Sprintf(
			"; what became of putting the stoppage of run %s to the development manager could not be recorded, so the record still says it may not have reached her: %v",
			entry.RunID, err))
	}
	return escalated, true, nil
}

// paused reports the operator's pause over everything the harness spends. A
// pause that cannot be read refuses the pass rather than being spent through,
// exactly as it does everywhere else it is read.
func (e Escalator) paused() (runstate.OperatorHold, bool, error) {
	if e.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := e.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (e Escalator) validate() error {
	var problems []error
	if e.Docket == nil {
		problems = append(problems, errors.New("escalating requires the triage docket the stoppages are on"))
	}
	if e.Runs == nil {
		problems = append(problems, errors.New("escalating requires the durable run state, because which stoppage an entry describes is read from the run's own record"))
	}
	if e.Records == nil {
		problems = append(problems, errors.New("escalating requires the record that bounds it to one delivery per docketed stoppage"))
	}
	if e.Manager == nil {
		problems = append(problems, errors.New("escalating requires the development manager's conversation to deliver into"))
	}
	return errors.Join(problems...)
}

func (e Escalator) now() time.Time {
	if e.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return e.Clock.Now().UTC()
}

// Render describes what one pass did, for whoever asked for it.
func (s EscalationSweep) Render() string {
	var rendered strings.Builder
	if s.Paused != nil {
		fmt.Fprintf(&rendered, "PAUSED: no stopped work was put to the development manager, since %s\n",
			s.Paused.HeldAt.UTC().Format(time.RFC3339))
	}
	for _, escalated := range s.Escalated {
		if escalated.Delivered {
			if decision := strings.TrimSpace(escalated.Decision); decision != "" {
				fmt.Fprintf(&rendered, "escalated the stoppage of run %s to the development manager, who triaged %s as %q\n",
					escalated.RunID, escalated.WorkItemID, decision)
			} else {
				fmt.Fprintf(&rendered, "escalated the stoppage of run %s to the development manager, who recorded no decision about %s\n",
					escalated.RunID, escalated.WorkItemID)
			}
		}
		if escalated.Problem != "" {
			fmt.Fprintln(&rendered, escalated.Problem)
		}
	}
	return rendered.String()
}
