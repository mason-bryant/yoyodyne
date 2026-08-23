package slack

// The push half of the heartbeat: the states under which the whole system is
// waiting on a person, sent to the people it is waiting on.
//
// The heartbeat next door reports the same class of state into the channel, and
// that worked — an operator read the line and asked about it. But a channel line
// is something you find when you look, and the one condition under which nobody
// is going to look is the condition where nothing is happening at all. So these
// three states go the other way: they find the operator rather than waiting to be
// found.
//
// The noise discipline is what keeps that worth having, and it is a rule about
// what does *not* reach a person directly. A single item that is blocked stays a
// channel warning with its status mark on the thread, because it has an owner —
// the development manager's docket — and something that interrupted every
// operator for each of those would teach them to stop reading the ones that
// matter. What is reserved for this tier is the system waiting on a human: the
// brake tripped, so nothing new is chosen until somebody releases it; capacity
// gone for longer than it is worth waiting out; and a directive nobody but the
// operator can settle, which stops the work it names wherever it stands.
//
// Each one is the governed escalation shape — the blocked outcome, why it is the
// operator's, the options, a recommendation — rather than a second format
// invented for a chat client. What is different is only that it is delivered
// instead of filed.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// DefaultCapacityWait is how long a system blocked on provider capacity is
	// left to come back on its own before somebody is told about it. Below it,
	// waiting is the plan and there is nothing to decide: the runs are parked, they
	// keep their claims and their worktrees, and they resume from their own records
	// when the provider serves again. Past it, waiting has stopped being a plan and
	// become a thing that is happening to the product, and whether to keep doing it
	// is a call nothing here can make.
	DefaultCapacityWait = 2 * time.Hour
	// DefaultFollowUp is how often a state that is still stopped is said again to
	// the people it is stopped on. It is the heartbeat's own cadence, deliberately:
	// the two are one behaviour said two ways, and a follow-up that arrived on a
	// different rhythm from the channel line would read as a second thing going
	// wrong.
	DefaultFollowUp = DefaultHeartbeat
)

// The prefixes a stopped state is marked under. The moment is in the mark, so a
// second hold a week later is a second thing to tell somebody about rather than
// the same one; a directive is marked by its own identifier, which is already the
// thing that makes it one directive rather than another.
const (
	capacityMark  = "capacity:"
	directiveMark = "directive:"
)

// maxStoppageDetailBytes bounds the part of an escalation that came from
// somebody's own words — the reason a hold carries, what a directive left
// unsettled. It is the heartbeat's bound for the heartbeat's reason: this is the
// first thing an operator reads on a phone, and it has to stay a line.
const maxStoppageDetailBytes = 200

// Escalations is the directive record as the escalating half reads it: what the
// harness has been told and nobody has settled. It is a reader and deliberately
// nothing else — recording is the inbound half's and revising is the operator's,
// and a reporting process that could do either would be a second authority over
// the record rather than a view of it.
type Escalations interface {
	List() ([]directive.Directive, error)
}

// stoppage is one state that has stopped the whole system, as the sink derived
// it: the escalation to send, and the durable name of the state it came from.
type stoppage struct {
	notify.Escalation
	// mark names this state, so a cursor can say which one is standing and a
	// different one re-arms rather than inheriting the last one's clock.
	mark string
}

