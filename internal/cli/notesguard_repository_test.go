package cli

// The tests here hold the notes-writer guard — scripts/bd-notes-guard.sh, the
// one wiring that runs it today, and the hook block the instruction files carry
// for the wiring that is still a paste — to what they claim.
//
// The guard exists because a work item's goal attribution is one line in its
// notes, so `bd update <id> --notes=` destroys it; twelve attributions were lost
// that way, every one from an interactive session. It is shell and Python, so
// nothing else in `make check` executes a line of it, and its whole value is in
// writes it refuses — a refusal first exercised on the day it was needed is a
// refusal nobody had.
//
// Two of these cover seams the guard cannot cover itself. Its reading of an
// attribution is a second implementation of goal.NamedIn in another language,
// and every way that drifts is silent and fails open. And the hook block in
// CLAUDE.md and AGENTS.md is what an operator pastes into `.claude/settings.json`
// by hand — Claude Code refuses an agent's write to a settings file, so no run
// can land it — which makes a block naming a moved script a hook that fails open
// in the population that caused every recorded loss.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// notesGuardTestPath is the guard's own suite. It fabricates hook payloads and
// a stub tracker, so neither this repository's real tracker nor a real session
// is read — which is what makes it cheap enough to run from here.
const notesGuardTestPath = "../../scripts/bd-notes-guard-test.sh"

// notesGuardScriptRelativePath is the guard itself, named from the repository
// root the way the documented hook command names it.
const notesGuardScriptRelativePath = "scripts/bd-notes-guard.sh"

// notesGuardDecisionPath is the half of the guard that decides, and so the half
// that carries the second implementation of goal.NamedIn.
const notesGuardDecisionPath = "../../scripts/bd-notes-guard.py"

// notesGuardSettingsPath is this repository's own Claude Code settings, which is
// where an interactive session's copy of the hook lives.
const notesGuardSettingsPath = "../../.claude/settings.json"

// notesGuardPrefixPattern reads the prefix the guard looks for out of its
// source. It is anchored to the assignment rather than searched for loosely, so
// the same string appearing in a comment or a message cannot satisfy it.
var notesGuardPrefixPattern = regexp.MustCompile(`(?m)^ATTRIBUTION_PREFIX = "([^"]*)"`)

// notesGuardInstructionPaths are the two instruction files that carry the hook
// block. They are independent files rather than one symlinked to the other, so
// the block has to be mirrored, and a mirror that drifted is what this checks.
var notesGuardInstructionPaths = []string{"../../CLAUDE.md", "../../AGENTS.md"}

// The markers around the block an operator copies. They are HTML comments so
// they survive rendering, and they are matched rather than the heading above
// them because a heading is prose somebody will reword.
const (
	notesGuardBlockOpen  = "<!-- BEGIN NOTES GUARD HOOK -->"
	notesGuardBlockClose = "<!-- END NOTES GUARD HOOK -->"
)

// notesGuardSettings is the shape of the documented block, which is a fragment
// of a Claude Code settings file: hooks keyed by the event they run on.
type notesGuardSettings struct {
	Hooks map[string][]notesGuardMatcher `json:"hooks"`
}

// notesGuardMatcher is what one event runs, and the tool it runs it for.
type notesGuardMatcher struct {
	Matcher string             `json:"matcher"`
	Hooks   []notesGuardAction `json:"hooks"`
}

// notesGuardAction is one command a matched tool call is put through.
type notesGuardAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// TestTheNotesGuardRefusesWhatItShould runs the guard's suite. What it holds is
// both directions of the decision: the writes that destroyed twelve
// attributions are refused, and ordinary work — `--append-notes`, every other
// bd verb, every command that is not bd at all — is not. A guard that refuses
// ordinary work is removed as surely as one that refuses nothing.
func TestTheNotesGuardRefusesWhatItShould(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", notesGuardTestPath, tool)
		}
	}
	suite := exec.Command("bash", notesGuardTestPath)
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", notesGuardTestPath, err, report)
	}
}

