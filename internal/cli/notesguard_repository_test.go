package cli

// The tests here hold the notes-writer guard — scripts/bd-notes-guard.sh, and
// the hook stanza the instruction files tell an operator to paste — to what
// they claim.
//
// The guard exists because a work item's goal attribution is one line in its
// notes, so `bd update <id> --notes=` destroys it; twelve attributions were lost
// that way, every one from an interactive session. It is shell and Python, so
// nothing else in `make check` executes a line of it, and its whole value is in
// writes it refuses — a refusal first exercised on the day it was needed is a
// refusal nobody had.
//
// The second test covers the seam the guard cannot cover itself. Claude Code
// refuses an agent's write to a settings file, so `.claude/settings.json` is
// landed by hand from the block in CLAUDE.md and AGENTS.md. That makes the block
// the thing an operator copies, and a copied block that names a script which has
// since moved is a hook that fails open in the population that caused every
// recorded loss.

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// notesGuardTestPath is the guard's own suite. It fabricates hook payloads and
// a stub tracker, so neither this repository's real tracker nor a real session
// is read — which is what makes it cheap enough to run from here.
const notesGuardTestPath = "../../scripts/bd-notes-guard-test.sh"

// notesGuardScriptRelativePath is the guard itself, named from the repository
// root the way the documented hook command names it.
const notesGuardScriptRelativePath = "scripts/bd-notes-guard.sh"

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
