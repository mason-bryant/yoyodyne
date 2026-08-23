# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

## Never replace a work item's notes

**`bd update <id> --notes=...` replaces the notes field wholesale. Use
`--append-notes` instead.**

```bash
bd update <id> --append-notes="what you want to record"   # adds to the notes
bd update <id> --notes="what you want to record"          # DESTROYS everything already there
```

A work item records the goal it serves as a `Goal served:` line in its notes, so
a wholesale replacement takes the attribution with it and the item afterwards
reads as work nobody ever attributed. This is not hypothetical: it has happened
twice, to six items and then to twelve more, every one of them from
`bd update --notes=` typed into an agent session. `docs/diagnoses/yoyodyne-ifd-122-goal-attribution-loss.md`
matches each destroyed record to the command that destroyed it.

`yoyo goals guard` refuses the command before it runs, and allows a replacement
that carries the `Goal served:` line through — so if the notes genuinely have to
be rewritten, include that line in the replacement. The harness wires the guard
into every developer run it makes on the Claude Code backend, which is where the
hook is passed. An interactive session gets it by adding a `PreToolUse` hook on
`Bash` to `.claude/settings.json`:

```json
{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]}}
```

The guard only covers sessions it is wired into, which is why the rule is
written here as well. `yoyo goals attribution` reports and fails on an
attribution destroyed on an open or blocked item; on a claimed or closed item
the witness keeps the words to put back but nothing fails, so the rule above is
the only thing standing between those items and a silent loss.

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
