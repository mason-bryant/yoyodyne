// Package report collects what an agent noticed while its own work succeeded.
//
// Every role can already reach the operator by failing: a spent repair budget
// becomes a durable blocker, and a failed run is reported where the operator
// looks. Nothing carried the other thing — a risk worked around, an assumption
// that may not hold, a defect outside the work that was assigned — which
// survived only as prose in a summary nobody surfaces. A report is that, and it
// is deliberately not a blocker: it is recorded beside the work rather than on
// it, nothing waits on it, and the run that produced it ends exactly as it
// would have.
//
// Collection is raw on purpose. The record is structured enough to be filtered
// later — the reporting role and agent, the run and work item it came from, a
// severity, and the text — because a pile with no structure cannot be triaged
// once there is volume, and no agent decides for the operator what is worth
// keeping.
package report

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// SchemaVersion is versioned independently of run and conversation state. A
// report is neither: it outlives the run that produced it, it has no phase and
// nothing to integrate, and it is written once and never revised.
const SchemaVersion = 1

// Fence opens the one block a reply may carry reports in. It is a distinct
// language tag rather than plain JSON so a report can never be confused with
// JSON an agent happens to be discussing.
const Fence = "```yoyodyne-report"

// MaxEntriesPerReply bounds how many reports one reply may carry, and
// MaxBlockBytes bounds the untrusted payload the block is decoded from. Volume
// is what makes a channel like this worthless, so a reply that files a whole
// list of observations is refused rather than collected.
// maxEntriesPerReplyText is the same bound as the contract states it; a test
// keeps the number an agent is told equal to the one enforced here.
const (
	MaxEntriesPerReply     = 5
	maxEntriesPerReplyText = "5"
	MaxBlockBytes          = 16 << 10
)

// MaxMessageBytes bounds one report's text. It is generous for the paragraph a
// report actually is and small enough that nothing can push a document into the
// collected pile.
const MaxMessageBytes = 4 << 10

// Severity is how much attention a report is asking for. The vocabulary is
// deliberately not the reviewer's blocker/major/minor: a review finding decides
// whether work is repaired, and a report decides nothing at all, so a shared
// word would invite a report to be read as a verdict.
type Severity string

const (
	// SeverityCritical is something already wrong that will cost somebody.
	SeverityCritical Severity = "critical"
	// SeverityWarning is a real risk or a fragile assumption that has not cost
	// anything yet.
	SeverityWarning Severity = "warning"
	// SeverityNote is worth knowing and asks for nothing.
	SeverityNote Severity = "note"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityCritical, SeverityWarning, SeverityNote:
		return true
	default:
		return false
	}
}

// MarkerWidth is the column a severity's marker occupies in a listing, so that
// what follows it lines up whether or not the line carries one.
const MarkerWidth = 2

// Marker is the mark a severity is picked out by where nothing may be dressed:
// a listing redirected to a file, a terminal that says it is dumb, an operator
// who set NO_COLOR. Colour is an addition everywhere in this harness and never
// the carrier of meaning, so this is what has to do the work on its own.
//
// It sits at the left of the line, because a reader scanning a pile is reading
// down the margin rather than across every line, and a note is marked with
// nothing at all: a mark on every line marks none of them.
func (s Severity) Marker() string {
	switch s {
	case SeverityCritical:
		return "!!"
	case SeverityWarning:
		return "!"
	default:
		return ""
	}
}

// Prefix is the marker and the space that separates it from what follows, or
// nothing where the severity carries no marker. It is what a line of prose is
// marked with, as opposed to a listing, which pads the marker to MarkerWidth so
// its identifiers line up.
func (s Severity) Prefix() string {
	if marker := s.Marker(); marker != "" {
		return marker + " "
	}
	return ""
}

// rank orders the severities by how much attention each asks for. It is what
// lets a pile be read worst-first: a reader working through what nobody has
// dealt with yet wants the thing already costing somebody before the thing that
// asks for nothing.
func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	case SeverityNote:
		return 2
	default:
		// A severity outside the vocabulary cannot be stored, so this is reached
		// only by a caller ordering something it built itself. It sorts last
		// rather than first: an unrecognized word is not evidence of urgency.
		return 3
	}
}

