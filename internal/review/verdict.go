// Package review holds the provider-neutral structured reviewer verdict.
// Review semantics live here rather than in a backend adapter so every
// provider is decoded and validated against the same contract.
package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// MaxVerdictBytes bounds the untrusted verdict payload a reviewer may return.
const MaxVerdictBytes = 64 << 10

type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionRepair  Decision = "repair"
)

type Severity string

const (
	SeverityBlocker Severity = "blocker"
	SeverityMajor   Severity = "major"
	SeverityMinor   Severity = "minor"
)

// The two vocabularies above as lists, which is what validates a verdict below:
// a value permitted here and a value the contract accepts are one list rather
// than two that can drift.
//
// Anything added to either has to be added to the durable schema that stores it,
// which keeps its own copy so a record is checked against what a record may hold
// rather than against this version of the harness. That is a real trap — the
// addition is accepted here and refused at the moment a run tries to store what
// it decided — so the two are held together by
// TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce rather than by
// whoever adds the value remembering.
var (
	decisions  = []Decision{DecisionApprove, DecisionRepair}
	severities = []Severity{SeverityBlocker, SeverityMajor, SeverityMinor}
)

// Decisions and Severities are those vocabularies as a caller outside this
// package reads them, each answered with a copy so nothing holding one can
// rewrite the contract.
func Decisions() []Decision { return slices.Clone(decisions) }

func Severities() []Severity { return slices.Clone(severities) }

// Location optionally anchors a finding to a place in the reviewed change.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// Finding is one actionable observation the developer can act on.
type Finding struct {
	Severity Severity  `json:"severity"`
	Message  string    `json:"message"`
	Location *Location `json:"location,omitempty"`
}

