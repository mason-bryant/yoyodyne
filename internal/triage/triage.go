// Package triage collects the work that has stopped moving and hands it to the
// development manager.
//
// The product manager has the backlog: what is admitted reaches it without
// anybody carrying it there. Nothing carried the other half. A run that ends on
// a durable blocker, and a publication the forge never merged, are both work
// that has stopped and both were only discoverable by an operator who went
// looking — in the tracker for one, on the forge for the other. That is the
// operator standing in as the development manager's eyes, which is exactly what
// the goal this serves says the normal loop must not need.
//
// So a docket entry is the same kind of thing a backlog item is: durable,
// created where the fact was established rather than where somebody happened to
// look, and delivered into the conversation of the role that decides about it.
// What it is not is a decision. An entry says a piece of work stopped and
// carries the evidence of how; whether it is repaired, escalated, or re-scoped
// is the development manager's, and nothing here has an opinion about it.
//
// An entry carries the evidence rather than a summary of it, because the
// decision it feeds turns on the detail: the reviewer's findings in the words
// the reviewer wrote them, the check that failed and what it printed, the
// branch and worktree that were preserved so somebody can still look at the
// change, what the forge says about the merge, and the counters that say how
// much budget the item has already spent. A development manager deciding a
// repair grant without the last of those writes reasoning the configured cap
// then contradicts.
//
// One entry is not an observation but a judgement, and it is here rather than in
// a channel of its own because its destination is the same. A developer or a
// reviewer that finds the work item unmeetable as written says so in the round it
// reached, and what that produces is a decision only the development manager can
// take — so it is docketed like everything else that has stopped moving, and
// reaches her the same way.
package triage

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// CapCleared is the ceiling a budget an operator cleared stands at. It is the
// same figure the durable record uses and is declared here for the reason the
// vocabulary beside it is: an entry's shape is what a development manager reads,
// and it must not change because the harness's own schema was refactored.
const CapCleared = math.MaxInt

// SchemaVersion is versioned independently of run state, and of the collected
// reports beside it, for the same reason each of those is: an entry outlives
// the run that produced it, it has no phase and nothing to integrate, and it is
// written once and never revised.
const SchemaVersion = 1

// Class is what stopped. The four are kept apart because they are found
// differently and read differently: a stopped run is an event the harness was
// present for, a stuck publication is a thing that has not happened, which
// nothing can be present for and only a scan can notice, an unready item is
// work that never started because the tree does not meet what it asks for, and
// an escalation is a role saying out loud that the item cannot be met at all.
type Class string

const (
	// ClassStoppedRun is a run that ended on a durable blocker.
	ClassStoppedRun Class = "stopped_run"
	// ClassEscalation is a developer or a reviewer having said, in the round it
	// reached, that the work item cannot be met as it stands. It is the one class
	// that is a judgement rather than an observation, and it is here because the
	// judgement has the same destination as the other three: the development
	// manager decides, and the docket is how work reaches her without anybody
	// carrying it.
	//
	// What separates it from a stopped run is what it cost. A stoppage is what is
	// left after a run spent its budget failing; an escalation is raised in the
	// round the role saw the problem, before any of that, which is the whole point
	// of the verb.
	ClassEscalation Class = "escalation"
	// ClassPublication is an approved publication that did not finish: one the
	// forge has not merged past the configured stuck-merge age, or one the
	// harness already recorded as outstanding — a merge the forge dropped, or one
	// it performed that could not be confirmed.
	ClassPublication Class = "publication"
	// ClassUnreadyItem is an item dispatch declined to start because a
	// prerequisite it states is not met by the tree. It is the one class with no
	// run behind it, and that is the point of it: the whole value of catching this
	// is that it costs a read instead of a run.
	ClassUnreadyItem Class = "unready_item"
)

func (c Class) Valid() bool {
	switch c {
	case ClassStoppedRun, ClassEscalation, ClassPublication, ClassUnreadyItem:
		return true
	default:
		return false
	}
}

// Title names a class the way the development manager reads it.
func (c Class) Title() string {
	switch c {
	case ClassStoppedRun:
		return "stopped run"
	case ClassEscalation:
		return "item raised as unmeetable"
	case ClassPublication:
		return "unfinished publication"
	case ClassUnreadyItem:
		return "item the tree is not ready for"
	default:
		return string(c)
	}
}

// The bounds one entry is held to. They are the same order as the bounds the
// durable run record holds the same evidence to, deliberately: an entry that
// could not carry what the run recorded would be an entry a reader has to go
// back to the run for, which is the errand this exists to remove.
const (
	MaxBlockerBytes     = 16 << 10
	MaxMessageBytes     = 4 << 10
	MaxCheckOutputBytes = 8 << 10
	MaxFindings         = 50
	MaxKeyBytes         = 256
	// MaxPrerequisites bounds what one unready entry names. The readiness check
	// bounds its own reading well below this; the entry states the ceiling anyway,
	// because a durable record must not be able to take an unbounded list from a
	// caller that stopped bounding it.
	MaxPrerequisites = 10
)

// Finding is one reviewer finding as the reviewer wrote it. It is declared here
// rather than imported from the durable run schema for the reason that schema
// declares its own copy of the review vocabulary: what reaches a development
// manager must not change shape because the run record was refactored.
type Finding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Check is the deterministic check that was failing when the work stopped, with
// the bounded output the run captured of it.
type Check struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

// Artifacts are the identifiers of what the stopped work left behind. They are
// recorded even when the branch or the worktree has since been removed, because
// naming what was preserved is half of what makes an entry actionable and
// naming what is gone is the other half: a development manager sent after a
// worktree that no longer exists learns that only by going there.
type Artifacts struct {
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	BaseCommit   string `json:"base_commit,omitempty"`
	// BranchRemoved and WorktreeRemoved are the harness's own record of the two
	// cleanup steps, so an entry never sends somebody after an artifact the
	// harness already removed.
	BranchRemoved   bool `json:"branch_removed,omitempty"`
	WorktreeRemoved bool `json:"worktree_removed,omitempty"`
}

// Publication is an unfinished publication as the harness recorded it. There
// are three of those and they are one thing here, because each is a change
// whose promotion is local and whose publication is not: a merge the forge is
// sitting on, a merge it dropped because a requirement of the base branch went
// unmet, and a merge it performed that the harness could not then confirm.
//
// Nothing here is asked of the forge when an entry is made: it is what the run
// or the reconciling sweep already observed, so building a docket costs no forge
// calls and an entry describes what was true when it was docketed.
type Publication struct {
	Number      int    `json:"number"`
	URL         string `json:"url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	HeadCommit  string `json:"head_commit,omitempty"`
	State       string `json:"state,omitempty"`
	Merged      bool   `json:"merged,omitempty"`
	MergeQueued bool   `json:"merge_queued,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// Message is the harness's recorded account of how the merge ended: the
	// outstanding-publication text a dropped or unconfirmed merge leaves behind.
	// It is empty on a publication nobody has recorded anything about, which is
	// what a merge that is simply sitting there looks like — and required on a
	// merged one, which is otherwise finished work rather than something
	// somebody has to look at.
	Message string `json:"message,omitempty"`
	// ApprovedAt is when the publication was approved and left unmerged, which
	// is when the run that made it ended. The age an entry reports is measured
	// from here to when it was docketed, so it is an age rather than a countdown
	// to a deadline nothing set.
	ApprovedAt time.Time `json:"approved_at"`
}

