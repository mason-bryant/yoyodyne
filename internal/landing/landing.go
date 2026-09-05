// Package landing carries a developer's claim about what its change does to the
// work item, as distinct from what it does to the repository.
//
// The two are not the same fact and the harness cannot derive one from the
// other. A change that integrates has landed something; whether what it landed
// is the work the item asked for is a judgement about content, and closure used
// to follow integration alone. That produced the defect this exists for: a run
// answered an item it could not do yet by landing a diagnosis that said in bold
// that the work had not been done and the item stayed open, the diagnosis
// integrated, and the item closed against it — retiring a marker the operator's
// accepted trade depended on, on evidence that said the opposite.
//
// So the developer says which kind of landing this is, and closure follows the
// claim rather than the integration. An honest "not doable yet" is valuable
// evidence and lands exactly as any other change does; what it does not do is
// discharge the item. A reply that claims nothing claims the ordinary landing,
// because nearly every run is one and a protocol that made every developer
// declare the obvious would be a protocol nobody read.
package landing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the one block a reply may claim a landing in. It is a distinct
// language tag rather than plain JSON so a claim can never be confused with JSON
// an agent happens to be discussing, and a distinct one from the report and
// amendment fences because it asks for something neither of those does: a report
// decides nothing, a proposal waits on somebody, and this decides whether the
// item closes.
const Fence = "```yoyodyne-landing"

// MaxBlockBytes bounds the untrusted payload the block is decoded from, and
// MaxWhyBytes bounds the developer's account of the claim. The account is a
// sentence or a paragraph naming what was landed instead; the whole diagnosis is
// the change itself and the summary, and neither belongs in a field the tracker
// records verbatim.
const (
	MaxBlockBytes = 8 << 10
	MaxWhyBytes   = 4 << 10
	// MaxBlockedByBytes bounds the impediment a leave-open claim names. It is one
	// work item identifier and nothing else, so the bound is generous for an
	// identifier and far too small for prose written where an identifier goes.
	MaxBlockedByBytes = 128
)

// blockedByPattern is the shape of the work item identifier a leave-open claim
// names. It mirrors the tracker's own, deliberately rather than by import: this
// package is the door an untrusted reply comes through, and a marker that is not
// an identifier has to be refused where the claim is read rather than carried as
// far as the write that would refuse it.
var blockedByPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Outcome is what a landing does to the work item it was made for. The
// vocabulary is deliberately not the run's — a run that lands evidence
// succeeded, and calling this outcome anything like "failed" would describe the
// run rather than the item.
type Outcome string

const (
	// OutcomeDischarged is the ordinary landing: the change is the work the item
	// asked for, and the item closes on it. It is what a reply claiming nothing
	// claims.
	OutcomeDischarged Outcome = "discharged"
	// OutcomeEvidence is a landing worth keeping that does not discharge the
	// item: a diagnosis, the conditions that have to hold first, the reason the
	// work is not doable yet. The change integrates and the item stays open.
	OutcomeEvidence Outcome = "evidence"
)

var outcomes = []Outcome{OutcomeDischarged, OutcomeEvidence}

// Outcomes is the closed vocabulary a claim may carry, as a caller outside this
// package reads it. It answers with a copy, because a package-level slice is a
// vocabulary anybody holding it could rewrite.
func Outcomes() []Outcome { return slices.Clone(outcomes) }

func (o Outcome) Valid() bool { return slices.Contains(outcomes, o) }

