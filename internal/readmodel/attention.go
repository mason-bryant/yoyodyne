package readmodel

// One thing waiting on a person, carried as the record it is about.
//
// Until yoyodyne-ifd.432.5 an attention entry was two sentences: what is
// waiting, and whose move it is. A sentence can be printed and nothing else — a
// surface that wants to show a proposed change in full, count the entries by
// who has to move, or act on one has nothing to open, nothing to group by, and
// nothing to name in the act. So an entry now carries the thing: its kind, from
// a closed vocabulary; the identifier of the record it is about; who moves
// next, from a second closed vocabulary; and the record itself, whole, where
// there is one — an amendment's target document, proposer, change, and reason
// among them.
//
// The two sentences are still what a terminal prints, and they are derived
// here from those fields rather than stored beside them. That is what makes
// the record and the line one thing: nothing can carry a sentence that says
// one thing over fields that say another, because the sentence is never
// written down. The JSON a script or a page reads carries both, the fields and
// the sentences computed from them at the moment of writing, so a reader that
// only wants the line still has it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AttentionKind is what sort of record one attention entry is about. The set
// is closed, for the reason the stall's reasons are: a kind nobody named is a
// kind no surface can show or act on, and the entry an operator most needs is
// exactly the one nobody thought to give a name.
type AttentionKind string

const (
	// AttentionAmendment is a change proposed to a document its proposer does
	// not own, which nobody has decided.
	AttentionAmendment AttentionKind = "amendment"
	// AttentionCarriedItem is an admitted work item marked for a conversation
	// rather than a developer run.
	AttentionCarriedItem AttentionKind = "conversation-carried-item"
	// AttentionReports is the collected report pile, once its oldest undecided
	// report has waited longer than any working cadence would leave it.
	AttentionReports AttentionKind = "report"
	// AttentionOwedStep is a run that ended still owing a step.
	AttentionOwedStep AttentionKind = "owed-step"
	// AttentionPublication is a promotion the forge has not published.
	AttentionPublication AttentionKind = "publication"
	// AttentionDegradedService is a part of the product its supervisor has
	// stopped restarting.
	AttentionDegradedService AttentionKind = "degraded-service"
	// AttentionHold is one of the switches over what the harness does: the
	// operator's hold over everything, the intake hold over what it chooses for
	// itself, and the provider holding every role at once. The entry's ID says
	// which of the three.
	AttentionHold AttentionKind = "hold"
	// AttentionDirective is a directive that pauses work and nobody has
	// resolved.
	AttentionDirective AttentionKind = "directive"
	// AttentionOutage is the provider answering nobody.
	AttentionOutage AttentionKind = "outage"
	// AttentionStall is a queue nothing is pulling from while admitted work
	// waits behind that: a session sitting idle over it, or no session at all.
	AttentionStall AttentionKind = "stall"
	// AttentionHeldWork is the admitted work somebody has to release, counted
	// by whose move it is rather than named item by item.
	AttentionHeldWork AttentionKind = "held-work"
)

// AttentionKinds is the whole vocabulary, so a test that has to cover every
// kind reads it from here rather than repeating the list.
func AttentionKinds() []AttentionKind {
	return []AttentionKind{
		AttentionAmendment,
		AttentionCarriedItem,
		AttentionReports,
		AttentionOwedStep,
		AttentionPublication,
		AttentionDegradedService,
		AttentionHold,
		AttentionDirective,
		AttentionOutage,
		AttentionStall,
		AttentionHeldWork,
	}
}

