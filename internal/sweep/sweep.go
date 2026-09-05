// Package sweep is what a role says at the end of a recurring pass over its own
// domain: what it found, what it did about each thing, and whether one turn was
// enough.
//
// A recurring task wakes a role on a cadence and nobody is watching the turn, so
// the prose it answers with reaches an operator only if something keeps it. That
// is what this is for. The fenced block is the same channel shape every other
// structured thing an agent says already uses — a report, a proposed amendment,
// an ask — for the reason that package gives: the splitting is identical, so a
// channel added later inherits the rules rather than a copy of them.
//
// Two things in it are load-bearing beyond the record. The status is how a heavy
// pass says one turn was not enough, which is what lets the harness take another
// turn rather than have the role try to fit a morning's work inside one and
// overflow whatever bounds that turn has. And every finding carries what was
// filed for it, because a fix with nothing filed is a silent repair — the thing
// the recurring sweep exists to stop being normal — and a record that could not
// tell the two apart could not show it either way.
//
// Nothing here decides anything or authorizes anything. A role acting on what it
// found does so under the authority it already holds, through the paths it
// already acts through; this is its account of having done so, and an account
// that claimed more than the role did would be caught where every other claim is
// — against the durable records the acts themselves left.
package sweep

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the one block a swept turn may carry its account in. It is a
// distinct language tag rather than plain JSON so a sweep result can never be
// confused with JSON a role happens to be discussing.
const Fence = "```yoyodyne-sweep"

// The bounds one turn's account is held to. A pass that found forty things in
// one turn has found something systemic, and the right answer to that is the
// summary saying so rather than forty entries nobody reads — so the cap is small
// enough to force that sentence and large enough that an ordinary busy turn fits
// inside it. The block bound is the untrusted payload the whole thing is decoded
// from.
const (
	MaxFindings     = 20
	MaxQuestions    = 5
	MaxSummaryBytes = 4 << 10
	MaxTextBytes    = 2 << 10
	MaxBlockBytes   = 32 << 10
)

// The bounds a whole firing's merged account is held to, which are deliberately
// not the per-turn ones above.
//
// A firing takes several turns and folds their accounts together, so a pass that
// legitimately reported the per-turn maximum on each of four turns has four times
// that many findings — and holding the merged account to the per-turn cap would
// refuse exactly the heavy pass iteration exists for. It refused it at the worst
// possible moment, too: the record is validated as it is written, so what was
// discarded was the whole durable report of the busiest passes, which is the one
// thing the recurring sweep exists to produce.
//
// So the pass bounds are what MaxMergedTurns turns at the per-turn caps come to.
// MaxMergedTurns is stated here rather than read from the configuration because
// this package is the channel and not the schedule; a test where both are visible
// keeps it at or above the largest turn bound a task may configure.
const (
	MaxMergedTurns   = 10
	MaxPassFindings  = MaxFindings * MaxMergedTurns
	MaxPassQuestions = MaxQuestions * MaxMergedTurns
)

// maxFindingsText and maxQuestionsText are the same bounds as the contract
// states them; a test keeps the numbers a role is told equal to the ones
// enforced here.
const (
	maxFindingsText  = "20"
	maxQuestionsText = "5"
)

// Status is whether the pass finished inside the turn it was given.
type Status string

const (
	// StatusComplete is a pass that is done: everything it found was dealt with
	// or written down, and there is nothing it is waiting on a further turn for.
	StatusComplete Status = "complete"
	// StatusMore is a pass with more to do than one turn holds. It is not a
	// failure and not a finding: it is the role saying so rather than trying to
	// fit the rest into a turn that will not hold it.
	StatusMore Status = "more"
)

func (s Status) Valid() bool {
	switch s {
	case StatusComplete, StatusMore:
		return true
	default:
		return false
	}
}

// Disposition is what became of one thing a pass found. The vocabulary is
// deliberately about what the role did rather than about how bad the thing is:
// a sweep is judged by whether what it found moved, and a severity word here
// would invite the account to argue its own importance instead.
type Disposition string

const (
	// DispositionFixed is a thing the role resolved itself, inside the authority
	// it already holds.
	DispositionFixed Disposition = "fixed"
	// DispositionFiled is a thing the role did not resolve and wrote down for
	// whoever owns it.
	DispositionFiled Disposition = "filed"
	// DispositionConsulted is a thing waiting on another role's ruling, which the
	// pass asked for.
	DispositionConsulted Disposition = "consulted"
	// DispositionLeft is a thing the role looked at and deliberately did nothing
	// about, with the reason in its detail. It is a real answer: a finding
	// somebody has considered and left alone is not one nobody has seen.
	DispositionLeft Disposition = "left"
)

func (d Disposition) Valid() bool {
	switch d {
	case DispositionFixed, DispositionFiled, DispositionConsulted, DispositionLeft:
		return true
	default:
		return false
	}
}

