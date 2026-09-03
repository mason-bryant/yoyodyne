package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// StateSchemaVersion stays 1 because every addition since has been an optional
// key. A state file written before them still decodes, which is the bar every
// schema change has to clear: `review_findings` keeps the meaning and the
// integer type it was written with, the structured findings the repair loop
// needs live under their own key beside it, as does the failing check that
// triggers the same loop, and the published pull request is absent entirely
// from a run that never published one — as is the queued merge inside it, whose
// absence means what it meant before queued merges existed: no merge is waiting
// on the forge. The recorded account of what a run changed is the same kind of
// addition: a state file written before it decodes unchanged, and its absence
// means what it always meant, which is that nothing summarized the change. The
// count of retried promotions is the same again: absent means no promotion of
// this run was ever re-prepared, which is what every run written before the
// retry existed did. The directive a run paused for is the same again: absent
// means no user directive ever held this run up, which is what every run
// written before directives were enforced meant. The operator hold a run parked
// on is the last of them: absent means the operator never held this run, which
// is what every run written before the hold existed meant. Why a run was
// selected is the newest of them and behaves identically: absent means nothing
// accounted for the choice, which is what every run written before selections
// were recorded meant. The protected paths a change was refused for is the
// newest of them and behaves the same way: absent means no such refusal was
// ever recorded against this run, which is what every run written before the
// gate existed meant. The review rounds a run accumulated and the blocker it
// stopped on are the two newest, and both behave identically: absent means
// nothing counted the rounds and nothing blocked this run, which is what every
// run written before triage docketed anything meant. What the work item was
// called is the newest of them and behaves the same way: absent means nothing
// recorded a title for it, which is what every run written before a surface
// needed to name the work in words meant. The account the run ran under and the
// configuration revision in force are the two newest, and they behave the same
// way: absent means nothing recorded which account or which configuration, which
// is what every run written before either was carried meant. The grants of
// further repair attempts triage has continued a stopped run on are the newest
// and behave identically: absent means nothing ever continued this run, which is
// what every run written before triage could meant, and the configured budget is
// then the whole of what bounded its repairs.
const StateSchemaVersion = 1

// The shape of the three things a run records about how it was configured and
// what was executing it. They are stated here rather than imported from the
// configuration package for the reason the review decisions above are: the
// durable schema stays independent of the code that produces what it stores, so
// a record is checked against what a record may hold rather than against what
// this version of the harness happens to write.
//
// buildPattern is shared by every record in this package that pins a harness
// build, and it holds one to being a Git object name because that is the only
// thing it ever is: a reader measures how old a build is by handing it to Git,
// and a field that could carry anything is a field that could carry an option.
var (
	accountAliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	configRevisionPattern = regexp.MustCompile(`^cfg-[a-f0-9]{8,}$`)
	buildPattern          = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
)

// Phase names the step a run reached. It is recorded alongside the status so a
// terminal run says where it stopped and an interrupted run says what was in
// flight, which is what reconciliation and diagnosis both need.
type Phase string

const (
	PhaseDeveloping  Phase = "developing"
	PhaseChecking    Phase = "checking"
	PhaseReviewing   Phase = "reviewing"
	PhaseIntegrating Phase = "integrating"
	PhaseCompleting  Phase = "completing"
	PhaseCleaningUp  Phase = "cleaning_up"
	PhaseComplete    Phase = "complete"
)

// Review decisions and finding severities are duplicated here rather than
// imported so the durable schema stays independent of the review implementation
// that produces them.
const (
	ReviewApprove = "approve"
	ReviewRepair  = "repair"
)

const (
	SeverityBlocker = "blocker"
	SeverityMajor   = "major"
	SeverityMinor   = "minor"
)

// The two vocabularies above stated as lists, which is what the validation below
// reads. Neither is repeated in a switch anywhere in this package, so the list
// and what a record may carry cannot come to disagree.
//
// Both lists are closed and both stay closed. A stored value nothing recognizes
// is worse than a refused one here: what reads these fields ranks a severity to
// order a listing, and builds the repair prompt a developer is handed back from
// them, and neither has an answer for a word it has never seen. Opening them —
// the tolerant reader, which is right where a record arrives from somewhere this
// code does not control — would move the refusal out of the save and into those
// readers, where it is silent.
//
// What closing them costs is the trap they once sprang: the reviewer's
// vocabulary grew, the durable one did not, and the addition was refused at save
// time in a process that had already reported the verdict to the tracker and the
// operator. That price is now paid where it can be seen instead.
// TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce holds the review
// package's vocabularies to being subsets of these, so an addition there fails a
// check rather than somebody's run.
var (
	reviewDecisions   = []string{ReviewApprove, ReviewRepair}
	findingSeverities = []string{SeverityBlocker, SeverityMajor, SeverityMinor}
)

// ReviewDecisions and FindingSeverities are those vocabularies as a caller
// outside this package reads them. Each answers with a copy, because a
// package-level slice is a vocabulary anybody holding it could rewrite.
func ReviewDecisions() []string { return slices.Clone(reviewDecisions) }

func FindingSeverities() []string { return slices.Clone(findingSeverities) }

// quotedAlternatives names a vocabulary the way a refusal has to: every value
// quoted, the last joined with "or". It is derived from the list rather than
// written out beside it so a value added to one is named by the other.
func quotedAlternatives(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	switch len(quoted) {
	case 0:
		return "nothing"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}

// The two ways the harness itself stops a provider invocation on time. They are
// recorded apart because they describe opposite things: a stalled invocation
// stopped emitting events and was doing nothing, while one that exhausted its
// budget was still emitting them and simply ran out of run. Neither is the
// provider reporting a failure, and neither is stored as one.
const (
	ProviderStopStalled         = "stalled"
	ProviderStopBudgetExhausted = "budget_exhausted"
)

// The two ways a provider refuses an attempt without judging the work, and so
// the two things a paused run can be waiting on. They share one deadline, one
// budget, and one polling discipline; what differs is the words an operator
// reads and the clock the wait is set by, and neither can be recovered from a
// deadline on its own. The empty cause reads as an exhausted usage limit, so a
// record written before an overload was waitable still describes itself.
const (
	PauseUsageLimit     = "usage_limit"
	PauseServerOverload = "server_overload"
)

// DescribePause names what a paused run is waiting on, as the object of "paused
// for" or "waiting out". kind is the provider's own name for an exhausted usage
// limit and says nothing about any other cause.
func DescribePause(cause, kind string) string {
	if cause == PauseOperatorHold {
		return "an operator hold on all harness activity"
	}
	if cause == PauseServerOverload {
		return "a transient provider server overload"
	}
	if strings.TrimSpace(kind) == "" {
		kind = "provider"
	}
	return "an exhausted " + kind + " usage limit"
}

// MaxCheckOutputBytes bounds the captured output a failing check may carry into
// durable state and into the developer's next attempt. A verbose suite must not
// be able to fill either with output that is mostly unrelated to the failure.
const MaxCheckOutputBytes = 8 << 10

// CheckFailure is the deterministic check a repair attempt was handed back. It
// is durable for the same reason the findings are: an attempt interrupted
// before it ran has to be reissued with exactly the input it was given, and a
// run that spends its attempts has to name what still fails. The output is the
// bounded capture rather than everything the check printed.
type CheckFailure struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

// Validate reports every contract violation in the recorded check at once.
func (c CheckFailure) Validate() error {
	var problems []error
	if strings.TrimSpace(c.Command) == "" {
		problems = append(problems, errors.New("command is required"))
	}
	if len(c.Output) > MaxCheckOutputBytes {
		problems = append(problems, fmt.Errorf("output is %d bytes, which exceeds the %d byte bound", len(c.Output), MaxCheckOutputBytes))
	}
	return errors.Join(problems...)
}

// MaxRefusedPaths bounds how many protected paths a refusal carries into
// durable state and into the developer's next attempt. A change that rewrote a
// whole artifact home must not be able to fill either with a listing, and the
// count of what was dropped is kept beside what was kept, so a bounded refusal
// never reads as the whole of what the gate caught.
const MaxRefusedPaths = 50

// PathRefusal is the protected-path gate's refusal a repair attempt was handed
// back: the upstream paths the change touched that the work item never granted.
// It is durable for the reason a failing check is, and for one more. An attempt
// interrupted before it ran has to be reissued with exactly the input it was
// given, and a run that spends its attempts has to name what it still refuses —
// and unlike a check, nothing re-derives this for a reader afterwards, because
// the worktree it describes is removed when the run is cleaned up.
type PathRefusal struct {
	// Paths are the refused paths, repository-relative, in the order they sort.
	Paths []string `json:"paths"`
	// Omitted is how many further refused paths the bound above dropped.
	Omitted int `json:"omitted,omitempty"`
	// Grants is what the work item did grant, recorded beside the refusal
	// because the two are read together: a refusal that looks wrong is most often
	// a grant that named the path differently, and a reader with only one half of
	// that cannot see it.
	Grants []string `json:"grants,omitempty"`
}

// Validate reports every contract violation in the recorded refusal at once.
func (p PathRefusal) Validate() error {
	var problems []error
	if len(p.Paths) == 0 {
		problems = append(problems, errors.New("at least one refused path is required"))
	}
	if len(p.Paths) > MaxRefusedPaths {
		problems = append(problems, fmt.Errorf("%d refused paths are recorded, which exceeds the bound of %d", len(p.Paths), MaxRefusedPaths))
	}
	for index, refused := range p.Paths {
		if strings.TrimSpace(refused) == "" {
			problems = append(problems, fmt.Errorf("paths[%d] is empty", index))
		}
	}
	if p.Omitted < 0 {
		problems = append(problems, errors.New("omitted cannot be negative"))
	}
	return errors.Join(problems...)
}

// Finding is one durable reviewer finding. Findings are recorded rather than
// only counted because they are the developer's input for the next repair
// attempt: a run interrupted between attempts has to hand back exactly what the
// reviewer asked for, and a run that spends its attempts has to name what is
// still unresolved.
type Finding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	if !slices.Contains(findingSeverities, f.Severity) {
		problems = append(problems, fmt.Errorf("severity %q must be %s", f.Severity, quotedAlternatives(findingSeverities)))
	}
	if strings.TrimSpace(f.Message) == "" {
		problems = append(problems, errors.New("message is required"))
	}
	if f.Line < 0 {
		problems = append(problems, fmt.Errorf("line %d cannot be negative", f.Line))
	}
	if f.Line > 0 && strings.TrimSpace(f.File) == "" {
		problems = append(problems, errors.New("line requires a file"))
	}
	return errors.Join(problems...)
}