// Valid reports whether a token is one of the kinds.
func (k AttentionKind) Valid() bool {
	for _, known := range AttentionKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// The three switches an AttentionHold entry can be about, as its ID names them.
// None of the three is a record with an identifier of its own — each is one
// file under the product, present or absent — so the name of the switch is
// what identifies it.
const (
	HoldOperator = "operator"
	HoldIntake   = "intake"
	HoldCapacity = "capacity"
)

// Mover is who has to act next on one thing waiting: the operator, one of the
// harness's roles, the harness itself, the forge, or nobody. It is the
// vocabulary a surface counts the attention line by, so it is a closed set of
// tokens rather than the possessive a sentence prints — the possessive is
// derived from it, below, and is the one wording every sentence on the line
// opens with.
type Mover string

const (
	MoverOperator Mover = "operator"
	// MoverHarness is the harness acting at its next sweep or poll: held work
	// whose decision is recorded and not yet carried out, or a promotion whose
	// record holds no pull request for the next reconcile to look up.
	MoverHarness Mover = "harness"
	// MoverForge is the forge merging a request it has queued.
	MoverForge Mover = "forge"
	// MoverProvider is the model provider answering again. No attention entry is
	// attributed to it — a usage window lifts on the provider's clock, which is
	// nobody's move — and it is here because a program manager's lane report may
	// name it as what a blocker is waiting on, and that report's movers are this
	// vocabulary's.
	MoverProvider Mover = "provider"
	// MoverNobody is a wait nobody ends: a provider's usage window lifts on the
	// provider's clock.
	MoverNobody Mover = "nobody"
	// MoverUnnamed is the role a conversation-carried item names where the
	// harness cannot read which: a bare marker, or one it does not recognize.
	// It is a token rather than an absence so a surface counting by mover has
	// something to count it under.
	MoverUnnamed Mover = "unnamed-role"

	MoverProductManager     = Mover(domain.RoleProductManager)
	MoverArchitect          = Mover(domain.RoleArchitect)
	MoverDevelopmentManager = Mover(domain.RoleDevelopmentManager)
)

// MoverOf is the mover for one of the harness's roles, and the unnamed mover
// for a role the harness does not recognize — which includes the empty role a
// conversation marker yields when it names none.
func MoverOf(role domain.AgentRole) Mover {
	if !role.Valid() {
		return MoverUnnamed
	}
	return Mover(role)
}

// Movers is the whole vocabulary, in the order a surface lists them: the
// operator first, because the line is called "needs a human" and his is the
// count that says whether it needs him; then the roles in the hierarchy's
// order; then the movers that are not people.
func Movers() []Mover {
	return []Mover{
		MoverOperator,
		MoverProductManager,
		MoverArchitect,
		MoverDevelopmentManager,
		Mover(domain.RoleDeveloper),
		Mover(domain.RoleReviewer),
		MoverHarness,
		MoverForge,
		MoverProvider,
		MoverNobody,
		MoverUnnamed,
	}
}

// Valid reports whether a token is one of the movers.
func (m Mover) Valid() bool {
	for _, known := range Movers() {
		if m == known {
			return true
		}
	}
	return false
}

// Possessive is the mover as every sentence on the attention line opens: "the
// operator's", "the development manager's", "nobody's". It is worded once here
// so a surface grouping the line by mover and a terminal printing it name the
// same person the same way.
func (m Mover) Possessive() string {
	switch m {
	case MoverOperator:
		return "the operator's"
	case MoverHarness:
		return "the harness's"
	case MoverForge:
		return "the forge's"
	case MoverProvider:
		return "the provider's"
	case MoverNobody:
		return "nobody's"
	case MoverUnnamed:
		return "the role it names"
	default:
		return "the " + domain.AgentRole(m).Title() + "'s"
	}
}

// Attention is one thing waiting on a person: what kind of thing, which one,
// whose move it is, and the record itself where there is one. The move is half
// the fact — a thread that says something is waiting without saying who on is
// the silence this whole surface exists to end — and the record is the other
// half a surface needs to show the thing or act on it.
//
// Exactly one of the record fields is set, the one the kind names; the rest
// are absent from the JSON. What and Whose are not fields: they are the two
// sentences derived from these, and the JSON carries them computed.
type Attention struct {
	Kind AttentionKind `json:"kind"`
	// ID is the identifier of the record the entry is about: an amendment's
	// id, a directive's, a run's, a work item's, a service's name, or the
	// switch an AttentionHold entry names. It is empty on the two entries that
	// are about a set rather than a record — the report pile and held work —
	// and on the stall and the outage it is the stall's reason and the
	// outage's cause, which is what identifies each of those.
	ID    string `json:"id,omitempty"`
	Mover Mover  `json:"mover"`
	// WorkItemID is the admitted work item the entry is about, where it is
	// about one: the carried item itself, the item a run was carrying, the
	// item an amendment's proposer was working on.
	WorkItemID string `json:"work_item_id,omitempty"`

	// Amendment is the proposed change whole, on an AttentionAmendment entry:
	// the target document, its kind and owner, the proposer's role, agent,
	// run, and work item, the change, and why.
	Amendment *amendment.Proposal `json:"amendment,omitempty"`
	// Directive is the unresolved directive whole, on an AttentionDirective
	// entry.
	Directive *directive.Directive `json:"directive,omitempty"`
	// OperatorHold, IntakeHold, and CapacityHold are the switch an
	// AttentionHold entry is about, one of them set to match the ID.
	OperatorHold *runstate.OperatorHold `json:"operator_hold,omitempty"`
	IntakeHold   *runstate.IntakeHold   `json:"intake_hold,omitempty"`
	CapacityHold *CapacityHold          `json:"capacity_hold,omitempty"`
	// Outage is the provider's outage record, on an AttentionOutage entry.
	Outage *runstate.ProviderOutage `json:"outage,omitempty"`
	// Stall is the stall as the not-startable line derived it, on an
	// AttentionStall entry.
	Stall *Stall `json:"stall,omitempty"`
	// Reports is how the pile stands, on an AttentionReports entry.
	Reports *report.Pile `json:"reports,omitempty"`
	// Service is the supervisor's record of the part it left down, on an
	// AttentionDegradedService entry.
	Service *runstate.SupervisedChild `json:"service,omitempty"`
	// OwedStep is where the run stopped, on an AttentionOwedStep entry; the
	// run is the ID and its item is WorkItemID.
	OwedStep *OwedStep `json:"owed_step,omitempty"`
	// Publication is the promotion and what the forge holds of it, on an
	// AttentionPublication entry; the run is the ID and its item is
	// WorkItemID.
	Publication *Publication `json:"publication,omitempty"`
	// HeldWork is which wait and how many items are in it, on an
	// AttentionHeldWork entry.
	HeldWork *HeldWork `json:"held_work,omitempty"`
	// Executor is the marker that hands the item to a conversation, on an
	// AttentionCarriedItem entry; the item is WorkItemID.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
}

// OwedStep is where a run that still owes a step stopped: its recorded status
// and phase, which between them say which step that is. The run and its item
// are on the entry.
type OwedStep struct {
	Status runstate.Status `json:"status"`
	Phase  runstate.Phase  `json:"phase,omitempty"`
}

// Publication is a promotion the forge has not published: where it was
// promoted to, the branch that carries it, the pull request the forge holds
// for it where the record holds one, and the drop where the forge dropped its
// merge. Which of the four movers it waits on is read off these.
type Publication struct {
	// TargetBranch is empty where the record holds no integration to read it
	// from; the sentence says so rather than the field carrying a placeholder.
	TargetBranch string                `json:"target_branch,omitempty"`
	Branch       string                `json:"branch"`
	PullRequest  *runstate.PullRequest `json:"pull_request,omitempty"`
	MergeDrop    *runstate.MergeDrop   `json:"merge_drop,omitempty"`
}

// HeldWait is which of the two waits held work is in.
type HeldWait string

const (
	// HeldAwaitingDecision is a stoppage the development manager has still to
	// decide about.
	HeldAwaitingDecision HeldWait = "decision"
	// HeldAwaitingCarryOut is a decision she recorded that the harness has
	// still to act on.
	HeldAwaitingCarryOut HeldWait = "carry-out"
)

// HeldWork is how many admitted items are in one of the two waits.
type HeldWork struct {
	Awaiting HeldWait `json:"awaiting"`
	Count    int      `json:"count"`
}

// What is the thing waiting, as the terminal prints it: derived from the
// entry's record, never stored.
func (a Attention) What() string {
	switch a.Kind {
	case AttentionHold:
		switch {
		case a.OperatorHold != nil:
			return fmt.Sprintf("all harness activity is held, since %s",
				a.OperatorHold.HeldAt.UTC().Format(time.RFC3339))
		case a.IntakeHold != nil:
			return fmt.Sprintf("intake is held, since %s: %s",
				a.IntakeHold.HeldAt.UTC().Format(time.RFC3339), singleLine(intakeClause(*a.IntakeHold), maxRefusalBytes))
		case a.CapacityHold != nil:
			what := "every role is held by the provider's usage window, since " + a.CapacityHold.Since.UTC().Format(time.RFC3339)
			if !a.CapacityHold.ResetsAt.IsZero() {
				what += ", until " + a.CapacityHold.ResetsAt.UTC().Format(time.RFC3339)
			}
			return what
		}
	case AttentionDirective:
		if a.Directive != nil {
			return fmt.Sprintf("directive %s is unresolved: %s",
				a.Directive.ID, singleLine(a.Directive.Unresolved, maxRefusalBytes))
		}
	case AttentionAmendment:
		if a.Amendment != nil {
			return fmt.Sprintf("a change to %s is proposed and undecided (%s)", a.Amendment.Artifact, a.Amendment.ID)
		}
	case AttentionOwedStep:
		return fmt.Sprintf("run %s of %s ended still owing a step", a.ID, a.WorkItemID)
	case AttentionPublication:
		if a.Publication != nil {
			target := a.Publication.TargetBranch
			if target == "" {
				target = "an unrecorded target"
			}
			if a.Publication.PullRequest == nil {
				return fmt.Sprintf("run %s promoted %s into %s and its record holds no pull request for branch %s, so nothing has asked the forge to merge it",
					a.ID, a.WorkItemID, target, a.Publication.Branch)
			}
			return fmt.Sprintf("run %s promoted %s into %s and the forge has not published it: pull request #%d %s",
				a.ID, a.WorkItemID, target, a.Publication.PullRequest.Number, a.Publication.PullRequest.URL)
		}
	case AttentionOutage:
		if a.Outage != nil {
			return a.Outage.Says()
		}
	case AttentionReports:
		if a.Reports != nil {
			return a.Reports.Describe()
		}
	case AttentionStall:
		if a.Stall != nil {
			what := a.Stall.Says
			// The provider answering nobody already says since when in its own
			// sentence; the two session states do not, and how long a queue has
			// been unpulled is half of what makes it worth acting on.
			if a.Stall.Reason != ReasonProviderAway && !a.Stall.Since.IsZero() {
				what += ", since " + a.Stall.Since.UTC().Format(time.RFC3339)
			}
			return what
		}
	case AttentionDegradedService:
		if a.Service != nil {
			return fmt.Sprintf("the %s service is degraded: %s", a.Service.Service, singleLine(a.Service.Reason, maxRefusalBytes))
		}
	case AttentionHeldWork:
		if a.HeldWork != nil {
			counted := count(a.HeldWork.Count, "admitted item")
			if a.HeldWork.Awaiting == HeldAwaitingCarryOut {
				return fmt.Sprintf("%s %s carry-out of a decision already recorded", counted, awaits(a.HeldWork.Count))
			}
			return fmt.Sprintf("%s %s the development manager's decision", counted, awaits(a.HeldWork.Count))
		}
	case AttentionCarriedItem:
		return fmt.Sprintf("%s is admitted for %q rather than a developer run", a.WorkItemID, a.Executor)
	}
	// An entry whose record is missing is still said rather than printed
	// blank: a blank line on the attention line is the confident emptiness
	// this package refuses everywhere else.
	return strings.TrimSpace(string(a.Kind)+" "+a.ID) + " is waiting and its record was not carried"
}

// Whose is whose move it is and what settles it, as the terminal prints it. It
// opens with the mover's possessive on every kind, so a surface counting the
// line by Mover and a reader of the sentence agree on who has to act; a test
// holds every kind to that.
func (a Attention) Whose() string {
	switch a.Kind {
	case AttentionHold:
		switch {
		case a.OperatorHold != nil:
			return ReasonOperatorHold.Whose()
		case a.IntakeHold != nil:
			// The hold's own record words it, because the same switch is placed
			// by the operator and by the brake, and only the record says which.
			return a.IntakeHold.Whose()
		case a.CapacityHold != nil:
			return a.CapacityHold.Whose()
		}
	case AttentionDirective:
		return a.Mover.Possessive() + " — the work it affects waits until `yoyo directive resolve` settles it"
	case AttentionAmendment:
		return a.Mover.Possessive() + " — nothing reaches the document until they or the operator decide it"
	case AttentionOwedStep:
		return a.Mover.Possessive() + " — `yoyo reconcile` reports which and settles it"
	case AttentionPublication:
		if a.Publication != nil {
			// Four cases have four different movers, and all four are settled by
			// the same sweep once the forge records the merge; each says so,
			// because that is what stops a reader going looking for a lever that
			// is not there.
			switch {
			case a.Publication.PullRequest == nil:
				return a.Mover.Possessive() + " — `yoyo reconcile` looks the request up on the forge by that branch, records it, and arms its merge; a forge that holds none is said on every sweep"
			case a.Publication.PullRequest.MergeQueued:
				return a.Mover.Possessive() + " — it merges once the base branch's requirements are met, and `yoyo reconcile` settles the run when it does"
			case a.Publication.MergeDrop != nil:
				return a.Mover.Possessive() + " — the forge dropped the merge; `yoyo triage rearm` repeats it once, or a person merges the request by hand, and `yoyo reconcile` settles it once the forge records the merge"
			default:
				return a.Mover.Possessive() + " — the request is on the forge unmerged; merge it, or leave it, and `yoyo reconcile` settles it once the forge records the merge"
			}
		}
	case AttentionOutage:
		return ReasonProviderAway.Whose()
	case AttentionReports:
		return a.Mover.Possessive() + " — reports are decided in conversation, and a pile this old says the schedule that works it is not keeping up"
	case AttentionStall:
		if a.Stall != nil {
			return a.Stall.Reason.Whose()
		}
	case AttentionDegradedService:
		return a.Mover.Possessive() + " — the supervisor has stopped restarting it; fix the cause, then `yoyo stop` and `yoyo start` bring it back, or start the part by hand and the supervisor takes it back"
	case AttentionHeldWork:
		if a.HeldWork != nil && a.HeldWork.Awaiting == HeldAwaitingCarryOut {
			return a.Mover.Possessive() + " — the decision is made, and what is outstanding is the harness acting on it"
		}
		return a.Mover.Possessive() + " — nothing pulls a stopped item until she decides what happens to it"
	case AttentionCarriedItem:
		return a.Mover.Possessive() + " — in conversation; no run will ever be started for it"
	}
	return a.Mover.Possessive() + " — the entry's record was not carried, so what settles it cannot be said"
}

// attentionFields is the entry's fields without its methods, so the wire shape
// can embed them without inheriting the JSON methods it is implementing.
type attentionFields Attention

// attentionWire is the entry as the JSON carries it: the fields, and the two
// sentences computed from them.
type attentionWire struct {
	attentionFields
	What  string `json:"what"`
	Whose string `json:"whose"`
}

// MarshalJSON writes the fields and, beside them, the two sentences derived
// from them at this moment. A reader that only wants the line has it; a
// reader that wants the record has that; and neither can be handed one that
// disagrees with the other, because the sentences are never taken from
// anywhere but the fields.
func (a Attention) MarshalJSON() ([]byte, error) {
	return json.Marshal(attentionWire{attentionFields: attentionFields(a), What: a.What(), Whose: a.Whose()})
}

// UnmarshalJSON reads the fields back and refuses an entry outside the shape:
// a kind or a mover that is not in its vocabulary, an unknown field, or a
// sentence that disagrees with the fields. The sentences are derived, so a
// document may leave them out; one that carries them is held to the
// derivation, because a hand-written fixture whose line says one thing over
// fields that say another is the disagreement this shape exists to make
// impossible. The vocabularies are held for the same reason: an entry whose
// mover nobody named would print as nobody's move in particular, which is the
// silence the line exists to end. Nothing in the harness stores a standing
// document and reads it back — the only readers are the dashboard's fixtures
// and a script reading `yoyo status --json` — so the refusal costs no stored
// record its readability when a sentence is reworded.
func (a *Attention) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire attentionWire
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	decoded := Attention(wire.attentionFields)
	if !decoded.Kind.Valid() {
		return fmt.Errorf("attention entry %s: its kind %q is not one of %v", decoded.ID, decoded.Kind, AttentionKinds())
	}
	if !decoded.Mover.Valid() {
		return fmt.Errorf("attention entry %s %s: its mover %q is not one of %v", decoded.Kind, decoded.ID, decoded.Mover, Movers())
	}
	if strings.TrimSpace(wire.What) != "" && wire.What != decoded.What() {
		return fmt.Errorf("attention entry %s %s: its what %q disagrees with its record, which says %q", decoded.Kind, decoded.ID, wire.What, decoded.What())
	}
	if strings.TrimSpace(wire.Whose) != "" && wire.Whose != decoded.Whose() {
		return fmt.Errorf("attention entry %s %s: its whose %q disagrees with its record, which says %q", decoded.Kind, decoded.ID, wire.Whose, decoded.Whose())
	}
	*a = decoded
	return nil
}