// TestTheNotesGuardLooksForTheLineTheHarnessWrites pins the guard's copy of the
// attribution prefix to the one the harness writes and reads back.
//
// This is the one claim about the duplication that needs nothing but a file
// read, so it holds where the environment has no python3 and
// TestTheNotesGuardReadsAnAttributionTheSameWayTheHarnessDoes cannot run at all.
// What makes it worth a check rather than a comment is which way it breaks: if
// goal.AttributionPrefix changes, named_in() stops matching anything, every
// replacing write looks like a write against an unattributed item, and the
// guard allows the destruction silently. It would not fail, or warn, or refuse
// too much — it would go on passing its own suite while protecting nothing,
// which is exactly the failure this whole item exists to prevent.
func TestTheNotesGuardLooksForTheLineTheHarnessWrites(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(notesGuardDecisionPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", notesGuardDecisionPath, err)
	}
	assignment := notesGuardPrefixPattern.FindStringSubmatch(string(source))
	if assignment == nil {
		t.Fatalf("%s no longer assigns ATTRIBUTION_PREFIX, so the guard's copy of %q cannot be held to it",
			notesGuardDecisionPath, goal.AttributionPrefix)
	}
	if assignment[1] != goal.AttributionPrefix {
		t.Errorf("%s looks for %q; the harness writes and reads back %q (goal.AttributionPrefix). A guard looking for the wrong line finds no attribution to protect and allows every replacing write.",
			notesGuardDecisionPath, assignment[1], goal.AttributionPrefix)
	}
}

// TestTheNotesGuardReadsAnAttributionTheSameWayTheHarnessDoes holds the guard's
// named_in() to goal.NamedIn over the same notes, rather than holding only the
// prefix constant the two share.
//
// The prefix is the part of the rule that is one string; the rest of it is not.
// Which line counts, what is trimmed off it before the prefix is looked for,
// what is trimmed off the statement after, whether an empty statement counts at
// all, and which of several attributions wins are five more decisions made twice
// in two languages, and a diff to either side shows none of them. They all drift
// the same way when they drift — the guard reads no attribution, the replacing
// write looks like one against an unattributed item, and the destruction is
// allowed by a guard still passing its own suite.
//
// So the table is put through both. `--named-in` is the Python side answering
// the question on its own, without a hook payload or a tracker around it.
func TestTheNotesGuardReadsAnAttributionTheSameWayTheHarnessDoes(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("the guard's reader is Python and this environment has no python3: %v", err)
	}

	// Notes chosen for the parts of the rule that are not the prefix: the empty
	// statement, the later attribution, the whitespace at each end of the line
	// and of the statement, the prefix that is not at the start of a line, and
	// the line ending a tracker export can carry.
	for _, notes := range []string{
		"",
		"Filed directly, naming no goal.",
		"Admitted by the product manager.\nGoal served: Run development nearly autonomously.",
		"Goal served: An earlier goal.\nRepaired.\nGoal served: The goal it serves now.",
		"Goal served:",
		"Goal served:\nGoal served: The only one with a statement.",
		"Goal served: First.\nGoal served:",
		"   Goal served:   Spaced at both ends.   ",
		"Goal served:Tight against the colon.",
		"Goal served: Not the last line.\nA line that is not an attribution.",
		"goal served: lower case is not the prefix.",
		"Preceded by prose: Goal served: not at the start of the line.",
		"Goal served: With a carriage return.\r\nAnother line.\r\n",
		"Goal served: Trailing blank lines.\n\n\n",
	} {
		want, found := goal.NamedIn(notes)
		if (want != "") != found {
			t.Fatalf("goal.NamedIn(%q) = %q, %v; this test reads the statement alone as the whole answer", notes, want, found)
		}

		reader := exec.Command("python3", notesGuardDecisionPath, "--named-in")
		reader.Stdin = strings.NewReader(notes)
		var answer, complaint bytes.Buffer
		reader.Stdout, reader.Stderr = &answer, &complaint
		if err := reader.Run(); err != nil {
			t.Fatalf("%s --named-in error = %v: %s", notesGuardDecisionPath, err, complaint.String())
		}
		got := strings.TrimSuffix(answer.String(), "\n")
		if got != want {
			t.Errorf("notes %q: the guard reads the attribution as %q, goal.NamedIn reads it as %q. The two are separate implementations of one rule, and a guard reading no attribution where the harness reads one allows the write that destroys it.",
				notes, got, want)
		}
	}
}

