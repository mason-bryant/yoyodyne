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
// # One at a time, and only what nobody has judged
//
// A pass delivers one stoppage. A delivery is a conversation turn, and a pass
// that delivered a backlog of them at once would hold the queue closed for as
// long as the development manager took to answer all of them — so the oldest
// goes first, and the next pass takes the next. What that costs is that a
// morning with five stopped runs reaches her over five passes, which is the
// right trade for a loop that goes on choosing work while she reads.
//
// A delivery that failed is made again, up to the record's bound and no faster
// than its retry delay. The pacing is the half that is easy to leave out and
// makes the bound meaningless without it: whatever drives this decides how often
// it looks, and the loop that does today looks once per pull, so three attempts
// counted and not paced would be three attempts inside one command.
//
// And a stoppage she has already been given a repair grant or a re-run for is
// left alone, whether or not the harness has carried that decision out. Both are
// recorded against the item's budget the moment she decides, and acted on later,
// so between the two the stopped run still reads as untouched to anything looking
// only at the run — which is where every stoppage somebody carried to her by hand
// also sits. What that cannot see is a decision costing nothing, which leaves no
// counter anywhere the harness reads; see alreadyJudged for what follows from
// that.
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
	Withdraw(ctx context.Context, docketKey, problem string) error
	Find(docketKey string) (runstate.Escalation, bool, error)
}

// EscalationDecisions is the durable per-item record of what triage has already
// decided, which is the record the triage guards spend and refuse against. It is
// read to leave alone a stoppage the development manager has already given a
// repair grant or a re-run for — the two decisions that leave a mark the harness
// can read.
//
// Deciding and carrying out are two acts with a gap between them, and that gap
// is where this matters. A repair grant is recorded the moment she decides, and
// the stopped run's blocker is not cleared until `yoyo triage repair` acts on
// it, so a stoppage she settled an hour ago still looks exactly like one nobody
// has seen — to anything that reads the run alone. Delivering it again is the
// same evidence in front of her twice under two decisions, which is the harm
// this whole record exists to prevent, and it is the state every stoppage
// somebody carries to her by hand passes through.
//
// It is satisfied by runstate.TriageStore.
type EscalationDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// EscalationReruns is what the harness has already carried out of those
// decisions: the re-runs claimed for one work item. It is the other half of the
// same question — a decision recorded and a decision acted on are both reasons
// to leave a stoppage alone, and only the two records together tell either from
// a stoppage nobody has looked at.
//
// The repair carry-out needs neither: it continues the stopped run itself and
// clears its blocker as it does, so a repaired stoppage stops being one this
// delivers by the same rule that decides every other run.
//
// It is satisfied by runstate.RerunStore.
type EscalationReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
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

// Judgment is what came back from one delivery: her conversation, the triage
// decision she recorded about the stoppage where she recorded one, and what the
// turn cost.
type Judgment struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// CostUSD is what the provider charged for the turn, as it reported it. It is
	// carried back because the delivery is a spend the caller made rather than one
	// a run made, so a session counting what it has spent has no other way to see
	// it. A turn that failed carries what it cost too: the provider charged for it
	// exactly as it charges for one that answered.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Decision is the triage decision she recorded about this stoppage, in the
	// vocabulary the conversation records decisions in, and Reason the reasoning
	// she gave for it. Both are empty where she decided nothing, which is an
	// answer rather than a failure.
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ErrConversationUnreachable reports a delivery that failed before the
// development manager was asked anything: her conversation could not be opened
// at all. It is its own error because it is a failure whose attempt is provably
// worth giving back — nothing was said to her, and nothing was spent.
var ErrConversationUnreachable = errors.New("the development manager's conversation could not be opened")

// ErrDeliveryCancelled reports a delivery whose turn a cancellation killed
// before any answer of hers existed — a signal, a shutdown, a process-group
// teardown taken out from under the pull. It is separate from an unreachable
// conversation because it is a different fact about the same delivery and both
// are recorded in words a person reads: her conversation opened perfectly well,
// and what stopped the delivery was the harness's own death.
//
// It is the second failure whose attempt is worth giving back, and it is worth
// giving back for the reason stated on the Withdraw contract it is spent
// against: the turn ended before a reply existed, so nothing of hers was parsed,
// no action was applied, and the item's durable triage record never moved. A
// second delivery of it therefore cannot become a second decision, which is the
// harm the attempt record exists to prevent.
//
// What it does not claim is that she never saw the message. The provider had the
// prompt from the moment the invocation started. The claim is the narrower one
// the give-back actually rests on: this turn decided nothing and carried nothing
// out.
var ErrDeliveryCancelled = errors.New("the delivery was cancelled before the development manager answered")

// notTaken reports a delivery failure whose attempt is given back rather than
// spent. Both members are failures that produced no answer of hers, and every
// other ending is left as an attempt she may have been asked.
func notTaken(err error) bool {
	return errors.Is(err, ErrConversationUnreachable) || errors.Is(err, ErrDeliveryCancelled)
}

