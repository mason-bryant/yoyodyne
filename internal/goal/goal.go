// Package goal is the goals a repository records, read as the things work can
// be attributed to rather than as prose somebody checks by hand.
//
// Identity gave the goals document a name; it did not make the goal written on
// a work item mean anything. That goal is free text, and free text always
// resolves: an item attributed to a goal nobody wrote, to a goal whose wording
// moved on, and to a sentence that merely reads like one are indistinguishable
// from an item that serves an approved goal. It is the last link of the chain
// from the brief to the code, and it is the one that decides whether moving
// approval up from each item to the goals removes the gate or moves it.
//
// So a goal is read out of the goals artifacts themselves: each statement under
// a goals document's `Goals` heading is a goal that can be named, and resolving
// an attribution is finding the artifact that states it. The match is on the
// words, because the words are all the document offers — a goal carries no
// identity of its own inside the document stating it, so its statement is its
// identity. Case, spacing, and trailing punctuation are folded, because those
// differences are not disagreements about which goal is meant, and nothing else
// is guessed at: an attribution no document states is unresolved rather than
// approximately right.
//
// # What is done about each state, and why
//
// An attribution is checked where it is made, which is where work is admitted.
// Admitting work under a goal nothing states is refused there, because that is
// the one moment when refusing costs nothing: the item does not exist yet.
//
// Work admitted before any of this existed is grandfathered, deliberately and
// not by omission. It is reported as unattributed wherever the queue is read,
// it can acquire an attribution without anything already written being
// rewritten, and nothing refuses to run it. The alternative rules were both
// worse: a rule that failed every item admitted before attributions were
// checked would stop all work to close a gap that has cost nothing yet, and a
// backfill the harness performed itself would be the harness deciding what
// somebody else's work is for, which is the product manager's judgement and not
// a derivation.
//
// That is why an item with no attribution and an item whose attribution
// resolves to nothing are different states here rather than one "not
// traceable". The first is work that predates the rule and is somebody's to
// attribute; the second is a claim that is wrong, on an item that asserted
// something the goals do not say.
package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
)

// AttributionPrefix opens the line a work item records its goal on. It is one
// constant rather than the same string at each call site because the harness
// writes that line and reads it back: two spellings that drifted apart would
// leave every attributed item reading as one that names no goal.
const AttributionPrefix = "Goal served:"

// MaxStatementBytes bounds one goal statement. A goal is a sentence rather than
// a title, so it is generous; it is bounded at all because whatever a goals
// document states has to be nameable on a work item, and the field it is named
// in is bounded. A statement longer than this is reported rather than collected,
// because collecting a goal nothing can name would offer an attribution that is
// refused every time it is used.
const MaxStatementBytes = 400

// maxGoalsPerDocument bounds how many goals are read from one document. A goals
// document is a statement of intent somebody agreed to, not an export, and the
// bound keeps a runaway file from becoming the whole of what a caller holds.
const maxGoalsPerDocument = 200

// Goal is one goal a repository records, and the artifact that states it. The
// artifact is carried because that is what an attribution resolves to: knowing
// the words matched is not knowing which document they were agreed in.
type Goal struct {
	Statement  string `json:"statement"`
	ArtifactID string `json:"artifact_id"`
	// Path is the repository-relative file the statement was read from.
	Path string `json:"path"`
	// InForce says the artifact stating this goal is what the product currently
	// intends. A goal in a superseded or retired document is still recorded, so
	// an attribution to one can be told from an attribution to nothing at all.
	InForce bool `json:"in_force"`
}

// Problem names one goals document whose goals could not be read, and says why.
// It is reported beside the goals that did load: a document nobody can read is
// a gap in what work can be attributed to, and a gap nobody is told about looks
// exactly like a repository with fewer goals.
type Problem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return p.Path + ": " + p.Reason
}

