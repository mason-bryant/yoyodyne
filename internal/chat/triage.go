package chat

// What the development manager decides about work that has stopped moving.
//
// The docket is delivered into this role's conversation because deciding is
// this role's, and until now that was the whole of the loop: an entry arrived,
// a decision was reasoned out in prose, and the prose went wherever the
// conversation went. Nothing carried the decision to the item it was about, so
// the next reader of a stopped run — a later conversation, a later operator —
// found the same evidence and none of the reasoning, and decided it again.
//
// So a decision is a recorded act rather than a paragraph. It names the
// stoppage it settles, it lands on the work item where anybody who reads the
// item finds it, and the three decisions that buy another attempt spend the
// item's durable triage budget as they are recorded. That last part is what
// makes the guards real: a repair grant, a re-run, and a re-arm each go through
// the same gate the counters already enforce, so an item nothing was ever going
// to stop is stopped by the cap rather than by whoever happens to be reading.
//
// One decision moves a cap rather than spending one. Every override recorded in
// the week to 2026-09-06 was a cap refusing this role, an escalation, and an
// operator granting it within minutes under their own standing direction — so
// the operator step was latency rather than judgement, and the crossing is this
// role's now. It is narrow on purpose: one step, five times per item, and only
// with the argument for it, which lands on the item and reaches the operator in
// the channel as the crossing happens. That is a veto by reading rather than a
// permission to ask for. Past the five, and for anything larger than a step, the
// caps are the operator's again and the refusal says so.
//
// Escalation is the one decision that reaches the operator, and it is
// deliberately more than prose: a durable blocker on the item, so the item
// itself says it is waiting on a person, and a report at warning severity or
// above, so it reaches the pile the operator reads. A conversation that only
// said "somebody should look at this" is how the four hand surgeries this
// workflow exists to replace were found in the first place — late, and by
// accident.
//
// What the harness does not do is carry the decision out. Nothing here starts a
// run, hands a developer a grant, or asks a forge for anything: causing work is
// the harness's own hand on the operator's instruction, and this is a role
// deciding. The record and the budget are what a later hand acts on, and there
// are two of those, opposite to each other: `yoyo triage rerun` starts the item
// over and records this decision as why the fresh run exists, and `yoyo triage
// repair` re-enters the stopped run's own repair loop on the grant recorded
// here. Both read the intake hold and prove the stoppage is over first.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The decisions triage may record. They are the two decision trees the
// development manager works through — one for a run that stopped, one for a
// publication that did not finish — written as a closed vocabulary, because the
// harness acts on three of them and cannot act on prose.
const (
	// decisionRepair hands the item another bounded go at the change it already
	// has, on the branch and worktree the stopped run preserved.
	decisionRepair = "repair"
	// decisionRerun runs the item again from the start, which is what a correct
	// change whose ground moved needs.
	decisionRerun = "rerun"
	// decisionRescope splits what was out of scope into a child of the item. The
	// parent's own criteria are not narrowed here: they are the product
	// manager's, and what this records is the decision plus the argument for
	// narrowing them.
	decisionRescope = "rescope"
	// decisionRearm repeats an authorized merge request the forge dropped for a
	// transient cause.
	decisionRearm = "rearm"
	// decisionWait is the decision that nothing is to be done yet: the forge
	// still has the merge, and waiting is what it needs. It is recorded rather
	// than left unsaid so the next reader knows somebody looked.
	decisionWait = "wait"
	// decisionEscalate hands the entry to the operator, which is the only
	// decision that asks a person for anything.
	decisionEscalate = "escalate"
	// decisionCross raises one of the item's caps by a single step on this role's
	// own delegated authority, so a decision the cap refused becomes one that can
	// be recorded. It buys no attempt and spends no budget of its own; what it
	// spends is one of the five crossings the item gets, and the reason it carries
	// is reported to the operator as it is recorded.
	decisionCross = "cross"
)