// Finding is one unresolved thing a pass found and what became of it.
type Finding struct {
	// Issue is what was found, in the words a person reads.
	Issue string `json:"issue"`
	// Disposition is what the role did about it, and Detail is how.
	Disposition Disposition `json:"disposition"`
	Detail      string      `json:"detail,omitempty"`
	// Filed names the work filed for the root cause of this finding: the
	// identifiers where the role has them, and it is empty where nothing was
	// filed. It is a field of its own rather than prose inside the detail because
	// it is the one thing a week of these reports is read for — a fix that filed
	// nothing is a repair that leaves the cause in place, and a reader must not
	// have to infer which happened from a sentence.
	Filed []string `json:"filed,omitempty"`
}

// SilentRepair reports a fix that filed nothing for its root cause. It is
// stated here rather than computed by each reader for the reason the field
// above exists: it is the question the whole record is kept to answer.
func (f Finding) SilentRepair() bool {
	return f.Disposition == DispositionFixed && len(f.Filed) == 0
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	switch issue := strings.TrimSpace(f.Issue); {
	case issue == "":
		problems = append(problems, errors.New("issue is required"))
	case len(issue) > MaxTextBytes:
		problems = append(problems, fmt.Errorf("issue is %d bytes, limit is %d", len(issue), MaxTextBytes))
	}
	if !f.Disposition.Valid() {
		problems = append(problems, fmt.Errorf("disposition %q must be %q, %q, %q, or %q",
			f.Disposition, DispositionFixed, DispositionFiled, DispositionConsulted, DispositionLeft))
	}
	if len(f.Detail) > MaxTextBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, limit is %d", len(f.Detail), MaxTextBytes))
	}
	for i, filed := range f.Filed {
		if strings.TrimSpace(filed) == "" {
			problems = append(problems, fmt.Errorf("filed[%d] is blank", i))
		}
		if len(filed) > MaxTextBytes {
			problems = append(problems, fmt.Errorf("filed[%d] is %d bytes, limit is %d", i, len(filed), MaxTextBytes))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid finding: %w", err)
	}
	return nil
}

// Result is one turn's account of a recurring pass.
type Result struct {
	Status  Status `json:"status"`
	Summary string `json:"summary"`
	// Findings are what the pass found, and are empty on a pass that found
	// nothing — which on a healthy harness is most of them, and is the answer
	// this record most wants to be able to state plainly.
	Findings []Finding `json:"findings,omitempty"`
	// Questions are what the pass needs a person to settle. They are separate
	// from the findings because they are the only part of a report that asks for
	// anything: a report with no questions needs no attention, which is what
	// makes reading these at leisure possible at all.
	Questions []string `json:"questions,omitempty"`
}

