package orchestrator

// Docketing the work that has stopped moving.
//
// Two things stop work in a way no further attempt of the harness resolves. A
// run stops with its change still there — on a durable blocker the harness
// recorded, or by dying before anything could record one — and an approved
// change is published to a forge that then never merges it. The first is an
// event the harness is present for, so it is docketed where it happens. The
// second is the absence of an event, which nothing can be present for, so it is
// found by a scan: the reconciling sweep, and the build that runs when the
// development manager opens a conversation.
//
// The two shapes of the first are one class here because they are one fact to
// whoever reads them: the work is still there and nothing is going to pick it up.
// What separates them is only whether the harness got as far as saying so, and
// stoppedRun below says why that must not decide who hears about it.
//
// The scan is deliberately not a scheduled process, because there is none to
// hang it on. What that costs is stated rather than hidden: the configured
// stuck-merge age is a floor and not a promise. A publication becomes
// docketable at that age and is docketed the next time something scans, which
// on a quiet system is the next sweep or the next conversation.
//
// Both paths converge on one idempotent write keyed to the event, so a run that
// dockets its own stoppage and a sweep that settles the same run afterwards
// produce one entry between them rather than two accounts of one stoppage.
//
// A third thing stops work before it moves at all, and it is docketed here for
// the same reason as the other two rather than for a new one. An item whose own
// statement asks for something the tree does not have is work nothing is going
// to pick up either, and the only way anybody found that out was to spend a run
// on it — four times in a fortnight. RecordUnreadyItem is that finding made
// where it costs a read, and it is the one entry on this docket with no run
// behind it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Docket is the durable docket entries are recorded into and read back from.
// It is satisfied by runstate.DocketStore.
type Docket interface {
	RecordOnce(entry triage.Entry) (bool, error)
	List() ([]triage.Entry, error)
}

// DocketRuns is the run evidence a docket is built from: every record the
// harness holds. Reading them decides nothing about any run, which is what lets
// a build run beside whatever else is happening.
type DocketRuns interface {
	Recorded() ([]runstate.State, error)
}

// DocketDecisions is the durable per-item record of what triage has decided and
// what the item has cost, which is the record the triage guards spend and refuse
// against. The docket reads it rather than counting the same things again from
// the runs: a second count is a second answer, and an entry that reports one
// while the guard enforces the other is a decision made against numbers that do
// not exist.
//
// It is satisfied by runstate.TriageStore.
type DocketDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// DocketReruns is what the harness has carried out of those decisions: the
// re-runs claimed for one work item, one per docketed stoppage. It is the other
// half of the re-run gate — a decision authorizes one re-run and a claim is what
// says it was acted on — so an entry that showed the decisions without the claims
// would say a stoppage may be run again where the guard refuses it.
//
// It is satisfied by runstate.RerunStore.
type DocketReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
}

// Docketer makes and reads the triage docket. It has no tracker, no worktree
// access, and no forge access, and that is the point: docketing is a statement
// that work stopped, assembled from evidence somebody already recorded. What to
// do about a docketed entry is the development manager's, and nothing here can
// claim, repair, escalate, or retire anything.
type Docketer struct {
	Docket Docket
	Runs   DocketRuns
	// Decisions is the item's durable triage record. Required: every entry
	// reports what triage has already decided about its item, and an entry built
	// without that record would report an item nobody had decided anything about,
	// which is indistinguishable from one whose recovery is already authorized.
	Decisions DocketDecisions
	// Reruns is what has been carried out of those decisions. Required to read the
	// docket, for the same reason: a stoppage whose re-run has been claimed and
	// one whose decision is still waiting are opposite answers to the question the
	// development manager is about to ask.
	Reruns DocketReruns
	// Caps are the ceilings the guards refuse against, as the caller assembled
	// them for every other reader of the same record. They are reported beside
	// what has been spent, because a count with no ceiling beside it says nothing
	// about whether the next decision will be refused.
	Caps runstate.TriageCaps
	// Triage is what the docket measures against: the age past which an unmerged
	// publication is stuck, and the budgets every entry reports beside what the
	// item has already spent.
	Triage config.Triage
	// ProductID is which product an entry belongs to, and is required only by the
	// one entry that is not made from a run record. Every other entry takes it
	// from the run, which is the more reliable source and stays the source: this
	// is here because an item dispatch declined to start has no run to take it
	// from, not because the product is a thing this decides.
	ProductID domain.ProductID
	Clock     execution.Clock
}