// TestTheDocumentedNotesGuardHookIsPasteable holds the block an operator copies
// to three things: it parses as JSON, it runs the guard that is actually in the
// repository, and it reads the same in both instruction files. None of that is
// visible by eye in a diff, and all of it is silent when wrong — a hook naming a
// missing script is reported by Claude Code as a non-blocking error, which is a
// line in a transcript nobody reads and a guard nobody has.
func TestTheDocumentedNotesGuardHookIsPasteable(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("../../" + notesGuardScriptRelativePath); err != nil {
		t.Fatalf("Stat(%s) error = %v; the documented hook names a script this repository does not have",
			notesGuardScriptRelativePath, err)
	}

	blocks := map[string]string{}
	for _, path := range notesGuardInstructionPaths {
		block := notesGuardBlockIn(t, path)
		blocks[path] = block

		var settings notesGuardSettings
		if err := json.Unmarshal([]byte(block), &settings); err != nil {
			t.Fatalf("%s carries a hook block that is not JSON (%v); an operator is told to paste it into a settings file", path, err)
		}
		matchers := settings.Hooks["PreToolUse"]
		if len(matchers) == 0 {
			t.Fatalf("%s carries a hook block with no PreToolUse entry, which is the only event that can refuse a write", path)
		}
		named := false
		for _, matcher := range matchers {
			if matcher.Matcher != "Bash" {
				t.Errorf("%s wires the guard for tool %q; a `bd update` is a Bash call and nothing else", path, matcher.Matcher)
			}
			for _, hook := range matcher.Hooks {
				if hook.Type != "command" {
					t.Errorf("%s wires the guard as hook type %q, want \"command\"", path, hook.Type)
				}
				if strings.Contains(hook.Command, notesGuardScriptRelativePath) {
					named = true
				}
			}
		}
		if !named {
			t.Errorf("%s wires a PreToolUse hook that does not name %s, so pasting it would not install the guard",
				path, notesGuardScriptRelativePath)
		}
	}

	// The two files are independent — not symlinked, not sharing an inode — so
	// the block is duplicated by hand, and a change made to one of them is a
	// change half the agents reading this repository never see.
	first := notesGuardInstructionPaths[0]
	for _, path := range notesGuardInstructionPaths[1:] {
		if blocks[path] != blocks[first] {
			t.Errorf("the hook block in %s and %s have drifted apart; they are mirrored by hand and both are copied from",
				first, path)
		}
	}
}

