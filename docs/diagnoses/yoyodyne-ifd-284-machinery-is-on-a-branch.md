# yoyodyne-ifd.284: the design landed, the machinery is on a branch

yoyodyne-ifd.284 asks for the product manager's coherence scan: twice daily the
harness wakes the product manager to look for contradictions and redundancies
across the work, the designs, and the goals; the product manager fixes what is
theirs and elevates what is not; every scan ends in a durable report with the
questions for the operator at the top.

The item names its own precondition in its first sentence — *"the second
recurring-task consumer, shipping after the DM hourly sweep and **inheriting its
machinery**."* The design that machinery implements has landed. The machinery
itself has not: it exists, whole and reviewer-approved, on the branch of
yoyodyne-ifd.283, which is unmerged. This run checked, found the design and no
machinery behind it, and this is the record.

## The design is there

`docs/designs/configurable-workflows.md` carries a revision whose reason begins
*"yoyodyne-ifd.209.21 - recurring tasks recorded in the reserved trigger seat,
yaml selects what and when and never authority"*, and the section
`### Recurring tasks` is what it added. It settles the questions this scan
depends on:

- **Configuration selects what and when, never authority.** A task is a reason
  to invoke an agent, never a wider agent, bound to the agent's validated role
  contract by `configuration-never-grants-authority`.
- **Each run produces a durable dated report**, and anything the task decides to
  do is carried out through the role's existing typed action paths rather than a
  new mutation path.
- **One instance per task per window, deduplicated durably**, skipping rather
  than queueing when a turn is already in flight on the agent's conversation.
- **The two first consumers are named in it** — the development manager's hourly
  sweep and this scan — so the design was written to serve both.

So nothing this item needs is undesigned. What it needs is the code.

## The machinery is not — it is on ifd.283's branch

These are the commands, written so they can be copied and run.

```sh
# 1. does any Go code on this base declare the recurring-task schema?
grep -rnE 'RecurringTask|recurring_tasks' --include='*.go' internal cmd

# 2. is the trigger, its durable cadence, or its report channel here?
grep -rniE 'SweepStore|yoyodyne-sweep|RecurringClaims|ScheduleRecurring' \
  --include='*.go' internal cmd

# 3. does the word appear in non-test Go at all, under any spelling?
grep -rn --include='*.go' -i recurr internal cmd | grep -v '_test.go'

# 4. does the report package exist?
ls internal/sweep

# 5. did ifd.283 land on main?
git log --oneline main --grep='ifd.283'
git branch --contains origin/yoyodyne/yoyodyne-ifd-283/84b0dcff --list main
```

Run in this worktree on 2026-09-06, at base commit `3852b8f`:

1. **No matches.** Nothing declares the schema, and no configuration key is
   reserved for it.
2. **No matches.** Neither the trigger's interfaces, nor the durable cadence
   claim, nor the fenced channel a pass reports through.
3. **No matches.** The stem search is here because the first two are negatives
   on names somebody chose, and a negative on names is only as good as the guess
   behind it. Not one line of non-test Go on this base uses the word.
4. **No such directory.**
5. **Both empty.** No commit on main mentions the item, and main does not contain
   the branch's tip.

Five negatives in a row is the shape of a search that was quoted wrong rather
than a repository that is empty — which is how the ifd.298 diagnosis reached a
false conclusion and had it caught in review. So the first pattern was run once
more against the branch that does have the code:

```sh
git show origin/yoyodyne/yoyodyne-ifd-283/84b0dcff:internal/config/recurring.go \
  | grep -cE 'RecurringTask'
```

It answers **20**. The pattern matches; this base is what has nothing in it.

The machinery is real and it is elsewhere. `origin/yoyodyne/yoyodyne-ifd-283/84b0dcff`
carries 4,095 lines across 22 files: the strict `recurring_tasks` schema in
`internal/config`, the `Trigger` wired into the existing pull in
`internal/orchestrator`, the durable `SweepStore` in `internal/runstate`, the
`internal/sweep` fenced-report contract, and `yoyo sweeps` for reading the pile.
That change was reviewed and approved. It has not integrated: the item's record
shows it stopped in the `integrating` phase because its target branch moved away
from its recorded base commit, and the tracker export this run was given still
shows `yoyodyne-ifd.283` `open`.

## Why nothing was written against it

**Every field this scan needs is a type that does not exist here.** The scan is
a `config.RecurringTask` entry, its report is a `sweep.Result`, its cadence is a
`runstate.SweepClaim`, and its firing is an `orchestrator.Trigger`. Written on
this base, none of it compiles.

