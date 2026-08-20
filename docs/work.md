# How work flows once you approve it

`/work <beads-id>` and `yoyo run <beads-id>` execute the same thing. The run
claims the item, creates a branch and an isolated worktree outside your primary
checkout from exactly the branch the work will be promoted into, and asks the
developer for the change:

```sh
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit,
change summary, checks, and agent summary.

Then the change is gated on what it touched. The project configuration and the
artifact homes upstream of the work — `.yoyodyne/`, `docs/product/`,
`docs/designs/`, and `docs/decisions/` by default — are default-deny for a
developer's diff, because a developer that edits one is redefining what its own
work is measured against. A change that touches one without the work item
granting it is refused before any check runs and before any reviewer is asked,
and handed back to the same developer in the same repair loop a failing check
uses. An item grants an exception in its own text, on a line beginning
`Protected-path grant:`, so every exception is declared in reviewed item text
rather than discovered in a diff. A grant admits the path and decides nothing
about what goes into it, so the reviewer is told to read the item for the decided
change behind each grant and to raise a finding when none is named.
[Configuration](configuration.md#protected-paths-in-a-developers-change)
has the details.

Then the configured checks run in that worktree, and an independent reviewer —
its own provider invocation, with no tools at all — judges the change against
the work item, its design guidance and acceptance criteria, the invariants
delivered with it, and the check results. Everything the reviewer is shown is
treated as evidence rather than instruction, so an instruction the developer
left in the diff is data to analyze rather than something to follow. A verdict
of `repair` returns the findings to the same developer, up to
`execution.repair_attempts_before_replan` attempts, before the run gives up and
records a blocker.

What happens on approval depends on `approvals.integration`. This repository
sets it to `automatic`, so a run that passes its checks and is approved by the
reviewer is committed, fast-forwarded into the target branch, closed in Beads,
and its worktree and branch removed — the JSON reports the integrated commit and
what was cleaned up. A freshly generated configuration says `human` instead, so
a new project preserves the worktree for external integration until it opts in.
Either way the harness refuses `automatic` unless deterministic checks and a
reviewer agent both exist.

Development is parallel and integration is serial. Two runs may develop, check,
and review at the same time, but a run reaching its promotion phase waits its
turn: the harness takes a lease on the target branch out of the run state store
before it promotes and releases it once the promotion has settled, so at most
one promotion per target branch is ever in flight. The lease is an advisory file
lock, so it dies with the process holding it — a promotion whose process was
killed leaves no stale lock, and `yoyo reconcile` settles what it left behind.
No agent takes the lease or performs a promotion; the harness does both.

A fast-forward needs the target branch to still be where the run started from,
and it may not be: the run ahead of it in the queue can have promoted into it,
and committing to it yourself while a run is working moves it just as
effectively. The promotion fails closed either way, and the run then replays its
change onto where the target went, re-runs the checks, and gets a fresh
independent review before trying again — up to
`execution.integration_retries_before_reconciliation` times. The earlier
approval never carries over, because the diff it approved is not the one that
would now be promoted. A replay that conflicts is never
resolved automatically: the run stops, both sides survive untouched, and the
blocker on the item says so.

## Letting the harness choose the work

`/work <id>` and `yoyo run <id>` are you naming an item. `yoyo work` is the
harness choosing:

```sh
./bin/yoyo work                 # drain what is ready
./bin/yoyo work --limit 2       # start two runs and stop choosing
./bin/yoyo work --watch         # stay open, pulling work as it becomes ready
./bin/yoyo work --json
```

It reads the admitted work in the order the product manager set, takes the items
the tracker itself reports as ready to pull, and starts as many of them at once
as `execution.max_concurrent_developers` leaves free — which is `1` until you
raise it. Each run is the run above: its own branch, its own worktree, the same
checks, the same independent reviewer, the same serial promotion. The command
returns once every run it started has ended.

Capacity is enforced where a run is reserved rather than by the scheduler, so two
of these, or one of these and a `yoyo run` beside it, share one limit rather than
getting one each. A run that loses the race for the last free slot is reported as
declined and the pass exits zero: that is two schedulers doing exactly what they
should, not a failure.

Four things keep an item out of a pass, and the pass accounts for them at two
different grains. An **unresolved directive** is named against the item it paused,
with the directive's own words, because it needs a person and nothing else would
report that this item was passed over for it. The other three — the tracker not
reporting an item as ready, a run for it already being in flight anywhere, and no
free slot — are facts about the pass rather than about any one item, so that is
how they are reported: the stop reason says which of them ended the choosing, and
a pass that got as far as reading the queue prints how many items were admitted,
how many the tracker called ready to pull, and how many slots were taken. Counts
rather than a list, deliberately — a line per unready item would be a line per
backlog entry on every pass, which is how a listing stops being read. A pass that
stopped before reading the queue at all, because you were holding intake or the
machine was already full, says nothing about the backlog rather than reporting
zeroes it never looked up.

A fifth thing deliberately keeps nothing out: an item whose goal was amended after
it was admitted is pulled exactly as it would have been, because
[staleness reports rather than decides](artifacts.md#what-a-change-upstream-leaves-stale),
and what changed goes into the run's recorded reason instead.

Two things make this accountable rather than work happening behind your back.
Holding intake stops it choosing anything more while what is running finishes,
and it is read at every pull rather than once at the start, so a hold you place
mid-pass takes effect at the next selection. And every run it starts records, in
durable state, why that item was chosen — where it sat in the order, how much of
the queue was pullable, how much of the machine was free, and anything upstream
that had moved. `yoyo status` reads it back.

The configuration is re-read before every pull for the same reason: a capacity
you raise or a priority you reorder while a pass is running is picked up the next
time it chooses something, rather than at the next restart. Runs already in
flight keep the configuration they started under.
[Configuration](configuration.md#scheduling-ready-work) has the rest.

**`--watch` keeps it open.** Instead of returning when the queue empties, it
waits `execution.work_poll` — a minute by default — and reads the queue again,
until you stop it. Nothing else about the pass changes and nothing needed to:
every pull already re-reads the configuration and the queue, so work you admit is
picked up at the next poll and a reprioritization at the next pull, with no change
detection anywhere in it. An idle session costs one local tracker read per
interval and asks no provider anything. Holding intake brakes a watching session
in place rather than stopping it — it keeps polling, chooses nothing, and resumes
when you release it.

Three things guard a loop that no longer ends. A session does not start the same
item twice unless the item has changed — what it says, what it is for, its
priority, its status, what it depends on, its notes — so a start the harness
cannot get past is not retried every minute forever, and a blocker you release is
picked up because releasing it changed the item. Runs blocking one after another
with nothing landing between them hold intake at
`execution.blocked_runs_before_intake_hold`, so a broken machine cannot put the
whole backlog through a failed run overnight. And the session records what it is
doing — watching, idle, braked, resumed, stopped — where `yoyo status` and the
Slack sink read it, because an idle session and a dead one are otherwise the same
silence.

`--budget <usd>` caps what one session spends, and fails closed: a pass that
cannot price itself is refused before it starts, and a session that meets a run
whose evidence will not price stops and names it rather than counting it as free.

The default is still the drain, and `--until-drained` says so out loud. What
changes when you watch is what bounds the spend: a drain is bounded by the queue
emptying, and a watching session is bounded by what you admit to it.

Documentation counts as part of a work item rather than as follow-up: the
developer contract makes updating the documents that describe changed behavior
part of the assigned work, and the reviewer reports a change that leaves a
document asserting something the change has made false. That reconciliation is
diff-scoped, and the limit is worth stating plainly — the reviewer is given one
change, not the repository, so it catches a contradiction with documentation it
can see and misses a claim invalidated in a file the change never touches. What
that misses across a whole branch is what [`yoyo review`](#reviewing-what-a-branch-adds-up-to)
is for; nothing in the harness compares the accumulated documentation against
the repository as a whole.

## Reviewing what a branch adds up to

A per-item review sees exactly one work item's worktree, so a defect that is
consistent inside every change that produced it and wrong only in their sum is
structurally invisible to it. `yoyo review` is the same reviewer — the same
contract, the same structured verdict, the same independence — pointed at a
branch against the base it grew from:

```sh
./bin/yoyo review --base main                 # the branch you are on
./bin/yoyo review --base main --branch milestone --json
```

It describes every commit the branch carries over that base and diffs the whole
range as one patch, under the same bounds a single change is described within: a
range too large to show in full is reported as truncated, and a truncated change
cannot be approved, because what was not shown was not reviewed. The base must
be an ancestor of the branch — a base that has moved on is a reconciliation
rather than an accumulated change, and the command says so instead of quietly
reviewing a range you did not name.

The verdict is recorded with the same session and model evidence a per-item
review leaves behind, in the `branch-reviews` directory beside the runs and the
conversations, and what the reviewer noticed beside its verdict is collected
with every other report. It is a provider invocation like any other the harness
makes, so it records the event stream every other one records: it can be
followed while it runs with
[`yoyo-status`](operations.md#following-a-run-a-conversation-or-a-branch-review), and what
the provider reported it cost is priced beside runs and conversations rather
than quietly missing from the harness's total.

What a `repair` verdict here does is deliberate and narrow: **nothing to the work
already integrated.** Every commit under review was checked, reviewed, and
promoted by a run that has since settled, so there is no gate left to hold and
the harness does not revert or reopen a promotion on a second opinion — the
branch review is wired with no run store and no integration, so it could not if
it were asked to. What it does instead is answer one question, and enforce the
answer: the branch is approved only if an independent reviewer approved the whole
accumulated change, and `yoyo review` exits non-zero on anything else — a repair
verdict, a review that never answered, a change too large to be seen in full. The
findings are then work, and admitting work to the backlog is the product
manager's.

## Publishing, and the merge that follows it

Runs are local until a project sets `approvals.publishing` to `automatic`. With
publishing on, the developer phase is what pushes: when a developer attempt
finishes, the harness commits it, pushes the run branch to `execution.remote`,
and opens a pull request against the target branch — and each repair attempt
updates that same request. The approving reviewer verdict is what merges it: the
harness asks the forge to merge the pull request, subject to exactly the checks,
independence evidence, and fast-forward rule that gate integration, plus a fresh
check that the remote target has not moved. The harness makes every push and
every merge request itself and routes neither through an agent: no role is given
a credential, a tool, or a request for either, and the reviewer — the role whose
verdict authorizes the merge — runs with no tools at all, so it cannot perform
one. A developer does have a shell in its worktree and runs under your account,
so "no agent pushes" describes what the harness does rather than a boundary it
enforces; the [design document](designs/v1-harness-design.md#what-is-enforced-and-what-is-not)
says which half is which. The local target branch stays authoritative: the
harness fast-forwards it as it always has, and the forge merges the pull request
carrying exactly that commit under a merge commit — the one method that puts the
reviewed commit itself on the base, where a squash or a rebase would substitute
a rewritten copy. So the merge leaves the remote target at your local branch
plus one forge merge commit, identical in content, and the harness checks that
relationship on both sides of the merge. The last step of the promotion is to
catch your local branch up onto that merge commit: a fast-forward onto a commit
that already contains the promotion and carries exactly its content, so nothing
is rewritten, nothing is merged, and nothing is decided. That is the `git pull`
you used to have to remember after every merge, and it is why your checkout
stays level with the forge on its own. A fast-forward blocked by uncommitted
work in your checkout is held rather than forced, and says which file held it —
except for the control-plane exports the harness declares (Beads' passive JSONL
dumps), whose churn is discarded, because they are derived from a store that is
authoritative elsewhere. A merge that landed after its run was over, and any
catch-up that was held, are swept by
[`yoyo reconcile`](operations.md#recovering-interrupted-runs).
Your target branch itself is never pushed, so a branch protected against
direct pushes is merged into normally; a forge that refuses reports which
requirement was unmet, and a merge that did not carry the promotion is reported
rather than reconciled. The merge is asked for as of when your branch protection
is satisfied rather than as of now, so required checks that are still running
are waited for by the forge rather than refused seconds after the reviewer
approved. Waiting that way needs "Allow auto-merge" enabled on the repository;
when it is off and nothing is holding the pull request back the harness just
merges, so only a repository that has something to wait for and no way to wait
for it is reported as unpublishable, naming the setting. Administrator override
is never used to get past a protection rule. A run whose merge is queued that
way reports the pull request as queued and finishes;
[`yoyo reconcile`](operations.md#recovering-interrupted-runs) settles it once the forge has
merged, or reports an outstanding publication if the forge dropped the queued
merge. A repository with no configured remote publishes nothing and behaves
exactly as a purely local project does.

Merging belongs to `approvals.integration`, so the two settings compose rather
than imply one another. Publishing with `integration: human` opens the pull
request and stops: nothing is merged, the run branch survives on the remote, and
the worktree is preserved for you — which is what a `human` integration policy
means. See the
[configuration guide](configuration.md#publishing-through-pull-requests).