// PullRequest is the durable record of the pull request a run published its
// work through: which remote carries the branch, which commit was last pushed
// to it, and what the forge says about the request itself. It is recorded from
// the developer phase onward, so a run that stops anywhere after that still
// names the published work rather than leaving it to be rediscovered by hand.
type PullRequest struct {
	Remote     string `json:"remote"`
	Branch     string `json:"branch"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	HeadCommit string `json:"head_commit"`
	State      string `json:"state,omitempty"`
	Merged     bool   `json:"merged,omitempty"`
	// MergeMethod is the method the forge was asked to merge by, recorded
	// because it decides what the remote history looks like: only one of the
	// methods puts the promoted commit itself on the remote target rather than a
	// rewritten copy of it. MergeCommit is where that merge left the remote
	// target branch, which is the forge's own merge commit and therefore a
	// commit the local target branch does not carry.
	MergeMethod string `json:"merge_method,omitempty"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// MergeQueued reports a merge the forge accepted but has not performed: it
	// merges the request itself once the base branch's requirements are met,
	// which happens long after the run that asked for it has finished. It keeps
	// the run outstanding until somebody knows which way that went, and is
	// cleared when reconciliation observes the merge land or be dropped.
	MergeQueued bool `json:"merge_queued,omitempty"`
}

// Validate rejects a published record that cannot describe a real pull request.
func (p PullRequest) Validate() error {
	var problems []error
	if strings.TrimSpace(p.Remote) == "" {
		problems = append(problems, errors.New("pull_request remote is required"))
	}
	if !validLocalBranch(p.Branch) {
		problems = append(problems, errors.New("pull_request branch must be a local branch name"))
	}
	if p.Number <= 0 {
		problems = append(problems, errors.New("pull_request number must be positive"))
	}
	if strings.TrimSpace(p.URL) == "" {
		problems = append(problems, errors.New("pull_request url is required"))
	}
	if !commitPattern.MatchString(p.HeadCommit) {
		problems = append(problems, errors.New("pull_request head_commit is invalid"))
	}
	return errors.Join(problems...)
}

// MaxChangeRecordBytes bounds each half of the recorded account of what a run
// changed. A run that touched two hundred files must not be able to fill the
// state file with its own listing, and what the bound cuts is the tail of a
// summary rather than any part of the run's evidence: the change itself is in
// the commit.
const MaxChangeRecordBytes = 8 << 10

// Changes is what a run's worktree held when the harness last summarized it:
// the files it had touched and how much of each. It is recorded because it is
// the only account of what a run changed that outlives the worktree — cleanup
// removes the tree and the branch, and a diff nobody can take any more is a
// diff nobody can be shown.
type Changes struct {
	// Files is the name-status listing, and DiffStat is Git's own summary of how
	// much each file changed. Both are Git's words, kept as they were produced.
	Files    string `json:"files,omitempty"`
	DiffStat string `json:"diff_stat,omitempty"`
}

// RecordChanges makes a bounded record of a summarized change, or nothing at
// all when the summary is empty. A summary too long to keep is cut rather than
// refused: losing a run's state file over a verbose listing would cost far more
// than the tail of one.
func RecordChanges(files, diffStat string) *Changes {
	files = boundChangeRecord(files)
	diffStat = boundChangeRecord(diffStat)
	if files == "" && diffStat == "" {
		return nil
	}
	return &Changes{Files: files, DiffStat: diffStat}
}

// Validate rejects a recorded change that could not have been produced within
// the bounds the harness records under.
func (c Changes) Validate() error {
	var problems []error
	if len(c.Files) > MaxChangeRecordBytes {
		problems = append(problems, fmt.Errorf("changes files is %d bytes, which exceeds the %d byte bound", len(c.Files), MaxChangeRecordBytes))
	}
	if len(c.DiffStat) > MaxChangeRecordBytes {
		problems = append(problems, fmt.Errorf("changes diff_stat is %d bytes, which exceeds the %d byte bound", len(c.DiffStat), MaxChangeRecordBytes))
	}
	return errors.Join(problems...)
}

// boundChangeRecord cuts one half of a change record to its bound and says that
// it was cut, so nobody reads a clamped listing as a complete one.
func boundChangeRecord(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	trimmed := strings.TrimRight(text, "\n")
	if len(trimmed) <= MaxChangeRecordBytes {
		return trimmed
	}
	cut := MaxChangeRecordBytes - len(changeRecordCutNote)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimRight(trimmed[:cut], "\n") + changeRecordCutNote
}

const changeRecordCutNote = "\n[cut; the rest of this summary was not recorded]"

// MaxBlockerBytes bounds the durable blocker a run carries. It is the docket's
// own bound, shared rather than restated: a docket entry has to carry the
// blocker in the words it was recorded in, and two bounds that could drift
// would be a blocker the harness recorded and the docket then refused.
const MaxBlockerBytes = triage.MaxBlockerBytes

// RecordBlocker makes a bounded record of a blocker the harness recorded on a
// work item. A blocker too long to keep is cut rather than refused, exactly as
// a verbose change summary is: losing a run's state file over a reviewer that
// wrote at length would cost far more than the tail of one blocker, and the
// tracker holds the whole of it either way.
func RecordBlocker(notes string) string {
	trimmed := strings.TrimRight(notes, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return ""
	}
	if len(trimmed) <= MaxBlockerBytes {
		return trimmed
	}
	cut := MaxBlockerBytes - len(blockerCutNote)
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimRight(trimmed[:cut], "\n") + blockerCutNote
}

