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

// Decode strictly decodes a validated verdict from a bounded JSON document.
// Unknown fields, trailing content, and oversized input are rejected rather
// than silently tolerated.
func Decode(data []byte) (Verdict, error) {
	if len(data) == 0 {
		return Verdict{}, errors.New("decode review verdict: input is empty")
	}
	if len(data) > MaxVerdictBytes {
		return Verdict{}, fmt.Errorf("decode review verdict: input is %d bytes, limit is %d", len(data), MaxVerdictBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var verdict Verdict
	if err := decoder.Decode(&verdict); err != nil {
		return Verdict{}, fmt.Errorf("decode review verdict: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Verdict{}, errors.New("decode review verdict: unexpected trailing content after the verdict")
	}
	if err := verdict.Validate(); err != nil {
		return Verdict{}, err
	}
	return verdict, nil
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

func (d Decision) Valid() bool {
	return d == DecisionApprove || d == DecisionRepair
}

func (s Severity) Valid() bool {
	switch s {
	case SeverityBlocker, SeverityMajor, SeverityMinor:
		return true
	default:
		return false
	}
}
