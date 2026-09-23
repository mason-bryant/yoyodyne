// Package selfcheck carries a developer's record of having executed anything at
// all: the probe it ran before it changed a line, and the checks it ran against
// the change it is handing over.
//
// A reviewer judges evidence, and a developer that never executed anything
// submits claims. Both halves of that were paid for. On 2026-08-23 every shell
// a developer run could start died on the operating system's argument limit,
// and the run discovered it at first use, after its context was spent —
// `docs/diagnoses/yoyodyne-ifd-180-guard-absent-from-tree.md` is the reading of
// it. Before that, yoyodyne-ifd.149 wrote a guard, reported it delivered, and
// closed its item, with its shell dead the whole time and nothing it wrote ever
// having been run; the work was rediscovered missing weeks later.
//
// So the record is asked for rather than assumed, and the harness gates on it
// exactly as it gates on the paths a change touched:
//
//   - The probe is universal. It is one execution of the declared checks' entry
//     point before any edit, it costs almost nothing, and an environment that
//     cannot run it is one the run stops on at once rather than an hour later.
//     A probe recorded as failed ends the run naming what refused, because
//     nothing a developer can do to the change fixes a sandbox that cannot spawn
//     a process.
//   - The check evidence is scoped to what the declared checks would judge.
//     Demanding a full suite run for a change the suite never reads prices
//     honesty out and teaches padding, so a change touching only content no
//     declared check exercises submits on the probe alone. Which content that is
//     is a mechanical question the caller answers — `internal/composition` holds
//     this repository's answer — rather than a judgement the developer makes
//     about its own work.
//
// What this package does not do is believe the record on its own. The harness
// runs the declared checks itself before anything is reviewed or integrated, and
// that is what proves the change passes; this is what proves somebody executed
// it before handing it over, which is a different fact and the one that was
// missing.
package selfcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the one block a reply may record its executions in. It is a
// distinct language tag for the reason the landing, report, and amendment
// fences are distinct from each other: these are separate channels that mean
// different things, and a block in one of them must never be readable as
// another.
const Fence = "```yoyodyne-verification"

// The bounds an untrusted record is held to. A command is a command line, and a
// detail is the sentence naming what went wrong — the output itself belongs in
// the check log the run redirects into its own scratch directory, not in a field
// the harness stores and puts in front of a reviewer.
const (
	MaxBlockBytes   = 8 << 10
	MaxCommandBytes = 512
	MaxDetailBytes  = 2 << 10
	// MaxChecks bounds how many executions one record may list. A developer that
	// ran twenty commands against its change ran enough of them; a record longer
	// than that is padding, which is the failure mode this whole gate has to
	// avoid teaching.
	MaxChecks = 20
)

// Outcome is how one execution ended. The vocabulary is three words, and the
// distinction that earns the third one is whether the command ran at all: a
// suite that ran and failed is a change or a base commit to fix, and a command
// that could not be started is an environment nobody can work in. The harness
// answers those two in opposite ways — one is repaired and one ends the run —
// so a record that could not tell them apart would send every red baseline to
// the operator as a broken sandbox.
type Outcome string

const (
	// OutcomePassed is the command having run and exited successfully.
	OutcomePassed Outcome = "passed"
	// OutcomeFailed is the command having run and exited non-zero. It says the
	// environment works and something else does not — the change, or the commit
	// the run was cut from — which is what the checks the harness then runs
	// itself are for.
	OutcomeFailed Outcome = "failed"
	// OutcomeRefused is the command never having started: a shell that could not
	// be spawned, a binary that is not there, a sandbox that would not let it
	// run. Nothing a developer does to its change answers this one, so it is the
	// outcome a run ends on rather than repairs.
	OutcomeRefused Outcome = "refused"
)

var outcomes = []Outcome{OutcomePassed, OutcomeFailed, OutcomeRefused}

// Outcomes is the closed vocabulary a record may carry, as a caller outside this
// package reads it. It answers with a copy, because a package-level slice is a
// vocabulary anybody holding it could rewrite.
func Outcomes() []Outcome { return slices.Clone(outcomes) }

func (o Outcome) Valid() bool { return slices.Contains(outcomes, o) }

