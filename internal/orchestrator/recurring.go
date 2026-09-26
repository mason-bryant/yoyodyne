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
// Which role, how often, what to say, whether the task is on, and which model
// its turns ask for where it names one. That is the whole of it. The model is a
// spend decision rather than an authority: the turn is the role's, under the
// role's account and failover, holding what the role holds, and a turn the task
// does not cover asks for the role's own model. There is no configuration key
// here for a capability, a tool, an
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

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
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
	// Summon claims a firing out of cadence, which is what the intake brake asks
	// for: the development manager's sweep now, rather than at its next pass.
	Summon(ctx context.Context, task string, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
}

// RecurringReports is where a firing's account is kept. It is required for the
// reason the whole feature exists: a pass nobody watched that wrote nothing down
// is a turn spent in private, and the operator reading these at leisure is the
// entire point of running them.
//
// It is read back as well as written, for one thing: which pull requests the
// earlier passes already reported, so the forge reading says each once.
//
// It is satisfied by runstate.SweepStore.
type RecurringReports interface {
	Append(recorded runstate.Sweep) error
	List() ([]runstate.Sweep, []runstate.UnreadableSweep, error)
}

// RecurringForge is the harness's own reading of the forge, taken on the
// development manager's pass beside the role's turns: which pull requests the
// forge holds open for work that is closed or for a branch its target already
// carries. It is given the requests earlier passes reported and reports the
// rest.
//
// It is satisfied by forgehygiene.Sweeper.
type RecurringForge interface {
	Notice(ctx context.Context, reported map[int]bool) ([]runstate.ForgeNotice, error)
}

// RecurringRole is a role's conversation as the harness reaches it: one message
// sent into it, and what came back.
//
// Nothing here decides anything on the role's behalf and nothing carries its
// decisions out. What comes back is read from what it actually said, so a firing
// answered in prose with no account reports exactly that.
//
// The model is the task's own selector, and empty where the task names none: the
// turn then asks for the role's configured model, as every firing did before a
// task could name one. It is this turn's alone — nothing about the conversation
// the role keeps changes, so the next message the operator sends into it asks
// for the role's model again.
//
// The pass names the firing the turn belongs to — the task and which of its
// firings this is — so what the turn records on the pass's behalf, a program
// manager's lane report first among it, says which pass wrote it.
type RecurringRole interface {
	Wake(ctx context.Context, role domain.AgentRole, pass, model, message string) (Turn, error)
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
	// Model is the model that served the turn: the one it asked for, or the
	// alternate that answered where the provider moved it. It is carried back so
	// the pass's record names what it actually ran on rather than what the
	// configuration hoped for.
	Model string `json:"model,omitempty"`
	// Result is the account the role gave of the pass, where it gave one.
	Result *sweep.Result `json:"result,omitempty"`
	// ResultProblem names an account that could not be read, or a turn that
	// carried none. It is not a failed turn: the role answered, and what is lost
	// is the structure rather than the work. It is also set beside a Result the
	// turn did carry, where something about how it was carried is worth the
	// record saying — a reply with more than one block, of which the last was
	// read — so a problem here does not by itself mean the turn's account is
	// missing.
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
	// Model is what the firing's turns ran on, and empty where no turn was taken.
	Model string `json:"model,omitempty"`
	// Findings and SilentRepairs are what the pass found and how many of its
	// fixes filed nothing for their root cause. They are counts here because this
	// is the line a session prints; the whole account is in the durable report.
	Findings      int `json:"findings"`
	SilentRepairs int `json:"silent_repairs,omitempty"`
	// PullRequests is how many of the findings are the harness's own reading of
	// the forge rather than the role's, so the line a session prints does not
	// credit the role with what the harness noticed.
	PullRequests int `json:"pull_requests,omitempty"`
	// Truncated marks a pass that still had more to do when its turn bound ran
	// out. It is the one thing a reader cannot infer from a short report, and
	// leaving it unsaid would make a bounded pass look like a finished one.
	Truncated bool `json:"truncated,omitempty"`
	// Summoned is what fired this pass out of its cadence, where something did.
	Summoned string `json:"summoned,omitempty"`
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

// Trigger fires the configured recurring tasks. It has no tracker and no
// worktree access, and it starts nothing: what it does is wake a role on a
// cadence and write down what the role said it did. The one reading it takes
// itself is of the forge, on the development manager's pass, and that reading
// changes nothing on the forge either.
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
	// Holds is the operator's pause over everything the harness would spend.
	// Optional, and a trigger wired without one is one nothing can pause, which
	// is what every provider invocation was before the switch existed.
	Holds OperatorHolds
	// Outages is the product's record of the provider answering nobody. Optional,
	// and read for one thing: a firing due while it stands is recorded with that
	// reason and asks the role nothing, so what a week of passes says about the
	// outage is the outage rather than a column of zero turns. A trigger wired
	// without one fires into the refusal and records the turn that failed, which
	// is what every firing did until the wait was named.
	Outages RecurringOutages
	// OutageProbe is how long a standing outage is left before a due firing is
	// made into it anyway to find out whether the provider answers. It is the
	// same interval a run and a watch probe on. A firing is the one probe this
	// path has: a served turn ends the outage for every surface, and a refused
	// one re-records it, so a machine with nothing in its backlog and no watch
	// running still finds the network back on its own. Zero fires into every
	// due firing, which is what a trigger did before the wait was named.
	OutageProbe time.Duration
	// Forge is the harness's own reading of the forge's open pull requests,
	// taken on every pass of a development manager's task. Optional: a trigger
	// wired without one records what the role said and reads the forge for
	// nothing, which is what every pass did until the requests were counted.
	Forge RecurringForge
	// Docket is the triage docket as the development manager reads it, read
	// afresh for every firing of a task of hers and carried in the message that
	// wakes her. Optional: a trigger wired without one wakes her with the task
	// alone, which is what every pass did until three of them in one afternoon
	// decided none of three approved changes waiting on her — the docket in her
	// context was the one rendered when her conversation opened, and a pass
	// resumes that conversation rather than opening it.
	Docket RecurringDocket
	// Breakage is where a missed cadence is said to somebody, as a report the
	// harness files itself. Optional: a trigger wired without one still records
	// the miss in the sweep log, and only the report is left unsaid.
	Breakage RecurringBreakage
	// Attribution is the product, repository, and build a miss report is filed
	// under. The role and the run it names are the miss's own.
	Attribution report.Attribution
	Clock       execution.Clock
}

