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
	// spent the three rounds this class was built for, and the one that is hardest
	// to see: a stale build does not refuse, it proceeds, and everything
	// downstream of it looks perfectly valid.
	//
	// Nothing writes it yet, and that is a gap rather than an oversight. There is
	// no refusal site to hang it on, because the failure is precisely the absence
	// of one; recognizing it needs the harness to record which build reserved a run
	// and which build carried out each triage decision, and to compare the two at
	// dispatch. Until that exists a round refused this way is caught by whichever
	// of the causes above its symptom trips — which is how the field cases reached
	// this class, as handback-missing-change.
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
// delivery — can be asked. Settled is what says the round got that far at all,
// and Problem is what says a settle that got there could not finish: those are
// three different states, and a reader shown one as another decides an
// escalation against a figure the harness knows is wrong.
type EnvironmentalRefusal struct {
	Cause EnvironmentalCause `json:"cause"`
	// Detail is the harness's own account of what it found, folded to a line. It
	// is evidence rather than the blocker: the blocker says what a person has to
	// do about the stoppage, and this says which environmental failure produced
	// it.
	Detail     string    `json:"detail,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	// Settled says the round this cause belongs to has ended and the class was
	// decided on it. It is what makes the settle one-shot: a cause recorded on a
	// round the harness turned away without charging it is settled there and then,
	// so a later round of the same run that happens to deliver nothing is judged on
	// its own evidence instead of inheriting this one's.
	Settled bool `json:"settled,omitempty"`
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
	// Something given back is something that was classified, and a classification
	// is something a settle made. A record the other way round could not have been
	// written by the settle, which is the only thing that writes any of the three.
	if (r.RoundReturned || r.GrantReturned) && !r.Refused {
		problems = append(problems, errors.New("a round or grant returned requires the refusal that returned it"))
	}
	if r.Refused && !r.Settled {
		problems = append(problems, errors.New("a refused round requires the settle that classified it"))
	}
	return errors.Join(problems...)
}

// Describe says what one refusal came to: which environmental failure it was,
// and what the item was therefore charged or not charged for the round.
//
// It is the one derivation of that, and every surface phrases around it rather
// than reading the flags again. The states are close enough to be got wrong
// separately — and the one that matters most is the one a drift would silently
// drop, because a refusal whose return could not be written is the single case
// where the item's counters really are higher than what the round cost it. Three
// copies of this would be three places for that case to go missing.
//
// It never says "spent nothing" on a figure it did not actually give back. A
// refusal that returned nothing says so, because "environmentally refused" with
// no accounting after it is exactly the sentence a reader takes on trust.
//
// It carries neither the detail nor the problem text. Those are evidence, and a
// surface that wants them renders them beside this rather than inside it: a
// docket entry has room for a block and a thread line does not.
func (r EnvironmentalRefusal) Describe() string {
	named := fmt.Sprintf("%s (%s)", r.Cause, r.Cause.Title())
	switch {
	case !r.Settled:
		return fmt.Sprintf("environmental cause recorded: %s; the round it belongs to has not settled, so nothing has been decided about what it cost", named)
	case !r.Refused && strings.TrimSpace(r.Problem) != "":
		return fmt.Sprintf("environmental cause recorded: %s, and whether the round delivered anything could not be read, so it spent as any round does", named)
	case !r.Refused:
		return fmt.Sprintf("environmental cause recorded: %s, and the round delivered a change all the same, so it spent as any round does", named)
	case strings.TrimSpace(r.Problem) != "":
		return fmt.Sprintf("environmentally refused: %s, and what it should have been given back could not be written, so this item's counters are higher than the round cost it", named)
	case r.RoundReturned && r.GrantReturned:
		return fmt.Sprintf("environmentally refused: %s, so the review round it was charged and the granted repair round it consumed were both returned, and this item stands where it did before the round", named)
	case r.RoundReturned:
		return fmt.Sprintf("environmentally refused: %s, so the review round it was charged was returned and no repair grant had been consumed, and this item stands where it did before the round", named)
	case r.GrantReturned:
		return fmt.Sprintf("environmentally refused: %s, so the granted repair round it consumed was returned and no review round had been charged, and this item stands where it did before the round", named)
	default:
		return fmt.Sprintf("environmentally refused: %s, and it reached nothing that spends, so there was nothing to give back and this item stands where it did before the round", named)
	}
}
