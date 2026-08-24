// Package notesguard names the guard that keeps a careless tracker write from
// destroying a work item's attribution, so the two places that wire it in and
// the test that proves it fires all refer to the same script by one name.
//
// The guard itself is a shell script rather than Go: it runs as a Claude Code
// PreToolUse hook, before every Bash call in every session that gets a shell,
// and a hook that depends on a built binary being on PATH is a hook that stops
// guarding wherever the binary is missing -- silently, because Claude Code
// treats a hook that fails to run as an error to report rather than a refusal
// to honour. A script in the checkout is present wherever the checkout is.
//
// What lives here is the wiring the two populations share and the test that
// executes the script, which is what puts the guard's behaviour inside
// `make check` instead of in a shell test nothing runs.
package notesguard

// ScriptPath is where the guard lives, relative to the repository root. Both
// wirings resolve it against the session's project directory.
const ScriptPath = "scripts/bd-notes-guard.sh"

// HookCommand is the PreToolUse command both populations run.
//
// The path is quoted because it is not hypothetically one with a space in it:
// harness developer runs happen in worktrees under "Application Support", so an
// unquoted path would split and the guard would never run. CLAUDE_PROJECT_DIR
// is what Claude Code exports for the session's project root; the `:-.` fallback
// covers a session that does not set it, where the hook's working directory is
// already that root.
const HookCommand = `bash "${CLAUDE_PROJECT_DIR:-.}/` + ScriptPath + `"`