// RecurringBreakage is the report pile as a missed cadence files into it. It is
// satisfied by *runstate.ReportStore.
type RecurringBreakage interface {
	Append(reported report.Report) error
}

// RecurringDue is when one enabled task is next due, as the claim that paces it
// says. At is zero for a task that has never fired, which is due at once.
type RecurringDue struct {
	Task  string
	Role  domain.AgentRole
	Every time.Duration
	At    time.Time
}

// RecurringMiss is a task that went a whole interval past the time it fell due
// without firing, and what kept it from firing.
type RecurringMiss struct {
	Task  string
	Role  domain.AgentRole
	Every time.Duration
	// Due is when the task fell due, and Why is what the session that noticed
	// the miss knows kept it: the harness itself, the operator's pause, or no
	// session running at all.
	Due time.Time
	Why string
	// Severity is how the miss is said. Empty records it in the sweep log and
	// says it to nobody, which is the operator's own pause: a stop somebody
	// placed on purpose is not breakage.
	Severity report.Severity
}

// RecurringCadence is the schedule read without firing it, and the miss
// recorded where it has gone unfired. It is asked by a watching session that is
// waiting on a run of its own, so the wait ends when a task falls due rather
// than when the run does; on 2026-09-13 a session waited on one run for twenty
// hours, and the development manager's hourly task fired nothing in all of them.
//
// It is satisfied by Trigger. A schedule that does not satisfy it is waited on
// as it always was: the session reaches it when a run ends.
type RecurringCadence interface {
	Cadence(ctx context.Context) ([]RecurringDue, error)
	Missed(ctx context.Context, missed RecurringMiss) error
}

// recurringFinder is a claim store that can be read without claiming. It is
// satisfied by runstate.SweepStore; a store that cannot is one whose cadence
// nothing reads ahead of firing.
type recurringFinder interface {
	Find(task string) (runstate.SweepClaim, bool, error)
}

