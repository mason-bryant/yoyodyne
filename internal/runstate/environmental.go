package runstate

// A round the environment handed nothing, and what that costs the item: nothing.
//
// A run's budgets exist to bound the work's own failures. They were being spent
// by the harness's: a round dispatched into a worktree holding none of its
// change delivers an empty diff, and an empty diff spends a review round against
// the item's cap and consumes the repair grant that bought the round. Three
// items advanced toward escalation in one night that way, on rounds that
// delivered exactly what a dead bug handed them, and an escalation produced like
// that reads afterwards as an item nobody could finish.
//
// So the class is durable and it is named. A round is environmental when its
// diff is empty and its run recorded one of the causes below, and the
// conjunction is the whole of the definition. A cause on its own excuses
// nothing: a round that recorded one and still delivered a change spends exactly
// as any other round does. And an empty delivery on its own excuses nothing
// either — with no cause recorded it spends, which is what keeps laziness out of
// the class, and the evidence that tells the two apart is the run's own record.
//
// What the class is worth is decided where a run settles, because that is the
// first point both halves are known. What is recorded here is only the cause,
// written where the harness refuses; the settle reads it, asks the worktree
// whether anything was delivered, and gives back what an environmental round
// must not have spent.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxEnvironmentalDetailBytes bounds the harness's own account of what the
// environment did. It is the harness's words rather than a provider's, so it is
// held to a line rather than to the blocker's bound: what a reader needs is
// which cause it was, and the blocker beside it already carries the long form.
const MaxEnvironmentalDetailBytes = 1 << 10

// MaxEnvironmentalProblemBytes bounds a return the settle decided on and could
// not write. It is a harness failure said in one line, for the same reason.
const MaxEnvironmentalProblemBytes = 1 << 10

// EnvironmentalCause is what the environment did in place of handing a round its
// work. Every one of them is the harness's own failure rather than the work's,
// which is what makes the class a refusal rather than a verdict — the round
// delivered nothing because there was nothing there to deliver from.
//
// The set is closed and small on purpose. An open vocabulary is one every future
// stoppage can be squeezed into, and a class anything can join is a class that
// stops meaning "the harness is answerable for this".
type EnvironmentalCause string

const (
	// CauseHandbackMissingChange is a run picked up again to continue a change,
	// in a worktree that holds none of it. The developer is handed a failure
	// about work that is not there, and delivers an empty change or reinvents
	// one; either way the round is about the seeding rather than about the work.
	CauseHandbackMissingChange EnvironmentalCause = "handback-missing-change"
	// CauseDirtyPrimary is the primary checkout carrying uncommitted state the
	// harness does not own, so the worktree a round needed could not be made from
	// it. What the round was given is not the repository the work is against.
	CauseDirtyPrimary EnvironmentalCause = "dirty-primary"
	// CauseSandboxSpawnFailure is a provider invocation that never ran: the
	// sandbox the agent is confined to could not be entered, so no agent was ever
	// asked the question the round exists to ask.
	CauseSandboxSpawnFailure EnvironmentalCause = "sandbox-spawn-failure"
	// CauseStaleBinaryDispatch is a round dispatched by a build of the harness
	// older than the one the decision was made against, so the gates the decision
	// relied on were not in the binary that carried it out. It is the cause that
	// spent tonight's three rounds, and the one that is hardest to see from the
	// round's own record: everything downstream of it looks perfectly valid.
	CauseStaleBinaryDispatch EnvironmentalCause = "stale-binary-dispatch"
)

// Valid reports a cause this harness recognizes. A record naming anything else
// is refused rather than honored: the class returns budget, so a cause nothing
// declared is a budget nothing accounted for.
func (c EnvironmentalCause) Valid() bool {
	switch c {
	case CauseHandbackMissingChange, CauseDirtyPrimary, CauseSandboxSpawnFailure, CauseStaleBinaryDispatch:
		return true
	default:
		return false
	}
}

// Title says what a cause is, the way somebody reading a docket entry or a
// thread reads it. The identifier is what the record keys on and is not a
// sentence anybody should have to decode.
func (c EnvironmentalCause) Title() string {
	switch c {
	case CauseHandbackMissingChange:
		return "the worktree it was handed held none of the change it was to continue"
	case CauseDirtyPrimary:
		return "the primary checkout carried state the harness does not own"
	case CauseSandboxSpawnFailure:
		return "the sandbox the agent runs in could not be entered"
	case CauseStaleBinaryDispatch:
		return "the build that dispatched it was older than the decision it carried out"
	default:
		return string(c)
	}
}

