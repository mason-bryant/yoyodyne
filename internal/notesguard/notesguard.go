// Package notesguard names the guard that keeps a careless tracker write from
// destroying a work item's attribution, so whatever wires it in and the tests
// that prove it fires all refer to the same script by one name.
//
// The guard itself is a shell script rather than Go: it runs as a Claude Code
// PreToolUse hook, before every Bash call in every session that gets a shell,
// and a hook that depends on a built binary being on PATH is a hook that stops
// guarding wherever the binary is missing -- silently, because Claude Code
// treats a hook that fails to run as an error to report rather than a refusal
// to honour. A script in the checkout is present wherever the checkout is.
//
// Two populations get a shell, and only one consumes HookCommand today:
// harness developer runs, through the settings the Claude Code backend hands
// each run. Interactive sessions in a checkout are wired in `.claude/settings.json`,
// which this change does not touch, so that population is currently unguarded.
// docs/configuration.md states the same gap; do not describe it as wired here
// without wiring it there.
package notesguard

// ScriptPath is where the guard lives, relative to the repository root. Any
// wiring resolves it against the session's project directory.
const ScriptPath = "scripts/bd-notes-guard.sh"

// HookCommand is the PreToolUse command a wired population runs.
//
// The path is quoted because it is not hypothetically one with a space in it:
// harness developer runs happen in worktrees under "Application Support", so an
// unquoted path would split and the guard would never run. CLAUDE_PROJECT_DIR
// is what Claude Code exports for the session's project root; the `:-.` fallback
// covers a session that does not set it, where the hook's working directory is
// already that root.
//
// It exits 0 when the script is absent rather than running it anyway. This
// command is compiled into the harness, while the script it runs is checked out
// with the branch under development, so the two can disagree: a developer run on
// a branch that predates the script would otherwise have `bash` exit 127 on every
// single Bash call, turning one missing file into a hook error on every tool use
// for the life of the run. Degrading to unguarded is the lesser failure, and it
// is the one that matches what an older branch actually is.
const HookCommand = `guard="${CLAUDE_PROJECT_DIR:-.}/` + ScriptPath + `"; [ -f "$guard" ] || exit 0; bash "$guard"`