// Cadence reports when each enabled task is next due. It claims nothing, so a
// session that reads it and then fires still meets the claim as the due check.
func (t Trigger) Cadence(context.Context) ([]RecurringDue, error) {
	finder, readable := t.Claims.(recurringFinder)
	if !readable {
		return nil, nil
	}
	var dues []RecurringDue
	var problems []error
	for _, name := range t.names() {
		task := t.Tasks[name]
		if !task.Enabled {
			continue
		}
		due := RecurringDue{Task: name, Role: task.Role, Every: task.Every.Duration()}
		claimed, found, err := finder.Find(name)
		if err != nil {
			problems = append(problems, fmt.Errorf("read when the recurring task %s is due: %w", name, err))
			continue
		}
		if found && !claimed.FiredAt.IsZero() {
			due.At = claimed.NextDue(due.Every)
		}
		dues = append(dues, due)
	}
	return dues, errors.Join(problems...)
}

// Missed records a missed cadence in the sweep log, where every other thing
// that became of the task is, and says it as a report where it has a severity.
//
// The record is a firing that took no turn and spans the time the task went
// unfired, so `yoyo sweeps` reads the gap in the place a reader looks for the
// passes. The cadence is not moved: the task is still due, and fires at the
// first pass that reaches it once what kept it clears.
func (t Trigger) Missed(ctx context.Context, missed RecurringMiss) error {
	if t.Reports == nil {
		return errors.New("recording a missed cadence requires the sweep log")
	}
	// A gap another session already recorded — the one that was running when it
	// opened, before a restart — is not recorded or said again. What marks it is
	// a record of the task that took no turn and starts when the task fell due,
	// which is the shape only a miss has: a firing starts when it was claimed.
	recorded, _, err := t.Reports.List()
	if err != nil {
		return fmt.Errorf("read whether the missed cadence of the recurring task %s is already recorded: %w", missed.Task, err)
	}
	for _, earlier := range recorded {
		if earlier.Task == missed.Task && earlier.Turns == 0 && earlier.Result == nil && earlier.StartedAt.Equal(missed.Due) {
			return nil
		}
	}
	now := t.now()
	late := now.Sub(missed.Due)
	problem := boundedProblem([]string{fmt.Sprintf(
		"the recurring task %s, due every %s, fell due at %s and had not fired %s later, so its %s's standing look was not taken: %s; nothing was asked, and it fires at the first pass that reaches it once that clears",
		missed.Task, missed.Every, missed.Due.UTC().Format(time.RFC3339), late.Round(time.Minute), missed.Role, missed.Why)})
	var problems []error
	if err := t.Reports.Append(runstate.Sweep{
		Task:      missed.Task,
		Role:      missed.Role,
		StartedAt: missed.Due,
		EndedAt:   now,
		Problem:   problem,
	}); err != nil {
		problems = append(problems, fmt.Errorf("record the missed cadence of the recurring task %s: %w", missed.Task, err))
	}
	if missed.Severity != "" && t.Breakage != nil {
		if err := t.reportMiss(missed, now); err != nil {
			problems = append(problems, fmt.Errorf("report the missed cadence of the recurring task %s: %w", missed.Task, err))
		}
	}
	return errors.Join(problems...)
}

// reportMiss files the miss as the harness's own report. It names the task and
// the time it fell due as the run it came out of, because there is no run: a
// firing that never happened is what the report is about.
func (t Trigger) reportMiss(missed RecurringMiss, now time.Time) error {
	attribution := t.Attribution
	attribution.Role = report.HarnessReporter
	attribution.Agent = ""
	attribution.RunID = fmt.Sprintf("%s@%s", missed.Task, missed.Due.UTC().Format(time.RFC3339))
	attribution.WorkItemID = ""
	collected, err := report.Collect([]report.Entry{{
		Severity: missed.Severity,
		Message:  missedReportMessage(missed, now),
	}}, attribution, now)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := t.Breakage.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

// missedReportMessage is the two sentences a missed cadence is said in: what
// has not fired and what kept it, and what that costs while it stands.
func missedReportMessage(missed RecurringMiss, now time.Time) string {
	why := strings.Join(strings.Fields(missed.Why), " ")
	if limit := report.MaxMessageBytes / 2; len(why) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(why[cut]) {
			cut--
		}
		why = why[:cut] + " […]"
	}
	return fmt.Sprintf("The recurring task %s has not fired since it fell due at %s, %s ago: %s. "+
		"The %s's standing look is not being taken while it stands, and the task fires on its own at the first pass that reaches it once that clears.",
		missed.Task, missed.Due.UTC().Format(time.RFC3339), now.Sub(missed.Due).Round(time.Minute), why, missed.Role)
}