// triageDecisions lists the vocabulary in the order the contract states it, so a
// refusal names exactly what was available.
var triageDecisions = []string{
	decisionRepair, decisionRerun, decisionRescope, decisionRearm, decisionWait, decisionEscalate, decisionCross,
}

// triageVerbs is what each decision records about itself on the work item. The
// item's notes are read by people and by later conversations rather than by
// this package, so what lands there is a sentence rather than the vocabulary
// word that produced it.
var triageVerbs = map[string]string{
	decisionRepair:   "Triaged: handed back for one bounded repair of the change it already has",
	decisionRerun:    "Triaged: to be run again from the start",
	decisionRescope:  "Triaged: re-scoped, with what was out of scope split out",
	decisionRearm:    "Triaged: its dropped merge to be re-armed once",
	decisionWait:     "Triaged: waiting, because the forge still has it",
	decisionEscalate: "Escalated to the operator by triage",
	// The crossing's own sentence is built where it is recorded rather than taken
	// from here, because which cap was crossed and which of the five crossings this
	// was are the whole of what makes the note answerable. This is the fallback
	// nothing writes and the entry the vocabulary check reads.
	decisionCross: "Triaged: one of its caps crossed on the development manager's own authority",
}

// TriageBudgets is what one work item has already been given and may still be
// given. It is the durable per-item record every triage action goes through,
// satisfied by a caller that supplies the configured caps alongside the store:
// what an item may spend is a decision an operator wrote down, and a
// conversation is not the place that invents one.
//
// A conversation without it can still decide anything that spends nothing.
// What it cannot do is grant, re-run, or re-arm, because a budget that cannot be
// read must never be spent through as though it were empty.
type TriageBudgets interface {
	// GrantRepair records a repair grant and reports what it came to, truncated
	// to the review rounds the item's cap still has room for.
	GrantRepair(ctx context.Context, workItemID string) (runstate.RepairGrant, error)
	// RecordRerun records that triage caused this item to be run again.
	RecordRerun(ctx context.Context, workItemID string) (runstate.TriageCounters, error)
	// RecordMergeRearm records that triage re-armed a merge the forge dropped.
	RecordMergeRearm(ctx context.Context, workItemID string) (runstate.TriageCounters, error)
	// CrossCap raises one of this item's caps by a single step on the development
	// manager's own delegated authority, and reports what the crossing came to.
	// Whose authority it is recorded under is the caller's rather than this
	// conversation's, exactly as the sizes and the clock are: a conversation that
	// could name the role could name any of them.
	CrossCap(ctx context.Context, workItemID, budget, reason string) (runstate.TriageCrossing, error)
}

// Stoppages is what the harness durably recorded about the runs triage decides
// about. Two things are read from it: the work item a run was made for, and
// where a work item's own change actually is.
//
// A decision names two things that have to agree — the item it lands on, and
// the run whose stoppage it settles — and a conversation working a docket of
// several entries is exactly where they come apart. Two entries transposed
// write each decision's reasoning onto the other item, and both then read as
// decided, which is worse than either reading as undecided: the reasoning is
// about a change nobody looking at that item can see.
//
// The second is the same records read for the other half of deciding about a
// stoppage, which is what gets carved out of it: a child written against a
// change that is still on a preserved branch has no substrate, and nothing in
// the tracker has ever known where a change is. See substrate.go.
//
// It is optional like the rest, and a conversation without one records a
// decision unchecked rather than appearing to have checked it, and decomposes
// without the substrate gate rather than appearing to have applied it.
type Stoppages interface {
	// WorkItemOf reports the work item the named run was made for.
	WorkItemOf(ctx context.Context, runID string) (string, error)
	// UnlandedChange reports the change one work item's own runs made that never
	// reached the integration target, and whether there is one at all. Work the
	// harness never ran, and work whose change is on the target branch, both
	// report none: neither leaves a child of it standing on anything missing.
	UnlandedChange(ctx context.Context, workItemID string) (UnlandedChange, bool, error)
}