// escalationDeliveries pushes the state the system is stopped in to the people it
// is stopped on, and keeps pushing it while it stands.
//
// It differs from the heartbeat in exactly one way, and the difference is the
// whole point of it. A state the heartbeat has just started seeing is armed
// silently, because whatever made it said so in the channel as it happened; this
// says the first one immediately, because nothing else is going to reach the
// operator at all. What the two share is the other half: the state is said again
// while it stands, and the moment it clears the cursor forgets it — so releasing
// the brake cancels its own follow-ups without anything having to cancel them.
func (f *HarnessFeed) escalationDeliveries(cursor Cursor, held switches, states []runstate.State, streams map[string]struct{}) ([]Delivery, error) {
	streams[escalationStream] = struct{}{}

	stopped, found, err := f.whatStopped(held, states)
	if err != nil {
		return nil, err
	}
	if !found {
		// Whatever cleared it is what says so — the release, the provider serving
		// again, the directive being settled — and the cursor forgets the state so
		// the next one to stand is sent afresh rather than inheriting an interval
		// that has already run out.
		if cursor.Standing != "" {
			return []Delivery{{Stream: escalationStream, Cursor: Cursor{}}}, nil
		}
		return nil, nil
	}
	now := f.now()
	if cursor.Standing == stopped.mark && now.Sub(cursor.Said) < f.followUp() {
		return nil, nil
	}
	return []Delivery{{
		Stream:       escalationStream,
		Cursor:       Cursor{Standing: stopped.mark, Said: now},
		Direct:       true,
		Telling:      telling(stopped, now, f.followUp()),
		Notification: notify.FromEscalation(stopped.Escalation, now),
		InThread:     notify.EscalationOptions(stopped.Escalation, now),
	}}, nil
}

// telling names this saying of one stopped state, and it is deliberately derived
// from the state's own age rather than from the moment the pass happened to run.
//
// A pass that reached one operator and was refused by the workspace on the next
// is retried, and the retry has to be able to tell that the first one was already
// interrupted. A name taken from the clock would be a new name every retry and
// would wake them again; a name that was only the state would never let the hour
// after it wake anybody. The age in whole intervals is both: the same name for as
// long as one saying stands, and a different one for the next.
func telling(stopped stoppage, now time.Time, every time.Duration) string {
	round := 0
	if !stopped.Since.IsZero() && every > 0 && now.After(stopped.Since) {
		round = int(now.Sub(stopped.Since) / every)
	}
	return stopped.mark + "#" + strconv.Itoa(round)
}

// whatStopped derives what the system is stopped on, in the order an operator
// would act on it: the switch that stops the choosing, then the capacity that
// stops the spending, then the directive that stops one piece of work.
//
// One at a time, deliberately. Two of these standing together is one operator
// being interrupted twice about a machine that has stopped once, and the order
// above is the order in which clearing one might clear the next: a released brake
// is what lets the line find out whether it also has capacity.
func (f *HarnessFeed) whatStopped(held switches, states []runstate.State) (stoppage, bool, error) {
	if braked, found := brakeStoppage(held); found {
		return braked, true, nil
	}
	if blocked, found := capacityStoppage(states, f.now(), f.capacityWait()); found {
		return blocked, true, nil
	}
	return f.directiveStoppage()
}