// RecurringDocket is the triage docket rendered as the development manager's
// conversation renders it: the live entries, one per stopped run, oldest first,
// with what the window could not show counted. A docket that could not be read
// renders as saying so rather than as empty, because a pass told nothing has
// stopped when nothing could be read would decide nothing on a false reading.
type RecurringDocket interface {
	Window() string
}

// RecurringOutages is the outage record as a firing reads it. It is satisfied
// by *runstate.ProviderOutageStore.
type RecurringOutages interface {
	Standing() (runstate.ProviderOutage, bool, error)
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
	// Whether the provider is answering anybody is read once, before any task is
	// claimed. A firing made into a login nobody has renewed spends a claim on a
	// turn that cannot be served and records a turn that failed, which over three
	// days reads as a schedule that is broken rather than a provider that is away.
	outage, away, err := t.providerAway()
	if err != nil {
		problems = append(problems, err)
	}
	for _, name := range t.names() {
		task := t.Tasks[name]
		if !task.Enabled {
			continue
		}
		// The claim is the due check. Asking first and claiming after would be two
		// reads and a write with a window between them, which is exactly the window
		// two concurrent sessions land in.
		claimed, err := t.Claims.Claim(ctx, name, task.Every.Duration(), t.now())
		if err != nil {
			// A task that is not due is the ordinary answer on almost every pull, and
			// so is one another process claimed a moment ago. Neither is this pass's
			// to report.
			if errors.Is(err, runstate.ErrSweepNotDue) {
				continue
			}
			problems = append(problems, fmt.Errorf("claim the firing of the recurring task %s: %w", name, err))
			continue
		}
		if away {
			fired := t.refuse(ctx, name, task, outage)
			return RecurringSweep{Fired: []Fired{fired}}, errors.Join(problems...)
		}
		fired := t.run(ctx, name, passName(claimed), task, wakeMessage(name, task, t.docketFor(task)), "")
		return RecurringSweep{Fired: []Fired{fired}}, errors.Join(problems...)
	}
	return RecurringSweep{}, errors.Join(problems...)
}

// passName is how a firing is named where a turn records it wrote something on
// the firing's behalf: the task, and which of its firings this is. The count is
// the claim's own, so two sessions polling one schedule cannot name two firings
// alike.
func passName(claimed runstate.SweepClaim) string {
	return fmt.Sprintf("%s#%d", claimed.Task, claimed.Firings)
}

// BrakeSummons is what the intake brake puts in front of the development
// manager when it summons her: the hold as the brake placed it, with the runs
// that tripped it on its own record.
type BrakeSummons struct {
	Hold runstate.IntakeHold
}

// ErrNoSummonableTask reports a configuration that schedules no enabled task
// for the development manager, so there is no sweep of hers to summon. The
// brake records it on the hold and the cooldown probes the line regardless: a
// summons that cannot be made is not a hold that waits.
var ErrNoSummonableTask = errors.New("no enabled recurring task wakes the development manager, so there is no sweep to summon")