// Environmental is a round the environment refused rather than the work failing,
// as the run recorded it. It is declared here rather than imported from the
// durable run schema for the reason Finding is: what reaches a development
// manager must not change shape because the harness's own schema was refactored.
//
// It is the one thing on an entry that changes what the counters beside it mean.
// A stoppage whose last round was environmentally refused is usually an item
// that spent nothing on that round, so a development manager reading the
// counters alone would see an item one round closer to its cap than it is — and
// would decide an escalation the item never earned. Usually rather than always:
// a refusal whose return could not be written, and one asking for a round
// another process charged, both leave the round spent. Which of them this is, is
// what Account says.
type Environmental struct {
	Cause  string `json:"cause"`
	Detail string `json:"detail,omitempty"`
	// Settled says the round the cause belongs to has ended and the class was
	// decided on it; Refused says the class applied. False for Refused is a cause
	// recorded on a round that delivered a change anyway, which spent exactly as
	// any round does, or on one whose settle could not tell.
	Settled       bool `json:"settled,omitempty"`
	Refused       bool `json:"refused,omitempty"`
	RoundReturned bool `json:"round_returned,omitempty"`
	GrantReturned bool `json:"grant_returned,omitempty"`
	// Account is the harness's own sentence about what this round cost the item,
	// carried onto the entry rather than derived here from the flags above. The
	// accounting has several states, and the ones that leave the round spent — a
	// return the settle decided on and could not write, and a round another
	// process is credited with — are the ones a reader must not be told the
	// opposite of, so it is derived once where the round settles and every surface
	// says the same words. An entry written before this was carried has none, and
	// the rendering says so rather than inventing an accounting for it.
	Account string `json:"account,omitempty"`
	// Problem is a return the settle decided on and could not write, which is the
	// one case where the counters above are higher than what the item actually
	// cost and nothing has corrected them.
	Problem string `json:"problem,omitempty"`
}

// Prerequisite is one thing an item's own statement asks of the tree that the
// tree does not have. It is declared here rather than imported from the package
// that reads it for the reason Finding is: what reaches a development manager
// must not change shape because the reading was refactored.
type Prerequisite struct {
	// Kind is the shape of the prerequisite, in the closed vocabulary the
	// readiness check names them by.
	Kind string `json:"kind"`
	// Missing is what the item needs and the tree does not have, in the item's
	// own words where the item supplied them.
	Missing string `json:"missing"`
	// Evidence is the read that says so. An entry whose refusal nobody can check
	// is one nobody can overrule, which for work that never started is the whole
	// of what the development manager has to go on.
	Evidence string `json:"evidence,omitempty"`
	// Decides is who releases it. It is on the entry rather than inferred,
	// because an item held back by nobody in particular is held back forever.
	Decides string `json:"decides,omitempty"`
}

// Unready is why dispatch declined to start an item: every prerequisite it
// states that the tree does not meet, as they were read.
//
// It describes a reading rather than an event, so unlike every other entry here
// it says what was true when it was written and may since have become false —
// the code the item pointed at can land, and then the item is ready. That is
// said out loud on the entry rather than left for a reader to work out, because
// a docket entry that reads like a standing fact is one somebody acts on months
// later.
type Unready struct {
	Prerequisites []Prerequisite `json:"prerequisites"`
	// ReadAt is when the tree was read. The prerequisites are that reading, and
	// the reading is what may have gone out of date.
	ReadAt time.Time `json:"read_at"`
}

// Kinds are the kinds this reading found, in the order they were recorded. It is
// what the entry's key is derived from: two readings that found the same kinds of
// thing about one item are the same fact however the wording moved.
func (u Unready) Kinds() []string {
	kinds := make([]string, 0, len(u.Prerequisites))
	for _, prerequisite := range u.Prerequisites {
		kinds = append(kinds, strings.TrimSpace(prerequisite.Kind))
	}
	return kinds
}

// Escalation is a role's judgement that the work item cannot be met as it
// stands, as the run recorded it. It is the whole content of the one class that
// carries a judgement: what it asks the development manager for is a decision
// about the item — replan, park, resequence, or redirect — rather than anything
// about the change, of which there is none to speak of.
type Escalation struct {
	// RaisedBy is the role that said it. It is on the entry because the two are
	// read differently: a developer saw the item from inside the work, and a
	// reviewer saw a change made for it and judged that no change would do.
	RaisedBy domain.AgentRole `json:"raised_by"`
	// Reason is that role's own account, in its own words. It is the whole of what
	// the decision is made from, so it is carried verbatim rather than summarized
	// for the reason a reviewer's findings are.
	Reason string `json:"reason"`
}