const blockerCutNote = "\n[cut; the work item carries the whole of this blocker]"

// DirectivePause is the user directive a run stopped short for. It is recorded
// before the run returns, for the same reason a usage-limit deadline is: the
// pause has to survive the process, so a later invocation can tell a run that is
// waiting from one that was interrupted, and can resume it rather than start a
// second attempt at the same item.
//
// The directive itself lives in the product's own directive store, which is what
// makes it reachable from every process. What is copied here is only what a
// reader of this run needs in order to say what the run is waiting for without
// going and finding it: which directive, and what about it is unresolved.
type DirectivePause struct {
	DirectiveID string `json:"directive_id"`
	Kind        string `json:"kind"`
	Unresolved  string `json:"unresolved"`
}

// Validate rejects a recorded pause that cannot say what the run is waiting for.
func (d DirectivePause) Validate() error {
	var problems []error
	if strings.TrimSpace(d.DirectiveID) == "" {
		problems = append(problems, errors.New("directive_id is required"))
	}
	if strings.TrimSpace(d.Kind) == "" {
		problems = append(problems, errors.New("kind is required"))
	}
	// A pause nobody can name the reason for is a pause nobody can lift, which is
	// exactly the state enforcing directives exists to prevent.
	if strings.TrimSpace(d.Unresolved) == "" {
		problems = append(problems, errors.New("unresolved is required"))
	}
	return errors.Join(problems...)
}

// DependencyPause is the unfinished work a run stopped short for: the blocking
// dependencies its work item carried when the run last read it. It is recorded
// before the run returns, for the same reason a directive pause is — the pause
// has to survive the process, so a later invocation can tell a run that is
// waiting from one that was interrupted, and resume it rather than start a
// second attempt at the same item.
//
// The dependency graph itself lives in the tracker, which is what makes it
// reachable from every process. What is copied here is only what a reader of
// this run needs in order to say what the run is waiting for without going and
// finding it: the items it is waiting on.
type DependencyPause struct {
	Blockers []string `json:"blockers"`
}

// Summary names the work this pause is waiting on, in one line, for a reader who
// needs to know what to close rather than the whole dependency graph.
func (d DependencyPause) Summary() string {
	return strings.Join(d.Blockers, ", ")
}

// Validate rejects a recorded pause that cannot say what the run is waiting for.
// A pause nobody can name the blocker of is a pause nobody can lift, which is
// exactly the state enforcing dependencies exists to prevent.
func (d DependencyPause) Validate() error {
	if len(d.Blockers) == 0 {
		return errors.New("blockers is required")
	}
	for _, blocker := range d.Blockers {
		if strings.TrimSpace(blocker) == "" {
			return errors.New("every blocker must name the work item it waits on")
		}
	}
	return nil
}

// MaxRepairContinuations bounds how many granted continuations one run's record
// may carry. What actually bounds them is the item's per-item grant cap, which
// refuses long before this; this is the record's own bound, so a budget somebody
// configured absurdly cannot grow a state file without limit.
const MaxRepairContinuations = 16

// RepairContinuation is one carried-out repair grant that re-entered this run's
// repair loop after it had already stopped. It is the run's half of a triage
// decision the item's durable counters record the other half of: the counters
// say what the development manager granted the item and whether the round cap
// cut it, and this says what was actually done with that grant here — how many
// attempts this run may now make, and the reasoning the harness was given when
// it was asked to continue.
//
// The blocker it superseded travels with it. Re-entry clears the run's standing
// blocker, because a run that is going again has not stopped and the docket,
// `yoyo status`, and reconciliation all read that field as the fact that it has;
// keeping the words here is what stops the clearing losing the evidence of what
// the run was stopped for.
type RepairContinuation struct {
	// GrantedAttempts is what this continuation added to the run's repair budget,
	// out of the rounds the item's record says triage granted it. Summed across
	// every run of one item it is what says how much of that grant has been
	// carried out, which is what stops one decision being acted on twice.
	GrantedAttempts int `json:"granted_attempts"`
	// Reason is the development manager's triage reasoning as the harness was
	// given it, which is why this run is going again. A continuation nobody can
	// account for is exactly the work that looks like it is happening behind
	// somebody's back.
	Reason      string    `json:"reason"`
	ContinuedAt time.Time `json:"continued_at"`
	// SupersededBlocker is the durable blocker this re-entry cleared, in the
	// words it was recorded in. It is absent only on a re-entry of a run that
	// carried none, which nothing here produces.
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// Returned says the round this continuation bought was environmentally
	// refused, so the grant it came out of was never actually spent on anything.
	// It is what keeps the attempts still counting toward this run's own budget —
	// the run did spend an attempt slot on the refusal — while leaving the item's
	// grant where it was, which is the two different questions the same number
	// answered before: what this run may still do, and what triage has already
	// handed the item. See environmental.go.
	Returned bool `json:"returned,omitempty"`
}