// Verdict is the reviewer's approve-or-repair decision on one change.
type Verdict struct {
	Decision Decision  `json:"decision"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
}

// UndecodableVerdictError reports a reply that could not be read as a verdict at
// all: empty, past the size bound, not a single JSON document, or malformed. It
// is deliberately distinct from a verdict that was read and then refused,
// because a reply nothing can parse says nothing whatever about the change --
// that review was never made, and asking for it again costs one review where
// giving up costs the whole change. A verdict that was read and refused has
// already said what it thinks.
type UndecodableVerdictError struct {
	cause error
}

func (e UndecodableVerdictError) Error() string {
	return "decode review verdict: " + e.cause.Error()
}

func (e UndecodableVerdictError) Unwrap() error {
	return e.cause
}

func undecodable(cause error) error {
	return UndecodableVerdictError{cause: cause}
}

// Decode decodes a validated verdict from a bounded JSON document and names
// every field the closed schema does not define.
//
// Strictness is kept where it protects the contract and dropped where it only
// punishes. The document must be one JSON object within the size bound, and
// every field the schema does name keeps exactly its validation: an unknown
// decision, an unknown severity, or a missing required field is still refused.
// An unknown *extra* field is not a corrupted verdict, it is a verbose one --
// verdicts are model-generated and a model asked for structured output will
// occasionally embellish the schema -- so the extras are named for the caller to
// record as evidence of drift and the verdict is decoded without them.
func Decode(data []byte) (Verdict, []string, error) {
	if len(data) == 0 {
		return Verdict{}, nil, undecodable(errors.New("input is empty"))
	}
	if len(data) > MaxVerdictBytes {
		return Verdict{}, nil, undecodable(fmt.Errorf("input is %d bytes, limit is %d", len(data), MaxVerdictBytes))
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	var verdict Verdict
	if err := decoder.Decode(&verdict); err != nil {
		return Verdict{}, nil, undecodable(err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Verdict{}, nil, undecodable(errors.New("unexpected trailing content after the verdict"))
	}
	// The drift is reported whatever becomes of the verdict beside it: a reviewer
	// that embellished the schema did so whether or not what it decided survives
	// validation.
	unknown := unknownFields(data)
	if err := verdict.Validate(); err != nil {
		return Verdict{}, unknown, err
	}
	return verdict, unknown, nil
}

// The closed schema, named once so the decoder and the drift walk below cannot
// disagree about what the contract defines.
var (
	verdictFields  = []string{"decision", "summary", "findings"}
	findingFields  = []string{"severity", "message", "location"}
	locationFields = []string{"file", "line"}
)

// unknownFields names every field the closed schema does not define, in the
// order a reader would come across them: the verdict's own, then each finding's,
// then that finding's location's. It is deliberately tolerant of anything it
// cannot re-read, because the typed decode above has already settled whether the
// document is a verdict at all; this walk only says what else was in it.
func unknownFields(data []byte) []string {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil
	}
	unknown := unknownKeys(document, "", verdictFields)
	var findings []json.RawMessage
	if err := json.Unmarshal(document["findings"], &findings); err != nil {
		return unknown
	}
	for index, raw := range findings {
		var finding map[string]json.RawMessage
		if err := json.Unmarshal(raw, &finding); err != nil {
			continue
		}
		prefix := fmt.Sprintf("findings[%d].", index)
		unknown = append(unknown, unknownKeys(finding, prefix, findingFields)...)
		var location map[string]json.RawMessage
		if err := json.Unmarshal(finding["location"], &location); err != nil {
			continue
		}
		unknown = append(unknown, unknownKeys(location, prefix+"location.", locationFields)...)
	}
	return unknown
}

// unknownKeys names the fields of one object that the schema does not define.
// They are sorted because Go's map iteration is not ordered and the recorded
// evidence has to read the same way every time it is produced.
func unknownKeys(fields map[string]json.RawMessage, prefix string, known []string) []string {
	var unknown []string
	for name := range fields {
		if slices.Contains(known, name) {
			continue
		}
		unknown = append(unknown, prefix+name)
	}
	slices.Sort(unknown)
	return unknown
}

// Validate reports every contract violation in the verdict at once.
func (v Verdict) Validate() error {
	var problems []error
	if !v.Decision.Valid() {
		problems = append(problems, fmt.Errorf("decision %q must be %q or %q", v.Decision, DecisionApprove, DecisionRepair))
	}
	if strings.TrimSpace(v.Summary) == "" {
		problems = append(problems, errors.New("summary is required"))
	}
	if v.Decision == DecisionRepair && len(v.Findings) == 0 {
		problems = append(problems, errors.New("repair requires at least one finding"))
	}
	for i, finding := range v.Findings {
		if err := finding.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid review verdict: %w", errors.Join(problems...))
	}
	return nil
}

// Resolve returns the decision a valid verdict actually supports, rejecting one
// that contradicts its own findings. Approval cannot carry a blocker or major
// finding, because a change that still needs that work is not approved
// whatever the reviewer labelled it. Repair stays authoritative for any valid
// finding, including a purely minor one.
func (v Verdict) Resolve() (Decision, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	if v.Decision == DecisionRepair {
		return DecisionRepair, nil
	}
	var actionable []string
	for _, finding := range v.Findings {
		if finding.Severity == SeverityBlocker || finding.Severity == SeverityMajor {
			actionable = append(actionable, string(finding.Severity))
		}
	}
	if len(actionable) > 0 {
		return "", fmt.Errorf("contradictory review verdict: approve carries %d %s finding(s) that require repair", len(actionable), strings.Join(uniqueStrings(actionable), " and "))
	}
	return DecisionApprove, nil
}

// Validate reports every contract violation in the finding at once.
func (f Finding) Validate() error {
	var problems []error
	if !f.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q", f.Severity, SeverityBlocker, SeverityMajor, SeverityMinor))
	}
	if strings.TrimSpace(f.Message) == "" {
		problems = append(problems, errors.New("message is required"))
	}
	if f.Location != nil {
		if err := f.Location.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("location: %w", err))
		}
	}
	return errors.Join(problems...)
}

// Validate rejects locations that cannot point at a place in the change.
func (l Location) Validate() error {
	var problems []error
	if strings.TrimSpace(l.File) == "" {
		problems = append(problems, errors.New("file is required"))
	}
	if l.Line < 0 {
		problems = append(problems, fmt.Errorf("line %d cannot be negative", l.Line))
	}
	return errors.Join(problems...)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (d Decision) Valid() bool {
	return slices.Contains(decisions, d)
}

func (s Severity) Valid() bool {
	return slices.Contains(severities, s)
}
