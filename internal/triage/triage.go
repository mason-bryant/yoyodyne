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
package triage

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SchemaVersion is versioned independently of run state, and of the collected
// reports beside it, for the same reason each of those is: an entry outlives
// the run that produced it, it has no phase and nothing to integrate, and it is
// written once and never revised.
const SchemaVersion = 1

// Class is what stopped. The two are kept apart because they are found
// differently and read differently: a stopped run is an event the harness was
// present for, and a stuck publication is a thing that has not happened, which
// nothing can be present for and only a scan can notice.
type Class string

const (
	// ClassStoppedRun is a run that ended on a durable blocker.
	ClassStoppedRun Class = "stopped_run"
	// ClassPublication is an approved publication that did not finish: one the
	// forge has not merged past the configured stuck-merge age, or one the
	// harness already recorded as outstanding — a merge the forge dropped, or one
	// it performed that could not be confirmed.
	ClassPublication Class = "publication"
)

func (c Class) Valid() bool {
	switch c {
	case ClassStoppedRun, ClassPublication:
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
	case ClassPublication:
		return "unfinished publication"
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

// Counters are what the item has already spent, beside what the project
// configured it may spend. Both halves travel together on purpose: a
// development manager that sees five rounds without seeing the cap of four
// cannot tell whether granting another is a decision or a contradiction.
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
}

// Exhausted reports an item that has already accumulated the configured review
// rounds, which is the one counter that decides something on its own: past it,
// another repair is not triage's to grant.
func (c Counters) Exhausted() bool { return c.ReviewRounds >= c.ReviewRoundsCap }

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
	Counters    Counters     `json:"counters"`
}

// Key names the durable event an entry is about. It is derived rather than
// generated so that the same event yields the same key in every process that
// notices it, which is the whole of what makes docketing idempotent: a run stop
// is one event whether the run recorded it or a later sweep found it, and a
// publication is one event however many sweeps walk past it.
func Key(class Class, runID string) string {
	return string(class) + ":" + strings.TrimSpace(runID)
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	if e.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !e.Class.Valid() {
		problems = append(problems, fmt.Errorf("class %q must be %q or %q", e.Class, ClassStoppedRun, ClassPublication))
	}
	switch key := strings.TrimSpace(e.Key); {
	case key == "":
		problems = append(problems, errors.New("key is required"))
	case len(key) > MaxKeyBytes:
		problems = append(problems, fmt.Errorf("key is %d bytes, limit is %d", len(key), MaxKeyBytes))
	case e.Class.Valid() && key != Key(e.Class, e.RunID):
		// A key that does not derive from the event it claims to describe would
		// make two records of one stoppage, which is the one thing the key exists
		// to prevent.
		problems = append(problems, fmt.Errorf("key %q does not name the %s event of run %s", e.Key, e.Class, e.RunID))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(e.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
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
	// Each class is held to the evidence that makes it the thing it claims to
	// be. An entry that cannot say what stopped is an entry nobody can act on,
	// which is worse than no entry: it looks like coverage.
	switch e.Class {
	case ClassStoppedRun:
		if strings.TrimSpace(e.Blocker) == "" {
			problems = append(problems, errors.New("a stopped run entry carries the durable blocker that stopped it"))
		}
		if e.Publication != nil {
			problems = append(problems, errors.New("a stopped run entry describes a run rather than a publication"))
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
	fmt.Fprintf(&rendered, "  [%s] %s on %s (%s)\n",
		e.Class.Title(), e.RecordedAt.UTC().Format(time.RFC3339), e.item(), e.RunID)
	if e.Blocker != "" {
		rendered.WriteString(indented("Blocker", e.Blocker))
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
	fmt.Fprintf(&rendered, "      Triage counters: %d of %d review round(s) used%s; %d repair attempt(s) spent in this run; a grant would hand it %d\n",
		e.Counters.ReviewRounds, e.Counters.ReviewRoundsCap, exhaustedNote(e.Counters),
		e.Counters.RepairAttempts, e.Counters.RepairGrantAttempts)
	return rendered.String()
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

func exhaustedNote(counters Counters) string {
	if counters.Exhausted() {
		return " (the cap is reached: another repair is not triage's to grant)"
	}
	return ""
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