// Execution is one command the developer says it ran, and how it ended.
type Execution struct {
	// Command is what was run, as it was typed.
	Command string `json:"command"`
	// Outcome is how it ended.
	Outcome Outcome `json:"outcome"`
	// Detail is what went wrong, required on anything but a pass and empty
	// otherwise. What belongs in it is the message the command itself printed,
	// because a tool that refuses often says how to stop it refusing — a Go build
	// cache the sandbox will not let it write names the redirect that fixes it —
	// and a detail somebody paraphrased is the one that loses the fix.
	Detail string `json:"detail,omitempty"`
}

// Passed reports an execution that ran and succeeded.
func (e Execution) Passed() bool { return e.Outcome == OutcomePassed }

// Started reports an execution that actually ran, whichever way it then went. It
// is the question the probe exists to answer, and it is deliberately not the
// same as having passed: a suite that runs and fails has proved the environment
// works.
func (e Execution) Started() bool { return e.Outcome != OutcomeRefused }

// Validate reports every contract violation in one execution at once. The
// position is named by the caller, which knows whether this is the probe or one
// of the checks.
func (e Execution) Validate() error {
	var problems []error
	trimmed := strings.TrimSpace(e.Command)
	switch {
	case trimmed == "":
		problems = append(problems, errors.New("command is required"))
	case len(trimmed) > MaxCommandBytes:
		problems = append(problems, fmt.Errorf("command is %d bytes, limit is %d", len(trimmed), MaxCommandBytes))
	}
	if !e.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q must be %s", e.Outcome, quotedOutcomes()))
	}
	detail := strings.TrimSpace(e.Detail)
	switch {
	case e.Outcome.Valid() && e.Outcome != OutcomePassed && detail == "":
		problems = append(problems, errors.New("detail is required on anything but a pass, carrying what the command itself said"))
	case len(detail) > MaxDetailBytes:
		problems = append(problems, fmt.Errorf("detail is %d bytes, limit is %d", len(detail), MaxDetailBytes))
	}
	return errors.Join(problems...)
}

// Record is one developer's account of what it executed, exactly as it wrote it.
// Everything else about it — which run made it, which change it was made
// against — is what the harness knows and the agent does not get to assert.
type Record struct {
	// Probe is the execution made before anything was changed. It is required of
	// every record, whatever the change turned out to touch.
	Probe Execution `json:"probe"`
	// Checks are the executions made against the change itself. They are what a
	// change the declared checks would read has to carry, and what a change
	// nothing reads may leave out.
	Checks []Execution `json:"checks,omitempty"`
}

// Recorded reports a record a developer actually made, as opposed to the zero
// record a reply carrying no block leaves.
func (r Record) Recorded() bool { return strings.TrimSpace(r.Probe.Command) != "" }

// Probed reports an environment that proved it can execute. A probe that ran and
// failed proves exactly that, so this asks whether the command started rather
// than whether it passed — the failing suite is somebody's to fix, and it is not
// the environment.
func (r Record) Probed() bool { return r.Recorded() && r.Probe.Started() }

// ProbeRefused reports an environment that could not start the probe at all. It
// is the one outcome no change can answer, and the caller ends the run on it.
func (r Record) ProbeRefused() bool { return r.Recorded() && !r.Probe.Started() }

// Evidenced reports at least one execution against the change itself that ran
// and passed. A record whose every check failed is not evidence that the change
// was exercised successfully, and it is the case the harness hands back rather
// than the one it stops on: a check that fails is a change to repair.
func (r Record) Evidenced() bool {
	for _, check := range r.Checks {
		if check.Passed() {
			return true
		}
	}
	return false
}

// Failures are the executions that did not pass, the probe included, in the
// order the record carries them. A refusal is one of them: it did not pass
// either, and what tells the two apart is each execution's own outcome.
func (r Record) Failures() []Execution {
	var failed []Execution
	if r.Recorded() && !r.Probe.Passed() {
		failed = append(failed, r.Probe)
	}
	for _, check := range r.Checks {
		if !check.Passed() {
			failed = append(failed, check)
		}
	}
	return failed
}

// Validate reports every contract violation in the record at once.
func (r Record) Validate() error {
	var problems []error
	if err := r.Probe.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("probe: %w", err))
	}
	if len(r.Checks) > MaxChecks {
		problems = append(problems, fmt.Errorf("%d checks are recorded, limit is %d", len(r.Checks), MaxChecks))
	}
	for index, check := range r.Checks {
		if err := check.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("checks[%d]: %w", index, err))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid verification record: %w", err)
	}
	return nil
}

