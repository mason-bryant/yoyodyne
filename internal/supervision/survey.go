package supervision

// One reading of the management loop: what the harness may do now, what it must
// not do, and what somebody should be told.
//
// It is one function rather than two — a normal reading and a restart reading —
// because they are the same question asked of the same records. A restart is
// simply the reading where nothing is held: every request some process was
// carrying when it died turns up with an attempt nobody owns, and is either
// delivered again or, if its answer is already on the record, settled without
// being asked twice. Making the restart a special case would mean the ordinary
// path and the recovery path could disagree, and the recovery path is the one
// that runs least and matters most.
//
// Survey never fails and never blocks. It reads, it compares, and it returns a
// plan; the harness decides what of it to act on, invokes under its own lease
// and its own gates, and writes the records back. Nothing here is a gate on the
// scheduler, and nothing here invokes anything.

import (
	"fmt"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// DefaultInFlight is how many deliveries one product may have open at once
// where nothing configures otherwise. Four is enough for the roles to work in
// parallel on unrelated topics and small enough that a loop which has started
// talking to itself is capped before the cost is interesting.
const DefaultInFlight = 4

// Bounds is what the loop is held to across one product. Per-topic
// serialization is not among them: one durable conversation takes its requests
// one at a time, always, because two deliveries interleaved against one
// transcript is a corrupted transcript rather than a tuning choice.
type Bounds struct {
	InFlight int
}

func (b Bounds) inFlight() int {
	if b.InFlight > 0 {
		return b.InFlight
	}
	return DefaultInFlight
}

// State is everything one reading is made from.
type State struct {
	Requests  []Request
	Readiness []Readiness
	// Revisions is what everything the records name is at now, by reference key.
	// A reading given none of it is one that was not asked about staleness: no
	// judgment is compared and nothing is reported as unreadable, which is what
	// a caller that only wants the delivery plan gets.
	Revisions map[string]string
	// Held names the requests a live process is delivering right now, by
	// identifier. It comes from the leases the harness holds, so a process that
	// died is absent from it without anybody having to clean up after it — and a
	// restart, which holds nothing, passes an empty one.
	Held   map[string]bool
	Bounds Bounds
}

// Delivery is one request the harness may put in front of its target role now.
type Delivery struct {
	RequestID string           `json:"request_id"`
	Topic     string           `json:"topic"`
	To        domain.AgentRole `json:"to"`
	Kind      Kind             `json:"kind"`
	// Attempt is the number this delivery will be recorded as, so the harness
	// writes the attempt before it invokes rather than working out afterwards
	// what it spent.
	Attempt int `json:"attempt"`
	// Reclaimed marks a delivery that follows an attempt whose holder is gone.
	Reclaimed bool   `json:"reclaimed,omitempty"`
	Because   string `json:"because"`
}

// Queued is one request that is ready but not being delivered yet, and what it
// waits for. It is reported rather than left out, because a request that is
// simply absent from a plan reads as one nothing is happening to.
type Queued struct {
	RequestID string `json:"request_id"`
	Topic     string `json:"topic"`
	// Behind names the request holding this one's topic, where that is what it
	// waits for. It is empty where what it waits for is the product's bound.
	Behind  string `json:"behind,omitempty"`
	Because string `json:"because"`
}

// Settlement is one request that has ended and needs its ending written.
type Settlement struct {
	RequestID string  `json:"request_id"`
	Outcome   Outcome `json:"outcome"`
	// Escalate marks an ending the operator has to hear about. A request that ran
	// out of attempts is the case: it is rare, it is expensive, and a loop that
	// swallowed it would retry silently for ever.
	Escalate bool   `json:"escalate,omitempty"`
	Because  string `json:"because"`
}

// Wakeup is a role to wake because a judgment it owns was made against
// something that has since moved. It is advisory: the item is not held, nothing
// is reordered, and whether the judgment actually changes is the owning role's
// to say.
type Wakeup struct {
	Role     domain.AgentRole `json:"role"`
	Judgment Judgment         `json:"judgment"`
	Item     string           `json:"item"`
	Moved    []Moved          `json:"moved"`
	Because  string           `json:"because"`
}

// Degraded is state this reading could not judge, or a record nothing will
// finish. It is named rather than dropped: the failure this whole slice is
// against is work that quietly stops mattering to anybody.
type Degraded struct {
	RequestID string   `json:"request_id,omitempty"`
	Item      string   `json:"item,omitempty"`
	Judgment  Judgment `json:"judgment,omitempty"`
	Because   string   `json:"because"`
}

// Plan is one reading.
type Plan struct {
	Deliver  []Delivery   `json:"deliver,omitempty"`
	Queued   []Queued     `json:"queued,omitempty"`
	Settle   []Settlement `json:"settle,omitempty"`
	Wake     []Wakeup     `json:"wake,omitempty"`
	Degraded []Degraded   `json:"degraded,omitempty"`
}

// Anything reports a reading that found something to do or something to say.
func (p Plan) Anything() bool {
	return len(p.Deliver) > 0 || len(p.Queued) > 0 || len(p.Settle) > 0 ||
		len(p.Wake) > 0 || len(p.Degraded) > 0
}

// Survey reads the durable records and says what the harness may do now.
func Survey(state State) Plan {
	requests := make([]Request, len(state.Requests))
	copy(requests, state.Requests)
	SortRequests(requests)

	plan := Plan{}
	limit := state.Bounds.inFlight()

	// What some live process is already carrying holds its topic and counts
	// against the bound. A restart holds nothing, so this pass finds nothing and
	// every interrupted request falls through to be reclaimed below.
	// The lease decides this rather than the attempt record. A harness takes the
	// lease and then writes the attempt, so a request whose lease is held and
	// whose attempt is not written yet is one somebody is about to deliver — and
	// planning it here, on the evidence that no attempt is recorded, is exactly
	// the double delivery the lease exists to prevent.
	topics := make(map[string]string)
	open := 0
	for _, request := range requests {
		if !request.Open() || request.Answered() || !state.Held[request.ID] {
			continue
		}
		open++
		if _, taken := topics[request.Topic]; !taken {
			topics[request.Topic] = request.ID
		}
	}

	for _, request := range requests {
		if !request.Open() {
			continue
		}
		// The answer is on the record, so the request is over whatever else is
		// true of it. This is the case a restart must get right: a process that
		// died between recording the answer and writing the ending left a request
		// that looks unfinished and has already been paid for.
		if request.Answered() {
			plan.Settle = append(plan.Settle, Settlement{
				RequestID: request.ID,
				Outcome:   OutcomeAnswered,
				Because: fmt.Sprintf("the %s's answer to attempt %d is on the record; it settles rather than being asked again",
					request.To, request.Response.Attempt),
			})
			continue
		}
		if state.Held[request.ID] {
			// Somebody is carrying it. Not deliverable, not queued, and not this
			// reading's business.
			continue
		}
		attempt, running := request.InFlight()
		spent := request.Spent()
		if spent >= request.CycleLimit {
			plan.Settle = append(plan.Settle, Settlement{
				RequestID: request.ID,
				Outcome:   OutcomeUnresolved,
				Escalate:  true,
				Because: fmt.Sprintf("%d of %d attempts are spent and the %s has not answered",
					spent, request.CycleLimit, request.To),
			})
			if running {
				plan.Degraded = append(plan.Degraded, Degraded{
					RequestID: request.ID,
					Because: fmt.Sprintf("attempt %d was left open by %s, which is gone, and the cycle limit is spent: nothing will deliver it again and no answer was recorded",
						attempt.Number, attempt.Holder),
				})
			}
			continue
		}
		if holder, taken := topics[request.Topic]; taken {
			plan.Queued = append(plan.Queued, Queued{
				RequestID: request.ID,
				Topic:     request.Topic,
				Behind:    holder,
				Because:   "one topic takes its requests one at a time, and this one is already taken",
			})
			continue
		}
		if open >= limit {
			plan.Queued = append(plan.Queued, Queued{
				RequestID: request.ID,
				Topic:     request.Topic,
				Because:   fmt.Sprintf("%d deliveries are already open, which is the bound", limit),
			})
			continue
		}
		delivery := Delivery{
			RequestID: request.ID,
			Topic:     request.Topic,
			To:        request.To,
			Kind:      request.Kind,
			Attempt:   spent + 1,
			Reclaimed: running,
			Because: fmt.Sprintf("attempt %d of %d for the %s",
				spent+1, request.CycleLimit, request.To),
		}
		if running {
			delivery.Because = fmt.Sprintf("attempt %d was left open by %s, which is gone, and no answer was recorded; this is attempt %d of %d",
				attempt.Number, attempt.Holder, spent+1, request.CycleLimit)
		}
		plan.Deliver = append(plan.Deliver, delivery)
		topics[request.Topic] = request.ID
		open++
	}

	// Staleness is only judged where the caller said what things are at now.
	if len(state.Revisions) == 0 {
		return plan
	}
	// A request this reading has just settled is over, whatever its own record
	// still says: its outcome is empty only because the harness has not written
	// the ending yet. Reporting that its answer will be read against something
	// that has moved would be degraded state nobody can act on, which is the one
	// thing that makes the list unreadable.
	settling := make(map[string]bool, len(plan.Settle))
	for _, settlement := range plan.Settle {
		settling[settlement.RequestID] = true
	}
	for _, request := range requests {
		if !request.Open() || settling[request.ID] {
			continue
		}
		for _, changed := range request.Moved(state.Revisions) {
			plan.Degraded = append(plan.Degraded, Degraded{
				RequestID: request.ID,
				Because: fmt.Sprintf("it was written against %s at %s, which is now at %s, so the answer will be read against something that has moved",
					changed.Reference.Key(), changed.Reference.Revision, changed.Now),
			})
		}
		for _, unread := range request.Unknown(state.Revisions) {
			plan.Degraded = append(plan.Degraded, Degraded{
				RequestID: request.ID,
				Because: fmt.Sprintf("it was written against %s at %s, and nothing here says what that is at now",
					unread.Key(), unread.Revision),
			})
		}
	}
	for _, judgment := range Current(state.Readiness) {
		if changed := judgment.Moved(state.Revisions); len(changed) > 0 {
			plan.Wake = append(plan.Wake, Wakeup{
				Role:     judgment.Judgment.Owner(),
				Judgment: judgment.Judgment,
				Item:     judgment.Item,
				Moved:    changed,
				Because: fmt.Sprintf("%s for %s was judged %s against %s, which has moved since; the item is not held for it",
					judgment.Judgment, judgment.Item, judgment.Disposition, describeMoved(changed)),
			})
		}
		for _, unread := range judgment.Unknown(state.Revisions) {
			plan.Degraded = append(plan.Degraded, Degraded{
				Item:     judgment.Item,
				Judgment: judgment.Judgment,
				Because: fmt.Sprintf("it was judged against %s at %s, and nothing here says what that is at now, so whether it is stale cannot be told",
					unread.Key(), unread.Revision),
			})
		}
	}
	return plan
}

// SortRequests orders requests the way they are taken: oldest first, and by
// identifier where two were opened at the same instant, so two readings of one
// store deliver in the same order. Age is the whole of the priority — a request
// that has waited longest is delivered first, and no kind jumps the queue,
// because an escalation that overtook a consult would be this package deciding
// what matters.
func SortRequests(requests []Request) {
	sort.SliceStable(requests, func(first, second int) bool {
		left, right := requests[first], requests[second]
		if !left.OpenedAt.Equal(right.OpenedAt) {
			return left.OpenedAt.Before(right.OpenedAt)
		}
		return left.ID < right.ID
	})
}

// describeMoved names what moved, for the sentence a role reads when it is
// woken. One reference is named; several are counted and the first named, so the
// reason stays a sentence.
func describeMoved(changed []Moved) string {
	if len(changed) == 1 {
		return changed[0].Reference.Key()
	}
	return fmt.Sprintf("%s and %d others", changed[0].Reference.Key(), len(changed)-1)
}