**Writing it again would be writing it twice.** The alternative to depending on
those types is declaring them, which is re-authoring an approved change of four
thousand lines so that two versions of one schema arrive at main from two
branches. The second one to integrate would be a conflict across every file
rather than a contribution, and the reviewer would be re-reviewing ifd.283 under
this item's number.

**Carrying the branch in would be integrating it.** Cherry-picking ifd.283's
commits onto this branch would put someone else's approved-but-unintegrated work
inside this change, which is the harness's promotion to make and not a
developer's.

## What this item actually adds, once the machinery is here

This is the part worth keeping, so the run that picks the item up after ifd.283
lands does not work it out again. Every piece named here was read on
`origin/yoyodyne/yoyodyne-ifd-283/84b0dcff`, and the finding is that the item is
much smaller than its description suggests — most of what it asks for is already
built, generically, by the sibling.

**Already inherited, needing nothing:**

- **Questions on top.** `sweep.Result` already carries `Questions []string`
  separate from the findings, with the item's own rationale written into it: *"a
  report with no questions needs no attention."* `yoyo sweeps` already leads each
  pass with them.
- **Findings checked against admitted work before filing.** `wakeMessage` in
  `internal/orchestrator/recurring.go` adds that instruction to every task's
  configured prompt, deliberately outside the prompt so a project cannot edit it
  away. This item's constraint is a property of the mechanism, not of its
  configuration.
- **Heavy scans iterate turns.** `Trigger.run` loops to the task's `max_turns` on
  a `more` status and marks the pass truncated when the bound rather than the
  work ended it.
- **Durable reports.** `runstate.SweepStore` appends one record per firing and
  `yoyo sweeps` reads them, with `--limit 0` for the week's worth the item's Done
  clause asks about.
- **The persona requirement, structurally.** `roleConversation.Wake` in
  `internal/cli/recurring.go` opens the role's conversation through the same
  `openChat` an operator uses and sends one message through `session.Send`;
  `chat.go` sets `Persona: agent.Persona.Text` on that path. A scheduled turn
  therefore reads `.yoyodyne/personas/product-manager.md`, Defending-the-goals
  and all, because it *is* a conversational turn.

**What is left for this item, and it is three things:**

1. **The configured task.** A `product-manager-coherence` entry at a twelve-hour
   cadence, with the prompt naming the classes the item names — duplicate
   admissions, items whose premises later work disproved, stale sequencing notes,
   attributions that no longer resolve, designs and defaults pointing opposite
   ways — and the fix-versus-elevate boundary stated in the prompt rather than
   assumed: the backlog, its order, attributions and notes are the product
   manager's to fix; a design contradiction goes to the architect and a goal
   change goes to the operator. `renderScaffoldRecurring` in
   `internal/config/scaffold.go` is where ifd.283 put the development manager's
   example, and this is its sibling. The scaffold is Go and editable; a live
   `.yoyodyne/config.yaml` is a protected path and needs a grant on the item.
2. **A test that pins the persona requirement.** The operator stated it
   explicitly, so it should fail if someone later routes a firing around
   `openChat` — a test asserting that a recurring firing for the product manager
   resolves the same persona text a hand-opened conversation resolves. Today the
   property holds by construction and nothing would notice if it stopped.
3. **The Defending-the-goals clause reaching a scheduled turn.** The persona
   governs the turn, but the wake message never mentions goals. A scan that finds
   a *directive* conflicting with a recorded goal has to name it, recommend a
   resolution, and ask the one settling question — and the natural home for that
   instruction is this task's configured prompt, since it is this task's subject
   matter rather than every recurring task's.

**One thing to look at rather than assume.** The design settles schedules as
*operator-local policy, per settled question 16*, alongside account pools;
ifd.283 implemented cadence and `enabled` as project configuration under
`recurring_tasks` in the ordinary layered config. The layering may already make
that operator-local in practice, but the run that adds a second consumer should
confirm which layer this scan's twelve-hour cadence is meant to live in rather
than copying the sibling's placement without asking.

## What would release this item

1. **yoyodyne-ifd.283 lands.** Its change is approved and its checks passed; what
   stopped it is that main moved out from under its recorded base commit, so what
   it needs is re-integration rather than more development. Nothing else blocks
   this item, and nothing in it can be started before that.
2. **ifd.284 is released**, and the three things above are written against the
   machinery that then exists.