// DocketBuild is what one build found: the whole docket as it now stands, and
// how many entries this build is what created. The count is reported rather
// than the entries themselves because a build is not a notification — an entry
// created by this build and one created by last week's sweep are the same
// standing fact to whoever reads the docket.
type DocketBuild struct {
	Entries []triage.Entry `json:"entries"`
	Added   int            `json:"added"`
}

// Build scans every recorded run, dockets what has stopped and is not docketed
// yet, and returns the docket as it stands. It is safe to repeat and safe to
// run concurrently with anything: every write is keyed to the event it
// describes, so a build that races another build records the same entries and
// the docket collapses them.
//
// A run whose record cannot supply an entry is skipped rather than failing the
// build: a docket that refuses to be read because one run is odd is a docket
// nobody sees, and the runs beside it are exactly the ones somebody needs.
//
// What comes back is joined to the triage record as it stands now, not as it
// stood when each entry was written. An entry is recorded once, as the work
// stops, and every decision about it is made afterwards — so a build that
// returned the entries as recorded would show every decision as absent, which is
// what let one authorized re-run be about to be decided a second time.
func (d Docketer) Build() (DocketBuild, error) {
	if err := d.validate(); err != nil {
		return DocketBuild{}, err
	}
	if d.Reruns == nil {
		return DocketBuild{}, errors.New("reading the docket requires the re-runs already carried out, so an entry never shows a stoppage as re-runnable that the harness would refuse")
	}
	recorded, err := d.Runs.Recorded()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the recorded runs to build the triage docket: %w", err)
	}
	// What is already docketed is read once rather than once per candidate. The
	// store refuses a repeated key anyway; this is what stops a build over a long
	// history from re-reading the whole docket for every run in it.
	docketed, err := d.Docket.List()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the triage docket: %w", err)
	}
	already := make(map[string]bool, len(docketed))
	for _, entry := range docketed {
		already[entry.Key] = true
	}
	now := d.now()
	added := 0
	var problems []error
	for _, state := range recorded {
		entries, err := d.entriesFor(state, now, already)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, entry := range entries {
			created, err := d.Docket.RecordOnce(entry)
			if err != nil {
				problems = append(problems, fmt.Errorf("docket %s of run %s: %w", entry.Class, state.RunID, err))
				continue
			}
			if created {
				added++
			}
		}
	}
	entries, err := d.Docket.List()
	if err != nil {
		problems = append(problems, fmt.Errorf("read the triage docket: %w", err))
		return DocketBuild{Added: added}, errors.Join(problems...)
	}
	problems = append(problems, d.joinDecisions(entries)...)
	return DocketBuild{Entries: entries, Added: added}, errors.Join(problems...)
}

// joinDecisions puts the triage record as it now stands onto every entry: what
// has been decided about the item, what the caps will refuse the next one
// against, and what has already been claimed against this entry's own stoppage.
//
// The record is read once per work item rather than once per entry, because one
// item's stoppages share one record and a docket over a long history would
// otherwise read the same file repeatedly.
//
// An item whose record cannot be read says so on its entries and is reported as
// a problem, and its counters are left as the entry recorded them rather than
// blanked: a zero nobody could distinguish from a fresh item is the reading that
// spends a decision twice.
func (d Docketer) joinDecisions(entries []triage.Entry) []error {
	var problems []error
	read := make(map[string]itemDecisions, len(entries))
	for index := range entries {
		entry := &entries[index]
		decisions, seen := read[entry.WorkItemID]
		if !seen {
			decisions = d.decisionsFor(entry.WorkItemID)
			read[entry.WorkItemID] = decisions
			if decisions.problem != nil {
				problems = append(problems, decisions.problem)
			}
		}
		if decisions.problem != nil {
			entry.CountersProblem = decisions.problem.Error()
			continue
		}
		// Whatever a previous reader could not do is not this reader's answer: the
		// record was read, so the entry carries what it says and nothing about the
		// reading of it.
		entry.CountersProblem = ""
		// The repair attempts stay as the entry recorded them: they are what the
		// stopped run spent of its own budget, which is evidence about that run
		// rather than a figure any guard reads.
		entry.Counters = d.counters(decisions.counters, entry.Counters.RepairAttempts, len(decisions.claimed))
		entry.Rerun = rerunOf(*entry, decisions.claimed)
		// Joined here and never written, exactly as the re-run above is: an override
		// answers the escalation this entry produced, so it is always made after the
		// entry exists.
		entry.Overrides = docketedOverrides(decisions.counters.Overrides)
	}
	return problems
}