// Entry is one report exactly as an agent wrote it: a severity and the text.
// Everything else about a report — which role, which run, which work item — is
// what the harness knows and the agent does not get to assert.
type Entry struct {
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Validate reports every contract violation in the entry at once.
func (e Entry) Validate() error {
	var problems []error
	if !e.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q",
			e.Severity, SeverityCritical, SeverityWarning, SeverityNote))
	}
	switch message := strings.TrimSpace(e.Message); {
	case message == "":
		problems = append(problems, errors.New("message is required"))
	case len(message) > MaxMessageBytes:
		problems = append(problems, fmt.Errorf("message is %d bytes, limit is %d", len(message), MaxMessageBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid report: %w", err)
	}
	return nil
}

// Report is one durable collected report: the entry an agent wrote, and the
// attribution the harness supplied for it. The attribution is what makes the
// pile triageable later without deciding now how it will be filtered.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Role is the contract the reporter worked under and Agent is the configured
	// agent that filled it. Both are recorded because a project may configure
	// more than one agent for a role, and "which developer said this" is then a
	// different question from "a developer said this".
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// RunID is the invocation the report came out of: a run for a role the
	// pipeline executes, a conversation for one the operator talks to. It is the
	// same identifier the event log for that invocation is named by, so a report
	// leads back to everything else that invocation recorded.
	RunID string `json:"run_id"`
	// WorkItemID is the item the reporter was working on. It is absent for a
	// conversation, which has no assigned work rather than an unknown one.
	WorkItemID   string           `json:"work_item_id,omitempty"`
	ProductID    domain.ProductID `json:"product_id"`
	RepositoryID string           `json:"repository_id"`
	Severity     Severity         `json:"severity"`
	Message      string           `json:"message"`
	RecordedAt   time.Time        `json:"recorded_at"`
}

var idPattern = regexp.MustCompile(`^report-[a-f0-9]{32}$`)

// ValidID reports whether an identifier names a report. It is exported because
// the identifier travels now: a report is read out of the pile and named back by
// whoever says what became of it, and a handling recorded against an identifier
// nothing checked is a handling for a report that does not exist.
func ValidID(id string) bool {
	return idPattern.MatchString(strings.TrimSpace(id))
}

func NewID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate report id: %w", err)
	}
	return "report-" + hex.EncodeToString(bytes), nil
}