// Validate reports every contract violation in the recorded continuation at once.
func (c RepairContinuation) Validate() error {
	var problems []error
	if c.GrantedAttempts < 1 {
		problems = append(problems, errors.New("a continuation grants at least one repair attempt"))
	}
	if strings.TrimSpace(c.Reason) == "" {
		problems = append(problems, errors.New("the triage reasoning this run was continued on is required"))
	}
	if len(c.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(c.Reason), MaxSelectionReasonBytes))
	}
	if c.ContinuedAt.IsZero() {
		problems = append(problems, errors.New("continued_at is required"))
	}
	if len(c.SupersededBlocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_blocker is %d bytes, which exceeds the %d byte bound", len(c.SupersededBlocker), MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// Integration is the durable evidence of a completed promotion: exactly which
// commit the harness created and which commit the target moved from and to.
type Integration struct {
	TargetBranch         string `json:"target_branch"`
	SourceCommit         string `json:"source_commit"`
	TargetCommit         string `json:"target_commit"`
	PreviousTargetCommit string `json:"previous_target_commit"`
}

type State struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id"`
	WorkItemID    string           `json:"work_item_id"`
	// WorkItemTitle is what the item is called, written with the run because the
	// claim is where the harness has the tracker's answer in hand and everything
	// reading the record afterwards does not. It is what lets a surface name the
	// work in words a person reads rather than in an identifier they would have to
	// resolve, and it is a copy rather than a reference on purpose: the record says
	// what the item was called when the run started, which is what an account of
	// that run should say however the item is renamed later. Absent means nothing
	// recorded a title, which is what every run written before this did.
	WorkItemTitle string `json:"work_item_title,omitempty"`
	// Selection is why the harness is running this item: who chose it and on
	// what grounds. It is written when the run is reserved and never rewritten.
	// Absent means nothing accounted for the choice, which is not the same as a
	// choice with no reason and is reported as such.
	Selection *Selection     `json:"selection,omitempty"`
	Backend   domain.Backend `json:"backend"`
	// AccountAlias is the provider account this run's agents ran under, named by
	// the alias the configuration gives it. It is written when the run is reserved
	// and never rewritten, because it is a fact about what was spent rather than
	// about what is configured now: one account is what the harness runs today, so
	// what this buys is that every record already says which — and the day there
	// is a second account, nothing written before it has to be guessed at.
	//
	// Absent means nothing recorded an account, which is what every run written
	// before this did.
	AccountAlias string `json:"account_alias,omitempty"`
	// ConfigRevision identifies the configuration in force when this run was
	// started: a digest of every effective value, so two runs carrying one
	// revision were configured identically and a run whose configuration was
	// edited under it is distinguishable from one that was not. Like the account,
	// it is written once and never rewritten — a run resumed by a later process
	// keeps the revision it was set up under, which is what makes it evidence
	// about this run rather than a reading of whatever the file says now.
	//
	// Absent means nothing recorded a configuration, which is what every run
	// written before this did.
	ConfigRevision string `json:"config_revision,omitempty"`
	// Build is the repository revision the harness binary that reserved this run
	// was built from. It is written with the account and the configuration and
	// never rewritten, for the same reason and for one of its own: a run picked up
	// again by a later process still says which build started it, which is what an
	// account of what that run did has to be able to answer.
	//
	// It is here because a process goes on running whatever it was started with
	// while the harness moves on underneath it, and nothing else in a run's record
	// says which of the two dispatched it. That gap is not hypothetical: four
	// repair dispatches in the week of 2026-08-27 were made after the refusal that
	// should have turned each of them away had merged, by a resident scheduler
	// nobody could show was running the merged code, and the diagnosis had to stop
	// there because no record could name the binary. Most of that week's code
	// defects were deployment defects and nothing could tell the two apart.
	//
	// Absent means nothing recorded a build, which is what every run written
	// before this did and what a binary carrying no revision of its own produces —
	// a comparison nobody can make, rather than a run that is current.
	Build string `json:"build,omitempty"`
	// ProviderSessionID is the developer session. The reviewer's session is
	// recorded separately because the two are always distinct invocations.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ProviderModel is the selector the developer invocation requested and
	// ProviderResolvedModel is what the provider reported serving it. A
	// floating alias makes the resolved identifier the only real audit record.
	ProviderModel         string     `json:"provider_model,omitempty"`
	ProviderResolvedModel string     `json:"provider_resolved_model,omitempty"`
	Status                Status     `json:"status"`
	Phase                 Phase      `json:"phase,omitempty"`
	LastSequence          uint64     `json:"last_sequence"`
	StartedAt             time.Time  `json:"started_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	WorktreePath          string     `json:"worktree_path,omitempty"`
	Branch                string     `json:"branch,omitempty"`
	BaseCommit            string     `json:"base_commit,omitempty"`
	// HarnessCommit is the last commit the harness itself made in this run's
	// worktree, which publishing needs before it can push a branch. It is durable
	// because it is what permits the worktree's HEAD to have moved: a resumed run
	// that could not name the commit it made would either refuse its own work or
	// have to accept whatever it finds, and the second is how an agent's commit
	// gets promoted.
	HarnessCommit string `json:"harness_commit,omitempty"`
	// WorktreeRemoved and BranchRemoved record the two cleanup steps
	// separately, because they cannot be performed atomically. Recording them
	// apart is what lets an interrupted cleanup be resumed and what keeps a
	// preserved-artifact claim truthful.
	WorktreeRemoved bool `json:"worktree_removed,omitempty"`
	BranchRemoved   bool `json:"branch_removed,omitempty"`
	// ArtifactsRetiredBy names the run that superseded this one, on a run whose
	// artifacts were retired by triage rather than cleaned up after a promotion
	// of its own. It is the second way a removal is earned, and the reason it is
	// recorded rather than inferred: a stopped run integrates nothing, so without
	// it a removal on one would be a claim with no evidence behind it — which is
	// exactly what the rule below refuses.
	//
	// Absent is every run whose artifacts it removed itself, which is all of them
	// until triage retires one.
	ArtifactsRetiredBy string `json:"artifacts_retired_by,omitempty"`
	// WorktreeSweptAt is when the convergence sweep retired this run's checkout
	// as hygiene, on a settled run that neither promoted anything nor was
	// superseded. It is the third way a removal is earned and it covers the
	// checkout alone, because the sweep only ever unregisters an empty checkout
	// and never touches a branch: what that checkout carried is still on the
	// branch, so nothing about the run stops being recoverable.
	//
	// It is recorded for the reason the other two are. `yoyo status`, the triage
	// docket, and a re-run all read WorktreeRemoved as the answer to whether the
	// directory is still there, and a removal nothing wrote down leaves every one
	// of them sending somebody after a checkout that is gone.
	//
	// Absent is every run whose checkout the sweep has not taken, which is all of
	// them while it is still within the tail the sweep holds back.
	WorktreeSweptAt *time.Time `json:"worktree_swept_at,omitempty"`
	// PreservedWorkRef names the ref carrying whatever this run left uncommitted
	// in its checkout, written when the sweep retired that checkout and had
	// something to move out of it first. It is deliberately not a branch: a branch
	// would be swept, listed, and considered by every containment proof the
	// harness makes, and this is none of those things — only a garbage-collection
	// root and an answer to where the work went.
	//
	// This record is the only place that answer lives. A stopped run's
	// half-finished change is exactly what somebody comes looking for months
	// later, and nothing else in the repository connects the ref to the item it
	// belonged to.
	//
	// Absent is every run whose checkout held nothing to move, and every run whose
	// checkout is still there.
	PreservedWorkRef string `json:"preserved_work_ref,omitempty"`
	// TargetBranch is the integration target fixed when the worktree was
	// created. It is durable so a resumed run promotes the work into the branch
	// it was written against rather than whatever happens to be checked out
	// when the run is picked up again.
	TargetBranch        string `json:"target_branch,omitempty"`
	ReviewSessionID     string `json:"review_session_id,omitempty"`
	ReviewModel         string `json:"review_model,omitempty"`
	ReviewResolvedModel string `json:"review_resolved_model,omitempty"`
	ReviewDecision      string `json:"review_decision,omitempty"`
	ReviewSummary       string `json:"review_summary,omitempty"`
	ReviewFindings      int    `json:"review_findings,omitempty"`
	// ReviewFindingDetails carries the findings themselves. It is a separate key
	// from the ReviewFindings count, which predates it and still means what it
	// always did, so a state file written before the repair loop existed keeps
	// decoding unchanged.
	ReviewFindingDetails []Finding `json:"review_finding_details,omitempty"`
	// ReviewRounds counts the reviews this run obtained a verdict from, whichever
	// way each of them went. It is not the same thing as the repair attempts
	// below and cannot be derived from them: a repair attempt handed back a
	// refused path or a failing check never reaches a reviewer, and an approved
	// change was reviewed without any repair at all. It is cumulative rather
	// than descriptive of the current attempt, so unlike every other piece of
	// review evidence it survives an attempt being cleared — what triage measures
	// against its configured cap is how many rounds the work has taken in total,
	// across repairs and across runs, and a counter reset by the next attempt
	// would answer a different question every time it was read.
	ReviewRounds int `json:"review_rounds,omitempty"`
	// CheckFailure carries the failing deterministic check a repair attempt was
	// handed. It and ReviewFindingDetails are the two kinds of repair input, and
	// at most one of them describes the current attempt: the checks are re-run
	// after every attempt, so recording a failing check clears findings that
	// describe a change the gate has already moved past, and passing checks
	// clear the failure.
	CheckFailure *CheckFailure `json:"check_failure,omitempty"`
	// PathRefusal carries the protected paths the gate refused before any check
	// ran. It is the third kind of repair input and behaves as the other two do:
	// at most one of the three describes the current attempt, and because this
	// gate is decided before the checks, recording a refusal clears both of the
	// others rather than competing with them for the next attempt.
	PathRefusal *PathRefusal `json:"path_refusal,omitempty"`
	// RepairAttempts counts the repair attempts already handed back to the
	// developer, whichever kind of failure triggered them: one budget covers
	// both, so it bounds the developer invocations a run can make rather than
	// bounding each trigger separately. It is recorded before each attempt
	// starts, so an interrupted run resumes at the attempt it reached and a
	// restart cannot buy the run a fresh budget.
	RepairAttempts int `json:"repair_attempts,omitempty"`
	// RepairContinuations are the grants of further repair attempts triage has
	// used to re-enter this run's repair loop after it stopped. They add to the
	// configured budget rather than replacing it, so the count above stays what it
	// always was — every attempt this run has handed back — and what bounds it is
	// read from the two together. Absent is every run nothing continued, which is
	// all of them until triage does.
	RepairContinuations []RepairContinuation `json:"repair_continuations,omitempty"`
	// Environmental is why this run's round delivered nothing, where the answer
	// is the environment rather than the work: a worktree that held none of the
	// change, a checkout the harness does not own, a sandbox that could not be
	// entered, a build older than the decision it carried out. It is what a round
	// is classified environmental by at settle, which is what stops the harness's
	// own failures spending the item's budgets. Absent is every run nothing
	// refused, which is nearly all of them. See environmental.go.
	Environmental *EnvironmentalRefusal `json:"environmental,omitempty"`
	// IntegrationRetries counts the promotions this run has re-prepared after
	// losing a race for its target branch: the change replayed onto where the
	// target went, re-checked, and re-reviewed. It is recorded before the retry
	// begins, for the same reason the repair count is, so a process that dies
	// mid-retry resumes against the budget it had rather than a fresh one. It is
	// bounded separately from the repair budget because it bounds a different
	// thing: how long a run keeps chasing a moving target, rather than how many
	// times a developer is asked to fix its own change.
	IntegrationRetries int `json:"integration_retries,omitempty"`
	// TransientRelaunches counts the provider invocations this run has reissued
	// after one died without judging the work — an API error the provider's own
	// retries did not outlast, or a response cut off mid-flight. One budget covers
	// the developer and the reviewer both, because what it bounds is how many
	// times a run absorbs the provider dying under it rather than how often either
	// role is asked; separate budgets would let a run alternating between them
	// absorb twice what an operator configured. It is bounded apart from the
	// repair budget for the reason that budget is bounded apart from the
	// integration one: nothing here is a fault in the change, so spending a repair
	// attempt on it would charge the developer for the provider's weather. It is
	// recorded before each relaunch begins, so a process that dies mid-relaunch
	// resumes against the budget it had rather than buying a fresh one.
	TransientRelaunches int `json:"transient_relaunches,omitempty"`
	// UsageLimitResetsAt is the deadline a run paused for an exhausted provider
	// usage limit is waiting on. It is written before the wait begins, so a
	// process that dies during the wait does not lose the deadline and a restart
	// honors it instead of retrying straight back into the same limit. It is
	// cleared once the deadline passes and the attempt is reissued, so a run
	// carrying one is a run that is still waiting.
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	// UsageLimitKind is the provider's own name for the limit that paused the
	// run, kept as evidence for whoever reads the record afterwards. It outlives
	// the deadline: what stopped the run is worth knowing even once the run has
	// resumed.
	UsageLimitKind string `json:"usage_limit_kind,omitempty"`
	// UsageLimitPausedSeconds is how much waiting this run has committed to
	// across every pause it has taken. It is what bounds a run against the
	// configured maximum pause: bounding each wait on its own would let a
	// provider that refuses repeatedly walk a run past the maximum an operator
	// configured, one acceptable-looking wait at a time. It is recorded in whole
	// seconds because the provider states reset times in whole seconds, and it is
	// added to when a wait is committed rather than as it elapses, so a restart
	// part-way through a wait cannot buy the run a fresh budget.
	UsageLimitPausedSeconds int64 `json:"usage_limit_paused_seconds,omitempty"`
	// PauseCause is which refusal the recorded deadline is being waited out for.
	// The deadline and the budget are shared by both, so without this a run
	// waiting out a transiently overloaded server would be described to its
	// operator as one waiting out an exhausted account. It is empty on a run that
	// is not waiting, and an empty value alongside a deadline reads as a usage
	// limit, which is what every record written before this field described.
	PauseCause string `json:"pause_cause,omitempty"`
	// OperatorHeldSince is when this run parked at a provider-call boundary
	// because the operator holds all harness activity. It is written before the
	// wait begins, exactly as a usage-limit deadline is, so a process that dies
	// while the harness is held leaves a run that still says why it stopped and
	// can be picked up again. It is cleared as the run carries on, so a run
	// carrying one is a run still waiting on the operator.
	//
	// There is no deadline beside it because there is nothing to record: what
	// lifts an operator hold is the operator.
	OperatorHeldSince *time.Time `json:"operator_held_since,omitempty"`
	// OperatorHeldSeconds is how much of this run's elapsed time the operator's
	// hold accounts for, across every hold it has parked on. It is kept apart
	// from the usage-limit budget above because it answers a different question
	// and is bounded by nothing the harness configures: the provider never
	// refused this run, and a maximum pause that stopped a held run would be the
	// harness overriding the operator. What it is for is the ledger — so time a
	// run spent doing nothing says whose decision that was.
	OperatorHeldSeconds int64 `json:"operator_held_seconds,omitempty"`
	// ProviderStop records that the harness stopped a provider invocation on
	// time -- because it stalled, or because it exhausted its total budget --
	// rather than the provider ending it. It is written only when what the
	// invocation leaves behind can still be continued, so a run carrying one is
	// a run owed a continuation, exactly as a recorded usage-limit deadline is.
	// It is cleared by the next attempt, whichever way that one goes.
	ProviderStop string `json:"provider_stop,omitempty"`
	// DirectivePause records that an unresolved user directive stopped this run
	// short of finishing: one that changes a governed artifact this work derives
	// from, or one nobody can act on until the operator says what they meant. Like
	// a recorded deadline it is an instruction to resume later rather than a
	// failure, so a run carrying one keeps its claim, its worktree, and its
	// branch, and is picked up again once the directive is resolved. It is cleared
	// as the run resumes.
	DirectivePause *DirectivePause `json:"directive_pause,omitempty"`
	// DependencyPause records that unfinished work this item was made to wait on
	// stopped the run short of finishing. Like a directive pause it is an
	// instruction to resume later rather than a failure, so a run carrying one
	// keeps its claim, its worktree, and its branch, and is picked up again once
	// the work it waits on is closed. It is cleared as the run resumes.
	//
	// It is a separate field from the directive pause rather than a second kind of
	// one because what lifts them differs: a directive is settled by a person
	// deciding, and this is lifted by other work finishing.
	DependencyPause *DependencyPause `json:"dependency_pause,omitempty"`
	// Changes is what the run's worktree held when it was last summarized. It is
	// absent from a run that never got as far as producing one, and it outlives
	// the worktree it describes, which is the whole reason it is here rather than
	// only in the outcome the run returned.
	Changes     *Changes     `json:"changes,omitempty"`
	Integration *Integration `json:"integration,omitempty"`
	// PullRequest records the published pull request when the project opted in
	// to publishing and the repository had a remote to publish to. It is absent
	// for a purely local run, which is what a project gets by default.
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	// PublishFailure explains why publishing a promotion did not finish. The
	// local target branch is the authoritative one, so a promotion that could not
	// be pushed is an outstanding publication rather than a failed run — the same
	// kind of fact as an outstanding cleanup.
	PublishFailure string `json:"publish_failure,omitempty"`
	// Failure is why this run ended, in the words of whoever ended it, and it is
	// what every surface prints where it answers "why did this stop". It is
	// written by whatever made the run terminal — the pipeline as it fails a run,
	// the sweep as it settles one — and a sweep settling a stoppage onto a record
	// that was already terminal fills it in where that record gives no reason,
	// because a stoppage nobody can read a reason for is what the surfaces cannot
	// recover from afterwards.
	//
	// It is text and never a test. Whether a run stopped is Blocker's to answer
	// and Outcome()'s to say: a run can end with a reason and no blocker, which is
	// a failure nobody has to decide about, and a stoppage can reach a record
	// whose reason was written before it. A reader inferring a stoppage from a
	// non-empty Failure is a second classification that will disagree with the
	// read model's, which is exactly what the fixed outcome vocabulary exists to
	// prevent.
	Failure string `json:"failure,omitempty"`
	// Blocker is the durable blocker exactly as it was recorded on the work item
	// when this run stopped on something no further attempt of the harness could
	// resolve. The tracker holds the authoritative copy; this one is kept because
	// the blocker is evidence about the run and the run is what outlives the
	// process that wrote it — a triage docket entry built from this record has to
	// carry the blocker in the words it was recorded in, and a later reader of the
	// run must not have to go and find which of the item's notes was this run's.
	// Absent means this run stopped on nothing anybody has to decide, which is
	// what every record written before the docket existed means.
	//
	// It is the one field a stoppage is inferred from, which is why the words and
	// the fact are two fields rather than one: Failure above says why a run ended
	// and this says that somebody now owns it.
	Blocker string `json:"blocker,omitempty"`
	// CleanupFailure explains why post-completion cleanup did not finish
	// cleanly. The run's work is already integrated, closed, and durable when it
	// is set, so it is reconciliation input rather than a run failure. It says
	// nothing on its own about what survives: WorktreeRemoved and BranchRemoved
	// carry that, and either can still be false, leaving a real artifact behind,
	// or both can be true because the removals succeeded and only the check that
	// confirms them could not run. A reconciler resumes cleanup in both cases,
	// which is a safe no-op over artifacts that are already gone.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure explains why a completed run's final record
	// took more than the normal write to land. The failure it names is the
	// store refusing the terminal save, so the field reaches disk only when a
	// later best-effort write succeeds — at which point the record is whole
	// and this says it arrived late. It matters because it is the one failure
	// class whose work-item note is itself unreliable: recording that note is
	// part of what was failing, so the run record is its authoritative home.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
}

var (
	runIDPattern  = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
	branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

// ValidRunID reports a run identifier of the shape this package mints. It is
// exported because a run is named from outside the harness too: a triage
// decision names the run whose stoppage it settles, copied out of a docket entry
// by an agent, and a name that could never have identified a run is refused
// where it is read rather than written onto a work item.
func ValidRunID(runID string) bool {
	return runIDPattern.MatchString(strings.TrimSpace(runID))
}

// validLocalBranch mirrors the integration target rule the worktree manager
// enforces: a plain local branch name, never HEAD and never a fully qualified
// ref. Durable evidence is re-read by a reconciler that acts on it, so it is
// held to the same shape here rather than only at the point it was produced.
func validLocalBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	if !branchPattern.MatchString(branch) {
		return false
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") {
		return false
	}
	return branch != "HEAD" && !strings.HasPrefix(branch, "refs/")
}

func NewRunID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run-" + hex.EncodeToString(bytes), nil
}

func (s State) Validate() error {
	var problems []error
	if s.SchemaVersion != StateSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", StateSchemaVersion))
	}
	if !runIDPattern.MatchString(s.RunID) {
		problems = append(problems, errors.New("run_id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(s.RepositoryID) == "" {
		problems = append(problems, errors.New("repository_id is required"))
	}
	if strings.TrimSpace(s.WorkItemID) == "" {
		problems = append(problems, errors.New("work_item_id is required"))
	}
	if !s.Backend.Valid() {
		problems = append(problems, errors.New("backend is invalid"))
	}
	if !s.Status.Valid() {
		problems = append(problems, errors.New("status is invalid"))
	}
	if s.StartedAt.IsZero() || s.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("started_at and updated_at are required"))
	}
	if s.UpdatedAt.Before(s.StartedAt) {
		problems = append(problems, errors.New("updated_at cannot be before started_at"))
	}
	if s.Status.Terminal() && s.CompletedAt == nil {
		problems = append(problems, errors.New("terminal status requires completed_at"))
	}
	if !s.Status.Terminal() && s.CompletedAt != nil {
		problems = append(problems, errors.New("non-terminal status cannot have completed_at"))
	}
	worktreeFields := 0
	for _, value := range []string{s.WorktreePath, s.Branch, s.BaseCommit} {
		if value != "" {
			worktreeFields++
		}
	}
	if worktreeFields != 0 && worktreeFields != 3 {
		problems = append(problems, errors.New("worktree_path, branch, and base_commit must be recorded together"))
	}
	if s.BaseCommit != "" && !commitPattern.MatchString(s.BaseCommit) {
		problems = append(problems, errors.New("base_commit is invalid"))
	}
	if s.HarnessCommit != "" {
		if !commitPattern.MatchString(s.HarnessCommit) {
			problems = append(problems, errors.New("harness_commit is invalid"))
		}
		// The commit was made in this run's worktree, so a record of one without
		// the worktree that produced it permits a HEAD nothing accounted for.
		if s.WorktreePath == "" {
			problems = append(problems, errors.New("harness_commit requires the worktree that produced it"))
		}
		if s.HarnessCommit == s.BaseCommit {
			problems = append(problems, errors.New("harness_commit cannot be the base commit, which proves no commit was made"))
		}
	}
	if s.Selection != nil {
		if err := s.Selection.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("selection: %w", err))
		}
	}
	// All three are absent from every record written before they were carried, so
	// what is checked is the shape of one that is there: a record naming an
	// account, a configuration, or a build nothing could have produced says less
	// than one naming none of them, because it reads as evidence.
	if s.AccountAlias != "" && !accountAliasPattern.MatchString(s.AccountAlias) {
		problems = append(problems, errors.New("account_alias is not an account alias"))
	}
	if s.ConfigRevision != "" && !configRevisionPattern.MatchString(s.ConfigRevision) {
		problems = append(problems, errors.New("config_revision is not a configuration revision"))
	}
	if s.Build != "" && !buildPattern.MatchString(s.Build) {
		problems = append(problems, errors.New("build is not a revision"))
	}
	if s.Phase != "" && !s.Phase.Valid() {
		problems = append(problems, errors.New("phase is invalid"))
	}
	if s.ReviewDecision != "" && !slices.Contains(reviewDecisions, s.ReviewDecision) {
		problems = append(problems, errors.New("review_decision is invalid"))
	}
	if s.ReviewFindings < 0 {
		problems = append(problems, errors.New("review_findings cannot be negative"))
	}
	for index, finding := range s.ReviewFindingDetails {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("review_finding_details[%d]: %w", index, err))
		}
	}
	// The count predates the findings themselves and a file written before them
	// carries only the count, so the two are held to agreeing only once both are
	// present. Disagreement there would mean the recorded evidence and the
	// developer's next input describe different reviews.
	if len(s.ReviewFindingDetails) > 0 && s.ReviewFindings != len(s.ReviewFindingDetails) {
		problems = append(problems, fmt.Errorf("review_findings is %d but %d review_finding_details are recorded", s.ReviewFindings, len(s.ReviewFindingDetails)))
	}
	if s.CheckFailure != nil {
		if err := s.CheckFailure.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("check_failure: %w", err))
		}
	}
	if s.PathRefusal != nil {
		if err := s.PathRefusal.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("path_refusal: %w", err))
		}
	}
	if s.Changes != nil {
		if err := s.Changes.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("changes: %w", err))
		}
	}
	if s.RepairAttempts < 0 {
		problems = append(problems, errors.New("repair_attempts cannot be negative"))
	}
	if len(s.RepairContinuations) > MaxRepairContinuations {
		problems = append(problems, fmt.Errorf("%d repair continuations are recorded, which exceeds the bound of %d", len(s.RepairContinuations), MaxRepairContinuations))
	}
	for index, continuation := range s.RepairContinuations {
		if err := continuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("repair_continuations[%d]: %w", index, err))
		}
	}
	if s.Environmental != nil {
		if err := s.Environmental.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("environmental: %w", err))
		}
	}
	if s.ReviewRounds < 0 {
		problems = append(problems, errors.New("review_rounds cannot be negative"))
	}
	if len(s.Blocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("blocker is %d bytes, which exceeds the %d byte bound", len(s.Blocker), MaxBlockerBytes))
	}
	if s.IntegrationRetries < 0 {
		problems = append(problems, errors.New("integration_retries cannot be negative"))
	}
	if s.TransientRelaunches < 0 {
		problems = append(problems, errors.New("transient_relaunches cannot be negative"))
	}
	if s.UsageLimitPausedSeconds < 0 {
		problems = append(problems, errors.New("usage_limit_paused_seconds cannot be negative"))
	}
	if s.UsageLimitResetsAt != nil {
		// A pause is an instruction to resume later, so it is only coherent on a
		// run that can still be resumed. Recorded on a terminal run it would
		// promise a continuation that nothing will ever make.
		if s.UsageLimitResetsAt.IsZero() {
			problems = append(problems, errors.New("usage_limit_resets_at cannot be the zero time"))
		}
		if s.Status.Terminal() {
			problems = append(problems, errors.New("usage_limit_resets_at requires a run that is still in flight"))
		}
	}
	if s.PauseCause != "" && s.PauseCause != PauseUsageLimit && s.PauseCause != PauseServerOverload && s.PauseCause != PauseOperatorHold {
		problems = append(problems, errors.New("pause_cause is invalid"))
	}
	if s.OperatorHeldSeconds < 0 {
		problems = append(problems, errors.New("operator_held_seconds cannot be negative"))
	}
	if s.OperatorHeldSince != nil {
		if s.OperatorHeldSince.IsZero() {
			problems = append(problems, errors.New("operator_held_since cannot be the zero time"))
		}
		// A hold is an instruction to carry on once the operator lifts it, so like
		// every other pause it is only coherent on a run something can still carry
		// on: recorded on a terminal run it would promise a continuation that
		// nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("operator_held_since requires a run that is still in flight"))
		}
	}
	if s.ProviderStop != "" {
		if s.ProviderStop != ProviderStopStalled && s.ProviderStop != ProviderStopBudgetExhausted {
			problems = append(problems, errors.New("provider_stop is invalid"))
		}
		// Like a recorded pause, a recorded stop is an instruction to continue
		// later. On a terminal run it would promise a continuation nothing will
		// ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("provider_stop requires a run that is still in flight"))
		}
	}
	if s.DirectivePause != nil {
		if err := s.DirectivePause.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("directive_pause: %w", err))
		}
		// A directive pause is the same kind of instruction as the two above:
		// resume this later. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("directive_pause requires a run that is still in flight"))
		}
	}
	if s.DependencyPause != nil {
		if err := s.DependencyPause.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("dependency_pause: %w", err))
		}
		// A dependency pause is the same kind of instruction as the ones above:
		// resume this later. Recorded on a terminal run it would promise a
		// continuation nothing will ever make.
		if s.Status.Terminal() {
			problems = append(problems, errors.New("dependency_pause requires a run that is still in flight"))
		}
	}
	if s.TargetBranch != "" && !validLocalBranch(s.TargetBranch) {
		problems = append(problems, errors.New("target_branch must be a local branch name"))
	}
	if s.Integration != nil {
		// Recorded integration is a claim that approved work was promoted, so it
		// is only coherent with the approval, the worktree that produced it, and
		// the two independent invocations that authorized it.
		if s.ReviewDecision != ReviewApprove {
			problems = append(problems, errors.New("integration requires an approving review decision"))
		}
		if s.BaseCommit == "" {
			problems = append(problems, errors.New("integration requires the integrated worktree"))
		}
		// Review and integration are reachable only through passing checks, so a
		// promotion recorded alongside a failing one describes a gate that was
		// never actually cleared.
		if s.CheckFailure != nil {
			problems = append(problems, errors.New("integration requires no recorded failing check"))
		}
		// And the same for the gate in front of them: a promotion recorded
		// alongside a refused protected path describes a change that reached
		// integration carrying an edit to the intent it was written against.
		if s.PathRefusal != nil {
			problems = append(problems, errors.New("integration requires no recorded protected-path refusal"))
		}
		// The target is fixed before the work starts, so an integration into a
		// different branch describes a promotion this run was never set up to
		// make.
		if s.TargetBranch != "" && s.Integration.TargetBranch != s.TargetBranch {
			problems = append(problems, fmt.Errorf("integration target_branch %q does not match the recorded target_branch %q", s.Integration.TargetBranch, s.TargetBranch))
		}
		// Integration is only produced by the integrating step, so evidence of it
		// alongside an earlier phase describes a run history that cannot have
		// happened. A reconciler reads the phase to decide what remains to do, so
		// an impossible pairing must not be storable.
		if !s.Phase.reached(PhaseIntegrating) {
			problems = append(problems, errors.New("integration requires the integrating phase or later"))
		}
		problems = append(problems, s.validateIndependentInvocations()...)
		if err := s.Integration.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if s.PullRequest != nil {
		if err := s.PullRequest.Validate(); err != nil {
			problems = append(problems, err)
		}
		// The pull request publishes this run's branch, so a record naming a
		// different one describes a publication this run never made.
		if s.Branch != "" && s.PullRequest.Branch != s.Branch {
			problems = append(problems, fmt.Errorf("pull_request branch %q does not match the run branch %q", s.PullRequest.Branch, s.Branch))
		}
		// The forge is only asked to merge a pull request once the promotion it
		// carries has been made locally, so a merge this run asked for cannot be
		// recorded before the promotion that authorized it is. The merge method is
		// what says the run asked: it is written where the harness makes the merge
		// request, which never runs without an integration in hand.
		//
		// A merged request with no method is the other thing entirely — a merge
		// nobody here asked for, which somebody made on the forge after the run was
		// over. Recording it is an observation of what the forge did rather than a
		// claim that this run promoted anything, and refusing to store it is what
		// froze a failed run's publication at its death-moment state for good.
		if s.PullRequest.Merged && s.PullRequest.MergeMethod != "" && s.Integration == nil {
			problems = append(problems, errors.New("a merged pull request the run asked the forge for requires recorded integration"))
		}
	}
	// A removal is only ever recorded with the evidence that earned it. There are
	// three kinds: the run promoted its own work and cleaned up after it, triage
	// retired what it preserved once another run superseded it, or the
	// convergence sweep retired an empty checkout as hygiene. A record carrying
	// none of them describes cleanup nothing authorized.
	//
	// The third covers the checkout alone. The sweep never touches a branch, so a
	// branch removed with no integration and no superseding run behind it is
	// still a removal with no evidence for it.
	retiredBy := strings.TrimSpace(s.ArtifactsRetiredBy)
	sweptWorktree := s.WorktreeSweptAt != nil
	if ((s.WorktreeRemoved && !sweptWorktree) || s.BranchRemoved) && s.Integration == nil && retiredBy == "" {
		problems = append(problems, errors.New("removed artifacts require recorded integration, the run that superseded this one and retired them, or the convergence sweep that retired the checkout"))
	}
	if sweptWorktree && !s.WorktreeRemoved {
		problems = append(problems, errors.New("a recorded checkout sweep names a checkout that was removed, and this one removed none"))
	}
	if preservedWork := strings.TrimSpace(s.PreservedWorkRef); preservedWork != "" {
		// Only the sweep writes it, and it writes it as part of the removal, so a
		// record carrying one without the sweep describes a capture nothing did.
		if !sweptWorktree {
			problems = append(problems, errors.New("preserved_work_ref requires the checkout sweep that recorded it"))
		}
		// A branch here would be swept by the branch sweep and answer the
		// containment proofs the harness makes about run branches, which is exactly
		// what keeping the capture out of refs/heads avoids.
		if strings.HasPrefix(preservedWork, "refs/heads/") || !strings.HasPrefix(preservedWork, "refs/") {
			problems = append(problems, fmt.Errorf("preserved_work_ref %q must be a ref outside refs/heads", s.PreservedWorkRef))
		}
	}
	if retiredBy != "" {
		if !ValidRunID(retiredBy) {
			problems = append(problems, fmt.Errorf("artifacts_retired_by %q is not a run identifier", s.ArtifactsRetiredBy))
		}
		if retiredBy == s.RunID {
			problems = append(problems, errors.New("a run does not supersede itself; artifacts it removed after its own promotion are recorded by that integration"))
		}
		if !s.WorktreeRemoved && !s.BranchRemoved {
			problems = append(problems, errors.New("a recorded retirement names what it removed, and this one removed neither artifact"))
		}
	}
	// A run is complete only once nothing is left to clean up; anything else is
	// still an outstanding-cleanup marker.
	if s.Integration != nil && s.Phase == PhaseComplete && (!s.WorktreeRemoved || !s.BranchRemoved) {
		problems = append(problems, errors.New("complete phase requires both the worktree and branch to be removed"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid run state: %w", errors.Join(problems...))
	}
	return nil
}

// UsageLimitPaused reports how long this run has already committed to waiting
// out provider usage limits, which is what its remaining pause budget is
// measured against.
func (s State) UsageLimitPaused() time.Duration {
	return time.Duration(s.UsageLimitPausedSeconds) * time.Second
}

// GrantedRepairAttempts is how many further repair attempts triage has granted
// this run across every continuation of it.
func (s State) GrantedRepairAttempts() int {
	granted := 0
	for _, continuation := range s.RepairContinuations {
		granted += continuation.GrantedAttempts
	}
	return granted
}

// CarriedOutRepairAttempts is how much of the item's repair grant this run has
// actually consumed. It is the same sum as above minus the continuations whose
// round the environment refused, because a round handed an empty worktree bought
// the item nothing and must not read afterwards as a grant that was used.
//
// The two are deliberately not one number. This run's own budget still counts a
// refused continuation — the attempt slot was spent, and a run whose attempts
// and budget disagreed would hand a developer a prompt saying which attempt of
// how many this is and be wrong. What the item was charged is a different
// question, and it is this one.
func (s State) CarriedOutRepairAttempts() int {
	carried := 0
	for _, continuation := range s.RepairContinuations {
		if continuation.Returned {
			continue
		}
		carried += continuation.GrantedAttempts
	}
	return carried
}

// ReturnGrantedRound gives back the granted repair round the most recent
// continuation consumed, and reports whether there was one to give back. It is
// the run's half of settling an environmental round: the item's own record keeps
// the grant, and this is what stops the grant reading as carried out.
//
// The most recent continuation is the whole of what can be returned, because it
// is the one that bought the round now settling. An earlier one bought a round
// that was already judged on its own merits.
func (s *State) ReturnGrantedRound() bool {
	last := len(s.RepairContinuations) - 1
	// A settle asked twice for the same round finds the return already made: the
	// record is what the caller wanted it to be, and nothing more is given back.
	if last < 0 || s.RepairContinuations[last].Returned {
		return false
	}
	s.RepairContinuations[last].Returned = true
	return true
}

// RepairBudget is how many repair attempts this run may make in total: what the
// project configured, plus what triage has granted it since. It is derived here
// rather than at each reader because the two have to agree — the loop that stops
// spending and the prompt that tells a developer which attempt of how many this
// is are the same budget said twice, and a budget that read differently in the
// two would hand back an attempt describing itself as the last.
func (s State) RepairBudget(configured int) int {
	if configured < 0 {
		configured = 0
	}
	return configured + s.GrantedRepairAttempts()
}

// OperatorHeld reports how much of this run's elapsed time the operator's hold
// accounts for. It bounds nothing — nothing bounds an operator — and is the
// ledger's answer to why a run took as long as it did.
func (s State) OperatorHeld() time.Duration {
	return time.Duration(s.OperatorHeldSeconds) * time.Second
}

// Outstanding reports that a run still owes a step somebody has to take. A run
// that never reached a terminal status was interrupted mid-flight. A terminal
// run with nothing integrated owes nothing: its artifacts are deliberately
// preserved. An integrated one owes cleanup until it reaches the complete
// phase, because integration is what schedules the removal of the artifacts
// that produced it, and it owes the answer to a merge the forge queued for as
// long as that merge is unresolved — the forge performs it minutes after the
// run itself is over, and what it did with it has to be found out.
func (s State) Outstanding() bool {
	if !s.Status.Terminal() {
		return true
	}
	if s.Integration == nil {
		return false
	}
	return s.Phase != PhaseComplete || (s.PullRequest != nil && s.PullRequest.MergeQueued)
}

// validateIndependentInvocations enforces what an integrated change claims: two
// separate provider invocations, each with its own recorded session and its own
// declared model selector. A missing or reused session identity means nothing
// independent was proven, so it must never appear alongside integration.
func (s State) validateIndependentInvocations() []error {
	var problems []error
	// Compare the normalized identifiers: two sessions that differ only in
	// surrounding whitespace are one session, and must not read as independent.
	developer := strings.TrimSpace(s.ProviderSessionID)
	reviewer := strings.TrimSpace(s.ReviewSessionID)
	if developer == "" || reviewer == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer session identifiers"))
	} else if developer == reviewer {
		problems = append(problems, errors.New("integration requires distinct developer and reviewer session identifiers"))
	}
	if strings.TrimSpace(s.ProviderModel) == "" || strings.TrimSpace(s.ReviewModel) == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer model selectors"))
	}
	return problems
}

// Validate rejects integration evidence that cannot describe a real promotion.
func (i Integration) Validate() error {
	var problems []error
	if strings.TrimSpace(i.TargetBranch) == "" {
		problems = append(problems, errors.New("integration target_branch is required"))
	} else if !validLocalBranch(i.TargetBranch) {
		problems = append(problems, errors.New("integration target_branch must be a local branch name"))
	}
	for _, commit := range []struct {
		field string
		value string
	}{
		{field: "source_commit", value: i.SourceCommit},
		{field: "target_commit", value: i.TargetCommit},
		{field: "previous_target_commit", value: i.PreviousTargetCommit},
	} {
		if !commitPattern.MatchString(commit.value) {
			problems = append(problems, fmt.Errorf("integration %s is invalid", commit.field))
		}
	}
	if i.SourceCommit != "" && i.SourceCommit == i.PreviousTargetCommit {
		problems = append(problems, errors.New("integration did not move the target"))
	}
	// Integration is fast-forward only: the target ends up at exactly the commit
	// the harness created. Any other pair describes a merge or a reset that this
	// harness never performs, so it is rejected rather than recorded.
	if i.SourceCommit != "" && i.TargetCommit != "" && i.SourceCommit != i.TargetCommit {
		problems = append(problems, errors.New("integration target_commit must equal the fast-forwarded source_commit"))
	}
	return errors.Join(problems...)
}

// phaseOrder lists the phases in the order a run reaches them, so evidence can
// be checked against the step that must have produced it.
var phaseOrder = []Phase{
	PhaseDeveloping, PhaseChecking, PhaseReviewing,
	PhaseIntegrating, PhaseCompleting, PhaseCleaningUp, PhaseComplete,
}

// reached reports whether this phase is at or past the given one. An unknown
// phase reaches nothing: it is already rejected as invalid, and treating it as
// satisfying an ordering would let a malformed record pass a coherence check.
func (p Phase) reached(other Phase) bool {
	position := func(phase Phase) int {
		for index, candidate := range phaseOrder {
			if candidate == phase {
				return index
			}
		}
		return -1
	}
	self, target := position(p), position(other)
	return self >= 0 && target >= 0 && self >= target
}

func (p Phase) Valid() bool {
	switch p {
	case PhaseDeveloping, PhaseChecking, PhaseReviewing, PhaseIntegrating, PhaseCompleting, PhaseCleaningUp, PhaseComplete:
		return true
	default:
		return false
	}
}

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled || s == StatusTimedOut
}

func DefaultRoot(getenv func(string) string, userHomeDir func() (string, error), goos string) (string, error) {
	if value := strings.TrimSpace(getenv("YOYODYNE_STATE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("YOYODYNE_STATE_HOME must be an absolute path")
		}
		return filepath.Clean(value), nil
	}
	if value := strings.TrimSpace(getenv("XDG_STATE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("XDG_STATE_HOME must be an absolute path")
		}
		return filepath.Join(filepath.Clean(value), "yoyodyne"), nil
	}

	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Yoyodyne", "state"), nil
	case "windows":
		if localAppData := strings.TrimSpace(getenv("LOCALAPPDATA")); localAppData != "" {
			if !filepath.IsAbs(localAppData) {
				return "", errors.New("LOCALAPPDATA must be an absolute path")
			}
			return filepath.Join(localAppData, "Yoyodyne", "state"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Yoyodyne", "state"), nil
	default:
		return filepath.Join(home, ".local", "state", "yoyodyne"), nil
	}
}

func SystemDefaultRoot(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	return DefaultRoot(getenv, userHomeDir, runtime.GOOS)
}