// EscalationError reports an escalation that carried no report. It is the
// harness refusing rather than the provider failing, exactly as an authority
// refusal is: nothing in the block was carried out, the item was not blocked,
// and the prose the role wrote is still the operator's to read.
type EscalationError struct {
	WorkItemID string
}

func (e *EscalationError) Error() string {
	return fmt.Sprintf(
		"the development manager escalated %s without reporting it at %q severity or above; nothing was carried out, because an escalation the operator never sees is not an escalation",
		e.WorkItemID, report.SeverityWarning)
}

// triageProblems checks a triage decision as far as one action can be checked:
// it names a decision from the vocabulary, and it names the run whose stoppage
// it settles. Whether that run is on the docket needs the docket rather than the
// action, and is deliberately not asked: the entry the decision is about may
// have been cut from a bounded listing, and refusing a decision for that would
// refuse exactly the oldest stoppages nobody has got to yet. Whether the run is
// this item's own stopped work is asked, but where the run record can be read
// rather than here — see refuseTransposedStoppage.
func (a TrackerAction) triageProblems() []error {
	var problems []error
	switch decision := strings.TrimSpace(a.Decision); {
	case decision == "":
		problems = append(problems, fmt.Errorf("triage requires \"decision\", one of %s", strings.Join(triageDecisions, ", ")))
	case triageVerbs[decision] == "":
		problems = append(problems, fmt.Errorf("triage decision %q is not a decision; the decisions are %s",
			decision, strings.Join(triageDecisions, ", ")))
	}
	switch run := strings.TrimSpace(a.Run); {
	case run == "":
		problems = append(problems, errors.New("triage requires \"run\", the run the docket entry names"))
	case !runstate.ValidRunID(run):
		problems = append(problems, fmt.Errorf("triage run %q is not a run identifier; a docket entry names the run it is about", run))
	}
	problems = append(problems, a.crossingProblems()...)
	return problems
}

// crossingProblems holds the one decision that names a budget to the budget
// vocabulary, and holds every other decision to naming none.
//
// The budget is required rather than inferred from whatever last refused, because
// two of the three decisions that spend a budget stand behind two of them: a
// crossing that guessed would raise one cap while the other went on refusing the
// same decision, which is the two-sittings-per-item failure the refusals were
// already reworded to end.
//
// The names are the store's own rather than a second spelling of them here. A
// refusal prints the list, so what a development manager types is the words the
// refusal used.
func (a TrackerAction) crossingProblems() []error {
	var problems []error
	budget := strings.TrimSpace(a.Budget)
	if strings.TrimSpace(a.Decision) != decisionCross {
		if budget != "" {
			problems = append(problems, fmt.Errorf(
				"only the %q decision names a \"budget\", and this one is %q; a cap is crossed by crossing it rather than as an argument to another decision",
				decisionCross, strings.TrimSpace(a.Decision)))
		}
		return problems
	}
	switch {
	case budget == "":
		problems = append(problems, fmt.Errorf("triage %q requires \"budget\", the cap being crossed: %s",
			decisionCross, strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	case !slices.Contains(runstate.TriageOverrideBudgets(), budget):
		problems = append(problems, fmt.Errorf("triage budget %q is not a cap; the caps are %s",
			budget, strings.Join(runstate.TriageOverrideBudgets(), ", ")))
	}
	// The reason is required on every action that changes something, and it is
	// required again here in the crossing's own words. What makes this delegation
	// answerable is that the argument reaches the operator at the moment the cap is
	// crossed, so a crossing that carried none would be the one thing the operator
	// agreed to on condition it could not happen.
	if strings.TrimSpace(a.Reason) == "" {
		problems = append(problems, fmt.Errorf(
			"triage %q requires \"reason\", the justification for crossing the cap: it is recorded on the item and reported to the operator as the crossing happens, and a crossing nobody argued for is refused outright",
			decisionCross))
	}
	return problems
}

// TriageDecision reports what this reply recorded about the stoppage of one
// run: the decision, in the vocabulary above, and the reasoning recorded with
// it. It reports nothing for a turn that decided nothing about that stoppage,
// which is an answer rather than a failure — a development manager who read a
// stoppage and left it alone has answered.
//
// It is exported because the harness now delivers a stoppage into this
// conversation rather than waiting for somebody to carry it here, and what it
// has to write down afterwards is what came back. Reading the recorded actions
// is the only honest way to know: the prose of a reply can say anything, and
// what a decision is worth is that it was carried out against the item's durable
// triage budget. Only applied actions are read, so nothing here can report a
// decision the tracker refused.
func (r Reply) TriageDecision(runID string) (decision, reason string, found bool) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return "", "", false
	}
	for _, outcome := range r.Actions {
		action := outcome.Action
		if !outcome.Applied || action.Action != actionTriage || strings.TrimSpace(action.Run) != run {
			continue
		}
		// A crossing is not what became of the stoppage. It raises a cap so that a
		// decision can be recorded, and the decision is a separate act in the same
		// reply or a later one — so reporting it here would say a stoppage had been
		// settled by the step taken before settling it. A turn that crossed and
		// decided nothing else reports nothing, which is the honest answer: the item
		// has more room and still has nothing decided about it.
		if strings.TrimSpace(action.Decision) == decisionCross {
			continue
		}
		return strings.TrimSpace(action.Decision), strings.TrimSpace(action.Reason), true
	}
	return "", "", false
}

