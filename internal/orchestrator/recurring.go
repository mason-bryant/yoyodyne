package orchestrator

// Waking a role on a cadence, with nobody standing there to do the waking.
//
// Everything else the harness starts is reactive: an item is admitted, a run
// stops, an operator asks. The work that has no trigger is the standing kind — a
// look over what has gone unresolved, at intervals, whether or not anything
// happened — and until now the only thing that could start it was a person
// remembering to. That person is what this replaces, and it replaces nothing
// else: the role woken is the role the configuration names, the conversation is
// its own, the authority is the one its role already holds, and what it does
// about what it finds it does through the paths it already acts through.
//
// # What configuration decides, and what it cannot
//
// Which role, how often, what to say, and whether the task is on. That is the
// whole of it. There is no configuration key here for a capability, a tool, an
// account, or an authority of any kind, and the absence is the point: a schedule
// that could widen what a role may do would make the schedule the place to look
// for what the harness is allowed to do, which is exactly what keeping capability
// in trusted code exists to prevent. A recurring turn is authorized identically
// to a conversation an operator opens by hand, because it is one.
//
// # One firing per pass, and turns inside it
//
// A pass fires at most one task, for the reason a pass delivers at most one
// stoppage: a firing is conversation turns, and a pass that fired three tasks
// would hold the queue closed for as long as all three took. The next pass takes
// the next due task, and on a poll loop that is a minute later.
//
// Inside a firing, turns iterate. A pass with more to do than one turn holds says
// so in its own account and is given another, up to the task's bound. That is the
// alternative to the two things a single turn forces: a role rushing a morning's
// work into bounds that will not hold it, or one silently truncating at whatever
// limit it hit. Both look identical to a reader afterwards — a short report — and
// the whole value of a recurring pass is that its reports can be believed.
//
// # A firing that failed waits for its next cadence
//
// This is the deliberate opposite of the escalation beside it, and the store
// says why: a stoppage that failed to reach the development manager is one
// specific thing nobody has heard about, and a recurring pass that failed is run
// again at its next cadence anyway, because the next pass looks at everything
// this one would have. What a fast retry would buy is turns spent against a
// provider that is out of capacity, once per pull, for as long as the outage
// lasts.
//
// # The pause and not the intake hold
//
// A firing is a provider invocation, so the operator's pause covers it exactly as
// it covers a run, a conversation turn, and a delivery. The intake hold is
// deliberately not read: holding intake stops the harness choosing work, and this
// chooses nothing, claims nothing, and starts nothing — what it produces is a
// role looking at what has already gone wrong, which is usually what a held
// queue is waiting on.
//
// # The development manager is handed the docket
//
// A wakeup of the development manager carries where the triage docket stands,
// read at the moment of the firing: every stoppage nobody has decided about, and
// every decision recorded that the harness has not carried out. It is on the
// wake message rather than left to her conversation's own context because that
// context is gathered once, when the conversation opens, and a sweep on a resumed
// conversation reads a docket days old. That is how the hourly sweep came to
// report calm on 2026-09-14 while thirty stoppages waited where it could not see
// them.
//
// What that makes the sweep is the re-offer. The event-driven delivery beside it
// puts a stoppage to her once, when the run newly stops, and only for the two
// classes it covers; this puts every undecided entry to her on every cadence,
// whatever class it is and however it reached the docket, until a decision naming
// its run is recorded. A wait is a decision, so it is bounded by her rather than
// by the harness, and it is idempotent by construction: the same docket read
// twice is the same list twice.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// RecurringClaims is the durable cadence: where a firing is claimed before it is
// made, and what became of it recorded after. It is what makes the cadence hold
// across processes and across restarts — two sessions polling one schedule
// produce one firing — and a trigger wired without it is one that would fire
// every pull.
//
// It is satisfied by runstate.SweepStore.
type RecurringClaims interface {
	Claim(ctx context.Context, task string, every time.Duration, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
}

// RecurringReports is where a firing's account is kept. It is required for the
// reason the whole feature exists: a pass nobody watched that wrote nothing down
// is a turn spent in private, and the operator reading these at leisure is the
// entire point of running them.
//
// It is satisfied by runstate.SweepStore.
type RecurringReports interface {
	Append(recorded runstate.Sweep) error
}

// RecurringRole is a role's conversation as the harness reaches it: one message
// sent into it, and what came back.
//
// Nothing here decides anything on the role's behalf and nothing carries its
// decisions out. What comes back is read from what it actually said, so a firing
// answered in prose with no account reports exactly that.
type RecurringRole interface {
	Wake(ctx context.Context, role domain.AgentRole, message string) (Turn, error)
}

// RecurringDocket is where the triage docket stands, read at the moment a
// firing is made. It is read and never written: handing the docket to the
// development manager settles nothing about any entry on it.
//
// It is satisfied by a caller wrapping readmodel.ReadDocket.
type RecurringDocket interface {
	Standing(ctx context.Context) readmodel.DocketStanding
}

// Turn is what one turn of a firing came to.
type Turn struct {
	ConversationID string `json:"conversation_id,omitempty"`
	// CostUSD is what the provider charged for the turn, as it reported it. It is
	// carried back because a firing is a spend the caller made rather than one a
	// run made, so a session counting what it has spent has no other way to see
	// it. A turn that failed carries what it cost too: the provider charged for
	// it exactly as it charges for one that answered.
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Result is the account the role gave of the pass, where it gave one.
	Result *sweep.Result `json:"result,omitempty"`
	// ResultProblem names an account that could not be read, or a turn that
	// carried none. It is not a failed turn: the role answered, and what is lost
	// is the structure rather than the work.
	ResultProblem string `json:"result_problem,omitempty"`
}

// ErrRoleUnreachable reports a firing that failed before the role was asked
// anything: its conversation could not be opened at all. It is its own error
// because it is a failure that provably spent nothing and said nothing, so what
// it is worth recording about the firing is different.
var ErrRoleUnreachable = errors.New("the role's conversation could not be opened")

// Fired is one task this pass woke, and what came back. It reports a firing that
// did not happen as carefully as one that did.
type Fired struct {
	Task string           `json:"task"`
	Role domain.AgentRole `json:"role"`
	// Turns is how many turns the firing took, and CostUSD what they cost. Both
	// are on the record whichever way the firing went.
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd,omitempty"`
	// Findings and SilentRepairs are what the pass found and how many of its
	// fixes filed nothing for their root cause. They are counts here because this
	// is the line a session prints; the whole account is in the durable report.
	Findings      int `json:"findings"`
	SilentRepairs int `json:"silent_repairs,omitempty"`
	// Truncated marks a pass that still had more to do when its turn bound ran
	// out. It is the one thing a reader cannot infer from a short report, and
	// leaving it unsaid would make a bounded pass look like a finished one.
	Truncated bool `json:"truncated,omitempty"`
	// Undecided and Uncarried are what the docket handed to the development
	// manager held: the stoppages with no decision standing and the decisions the
	// harness has not carried out. They are on the pass's own line so the operator
	// reading it sees the same figures she was woken with, and they are zero on
	// every task that is not hers.
	Undecided int `json:"undecided,omitempty"`
	Uncarried int `json:"uncarried,omitempty"`
	// Problem is what stopped or spoiled the firing.
	Problem string `json:"problem,omitempty"`
}

// RecurringSweep is what one pass did about the schedule. A pass that found
// nothing due reports nothing, which is almost every pass.
type RecurringSweep struct {
	Fired []Fired `json:"fired,omitempty"`
	// Paused is the operator's pause, when one is what stopped this. Nothing was
	// claimed and every task keeps its cadence.
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
}

// Trigger fires the configured recurring tasks. It has no tracker, no worktree
// access, and no forge access, and it starts nothing: what it does is wake a role
// on a cadence and write down what the role said it did.
type Trigger struct {
	// Tasks is the schedule as this pull read the configuration, keyed by the
	// name each task is recorded under. It is passed in rather than read here for
	// the reason every other configured value on a pull is: a cadence changed
	// under a running session takes effect at the next pull.
	Tasks map[string]config.RecurringTask
	// Claims is what makes the cadence a cadence. Required.
	Claims RecurringClaims
	// Reports is where a firing's account is kept. Required.
	Reports RecurringReports
	// Roles is how the harness reaches a role's conversation. Required.
	Roles RecurringRole
	// Docket is where the triage docket stands, handed to the development manager
	// with every wakeup of hers. Optional, and a trigger wired without one wakes
	// her blind to the docket, which is what every sweep before yoyodyne-ifd.353
	// was; the wake message then says so rather than leaving her to report calm.
	Docket RecurringDocket
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and a trigger wired without one is one nothing can pause, which
	// is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// Fire wakes the first task that is due, and reports what came of it.
//
// The order is the order the guarantees need. The pause is read before anything
// is claimed, so a paused harness costs no task its cadence; the firing is
// claimed before the first turn is taken, so a process that dies between the two
// has recorded a firing that produced nothing rather than made one nothing paces;
// and the account is written after, because until the role has answered there is
// nothing to write.
func (t Trigger) Fire(ctx context.Context) (RecurringSweep, error) {
	if err := t.validate(); err != nil {
		return RecurringSweep{}, err
	}
	hold, held, err := t.paused()
	if err != nil {
		return RecurringSweep{}, err
	}
	if held {
		return RecurringSweep{Paused: &hold}, nil
	}
	var problems []error
	for _, name := range t.names() {
		task := t.Tasks[name]
		if !task.Enabled {
			continue
		}
		// The claim is the due check. Asking first and claiming after would be two
		// reads and a write with a window between them, which is exactly the window
		// two concurrent sessions land in.
		if _, err := t.Claims.Claim(ctx, name, task.Every.Duration(), t.now()); err != nil {
			// A task that is not due is the ordinary answer on almost every pull, and
			// so is one another process claimed a moment ago. Neither is this pass's
			// to report.
			if errors.Is(err, runstate.ErrSweepNotDue) {
				continue
			}
			problems = append(problems, fmt.Errorf("claim the firing of the recurring task %s: %w", name, err))
			continue
		}
		fired := t.run(ctx, name, task)
		return RecurringSweep{Fired: []Fired{fired}}, errors.Join(problems...)
	}
	return RecurringSweep{}, errors.Join(problems...)
}

// run takes one firing's turns and records what they came to. It never returns
// an error: a firing that failed is a fact about the schedule that belongs in the
// record and beside the pass, rather than something that stops the pull.
func (t Trigger) run(ctx context.Context, name string, task config.RecurringTask) Fired {
	fired := Fired{Task: name, Role: task.Role}
	recorded := runstate.Sweep{
		Task:      name,
		Role:      task.Role,
		StartedAt: t.now(),
	}
	var merged *sweep.Result
	var problems []string
	docket, docketProblem := t.docketFor(ctx, task)
	if docketProblem != "" {
		// Said on the record as well as to her: a pass that was woken over a docket
		// nobody could read is one whose "no findings" means less than it says.
		problems = append(problems, docketProblem)
	}
	if docket != nil {
		fired.Undecided, fired.Uncarried = len(docket.Undecided), len(docket.Uncarried)
		recorded.DocketUndecided, recorded.DocketUncarried = fired.Undecided, fired.Uncarried
	}
	message := wakeMessage(name, task, docket)
	for turn := 0; turn < task.Turns(); turn++ {
		answered, err := t.Roles.Wake(ctx, task.Role, message)
		// What the turn cost is carried whichever way it went, because the provider
		// charges for a turn that failed exactly as for one that answered.
		fired.CostUSD += answered.CostUSD
		recorded.CostUSD += answered.CostUSD
		if conversation := strings.TrimSpace(answered.ConversationID); conversation != "" {
			recorded.ConversationID = conversation
		}
		if err != nil {
			problems = append(problems, describeFailedTurn(name, task.Role, turn+1, err))
			break
		}
		fired.Turns++
		recorded.Turns++
		if answered.ResultProblem != "" {
			problems = append(problems, answered.ResultProblem)
		}
		if answered.Result == nil {
			// A turn that answered without an account ends the firing rather than
			// asking again: the role has said what it had to say, and a second turn
			// would be spent asking it to reformat rather than to do anything.
			break
		}
		if merged == nil {
			merged = answered.Result
		} else {
			folded := merged.Merge(*answered.Result)
			merged = &folded
		}
		if answered.Result.Status != sweep.StatusMore {
			break
		}
		if turn+1 >= task.Turns() {
			// The bound ended the pass rather than the work. Said out loud in both
			// places, because a truncated pass and a finished one produce the same
			// short report and nothing else distinguishes them.
			fired.Truncated = true
			problems = append(problems, fmt.Sprintf(
				"the recurring task %s still had more to do after all %d of its turns, so its pass is partial and the rest waits for the next firing",
				name, task.Turns()))
			break
		}
		message = continueMessage(name)
	}
	// A firing that produced no account says so here rather than relying on
	// whoever wired the conversation to have said it. The record refuses a sweep
	// that can explain itself neither way, and that refusal must never be what
	// loses the report: a pass with no account and no problem is exactly the
	// firing an operator most needs to be able to find.
	if merged == nil && len(problems) == 0 {
		problems = append(problems, fmt.Sprintf(
			"the pass of the recurring task %s produced no account of itself, so what it found is only in the %s's conversation",
			name, task.Role))
	}
	recorded.EndedAt = t.now()
	recorded.Result = merged
	// Bounded, because the record's own bound on this prose refuses a record that
	// carries too much of it — and every one of these sentences ends with a
	// provider's error message, whose length nothing here controls. Losing a whole
	// pass's report because the description of a smaller failure ran long is the
	// exact trade this must not make.
	recorded.Problem = boundedProblem(problems)
	fired.Problem = recorded.Problem
	if merged != nil {
		fired.Findings = len(merged.Findings)
		fired.SilentRepairs = merged.SilentRepairs()
	}
	t.settle(ctx, &fired, recorded)
	return fired
}

// settle writes the firing's durable report and records what became of it against
// the cadence.
//
// Both writes happen under a context detached from the firing's own, for the
// reason the escalation's records are: a shutdown cancels the very context the
// firing ran under, and it can land between the role's answer arriving and these
// writes. A report lost there is a pass that spent turns and told nobody, which
// is the one outcome the whole mechanism exists to prevent.
func (t Trigger) settle(ctx context.Context, fired *Fired, recorded runstate.Sweep) {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if err := t.Reports.Append(recorded); err != nil {
		fired.Problem = appendProblem(fired.Problem, t.recordWithoutTheAccount(recorded, err))
	}
	if _, err := t.Claims.Settle(write, recorded.Task, boundedProblem([]string{fired.Problem})); err != nil {
		fired.Problem = appendProblem(fired.Problem, fmt.Sprintf(
			"what became of the firing of the recurring task %s could not be written against its cadence: %v", recorded.Task, err))
	}
}

// recordWithoutTheAccount is the second attempt at recording a firing whose first
// attempt was refused, and it says what became of both.
//
// The bounds above are meant to make the first attempt always succeed, and this
// is here because "meant to" is not a guarantee an operator can read. A firing
// that spent turns and left nothing behind is indistinguishable from one that
// never happened, so what is written instead is the same firing with its account
// left off: which task, which role, how many turns, what it cost, and the fact
// that the account itself would not store. That is a much smaller record, built
// only from what the harness itself knows, so what could refuse it is a store
// that cannot be written at all — which is a different problem and one every
// other record on this machine has too.
func (t Trigger) recordWithoutTheAccount(recorded runstate.Sweep, refused error) string {
	lost := fmt.Sprintf("the account of the pass of the recurring task %s would not store, so this record carries the firing without it: %v",
		recorded.Task, refused)
	reduced := recorded
	reduced.Result = nil
	reduced.Problem = boundedProblem([]string{lost, recorded.Problem})
	if err := t.Reports.Append(reduced); err != nil {
		return fmt.Sprintf("the pass of the recurring task %s could not be recorded at all, so what it found reaches nobody: %v; and again without its account: %v",
			recorded.Task, refused, err)
	}
	return lost
}

// boundedProblem joins what went wrong with a firing and holds it to what the
// durable record accepts. The pieces are kept in the order they happened and the
// join is cut rather than dropped, so what survives is the earliest failures —
// which are the ones that caused the rest.
func boundedProblem(problems []string) string {
	var kept []string
	for _, problem := range problems {
		if strings.TrimSpace(problem) != "" {
			kept = append(kept, strings.TrimSpace(problem))
		}
	}
	joined := strings.Join(kept, "; ")
	if len(joined) <= runstate.MaxSweepTextBytes {
		return joined
	}
	const cut = " […]"
	return strings.TrimSpace(joined[:runstate.MaxSweepTextBytes-len(cut)]) + cut
}

func appendProblem(existing, addition string) string {
	switch {
	case strings.TrimSpace(addition) == "":
		return existing
	case strings.TrimSpace(existing) == "":
		return addition
	default:
		return existing + "; " + addition
	}
}

// describeFailedTurn says what became of a turn that did not answer, in the words
// each failure earns. A conversation that could never be opened asked the role
// nothing and spent nothing, which is a different sentence from a turn that
// started and failed somewhere inside it.
func describeFailedTurn(name string, role domain.AgentRole, turn int, err error) string {
	if errors.Is(err, ErrRoleUnreachable) {
		if turn == 1 {
			return fmt.Sprintf("the recurring task %s could not be put to the %s at all, so nothing was asked and its next firing is at its next cadence: %v", name, role, err)
		}
		// A later turn losing the conversation is not a firing that asked nothing.
		// The turns before it answered and the account this record carries is
		// theirs, so the sentence has to name the turn rather than the firing —
		// otherwise the prose says nothing was asked while the turn count beside it
		// says otherwise, and a reader has to decide which of the two to believe.
		return fmt.Sprintf("turn %d of the recurring task %s could not be put to the %s, so its pass is partial and carries only the turns before it: %v", turn, name, role, err)
	}
	return fmt.Sprintf("turn %d of the recurring task %s failed, so its pass is partial: %v", turn, name, err)
}

// wakeMessage is what the harness says when it wakes a role for a task.
//
// Three parts, in this order and for three different reasons. The standing
// preamble is the harness's: it says who woke the role and why, so a scheduled
// turn can never read as somebody asking. The configured prompt is the project's
// — the task itself. The contract is the channel's, stated where the bounds are
// enforced.
//
// The one instruction the harness adds to the project's own is about filing, and
// it is here rather than in the configured prompt deliberately: checking a
// finding against work already admitted before filing it is a constraint on every
// recurring task, not a preference of one, and a project that edited its prompt
// must not be able to edit it away. Until the admission guard exists it is the
// only thing standing between a weekly cadence and a duplicate admitted every
// week, which has already cost this project a full run and two review rounds
// twice.
func wakeMessage(name string, task config.RecurringTask, docket *readmodel.DocketStanding) string {
	parts := []string{
		fmt.Sprintf("The harness woke you for the recurring task %q, which runs every %s. Nobody is waiting at a terminal for this: what you produce is recorded and read later.", name, task.Every),
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and nothing about being woken on a schedule widens what you may decide or change.",
		"Before you file anything, check it against the work already admitted. A duplicate admission costs a whole run and the reviews after it, and a task that runs on a cadence files the same duplicate on every cadence.",
		"",
		strings.TrimSpace(task.Prompt),
		"",
	}
	// The docket goes between the task and the contract, as evidence the task is
	// to be done over. It is the harness's own reading rather than the role's, so
	// it comes after the project's instruction and is never mistaken for part of
	// it; and it goes before the contract so a pass that says NO FINDINGS has read
	// what it is denying.
	if section := docketSection(task.Role, docket); section != "" {
		parts = append(parts, section, "")
	}
	parts = append(parts, sweep.Contract())
	return strings.Join(parts, "\n")
}

// docketFor reads where the docket stands for a task that wakes the development
// manager, and reports what stopped the reading. Every other role is handed
// nothing: the docket is hers to decide, and a docket delivered to a role that
// cannot act on it is a section every pass pays for and reads past.
//
// A reading with problems is still handed over. What it found is real, and what
// it could not read it says; the problem is reported beside the pass so the
// record shows the sweep was woken over a docket it could only partly see.
func (t Trigger) docketFor(ctx context.Context, task config.RecurringTask) (*readmodel.DocketStanding, string) {
	if task.Role != domain.RoleDevelopmentManager || t.Docket == nil {
		return nil, ""
	}
	standing := t.Docket.Standing(ctx)
	if len(standing.Problems) == 0 {
		return &standing, ""
	}
	return &standing, fmt.Sprintf("the docket handed to the %s could not be read in full, so its pass may have been made over stoppages it could not see: %s",
		task.Role, strings.Join(standing.Problems, "; "))
}

// docketSection is the docket as the wake message carries it. A development
// manager woken by a trigger wired without one is told so in as many words,
// because the alternative is a sweep that reads its opening context's docket —
// gathered when the conversation opened, however long ago — as though it were
// current, and reports calm off it.
func docketSection(role domain.AgentRole, docket *readmodel.DocketStanding) string {
	if role != domain.RoleDevelopmentManager {
		return ""
	}
	if docket == nil {
		return "# Triage docket: not read\n\nThis wakeup was not handed the triage docket, and the docket in your opening context was gathered when the conversation opened rather than now. Do not report that nothing is waiting on your decision: survey the stopped work before concluding anything about it."
	}
	return strings.TrimSpace(docket.Render())
}

// continueMessage is what a pass that said it had more to do is given next. It
// deliberately says nothing about what to look at: the role has the whole of its
// own previous turn in the conversation, and repeating the task here would be a
// second copy of an instruction that can disagree with the first.
func continueMessage(name string) string {
	return strings.Join([]string{
		fmt.Sprintf("You said the pass of %q had more to do than that turn held. Carry on with the rest of it.", name),
		"Report only what this turn found: what you already reported is kept, and repeating it would be counted twice.",
		"",
		sweep.Contract(),
	}, "\n")
}

// names lists the configured tasks in a stable order, so which task a pass fires
// is decided by the schedule rather than by map iteration.
func (t Trigger) names() []string {
	names := make([]string, 0, len(t.Tasks))
	for name := range t.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// paused reports the operator's pause over everything the harness spends. A pause
// that cannot be read refuses the pass rather than being spent through, exactly
// as it does everywhere else it is read.
func (t Trigger) paused() (runstate.OperatorHold, bool, error) {
	if t.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := t.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (t Trigger) validate() error {
	var problems []error
	if t.Claims == nil {
		problems = append(problems, errors.New("firing a recurring task requires the durable claim that paces it, because a cadence nothing records fires on every pull"))
	}
	if t.Reports == nil {
		problems = append(problems, errors.New("firing a recurring task requires somewhere to record what it found, because a pass nobody watched that wrote nothing down is a turn spent in private"))
	}
	if t.Roles == nil {
		problems = append(problems, errors.New("firing a recurring task requires the role's conversation to wake"))
	}
	return errors.Join(problems...)
}

func (t Trigger) now() time.Time {
	if t.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return t.Clock.Now().UTC()
}

// Render describes what one pass did about the schedule, for whoever asked.
func (s RecurringSweep) Render() string {
	var rendered strings.Builder
	if s.Paused != nil {
		fmt.Fprintf(&rendered, "PAUSED: no recurring task was fired, since %s\n",
			s.Paused.HeldAt.UTC().Format(time.RFC3339))
	}
	for _, fired := range s.Fired {
		switch {
		case fired.Turns == 0:
			fmt.Fprintf(&rendered, "the recurring task %s did not reach the %s\n", fired.Task, fired.Role)
		case fired.Findings == 0:
			fmt.Fprintf(&rendered, "the recurring task %s woke the %s and found nothing, in %d turn(s)\n",
				fired.Task, fired.Role, fired.Turns)
		default:
			fmt.Fprintf(&rendered, "the recurring task %s woke the %s, which found %d thing(s) in %d turn(s)\n",
				fired.Task, fired.Role, fired.Findings, fired.Turns)
		}
		if fired.Undecided > 0 || fired.Uncarried > 0 {
			fmt.Fprintf(&rendered, "  it was handed the docket: %d stoppage(s) with no decision standing, %d recorded decision(s) not yet carried out\n",
				fired.Undecided, fired.Uncarried)
		}
		if fired.SilentRepairs > 0 {
			fmt.Fprintf(&rendered, "  %d of its fixes filed nothing for their root cause\n", fired.SilentRepairs)
		}
		if fired.Problem != "" {
			fmt.Fprintf(&rendered, "  %s\n", fired.Problem)
		}
	}
	return rendered.String()
}