// Counters are what the item has already spent, beside what the project
// configured it may spend. Both halves travel together on purpose: a
// development manager that sees five rounds without seeing the cap of four
// cannot tell whether granting another is a decision or a contradiction.
//
// Every figure here is read from the durable per-item triage record the guards
// spend and refuse against, rather than counted again from somewhere else. That
// is the whole of what stops the two disagreeing: a view working from its own
// count can show a decision as unrecorded that the guard would refuse a second
// of, which is how one authorized recovery nearly got spent twice.
type Counters struct {
	// ReviewRounds is how many reviews this work item has accumulated across
	// every run made for it, and ReviewRoundsCap is the configured total past
	// which triage may no longer hand it back for another repair.
	ReviewRounds    int `json:"review_rounds"`
	ReviewRoundsCap int `json:"review_rounds_cap"`
	// RepairAttempts is what the stopped run spent of its own repair budget, and
	// RepairGrantAttempts is what a grant would hand the item.
	RepairAttempts      int `json:"repair_attempts"`
	RepairGrantAttempts int `json:"repair_grant_attempts"`
	// The three decisions triage records against a durable budget, each beside
	// the cap that refuses the next one. A decision is spent as it is recorded
	// and long before anything acts on it, so an entry that showed none of them
	// would describe an item nobody had decided anything about — which is exactly
	// what an item with a decision already standing looks like from the outside.
	RepairGrants    int `json:"repair_grants"`
	RepairGrantsCap int `json:"repair_grants_cap"`
	// GrantedRounds is what those grants came to, and TruncatedGrants how many of
	// them the round cap cut down on the way. They are the detail the harness
	// reports back as it records a grant — cut from two rounds to the one the cap
	// still had room for — and the count alone cannot carry it: a grant that was
	// halved and one given in full are different facts about how close this item
	// is to the end of what it will be given, and the entry that shows only "1 of
	// 1" is the entry that sends a development manager to `yoyo status` for the
	// rest.
	GrantedRounds   int `json:"granted_rounds"`
	TruncatedGrants int `json:"truncated_grants"`
	// CommittedRounds is what this item's grants have committed it to, which is
	// the figure the round budget is actually refused against. It exceeds the
	// rounds counted exactly while a grant is recorded and not yet spent, and that
	// window is where a view reporting the rounds alone shows room the guard does
	// not have.
	CommittedRounds int `json:"committed_rounds"`
	Reruns          int `json:"reruns"`
	RerunsCap       int `json:"reruns_cap"`
	MergeRearms     int `json:"merge_rearms"`
	MergeRearmsCap  int `json:"merge_rearms_cap"`
	// RerunsCarriedOut is how many of the recorded re-runs the harness has
	// actually claimed. It is the other half of the re-run gate: a decision
	// authorizes one re-run, so an item with as many claims as decisions has had
	// everything triage decided about it carried out, and the counter alone
	// cannot tell that from a decision still waiting to be acted on.
	RerunsCarriedOut int `json:"reruns_carried_out"`
	// Crossings is how many of this item's caps the development manager has
	// crossed on his own delegated authority, and CrossingsBound how many he gets.
	// They travel together for the reason every count here travels with its
	// ceiling: a development manager who sees four crossings without seeing the
	// bound of five cannot tell whether crossing again is a decision or the
	// escalation the bound exists to force.
	//
	// An entry written before the delegation existed carries zero of both, which
	// reads as an item nobody has crossed anything on — which is what every item
	// was.
	Crossings      int `json:"crossings"`
	CrossingsBound int `json:"crossings_bound"`
}

// CrossingsSpent reports an item whose delegated crossings are gone, which is the
// counter that hands the item back to the operator: past it, more room is not the
// development manager's to give.
func (c Counters) CrossingsSpent() bool {
	return c.CrossingsBound > 0 && c.Crossings >= c.CrossingsBound
}

// Override is one recorded decision to cross this item's caps, as the durable
// triage record has it. It is declared here rather than imported from that record
// for the reason Finding is: what reaches a development manager must not change
// shape because the harness's own schema was refactored.
type Override struct {
	Budget    string    `json:"budget"`
	Cap       int       `json:"cap,omitempty"`
	Cleared   bool      `json:"cleared,omitempty"`
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason"`
	// CrossedBy is the role that crossed the cap on its own delegated authority,
	// and is empty for the operator's own hand. It is carried because a development
	// manager reading an item's budgets has to be able to tell room the operator
	// gave it from room he gave it himself: the first is an answered escalation and
	// the second counts against the crossings he has left.
	CrossedBy string `json:"crossed_by,omitempty"`
}

// Delegated reports an override a role recorded on its own authority rather than
// the operator's.
func (o Override) Delegated() bool { return strings.TrimSpace(o.CrossedBy) != "" }

// Describe says what one override did, the way the entry that carries it reads.
func (o Override) Describe() string {
	crossed := fmt.Sprintf("raised the %s cap to %d", o.Budget, o.Cap)
	if o.Cleared {
		crossed = fmt.Sprintf("cleared the %s cap", o.Budget)
	}
	decided := fmt.Sprintf("decided by %s", strings.TrimSpace(o.DecidedBy))
	if o.Delegated() {
		decided = fmt.Sprintf("crossed by the %s on delegated authority", strings.TrimSpace(o.CrossedBy))
	}
	return fmt.Sprintf("%s, %s at %s: %s",
		crossed, decided, o.DecidedAt.UTC().Format(time.RFC3339), strings.TrimSpace(o.Reason))
}

// Decided reports triage having recorded a re-run of this item that the harness
// has not carried out. It is the state that most needs saying out loud: the
// decision is already spent, so deciding a second is a second decision rather
// than a repeat of this one.
func (c Counters) Decided() bool { return c.Reruns > c.RerunsCarriedOut }

// Rerun is the re-run the harness has already claimed against one docketed
// stoppage: what a guard refuses a second of, named on the entry it is about.
// RunID is the fresh run it started, and is absent on a claim whose run never
// got as far as being reserved.
type Rerun struct {
	ClaimedAt time.Time `json:"claimed_at"`
	RunID     string    `json:"run_id,omitempty"`
}

// Committed is the round figure the budget is measured against: what this item
// has cost, or what a recorded grant has promised it, whichever is greater. The
// two are not added, for the reason the record that keeps them does not add
// them — a grant's rounds turn into counted rounds as the attempts it bought are
// judged, so a sum would charge a carried-out grant twice.
func (c Counters) Committed() int {
	if c.CommittedRounds > c.ReviewRounds {
		return c.CommittedRounds
	}
	return c.ReviewRounds
}

// RoundsUncommitted is the room the round cap has left for a decision that buys
// rounds. It is the arithmetic the guards refuse against rather than a second
// reading of the same numbers: a view measuring against the rounds counted would
// show room a recorded grant has already spoken for, which is a development
// manager deciding a repair the guard then refuses.
func (c Counters) RoundsUncommitted() int {
	if remaining := c.ReviewRoundsCap - c.Committed(); remaining > 0 {
		return remaining
	}
	return 0
}

// GrantOutstanding reports a repair grant recorded whose rounds the item has not
// spent yet. It is what Decided reports for a re-run: the decision is already
// spent, so deciding a second is a second decision rather than a repeat of this
// one.
func (c Counters) GrantOutstanding() bool { return c.CommittedRounds > c.ReviewRounds }

// Exhausted reports an item with no round left for a decision that buys one,
// which is the counter that decides something on its own: past it, another
// repair is not triage's to grant. It asks what the item is committed to rather
// than only what it has cost, because that is what the guard asks.
func (c Counters) Exhausted() bool { return c.RoundsUncommitted() == 0 }