// escalates reports a decision that asks a person for something, which is the
// one decision the harness holds to a further condition than the action itself
// carries.
func (a TrackerAction) escalates() bool {
	return a.Action == actionTriage && strings.TrimSpace(a.Decision) == decisionEscalate
}

// refuseUnreportedEscalation refuses an escalation the operator would never see.
// An escalation is a durable blocker and a report, and the report half cannot be
// checked where the action is validated, because it is in a different block of
// the same reply.
//
// The severity floor is warning rather than note for the reason the report
// vocabulary draws that line: a note asks for nothing, and an escalation asks
// for a person.
func refuseUnreportedEscalation(parsed parsedReply) error {
	// One escalating reply may cover several docket entries, and each blocked
	// item needs its own account: a single report satisfying every escalation
	// would leave the rest blocked with nothing reaching the operator about
	// them. So the count of warning-or-above reports must cover the count of
	// escalations, and the refusal names every item that would go unaccounted.
	var escalated []string
	for _, action := range parsed.Actions {
		if action.escalates() {
			escalated = append(escalated, strings.TrimSpace(action.ID))
		}
	}
	if len(escalated) == 0 {
		return nil
	}
	reported := 0
	for _, entry := range parsed.Reports {
		if entry.Severity == report.SeverityWarning || entry.Severity == report.SeverityCritical {
			reported++
		}
	}
	if reported >= len(escalated) {
		return nil
	}
	return &EscalationError{WorkItemID: strings.Join(escalated[reported:], ", ")}
}

// carryOutTriage records one triage decision. Whether the run it names is this
// item's stopped work is asked first, because a decision landing on the wrong
// item is a decision that should never have been paid for; the budget is then
// spent before the decision is written down, which is the order the durable
// counters are built for: a process that dies between the two has spent an
// attempt nobody took, rather than recorded a decision nothing bounds.
//
// A refusal from the budget is the gate doing its job rather than a failure of
// the conversation: the development manager is told which cap refused it and
// what it has left, which is exactly the evidence for escalating instead.
func (s *Session) carryOutTriage(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	decision := strings.TrimSpace(action.Decision)
	run := strings.TrimSpace(action.Run)
	if err := s.refuseTransposedStoppage(ctx, id, run); err != nil {
		outcome.fail(err)
		return
	}
	// A crossing is not one of the decisions the budget bounds — it is what raises
	// one of those budgets — so it is carried out on its own path rather than
	// through the spend below. It still lands on the item, in a note naming the
	// cap, the crossing number, and the argument, which is the half of the
	// delegation that outlives the channel message beside it.
	if decision == decisionCross {
		s.carryOutCapCrossing(ctx, outcome, id, run)
		return
	}
	spent, err := s.spendTriageBudget(ctx, id, decision)
	if err != nil {
		outcome.refused(refusedPastCap(err))
		return
	}
	note := s.trackerProvenance(triageVerbs[decision]+", on the stopped work of run "+run, action.Reason)
	if decision == decisionEscalate {
		// An escalation is recorded as a blocker rather than as a note, because
		// the item itself has to say it is waiting on a person: a note leaves the
		// item looking like work in flight, which is the state this whole workflow
		// exists to stop somebody discovering by accident.
		if _, err := s.options.Tracker.Block(ctx, id, note); err != nil {
			outcome.fail(err)
			return
		}
		outcome.applied("escalated %s to the operator and blocked it, on the stopped work of run %s", id, run)
		return
	}
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("triaged %s as %q, on the stopped work of run %s%s", id, decision, run, spent)
}