// Summon fires the development manager's sweep now, out of its cadence, with
// the brake's trip in front of her. It is the delivery the brake was missing:
// until it existed the trip reached her at her next scheduled pass, an hour
// away at most and with nothing in the wake saying the line had stopped.
//
// Everything about the firing is the cadence's own — the same claim, the same
// role conversation, the same turn bound, the same durable report — with two
// differences. The claim is made whether or not the task is due, and the wake
// message carries the trip: which runs blocked and why, what she may decide,
// and when the brake probes the line by itself if she decides nothing. The
// entries ride the wake rather than the conversation's opening briefing,
// because the briefing is assembled when the conversation opens and what she
// has to look at is what arrived with the message.
//
// A provider answering nobody refuses it exactly as it refuses a scheduled
// firing, and for the same reason: a summons made into a login nobody renewed
// asks her nothing and spends a claim doing it. The pause covers it as it
// covers every turn.
func (t Trigger) Summon(ctx context.Context, summons BrakeSummons) (Fired, error) {
	if err := t.validate(); err != nil {
		return Fired{}, err
	}
	if _, held, err := t.paused(); err != nil {
		return Fired{}, err
	} else if held {
		return Fired{}, errors.New("the operator has paused harness activity, so the development manager was not summoned")
	}
	name, task, found := t.developmentManagerTask()
	if !found {
		return Fired{}, ErrNoSummonableTask
	}
	outage, away, err := t.providerAway()
	if err != nil {
		return Fired{}, err
	}
	if away {
		return Fired{}, fmt.Errorf("the provider is answering nobody, so the development manager was not summoned: %s", outage.Says())
	}
	claimed, err := t.Claims.Summon(ctx, name, t.now())
	if err != nil {
		return Fired{}, fmt.Errorf("claim the summoned firing of the recurring task %s: %w", name, err)
	}
	summoned := summonedBy(summons.Hold)
	fired := t.run(ctx, name, passName(claimed), task, summonsMessage(name, task, summons.Hold, t.docketFor(task)), summoned)
	return fired, nil
}

// docketFor is the docket a firing of this task carries: the development
// manager's, read now, and nothing for any other role. It is read once per
// firing rather than once per turn, because the turns after the first continue
// the same pass over what the first was shown.
func (t Trigger) docketFor(task config.RecurringTask) string {
	if t.Docket == nil || task.Role != domain.RoleDevelopmentManager {
		return ""
	}
	return t.Docket.Window()
}

// developmentManagerTask is the first enabled task, in name order, that wakes
// the development manager. A project that schedules two of them gets the first
// summoned, which is the same choice the cadence makes about which fires first.
func (t Trigger) developmentManagerTask() (string, config.RecurringTask, bool) {
	for _, name := range t.names() {
		task := t.Tasks[name]
		if task.Enabled && task.Role == domain.RoleDevelopmentManager {
			return name, task, true
		}
	}
	return "", config.RecurringTask{}, false
}

// summonedBy is what the sweep record says summoned it, bounded to what the
// record accepts.
func summonedBy(hold runstate.IntakeHold) string {
	said := "the intake brake, after " + strings.TrimSpace(hold.Reason)
	if hold.Brake != nil && hold.Brake.Probe != nil && hold.Brake.Probe.Blocked {
		said = fmt.Sprintf("the intake brake, after its probe run of %s blocked", hold.Brake.Probe.WorkItemID)
	}
	return boundedProblem([]string{said})
}

// providerAway reads whether the provider is answering nobody, and reports the
// outage only while it is too fresh to probe: once the probe interval has
// passed since the provider was last met refusing, the firing is made into it,
// because the firing is the only thing on this path that can find out whether
// the provider answers. A record that cannot be read is reported beside the
// pass and the firing is made: a schedule that stopped firing because it could
// not open one file would be a worse failure than a turn spent into a refusal.
func (t Trigger) providerAway() (runstate.ProviderOutage, bool, error) {
	if t.Outages == nil {
		return runstate.ProviderOutage{}, false, nil
	}
	outage, standing, err := t.Outages.Standing()
	if err != nil {
		return runstate.ProviderOutage{}, false, fmt.Errorf("read whether the provider is answering before firing: %w", err)
	}
	if !standing || !t.now().Before(outage.LastSeen.Add(t.OutageProbe)) {
		return runstate.ProviderOutage{}, false, nil
	}
	return outage, true, nil
}

// refuse records a firing the provider could not have served: the cadence is
// moved exactly as a firing that failed moves it, no turn is taken, and what
// the record says is the wait rather than a turn count of zero. The next
// firing due once the probe interval has passed is made, and finds out.
func (t Trigger) refuse(ctx context.Context, name string, task config.RecurringTask, outage runstate.ProviderOutage) Fired {
	fired := Fired{Task: name, Role: task.Role}
	recorded := runstate.Sweep{
		Task:      name,
		Role:      task.Role,
		StartedAt: t.now(),
	}
	recorded.EndedAt = recorded.StartedAt
	problems := []string{fmt.Sprintf(
		"the recurring task %s was not put to the %s: %s; nothing was asked, and its next firing is at its next cadence",
		name, task.Role, outage.Says())}
	// The forge is not the provider, so a pass the provider could not serve still
	// reads it: a request held open for nothing is not made less so by an outage.
	problems = append(problems, t.noticeForge(ctx, task, &recorded))
	recorded.Problem = boundedProblem(problems)
	fired.Problem = recorded.Problem
	if recorded.Result != nil {
		fired.Findings = len(recorded.Result.Findings)
	}
	fired.PullRequests = len(recorded.PullRequests)
	t.settle(ctx, &fired, recorded)
	return fired
}