// brakeStoppage is the brake tripped: nothing new is chosen, and nothing chooses
// again until a human releases it. Both switches are it — the operator's own hold
// over everything, and a held intake, whoever or whatever held it — because what
// makes this the tier it is is not who placed the brake but that no role can
// release one.
func brakeStoppage(held switches) (stoppage, bool) {
	if held.operatorHeld {
		return stoppage{
			mark: holdMark + stamp(held.operator.HeldAt),
			Escalation: notify.Escalation{
				Stopped: "all harness activity is held: nothing is running, nothing new is being chosen, and nothing is being spent",
				Why:     "a hold is lifted by a person and by nothing else. No role may lift it, no run resumes past it, and the harness will not decide on its own that it has been long enough.",
				Since:   held.operator.HeldAt,
				Record:  "`yoyo status`, which says what is parked behind the hold",
				Options: []string{
					"lift it — the work that was in flight carries on from its own record (`yoyo resume`)",
					"leave it in place, and say what has to be true before it lifts; that is recorded as direction every role reads",
					"something else, or you want more of the record in front of you first",
				},
				// Nothing records why a hold over everything was placed, so nothing here
				// can argue for keeping one. Saying that plainly is better than a
				// recommendation dressed up out of nothing.
				Recommendation: "(a), on the evidence there is: the record does not say what the hold was placed for, so nothing here can make the case for keeping it. If you know what it was, (b) is the option that writes it down.",
				Topic:          notify.Product(),
			},
		}, true
	}
	if !held.intakeHeld {
		return stoppage{}, false
	}
	stopped := "intake is held, so nothing new is being chosen however much is ready"
	reason := singleLine(held.intake.Reason, maxStoppageDetailBytes)
	if reason != "" {
		stopped += " — " + reason
	}
	// The recommendation follows the record rather than a guess about who held it.
	// A hold that says what it was for is one somebody had a reason for, and
	// releasing it without reading that reason is how a failure storm starts again;
	// a hold that says nothing has left nothing to argue for keeping.
	recommendation := "(a), on the evidence there is: nothing in the record says what intake was held for, so there is nothing here to weigh against releasing it."
	if reason != "" {
		recommendation = "(b) until you have looked at what it was held for — " + reason + " — because whatever caught this is still there if intake is released without it being dealt with."
	}
	return stoppage{
		mark: intakeMark + stamp(held.intake.HeldAt),
		Escalation: notify.Escalation{
			Stopped: stopped,
			Why:     "intake is released by a person. The harness will not start choosing again on its own, and no role may release it — which is exactly what the brake is for.",
			Since:   held.intake.HeldAt,
			Record:  "`yoyo status`, which says what is ready behind the hold and what is still in flight",
			Options: []string{
				"release intake and let the line choose from the backlog again (`yoyo release`)",
				"keep it held until what stopped it is dealt with, and say what that is; it is recorded as direction every role reads",
				"something else, or you want more of the record in front of you first",
			},
			Recommendation: recommendation,
			Topic:          notify.Product(),
		},
	}, true
}

// capacityStoppage is the system blocked on provider capacity for longer than
// waiting it out is a plan.
//
// It is derived from the runs rather than from the log of refusals, because what
// matters is not that a provider refused something once but that everything in
// flight is now parked on one — a line with a run still working is a line that is
// moving, and the heartbeat next door is right to say nothing about it. A product
// with nothing in flight at all is not this state either: it is either choosing
// work, or stopped by something above.
func capacityStoppage(states []runstate.State, now time.Time, budget time.Duration) (stoppage, bool) {
	var oldest runstate.State
	parked := 0
	for _, state := range states {
		if state.Status.Terminal() {
			continue
		}
		if state.UsageLimitResetsAt == nil {
			// One run still working is the whole answer: the system is not stopped,
			// whatever the others are waiting on.
			return stoppage{}, false
		}
		parked++
		if oldest.RunID == "" || state.UpdatedAt.Before(oldest.UpdatedAt) {
			oldest = state
		}
	}
	if parked == 0 {
		return stoppage{}, false
	}
	since := oldest.UpdatedAt
	if since.IsZero() || now.Sub(since) < budget {
		return stoppage{}, false
	}

	// Whether the provider named a moment it comes back, and whether that moment
	// has been and gone, is the whole of what separates a wait from a stall.
	resets := *oldest.UsageLimitResetsAt
	recommendation := fmt.Sprintf("(a): the provider said this lifts at %s, and every parked run resumes from its own record when it does, without anybody doing anything.",
		resets.UTC().Format(time.RFC3339))
	if !resets.After(now) {
		recommendation = fmt.Sprintf("(b): the moment the provider named — %s — has been and gone and the runs are still parked, so this has stopped being a wait.",
			resets.UTC().Format(time.RFC3339))
	}
	return stoppage{
		mark: capacityMark + stamp(since),
		Escalation: notify.Escalation{
			Stopped: fmt.Sprintf("every run in flight is parked on provider capacity: %s, the oldest of them since %s",
				runCount(parked), since.UTC().Format(time.RFC3339)),
			Why:    "nobody chose this and no role can end it. Waiting it out was the plan and the wait has now run past what the harness will sit through without saying so, which makes carrying on waiting a decision rather than a default.",
			Since:  since,
			Record: "run " + oldest.RunID + ", and `yoyo status` for the rest of them",
			Options: []string{
				"keep waiting — the parked runs resume from their own records when capacity comes back",
				"hold everything until you have looked at what this is costing, so nothing new is started or spent meanwhile (`yoyo pause`)",
				"something else, or you want the accounts and the costs in front of you first",
			},
			Recommendation: recommendation,
			Topic:          notify.Product(),
			Refs:           notify.Refs{RunID: oldest.RunID, WorkItemID: oldest.WorkItemID},
		},
	}, true
}