// itemDecisions is one work item's triage record as the guards read it: what has
// been decided, what has been carried out, and what stopped either being read.
type itemDecisions struct {
	counters runstate.TriageCounters
	claimed  []runstate.Rerun
	problem  error
}

func (d Docketer) decisionsFor(workItemID string) itemDecisions {
	counters, err := d.Decisions.Counters(workItemID)
	if err != nil {
		return itemDecisions{problem: fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)}
	}
	claimed, err := d.Reruns.Claimed(workItemID)
	if err != nil {
		return itemDecisions{problem: fmt.Errorf("read the re-runs already carried out for %s: %w", workItemID, err)}
	}
	return itemDecisions{counters: counters, claimed: claimed}
}

// rerunOf is the re-run claimed against one entry's own stoppage, which is what
// the once-per-stoppage guard refuses a second of. It is matched on the docket
// key rather than on the run or the item, because the key is what the claim was
// taken under.
func rerunOf(entry triage.Entry, claimed []runstate.Rerun) *triage.Rerun {
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return &triage.Rerun{ClaimedAt: existing.ClaimedAt, RunID: existing.RunID}
		}
	}
	return nil
}

// RecordStoppedRun dockets one run that stopped with its change still there, at
// the moment it ended. It reports whether this call is what created the entry, so
// a caller can tell docketing a stoppage from finding it already docketed.
//
// It is the wider of the two questions asked in this file. A run that ended on a
// durable blocker is docketed here and re-derived by the scan; a run that died
// before anything could record one is docketed here and nowhere else, for the
// reason preservedDeath states. This is the only place both are asked, and it is
// called wherever a run becomes terminal — by the pipeline as it fails one, and
// by the sweep as it settles one — which is what makes "at the moment it ended"
// true of either.
//
// A run that neither dockets anything and is not an error: that is every run that
// ended for a reason nobody has to decide about, which is most of them.
func (d Docketer) RecordStoppedRun(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	if !stoppedRun(state) && !preservedDeath(state) {
		return false, nil
	}
	entry, err := d.stoppedRunEntry(state, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// RecordUnreadyItem dockets one item dispatch declined to start because the tree
// does not meet a prerequisite it states. It reports whether this call is what
// created the entry, so a caller can tell docketing a finding from finding it
// already docketed — which for a watching session is nearly every pull, since
// the queue is re-read every interval and the item is still unready.
//
// It is the one entry here made about work that never ran, and it takes the item
// and the reading rather than a run record because there is no run: the whole
// value of catching this is that it cost a read. What that means for the entry's
// other halves is stated in the entry rather than left to be noticed — nothing
// was preserved, nothing failed, and the counters are the item's own history
// rather than anything this finding spent.
//
// An item that meets everything it states dockets nothing and is not an error,
// which is nearly every item.
func (d Docketer) RecordUnreadyItem(item beads.WorkItem, unmet []readiness.Unmet) (bool, error) {
	if d.Docket == nil {
		return false, errors.New("a triage docket is required to route an unready item to it")
	}
	if len(unmet) == 0 {
		return false, nil
	}
	entry, err := d.unreadyEntry(item, unmet, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

func (d Docketer) unreadyEntry(item beads.WorkItem, unmet []readiness.Unmet, now time.Time) (triage.Entry, error) {
	read := now.UTC()
	prerequisites := make([]triage.Prerequisite, 0, len(unmet))
	for _, one := range unmet {
		prerequisites = append(prerequisites, triage.Prerequisite{
			Kind:     string(one.Kind),
			Missing:  one.Missing,
			Evidence: one.Evidence,
			Decides:  one.Decides,
		})
	}
	if len(prerequisites) > triage.MaxPrerequisites {
		prerequisites = prerequisites[:triage.MaxPrerequisites]
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.UnreadyKey(item.ID, readiness.Kinds(unmet)),
		Class:         triage.ClassUnreadyItem,
		ProductID:     d.ProductID,
		WorkItemID:    item.ID,
		WorkItemTitle: item.Title,
		RecordedAt:    read,
		Unready:       &triage.Unready{Prerequisites: prerequisites, ReadAt: read},
	}
	// The counters are read for the reason every other entry reads them: what a
	// development manager may still decide about this item is the same question
	// whether the item stopped or never started, and an entry showing zeros is
	// indistinguishable from an item nobody has decided anything about. A record
	// that will not read says so on the entry rather than failing the finding,
	// because a finding nobody is told at all is worse than one whose budget line
	// is missing.
	entry.Counters, entry.CountersProblem = d.unreadyCounters(item.ID)
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket %s as unready: %w", item.ID, err)
	}
	return entry, nil
}

// entriesFor is what one run record contributes to the docket, leaving out
// what is already on it. A run can contribute both: a stoppage is about the run
// and a stuck publication is about what the forge did with its work, and one
// run can have done both.
func (d Docketer) entriesFor(state runstate.State, now time.Time, already map[string]bool) ([]triage.Entry, error) {
	var entries []triage.Entry
	var problems []error
	if stoppedRun(state) && !already[triage.Key(triage.ClassStoppedRun, state.RunID)] {
		entry, err := d.stoppedRunEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	if publicationDocketed(state, already) {
		return entries, errors.Join(problems...)
	}
	if stuckPublication(state, now, d.Triage.StuckMergeAge.Duration()) {
		entry, err := d.publicationEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	return entries, errors.Join(problems...)
}

// publicationDocketed reports this run's publication already being on the
// docket. Both keys it can carry are asked: an entry made before the pull
// request joined the key names the run alone, the log is append-only and nothing
// rewrites it, so a build that asked only the current key would docket every one
// of those a second time.
func publicationDocketed(state runstate.State, already map[string]bool) bool {
	if already[triage.Key(triage.ClassPublication, state.RunID)] {
		return true
	}
	published := state.PullRequest
	return published != nil && already[triage.PublicationKey(state.RunID, published.Number)]
}

// stoppedRun reports a run that ended on a durable blocker. Both halves matter.
// The blocker is what says a person has to decide something, and the terminal
// status is what says nobody is going to: a run that is still going, or parked
// waiting out a provider, an operator, or a directive, is owed a continuation
// and is not stopped work at all — every one of those pauses is recorded on a
// run that is still running, which is exactly what this excludes.
//
// It is the whole of what the scan below re-derives, and deliberately not the
// whole of what is docketed: preservedDeath is the other half, and is recorded
// where the death happens rather than found by walking the history.
func stoppedRun(state runstate.State) bool {
	return state.Blocker != "" && state.Status.Terminal()
}

// preservedDeath reports a run that died holding its change, with nothing having
// classified the death as a stoppage.
//
// The harness has two routes to a terminal record and they disagreed about these.
// A run whose process was killed is settled by a sweep, which blocks the item
// when the artifacts survive and dockets the stoppage off that blocker. A run
// that fails inside its own process records an outcome note instead — no blocker,
// and so no entry — because the harness may yet resume it, and a blocked item is
// one it would refuse to resume. That is the right call for the record and the
// wrong one for the docket: once the run is terminal nothing resumes it, and the
// change is still sitting on a branch with no recorded way to decide about it.
// `yoyo triage rerun` and `yoyo triage repair` both act on a docketed stoppage,
// so a push the remote refused (yoyodyne-ifd.242, yoyodyne-ifd.267) or a backend
// that broke mid-attempt (yoyodyne-ifd.209.6) left the development manager's
// recorded decision unexecutable, and the exit each time was a person dispatching
// the item by name — which starts a run and records no decision at all.
//
// Three conditions narrow it, and each excludes a run that is not this. A status
// other than failed is a run something stopped rather than one that died: an
// operator's stop and a deadline are both deliberate, and neither hands anybody a
// decision. A run that integrated its work is reconciliation's to finish, and an
// unfinished publication of it is docketed as the publication it is. And a run
// that left nothing behind is the harness having failed to carry it, which is a
// failure to read rather than work to decide about.
//
// # Why only where the death happens
//
// This is asked as a run ends and never by the scan that walks the recorded
// history, which is the one asymmetry here worth stating because it looks like an
// oversight. A blocker is a durable classification that stands on a record
// forever, so re-deriving it costs nothing: a run that carried one always did. A
// death that preserved its change is not — it is a shape every terminal failed run
// with a surviving branch has, including every record written before the harness
// carried a blocker at all, which the run schema says is most of them. Re-deriving
// it over the history would have docketed 48 of the 374 runs this repository had
// recorded on 2026-09-04, nearly all of them long-closed work, in one build; the
// development manager's context lists 25 entries newest first, so that one build
// would have hidden every stoppage she actually had to decide about behind months
// of settled ones. A docket read as complete when it is not is worse than one that
// says what it could not show, and this is the version of that failure the docket
// itself would have caused.
//
// What it costs is that a death whose docket write fails is not picked up later,
// where a blocker would be. That is reported by the run that could not write it,
// and is the same trade the scan makes everywhere else: the run's record still
// says what happened, and nothing about the change is lost.
func preservedDeath(state runstate.State) bool {
	return state.Status == runstate.StatusFailed &&
		state.Integration == nil &&
		strings.TrimSpace(state.Failure) != "" &&
		state.Artifacts().Preserved()
}

// stuckPublication reports an approved publication that did not finish and is
// not going to without somebody looking at it. There are two kinds, and they
// are one class because they need the same thing from the same reader.
//
// The first is a publication the harness already recorded as outstanding: a
// merge the forge dropped because a requirement of the base branch went unmet,
// which the harness never merges past, or one the forge performed that the
// harness could not then confirm. Nothing about either changes with time, so
// both are docketable the moment they are recorded.
//
// The second is a publication nothing has recorded anything about, which is
// what a merge that is simply not happening looks like. Nothing happened to it,
// so there is no event to hang a deadline on, and it is docketed on its age
// instead — measured from when the run that made it ended, which is when it
// became something waiting on the forge.
func stuckPublication(state runstate.State, now time.Time, stuckMergeAge time.Duration) bool {
	published := state.PullRequest
	if published == nil {
		return false
	}
	// A run still in flight owns its own publication, and a parked one is owed
	// the rest of its own step. Neither is work that has stopped.
	if !state.Status.Terminal() {
		return false
	}
	// Only an approved publication is stuck. A pull request from a run that was
	// never approved is a branch nobody authorized merging, and the run's own
	// blocker is what says so.
	if state.ReviewDecision != runstate.ReviewApprove {
		return false
	}
	// An outstanding publication the harness recorded is docketable whichever
	// way the merge went, and there are two of those: a merge the forge dropped,
	// and one it performed that the harness could not confirm. The second is the
	// reason this is asked before the merged publication below is dismissed —
	// the merge happened and the publication still did not finish.
	if state.PublishFailure != "" {
		return true
	}
	// Past that, a merged publication is finished work.
	if published.Merged {
		return false
	}
	// A threshold of no time at all would docket every publication the instant
	// it was made, which the configuration refuses; this refuses to act on one
	// anyway, because a docket built from a configuration nobody validated must
	// not be a docket of everything.
	if stuckMergeAge <= 0 {
		return false
	}
	return now.Sub(publicationApprovedAt(state)) >= stuckMergeAge
}

// publicationApprovedAt is when the publication became something waiting on the
// forge. It is the moment the run ended rather than the moment the record was
// last touched: a sweep that walks past a stuck publication and writes nothing
// must not be able to reset its age, and one that does write must not either.
func publicationApprovedAt(state runstate.State) time.Time {
	if state.CompletedAt != nil {
		return *state.CompletedAt
	}
	return state.UpdatedAt
}

func (d Docketer) stoppedRunEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.recordedCounters(state)
	if err != nil {
		return triage.Entry{}, err
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, state.RunID),
		Class:         triage.ClassStoppedRun,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		RecordedAt:    now.UTC(),
		Blocker:       state.Blocker,
		// The reason the run gave for dying is carried only where it is what makes
		// this a stoppage. Every other entry has a blocker that says the same thing
		// in the words the work item carries, and printing the failure beside it
		// would be the same fact twice on every ordinary stoppage.
		Failure:       docketFailure(state),
		Summary:       state.ReviewSummary,
		Findings:      docketFindings(state.ReviewFindingDetails),
		Check:         docketCheck(state.CheckFailure),
		Artifacts:     docketArtifacts(state),
		Environmental: docketEnvironmental(state.Environmental),
		Counters:      counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the stoppage of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

func (d Docketer) publicationEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.recordedCounters(state)
	if err != nil {
		return triage.Entry{}, err
	}
	published := *state.PullRequest
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		// The publication is keyed to the run and the pull request together, so
		// what an entry is about is two durable facts a reader can check rather
		// than something anybody has to infer from the state of the item.
		Key:           triage.PublicationKey(state.RunID, published.Number),
		Class:         triage.ClassPublication,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		RecordedAt:    now.UTC(),
		Summary:       state.ReviewSummary,
		Findings:      docketFindings(state.ReviewFindingDetails),
		Artifacts:     docketArtifacts(state),
		Publication: &triage.Publication{
			Number:      published.Number,
			URL:         published.URL,
			Branch:      published.Branch,
			HeadCommit:  published.HeadCommit,
			State:       published.State,
			Merged:      published.Merged,
			MergeQueued: published.MergeQueued,
			MergeMethod: published.MergeMethod,
			MergeCommit: published.MergeCommit,
			Message:     state.PublishFailure,
			ApprovedAt:  publicationApprovedAt(state).UTC(),
		},
		Counters: counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the unmerged publication of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

// counters are what the item has already spent and been given, beside what it is
// allowed to spend, and what has been carried out of what triage decided.
//
// Every figure but the repair attempts comes from the item's own triage record
// and the caps the guards refuse against, rather than being counted again here.
// The rounds are the case that shows why: the record counts a round per
// developer attempt judged, and summing the rounds each run recorded counts a
// replayed change's re-review as well — so a docket that added them up could
// report an item at its cap that the guard would grant, and refuse in a reader's
// head what nothing refuses in the record.
//
// It is every figure the record holds rather than a chosen few. A field the
// ledger keeps and the view drops is a decision the guards can see and the
// development manager cannot, which reads on the docket as a decision nobody
// took: the rounds a grant came to, whether the cap cut it, and what it stands
// committed to are all of them detail the harness reported as it recorded the
// grant, and all of them were missing from the entry it was recorded against.
//
// The caps it reports are the configured ones as the item's own recorded
// overrides leave them, which is what the guards refuse against. An operator has
// crossed a cap precisely when a development manager was refused past it, so an
// entry stating the configured figure would refuse in its reader's head exactly
// the decision the operator sat down to make possible. The overrides themselves
// are on the entry rather than in here — a budget larger than the project
// configured and no account of why is a number a reader has to decide whether to
// trust, and they are joined where the docket is read for the reason the re-run
// beside them is.
func (d Docketer) counters(ledger runstate.TriageCounters, repairAttempts, rerunsCarriedOut int) triage.Counters {
	permitted := d.Caps.Overridden(ledger.Overrides)
	return triage.Counters{
		ReviewRounds:        ledger.ReviewRounds,
		ReviewRoundsCap:     permitted.ReviewRounds,
		RepairAttempts:      repairAttempts,
		RepairGrantAttempts: d.Triage.RepairGrantAttempts,
		RepairGrants:        ledger.RepairGrants,
		RepairGrantsCap:     permitted.RepairGrants,
		GrantedRounds:       ledger.GrantedRounds,
		TruncatedGrants:     ledger.TruncatedGrants,
		CommittedRounds:     ledger.CommittedRounds,
		Reruns:              ledger.Reruns,
		RerunsCap:           permitted.Reruns,
		MergeRearms:         ledger.MergeRearms,
		MergeRearmsCap:      permitted.MergeRearms,
		RerunsCarriedOut:    rerunsCarriedOut,
	}
}

// docketedOverrides carries the operator's recorded cap decisions onto an entry.
// It is a copy rather than the record's own type for the reason every other
// figure here is copied: what reaches a development manager must not change shape
// because the durable schema was refactored.
func docketedOverrides(recorded []runstate.TriageOverride) []triage.Override {
	if len(recorded) == 0 {
		return nil
	}
	carried := make([]triage.Override, 0, len(recorded))
	for _, override := range recorded {
		carried = append(carried, triage.Override{
			Budget:    override.Budget,
			Cap:       override.Cap,
			Cleared:   override.Cleared,
			DecidedBy: override.DecidedBy,
			DecidedAt: override.DecidedAt,
			Reason:    override.Reason,
		})
	}
	return carried
}

// recordedCounters are the counters an entry is written with: the item's triage
// record as it stands at the moment the work stopped. What is written is a
// snapshot and is refreshed wherever the docket is read, because the decisions
// that matter to a reader are all made after the entry exists.
//
// What has been carried out is not read here. A stoppage being docketed has had
// nothing carried out about it yet, and reading the claims to write a zero would
// buy nothing the read side does not do again properly.
func (d Docketer) recordedCounters(state runstate.State) (triage.Counters, error) {
	ledger, err := d.Decisions.Counters(state.WorkItemID)
	if err != nil {
		return triage.Counters{}, fmt.Errorf("read what triage has recorded about %s: %w", state.WorkItemID, err)
	}
	return d.counters(ledger, state.RepairAttempts, 0), nil
}

// unreadyCounters is what the item has spent and what it may still spend, and
// the reason it could not be read where that is the answer. Nothing this finding
// did costs the item anything — no run was made — so the repair attempts and the
// re-runs carried out are zero rather than counted from anywhere.
func (d Docketer) unreadyCounters(workItemID string) (triage.Counters, string) {
	if d.Decisions == nil {
		return d.counters(runstate.TriageCounters{}, 0, 0),
			"nothing was wired to read what triage has recorded about " + workItemID + ", so the figures beside this entry are the configured caps and no spend at all"
	}
	ledger, err := d.Decisions.Counters(workItemID)
	if err != nil {
		return d.counters(runstate.TriageCounters{}, 0, 0),
			fmt.Sprintf("read what triage has recorded about %s: %v", workItemID, err)
	}
	return d.counters(ledger, 0, 0), ""
}

func docketFindings(findings []runstate.Finding) []triage.Finding {
	if len(findings) == 0 {
		return nil
	}
	// The reviewer's words are copied rather than summarized: what makes a
	// finding worth carrying to a development manager is the reviewer's own
	// account of it, and a paraphrase is a second opinion nobody asked for.
	docketed := make([]triage.Finding, 0, len(findings))
	for _, finding := range findings {
		docketed = append(docketed, triage.Finding{
			Severity: finding.Severity,
			Message:  finding.Message,
			File:     finding.File,
			Line:     finding.Line,
		})
	}
	return docketed
}

// docketEnvironmental carries the run's account of a round the environment
// refused onto the entry. It is written into the entry rather than joined where
// the docket is read, unlike the decisions beside it, because it is settled as
// the run ends: what a development manager needs from it is what the counters on
// the entry already do or do not include, and that was decided before the entry
// was made.
func docketEnvironmental(refused *runstate.EnvironmentalRefusal) *triage.Environmental {
	if refused == nil {
		return nil
	}
	return &triage.Environmental{
		Cause:         string(refused.Cause),
		Detail:        refused.Detail,
		Settled:       refused.Settled,
		Refused:       refused.Refused,
		RoundReturned: refused.RoundReturned,
		GrantReturned: refused.GrantReturned,
		Problem:       refused.Problem,
		// The accounting sentence is carried rather than re-derived from the flags
		// beside it, so the docket, the thread, and the run's own notes say the same
		// words about what this round cost.
		Account: refused.Describe(),
	}
}

// docketFailure is the reason a preserved death gives for having died, bounded to
// what an entry may carry. A run's own record keeps whatever the failing step
// gave it and nothing bounds that, so an entry built from it unbounded would be
// refused for its length — and a stoppage refused at the docket is one the
// development manager never hears about, which is the silence this whole path
// exists to end.
func docketFailure(state runstate.State) string {
	if state.Blocker != "" || !preservedDeath(state) {
		return ""
	}
	return runstate.RecordFailure(state.Failure)
}

func docketCheck(failure *runstate.CheckFailure) *triage.Check {
	if failure == nil {
		return nil
	}
	return &triage.Check{Command: failure.Command, ExitCode: failure.ExitCode, Output: failure.Output}
}

func docketArtifacts(state runstate.State) triage.Artifacts {
	return triage.Artifacts{
		Branch:          state.Branch,
		WorktreePath:    state.WorktreePath,
		TargetBranch:    state.TargetBranch,
		BaseCommit:      state.BaseCommit,
		BranchRemoved:   state.BranchRemoved,
		WorktreeRemoved: state.WorktreeRemoved,
	}
}

func (d Docketer) validate() error {
	var problems []error
	if d.Docket == nil {
		problems = append(problems, errors.New("a triage docket is required"))
	}
	if d.Runs == nil {
		problems = append(problems, errors.New("the recorded runs are required to build a triage docket"))
	}
	if d.Decisions == nil {
		problems = append(problems, errors.New("the item's durable triage record is required, because every entry reports what triage has already decided about its item"))
	}
	return errors.Join(problems...)
}

func (d Docketer) now() time.Time {
	if d.Clock == nil {
		return execution.RealClock{}.Now()
	}
	return d.Clock.Now()
}