// undelivered says what became of a delivery whose attempt is being given back,
// in the words each of the two failures actually earns.
//
// The two are not the same sentence, and writing them as one would put the claim
// ErrDeliveryCancelled explicitly refuses to make into the record a person reads:
// a cancelled turn is not a stoppage that was never put to her — the provider
// had the prompt from the moment the invocation started — it is a turn that
// ended before she answered.
func undelivered(runID string, judgeErr error) string {
	if errors.Is(judgeErr, ErrDeliveryCancelled) {
		return fmt.Sprintf("the turn putting the stoppage of run %s to the development manager ended before she answered", runID)
	}
	return fmt.Sprintf("the stoppage of run %s was not put to the development manager", runID)
}

// undeliveredAndWaiting is that sentence with what happens next on it, which is
// the same promise either way: the attempt came back, and the stoppage is put to
// her once the delay the record keeps has passed.
func undeliveredAndWaiting(runID string, judgeErr error, delay time.Duration) string {
	if errors.Is(judgeErr, ErrDeliveryCancelled) {
		return fmt.Sprintf("%s, and it will be put to her again once %s has passed", undelivered(runID, judgeErr), delay)
	}
	return fmt.Sprintf("%s and will be once %s has passed", undelivered(runID, judgeErr), delay)
}

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
	// Decisions and Reruns are what triage has already decided about the item and
	// what has been carried out of it. Both required: a delivery made without
	// reading them is one made to a development manager who settled this stoppage
	// an hour ago, which is the failure this is here to avoid rather than one to
	// degrade gracefully into.
	Decisions EscalationDecisions
	Reruns    EscalationReruns
	Manager   DevelopmentManager
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
	// CostUSD is what the turn cost, as the provider reported it. It is on the
	// sweep so that whatever drives the delivery can count it: a delivery is a
	// spend made by the caller rather than by a run, and a session bounded by a
	// budget must not spend past it on turns nothing counted.
	CostUSD float64 `json:"cost_usd,omitempty"`
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
	var sweep EscalationSweep
	var problems []error
	// delivering is this pass still having its one delivery to make. It goes false
	// as soon as one is made, and on a store that would not record one: the
	// delivery this pass would make next is the one the next pass makes, and a
	// turn spent on a record nothing bounds is what the record exists to prevent.
	// The scan carries on either way, because the stoppages behind it may need
	// saying even where none of them is being delivered.
	delivering := true
	for _, entry := range entries {
		standing, recorded, err := e.standingOf(entry)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		switch standing {
		case standingAbandoned:
			// Said on every pass, for as long as it is true. The harness has stopped
			// trying and nothing else will notice: a stoppage that reached nobody and
			// is reported nowhere is precisely the silence this exists to end.
			sweep.Escalated = append(sweep.Escalated, abandoned(entry, recorded))
		case standingAwaiting:
			if !delivering {
				continue
			}
			escalated, attempted, err := e.deliver(ctx, entry)
			if err != nil {
				problems = append(problems, err)
				delivering = false
				continue
			}
			if !attempted {
				// Another process delivered this stoppage between the reading and the
				// claim, which is the record doing its job. The next entry is this
				// pass's to look at.
				continue
			}
			sweep.Escalated = append(sweep.Escalated, escalated)
			delivering = false
		case standingSettled, standingCooling:
			// Nothing to do and nothing to say. A settled stoppage is somebody
			// else's business now, and a cooling one was reported by the pass that
			// attempted it — saying it again on every pull between here and its next
			// attempt would bury what the pass actually did.
		}
	}
	return sweep, errors.Join(problems...)
}

// What one docket entry is to this pass.
type escalationStanding int

const (
	// standingSettled is a stoppage this pass has nothing to do or say about:
	// not the stoppage this delivers, one she has already been shown, or one she
	// has already judged.
	standingSettled escalationStanding = iota
	// standingAwaiting is a stoppage nobody has put to her and the harness still
	// will.
	standingAwaiting
	// standingCooling is one whose last attempt failed too recently to be worth
	// repeating yet. The pass that made that attempt is what said so; repeating
	// the sentence every pull would bury it.
	standingCooling
	// standingAbandoned is one the harness tried and stopped trying. It is said
	// out loud rather than skipped, because what it needs now is a person and
	// nothing else in the harness is going to mention it.
	standingAbandoned
)

// abandoned is what a pass says about a stoppage the harness has given up
// delivering. The words are the store's own refusal, so what the pass says and
// what a second attempt would be refused with are one sentence rather than two.
func abandoned(entry triage.Entry, recorded runstate.Escalation) Escalated {
	return Escalated{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Problem:    runstate.EscalationSpentError{Existing: recorded}.Error(),
	}
}