// directiveStoppage is a directive nobody but the operator can settle, and the
// work it named is stopped where it stands until they do.
//
// It is here rather than left to the channel because it is the one blocked state
// whose owner is the operator themselves. A blocked item has the development
// manager's docket; a run parked on a hold is waiting on somebody who knows they
// placed it. An unresolved directive is waiting on an answer only the person who
// gave the instruction has, and nothing else in the harness is going to ask them
// for it.
func (f *HarnessFeed) directiveStoppage() (stoppage, bool, error) {
	if f.Directives == nil {
		return stoppage{}, false, nil
	}
	recorded, err := f.Directives.List()
	if err != nil {
		return stoppage{}, false, fmt.Errorf("read what the harness has been told: %w", err)
	}
	// The oldest unresolved one is the one that has stopped work longest and the one
	// an operator would settle first. The rest are in the record and reach the same
	// person the moment this one is settled.
	directive.Sort(recorded)
	for _, told := range recorded {
		if !told.Pauses() {
			continue
		}
		topic := notify.Product()
		// A directive that named exactly one item is about that item, so the decision
		// is scoped to it. One that named several, or none, is about more work than a
		// thread can be addressed to, and the whole line is the honest reading.
		if len(told.Scope) == 1 {
			if scoped, err := notify.WorkItem(told.Scope[0]); err == nil {
				topic = scoped
			}
		}
		// The item is named first where there is one, because that is what somebody
		// reading this on a phone matches against: the identifier is what they know
		// the work by, and the directive's own is what they resolve it with.
		stopped := fmt.Sprintf("%s is unresolved, and the work it affects stops at its next gate: %s",
			told.ID, singleLine(told.Unresolved, maxStoppageDetailBytes))
		if item := workItemOf(topic); item != "" {
			stopped = fmt.Sprintf("%s is stopped: %s is unresolved, so the work waits at its next gate — %s",
				item, told.ID, singleLine(told.Unresolved, maxStoppageDetailBytes))
		}
		return stoppage{
			mark: directiveMark + told.ID,
			Escalation: notify.Escalation{
				Stopped: stopped,
				Why:     "this is " + told.Kind.Headline() + ", and it is settled by the operator and by nobody else.",
				Since:   told.ReceivedAt,
				Record:  "`yoyo directive list`, which shows it whichever way it was recorded",
				Options: []string{
					"settle it — say how, and the work picks up from exactly where it stopped",
					"withdraw it — say so, and the work carries on against the intent it already had",
					"something else, or you want to see what it is holding up first",
				},
				Recommendation: "(a): nothing else settles this, and the work it names is stopped for as long as it stands.",
				Topic:          topic,
				Refs:           notify.Refs{DirectiveID: told.ID, WorkItemID: workItemOf(topic)},
			},
		}, true, nil
	}
	return stoppage{}, false, nil
}

// runCount says how many runs are parked the way a sentence would.
func runCount(parked int) string {
	if parked == 1 {
		return "one run"
	}
	return fmt.Sprintf("%d runs", parked)
}

// capacityWait is how long a capacity block is left to clear on its own.
func (f *HarnessFeed) capacityWait() time.Duration {
	if f.CapacityWait > 0 {
		return f.CapacityWait
	}
	return DefaultCapacityWait
}

// followUp is how often a state that is still stopped is said again.
func (f *HarnessFeed) followUp() time.Duration {
	if f.Heartbeat > 0 {
		return f.Heartbeat
	}
	return DefaultFollowUp
}