// Claim is one landing claim exactly as a developer wrote it. Everything else
// about it — which run made it, which item it was made on — is what the harness
// knows and the agent does not get to assert.
type Claim struct {
	// Outcome is which kind of landing this is.
	Outcome Outcome `json:"outcome"`
	// Why is the developer's own account of the claim. It is required, because a
	// claim that the item is not discharged and says nothing about what is left
	// leaves whoever reads the item afterwards exactly where the false closure
	// left them.
	Why string `json:"why"`
	// BlockedBy is the impediment, named as the work item this one now waits for.
	// It is the one way to ask for the item to be left open rather than parked,
	// and it is optional because the default is the parking: an undischarged item
	// returned to the backlog bare is one autonomous selection picks again
	// immediately, for another run and another diagnosis of the same impediment.
	//
	// There is deliberately no way to ask for bare openness. The marker is what
	// selection actually honours — a blocking dependency holds the item back until
	// the impediment closes, and then releases it without anybody remembering to —
	// so a claim that leaves the item open either names what it is waiting for or
	// gets the parking.
	BlockedBy string `json:"blocked_by,omitempty"`
}

// Made reports a claim a developer actually made, as opposed to the zero claim a
// reply carrying no block leaves.
func (c Claim) Made() bool { return c.Outcome != "" }

// Discharges reports whether this claim closes the item it was made on. The zero
// claim discharges: a reply with no block is the ordinary run, and every run made
// before this channel existed is one of those.
//
// It is stated as "everything except evidence discharges" rather than as
// "discharged discharges" so that the default can never be the closing one by
// accident — a value this vocabulary does not recognize is refused before it is
// ever stored, and this is the derivation every closure site reads.
func (c Claim) Discharges() bool { return c.Outcome != OutcomeEvidence }

// Impediment is the work item a leave-open claim named, and is empty for every
// other claim. A claim that does not discharge its item and names none is the
// default, which parks.
func (c Claim) Impediment() string { return strings.TrimSpace(c.BlockedBy) }

// Parks reports a claim whose item goes back to the backlog parked. It is the
// default for a landing that does not discharge, and it is stated as the absence
// of the marker rather than as a third outcome so that a claim can never ask for
// an undischarged item to sit in the queue with nothing holding it back.
func (c Claim) Parks() bool { return !c.Discharges() && c.Impediment() == "" }

// Validate reports every contract violation in the claim at once.
func (c Claim) Validate() error {
	var problems []error
	if !c.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q must be %s", c.Outcome, quotedOutcomes()))
	}
	trimmed := strings.TrimSpace(c.Why)
	switch {
	case trimmed == "":
		problems = append(problems, errors.New("why is required"))
	case len(trimmed) > MaxWhyBytes:
		problems = append(problems, fmt.Errorf("why is %d bytes, limit is %d", len(trimmed), MaxWhyBytes))
	}
	// The marker is refused on a discharging claim rather than ignored there. A
	// developer that wrote both said two things that cannot both be true — the
	// item closes, and it waits for something — and guessing which it meant is how
	// a closed item comes to carry a dependency nobody can account for.
	if impediment := c.Impediment(); impediment != "" {
		switch {
		case c.Outcome.Valid() && c.Discharges():
			problems = append(problems, errors.New(`blocked_by is only for a landing that does not discharge the item`))
		case len(impediment) > MaxBlockedByBytes:
			problems = append(problems, fmt.Errorf("blocked_by is %d bytes, limit is %d", len(impediment), MaxBlockedByBytes))
		case !blockedByPattern.MatchString(impediment):
			problems = append(problems, fmt.Errorf("blocked_by %q must be a work item identifier", impediment))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid landing claim: %w", err)
	}
	return nil
}

// Describe says which landing was claimed and why, in the words a reviewer and a
// work item's notes are given it in. A claim nobody made describes nothing:
// there is no claim to attribute, and inventing one would put words on the item
// the developer never wrote.
func (c Claim) Describe() string {
	if !c.Made() {
		return ""
	}
	headline := "The developer claims this change discharges the work item."
	if !c.Discharges() {
		headline = "The developer claims this change lands evidence and does not discharge the work item."
		// Which of the two undischarged landings it is, because they leave the item
		// in different places and the reviewer is the only reader that sees the
		// claim beside the change that was offered under it.
		if c.Parks() {
			headline += " The item is parked with that account as its parking reason."
		} else {
			headline += " The item is left open waiting on " + c.Impediment() + "."
		}
	}
	return headline + "\nWhy: " + strings.Join(strings.Fields(c.Why), " ")
}