// operatorHoldAttention is the operator's hold as the attention line carries it.
func operatorHoldAttention(hold runstate.OperatorHold) Attention {
	return Attention{Kind: AttentionHold, ID: HoldOperator, Mover: MoverOperator, OperatorHold: &hold}
}

// intakeHoldAttention is the intake hold as the attention line carries it,
// with whose move it is read off the hold's own record: the operator's for a
// hold they placed, and for one the brake placed the development manager's
// while she decides, the harness's while a probe runs or a decision waits to
// be carried out, and the operator's only once it is escalated — by her, or by
// the harness at the bound on its summons-and-probe loop. The record's own
// Whose words the same cases in the same order, and a test holds the two
// together: her release is read ahead of an escalation, because the two can
// stand together on a hold the harness escalated and her release is what the
// session acts on.
func intakeHoldAttention(hold runstate.IntakeHold) Attention {
	mover := MoverOperator
	if hold.Braked() {
		switch {
		case hold.Brake.Decision == runstate.BrakeDecisionRelease:
			mover = MoverHarness
		case hold.Brake.Escalated():
			mover = MoverOperator
		case hold.Brake.Decision == runstate.BrakeDecisionProbe, hold.Brake.Probing():
			mover = MoverHarness
		default:
			mover = MoverDevelopmentManager
		}
	}
	return Attention{Kind: AttentionHold, ID: HoldIntake, Mover: mover, IntakeHold: &hold}
}

