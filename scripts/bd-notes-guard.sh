#!/usr/bin/env bash
#
# bd-notes-guard.sh - refuse a `bd update <id> --notes=` that would destroy the
# goal attribution living in that item's notes.
#
# This is a Claude Code PreToolUse hook. It reads the hook payload on stdin and,
# for a Bash tool call, decides one thing: whether the command about to run
# replaces the notes of an item whose notes are the only copy of its
# `Goal served:` line.
#
#   allowed  exit 0, silently -- the ordinary answer
#   refused  exit 2, with the reason on stderr, which is what blocks the call
#
# Why this exists. An attribution is one line in a work item's notes
# (`internal/goal/goal.go`, read back by `goal.NamedIn`), so the notes are the
# record and a writer that replaces them takes the attribution with it. Twelve
# attributions were destroyed that way before anybody noticed, every one of them
# from an interactive session. See
# docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md, which matches each
# loss to the command that caused it.
#
# Who runs this. There are two populations and one script, so the refusal is the
# same in both rather than merely alike:
#
#   harness developer runs  wired in `developerSettings`
#                           (internal/backend/claudecode/backend.go), which names
#                           this script by an absolute path into the run's own
#                           worktree and is held to that by
#                           TestDeveloperRunsPutBashThroughTheNotesGuard.
#   interactive sessions    wired in this repository's .claude/settings.json,
#                           which names it through $CLAUDE_PROJECT_DIR because
#                           that file is shared by every checkout and no absolute
#                           path is true in all of them. Held to that by
#                           TestInteractiveSessionsAreWiredToTheNotesGuard.
#
# `beads.Client.Update` passing only `--append-notes` is a separate guarantee
# and covers neither of the above: it makes the writes the *harness itself*
# issues safe, and says nothing about an agent typing `bd update` into Bash,
# which is what this script is for.
#
# This half is the wrapper: it decides whether the payload is worth examining at
# all, and bd-notes-guard.py decides what to do about it. The split is so the
# examining half can be ordinary Python in a file rather than a quoted string
# inside a shell script, and so the payload -- an arbitrarily long shell command
# -- reaches it on stdin, where no argument-length limit applies.
#
# Requires bash. Uses python3 where it is present, and nothing else. Nothing is
# written; the only side effect is one `bd show <id> --json` per item named by a
# replacing write, which is none at all for almost every command reaching here.

set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
payload="$(cat)"

# Almost every Bash call in a session has nothing to do with this, and this hook
# runs before all of them. The flag survives JSON encoding unaltered, so its
# absence from the raw payload is proof there is nothing here to examine, and
# proving that is worth doing before spawning an interpreter.
case "$payload" in
  (*--notes*) ;;
  (*) exit 0 ;;
esac

# Which way this fails matters, and the two halves fail in opposite directions.
# With no python3 the guard cannot tell a `bd update` from any other command, so
# it allows and says so: a hook that blocked every Bash call over a missing
# interpreter is a hook the operator removes, which loses the guard rather than
# tightening it. Once a command is *known* to be the replacing form,
# bd-notes-guard.py refuses on a tracker read it cannot complete -- being wrong
# there costs one retry, where being wrong the other way costs the record.
if ! command -v python3 >/dev/null 2>&1; then
  printf '%s\n' \
    'bd-notes-guard: no python3 on PATH, so this "--notes" write went unchecked.' \
    'If it is a `bd update <id> --notes=`, it replaces that item'"'"'s notes wholesale' \
    'and takes any `Goal served:` line with them. Prefer --append-notes.' >&2
  exit 0
fi

printf '%s' "$payload" | python3 "$here/bd-notes-guard.py"