// Missing says what a record still owes, given whether the change it was made
// for is one the declared checks would read. It answers with every problem at
// once and in the order they have to be fixed, because a developer told one
// thing at a time spends a round per sentence.
//
// An empty answer is a record that satisfies the bar. It is deliberately not the
// same thing as a change that passes: what passes is decided by the harness
// running the checks itself, afterwards.
func (r Record) Missing(evidenceRequired bool) []string {
	var owed []string
	if !r.Recorded() {
		owed = append(owed, "no verification block: the reply has to record the probe you ran before changing anything")
		if evidenceRequired {
			owed = append(owed, "no record of running a check against your change, which this change needs because the declared checks read the files it touches")
		}
		return owed
	}
	if evidenceRequired && len(r.Checks) == 0 {
		owed = append(owed, "no record of running a check against your change, which this change needs because the declared checks read the files it touches")
	}
	if evidenceRequired && len(r.Checks) > 0 && !r.Evidenced() {
		owed = append(owed, "every check you recorded against this change failed, so nothing here shows the change working")
	}
	return owed
}

func quotedOutcomes() string {
	quoted := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		quoted = append(quoted, `"`+string(outcome)+`"`)
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// Extract splits a reply into what the agent said and the executions it
// recorded. The record comes only from the fenced block: prose saying the tests
// passed is not a record, and it is exactly the prose a false closure is built
// out of.
//
// What the agent said comes back without the block whichever way that goes, for
// the reason a landing's does: the reply is the run's account of itself and this
// channel must not cost it. A block that could not be read is not nothing — the
// caller is told, and what it does about an unreadable record is its decision
// rather than this package's.
func Extract(reply string) (string, Record, error) {
	block, err := fenced.Split(reply, Fence, "verification")
	if err != nil {
		return block.Before, Record{}, err
	}
	if !block.Found {
		return block.Before, Record{}, nil
	}
	record, err := Decode(block.Payload)
	if err != nil {
		return block.Before, Record{}, err
	}
	return block.Rest, record, nil
}

// Decode strictly decodes the block payload. Unknown fields, trailing content,
// and oversized input are refused rather than tolerated, for the reason a
// landing claim refuses them: what a gate decides on has to be exactly what the
// agent wrote.
//
// It is a validator of what an agent just said rather than a reader of a durable
// record, so it stays strict where the run-record listings became tolerant: the
// block was written seconds ago by a developer this build told what to write,
// and a key this build does not know is a developer that recorded something
// other than what it was asked for. The refusal reaches the caller as an error
// naming the block, and the submission is held for the evidence rather than
// admitted on a record that was read past.
func Decode(payload string) (Record, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Record{}, errors.New("decode verification record: the verification block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return Record{}, fmt.Errorf("decode verification record: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("decode verification record: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Record{}, errors.New("decode verification record: unexpected trailing content after the record")
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	record.Probe = trimmedExecution(record.Probe)
	for index, check := range record.Checks {
		record.Checks[index] = trimmedExecution(check)
	}
	return record, nil
}

func trimmedExecution(execution Execution) Execution {
	execution.Command = strings.TrimSpace(execution.Command)
	execution.Detail = strings.TrimSpace(execution.Detail)
	return execution
}

// Describe says what a developer recorded executing, as one passage a reviewer
// and a work item's notes can both be shown. It states what is true of the
// record and directs no reader to do anything about it, which is what lets one
// wording serve both.
//
// The record nobody made is described too. A reviewer shown only the records
// somebody wrote would be shown nothing on exactly the runs this gate exists
// for, and "the developer recorded nothing" is the single most useful sentence
// in that case.
func (r Record) Describe() string {
	if !r.Recorded() {
		return "The developer recorded no execution of its own: neither a probe of the environment nor a check run against the change."
	}
	var described strings.Builder
	described.WriteString("Before changing anything, the developer ran " + describeExecution(r.Probe) + ".")
	switch {
	case len(r.Checks) == 0:
		described.WriteString("\nIt recorded no check run against the change itself.")
	default:
		described.WriteString("\nAgainst the change itself it ran:")
		for _, check := range r.Checks {
			described.WriteString("\n- " + describeExecution(check) + ".")
		}
	}
	return described.String()
}

func describeExecution(execution Execution) string {
	described := "`" + execution.Command + "`, which " + describeOutcome(execution.Outcome)
	if detail := strings.TrimSpace(execution.Detail); detail != "" {
		described += " (" + folded(detail) + ")"
	}
	return described
}

// describeOutcome says what an outcome means rather than repeating the word a
// reader would have to look up. The refusal is the one worth spelling out: "the
// command would not start" and "the command failed" are opposite facts about the
// environment, and a description that said "refused" for both would be the same
// conflation the vocabulary exists to undo.
func describeOutcome(outcome Outcome) string {
	switch outcome {
	case OutcomePassed:
		return "passed"
	case OutcomeFailed:
		return "ran and failed"
	case OutcomeRefused:
		return "would not start at all"
	default:
		return string(outcome)
	}
}

// folded is a developer's sentence as one line, which is what a prompt section
// and a work item's notes are both read as.
func folded(detail string) string { return strings.Join(strings.Fields(detail), " ") }

// Contract is the section a developer's immutable contract carries, with this
// project's own declared checks named in it. It is here beside the vocabulary it
// describes so the words an agent is given and the words the harness accepts
// cannot drift apart, and it is built from the configured checks rather than
// written out because a contract naming checks a project does not declare is a
// contract asking for something nobody can run.
func Contract(checks []string) string {
	var contract strings.Builder
	contract.WriteString(`# Proving you can execute, before and after

Your first action in this worktree, before you read far and before you change a line, is to execute something and see it work. `)
	switch {
	case len(checks) == 0:
		contract.WriteString("This project declares no checks, so run whatever this repository's own build or test entry point is.")
	default:
		contract.WriteString("This project's declared checks are:\n")
		for _, check := range checks {
			contract.WriteString("\n- `" + check + "`")
		}
		contract.WriteString("\n\nRun one of them, or the build step underneath them if the suite is slow — the point is that a command in this worktree ran and exited.")
	}
	contract.WriteString(`

That probe is asked of every run, whatever the work turns out to be, because it is nearly free and because the thing it catches is invisible from the inside: an environment where nothing can be spawned at all looks exactly like an environment nobody has asked yet. A run that discovers it at first use discovers it having already spent its context, and one that never tries can write code, report it working, and close a work item on it — which is how a guard this repository depends on came to be reported delivered, never having been run, and rediscovered missing weeks later.

What the probe answers is whether commands run here, and not whether they pass. A probe that ran and came back red has answered it: the environment works, something else is broken — your worktree's base commit, most likely — and you carry on and say so in your summary. A probe that could not start at all is the other answer, and it is the one nothing you do to the change can fix. Stop there, reply immediately with the block below recording it as refused, and do not work around it or carry on and hope. The run ends on that record and the environment is reported, which is the cheapest ending available and the only one that tells anybody what is wrong.

Whichever way it goes, put the command's own message in the "detail" rather than your paraphrase of it. A tool that refuses often says how to stop it refusing — a build cache the sandbox will not let it write names the redirect that fixes it, right there in the failure — and that sentence is the whole value of the record to whoever reads it next.

When you hand the change over, the block also records what you ran against the change itself. That half is required when the declared checks read the kind of files you touched, and not otherwise: a change to content nothing here checks submits on the probe alone. The harness decides which of the two your change is, mechanically, from the same coverage the checks are held to — so it is never a judgement you have to make, and never one you can get wrong.

Put exactly one block of this shape in your reply:

` + "```" + `yoyodyne-verification
{"probe":{"command":"make build","outcome":"passed"},"checks":[{"command":"make test","outcome":"passed"}]}
` + "```" + `

Each entry is the command as you typed it and how it ended. "passed" is it ran and exited successfully; "failed" is it ran and exited non-zero; "refused" is it never started — no shell, no binary, a sandbox that would not run it. Anything but a pass takes a "detail", and the message the command printed is what belongs there; keep the output itself in your scratch directory and name the file there rather than pasting it. Record what you actually ran, including a focused command rather than the whole suite — that is what the harness asks for, and the declared checks are run by the harness itself afterwards either way, so nothing is bought by claiming more than you did.

A change handed over without this block is handed back to you for it, and a run that spends its attempts that way stops without reaching a reviewer.`)
	return contract.String()
}