// directiveAttention is an unresolved directive as the attention line carries
// it.
func directiveAttention(paused directive.Directive) Attention {
	return Attention{Kind: AttentionDirective, ID: paused.ID, Mover: MoverOperator, Directive: &paused}
}

// amendmentAttention is an undecided proposal as the attention line carries
// it: whose move it is is the document's owner, and the proposal is carried
// whole so a surface can show what was proposed and why, and act on it by id.
func amendmentAttention(proposal amendment.Proposal) Attention {
	return Attention{
		Kind:       AttentionAmendment,
		ID:         proposal.ID,
		Mover:      MoverOf(proposal.Owner),
		WorkItemID: proposal.WorkItemID,
		Amendment:  &proposal,
	}
}

// owedStepAttention is a run that ended still owing a step, as the attention
// line carries it.
func owedStepAttention(state runstate.State) Attention {
	return Attention{
		Kind:       AttentionOwedStep,
		ID:         state.RunID,
		Mover:      MoverOperator,
		WorkItemID: state.WorkItemID,
		OwedStep:   &OwedStep{Status: state.Status, Phase: state.Phase},
	}
}

// outageAttention is the provider answering nobody, as the attention line
// carries it.
func outageAttention(outage runstate.ProviderOutage) Attention {
	return Attention{Kind: AttentionOutage, ID: string(outage.Cause), Mover: MoverOperator, Outage: &outage}
}

