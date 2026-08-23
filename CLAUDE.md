# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:6cd5cc61 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

## Never replace a work item's notes

`bd update <id> --notes=` **replaces** the notes field wholesale. Use
`bd update <id> --append-notes=` to add to it.

This matters more here than it looks. A work item's goal attribution is one line
in its notes — `Goal served: <statement>`, written and read back by
`internal/goal/goal.go` — so the notes are the record, and a writer that
replaces them takes the attribution with it. Twelve attributions were destroyed
exactly that way, every one of them from an interactive session; each loss is
matched to the command that caused it in
[the diagnosis](docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md).

[`scripts/bd-notes-guard.sh`](scripts/bd-notes-guard.sh) enforces this as a
`PreToolUse` hook: it refuses a `bd update --notes=` against an item whose notes
name a goal, unless the replacement carries that same `Goal served:` line
across. It allows everything else, silently.

**Harness developer runs already run it** — `developerSettings` in
`internal/backend/claudecode/backend.go` wires this same script, by an absolute
path into the run's own worktree, and a test holds it to that. **Interactive
sessions do not, yet.** One script serves both, so the refusal is identical
rather than merely similar; what differs is only whether the hook is wired, and
for interactive sessions that is the paste below.

<!-- BEGIN NOTES GUARD HOOK -->
```json
{
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "command": "bash \"$CLAUDE_PROJECT_DIR/scripts/bd-notes-guard.sh\"",
            "type": "command"
          }
        ],
        "matcher": "Bash"
      }
    ]
  }
}
```
<!-- END NOTES GUARD HOOK -->

Merge that `PreToolUse` entry into `.claude/settings.json` alongside the
`SessionStart` hook already there. It has to be landed by hand: Claude Code
refuses an agent's write to a settings file, so no harness run can wire its own
session's hooks. `internal/cli` checks the block above stays valid and keeps
naming a script that exists, but nothing can check that you pasted it — until
you do, an interactive `bd update --notes=` is unguarded.

## Build & Test

_Add your build and test commands here_

```bash
# Example:
# npm install
# npm test
```

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_
