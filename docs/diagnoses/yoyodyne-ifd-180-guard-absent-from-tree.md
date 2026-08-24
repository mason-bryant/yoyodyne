# Diagnosis: ifd.149 closed against a notes-writer guard that is not in the tree

Work item: yoyodyne-ifd.180. Diagnosis-class against the records: this document
changes no tracker state and restores no code. What it establishes is what
yoyodyne-ifd.149's run actually built, that the work is absent from the tree
today, and which part of the "how did it close" question the available records
can and cannot answer.

Records read at 2026-08-23, against the Claude Code session transcripts under
`~/.claude/projects/` and the working tree of branch
`yoyodyne/yoyodyne-ifd-180/5a2c061c`. The Beads database could not be queried:
`bd` is unreachable from this run for the same reason described under
[Limitations](#limitations-on-this-diagnosis).

## Summary

ifd.149's run did not skip the work. It implemented the guard across at least
five files, in a shape different from and more ambitious than the one ifd.180
assumed, and then never compiled or tested any of it — its session was hit
repeatedly by the E2BIG shell-spawn wall, and no `make` check ran in it at all.
None of that code is in the tree today. Not one identifier from it survives.

So the two halves of the work item's premise separate. That the delivered work
is **absent and was never verified** is established here. That the E2BIG wall is
what caused a *closure claiming otherwise* is **not** established — it is
consistent with the evidence and remains the leading explanation, but the
records that would settle it are the ones this run cannot reach.

## What ifd.149's run actually built

Worktree `yoyodyne-ifd-149-e0a6b4e0`; principal developer session
`7b80809c-5656-4905-9a7d-497dd7eb08dc`. Its design was a Go decision function
behind a `yoyo` CLI hook entry point, not a standalone shell script:

| File | What it carried |
| --- | --- |
| `internal/beads/notesguard.go` | `DestroyedAttribution(command string) string`, with `notesReplacement`, `invokesBd`, and `simpleCommands` splitting a shell line into individual commands |
| `internal/beads/notesguard_test.go` | table-driven cases, including `--append-notes` and `bd create --type=task --title=…` as permitted |
| `internal/cli/goals.go` | `hookDecision` / `hookDecisionOutput` written as JSON, reading `toolInput.Command` — the PreToolUse entry point |
| `internal/cli/goals_test.go` | decodes `hookDecision` |
| the Claude Code backend | `developerSettings`, carrying the hook beside the OS-level sandbox |

It also amended the repository's agent instructions with the hook stanza and the
sentence "The guard only covers sessions it is wired into, which is why the rule
is written here as well", alongside a `yoyo goals attribution` reporting command.

That is a coherent, more thorough design than a hook script: it splits compound
command lines properly rather than pattern-matching a payload, and it put the
decision in Go where the project's checks would have covered it.

## It was never verified

The session shows the E2BIG wall striking at least nine times, first at
transcript line 17 — before the first of those files was created, at line 112 —
and again at lines 307, 330 and 403. The failure is the same one still active
today: `Could not start /bin/zsh: the command line plus environment exceed the
OS exec argument limit (E2BIG)`, from a Bash sandbox profile inflated by one
deny path per registered git worktree.

The session contains no occurrence of `Check: make` and none of `passed=true`.
No build, no test, no vet, no formatting check ran against any of that code.

## It is not in the tree

Searched across the whole working tree: `hookDecision`, `DestroyedAttribution`,
`simpleCommands`, and `invokesBd` return zero matches; `internal/beads/`
contains only `client.go`, `client_test.go`, `conformance_test.go`, `remote.go`
and `remote_test.go`; `PreToolUse` appears nowhere; and the string `ifd.149`
appears nowhere in any file. The absence is total rather than partial, which is
what distinguishes this from a change that landed and then decayed.

## What this does not establish

**Whether the work was integrated and then lost, or never integrated at all.**
Those two have very different consequences — the first implicates the promotion
and reconciliation path, the second implicates only this run — and telling them
apart needs the branch history and the tracker's record of the run, neither
reachable from here.

**What ifd.149's closing summary actually claimed, and what its reviewer saw.**
The run did emit `yoyodyne-report` blocks (transcript lines 315, 319, 321, 413),
so it was not uniformly silent; their text could not be extracted through the
transcripts' JSON escaping without a shell. Whether it reported the wall, and
whether it nonetheless asserted a delivered guard, is a question for the notes
on ifd.149 and the review record.

Until those are read, "the wall silently corrupted the run" is the leading
explanation and not a proven one. The weaker claim is fully proven and is
sufficient on its own: **an item closed against work that was never built,
never verified, and is not present.**

## Limitations on this diagnosis

Every shell invocation from this run fails with E2BIG, so `bd`, `git`, and
`make` were all unavailable. The findings above rest on the session transcripts
and on direct reads and searches of the working tree, which agree with each
other. The tracker's own record of ifd.149 — its notes, its close reason, its
review decision — is the missing channel, and it is the one that would answer
the closure question. `.beads/issues.jsonl` does not substitute: it is a stale
export whose newest rows are ifd.64-era and it carries no ifd.149 row.

## The correction ifd.149's record needs

This run could not write it, for the reason above. The text it should carry,
appended with `--append-notes` so nothing already on the item is replaced:

> Correction (yoyodyne-ifd.180, 2026-08-23): the notes-writer guard this item
> was closed against is not in the tree. The run implemented it across
> `internal/beads/notesguard.go`, `internal/cli/goals.go`, their tests, and the
> Claude Code backend's developer settings, but the E2BIG shell-spawn wall
> prevented every check — no build, test, vet, or format check ran — and none of
> the code is present today. See
> `docs/diagnoses/yoyodyne-ifd-180-guard-absent-from-tree.md`. Whether the change
> was integrated and lost or never integrated is unresolved.