// reportsAttention is a pile whose oldest undecided report has waited too
// long, as the attention line carries it.
func reportsAttention(pile report.Pile) Attention {
	return Attention{Kind: AttentionReports, Mover: MoverProductManager, Reports: &pile}
}

// degradedServiceAttention is a part the supervisor has left down, as the
// attention line carries it.
func degradedServiceAttention(child runstate.SupervisedChild) Attention {
	return Attention{Kind: AttentionDegradedService, ID: string(child.Service), Mover: MoverOperator, Service: &child}
}

// heldWorkAttention is one of the two waits held work is in, with how many
// items are in it: the development manager's where a decision is owed, and the
// harness's where one is recorded and not yet carried out.
func heldWorkAttention(awaiting HeldWait, items int) Attention {
	mover := MoverDevelopmentManager
	if awaiting == HeldAwaitingCarryOut {
		mover = MoverHarness
	}
	return Attention{Kind: AttentionHeldWork, Mover: mover, HeldWork: &HeldWork{Awaiting: awaiting, Count: items}}
}

// carriedItemAttention is an admitted item marked for a conversation, as the
// attention line carries it: whose move it is is the role the marker names,
// and the unnamed mover where it names none the harness recognizes.
func carriedItemAttention(workItemID string, executor domain.WorkItemExecutor) Attention {
	return Attention{
		Kind:       AttentionCarriedItem,
		ID:         workItemID,
		Mover:      MoverOf(executor.Role()),
		WorkItemID: workItemID,
		Executor:   executor,
	}
}
