package notesguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// blockedExit is the exit status Claude Code reads as "refuse this tool call
// and show the model why". Every other non-zero status is reported and then
// ignored, so asserting the number is asserting that the guard guards.
const blockedExit = 2

// scriptPath locates the guard from this test's own file rather than from the
// working directory, so the test finds it wherever `go test` is run from.
func scriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file to find the guard script")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", ScriptPath)
}

// runGuard feeds the guard the payload shape Claude Code actually sends a
// PreToolUse hook and reports how it answered.
func runGuard(t *testing.T, command string) (exitCode int, stderr string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"session_id":      "test-session",
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input": map[string]any{
			"command":     command,
			"description": "a command under test",
		},
	})
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}

	cmd := exec.Command("bash", scriptPath(t))
	cmd.Stdin = bytes.NewReader(payload)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err == nil {
		return 0, errBuf.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), errBuf.String()
	}
	t.Fatalf("run guard: %v", err)
	return 0, ""
}

func TestTheGuardRefusesEveryWriterThatReplacesNotes(t *testing.T) {
	// Each of these destroys an item's `Goal served:` line. The first is the
	// literal shape that cost twelve items their attribution.
	replacing := []string{
		`bd update yoyodyne-ifd.45 --notes="Fresh evidence 2026-08-18: something"`,
		`bd update yoyodyne-ifd.45 --notes "separated by a space"`,
		`bd  update yoyodyne-ifd.45 --notes=x`,
		`cd /tmp && bd update yoyodyne-ifd.45 --notes=x`,
		`bd update yoyodyne-ifd.45 --status=open --notes=x`,
	}
	for _, command := range replacing {
		exitCode, stderr := runGuard(t, command)
		if exitCode != blockedExit {
			t.Errorf("command %q exited %d, want %d (the status that blocks the call)", command, exitCode, blockedExit)
		}
		// A refusal an agent cannot act on is a refusal it works around, so the
		// safe writer has to be named in the message itself.
		if !strings.Contains(stderr, "--append-notes") {
			t.Errorf("command %q was refused without naming --append-notes: %s", command, stderr)
		}
	}
}

func TestTheGuardLetsEverySafeWriterThrough(t *testing.T) {
	// --append-notes is how the harness itself writes every run record; a guard
	// that blocked it would stop the harness rather than the careless writer.
	// `bd create --notes` names an item that does not exist yet, so it replaces
	// nothing.
	safe := []string{
		`bd update yoyodyne-ifd.45 --append-notes="the run's own record"`,
		`bd create --title="a new item" --notes="first notes"`,
		`bd update yoyodyne-ifd.45 --status=closed`,
		`bd show yoyodyne-ifd.45`,
		`go test ./...`,
		`git status`,
	}
	for _, command := range safe {
		exitCode, stderr := runGuard(t, command)
		if exitCode != 0 {
			t.Errorf("command %q exited %d, want 0; stderr: %s", command, exitCode, stderr)
		}
	}
}

func TestTheHookCommandQuotesAPathThatWillContainSpaces(t *testing.T) {
	// Harness developer runs happen in worktrees under "Application Support".
	// An unquoted path splits there and the hook silently never runs, which
	// looks exactly like a guard that is wired in.
	if !strings.Contains(HookCommand, `"${CLAUDE_PROJECT_DIR:-.}/`+ScriptPath+`"`) {
		t.Errorf("hook command does not quote the script path: %s", HookCommand)
	}
}