// carryOutCapCrossing raises one of the item's caps on this role's own delegated
// authority and writes the crossing onto the item.
//
// The cap is raised before the note is written, which is the order every decision
// here keeps: a process that dies between the two has crossed a cap it did not
// write down, rather than written down a crossing nothing permits. What that
// costs is a crossing the item's notes do not explain, and the durable triage
// record carries the reason either way.
//
// Nothing is spent and nothing is bought. The decision the crossing was for is
// still a separate decision, recorded afterwards and refused by everything it was
// always refused by — the crossing only moves the one cap it names.
func (s *Session) carryOutCapCrossing(ctx context.Context, outcome *TrackerOutcome, workItemID, runID string) {
	action := outcome.Action
	budget := strings.TrimSpace(action.Budget)
	if s.options.Triage == nil {
		outcome.fail(errors.New("no triage budget is wired to this conversation, so a cap cannot be crossed and nothing was recorded"))
		return
	}
	crossing, err := s.options.Triage.CrossCap(ctx, workItemID, budget, strings.TrimSpace(action.Reason))
	if err != nil {
		// A crossing past the bound names the operator's command, so it is the same
		// kind of refusal as the one that sent the role here: what it says is what
		// to do instead, and cutting it to a line is what loses that.
		outcome.refused(err)
		return
	}
	verb := fmt.Sprintf("Triaged: the %s cap crossed to %d on the development manager's own authority, which is crossing %d of %d for this item, on the stopped work of run %s",
		crossing.Budget, crossing.Cap, crossing.Number, crossing.Bound, runID)
	if _, err := s.options.Tracker.Update(ctx, workItemID, beads.WorkItemChange{
		AppendNotes: s.trackerProvenance(verb, action.Reason),
	}); err != nil {
		outcome.fail(err)
		return
	}
	// The crossing travels with the outcome so that what the operator is told in
	// the channel is the record's own figures rather than a second count of them.
	// It is the whole of the veto: the crossing is in force already, and what keeps
	// it answerable is that the operator reads it at the moment it happens.
	outcome.Crossing = &CapCrossing{
		Budget:    crossing.Budget,
		Cap:       crossing.Cap,
		Crossing:  crossing.Number,
		Crossings: crossing.Bound,
	}
	outcome.applied("crossed the %s cap for %s to %d on your own authority, which is crossing %d of %d for this item and is reported to the operator as it stands; nothing was spent and no attempt was bought, so the decision it permits is still one to record",
		crossing.Budget, workItemID, crossing.Cap, crossing.Number, crossing.Bound)
}