// EnvironmentalRefusal is one round's account of the environment refusing it,
// and of what the settle then gave back.
//
// The cause is written where the refusal is decided, because that is the only
// place that knows which one it was. Everything after it is written at settle,
// which is the first point the other half of the definition — an empty
// delivery — can be asked. A record carrying a cause and nothing else is a run
// that has not settled yet, or one whose settle could not read the worktree, and
// Problem is what tells those apart.
type EnvironmentalRefusal struct {
	Cause EnvironmentalCause `json:"cause"`
	// Detail is the harness's own account of what it found, folded to a line. It
	// is evidence rather than the blocker: the blocker says what a person has to
	// do about the stoppage, and this says which environmental failure produced
	// it.
	Detail     string    `json:"detail,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	// Refused says the settle found both halves of the definition and classified
	// the round environmental. It is false on a round that recorded a cause and
	// delivered a change anyway, which spends exactly as any other round does —
	// and stays false on one whose settle could not tell, which is the direction
	// that costs an item a round it should have kept rather than one it should
	// have spent.
	Refused bool `json:"refused,omitempty"`
	// RoundReturned and GrantReturned are what the settle actually gave back: the
	// review round the item was charged against its cap, and the granted repair
	// round the continuation consumed. Either can be false on a refused round
	// that never reached the thing it would have spent — a handback refused
	// before any reviewer was asked has no round to return.
	RoundReturned bool `json:"round_returned,omitempty"`
	GrantReturned bool `json:"grant_returned,omitempty"`
	// Problem is a return the settle decided on and could not write. It is never
	// left unsaid: a round classified environmental whose budget was not actually
	// returned is an item walking toward its cap with a record that says it is
	// not, which is the exact failure this class exists to end.
	Problem string `json:"problem,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (r EnvironmentalRefusal) Validate() error {
	var problems []error
	if !r.Cause.Valid() {
		problems = append(problems, fmt.Errorf("environmental cause %q is not one this harness records", r.Cause))
	}
	if len(r.Detail) > MaxEnvironmentalDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(r.Detail), MaxEnvironmentalDetailBytes))
	}
	if len(r.Problem) > MaxEnvironmentalProblemBytes {
		problems = append(problems, fmt.Errorf("problem is %d bytes, which exceeds the %d byte bound", len(r.Problem), MaxEnvironmentalProblemBytes))
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	// Something given back is something that was classified, and nothing else
	// writes either flag. A record the other way round could not have been written
	// by the settle, which is the only thing that writes them.
	if (r.RoundReturned || r.GrantReturned) && !r.Refused {
		problems = append(problems, errors.New("a round or grant returned requires the refusal that returned it"))
	}
	return errors.Join(problems...)
}

// Describe says what one refusal came to, the way a reader of a docket entry or
// a thread reads it: which environmental failure it was, and what the item was
// therefore not charged. A refusal that returned nothing says so rather than
// staying silent, because "environmentally refused" with no accounting after it
// is exactly the sentence a reader would take on trust.
func (r EnvironmentalRefusal) Describe() string {
	if !r.Refused {
		return fmt.Sprintf("%s (%s), and the round delivered a change all the same, so it spent as any round does",
			r.Cause, r.Cause.Title())
	}
	returned := "so it spent no budget and counted toward no cap"
	switch {
	case r.RoundReturned && r.GrantReturned:
		returned = "so the review round it was charged and the granted repair round it consumed were both returned"
	case r.RoundReturned:
		returned = "so the review round it was charged was returned; no repair grant had been consumed"
	case r.GrantReturned:
		returned = "so the granted repair round it consumed was returned; no review round had been charged"
	}
	described := fmt.Sprintf("environmentally refused: %s (%s), %s", r.Cause, r.Cause.Title(), returned)
	if detail := strings.TrimSpace(r.Detail); detail != "" {
		described += " — " + detail
	}
	if problem := strings.TrimSpace(r.Problem); problem != "" {
		described += "; what could not be returned: " + problem
	}
	return described
}