// run takes one firing's turns and records what they came to. It never returns
// an error: a firing that failed is a fact about the schedule that belongs in the
// record and beside the pass, rather than something that stops the pull.
func (t Trigger) run(ctx context.Context, name, pass string, task config.RecurringTask, message, summoned string) Fired {
	fired := Fired{Task: name, Role: task.Role, Summoned: summoned}
	recorded := runstate.Sweep{
		Task:      name,
		Role:      task.Role,
		StartedAt: t.now(),
		Summoned:  summoned,
	}
	var merged *sweep.Result
	var problems []string
	for turn := 0; turn < task.Turns(); turn++ {
		answered, err := t.Roles.Wake(ctx, task.Role, pass, task.ModelSelector(), message)
		// What the turn cost is carried whichever way it went, because the provider
		// charges for a turn that failed exactly as for one that answered — and so
		// is the model it cost that on, which is what the spend is attributed to.
		fired.CostUSD += answered.CostUSD
		recorded.CostUSD += answered.CostUSD
		if model := strings.TrimSpace(answered.Model); model != "" && len(model) <= runstate.MaxSweepModelBytes {
			recorded.Model = model
			fired.Model = model
		}
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
	// The harness's own reading of the forge joins the account after the role's
	// turns, so what the role said is intact and what the harness noticed is
	// stated beside it.
	problems = append(problems, t.noticeForge(ctx, task, &recorded))
	// Bounded, because the record's own bound on this prose refuses a record that
	// carries too much of it — and every one of these sentences ends with a
	// provider's error message, whose length nothing here controls. Losing a whole
	// pass's report because the description of a smaller failure ran long is the
	// exact trade this must not make.
	recorded.Problem = boundedProblem(problems)
	fired.Problem = recorded.Problem
	if recorded.Result != nil {
		fired.Findings = len(recorded.Result.Findings)
		fired.SilentRepairs = recorded.Result.SilentRepairs()
	}
	fired.PullRequests = len(recorded.PullRequests)
	t.settle(ctx, &fired, recorded)
	return fired
}

// noticeForge adds the harness's own reading of the forge to a development
// manager's pass: every open pull request whose work item is closed or whose
// branch its target already carries, stated as a finding and recorded by number
// so no later pass says it again. It reports what stopped the reading, or
// nothing, as a problem for the record; it never fails the firing, because the
// role's account is already in hand and a forge that could not be read must not
// cost it.
//
// A task of any other role is left alone: the forge is the development
// manager's domain, and the reading is taken on its pass whether or not the
// role itself was reached — the forge is not the provider.
func (t Trigger) noticeForge(ctx context.Context, task config.RecurringTask, recorded *runstate.Sweep) string {
	if t.Forge == nil || task.Role != domain.RoleDevelopmentManager {
		return ""
	}
	reported, err := t.reportedRequests()
	if err != nil {
		// Without the earlier passes there is no saying which requests were already
		// reported, and reporting them all again every hour is the thing this
		// exists to not do; the reading waits for a pass that can read the log.
		return fmt.Sprintf("the forge was not read on this pass because the earlier passes' reports could not be read: %v", err)
	}
	notices, err := t.Forge.Notice(ctx, reported)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("the forge could not be fully read on this pass: %v", err))
	}
	if len(notices) == 0 {
		return strings.Join(problems, "; ")
	}
	if recorded.Result == nil {
		// The role gave no account, and the notices still have to be stated
		// somewhere a reader looks. The account is the harness's then, and says so;
		// the problem beside it still says the role's own account is missing.
		recorded.Result = &sweep.Result{
			Status:  sweep.StatusComplete,
			Summary: fmt.Sprintf("No account of this pass came from the %s; the findings are the harness's own reading of the forge.", task.Role),
		}
	}
	// What fits is bounded by the account's own cap. The requests that do not fit
	// are not recorded as reported, so the next pass states them; a shortened list
	// that said nothing would leave them reported nowhere.
	room := sweep.MaxPassFindings - len(recorded.Result.Findings)
	if room < 0 {
		room = 0
	}
	kept := notices
	if len(kept) > room {
		kept = kept[:room]
		problems = append(problems, fmt.Sprintf(
			"%d open pull request(s) noticed on the forge are not listed because the pass's account is at its bound of %d findings; they are stated on the next pass",
			len(notices)-room, sweep.MaxPassFindings))
	}
	for _, noticed := range kept {
		recorded.Result.Findings = append(recorded.Result.Findings, noticed.Finding())
	}
	recorded.PullRequests = append(recorded.PullRequests, kept...)
	return strings.Join(problems, "; ")
}