// refuseTransposedStoppage refuses a decision whose run was made for some other
// work item. It is weaker than asking whether the run is on the docket, and
// deliberately so: an entry may have been cut from a bounded listing, and
// refusing a decision for that would refuse exactly the oldest stoppages nobody
// has got to yet. What this catches is the failure a docket of several entries
// actually produces — two of them transposed — where both items end up carrying
// reasoning about a change that is not theirs.
//
// A run the store cannot answer for is refused too. The decision is written onto
// an item as settled fact about a particular stoppage, and a stoppage nothing
// can be found out about is not one this conversation has established anything
// against; refusing costs the decision nothing, because nothing is spent before
// this is asked.
func (s *Session) refuseTransposedStoppage(ctx context.Context, workItemID, runID string) error {
	if s.options.Stoppages == nil {
		return nil
	}
	stoppedFor, err := s.options.Stoppages.WorkItemOf(ctx, runID)
	if err != nil {
		return fmt.Errorf("run %s could not be read, so nothing says it is %s's stopped work; nothing was recorded and nothing was spent: %w",
			runID, workItemID, err)
	}
	stopped := strings.TrimSpace(stoppedFor)
	if stopped == "" {
		return fmt.Errorf("run %s records no work item, so nothing says it is %s's stopped work; nothing was recorded and nothing was spent",
			runID, workItemID)
	}
	if stopped != workItemID {
		return fmt.Errorf("run %s was made for %s rather than for %s, so this decision would land on an item whose stopped work it is not about; nothing was recorded and nothing was spent",
			runID, stopped, workItemID)
	}
	return nil
}

// refusedPastCap says what a cap refusal leaves available, and leaves every other
// failure exactly as it was.
//
// A refusal is the gate working. It was also, until a cap could be crossed, the
// end of the road: the decision could not be recorded, so nothing could carry it
// out, and escalating recorded nothing either — which left a cap-exhausted item
// unrunnable by every path the harness keeps a record of. A development manager
// who is refused without knowing the remedy exists escalates into the same
// silence the escalation is meant to break.
//
// It names the remedy that is this role's own first, because it is the one that
// costs nobody a wait: every override recorded in the week to 2026-09-06 was
// granted, most within minutes, and the operator step was latency rather than
// judgement. So what the refusal offers is the crossing verb, bounded and
// justified; the operator's command is what it offers after that, for the item
// that has already had its five and for the raise nobody delegated.
//
// It names commands rather than remedies, and that is the whole of what this text
// got wrong twice. "The operator can record an override against the item" was
// read as the item's notes — the only place a conversation writes to an item at
// all — so the operator answered the escalation there, exactly as the words
// directed, the same decision was asked for again, and the identical refusal came
// back. No guard reads a note, and nothing in the sentence named the verb that
// does. So the refusal prints what to write, with the budget that refused and the
// item already in it, and says plainly that a note is not one.
func refusedPastCap(err error) error {
	if !errors.Is(err, runstate.ErrTriageCapReached) {
		return err
	}
	return fmt.Errorf("%w. Nothing written into the item's notes crosses that cap — no guard reads prose. What crosses it from here is %s, up to %d times per item and only with the reason it is being crossed for, which is recorded on the item and reported to the operator as you record it; once the crossing is recorded, asking for this same decision again records it. Past those %d, or for a raise larger than one, escalate and the operator crosses it themselves with %s",
		err, crossingDecisions(err), runstate.MaxDelegatedCapCrossings, runstate.MaxDelegatedCapCrossings, overrideCommands(err))
}

// crossingDecisions are the crossings that would clear one refusal, written as
// the blocks that record them, with the budget and the item already in each.
//
// One per budget that refused, for the reason the operator's commands beside them
// are one per budget: an action can stand behind two of them, and crossing one
// leaves the same decision refused by the other. Both in one turn is one sitting
// rather than two, which is exactly what cost two override ceremonies minutes
// apart on each of two items on 2026-09-05.
//
// The run is left as a placeholder rather than filled in. Everything else here is
// a figure the refusal already holds, and the run is the one thing it does not:
// the decision that was refused named it, and naming the wrong one is what the
// transposition guard exists to catch.
func crossingDecisions(err error) string {
	var capped runstate.TriageCapError
	if !errors.As(err, &capped) || len(capped.Refusals) == 0 {
		return `a triage action of ` + "`" + `{"action":"triage","id":"<work item>","run":"<run>","decision":"cross","budget":"<budget>","reason":"<why>"}` + "`"
	}
	crossings := make([]string, 0, len(capped.Refusals))
	for _, refusal := range capped.Refusals {
		crossings = append(crossings, fmt.Sprintf("`{\"action\":\"triage\",\"id\":%q,\"run\":\"<the run the entry names>\",\"decision\":%q,\"budget\":%q,\"reason\":\"<why>\"}`",
			capped.WorkItemID, decisionCross, refusal.Budget))
	}
	if len(crossings) == 1 {
		return crossings[0]
	}
	return strings.Join(crossings[:len(crossings)-1], ", ") + " and " + crossings[len(crossings)-1] + " — both are needed, since either budget alone still refuses it"
}

