package notesguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
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

// aReplacingWrite is the literal shape that cost twelve items their attribution.
const aReplacingWrite = `bd update yoyodyne-ifd.45 --notes="Fresh evidence 2026-08-18: something"`

// repoRoot locates the checkout from this test's own file rather than from the
// working directory, so the test finds the script wherever `go test` is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file to find the guard script")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// hookPayload is the shape Claude Code sends a PreToolUse hook.
func hookPayload(t *testing.T, command string) []byte {
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
	return payload
}

func runProcess(t *testing.T, cmd *exec.Cmd, payload []byte) (exitCode int, stderr string) {
	t.Helper()
	cmd.Stdin = bytes.NewReader(payload)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return 0, errBuf.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), errBuf.String()
	}
	t.Fatalf("run %v: %v", cmd.Args, err)
	return 0, ""
}

// runGuard asks the script itself what it thinks of a command.
func runGuard(t *testing.T, command string) (exitCode int, stderr string) {
	t.Helper()
	return runProcess(t, exec.Command("bash", filepath.Join(repoRoot(t), ScriptPath)), hookPayload(t, command))
}

// runHookCommand runs what a wired population actually carries -- the whole
// HookCommand string through a shell, against a project directory of its own --
// rather than the script directly. This is the half that decides whether the
// guard fires at all, and it is not exercised by running the script by hand.
func runHookCommand(t *testing.T, projectDir, command string) (exitCode int, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", HookCommand)
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+projectDir)
	return runProcess(t, cmd, hookPayload(t, command))
}

func TestTheGuardRefusesEveryWriterThatReplacesNotes(t *testing.T) {
	// Each of these destroys an item's `Goal served:` line.
	replacing := []string{
		aReplacingWrite,
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

func TestTheWiredHookCommandFiresFromAProjectDirectoryContainingASpace(t *testing.T) {
	// Harness developer runs happen in worktrees under "Application Support".
	// An unquoted path splits there and the hook silently never runs, which
	// looks exactly like a guard that is wired in -- so the quoting is tested by
	// running it against such a path rather than by reading the string.
	projectDir := filepath.Join(t.TempDir(), "Application Support", "a worktree")
	if err := os.MkdirAll(filepath.Join(projectDir, "scripts"), 0o755); err != nil {
		t.Fatalf("create project directory: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), ScriptPath))
	if err != nil {
		t.Fatalf("read guard script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ScriptPath), script, 0o644); err != nil {
		t.Fatalf("place guard script: %v", err)
	}

	exitCode, stderr := runHookCommand(t, projectDir, aReplacingWrite)
	if exitCode != blockedExit {
		t.Fatalf("wired hook exited %d, want %d; stderr: %s", exitCode, blockedExit, stderr)
	}
	if !strings.Contains(stderr, "--append-notes") {
		t.Fatalf("wired hook refused without naming --append-notes: %s", stderr)
	}

	if exitCode, stderr := runHookCommand(t, projectDir, `bd update yoyodyne-ifd.45 --append-notes="safe"`); exitCode != 0 {
		t.Fatalf("wired hook exited %d on a safe writer, want 0; stderr: %s", exitCode, stderr)
	}
}

func TestTheWiredHookCommandIsANoOpWhereTheScriptIsAbsent(t *testing.T) {
	// The command is compiled into the harness; the script is checked out with
	// the branch. A developer run on a branch predating the script must degrade
	// to unguarded, not to `bash` exiting 127 on every Bash call it ever makes.
	exitCode, stderr := runHookCommand(t, t.TempDir(), aReplacingWrite)
	if exitCode != 0 {
		t.Fatalf("wired hook exited %d with no script present, want 0; stderr: %s", exitCode, stderr)
	}
	if stderr != "" {
		t.Fatalf("wired hook complained with no script present: %s", stderr)
	}
}