// Entry is one durable docket entry. It is keyed rather than only identified:
// what makes two entries the same is the event they describe, not when
// something noticed it, so the run that stopped and the sweep that settles it
// afterwards docket one stoppage between them rather than two.
type Entry struct {
	SchemaVersion int              `json:"schema_version"`
	Key           string           `json:"key"`
	Class         Class            `json:"class"`
	ProductID     domain.ProductID `json:"product_id"`
	RunID         string           `json:"run_id"`
	WorkItemID    string           `json:"work_item_id"`
	// WorkItemTitle is what the item is called, carried from the run record that
	// wrote it down at claim time. An entry outlives that run, so a reader who
	// finds it has the identifier and nothing else to say what stopped unless the
	// entry says it in words. Absent means the run recorded no title, which is
	// what every run docketed before titles were carried did.
	WorkItemTitle string    `json:"work_item_title,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
	// Blocker is the durable blocker exactly as it was recorded on the work
	// item. On a publication entry it is empty: nothing blocked the item, the
	// change is integrated, and only its publication is unfinished.
	Blocker string `json:"blocker,omitempty"`
	// Failure is why the run ended, in the words of whatever ended it. It is what
	// a stopped-run entry says instead of a blocker where the death came before
	// anything could record one — a push the remote refused, a backend that broke
	// mid-attempt, a step the machine would not carry — and the run left the change
	// behind. Those deaths hand a person exactly what a blocker does: work that is
	// still there and nothing that will pick it up on its own.
	//
	// It is a field of its own rather than a blocker written after the fact,
	// because a blocker is what the work item carries and these items carry none.
	// A reader is owed that distinction: the item's own status is where they would
	// go next, and an entry that dressed a failure as a blocker would send them
	// looking for something nobody recorded.
	Failure string `json:"failure,omitempty"`
	// Findings are the reviewer's own words about the change, and Check is the
	// deterministic check that was failing. Both are absent from work that
	// stopped before either had anything to say.
	Findings []Finding `json:"findings,omitempty"`
	Check    *Check    `json:"check,omitempty"`
	// Summary is the reviewer's summary of the last review, kept beside the
	// findings because a finding list with no verdict prose reads as a set of
	// complaints rather than as a judgement.
	Summary     string       `json:"summary,omitempty"`
	Artifacts   Artifacts    `json:"artifacts"`
	Publication *Publication `json:"publication,omitempty"`
	// Unready is why dispatch declined to start this item, on the one class that
	// describes work which never ran. It carries the whole of what a development
	// manager has to decide about: what the item asks for, what the read found,
	// and who releases it.
	Unready *Unready `json:"unready,omitempty"`
	// Escalation is a role's judgement that the item cannot be met as it stands,
	// on the one class that carries one. It is written into the entry rather than
	// joined where the docket is read, for the reason the environmental refusal
	// beside it is: it is settled as the run ends, which is before the entry
	// exists.
	Escalation *Escalation `json:"escalation,omitempty"`
	// Environmental is the environment having refused this stoppage's last round,
	// when it did. It is written into the entry rather than joined where the docket
	// is read, unlike the re-run and the overrides beside it, because it is not a
	// decision made afterwards: it is settled as the run ends, which is before the
	// entry exists.
	Environmental *Environmental `json:"environmental,omitempty"`
	Counters      Counters       `json:"counters"`
	// Rerun is the re-run already claimed against this entry's own stoppage, when
	// there is one. It is joined to the entry where the docket is read rather
	// than written into the log: an entry is recorded once as the work stops, and
	// every decision about it is made afterwards, so a claim frozen into the
	// entry could only ever be absent.
	Rerun *Rerun `json:"rerun,omitempty"`
	// Overrides are the operator's own recorded decisions to cross this item's
	// caps. Every cap in Counters is already the configured one as these leave it,
	// so they are not arithmetic a reader has to do — they are the account of why a
	// budget is larger than the project configured, and who is answerable for it. A
	// development manager that saw the room and not the decision would read an
	// override as a cap they had misremembered.
	//
	// Like the re-run beside it, it is joined to the entry where the docket is read
	// rather than written into the log, and for a sharper version of the same
	// reason: an override answers the escalation this entry produced, so one frozen
	// into the entry could only ever be absent.
	Overrides []Override `json:"overrides,omitempty"`
	// CountersProblem is why the item's durable triage record could not be read
	// for this entry. It is stated rather than left to zeros, which would read as
	// an item nothing has been decided about — the one reading that turns an
	// unreadable record into a second decision nobody meant to make.
	CountersProblem string `json:"counters_problem,omitempty"`
}

// Key names the durable event an entry is about. It is derived rather than
// generated so that the same event yields the same key in every process that
// notices it, which is the whole of what makes docketing idempotent: a run stop
// is one event whether the run recorded it or a later sweep found it, and a
// publication is one event however many sweeps walk past it.
func Key(class Class, runID string) string {
	return string(class) + ":" + strings.TrimSpace(runID)
}

// PublicationKey names the publication event: the run that made the publication
// and the pull request it made. The pull request is part of the identity rather
// than only evidence carried on the entry, because "the publication of this run"
// is not by itself something anybody can point at — one run can publish more
// than once, and a reader that has to work out which publication an entry is
// about from the item's state is a reader that can work it out wrongly. A key
// built from the two durable facts is what nothing has to interpret.
func PublicationKey(runID string, number int) string {
	return Key(ClassPublication, runID) + "#" + strconv.Itoa(number)
}

// UnreadyKey names the event an unready item is: this item found unready for
// these kinds of prerequisite. There is no run in it because there is no run —
// that is what the class is — so the item and what was found are what identify
// it.
//
// The kinds are part of the identity rather than only evidence on the entry. A
// pull re-reads the queue every interval, so keying on the item alone would
// docket one item once and never say a word again when a different prerequisite
// went unmet months later; keying on the reading's wording would docket the same
// finding afresh every time somebody reworded the item. The kinds are the closed
// vocabulary in between, and they are sorted so that two readings that found the
// same things in a different order are one entry.
func UnreadyKey(workItemID string, kinds []string) string {
	sorted := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if trimmed := strings.TrimSpace(kind); trimmed != "" {
			sorted = append(sorted, trimmed)
		}
	}
	slices.Sort(sorted)
	return string(ClassUnreadyItem) + ":" + strings.TrimSpace(workItemID) + ":" + strings.Join(slices.Compact(sorted), "+")
}

// keys are the keys one entry may legitimately carry. There are two only for a
// publication, and only for what is already on disk: entries recorded before the
// pull request joined the key name the run alone, and the docket is an
// append-only log that nothing rewrites. Both are accepted where an entry is
// read and one is written where an entry is made.
func (e Entry) keys() []string {
	if e.Class == ClassUnreadyItem {
		if e.Unready == nil {
			return nil
		}
		return []string{UnreadyKey(e.WorkItemID, e.Unready.Kinds())}
	}
	derived := []string{Key(e.Class, e.RunID)}
	if e.Class == ClassPublication && e.Publication != nil {
		derived = append(derived, PublicationKey(e.RunID, e.Publication.Number))
	}
	return derived
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !e.Class.Valid() {
		problems = append(problems, fmt.Errorf("class %q must be %q, %q, %q or %q",
			e.Class, ClassStoppedRun, ClassEscalation, ClassPublication, ClassUnreadyItem))
	}
	switch key := strings.TrimSpace(e.Key); {
	case key == "":
		problems = append(problems, errors.New("key is required"))
	case len(key) > MaxKeyBytes:
		problems = append(problems, fmt.Errorf("key is %d bytes, limit is %d", len(key), MaxKeyBytes))
	case e.Class.Valid() && !slices.Contains(e.keys(), key):
		// A key that does not derive from the event it claims to describe would
		// make two records of one stoppage, which is the one thing the key exists
		// to prevent.
		problems = append(problems, fmt.Errorf("key %q does not name the %s event of run %s", e.Key, e.Class, e.RunID))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	// Every class but one describes something a run did, so the run is what the
	// entry is about. An unready item is the exception by construction: catching
	// it before dispatch is the whole point, and an entry that had to name a run
	// could only be written by the run this exists to save.
	if strings.TrimSpace(e.RunID) == "" && e.Class != ClassUnreadyItem {
		problems = append(problems, errors.New("run id is required"))
	}
	if strings.TrimSpace(e.RunID) != "" && e.Class == ClassUnreadyItem {
		problems = append(problems, errors.New("an unready item entry names no run: nothing ran, which is what the class says"))
	}
	if strings.TrimSpace(e.WorkItemID) == "" {
		problems = append(problems, errors.New("work item id is required"))
	}
	if e.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if len(e.Blocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("blocker is %d bytes, limit is %d", len(e.Blocker), MaxBlockerBytes))
	}
	if len(e.Failure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("failure is %d bytes, limit is %d", len(e.Failure), MaxBlockerBytes))
	}
	if len(e.Summary) > MaxMessageBytes {
		problems = append(problems, fmt.Errorf("summary is %d bytes, limit is %d", len(e.Summary), MaxMessageBytes))
	}
	if len(e.Findings) > MaxFindings {
		problems = append(problems, fmt.Errorf("%d findings are recorded, which exceeds the bound of %d", len(e.Findings), MaxFindings))
	}
	for index, finding := range e.Findings {
		if strings.TrimSpace(finding.Message) == "" {
			problems = append(problems, fmt.Errorf("findings[%d]: message is required", index))
		}
		if len(finding.Message) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("findings[%d]: message is %d bytes, limit is %d", index, len(finding.Message), MaxMessageBytes))
		}
		if finding.Line < 0 {
			problems = append(problems, fmt.Errorf("findings[%d]: line %d cannot be negative", index, finding.Line))
		}
	}
	if e.Check != nil {
		if strings.TrimSpace(e.Check.Command) == "" {
			problems = append(problems, errors.New("check: command is required"))
		}
		if len(e.Check.Output) > MaxCheckOutputBytes {
			problems = append(problems, fmt.Errorf("check: output is %d bytes, limit is %d", len(e.Check.Output), MaxCheckOutputBytes))
		}
	}
	if e.Environmental != nil {
		if strings.TrimSpace(e.Environmental.Cause) == "" {
			problems = append(problems, errors.New("environmental: the cause is required, because what it excuses the item is decided by which one it was"))
		}
		if len(e.Environmental.Detail) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("environmental: detail is %d bytes, limit is %d", len(e.Environmental.Detail), MaxMessageBytes))
		}
		if len(e.Environmental.Problem) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("environmental: problem is %d bytes, limit is %d", len(e.Environmental.Problem), MaxMessageBytes))
		}
		if len(e.Environmental.Account) > MaxMessageBytes {
			problems = append(problems, fmt.Errorf("environmental: account is %d bytes, limit is %d", len(e.Environmental.Account), MaxMessageBytes))
		}
	}
	// Each class is held to the evidence that makes it the thing it claims to
	// be. An entry that cannot say what stopped is an entry nobody can act on,
	// which is worse than no entry: it looks like coverage.
	switch e.Class {
	case ClassStoppedRun:
		// Either says what stopped the run, and one of them has to. A stoppage the
		// harness classified carries the blocker the work item carries; a run that
		// died before anything could classify it carries the reason it gave for
		// dying.
		if strings.TrimSpace(e.Blocker) == "" && strings.TrimSpace(e.Failure) == "" {
			problems = append(problems, errors.New("a stopped run entry carries the durable blocker that stopped it, or the failure of a death that recorded none"))
		}
		if e.Publication != nil {
			problems = append(problems, errors.New("a stopped run entry describes a run rather than a publication"))
		}
	case ClassEscalation:
		// The judgement is the whole of the entry, so an entry that cannot carry it
		// is one the development manager can read and not decide from.
		switch {
		case e.Escalation == nil:
			problems = append(problems, errors.New("an escalation entry carries the judgement that was raised"))
		default:
			// The two roles inside a run and no others. Every other role decides about
			// work rather than doing it, and an entry naming one would describe an
			// escalation nothing in a run could have raised.
			if raised := e.Escalation.RaisedBy; raised != domain.RoleDeveloper && raised != domain.RoleReviewer {
				problems = append(problems, fmt.Errorf("escalation: %q is not one of the roles that raises one, which are %q and %q",
					raised, domain.RoleDeveloper, domain.RoleReviewer))
			}
			switch reason := strings.TrimSpace(e.Escalation.Reason); {
			case reason == "":
				problems = append(problems, errors.New("escalation: the reason is required, because it is the whole of what the decision is made from"))
			case len(reason) > MaxBlockerBytes:
				problems = append(problems, fmt.Errorf("escalation: the reason is %d bytes, limit is %d", len(reason), MaxBlockerBytes))
			}
		}
		if e.Publication != nil {
			problems = append(problems, errors.New("an escalation entry describes a judgement about the item rather than a publication"))
		}
	case ClassUnreadyItem:
		switch {
		case e.Unready == nil:
			problems = append(problems, errors.New("an unready item entry carries the prerequisites the tree does not meet"))
		case len(e.Unready.Prerequisites) == 0:
			problems = append(problems, errors.New("an unready item entry names at least one unmet prerequisite: an item nothing is wrong with is one that was dispatched"))
		case len(e.Unready.Prerequisites) > MaxPrerequisites:
			problems = append(problems, fmt.Errorf("%d prerequisites are recorded, which exceeds the bound of %d", len(e.Unready.Prerequisites), MaxPrerequisites))
		}
		if e.Unready != nil {
			if e.Unready.ReadAt.IsZero() {
				problems = append(problems, errors.New("unready: read_at is required, because what the entry says is a reading of the tree at a moment rather than a standing fact"))
			}
			for index, prerequisite := range e.Unready.Prerequisites {
				if strings.TrimSpace(prerequisite.Kind) == "" {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: kind is required, because the entry's key is derived from it", index))
				}
				if strings.TrimSpace(prerequisite.Missing) == "" {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: what is missing is required", index))
				}
				if len(prerequisite.Missing) > MaxMessageBytes {
					problems = append(problems, fmt.Errorf("prerequisites[%d]: what is missing is %d bytes, limit is %d", index, len(prerequisite.Missing), MaxMessageBytes))
				}
			}
		}
		if e.Publication != nil || strings.TrimSpace(e.Blocker) != "" || strings.TrimSpace(e.Failure) != "" {
			problems = append(problems, errors.New("an unready item entry describes work that never started: there is no blocker, no failure and no publication to carry"))
		}
	case ClassPublication:
		if e.Publication == nil {
			problems = append(problems, errors.New("a publication entry carries the publication it is about"))
		} else {
			if e.Publication.Number <= 0 {
				problems = append(problems, errors.New("publication: number must be positive"))
			}
			// A merged publication is finished work unless the harness recorded
			// that finishing it did not complete — a merge it could not confirm
			// is the one merged publication that still needs a person.
			if e.Publication.Merged && strings.TrimSpace(e.Publication.Message) == "" {
				problems = append(problems, errors.New("publication: a merged publication with nothing outstanding recorded is not stuck"))
			}
			if e.Publication.ApprovedAt.IsZero() {
				problems = append(problems, errors.New("publication: approved_at is required, because the age is measured from it"))
			}
			if len(e.Publication.Message) > MaxBlockerBytes {
				problems = append(problems, fmt.Errorf("publication: message is %d bytes, limit is %d", len(e.Publication.Message), MaxBlockerBytes))
			}
		}
		if strings.TrimSpace(e.Blocker) != "" {
			problems = append(problems, errors.New("a publication entry names no blocker: the change is integrated and only its publication is unfinished"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid triage docket entry: %w", err)
	}
	return nil
}

// Render describes one entry for whoever is reading the docket. Everything a
// provider or a forge produced is indented under the harness's own line and
// never printed at the margin, exactly as a collected report is.
func (e Entry) Render() string {
	var rendered strings.Builder
	// The run is named where there is one. An unready item has none, and printing
	// an empty pair of brackets for it would read as a run whose identifier
	// nobody recorded — which is a different thing entirely, and one somebody
	// would go looking for.
	if strings.TrimSpace(e.RunID) == "" {
		fmt.Fprintf(&rendered, "  [%s] %s on %s (nothing ran)\n",
			e.Class.Title(), e.RecordedAt.UTC().Format(time.RFC3339), e.item())
	} else {
		fmt.Fprintf(&rendered, "  [%s] %s on %s (%s)\n",
			e.Class.Title(), e.RecordedAt.UTC().Format(time.RFC3339), e.item(), e.RunID)
	}
	rendered.WriteString(e.renderUnready())
	rendered.WriteString(e.renderEscalation())
	if e.Blocker != "" {
		rendered.WriteString(indented("Blocker", e.Blocker))
	}
	// Said only where there is no blocker, and labelled as what it is. A death
	// that recorded no blocker left the item carrying none, so a reader told
	// "blocker" would go to the item for words nobody wrote there; and the same
	// reason printed twice beside a blocker that already says it would be noise on
	// every ordinary stoppage.
	if e.Blocker == "" && e.Failure != "" {
		rendered.WriteString(indented("Died holding its change; the work item carries no blocker for it", e.Failure))
	}
	if e.Summary != "" {
		rendered.WriteString(indented("Review summary", e.Summary))
	}
	for _, finding := range e.Findings {
		location := ""
		if finding.File != "" {
			location = fmt.Sprintf(" (%s:%d)", finding.File, finding.Line)
		}
		rendered.WriteString(indented(fmt.Sprintf("Finding [%s]%s", finding.Severity, location), finding.Message))
	}
	if e.Check != nil {
		rendered.WriteString(indented(
			fmt.Sprintf("Failing check: %s (exit %d)", e.Check.Command, e.Check.ExitCode), e.Check.Output))
	}
	rendered.WriteString(e.renderArtifacts())
	if e.Publication != nil {
		rendered.WriteString(e.renderPublication())
	}
	// Said before the counters rather than after them, because it is what the
	// counters mean rather than a remark about them: a development manager who
	// read the figures first has already decided how close this item is to its cap.
	rendered.WriteString(e.renderEnvironmental())
	fmt.Fprintf(&rendered, "      Triage counters: %d of %s review round(s) used%s; %d repair attempt(s) spent in this run; a grant would hand it %d\n",
		e.Counters.ReviewRounds, capFigure(e.Counters.ReviewRoundsCap), roundsNote(e.Counters),
		e.Counters.RepairAttempts, e.Counters.RepairGrantAttempts)
	rendered.WriteString(e.renderDecisions())
	return rendered.String()
}

// renderUnready says what the item asks of the tree that the tree does not have,
// and who releases each of them. It is silent on every entry that describes a
// run, which is nearly all of them.
//
// When the tree was read is said with it, and said as a reading rather than as a
// fact: everything else on a docket is an event that happened and stays
// happened, and this is the one entry whose subject can become false without
// anybody touching the item.
func (e Entry) renderUnready() string {
	if e.Unready == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was dispatched: the tree was read at %s and does not meet what this item asks for.\n",
		e.Unready.ReadAt.UTC().Format(time.RFC3339))
	for _, prerequisite := range e.Unready.Prerequisites {
		rendered.WriteString(indented("Unmet ["+prerequisite.Kind+"]", prerequisite.Missing))
		if evidence := strings.TrimSpace(prerequisite.Evidence); evidence != "" {
			rendered.WriteString(indented("The read that says so", evidence))
		}
		if decides := strings.TrimSpace(prerequisite.Decides); decides != "" {
			rendered.WriteString(indented("Who releases it", decides))
		}
	}
	return rendered.String()
}

// renderEscalation says which role judged the item unmeetable and what it said,
// and it says what the entry is asking for: a decision about the item rather
// than about a change, because there is no change to decide about.
//
// It names what the escalation cost as well, and that is the half a reader would
// otherwise supply wrongly. Every other entry on this docket is work that spent
// its budget before anybody heard about it, so the counters below read as a
// nearly-spent item by default; an escalation is raised in the round it was
// reached, which is what makes replanning it still affordable.
//
// It is silent on every entry that is not one, which is nearly all of them.
func (e Entry) renderEscalation() string {
	raised := e.Escalation
	if raised == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Nothing was integrated: the %s judged this item cannot be met as it stands, in the round it reached, and raised it for your decision — replan, park, resequence, or redirect.\n",
		raised.RaisedBy.Title())
	rendered.WriteString(indented("Why the "+raised.RaisedBy.Title()+" says it cannot be met", raised.Reason))
	return rendered.String()
}

// renderDecisions says what triage has already decided about this item, in the
// figures the guards will be read against. It is never silent: a decision the
// guard would refuse a second of and an entry that shows nothing recorded are
// how one authorized recovery is nearly spent twice, once by the development
// manager and once by whoever is helping them.
func (e Entry) renderDecisions() string {
	if e.CountersProblem != "" {
		return indented("Triage decisions could not be read",
			e.CountersProblem+"\nThis says nothing about what has been decided: read the record before deciding anything that spends a budget.")
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Triage decisions recorded: %d of %s repair grant(s)%s; %d of %s re-run(s), %d carried out; %d of %s merge re-arm(s)\n",
		e.Counters.RepairGrants, capFigure(e.Counters.RepairGrantsCap), grantedNote(e.Counters),
		e.Counters.Reruns, capFigure(e.Counters.RerunsCap), e.Counters.RerunsCarriedOut,
		e.Counters.MergeRearms, capFigure(e.Counters.MergeRearmsCap))
	rendered.WriteString(e.renderOverrides())
	rendered.WriteString(e.renderCrossingStanding())
	rendered.WriteString(e.renderGrantStanding())
	rendered.WriteString(e.renderRerunStanding())
	rendered.WriteString(e.renderRearmStanding())
	return rendered.String()
}

// renderEnvironmental says the environment refused this stoppage's last round,
// and what the item was therefore not charged for it. It is silent on every
// ordinary stoppage, which is nearly all of them.
//
// A refusal whose return could not be written says so in the same breath. That
// is the one state where the counters below it are higher than what the item
// actually cost and nothing has corrected them, and a reader who is not told
// will decide an escalation against a figure the harness knows is wrong.
func (e Entry) renderEnvironmental() string {
	refused := e.Environmental
	if refused == nil {
		return ""
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Round: %s\n", nonEmpty(refused.Account,
		fmt.Sprintf("environmental cause recorded: %s; what it cost this item is not recorded on this entry", refused.Cause)))
	if detail := strings.TrimSpace(refused.Detail); detail != "" {
		rendered.WriteString(indented("What the harness found", detail))
	}
	if problem := strings.TrimSpace(refused.Problem); problem != "" {
		rendered.WriteString(indented("This round could not be paid back in full",
			problem+"\nThe counters below therefore include a round this item did not cost: read them as higher than the item stands at."))
	}
	return rendered.String()
}

// renderOverrides says which of this item's caps an operator crossed, and who
// crossed them. The budgets above are already stated as the overrides leave them,
// which is exactly why this cannot be left out: a development manager reading
// room the configured cap does not have, with nothing saying where it came from,
// is a development manager deciding whether to trust the number.
//
// It is silent on the ordinary item, which is every item. An override is an
// operator answering an escalation by hand, and a line announcing that nobody has
// had to is one every reader learns to skip.
func (e Entry) renderOverrides() string {
	var rendered strings.Builder
	for _, override := range e.Overrides {
		// The two are labelled apart because they answer different questions. An
		// operator override is somebody else having answered an escalation, and a
		// delegated crossing is the development manager's own earlier decision about
		// this item — which is the best evidence there is about whether crossing it
		// again will help, and it is one of five.
		label := "Operator override"
		if override.Delegated() {
			label = "Cap crossed on delegated authority"
		}
		fmt.Fprintf(&rendered, "      %s: %s\n", label, override.Describe())
	}
	return rendered.String()
}

// renderCrossingStanding says where this item stands with the caps the
// development manager may cross himself. It is silent until one has been
// crossed, for the reason the re-arm standing is: an untouched delegation
// announced on every ordinary stoppage is a line every reader learns to skip.
//
// Once one has been crossed it is never silent again, and the exhausted case says
// what happens instead rather than only that the room is gone: an item past the
// bound is the operator's, and a development manager who learns that by being
// refused has spent a turn finding out something the entry could have told them.
func (e Entry) renderCrossingStanding() string {
	counters := e.Counters
	if counters.Crossings == 0 {
		return ""
	}
	if counters.CrossingsSpent() {
		return fmt.Sprintf("      A further cap crossing for %s is not yours: %d of %d crossing(s) on your own authority are recorded, and past the bound the caps are the operator's again. Escalate, and say which cap and why.\n",
			e.WorkItemID, counters.Crossings, counters.CrossingsBound)
	}
	return fmt.Sprintf("      %d of %s cap crossing(s) on your own authority are recorded against %s; each further one is reported to the operator as you record it.\n",
		counters.Crossings, capFigure(counters.CrossingsBound), e.WorkItemID)
}

// capFigure is one ceiling as a reader reads it. A cleared cap stands at a number
// nothing this harness counts comes near, so printing the number would be a line
// a development manager has to decode before they can act on it. Who cleared it
// is not repeated here: the override line beside these figures says so, and every
// budget on the entry would otherwise carry the same clause.
func capFigure(limit int) string {
	if limit == CapCleared {
		return "no cap"
	}
	return strconv.Itoa(limit)
}

// renderGrantStanding says where this item stands with the repair grants triage
// may still give it, in the figures the grant guard reads. The counts above
// cannot answer it: the grant counter is a total nothing clears, and the round
// budget refuses against what the item is committed to rather than what it has
// cost, so an entry that stopped at the counts leaves a development manager to
// find out by deciding a repair and being refused. Which is the round-trip this
// says out loud instead.
func (e Entry) renderGrantStanding() string {
	counters := e.Counters
	if counters.RepairGrants == 0 {
		return ""
	}
	spentGrants := counters.RepairGrants >= counters.RepairGrantsCap
	spentRounds := counters.RoundsUncommitted() == 0
	// Both at once is said as both, rather than as the first of them. A grant
	// stands behind two budgets, and an entry that named one sent an operator to
	// cross it and the same decision back to be refused by the other — which cost
	// two override ceremonies minutes apart on each of two items on 2026-09-05.
	if spentGrants && spentRounds {
		return fmt.Sprintf("      A further repair grant for %s is refused by both of its budgets: %d of %d permitted grant(s) are already recorded, and %d of %d round(s) are spent or committed. Crossing either one alone leaves the other refusing it.\n",
			e.WorkItemID, counters.RepairGrants, counters.RepairGrantsCap, counters.Committed(), counters.ReviewRoundsCap)
	}
	if spentGrants {
		return fmt.Sprintf("      A further repair grant for %s is refused: %d of %d permitted grant(s) are already recorded, so deciding another spends nothing and is an escalation rather than a larger budget.\n",
			e.WorkItemID, counters.RepairGrants, counters.RepairGrantsCap)
	}
	if spentRounds {
		return fmt.Sprintf("      A further repair grant for %s is refused by the review round budget: %d of %d round(s) are spent or committed, so there is nothing left to grant.\n",
			e.WorkItemID, counters.Committed(), counters.ReviewRoundsCap)
	}
	if counters.GrantOutstanding() {
		return fmt.Sprintf("      A repair grant of %s is recorded and its rounds are not spent, so this stoppage may be handed back on the decision that stands; deciding another would spend a further one rather than repeat it.\n",
			e.WorkItemID)
	}
	return ""
}

// renderRearmStanding says when the merge re-arms are gone. It is silent until
// one has been recorded, because a re-arm is not the decision a stopped run is
// about and an entry that announced an untouched budget on every stoppage would
// be a line every reader learns to skip.
func (e Entry) renderRearmStanding() string {
	if e.Counters.MergeRearms == 0 || e.Counters.MergeRearms < e.Counters.MergeRearmsCap {
		return ""
	}
	return fmt.Sprintf("      A further merge re-arm for %s is refused: %d of %d permitted re-arm(s) are already recorded, and a merge the forge keeps dropping is a repository somebody has to look at.\n",
		e.WorkItemID, e.Counters.MergeRearms, e.Counters.MergeRearmsCap)
}

// renderRerunStanding says where this stoppage stands with the one re-run it
// gets, which is the question the counters above cannot answer on their own: the
// re-run counter is a total nothing clears, so what it means for this entry
// depends on what has been claimed against it.
func (e Entry) renderRerunStanding() string {
	if e.Rerun != nil {
		if e.Rerun.RunID == "" {
			return fmt.Sprintf("      This stoppage was already re-run at %s and no fresh run was recorded for it; triage re-runs a docketed stoppage once, so another is refused.\n",
				e.Rerun.ClaimedAt.UTC().Format(time.RFC3339))
		}
		return fmt.Sprintf("      This stoppage was already re-run as run %s; triage re-runs a docketed stoppage once, so another is refused.\n", e.Rerun.RunID)
	}
	if e.Counters.Decided() {
		return fmt.Sprintf("      A re-run of %s is already recorded and not yet carried out, so this stoppage may be run again on the decision that stands; deciding another would spend a further one rather than repeat it.\n",
			e.WorkItemID)
	}
	if e.Counters.Reruns > 0 {
		return fmt.Sprintf("      Every recorded re-run of %s has been carried out, so this stoppage has no decision of its own to act on; a further one is refused past the cap and is an escalation rather than a larger budget.\n",
			e.WorkItemID)
	}
	return ""
}

// item names the work an entry is about: the identifier, which is what the
// development manager acts on, and what the item is called, which is what tells
// them what stopped without their going to the tracker for it. An entry whose
// run recorded no title names the identifier alone.
func (e Entry) item() string {
	if title := strings.TrimSpace(e.WorkItemTitle); title != "" {
		return e.WorkItemID + " — " + title
	}
	return e.WorkItemID
}

func (e Entry) renderArtifacts() string {
	var rendered strings.Builder
	if e.Artifacts.Branch != "" {
		state := "preserved"
		if e.Artifacts.BranchRemoved {
			state = "removed"
		}
		fmt.Fprintf(&rendered, "      Branch (%s): %s\n", state, e.Artifacts.Branch)
	}
	if e.Artifacts.WorktreePath != "" {
		state := "preserved"
		if e.Artifacts.WorktreeRemoved {
			state = "removed"
		}
		fmt.Fprintf(&rendered, "      Worktree (%s): %s\n", state, e.Artifacts.WorktreePath)
	}
	if e.Artifacts.TargetBranch != "" {
		fmt.Fprintf(&rendered, "      Integration target: %s\n", e.Artifacts.TargetBranch)
	}
	return rendered.String()
}

func (e Entry) renderPublication() string {
	published := *e.Publication
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      Pull request #%d %s\n", published.Number, published.URL)
	merge := "the forge reports it unmerged"
	switch {
	case published.Merged:
		merge = "the forge merged it and the harness could not finish the publication"
	case published.MergeQueued:
		merge = "the forge has its merge queued"
	}
	fmt.Fprintf(&rendered, "      Forge state: %s, %s\n", nonEmpty(published.State, "unreported"), merge)
	if published.MergeCommit != "" {
		fmt.Fprintf(&rendered, "      Forge merge commit: %s\n", published.MergeCommit)
	}
	// The age is what made this an entry, so it is stated as an age rather than
	// left to be worked out from two timestamps.
	if !published.ApprovedAt.IsZero() {
		fmt.Fprintf(&rendered, "      Approved at %s, unmerged for %s when docketed\n",
			published.ApprovedAt.UTC().Format(time.RFC3339), describeAge(e.RecordedAt.Sub(published.ApprovedAt)))
	}
	if published.Message != "" {
		rendered.WriteString(indented("Forge merge message", published.Message))
	}
	return rendered.String()
}

// roundsNote qualifies the rounds an item has used with what it stands
// committed to, where the two differ. The rounds counted are what the item has
// cost and the commitment is what the cap refuses against, so an entry that
// stated the first alone reports room the guard does not have for exactly as
// long as a grant is waiting to be spent — which is the whole of the window a
// development manager reads a docket in.
func roundsNote(counters Counters) string {
	committed := counters.Committed()
	switch {
	case counters.Exhausted() && counters.GrantOutstanding():
		return fmt.Sprintf(" (%d of %s are committed by a grant not yet spent, so the cap is reached: another repair is not triage's to grant)",
			committed, capFigure(counters.ReviewRoundsCap))
	case counters.Exhausted():
		return " (the cap is reached: another repair is not triage's to grant)"
	case counters.GrantOutstanding():
		return fmt.Sprintf(" (%d of %s are committed by a grant not yet spent)", committed, capFigure(counters.ReviewRoundsCap))
	default:
		return ""
	}
}

// grantedNote is what the recorded grants actually came to, which the count of
// them does not say. A grant the round cap cut is the fact that says this item
// is at the end of what it will be given, and it is reported to whoever records
// the decision at the moment they record it — so an entry that dropped it is an
// entry that disagrees with what the harness already told them.
func grantedNote(counters Counters) string {
	if counters.GrantedRounds == 0 {
		return ""
	}
	if counters.TruncatedGrants > 0 {
		return fmt.Sprintf(" worth %d review round(s), %d of them cut down to the room the cap still had",
			counters.GrantedRounds, counters.TruncatedGrants)
	}
	return fmt.Sprintf(" worth %d review round(s)", counters.GrantedRounds)
}

// indented writes one labelled block of provider or forge prose under the
// entry's own line, so multi-line evidence never reads as harness output.
func indented(label, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "      " + label + "\n"
	}
	lines := strings.Split(trimmed, "\n")
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "      %s: %s\n", label, strings.TrimSpace(lines[0]))
	for _, line := range lines[1:] {
		fmt.Fprintf(&rendered, "        %s\n", strings.TrimRight(line, " \t"))
	}
	return rendered.String()
}

// describeAge states a duration the way somebody reads it rather than the way
// Go prints it: a publication that has been sitting for two days is not
// "48h0m0s" to anybody deciding what to do about it.
func describeAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	if age < time.Minute {
		return strconv.Itoa(int(age.Seconds())) + "s"
	}
	if age < time.Hour {
		return strconv.Itoa(int(age.Minutes())) + "m"
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(age.Hours()), int(age.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(age.Hours())/24, int(age.Hours())%24)
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
