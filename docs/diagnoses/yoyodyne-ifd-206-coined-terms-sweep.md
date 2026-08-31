# Sweep: coined terms in open tracker items and governed documents

Work item: yoyodyne-ifd.206. **This document is the audit half of that item, and
the item is not done.** No coinage has been replaced anywhere. The sweep has
been run and its result written down here, but neither of the two places it
applies to was writable by the run that produced it, for the reasons in
[What could not be applied](#what-could-not-be-applied). Both reasons are
boundaries working as designed, and neither is a developer's to lift.

What has to happen before ifd.206 can be closed is listed in
[What remains](#what-remains). Anyone reading this as evidence that the backlog
and the governed documents have been swept should stop there: they have been
measured, not changed.

Records read at 2026-08-30, against the live tracker export
(`/Users/mbryant/github/yoyodyne/.beads/issues.jsonl`, 309 rows, latest
`updated_at` 2026-08-31T03:54:33Z) and the working tree of
`yoyodyne/yoyodyne-ifd-206/0d039d51`. The tracker scan covers the **78 items
that are not closed** — 35 open, 32 blocked, 11 in progress — over their titles
and descriptions only. Notes are excluded deliberately: they are where the
harness appends each run's own record, so they are machine output rather than
authored operator-facing prose, and rewriting them would destroy attribution
(see `yoyodyne-ifd-122-goal-attribution-loss.md`). The document scan covers
every Markdown file under `docs/product`, `docs/designs`, and `docs/decisions`,
including `docs/decisions/invariants`.

The rule being applied is the legibility goal's plain-language clause
(`docs/product/goals/v1-goals.md:108`): *user-facing language chooses the
ordinary, literal word over metaphor, coinage, or term of art.*

## Summary

The governed documents are close to clean and the tracker is not. Across the
three artifact homes the sweep found **32 occurrences of 8 terms**, of which 18
are one mechanism name (`sink`) and the rest are 14 scattered occurrences. Across
the 78 open items it found **120 occurrences of 19 terms**. Titles matter most,
because a title is what the Slack channel, `yoyo status`, and the backlog
listing all show: **21 of the 78 open titles carry a coinage**.

Splitting them the way the operator directed:

- **Pure decoration — replace outright.** 13 terms, 54 occurrences in open
  items, 12 in governed documents. Every one names nothing that exists in code
  or in command output; each has an ordinary word that says the same thing.
  Listed with replacements in [Category 1](#category-1-decoration-to-be-replaced-outright).
- **Mechanism names — the architect rules.** 6 terms, 66 occurrences in open
  items, 20 in governed documents, and between them **2,361 occurrences across
  the Go source** and a large amount of command output. Each names a real thing
  a reader can point at, and each is why the item reserves this half for the
  architect: a rename crosses her interfaces. Listed with the evidence the
  ruling needs in
  [Category 2](#category-2-mechanism-names-where-the-architects-ruling-was-requested).

Two of the three terms the item names as pure decoration — *doorbell*, *front
door*, *courier-as-a-name* — appear nowhere in the tracker or the governed
documents except inside yoyodyne-ifd.206's own description, where they are cited
as examples. They were swept for and are absent. The illustration was of the
category, not of a live backlog.

## Category 1: decoration, to be replaced outright

Each row is the term, where it occurs, and the ordinary wording that replaces
it. The replacements are stated as wording rather than as a global substitution:
several of these terms carry a different meaning in different sentences, and a
find-and-replace over them would produce prose that is literal and wrong.

| Term | Open items | Governed docs | Replace with |
|---|---|---|---|
| `tranche` | 18 — ifd.117.1, .117.2, .117.3, .117.4, 121.5, 121.6 | — | **stage**, or **part 1 of 4**. In titles: `configuration.md split tranche 1` → `configuration.md split, part 1 of 4`. |
| `seam` | 8 — ifd.100, 130, 130.1, 130.2, 139 | 2 — `claude-only-v1-execution.md:27`, `claude-execution-and-account-routing.md:19` | Name the thing instead. `closing the copy-paste seam` → `so the operator's agent no longer copies artifacts to disk by hand`. `the conversation-hold seam` → `the way a console and the harness each reach the product manager`. In the decision record, `the seam a second connector re-enters through` → `the boundary a second connector attaches at`. |
| `posture` | 9 — ifd.202 (7), 207, 209 | 2 — `v1-harness-design.md:209`, `harness-is-the-only-role-invoker.md:20` | **tool permissions**, or **which tools a role may use**. `read-only tool posture` → `read-only tool permissions`. `the platform posture` (ifd.207) → `what each platform supports`. `relief-first posture` (ifd.209) → `relief first`. |
| `re-arm` | 5 — ifd.102.7, 68.15 | 4 — `v1-harness-design.md:32, :463` | **repeat the merge request**. `Re-arm a dropped queued merge` → `Repeat a merge request the forge dropped`. `a second drop is never re-armed` → `a second drop is never repeated`. |
| `sidecar` | 2 — ifd.78 | — | **a separate directory outside the repository**. Title: `the traceability chain in a sidecar` → `the traceability chain outside the managed repository` — which the title's own second half already says, so the coinage is redundant as well as opaque. |
| `wedged` | 2 — ifd.102.7, 152 | 1 — `v1-harness-design.md:463` | **stuck**, or say the condition: `a wedged required check` → `a required check that never finished`. `a rejected push wedges the next cut` → `a rejected push stops the next release until someone reconciles by hand`. |
| `in force` | 2 — ifd.68.23 | 3 — `goals/README.md:55`, `invariants/README.md:10`, `claude-execution-and-account-routing.md:31` | **active now**, or **still applies**. `every goal in force` → `every active goal`. `leaves it in force` → `leaves it active`. `the last valid revision stays in force` → `the last valid revision stays active`. The operator has named this one directly; see the note below. |
| `minute zero` | 2 — ifd.184 | — | **before development begins** — the phrase the same description already uses one clause earlier, so the coinage adds nothing. `refused loudly at minute zero` → `refused loudly before development begins`. |
| `soak` | 2 — ifd.209, 211 | — | **trial run**, or **a run kept alongside the old one for comparison**. `opt-in parity soak` → `an opt-in trial run compared against the old path`. |
| `one pane of glass` | 1 — ifd.130 (title) | — | **one window**. Title: `One pane of glass: yoyo chat is the entry point` → `One window: yoyo chat is the entry point`. |
| `starving` | 1 — ifd.68.7 (title) | — | **stopping**. Title: `tolerates additive schema changes instead of starving on them` → `tolerates additive schema changes instead of stopping on them`. |
| `whose-move` | 1 — ifd.187 | — | **waiting on the operator**. `whose-move the operator's` → `and it is waiting on the operator`. |
| `supersession pile` | 1 — ifd.121.6 | — | **the list of superseded pull requests**. |

Two of these are not new judgments by this sweep. The operator has already
objected to `in force` in a thread — recorded on ifd.68.23, where he asked *What
does 'in force from now' mean?* and the reply path recorded his question as a
directive; that item carries his general rule that a receipt must not explain a
term by restating it. He has separately asked for `waiting on you` in place of
`whose-move`. Both terms are still in the records.

### Not swept, and why

`papering over`, `eats every context budget`, `carved from`, `front the queue`
and similar are figures of speech rather than coined terms. The clause names
metaphor as well as coinage, so they are within the goal — but they are outside
this item's stated scope, which is *decorative coined terms*, and a sweep that
grew to every metaphor in 78 items would be a different and much larger change
than the one admitted. They are named here so the next reader knows they were
seen and left, not missed.

## Category 2: mechanism names, where the architect's ruling was requested

Each of these names something that exists. Per the item, each is either renamed
end to end or defined in plain words at first use in each document, and the
choice is the architect's because a rename crosses her interfaces. The evidence
below is what that ruling needs: how far the name reaches, and what the plain
words would be.

**The ruling has been requested, not merely awaited.** The run that produced
this document filed amendment proposals against `v1-harness-design` (for
`docket`, `brake`, `heartbeat`, `steer` and `handback`) and against
`slack-reporting-design` (for `sink`), which is the harness's persisted path for
putting a change to the role that owns a document and having it wait on that
role's decision. Until those are decided, no term below has a ruling and none
should be treated as settled either way.

The other channel that might look right for this is not available here. The
inter-role ask (`internal/exchange`) is wired only into `internal/chat`:
`exchange.AskingContract` is delivered from `internal/chat/role.go` and nothing
in `internal/orchestrator` references the package, so a developer run cannot
open one. It would also be the wrong instrument if it could — an ask is
decisionless by construction, and this needs a decision rather than an opinion.
A ruling that has to bind a rename across 2,361 occurrences lands as an
amendment or it does not land.

### `docket`

- Open items: 2. Governed documents: 1 (`management-and-supervision.md:35`).
- Code: 928 occurrences across 49 files, including the type name
  `runstate.DocketStore` and the file `internal/runstate/docket.go`.
- Command output: extensive and operator-facing. `yoyo reconcile` prints
  `%d stopped item(s) added to the triage docket for the development manager`;
  `yoyo triage` prints `the run the docket entry names` and `no stopped run of
  %s is on the triage docket`; the product manager's context bundle carries a
  `## Triage docket` heading and `The triage docket could not be read`.
- Plain words: **the list of stopped runs waiting on the development manager**.
  A rename would be the largest of the six and would cross the runstate store's
  on-disk layout as well as the CLI. Defining it at first use is cheap; the
  cost of keeping it is that `docket` is a legal term of art whose ordinary
  meaning — a court's schedule of cases — is close enough to mislead.

### `sink`

- Open items: 29. Governed documents: 18, nearly all in
  `slack-reporting-design.md`, where decision 2 is stated as *The sink is one
  separate, long-running process*.
- Code: 912 occurrences across 61 files, including `internal/slack/sink.go` and
  the `yoyo slack` command.
- Command output: heavy and operator-facing, concentrated in `yoyo doctor`,
  which prints twenty-odd distinct lines naming it — `no sink is running for
  this product, so nothing is being reported`, `the running sink posts into %s`,
  `another Slack sink is already running for this product`.
- Plain words: **the Slack reporter**, or **the process that posts to Slack**.
  This is the strongest rename candidate of the six: it is a term of art
  borrowed from dataflow programming, it has no ordinary-English meaning that
  helps a reader, and every one of the `yoyo doctor` lines reads correctly with
  `reporter` substituted.

### `brake`

- Open items: 12. Governed documents: 0.
- Code: 96 occurrences across 16 files.
- Command output: `internal/notify/voice.go:448` says *Selection is stopped by
  the intake hold, which is the brake working rather than failing*;
  `internal/orchestrator/schedule.go:944` says *which is the configured brake at
  %d*.
- Plain words: **the automatic stop after a set number of blocked runs in a
  row**. Note that the voice line above already pairs the coinage with the plain
  name of the thing (`the intake hold`), which is evidence the coinage is
  carrying no meaning the sentence needs.

### `heartbeat`

- Open items: 11. Governed documents: 0.
- Code: 63 occurrences across 8 files.
- Command output: the `yoyo slack --heartbeat` flag, whose help reads *how often
  to say again that the line is choosing nothing over ready work*, and the
  refusal *heartbeat must be positive; it is a cadence rather than a switch*.
- Plain words: **how often to repeat**. This is the weakest rename candidate:
  `heartbeat` is near-universal for a periodic liveness signal, and the flag's
  help text already defines it in plain words at its only point of use. If any
  of the six is kept as-is, this is it. (`cadence`, in the same refusal, is a
  Category 1 coinage on its own and should become *how often it repeats*.)

### `steer`

- Open items: 7. Governed documents: 1 (`slack-reporting-design.md:27`).
- Code: 296 occurrences across 36 files, including `internal/chat/steer.go` and
  `internal/slack/state.go`.
- Command output: `replies in these threads steer the work`, `it can discuss
  work but cannot see or steer it`, and the `yoyo chat` help *You steer the work
  yourself*.
- Plain words: **direct**, or **change what is being worked on**. Borderline:
  `steer` is an ordinary English verb and reads naturally in every one of those
  sentences. Recorded here for completeness rather than as a live problem.

### `handback`

- Open items: 5 — ifd.195, ifd.68.22. Governed documents: 0.
- Code: 66 occurrences across 9 files, of which three are not tests —
  `internal/runstate/environmental.go`, `internal/orchestrator/pipeline.go`,
  `internal/orchestrator/repaircontinue.go`.
- Command output: **none found**. This name reaches operators only through
  tracker item text, which makes it a Category 1 term for the prose and an
  internal identifier everywhere else. `invokes the handback path` →
  **hands the work back to the developer that made it**.

## What could not be applied

Neither half of the item's scope is writable by a developer run, for two
separate reasons. Both are boundaries working as designed, not faults.

**The governed documents are protected paths, and this item grants none.** The
homes the sweep covers — `docs/product`, `docs/designs`, `docs/decisions`, and
`docs/decisions/invariants` — are exactly the set `internal/protectedpath`
refuses in a developer's diff, and yoyodyne-ifd.206 carries no
`protected-path grant:` line in its title or description, and has no design or
acceptance-criteria field at all. The 12 governed-document occurrences in
Category 1 are therefore proposals to the artifact owners rather than edits, and
are listed above in applicable form.

**The tracker is outside the run's writable sandbox.** The Beads Dolt database
lives at `/Users/mbryant/github/yoyodyne/.beads/embeddeddolt`, in the primary
checkout rather than in the worktree, and the developer sandbox grants writes to
the worktree and to `.git` only. Every `bd` invocation — including read-only
ones such as `bd show`, which still opens the database read-write — fails with
`openat LOCK: operation not permitted`. The 54 Category 1 occurrences in open
items are therefore listed above rather than applied.

## What remains

ifd.206 is not done and should not be closed on the strength of this document.
Measured against the item's own four done conditions:

| Done condition | State |
|---|---|
| The sweep has run over open items and governed documents | **Met.** Both scans are complete and recorded here. |
| Decorative coinages are gone | **Not met.** All 66 Category 1 occurrences — 54 in open items, 12 in governed documents — are still in the records. |
| Every surviving mechanism term has an architect ruling, and a plain-words first-use definition where kept | **Not met.** The ruling is requested and undecided; no definition can be written until it is. |
| The item lists what was changed | **Not met.** Nothing was changed, and the tracker item could not be updated to say so. |

Three things have to be arranged by roles other than a developer, and none of
them is a judgment call this document is trying to pre-empt:

1. **A grant for the governed-document half.** ifd.206 needs
   `protected-path grant:` lines admitting `docs/product`, `docs/designs` and
   `docs/decisions`, written into the item's title, description, design
   guidance, or acceptance criteria — the fields somebody authored. A grant in
   the notes does not count, and nothing a developer writes grants a path.
2. **A writable tracker for the item half.** The run needs its sandbox widened
   to the primary checkout's `.beads` directory, or the tracker edits have to be
   made by something that already reaches it.
3. **The architect's ruling on the six mechanism terms**, requested by the two
   amendment proposals named in [Category 2](#category-2-mechanism-names-where-the-architects-ruling-was-requested).
   The Category 1 work does not depend on it and can proceed first.

The first two are independent of each other, so the item can be finished in two
runs rather than one if that is easier to arrange than a single run holding
both.

## How this was measured

Every count in this document comes from one of the three scans below, run on
2026-08-30 from the worktree root. They are recorded so the architect can
reproduce or spot-check them before deciding a rename, rather than taking the
numbers on trust.

**Tracker scan** — the 78 not-closed items, titles and descriptions only,
counting case-insensitive substring matches per term:

```bash
python3 - <<'PY'
import json, re, collections
rows = [json.loads(l) for l in open('/Users/mbryant/github/yoyodyne/.beads/issues.jsonl')]
open_items = [r for r in rows if r['status'] != 'closed']
cat1 = ["tranche","seam","posture","re-arm","sidecar","wedge","in force",
        "minute zero","soak","pane of glass","starv","whose-move","supersession pile"]
cat2 = ["docket","brake","heartbeat","sink","steer","handback"]
for name, terms in (("CAT1", cat1), ("CAT2", cat2)):
    total = collections.Counter()
    for r in open_items:
        text = ((r.get('title') or '') + "\n" + (r.get('description') or '')).lower()
        for t in terms:
            n = len(re.findall(re.escape(t), text))
            if n: total[t] += n
    print(name, sum(total.values()), len(total), dict(total))
# titles carrying any coinage
print(sum(1 for r in open_items
          if any(re.search(re.escape(t), r['title'], re.I) for t in cat1 + cat2)))
PY
```

**Governed-document scan** — the same two term lists over every Markdown file
under the three artifact homes. This is what produced the headline 32
occurrences of 8 terms: 12 of 5 in Category 1, 20 of 3 in Category 2.

```bash
python3 - <<'PY'
import os, re, collections
cat1 = ["tranche","seam","posture","re-arm","sidecar","wedge","in force",
        "minute zero","soak","pane of glass","starv","whose-move","supersession pile"]
cat2 = ["docket","brake","heartbeat","sink","steer","handback"]
for name, terms in (("CAT1", cat1), ("CAT2", cat2)):
    g = collections.Counter()
    for root in ['docs/product', 'docs/designs', 'docs/decisions']:
        for dp, _, fns in os.walk(root):
            for fn in fns:
                if not fn.endswith('.md'): continue
                txt = open(os.path.join(dp, fn)).read().lower()
                for t in terms:
                    n = len(re.findall(re.escape(t), txt))
                    if n: g[t] += n
    print(name, sum(g.values()), len(g), dict(g))
PY
```

**Code scan** — word-start matches over every `.go` file in the repository,
excluding `.git` and `.beads`. This is what produced 928 across 49 files for
`docket`, 912 across 61 for `sink`, 296 across 36 for `steer`, 96 across 16 for
`brake`, 66 across 9 for `handback`, 63 across 8 for `heartbeat`, and 2,361 in
total:

```bash
python3 -c "
import os, re, collections
terms = ['docket','brake','heartbeat','sink','steer','handback']
tot, files = collections.Counter(), collections.Counter()
for dp, _, fns in os.walk('.'):
    if any(x in dp for x in ('/.git','/.beads','node_modules')): continue
    for fn in fns:
        if not fn.endswith('.go'): continue
        txt = open(os.path.join(dp, fn)).read().lower()
        for t in terms:
            n = len(re.findall(r'\b' + t, txt))
            if n: tot[t] += n; files[t] += 1
print(dict(tot), sum(tot.values()), dict(files))"
```

Two caveats a spot-check will otherwise trip over. The code scan matches word
starts, so `docket` counts `docketed` and `DocketStore`, and `brake` counts
`braked` and `brakes`; this is deliberate, because a rename has to move those
too. And `ripgrep`'s count mode reports matching *lines* rather than
occurrences, so counting the same terms with `rg -c` gives slightly lower
figures — 879 for `docket`, 110 for `brake` — against multiple matches on one
line. The figures above are occurrences.

The operator-facing strings quoted in Category 2 were found by extracting Go
string literals of 25 characters or more containing a space, then matching the
six terms against them, which is what separates command output from identifiers.