// reportedRequests reads which pull requests the earlier passes reported, by
// number, from the durable reports themselves. The reports are the record of
// what was said, so they are what decides what has been; a second record of
// the same fact could come to disagree with the first. It is the harness's
// form of the check every role is told to make before filing — against what
// is already recorded, rather than against what it remembers.
//
// It reads the whole log, once per firing of the development manager's task,
// and that is a cost that grows with the log: the reader decodes every record
// to find the ones carrying requests, and nothing marks which those are from
// outside. It is accepted on two facts. The log gains one record per firing —
// an hourly task writes under nine thousand a year — and `yoyo sweeps --json`
// already reads all of it on demand. What the reading assumes is that the log
// is never pruned or rotated: nothing here does either, and a log cut back
// would forget the requests its lost records reported, so every one of those
// still open would be stated once more on the next pass. Once more and not
// hourly — the pass that restates them records them again.
//
// A line of the log that would not decode is set aside by the reader and
// carries nothing here, so a request that pass reported may be reported once
// more; that is one repeat for one torn write, and the alternative — reading
// nothing when one line is torn — would repeat every request instead.
func (t Trigger) reportedRequests() (map[int]bool, error) {
	recorded, _, err := t.Reports.List()
	if err != nil {
		return nil, err
	}
	reported := map[int]bool{}
	for _, entry := range recorded {
		for _, noticed := range entry.PullRequests {
			reported[noticed.Number] = true
		}
	}
	return reported, nil
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
	// The requests the account stated go with it, so a later pass states them
	// again rather than finding them reported in a record that shows nothing.
	reduced.PullRequests = nil
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
	const marker = " […]"
	cut := runstate.MaxSweepTextBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(joined[cut]) {
		cut--
	}
	return strings.TrimSpace(joined[:cut]) + marker
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
//
// A development manager's pass carries the docket too, between the preamble and
// the task, because a pass resumes her conversation and the docket in it is the
// one rendered when it opened. Stoppages otherwise reach her one per delivery,
// so a pass between deliveries saw none of them: on 2026-09-25 three passes ran
// after an approved change stopped at integration, and decided nothing about it
// or the two that stopped after it.
func wakeMessage(name string, task config.RecurringTask, docket string) string {
	lines := []string{
		fmt.Sprintf("The harness woke you for the recurring task %q, which runs every %s. Nobody is waiting at a terminal for this: what you produce is recorded and read later.", name, task.Every),
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra, and nothing about being woken on a schedule widens what you may decide or change.",
		"Before you file anything, check it against the work already admitted. A duplicate admission costs a whole run and the reviews after it, and a task that runs on a cadence files the same duplicate on every cadence.",
	}
	lines = append(lines, docketLines(docket)...)
	lines = append(lines,
		"",
		strings.TrimSpace(task.Prompt),
		"",
		sweep.Contract(),
	)
	return strings.Join(lines, "\n")
}

// docketLines is the docket a pass carries, introduced as what it is: read for
// this pass, and standing in for the one the conversation opened with. Nothing
// is added where the firing carries no docket.
func docketLines(docket string) []string {
	docket = strings.TrimSpace(docket)
	if docket == "" {
		return nil
	}
	return []string{
		"",
		"The triage docket below was read for this pass, and is the docket as it stands now rather than the one your conversation opened with. Every entry on it is a stoppage nobody has decided about, whether or not it was ever delivered to you on its own; decide what you can on this pass.",
		"",
		docket,
	}
}

// summonsMessage is what the harness says when the intake brake summons the
// development manager out of her cadence. It is the ordinary wake — the same
// preamble, the same task, the same contract — with the trip in front of it,
// because a summoned pass is her sweep with one thing added rather than a
// different conversation: what stopped the line, what she may decide about the
// hold, and what the brake does if she decides nothing.
//
// The entries are in the message rather than left to the conversation's opening
// briefing, deliberately. The briefing is assembled when her conversation opens
// and lists the docket as it stands; the runs that tripped the brake are on it
// too, among everything else, and a summons that pointed at the docket would be
// asking her to find the three entries this turn is about. The docket as it
// stands now follows the trip, as on every pass of hers, for everything else
// that is waiting on her.
func summonsMessage(name string, task config.RecurringTask, hold runstate.IntakeHold, docket string) string {
	lines := []string{
		fmt.Sprintf("The intake brake summoned you now, ahead of the cadence of %q: %s, and intake is held since %s. Nobody is waiting at a terminal for this: what you produce is recorded and read later.",
			name, strings.TrimSpace(hold.Reason), hold.HeldAt.UTC().Format(time.RFC3339)),
		"Your authority here is exactly the authority your role already holds — this turn grants you nothing extra.",
		"",
		"What tripped it, in the order the runs blocked:",
	}
	if hold.Brake != nil {
		for _, entry := range hold.Brake.Entries() {
			lines = append(lines, "- "+entry)
		}
		if probe := hold.Brake.Probe; probe != nil && probe.Blocked {
			run := "a probe run"
			if strings.TrimSpace(probe.RunID) != "" {
				run = "probe run " + probe.RunID
			}
			lines = append(lines, fmt.Sprintf("- and since then %s of %s, started under the hold to find out whether the line is fine, blocked as well: %s",
				run, probe.WorkItemID, strings.TrimSpace(probe.Reason)))
		}
	}
	cooldown := "the configured cooldown"
	if hold.Brake != nil {
		cooldown = hold.Brake.CooldownEndsAt.UTC().Format(time.RFC3339)
	}
	lines = append(lines,
		"",
		"Decide what happens to the hold, and record it as a brake decision in your tracker block: \"release\" if the line is fine or what stopped it is dealt with, so the harness chooses work again now; \"probe\" to keep the hold and have one probe run start now, which reopens intake if it lands and keeps it held if it blocks; or \"escalate\" to keep the hold for the operator, which is the only decision of yours under which it waits on a person — say why in the reason and report it at warning severity, so it reaches them.",
		"Triage the runs themselves as their docket entries warrant — repair, re-run, re-scope, or escalate each — exactly as you would on any pass; a decision about a run does not decide the hold, and a decision about the hold does not decide a run.",
		fmt.Sprintf("If you record no brake decision, a probe run starts by itself at %s, and the hold is released or kept on what becomes of it.", cooldown),
	)
	// Where the loop stands is said to her because she is the one who can end
	// it early: a summons that named neither the cycle nor the bound would ask
	// her to decide the same question every cooldown without telling her that
	// the harness will stop asking.
	if hold.Brake != nil && hold.Brake.Loop() != "" {
		lines = append(lines, fmt.Sprintf("This is %s; once it does, no further probe starts and the hold waits on the operator, so escalate it yourself sooner if that is where it belongs.", hold.Brake.Loop()))
	}
	lines = append(lines, docketLines(docket)...)
	lines = append(lines,
		"",
		strings.TrimSpace(task.Prompt),
		"",
		sweep.Contract(),
	)
	return strings.Join(lines, "\n")
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
		if fired.Summoned != "" {
			fmt.Fprintf(&rendered, "  summoned ahead of its schedule by %s\n", fired.Summoned)
		}
		if fired.SilentRepairs > 0 {
			fmt.Fprintf(&rendered, "  %d of its fixes filed nothing for their root cause\n", fired.SilentRepairs)
		}
		if fired.PullRequests > 0 {
			fmt.Fprintf(&rendered, "  %d of the findings are open pull requests the harness noticed on the forge, held open for work that is over\n", fired.PullRequests)
		}
		if fired.Problem != "" {
			fmt.Fprintf(&rendered, "  %s\n", fired.Problem)
		}
	}
	return rendered.String()
}