// overrideCommands are the commands that cross the caps one refusal came from,
// with the budget, the work item, and the ceiling already filled in, so what
// reaches the operator is something to run rather than something to reconstruct.
//
// One per budget that refused, because an action can stand behind two of them and
// crossing one leaves the same decision refused by the other. Both in one message
// is one sitting rather than two: the alternative is what actually happened on
// 2026-09-05, when two items each took two override ceremonies minutes apart
// because the first refusal named only the first budget.
//
// The ceiling is filled in rather than left as a placeholder for the same reason
// the budget is. An operator handed `--cap <n>` has to go and find what the item
// has spent before they can type a number, and the refusal beside this sentence
// is the only place that figure appears.
//
// A refusal carrying no budget to name falls back to the shape of the command
// rather than to a description of it. Nothing produces one today — every cap
// refusal here is a TriageCapError — but a refusal that lost the detail must
// still leave the operator holding a verb.
func overrideCommands(err error) string {
	var capped runstate.TriageCapError
	if !errors.As(err, &capped) || len(capped.Refusals) == 0 {
		return "`yoyo triage override --budget \"<budget>\" --cap <n> --by \"<operator>\" --reason \"<why>\" <work item>`"
	}
	commands := make([]string, 0, len(capped.Refusals))
	for _, refusal := range capped.Refusals {
		commands = append(commands, fmt.Sprintf("`yoyo triage override --budget %q --cap %d --by \"<operator>\" --reason \"<why>\" %s`",
			refusal.Budget, refusal.Permits(), capped.WorkItemID))
	}
	if len(commands) == 1 {
		return commands[0]
	}
	// Both are required rather than either, and the sentence says so: a decision
	// two budgets refuse is permitted by neither override alone.
	return strings.Join(commands[:len(commands)-1], ", ") + " and " + commands[len(commands)-1] + " — both are needed, since either budget alone still refuses it"
}

// spendTriageBudget spends what the decision costs and says what it came to, or
// says nothing for a decision that costs nothing. Three of the six buy another
// attempt at work that already failed once, and those are the three the durable
// budget bounds; re-scoping, waiting, and escalating buy no attempt at all and
// are never refused for budget.
func (s *Session) spendTriageBudget(ctx context.Context, workItemID, decision string) (string, error) {
	switch decision {
	case decisionRepair, decisionRerun, decisionRearm:
	default:
		return "", nil
	}
	if s.options.Triage == nil {
		return "", errors.New("no triage budget is wired to this conversation, so a repair, a re-run, or a re-arm cannot be bounded and was not recorded")
	}
	switch decision {
	case decisionRepair:
		granted, err := s.options.Triage.GrantRepair(ctx, workItemID)
		if err != nil {
			return "", err
		}
		if granted.Truncated {
			return fmt.Sprintf("; the grant was cut from %d round(s) to the %d the cap still had room for",
				granted.Requested, granted.Rounds), nil
		}
		return fmt.Sprintf("; it is granted %d further review round(s)", granted.Rounds), nil
	case decisionRerun:
		counters, err := s.options.Triage.RecordRerun(ctx, workItemID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("; %d re-run(s) of it are now recorded", counters.Reruns), nil
	default:
		counters, err := s.options.Triage.RecordMergeRearm(ctx, workItemID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("; %d merge re-arm(s) of it are now recorded", counters.MergeRearms), nil
	}
}