// Set is every goal the repository records.
type Set struct {
	Goals []Goal `json:"goals,omitempty"`
	// Sources are the goals artifacts read, in the order the artifact set holds
	// them, so what is reported can say where it looked.
	Sources  []string  `json:"sources,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
	// Unavailable is why the goals are not known at all, as opposed to known to
	// be none. A caller that could not load the artifacts says so here: work
	// admitted while the goals cannot be read is not work whose goal was
	// checked, and it must not be reported as though it were.
	Unavailable string `json:"unavailable,omitempty"`
}

// State is what a work item's attribution amounts to. The four are separated
// because they are four different things to do, and telling them apart is the
// point of checking at all.
type State string

const (
	// StateAttributed names a goal an in-force goals artifact states.
	StateAttributed State = "attributed"
	// StateUnresolved names something no in-force goals artifact states. It is a
	// claim that is wrong rather than a claim nobody made.
	StateUnresolved State = "unresolved"
	// StateUnattributed names no goal at all. This is what work admitted before
	// attributions were checked looks like, and it is grandfathered rather than
	// refused.
	StateUnattributed State = "unattributed"
	// StateUncheckable is an attribution the repository has nothing to check
	// against: no goals are recorded, or they could not be read. The attribution
	// is neither confirmed nor denied, and saying so is the whole of what is
	// honest here.
	StateUncheckable State = "uncheckable"
)

// Attribution is what one work item says about the goal it serves, judged
// against what the repository records.
type Attribution struct {
	State State `json:"state"`
	// Named is what the item names, in its own words. It is empty exactly when
	// the item names nothing.
	Named string `json:"named,omitempty"`
	// Goal is what the attribution resolved to, and is set only when it did.
	Goal Goal `json:"goal"`
	// Reason says what is wrong, and is set on everything but an attribution
	// that resolved.
	Reason string `json:"reason,omitempty"`
}

// Resolved reports an attribution that names a goal the product currently
// intends. Nothing else counts: an unresolved attribution is a wrong claim and
// an uncheckable one is an unanswered question, and neither is traceability.
func (a Attribution) Resolved() bool {
	return a.State == StateAttributed
}

// Note is the line a work item records its goal on. Everything that writes an
// attribution goes through here, so what is written is always what is read
// back.
func Note(statement string) string {
	return AttributionPrefix + " " + strings.TrimSpace(statement)
}

// NamedIn returns the goal a work item's notes record, and whether they record
// one. The last attribution wins: an item acquires one by having it appended to
// notes that are never rewritten, so the newest line is the current claim and
// the ones before it are the record of how it got there.
func NamedIn(notes string) (string, bool) {
	var named string
	found := false
	for _, line := range strings.Split(notes, "\n") {
		rest, isAttribution := strings.CutPrefix(strings.TrimSpace(line), AttributionPrefix)
		if !isAttribution {
			continue
		}
		if statement := strings.TrimSpace(rest); statement != "" {
			named, found = statement, true
		}
	}
	return named, found
}

// Collect reads the goals out of the goals artifacts a repository records. A
// document that states none is reported rather than dropped: a goals artifact
// with nothing under its `Goals` heading is a document somebody has yet to
// write, and reading it as a repository with fewer goals would attribute work
// to a set that silently shrank.
//
// Nothing here fails. A goals document that cannot be read is a problem beside
// a set that still holds every goal it did read, for the same reason a
// malformed artifact is reported rather than refusing the whole set.
func Collect(repositoryRoot string, artifacts artifact.Set) Set {
	var set Set
	for _, recorded := range artifacts.OfKind(artifact.KindGoals) {
		set.Sources = append(set.Sources, recorded.ID)
		content, err := readGoalsDocument(filepath.Join(repositoryRoot, filepath.FromSlash(recorded.Path)))
		if err != nil {
			set.Problems = append(set.Problems, Problem{Path: recorded.Path, Reason: err.Error()})
			continue
		}
		stated, problem := statements(content)
		if problem != "" {
			set.Problems = append(set.Problems, Problem{Path: recorded.Path, Reason: problem})
			continue
		}
		for _, statement := range stated {
			if len(statement) > MaxStatementBytes {
				set.Problems = append(set.Problems, Problem{
					Path: recorded.Path,
					Reason: fmt.Sprintf("a goal it states is %d bytes, limit is %d; work cannot name a goal that long, so it is not one work can be attributed to",
						len(statement), MaxStatementBytes),
				})
				continue
			}
			set.Goals = append(set.Goals, Goal{
				Statement:  statement,
				ArtifactID: recorded.ID,
				Path:       recorded.Path,
				InForce:    recorded.InForce(),
			})
		}
	}
	return set
}

// Unreadable is the set a caller holds when the artifacts themselves could not
// be loaded. It records why rather than being empty, because "the repository
// records no goals" and "the goals could not be read" lead to opposite
// conclusions about an attribution nobody could check.
func Unreadable(reason string) Set {
	return Set{Unavailable: strings.TrimSpace(reason)}
}

// Known reports that the repository records at least one goal in force, which
// is what makes an attribution checkable at all.
func (s Set) Known() bool {
	for _, candidate := range s.Goals {
		if candidate.InForce {
			return true
		}
	}
	return false
}

// Uncheckable says why nothing here can check an attribution, and whether that
// is the situation at all. A caller that is about to report on many items asks
// once rather than reaching the same answer once per item, and reports what it
// could not check instead of what it did not find.
func (s Set) Uncheckable() (string, bool) {
	if s.Known() {
		return "", false
	}
	return s.uncheckableReason(), true
}

// Attribute judges one named goal: the goal a creation states as it is admitted,
// or the one an item already carries.
func (s Set) Attribute(named string) Attribution {
	statement := strings.TrimSpace(named)
	if statement == "" {
		return Attribution{State: StateUnattributed, Reason: "it names no goal, so nothing says what the work is for"}
	}
	if !s.Known() {
		return Attribution{State: StateUncheckable, Named: statement, Reason: s.uncheckableReason()}
	}
	folded := fold(statement)
	var replaced *Goal
	for index, candidate := range s.Goals {
		if fold(candidate.Statement) != folded {
			continue
		}
		if candidate.InForce {
			return Attribution{State: StateAttributed, Named: statement, Goal: candidate}
		}
		if replaced == nil {
			replaced = &s.Goals[index]
		}
	}
	if replaced != nil {
		return Attribution{
			State: StateUnresolved,
			Named: statement,
			Reason: fmt.Sprintf("the only goal stated in those words is in %s, which is no longer in force, so it is not a goal the product currently intends",
				replaced.ArtifactID),
		}
	}
	return Attribution{
		State:  StateUnresolved,
		Named:  statement,
		Reason: fmt.Sprintf("no goal recorded in %s is stated in those words", strings.Join(s.Sources, ", ")),
	}
}

// AttributionOf judges what a work item's notes claim. An item whose notes
// record no goal is unattributed rather than wrong: it says nothing, and
// nothing is not a false claim.
func (s Set) AttributionOf(notes string) Attribution {
	named, recorded := NamedIn(notes)
	if !recorded {
		return Attribution{State: StateUnattributed, Reason: "it records no goal; nothing on the item says what the work is for"}
	}
	return s.Attribute(named)
}

// uncheckableReason says why there is nothing to check an attribution against.
// The three cases are separated because they are three different things to do:
// find out why the artifacts would not load, write the goals down, or put the
// goals that were written back in force.
func (s Set) uncheckableReason() string {
	switch {
	case s.Unavailable != "":
		return "the goals could not be read, so nothing checked it: " + s.Unavailable
	case len(s.Sources) == 0:
		return "the repository records no goals artifact, so there is nothing to check it against"
	default:
		return fmt.Sprintf("%s records no goal that is in force, so there is nothing to check it against", strings.Join(s.Sources, ", "))
	}
}

// fold is how two statements of one goal are compared. Case, surrounding and
// repeated whitespace, and trailing sentence punctuation are differences in how
// a goal was typed rather than disagreements about which goal is meant.
// Everything else is left alone: a statement that differs in a word is a
// different claim, and deciding it was near enough is exactly the inference this
// package replaces.
func fold(statement string) string {
	folded := strings.ToLower(strings.Join(strings.Fields(statement), " "))
	return strings.TrimRight(folded, ".;:,")
}

// headingPattern matches an ATX Markdown heading and captures its level and
// text, the same shape the specification structure contract is expressed over.
var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// goalsHeadingPattern matches the heading a goals document states its goals
// under. It matches the heading's whole text rather than its start, so a title
// that merely opens with the word — `# Goals for V1`, `# Goals of some
// product` — is a title rather than the section. A prefix match reads such a
// title as the goals heading, at level 1, and then nothing nested below it can
// end the section: every top-level entry in the rest of the document, under any
// heading, becomes a goal work may be attributed to.
var goalsHeadingPattern = regexp.MustCompile(`(?i)^goals?$`)

// negatedGoalsHeadingPattern matches a heading stating what the product will
// not do. Such a heading ends the goals section at whatever level it is written
// at, including one nested inside it: a non-goal is the opposite of a goal, and
// attributing work to one is worse than attributing it to nothing, so a
// document that files its non-goals under its goals rather than beside them is
// read as ending the goals there rather than as stating more of them.
var negatedGoalsHeadingPattern = regexp.MustCompile(`(?i)^non-?\s*goals?\b`)

// listItemPattern matches a top-level Markdown list item and captures its text.
// Only unindented items open a goal: a goals document states each goal as one
// entry, and what is indented under one continues or describes that goal rather
// than being another.
var listItemPattern = regexp.MustCompile(`^[-*+]\s+(.*)$`)

// nestedListItemPattern matches a list entry written under a goal, bulleted or
// numbered. It ends the statement above it rather than continuing it: an entry
// breaking a goal down is prose about the goal, and nobody hard-wraps a
// sentence into a bullet.
var nestedListItemPattern = regexp.MustCompile(`^\s+(?:[-*+]|\d+[.)])\s`)

// emphasisOpeningPattern matches a line that opens Markdown emphasis at its
// very start, in either spelling, with content rather than a space after the
// marker — which is what tells `*Supports: ...*` from a nested `* bullet`.
var emphasisOpeningPattern = regexp.MustCompile(`^(\*{1,2}|_{1,2})[^\s]`)

// trailer reports a line under a goal that annotates the goal rather than
// continuing it: the emphasized `*Supports: ...*` line naming what the goal
// serves upstream. It is recognised by the emphasis it opens with rather than
// by where that emphasis closes, because Markdown is hard-wrapped and a trailer
// long enough to wrap is as ordinary as a goal long enough to wrap. Demanding
// the closing marker on the same physical line would join a wrapped trailer
// into the statement, which is the same silent corruption of the recorded goal
// arrived at from the other side.
//
// A line that opens with an emphasized phrase and then carries on in plain text
// is prose rather than a trailer: the emphasis closed and the sentence went on,
// so it is the rest of a wrapped statement.
func trailer(line string) bool {
	opening := emphasisOpeningPattern.FindStringSubmatch(line)
	if opening == nil {
		return false
	}
	marker := opening[1]
	rest := line[len(marker):]
	closing := strings.Index(rest, marker)
	return closing < 0 || strings.TrimSpace(rest[closing+len(marker):]) == ""
}

// statements reads the goals one document states, or says why it states none.
// A goal is one top-level entry under the `Goals` heading, and its statement is
// that entry's opening paragraph rejoined onto one line. The rejoining is the
// point: Markdown is normally hard-wrapped, and recording only the first
// physical line would record a fragment of every wrapped goal — silently, so
// the words the document does state would resolve to nothing and the reason
// given would name the words rather than the truncation.
//
// A statement runs to the first thing that is not more of the same sentence: a
// blank line, an unindented line, a nested entry, or the emphasized trailer
// naming what the goal supports, wrapped or not. What follows any of those
// describes the goal rather than being part of it, and none of it is a second
// goal.
//
// The section runs to the next heading at the same level or above, or to any
// heading stating what the product will not do. Both bounds exist because
// collecting the wrong prose is worse here than collecting none: what this
// returns is what work may be attributed to, so a sentence read out of the
// wrong section becomes a goal somebody can admit work under.
func statements(content string) ([]string, string) {
	lines := strings.Split(withoutFrontmatter(content), "\n")
	level := 0
	inGoals := false
	inFence := false
	// open says the statement collected last is still being read, so the line in
	// hand may be the rest of it. Anything that is not more of that sentence
	// closes it, which is why every branch below says so.
	open := false
	var stated []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence, open = !inFence, false
			continue
		}
		if inFence || line == "" {
			open = false
			continue
		}
		if heading := headingPattern.FindStringSubmatch(line); heading != nil {
			open = false
			text := strings.TrimSpace(heading[2])
			switch {
			case negatedGoalsHeadingPattern.MatchString(text):
				// What the product will not do ends the goals wherever it is
				// written, level or no level. This is the one heading a level test
				// alone cannot be trusted with: filed under the goals rather than
				// beside them, it is nested, and every non-goal below it would be
				// collected as something work may serve.
				inGoals = false
			case inGoals && len(heading[1]) <= level:
				// The section ended. A heading below it divides the goals rather
				// than ending them, exactly as it does in the structure contract.
				inGoals = false
			case !inGoals && goalsHeadingPattern.MatchString(text):
				inGoals, level = true, len(heading[1])
			}
			continue
		}
		if !inGoals {
			open = false
			continue
		}
		// The raw line rather than the trimmed one decides what is a goal: an
		// indented entry describes the goal above it.
		if item := listItemPattern.FindStringSubmatch(raw); item != nil {
			open = false
			if len(stated) >= maxGoalsPerDocument {
				continue
			}
			if statement := strings.TrimSpace(item[1]); statement != "" {
				stated = append(stated, statement)
				open = true
			}
			continue
		}
		if !open {
			continue
		}
		if !indented(raw) || nestedListItemPattern.MatchString(raw) || trailer(line) {
			open = false
			continue
		}
		stated[len(stated)-1] += " " + line
	}
	switch {
	case level == 0:
		return nil, "it states no goals under a `Goals` heading — that is a heading whose whole text is `Goals`, so a title merely opening with the word is not one — and nothing in it is a goal work can be attributed to"
	case len(stated) == 0:
		return nil, "its `Goals` section states no goals as list entries, so nothing in it is a goal work can be attributed to"
	default:
		return stated, ""
	}
}

// indented reports a line written under the entry above it rather than beside
// it. Continuing a statement asks for the indentation Markdown itself asks for:
// an unindented line under a goal is a new block, and joining it to the goal
// would put prose the entry does not contain into what work is attributed to.
func indented(raw string) bool {
	return strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")
}

// withoutFrontmatter drops the artifact identity metadata a goals document
// carries at the top of the file. The identity is validated where artifacts are
// loaded; here it is neither a heading nor a goal, and reading it as content
// would let a `supports` entry be collected as something work can serve.
func withoutFrontmatter(content string) string {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return trimmed
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.Join(lines[index+1:], "\n")
		}
	}
	return trimmed
}

// readGoalsDocument reads one goals file, bounded the same way the artifact
// store bounds it: this is the same document read a second time for what it
// says, and a file too large to identify is too large to read goals out of.
func readGoalsDocument(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("it could not be read: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("it is not a regular file")
	}
	if info.Size() > artifact.MaxFileBytes {
		return "", fmt.Errorf("it is %d bytes, limit is %d", info.Size(), artifact.MaxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("it could not be read: %w", err)
	}
	return string(content), nil
}