// Validate reports every contract violation in the result at once.
//
// It is the contract a whole firing's account is held to, so its volume bounds
// are the pass ones: this is what the durable record validates against, and a
// record is written per firing rather than per turn. What one turn may send is a
// tighter question, asked by validateTurn where a turn's block is decoded.
func (r Result) Validate() error {
	var problems []error
	if !r.Status.Valid() {
		problems = append(problems, fmt.Errorf("status %q must be %q or %q", r.Status, StatusComplete, StatusMore))
	}
	switch summary := strings.TrimSpace(r.Summary); {
	case summary == "":
		problems = append(problems, errors.New("summary is required"))
	case len(summary) > MaxSummaryBytes:
		problems = append(problems, fmt.Errorf("summary is %d bytes, limit is %d", len(summary), MaxSummaryBytes))
	}
	if len(r.Findings) > MaxPassFindings {
		problems = append(problems, fmt.Errorf("%d findings in one pass, limit is %d", len(r.Findings), MaxPassFindings))
	}
	for i, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(r.Questions) > MaxPassQuestions {
		problems = append(problems, fmt.Errorf("%d questions in one pass, limit is %d", len(r.Questions), MaxPassQuestions))
	}
	for i, question := range r.Questions {
		switch trimmed := strings.TrimSpace(question); {
		case trimmed == "":
			problems = append(problems, fmt.Errorf("questions[%d] is blank", i))
		case len(trimmed) > MaxTextBytes:
			problems = append(problems, fmt.Errorf("questions[%d] is %d bytes, limit is %d", i, len(trimmed), MaxTextBytes))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep result: %w", err)
	}
	return nil
}

// validateTurn holds one turn's account to what a turn may send, which is the
// tighter of the two contracts and the one the role is told about. Everything
// else about the account is the same question either way, so it defers to
// Validate for the rest rather than restating it.
func (r Result) validateTurn() error {
	var problems []error
	if len(r.Findings) > MaxFindings {
		problems = append(problems, fmt.Errorf("%d findings in one turn, limit is %d", len(r.Findings), MaxFindings))
	}
	if len(r.Questions) > MaxQuestions {
		problems = append(problems, fmt.Errorf("%d questions in one turn, limit is %d", len(r.Questions), MaxQuestions))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid sweep result: %w", err)
	}
	return r.Validate()
}

// SilentRepairs counts the fixes this pass filed nothing for. It is what a
// report says out loud, and what a week of them is read against.
func (r Result) SilentRepairs() int {
	silent := 0
	for _, finding := range r.Findings {
		if finding.SilentRepair() {
			silent++
		}
	}
	return silent
}

// Merge folds a later turn of the same pass into this one. The findings
// accumulate and the last turn's status and summary stand, because a pass that
// took three turns found everything all three of them found and finished the way
// the last one says it did.
//
// It bounds what it accumulates at the pass caps, and that bounding is the point
// rather than a detail: what it produces is written straight into a durable
// record that validates as it is written, so an unbounded merge is a merge that
// can make the record unwritable — and the report it would then lose is the whole
// account of the busiest pass. Bounding here keeps the earlier findings, which
// are the ones a later turn was told not to repeat, and says in the summary how
// many were dropped, because a silently shortened list reads as a pass that found
// less than it did.
func (r Result) Merge(next Result) Result {
	merged := Result{Status: next.Status, Summary: next.Summary}
	if strings.TrimSpace(merged.Summary) == "" {
		merged.Summary = r.Summary
	}
	findings := append(append([]Finding(nil), r.Findings...), next.Findings...)
	questions := append(append([]string(nil), r.Questions...), next.Questions...)
	droppedFindings, droppedQuestions := 0, 0
	if len(findings) > MaxPassFindings {
		droppedFindings = len(findings) - MaxPassFindings
		findings = findings[:MaxPassFindings]
	}
	if len(questions) > MaxPassQuestions {
		droppedQuestions = len(questions) - MaxPassQuestions
		questions = questions[:MaxPassQuestions]
	}
	merged.Findings = findings
	merged.Questions = questions
	merged.Summary = noteDropped(merged.Summary, droppedFindings, droppedQuestions)
	return merged
}

// noteDropped says in the summary what the pass bounds cut, and keeps the summary
// inside its own bound while doing it. A note that pushed the summary past what
// the record accepts would lose the report it exists to preserve.
func noteDropped(summary string, findings, questions int) string {
	if findings == 0 && questions == 0 {
		return summary
	}
	note := fmt.Sprintf("(This pass reached its bound of %d findings and %d questions; %d finding(s) and %d question(s) from its later turns are not listed.)",
		MaxPassFindings, MaxPassQuestions, findings, questions)
	joined := strings.TrimSpace(summary)
	if joined != "" {
		joined += " "
	}
	joined += note
	if len(joined) <= MaxSummaryBytes {
		return joined
	}
	// The note is what a reader most needs of the two, so it is the part kept
	// whole: the summary is cut back far enough to leave room for it.
	room := MaxSummaryBytes - len(note) - 2
	if room <= 0 {
		return note[:min(len(note), MaxSummaryBytes)]
	}
	return strings.TrimSpace(summary[:min(len(summary), room)]) + " " + note
}

// Extract splits a reply into what the role said and the account it gave of its
// pass. The account comes only from the fenced block: prose describing what was
// found is not an account, and a block the contract does not accept is refused
// rather than half-read.
//
// A reply carrying no block is not a failure here. It is a role that answered in
// prose, which is a thing a person reading the conversation can still make sense
// of; what it costs is the structure, and the caller says so rather than losing
// the turn over it.
func Extract(reply string) (string, *Result, error) {
	block, err := fenced.Split(reply, Fence, "sweep")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	result, err := Decode(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, result, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what is written into a
// durable report has to be exactly what the role wrote.
func Decode(payload string) (*Result, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode sweep: the sweep block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("decode sweep: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded Result
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode sweep: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode sweep: unexpected trailing content after the sweep result")
	}
	// Held to the per-turn contract, which is the tighter of the two and the one
	// the role was told: this is one turn's block, whatever a whole pass may
	// accumulate across several of them.
	if err := decoded.validateTurn(); err != nil {
		return nil, err
	}
	return &decoded, nil
}

// Contract is what a role is told about this block when the harness wakes it.
// It is here beside the rules rather than beside the message that carries it,
// so the bounds a role is given and the bounds enforced on what it sends back
// are one statement.
func Contract() string {
	return strings.Join([]string{
		"End your answer with exactly one block of this shape, and nothing after it:",
		"",
		Fence,
		`{"status":"complete|more","summary":"what this pass found, in a sentence or two","findings":[{"issue":"what you found","disposition":"fixed|filed|consulted|left","detail":"what you did and why","filed":["work you filed for its root cause"]}],"questions":["what only a person can settle"]}`,
		"```",
		"",
		`"complete" is a pass with nothing left to do. "more" is a pass with more than this turn holds — say it rather than rushing the rest, and the harness gives you another turn.`,
		`A pass that found nothing carries no findings and says so in the summary; that is the ordinary result and it is worth stating plainly.`,
		`Every fix carries the work you filed for its root cause in "filed". A fix that files nothing is a silent repair, and the report says so.`,
		"At most " + maxFindingsText + " findings and " + maxQuestionsText + " questions in one turn: a pass that found more than that has found something systemic, and the summary is where that is said.",
	}, "\n")
}
