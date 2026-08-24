# How work flows once you approve it

*For an operator who has approved work and wants to know what happens
to it. Part of [yoyo's documentation](../README.md#further-reading).*

`/work <beads-id>` and `yoyo run <beads-id>` execute the same thing. The run
claims the item, creates a branch and an isolated worktree outside your primary
checkout from exactly the branch the work will be promoted into, and asks the
developer for the change:

```sh
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit,
change summary, checks, and agent summary.

Once the item is claimed the run asks the tracker to write out its passive export
in your primary checkout — `bd export`, one of the two files a run is allowed to
churn there — and tells the developer where that file is, **when it was last
written**, and when the run asked. It has to, because the tracker's own store is
a database the developer's sandbox cannot open, and the copy of the export inside
the isolated worktree is whatever the last commit carrying one held, which can be
days old and can be missing the very item the run is executing.

The age travels with the path because a clean export does not prove freshness:
beads keeps `.beads/issues.jsonl` only where a project turns it on — this
repository does, in `.beads/config.yaml` — and in a project that keeps none,
`bd export` succeeds and writes nothing. A run that finds no dump, or whose
export refused, is told it has no current view at all. Either way the developer
is told to report an answer taken from a stale file as unverified rather than as
a sweep of the tracker.

What the item waits on is read from the tracker at every point the run is about
to commit to work — before it is claimed or resumed, at the start of each round
of the gate, and once more before the promotion — rather than trusted from
whatever readiness selection saw. A dependency link added to an item that is
already in flight therefore takes effect on that run: it pauses at the next of
those boundaries exactly as an unresolved directive does, keeping its claim, its
branch, its worktree, and its developer session, and it carries on when the work
it waits on is closed or the link is removed. That matters because a development
manager linking a dependency onto work already moving is precisely how a gate
gets added late, and a run that answered from selection-time state would develop
straight through the gate filed to stop it — and spend review rounds on a change
that should never have been dispatched. Both the developer's context and the
reviewer's evidence state what the item waits on, and state it as `nothing` when
it waits on nothing, so neither can mistake an item this context happens not to
describe for one nothing blocks.

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
records a blocker. That budget is not always the last word: a development
manager who decides the change is worth another go can hand the item a grant of
further attempts, and [`yoyo triage
repair`](conversation.md#deciding-what-becomes-of-stopped-work) re-enters that
run's repair loop on the change it already has rather than starting the item
over. Its opposite is `yoyo triage rerun`, which starts the item over for a
change whose ground moved. **The two are different acts with different
accounting** — one spends the item's repair grant and the review rounds that
grant buys, the other spends its re-run budget — and neither of them is `yoyo
run <beads-id>`, which is you naming an item rather than carrying out a decision
somebody recorded about a run that stopped. `yoyo run` enforces that difference
rather than relying on it being understood: an item whose last run stopped with
its change preserved on a branch is refused a fresh clean run, naming both the
repair that would continue the change and the re-run that would start over
deliberately, because a fresh worktree off the target branch looks perfectly
valid and a developer given one delivers an empty change or reinvents the work.

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

Seven things keep an item out of a pass, and the pass accounts for them at two
different grains. Four are named against the item, because nothing else would
report that this particular item was passed over. An **unresolved directive** is
named with the directive's own words, because it needs a person. An item
whose **unfinished children already carry its execution** is skipped with those
children named: a decomposed epic and the child that does its work are both
reported as ready to pull, so a scheduler that did not know the difference would
buy the same change twice — two developers rewriting one file, the second of them
guaranteed a conflict at integration. A child covers whether it is queued,
blocked, or already claimed by a run in flight, and the container becomes
ordinary work again once its last unfinished child leaves the backlog. An
item that **would race work already in flight** is sequenced behind it rather
than started beside it, with the run it would have raced and what the two share
both named. And an item whose **executor is a persona conversation** rather than
a developer run is passed over with what carries it named, which the paragraph
after next is about. The other
three — the tracker not reporting an item as ready, a run for it already being in
flight anywhere, and no free slot — are facts about the pass rather than about any
one item, so that is how they are reported: the stop reason says which of them
ended the choosing, and
a pass that got as far as reading the queue prints how many items were admitted,
how many the tracker called ready to pull, and how many slots were taken. Counts
rather than a list, deliberately — a line per unready item would be a line per
backlog entry on every pass, which is how a listing stops being read. A pass that
stopped before reading the queue at all, because you were holding intake or the
machine was already full, says nothing about the backlog rather than reporting
zeroes it never looked up.

Sequencing is the one of those four that is a wait rather than a refusal. Two
items race when they are siblings of one epic, when one is the epic the other was
broken out of, or when the files they will change overlap. An item says which
files those are by naming them after `conflict-surface:` on a line of its own, in
its title, description, design guidance, or acceptance criteria — the fields
somebody authored, not the notes the harness appends each run's record to — and an
item that declares nothing has those same fields read for the files it plainly
names. That inference is deliberately narrow, taking a path with a separator and
an extension on the end and nothing else: a surface invented out of prose holds
unrelated work back, and unrelated work running at once is what the concurrency is
for. Nothing here enforces anything — the promotion lease still serializes
integration, and a change whose target moved is still replayed onto where it went
— and what it buys is the difference between one wait and a replayed, re-checked,
freshly reviewed run, or a stopped one where the replay will not apply. An item is
held for exactly as long as the run it would have raced lasts, because the
conflicts are re-read at every pull from what is actually in flight. And the slot a
hold frees is not idled: the pass carries on down the order to the next item that
races nothing, and both runs record what the sequencing did — the one that waited
says what it waited for, and the one pulled past it says which items it was pulled
ahead of.

Not everything in the backlog is a developer run. Promoting a document the
architect owns, settling a decomposition, recording a decision: those happen in a
conversation with a role, and the harness's own gates already say so — the
artifact homes are default-deny for a developer's diff, so a run pointed at one
of them produces a correctly refused empty change. What it also produces is a
spent run, two review rounds, and two rounds counted against that item's cap, so
an item mis-selected twice reaches its cap having done nothing and escalates work
nobody ever started.

So an item says what carries it, and whose conversation that is. The product
manager sets `executor` on the item as it is admitted — `conversation:` followed
by the role, as in `conversation:architect` — and `update` takes it too, for work
already in the queue. The bare word `conversation` is refused: from the handoff
until whoever holds the item starts on it, the role named here is the only thing
that says who has it, and a marker that named none left exactly that stretch
unattributed. An item carrying it keeps its place in the order, is reported in
the queue with what carries it, and is never selected for a developer run. It is
not a wait, and the pass says so rather than counting it among the items that are
about to become pullable: nothing clears, and what moves it is somebody opening
the conversation the item names. Work that says nothing is a developer run, which
is nearly all of it.

Naming the item yourself is unaffected. `yoyo run <id>` is you deciding, and the
marker steers what the harness chooses rather than what you may ask for.

The marker is not retroactive, which is the part worth knowing before you rely
on it: it covers exactly the items that carry it, so work admitted before you
started marking carries none and is chosen as ordinary developer work. Nothing
infers it — no reading of an item tells a conversation from a diff — so bringing
an existing queue under the guard means marking its conversation-executed items,
one `update` each, in the product manager's conversation.

An eighth thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, because
[staleness reports rather than decides](artifacts.md#what-a-change-upstream-leaves-stale),
and what changed goes into the run's recorded reason instead.

Two things make this accountable rather than work happening behind your back.
Holding intake stops it choosing anything more while what is running finishes,
and it is read at every pull rather than once at the start, so a hold you place
mid-pass takes effect at the next selection. And every run it starts records, in
durable state, why that item was chosen — where it sat in the order, how much of
the queue was pullable, how much of the machine was free, anything upstream that
had moved, and whether conflict-avoidance shaped the choice. `yoyo status` reads
it back.

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
A session that stops that way is a stopped line like any other: with work still
ready, [the Slack sink](reporting.md#reporting-into-slack) says so again every hour until
somebody starts one, rather than saying it once at the moment nobody was reading.

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

### Measuring the reviewer against itself

A branch review is a replayable function of a branch state: the same commits over
the same base, described the same way, judged under the same contract. That is
what makes a *shadow* review possible — the same review, made to measure the
reviewer rather than to judge the branch:

```sh
./bin/yoyo review --shadow --model sonnet --base main --branch milestone
./bin/yoyo review --compare                   # what the collected ones amount to
```

A shadow verdict approves nothing, whatever it decided, and that is enforced in
the record rather than remembered by whoever reads it: the durable review is
marked, and `Approved` answers no for a shadow verdict exactly as it does for a
repair one. That is what makes the measurement free of risk — a cheaper reviewer
pointed at a branch cannot leave an approval of it behind. `--model` is refused
without `--shadow` for the same reason: a review whose reviewer was chosen at a
terminal rather than by the configuration is a measurement and only ever that.
Because it decides nothing about the branch, a shadow review exits on the
question it was actually asked — whether it produced a verdict — so a shadow
`repair` verdict is a successful measurement rather than a failure.

The baselines are the branch reviews already recorded. Every verdict `yoyo
review` has ever given is in the `branch-reviews` log with the base commit and
head commit it was given on, and `--compare` pairs on those two — so a branch
state the configured reviewer has already judged can be shadowed without paying
for its baseline again. What that costs is one shadow review per state and
nothing else.

Reaching one of those states is the only manual step. `--branch` takes a local
branch name, so a state that is still a branch head is shadow-reviewable as it
stands; an earlier state needs a local branch pointed at the head commit the
recorded verdict names (`git branch <name> <commit>`), and `--base` takes the
base commit from the same record. That new branch name is deliberately not part
of the pairing: the same commits reached under a second name are the same code,
so the two reviews still pair, and each side's own branch name is reported so the
one thing the reviewers were told differently is visible. A state nothing has
reviewed has no baseline at all, and there an ordinary `yoyo review` is what
makes one first.

`--compare` reads what was recorded and invokes nothing. For each shadow review
it reports, per severity, how many of the baseline reviewer's findings the shadow
also anchored to, how many it missed, and how many it raised alone, with what
each of the two reviews cost beside it. Findings are paired by the file each
anchors to, which is the only thing two reviewers reliably agree on — they will
differ on the line and always on the wording — so a finding that names no file
cannot be paired at all, and the count of those is reported rather than folded
silently into the miss rate. Every finding is listed under its comparison for the
same reason: whether a missed finding was a local, mechanical catch or one that
only exists in the accumulated shape of the branch is a judgement about its
content, and the numbers cannot make it. A finding only the shadow raised is a
candidate false positive rather than a proven one — what this measures against is
the other reviewer, not what is true of the branch.

A shadow review costs money like any other provider invocation, and is priced
where every other branch review is: it records the same event stream, so
[`yoyo-status -c`](operations.md#following-a-run-a-conversation-or-a-branch-review) counts it
under `branch reviews`, and `--compare` reports each side's own cost from that
same log. It is not in `yoyo cost`, which prices work items from the runs made
for them — a branch review belongs to no run, and a shadow review belongs to no
work item either. So measuring a reviewer is spend an operator can see, but not
under the item that prompted it, and it is indistinguishable in the status total
from a review that gated something.

The first use of this is recorded in
[the ifd.92 experiment note](experiments/yoyodyne-ifd-92-shadow-review.md): what the
instrument is, which recorded verdicts are the benchmark, and what has not been
measured yet.

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
way reports the pull request as queued and finishes, leaving the work item open
because nothing has yet merged the change anywhere but locally;
[`yoyo reconcile`](operations.md#recovering-interrupted-runs) settles it once the
forge has merged — closing the item then — or, if the forge dropped the queued
merge, records an outstanding publication and hands the item back with a
blocker. A repository with no configured remote publishes nothing and behaves
exactly as a purely local project does.

Merging belongs to `approvals.integration`, so the two settings compose rather
than imply one another. Publishing with `integration: human` opens the pull
request and stops: nothing is merged, the run branch survives on the remote, and
the worktree is preserved for you — which is what a `human` integration policy
means. See the
[configuration guide](configuration.md#publishing-through-pull-requests).
