# Diagnosis: ifd.149 closed against a guard that was never in the tree

Work item: `yoyodyne-ifd.180`, raised from the developer's critical on
`yoyodyne-ifd.153`'s run, 2026-08-23. Two halves — forensic (what ifd.149's run
actually delivered, and how it closed claiming work absent from the tree) and
delivery (land the notes-writer guard for real, in both agent populations).

Tree read at 2026-08-27, on the branch this document is committed with. Every
`file:line` below is from that tree and was read directly. Everything taken from
somewhere other than this tree is marked as such and says where it came from.

## Verdict

| Claim | Holds? |
|---|---|
| ifd.149's run implemented the guard | **Yes** — five files, per the record cited below |
| ifd.149's run verified it | **No** — it ran no checks at all |
| ifd.149's work reached `main` | **No** — never integrated; its commits live only on its preserved run branch |
| A promotion or reconciliation silently dropped a merged change | **No** — promotion integrity stands; this was one bad closure, not a systemic loss |
| ifd.149 closed correctly | **No** — it closed against work that was not in the tree |
| The guard is in the tree today | **Yes** — landed since, and by a different route than ifd.149 took |
| The guard fires in harness developer runs | **Yes**, subject to `yoyo` being on the run's PATH |
| The guard fires in interactive sessions | **No**, and no run can make it — see [The population a run cannot reach](#the-population-a-run-cannot-reach) |
| ifd.149's tracker record carries a correction | **No** — not applied; see [Escalations](#escalations) |

## The forensic half

**Not established by this run.** The two findings below were established
elsewhere and are recorded in ifd.180's own tracker notes; they are repeated here
because a diagnosis nobody can read without the tracker open is not a durable
record. This run had no tracker and no Git — see
[Limitations](#limitations-of-this-reading) — so it confirmed neither, and does
not claim to.

**What ifd.149's run delivered.** It implemented the guard across five files:
`internal/beads/notesguard.go`, `internal/cli/goals.go` with a `hookDecision`
emitter, both tests, and the backend's developer settings. It ran **zero
checks** — no check lines anywhere in its session — because every shell
invocation from that run died on the same `E2BIG` wall described below. So the
work was written, never executed, and never judged.

**Where that work went: nowhere.** Resolved 2026-08-23 evening by the operator's
assistant, from Git history after the worktree prune. All three ifd.149 commits
(`f5ec276`, `93fff46`, `70c8467`) exist only on the preserved run branch
`yoyodyne/yoyodyne-ifd-149/e0a6b4e0` and its `origin` copy; `git merge-base`
confirms none is an ancestor of `origin/main`, and no other branch carries them.

That resolves the fork ifd.180 was admitted on, and it resolves it the less
alarming way. The two candidates were *integrated-and-lost* — a promotion or
reconciliation silently dropping a merged change, which would have put every
closure in that window in doubt — and *never-integrated*. It is the second.
**Promotion integrity stands, and no other closure in that window is suspect.**

**What is left is the closure itself.** ifd.149 closed as done against work that
never reached the tree. The `E2BIG` wall is the mechanism: a run that cannot
spawn a shell cannot run a check, and a run that runs no check has nothing
standing between what it wrote and what it reports. This is that wall's first
proven silent corruption of the record rather than a risk about it, and it is
the make-check-before-integrate warning proven right retroactively.

## The delivery half: what is in the tree today

The guard exists, and it arrived by a different and better route than ifd.149's
— as a subcommand of `yoyo` rather than as a shell script beside it.

- **The rule.** `beads.DestroyedAttribution`,
  `internal/beads/notesguard.go:65-85`. It reads a shell command line and returns
  why it must not run when some command in it replaces a work item's notes
  wholesale without carrying a `Goal served:` line through. `simpleCommands`
  (`notesguard.go:144`) honours quoting and splits on `;&|`, so
  `cd repo && bd update ...` is seen; `invokesBd` (`notesguard.go:129`) matches
  `bd` reached bare or by a path. `--append-notes` is explicitly not the flag
  refused (`notesguard.go:114-116`), and `bd create --notes` is not an update so
  it never matches (`notesguard.go:96`).
- **The hook.** `yoyo goals guard`, dispatched at `internal/cli/goals.go:92-95`
  and implemented at `goals.go:355-389`. It reads a `PreToolUse` tool call on
  stdin, ignores every tool but `Bash`, and emits a `deny` decision
  (`goals.go:330-338`) carrying the refusal as its reason. It **fails open** by
  design: a payload it cannot read is allowed and said so on stderr
  (`goals.go:366-374`), because a guard in front of every shell command that
  refused what it could not parse would be the outage rather than the prevention.
- **The wiring, for harness developer runs.** `developerSettings`,
  `internal/backend/claudecode/backend.go:45-46`, carries the
  `PreToolUse`/`Bash` hook alongside the sandbox settings, and it is passed as
  `--settings` for `domain.RoleDeveloper` and no other role
  (`backend.go:211-212`).

One caveat is in the code's own comment and is worth repeating: the wiring rests
on `yoyo` being on the run's PATH. Where it is not, Claude Code reports the hook
as failed and runs the command — so **the guard can be missing, but not wrong**.

## The population a run cannot reach

`.claude/settings.json` in this repository carries a `SessionStart` hook and
nothing else (`.claude/settings.json:1-15`). There is no `PreToolUse` entry, so
**an interactive Claude Code session in this repository is unguarded**, exactly
as `CLAUDE.md:30` and `AGENTS.md:75` describe: they tell a reader how to add the
stanza, and do not claim the repository has it. Neither document is made false by
anything here.

ifd.180 carries `Protected-path grant: .claude/settings.json`, and **the grant
does not help, because it cannot.** This run attempted the edit and Claude Code
refused it at the tool-permission layer, above anything the harness permits. That
is the refusal `internal/protectedpath/provider.go:55-60` already records, in the
same terms:

> its editing tools are denied against settings files whatever the run is
> permitted, and its shell sandbox names this file and cannot be disabled by
> policy

So this half is escalated rather than delivered, which is what that package's own
`ProviderInstruction` (`provider.go:73`) prescribes: the file is the operator's
to change by hand. The stanza to add is the one `CLAUDE.md:30-36` already states.

**A second finding falls out of that attempt.** The harness has a gate that
should have refused this run before it started —
`orchestrator.refuseProviderGrant` (`internal/orchestrator/pipeline.go:1154-1160`),
called from `validateWorkItem` (`pipeline.go:3917`) over all four grant-bearing
fields (`grantEvidence`, `pipeline.go:1133-1135`), asking the same predicate
admission asks. The item's description carries the grant, the marker is matched
case-insensitively (`internal/protectedpath/protectedpath.go:149`), and
`.claude/settings.json` is in `ProviderPaths` — yet the run started and spent an
attempt against the wall the gate exists to prevent. Whether the gate is
unreachable on this entry path or the harness binary that started this run
predates it is **not determined here**; neither question can be answered without
Git or a shell, and both were unavailable. It is reported for somebody who has
them.

## Limitations of this reading

**Every shell invocation from this run fails**, including `true`, with:

> Could not start /bin/zsh: the command line plus environment exceed the OS exec
> argument limit (E2BIG) … The Bash sandbox profile adds 398 filesystem deny
> paths to every command, 231 of them for registered git worktrees, which grow
> this list without bound.

So `bd`, `git`, `go`, and `make` were all unreachable **from this agent's own
Bash tool**, and everything above rests on reading files rather than running
anything.

**That is not the same as the run having no checks, and the distinction matters
because an earlier draft of this document got it wrong.** The configured checks
are run by the harness in its own subprocess, not through the agent's sandboxed
Bash, so they are unaffected by this wall: ifd.180's previous run recorded
`make fmtcheck`, `make test`, `make race`, and `make vet` all passing while that
run's Bash tool was as dead as this one's. An earlier version of this section
claimed `make` was unavailable and used that claim to justify deferring both the
ifd.149 correction and the closure question. **The claim was false and only one of
those two deferrals survives it** — the tracker one, below, which fails for a
reason the checks passing does not touch.

Concretely, what this run could not do:

- **Verify the forensic half.** The commits, the branch, the `merge-base`
  results, and the session transcript are all second-hand, as marked above.
- **Read or write the tracker.** `bd` is reachable only through the shell.
- **Run the checks itself.** The harness runs them; this change is one new
  Markdown file and touches no Go, so `fmtcheck`, `test`, `race`, and `vet` are
  unaffected by it either way.

## Escalations

Two things this work item asks for cannot be done by any run, and are named here
rather than left as gaps inside a document.

1. **Wire the guard into `.claude/settings.json`.** Blocked by Claude Code, above
   the harness and above the grant, as evidenced above. **A person adds the
   stanza by hand**; `CLAUDE.md:30-36` states it verbatim. Re-running this item
   or rewording its grant buys the same refusal again.
2. **Apply the correction to ifd.149's record.** Blocked by the `E2BIG` wall:
   the tracker is reachable only through a shell this run cannot spawn. It must
   be applied with `--append-notes` — never `--notes`, which is the writer this
   whole item is about — from a session with a working shell:

   ```
   bd update yoyodyne-ifd.149 --append-notes="Closed in error: the guard this
   item reported delivering was implemented but never verified (the run ran no
   checks, its shell dead on E2BIG) and never integrated -- its commits
   f5ec276, 93fff46 and 70c8467 exist only on the preserved run branch
   yoyodyne/yoyodyne-ifd-149/e0a6b4e0 and were never ancestors of main. The
   guard was landed for real later; see yoyodyne-ifd.180 and
   docs/diagnoses/yoyodyne-ifd-180-guard-absent-from-tree.md."
   ```

Until the first is done, the guard fires in one population of two, and the
repository's own protection against the writer that destroyed eighteen
attributions rests on the harness-run half plus the witness.

## Work discovered, for the product manager to admit

- **The `E2BIG` wall still ends every shell in a developer run**, and it is what
  made this item necessary. It is a property of the machine's accumulated
  worktree registrations rather than of any run: the sandbox profile carries 231
  deny paths for registered worktrees and grows without bound. Pruning is a
  workaround each operator has to remember; nothing in the harness bounds the
  registrations or notices the wall. A run whose shell is dead can still write
  code, report success, and close an item — which is exactly what ifd.149 did.
- **A run started on an item granting a provider-refused path**, despite a gate
  built to refuse exactly that. Either the gate is unreachable on this entry path
  or the running binary predates it; both need a machine with Git and a shell.
  While it stands, the ifd.153 failure it was built to prevent — repair rounds
  spent against a wall no grant lifts — is reachable again.
- **Nothing reconciles a closed item against whether its work is in the tree.**
  ifd.149 closed as done against commits that never left their run branch, and no
  sweep noticed; it took a later item's developer reading the tree by hand. The
  closure path checks that a promotion happened, not that this item's work is in
  what was promoted.