// standingOf reports what one docket entry is to this pass, and the escalation
// record behind that answer.
//
// It asks the run's own record rather than the entry, for the reason every
// triage action does: the entry says what was true when it was written, and what
// decides whether this is the 2-of-2 stoppage is what the run recorded about how
// it ended.
func (e Escalator) standingOf(entry triage.Entry) (escalationStanding, runstate.Escalation, error) {
	if entry.Class != triage.ClassStoppedRun {
		return standingSettled, runstate.Escalation{}, nil
	}
	recorded, found, err := e.Records.Find(entry.Key)
	if err != nil {
		return standingSettled, runstate.Escalation{}, fmt.Errorf("read whether the stoppage of run %s has been put to the development manager: %w", entry.RunID, err)
	}
	if found && recorded.Delivered() {
		return standingSettled, recorded, nil
	}
	state, err := e.Runs.Load(entry.RunID)
	if err != nil {
		return standingSettled, recorded, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	if !reviewRepairStoppage(state) {
		return standingSettled, recorded, nil
	}
	// Asked before the record's own state, so a stoppage she has since judged
	// stops being this pass's business whether the harness delivered it, gave up
	// delivering it, or never reached it at all.
	judged, err := e.alreadyJudged(entry)
	if err != nil {
		return standingSettled, recorded, err
	}
	if judged {
		return standingSettled, recorded, nil
	}
	switch {
	case found && recorded.Spent():
		return standingAbandoned, recorded, nil
	case found && recorded.Cooling(e.now()):
		return standingCooling, recorded, nil
	default:
		return standingAwaiting, recorded, nil
	}
}

// alreadyJudged reports a stoppage the development manager has settled, whether
// or not the harness has carried her decision out.
//
// Three states say she has, and the item's own durable triage record is where
// all three live. A repair grant recorded and not yet spent is a decision
// standing: she gave the item another round, and until `yoyo triage repair`
// takes it the stopped run still carries the blocker that makes it look
// untouched. A re-run recorded and not yet claimed is the same fact for the
// other carry-out. And a re-run already claimed against this entry is her
// decision acted on, which the run's record cannot say either, because a re-run
// starts a fresh run and leaves the stopped one exactly as it was.
//
// What it deliberately cannot see is a decision that spends nothing — an
// escalation to the operator, a re-scope, a wait. Those leave no counter, so a
// stoppage she settled that way is delivered again if the harness ever reaches
// it. The docket entry she is shown says what has been decided about the item,
// so the second delivery costs a turn and tells her nothing she cannot see; what
// it must never do is spend a budget, and nothing here can.
func (e Escalator) alreadyJudged(entry triage.Entry) (bool, error) {
	counters, err := e.Decisions.Counters(entry.WorkItemID)
	if err != nil {
		return false, fmt.Errorf("read what triage has already decided about %s: %w", entry.WorkItemID, err)
	}
	if counters.CommittedRounds > counters.ReviewRounds {
		return true, nil
	}
	claimed, err := e.Reruns.Claimed(entry.WorkItemID)
	if err != nil {
		return false, fmt.Errorf("read the re-runs already taken of %s: %w", entry.WorkItemID, err)
	}
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return true, nil
		}
	}
	return counters.Reruns > len(claimed), nil
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
// A delivery that produced no answer of hers gives the attempt back — her
// conversation could not be opened, or a cancellation killed the turn before a
// reply existed — so the stoppage keeps the delivery it is owed and the next
// pass makes it. Every other failure keeps the attempt, because a turn that
// answered is one this cannot claim did not — and the record is bounded, so a
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
		// A stoppage another process claimed between the reading above and this one
		// is not a failure of either: the record refused the second, which is what
		// it is for. Both refusals mean the same thing here — one of them delivered
		// it, or one of them is about to — and neither is this pass's to report.
		if errors.Is(err, runstate.ErrEscalationSpent) || errors.Is(err, runstate.ErrEscalationCooling) {
			return Escalated{}, false, nil
		}
		return Escalated{}, false, fmt.Errorf("record that the stoppage of run %s is being put to the development manager: %w", entry.RunID, err)
	}
	judgment, judgeErr := e.Manager.Judge(ctx, entry)
	// What the turn cost is carried whichever way it went, because the provider
	// charges for a turn that failed exactly as for one that answered.
	escalated.CostUSD = judgment.CostUSD
	if notTaken(judgeErr) {
		escalated.Problem = fmt.Sprintf("%s: %v",
			undeliveredAndWaiting(entry.RunID, judgeErr, runstate.EscalationRetryDelay), judgeErr)
		if err := e.Records.Withdraw(ctx, entry.Key, judgeErr.Error()); err != nil {
			escalated.Problem = fmt.Sprintf("%s, and the attempt taken for it could not be given back, so it has spent one of %d on a turn that decided nothing: %v",
				undelivered(entry.RunID, judgeErr), runstate.MaxEscalationAttempts, err)
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
	if e.Decisions == nil {
		problems = append(problems, errors.New("escalating requires the item's durable triage record, because a stoppage the development manager has already decided must not be put to her again"))
	}
	if e.Reruns == nil {
		problems = append(problems, errors.New("escalating requires the re-runs already carried out, which is the other half of what says a stoppage has been judged"))
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