// TestTheInteractiveNotesGuardWiringMatchesTheDocumentedBlock holds this
// repository's own `.claude/settings.json` to the block the instruction files
// tell an operator to paste into it — once it carries the paste at all.
//
// It skips rather than fails on a settings file with no PreToolUse entry
// because nothing that runs here can put one there. Claude Code refuses an
// agent's write to a settings file: Write and Edit are both denied on that path
// regardless of what the run is otherwise permitted — including a run holding an
// explicit grant for it, which was tried — so the paste is a person's hand and
// nothing else. A check that fails on a state no contributor working through an
// agent can leave is a red gate rather than a finding.
//
// **The skip expires the moment the entry lands.** Once `.claude/settings.json`
// carries a PreToolUse hook, the reason this is not an assertion is gone, and
// the branch below has to become a failure: otherwise deleting the interactive
// wiring — the half that covers the population all twelve losses came from —
// passes `make check` in silence, which is the exact shape of failure this file
// exists to catch everywhere else. Whoever pastes the block changes t.Skipf to
// t.Fatalf in the same commit.
//
// What it holds meanwhile is the failure that comes after the paste: a wiring
// that lands and then drifts from the documented block, or a script that moves
// out from under a settings file nothing else in `make check` reads.
func TestTheInteractiveNotesGuardWiringMatchesTheDocumentedBlock(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(notesGuardSettingsPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", notesGuardSettingsPath, err)
	}
	var settings notesGuardSettings
	if err := json.Unmarshal(content, &settings); err != nil {
		t.Fatalf("%s is not JSON (%v); Claude Code reads it before every session here", notesGuardSettingsPath, err)
	}
	if len(settings.Hooks["PreToolUse"]) == 0 {
		t.Skipf("%s wires no PreToolUse hook, so an interactive `bd update <id> --notes=` here is unguarded — which is the population that destroyed all twelve recorded attributions. Paste the block between %s and %s in %s; an agent cannot, because Claude Code refuses a write to a settings file. Turn this skip into a t.Fatalf in the same commit: once the entry is there, its absence is a deletion rather than a state nobody could fix.",
			notesGuardSettingsPath, notesGuardBlockOpen, notesGuardBlockClose, notesGuardInstructionPaths[0])
	}

	var documented notesGuardSettings
	block := notesGuardBlockIn(t, notesGuardInstructionPaths[0])
	if err := json.Unmarshal([]byte(block), &documented); err != nil {
		t.Fatalf("%s carries a hook block that is not JSON: %v", notesGuardInstructionPaths[0], err)
	}
	want := documented.Hooks["PreToolUse"]
	got := settings.Hooks["PreToolUse"]
	if !notesGuardMatchersEqual(got, want) {
		t.Errorf("%s wires %+v; the block the instruction files tell an operator to paste is %+v. The two have to say the same thing: the file is what an interactive session runs, and the block is what anybody setting up a checkout copies.",
			notesGuardSettingsPath, got, want)
	}
	// The guard is reached through $CLAUDE_PROJECT_DIR here, unlike in a
	// developer run, because one checked-in file serves every checkout and no
	// absolute path is true in all of them. It still has to name the script that
	// is in the repository: a hook command that does not resolve is a
	// non-blocking error Claude Code lets the call through on.
	if _, err := os.Stat("../../" + notesGuardScriptRelativePath); err != nil {
		t.Fatalf("Stat(%s) error = %v; %s wires a script this repository does not have",
			notesGuardScriptRelativePath, err, notesGuardSettingsPath)
	}
}

// notesGuardMatchersEqual compares two PreToolUse wirings by what they do rather
// than by the bytes they were written as, so re-indenting a settings file is not
// a failure and changing the command it runs is.
func notesGuardMatchersEqual(got, want []notesGuardMatcher) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index].Matcher != want[index].Matcher || len(got[index].Hooks) != len(want[index].Hooks) {
			return false
		}
		for action := range got[index].Hooks {
			if got[index].Hooks[action] != want[index].Hooks[action] {
				return false
			}
		}
	}
	return true
}

// notesGuardBlockIn pulls the fenced JSON an instruction file offers for pasting
// out from between its markers.
func notesGuardBlockIn(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	_, after, found := strings.Cut(string(content), notesGuardBlockOpen)
	if !found {
		t.Fatalf("%s no longer carries %s, so the hook an operator pastes cannot be checked", path, notesGuardBlockOpen)
	}
	block, _, found := strings.Cut(after, notesGuardBlockClose)
	if !found {
		t.Fatalf("%s opens %s and never closes it", path, notesGuardBlockOpen)
	}
	block = strings.TrimSpace(block)
	block = strings.TrimPrefix(block, "```json")
	block = strings.TrimSuffix(strings.TrimSpace(block), "```")
	return strings.TrimSpace(block)
}