// Validate reports every contract violation in the collected report at once.
func (r Report) Validate() error {
	var problems []error
	if r.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(r.ID) {
		problems = append(problems, errors.New("id is invalid"))
	}
	if err := domain.ValidateIdentifier("role", string(r.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(r.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", r.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	if r.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	problems = append(problems, Entry{Severity: r.Severity, Message: r.Message}.Validate())
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid collected report: %w", err)
	}
	return nil
}

// Extract splits a reply into what the agent said and what it reported. Reports
// come only from the fenced block: prose describing a worry is not a report, and
// a block the contract does not accept is refused rather than half-read.
//
// What the agent said comes back without the block whichever way that goes. A
// block that could not be read takes everything from its fence onwards with it,
// because the contract puts the block last and what precedes it is the role's
// actual output — a verdict that still has to decode, a summary that still has
// to read as prose. A report never costs its own reply, so the error is for
// saying the report was lost rather than for abandoning anything.
func Extract(reply string) (string, []Entry, error) {
	// A second report block is refused by the split, because a reply reports what
	// it has to report in one place and reading only the first would silently
	// drop the rest.
	block, err := fenced.Split(reply, Fence, "report")
	if err != nil {
		return block.Before, nil, err
	}
	if !block.Found {
		return block.Before, nil, nil
	}
	entries, err := Decode(block.Payload)
	if err != nil {
		return block.Before, nil, err
	}
	return block.Rest, entries, nil
}

// document is the payload shape of the fenced block. It always carries a list,
// so reporting one thing and reporting three are the same protocol.
type document struct {
	Reports []Entry `json:"reports"`
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what an operator is
// shown has to be exactly what the agent wrote.
func Decode(payload string) ([]Entry, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode reports: the report block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return nil, fmt.Errorf("decode reports: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var decoded document
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode reports: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode reports: unexpected trailing content after the reports")
	}
	if len(decoded.Reports) == 0 {
		return nil, errors.New("decode reports: a report block must carry at least one report")
	}
	if len(decoded.Reports) > MaxEntriesPerReply {
		return nil, fmt.Errorf("decode reports: %d reports in one reply, limit is %d", len(decoded.Reports), MaxEntriesPerReply)
	}
	var problems []error
	for i, entry := range decoded.Reports {
		if err := entry.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("reports[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid reports: %w", errors.Join(problems...))
	}
	return decoded.Reports, nil
}

// Attribution is what the harness knows about a reporter, which is everything
// about a report except its severity and its text.
type Attribution struct {
	Role         domain.AgentRole
	Agent        string
	RunID        string
	WorkItemID   string
	ProductID    domain.ProductID
	RepositoryID string
}

// Collect turns what an agent reported into durable records. Each one gets its
// own identity and the same attribution, because the reporter is a property of
// the invocation rather than of anything the agent said.
func Collect(entries []Entry, attribution Attribution, now time.Time) ([]Report, error) {
	collected := make([]Report, 0, len(entries))
	for _, entry := range entries {
		id, err := NewID()
		if err != nil {
			return nil, err
		}
		reported := Report{
			SchemaVersion: SchemaVersion,
			ID:            id,
			Role:          attribution.Role,
			Agent:         strings.TrimSpace(attribution.Agent),
			RunID:         attribution.RunID,
			WorkItemID:    attribution.WorkItemID,
			ProductID:     attribution.ProductID,
			RepositoryID:  attribution.RepositoryID,
			Severity:      entry.Severity,
			Message:       strings.TrimSpace(entry.Message),
			RecordedAt:    now.UTC(),
		}
		if err := reported.Validate(); err != nil {
			return nil, err
		}
		collected = append(collected, reported)
	}
	return collected, nil
}

// HandlingSchemaVersion is versioned independently of the report it is about. A
// handling is a different record with a different lifetime: the report is
// written once by whoever noticed something, and what became of it is written
// later by whoever dealt with it.
const HandlingSchemaVersion = 1

// MaxHandlingReasonBytes bounds what a handling may say became of a report. It
// matches the bound on the report's own text, because saying what was done about
// something is not a smaller statement than noticing it.
const MaxHandlingReasonBytes = 4 << 10

// Handling is what became of one collected report, recorded beside the pile
// rather than on the report.
//
// The report itself stays exactly as it was written — that is what makes the
// pile evidence rather than a worklist somebody has been editing — so a
// disposition is its own append-only record keyed by the report it settles.
// What it buys is the distinction the pile could not make before: a report
// somebody has acted on and a report nobody has read look identical in a listing
// of everything ever reported, and the second is the only one that still needs
// anybody.
//
// It records no outcome vocabulary on purpose. "Admitted as work", "already
// fixed", "not worth doing" are all the same fact to everything that reads this
// — somebody looked and decided — and the reason says which, in the words of
// whoever decided it.
type Handling struct {
	SchemaVersion int `json:"schema_version"`
	// ReportID is the report this settles. It is the whole of the key: a report
	// handled twice is two records and the later one is what is read, which is
	// the right way round for an append-only log.
	ReportID string `json:"report_id"`
	// Role is the contract whoever handled it worked under and Agent the
	// configured agent that filled it, recorded for the reason the report records
	// them: "the product manager dealt with this" and "which product manager" are
	// different questions in a project that configures two.
	Role  domain.AgentRole `json:"role"`
	Agent string           `json:"agent,omitempty"`
	// RunID is the invocation the handling was recorded in, which for the role
	// that triages the pile is a conversation. It leads back to what was said to
	// arrive at it, which is the part the reason cannot carry.
	RunID        string           `json:"run_id"`
	ProductID    domain.ProductID `json:"product_id"`
	RepositoryID string           `json:"repository_id"`
	// Reason is what was done about the report, or why it needed nothing. It is
	// required: a handling with no reason takes a report out of everybody's view
	// and says nothing about why, which is worse than leaving it in.
	Reason     string    `json:"reason"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the handling at once.
func (h Handling) Validate() error {
	var problems []error
	if h.SchemaVersion != HandlingSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", HandlingSchemaVersion))
	}
	if !ValidID(h.ReportID) {
		problems = append(problems, errors.New("report id is invalid"))
	}
	if err := domain.ValidateIdentifier("role", string(h.Role)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(h.RunID) == "" {
		problems = append(problems, errors.New("run id is required"))
	}
	if err := domain.ValidateIdentifier("product id", string(h.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if err := domain.ValidateIdentifier("repository id", h.RepositoryID); err != nil {
		problems = append(problems, err)
	}
	switch reason := strings.TrimSpace(h.Reason); {
	case reason == "":
		problems = append(problems, errors.New("reason is required"))
	case len(reason) > MaxHandlingReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxHandlingReasonBytes))
	}
	if h.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid report handling: %w", err)
	}
	return nil
}

// Handled indexes handlings by the report each is about, keeping the most
// recently recorded one where a report has been handled more than once. The
// later record is the current answer for the same reason the log is appended to
// rather than rewritten: what was decided first is history, and history is not
// what a listing is reporting.
func Handled(handlings []Handling) map[string]Handling {
	handled := make(map[string]Handling, len(handlings))
	for _, handling := range handlings {
		existing, seen := handled[handling.ReportID]
		if seen && existing.RecordedAt.After(handling.RecordedAt) {
			continue
		}
		handled[handling.ReportID] = handling
	}
	return handled
}

// Unhandled is the reports nobody has said what became of, in the order the pile
// holds them. It is the working set: what a role that triages the pile is
// actually being asked to look at, as opposed to everything that has ever been
// reported.
func Unhandled(reports []Report, handlings []Handling) []Report {
	handled := Handled(handlings)
	var open []Report
	for _, reported := range reports {
		if _, done := handled[reported.ID]; done {
			continue
		}
		open = append(open, reported)
	}
	return open
}

// BySeverity orders reports worst first, and the most recent first within one
// severity. It copies rather than sorting in place, because the pile's own order
// is the order it was reported in and nothing that reads it may disturb that.
//
// Recency inside a severity is what makes a bounded listing cut the right end: a
// critical report from this morning outranks a note from last week whichever way
// the bound falls, and among criticals the one nobody has got to yet is the
// newest.
func BySeverity(reports []Report) []Report {
	ordered := make([]Report, len(reports))
	copy(ordered, reports)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Severity != ordered[j].Severity {
			return ordered[i].Severity.rank() < ordered[j].Severity.rank()
		}
		return ordered[i].RecordedAt.After(ordered[j].RecordedAt)
	})
	return ordered
}

// Worst is the severity of the most attention-seeking report in a set, and the
// empty severity where there are none.
//
// It is what a summary that names a pile without listing it is marked and
// dressed by. "This run reported three things" says nothing about whether one of
// them is already costing somebody, and a closing line that read the same way
// for three notes and for a critical is exactly how a critical report goes
// unread.
func Worst(reports []Report) Severity {
	var worst Severity
	for _, reported := range reports {
		if worst == "" || reported.Severity.rank() < worst.rank() {
			worst = reported.Severity
		}
	}
	return worst
}

// Tally says how many reports of each severity a set holds, worst first, for
// that same summary. A severity nothing was filed at is left out rather than
// counted at zero: what the line is for is saying which of these there are, and
// three zeroes to read past is the noise this is trying to cut.
func Tally(reports []Report) string {
	counts := make(map[Severity]int, 3)
	for _, reported := range reports {
		counts[reported.Severity]++
	}
	var parts []string
	for _, severity := range []Severity{SeverityCritical, SeverityWarning, SeverityNote} {
		if counts[severity] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", severity, counts[severity]))
		}
	}
	return strings.Join(parts, ", ")
}

// Render describes what became of one report, for a listing that has just shown
// the report itself. It is indented under it and folded to one line: the reason
// came from whoever handled the report, and a listing is a listing.
func (h Handling) Render() string {
	handler := string(h.Role)
	if h.Agent != "" && h.Agent != string(h.Role) {
		handler = h.Agent + " (" + string(h.Role) + ")"
	}
	return fmt.Sprintf("      handled %s by the %s (%s): %s\n",
		h.RecordedAt.UTC().Format(time.RFC3339), handler, h.RunID, strings.Join(strings.Fields(h.Reason), " "))
}

// Render describes one collected report for whoever is reading the pile. The
// message came from a provider, so it is indented under the harness's own line
// and never printed at the margin.
//
// The marker leads the line, before the identifier, because the two are read
// differently: the identifier is what a reader who has stopped at a report needs
// in order to act on it, and the marker is what stops them at that one rather
// than at the note above it. It is padded to a fixed width so the identifiers
// still line up in a column, which is what makes the rest of a listing readable
// once the criticals are marked out of it.
//
// The identifier is otherwise still what a report is named by. Saying what
// became of one means naming it, and a listing that showed everything about a
// report except the word for it would leave the reader unable to act on what
// they had just read.
func (r Report) Render() string {
	var rendered strings.Builder
	// The agent is named only where it says something the role does not, which
	// is a project that configured more than one agent for the role.
	reporter := string(r.Role)
	if r.Agent != "" && r.Agent != string(r.Role) {
		reporter = r.Agent + " (" + string(r.Role) + ")"
	}
	fmt.Fprintf(&rendered, "  %-*s %s [%s] %s from the %s",
		MarkerWidth, r.Severity.Marker(), r.ID, r.Severity, r.RecordedAt.UTC().Format(time.RFC3339), reporter)
	if r.WorkItemID != "" {
		fmt.Fprintf(&rendered, " on %s", r.WorkItemID)
	}
	fmt.Fprintf(&rendered, " (%s)\n", r.RunID)
	for _, line := range strings.Split(strings.TrimSpace(r.Message), "\n") {
		fmt.Fprintf(&rendered, "      %s\n", strings.TrimSpace(line))
	}
	return rendered.String()
}

// Contract is the reporting section every role's immutable contract carries. It
// is one text in one place because every role reports the same way: a report
// from a developer and one from the product manager are the same record in the
// same pile, and three separately worded instructions would become three
// mechanisms that drift.
const Contract = `# Reporting what you noticed

Something you notice while your own work succeeds reaches the operator only if you report it. A report is not a blocker and does not behave like one: your work carries on exactly as it would have, nothing waits on the report, and it never changes the outcome of what you are doing. Something that stops you doing the work you were given is not a report at all — that is a failure, and you report it the way your role already reports one.

Report what somebody would want to be told and could act on: a risk you worked around, an assumption you had to make that may not hold, a defect or a stale document outside the work you were given, or something about your environment that stopped you verifying what you wanted to verify. Do not report what you did, which is what your own summary is for, and do not report routine observations. A channel full of things nobody needed to know is one nobody reads, and that is worse than nothing because it looks like coverage, so report nothing rather than something you would not want to interrupt a person for. Most replies carry no report at all.

To report, end what you say with exactly one block of this shape:

` + "```" + `yoyodyne-report
{"reports":[{"severity":"critical|warning|note","message":"what you noticed, what it affects, and what somebody would have to look at"}]}
` + "```" + `

"critical" is something already wrong that will cost somebody if nobody looks at it. "warning" is a real risk or a fragile assumption that has not cost anything yet. "note" is worth knowing and asks for nothing. One block carries at most ` + maxEntriesPerReplyText + ` reports, and each one takes a severity and a message and nothing else. Leave the block out entirely when you have nothing to report.`