func quotedOutcomes() string {
	quoted := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		quoted = append(quoted, `"`+string(outcome)+`"`)
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// Extract splits a reply into what the agent said and the landing it claimed. A
// claim comes only from the fenced block: prose saying the work is not done is
// not a claim, and it is precisely the prose an integration already read past
// once.
//
// What the agent said comes back without the block whichever way that goes, for
// the reason a report's does: the reply is the run's evidence and this channel
// must not cost it. Unlike a report, a block that could not be read is not
// nothing — the caller is told, and what it does about an unreadable claim is
// its decision rather than this package's.
func Extract(reply string) (string, Claim, error) {
	block, err := fenced.Split(reply, Fence, "landing")
	if err != nil {
		return block.Before, Claim{}, err
	}
	if !block.Found {
		return block.Before, Claim{}, nil
	}
	claim, err := Decode(block.Payload)
	if err != nil {
		return block.Before, Claim{}, err
	}
	return block.Rest, claim, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated: what decides whether an
// item closes has to be exactly what the agent wrote.
func Decode(payload string) (Claim, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Claim{}, errors.New("decode landing claim: the landing block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return Claim{}, fmt.Errorf("decode landing claim: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var claim Claim
	if err := decoder.Decode(&claim); err != nil {
		return Claim{}, fmt.Errorf("decode landing claim: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Claim{}, errors.New("decode landing claim: unexpected trailing content after the claim")
	}
	if err := claim.Validate(); err != nil {
		return Claim{}, err
	}
	claim.Why = strings.TrimSpace(claim.Why)
	claim.BlockedBy = claim.Impediment()
	return claim, nil
}

// Contract is the landing section a developer's immutable contract carries. It
// is here beside the vocabulary it describes so the words an agent is given and
// the words the harness accepts cannot drift apart.
const Contract = `# What your landing does to the work item

Two different things can be true when your change lands. Usually the work item is discharged by it: what you changed is the work the item asked for, and the item closes. Sometimes it is not — the work turns out not to be doable yet, and what there is to land is the evidence for that: a diagnosis, the conditions that have to hold first, the reason the item is still open. Both are worth landing, and only the first closes the item.

Your change cannot say which of the two it is, so you say. A reply carrying no block below claims the first, which is nearly every run.

To claim the second, put exactly one block of this shape in your reply, ahead of any other block it carries:

` + "```" + `yoyodyne-landing
{"outcome":"evidence","why":"what you landed instead, and what has to happen before the item can be discharged"}
` + "```" + `

"discharged" is the ordinary landing. "evidence" is a landing worth keeping that does not discharge the item: your change integrates exactly as it would have, nothing is written down as done that was not, and the item is parked with your account of the claim as its parking reason. Parking keeps the item in the product manager's order and says why it is not to be started, so nothing selects it again until somebody releases it. Write the "why" accordingly: name what would release the item, because that sentence is what whoever considers releasing it reads.

Where you can name the impediment as another work item, say so and the item is left open waiting on that item instead of parked:

` + "```" + `yoyodyne-landing
{"outcome":"evidence","why":"what you landed instead, and what has to happen before the item can be discharged","blocked_by":"the-work-item-that-has-to-land-first"}
` + "```" + `

That is the only alternative to the parking, and it is deliberate: an item put back with nothing marking it is one the harness picks again straight away, for another run and another diagnosis of the same impediment. Name an item that exists — you cannot open one, and a marker naming nothing holds the item back forever. If there is no such item, leave the marker out, take the parking, and name the work that has to be admitted in your summary.

The reviewer is shown which landing you claimed, so a diagnosis is judged as a diagnosis rather than as a missing implementation.

Claim "evidence" for work you found is not doable yet and landed the evidence for, not for work you simply did not finish. Something that stopped you is a failure, and you report it the way your role already reports one.`
