# Configuring triage thresholds and provider waits

What the harness does when a provider refuses or dies mid-run, and the
thresholds that decide when a stalled run, a stuck merge, or a work item that
has been given enough is escalated to you.

[The configuration index](../configuration.md) lists the other guides.

## Waiting out a provider that refuses

When the provider reports that a usage limit is exhausted, the run pauses rather
than failing: nothing is cleaned up, the Beads item stays claimed, the worktree
and branch survive, and the developer session is kept so the reissued attempt
continues the same change. This covers both provider invocations a run makes — a
developer attempt is reissued, and a review the provider declined is asked for
again without redeveloping the change or spending a repair attempt. Two settings
bound that wait, both written in Go's duration syntax (`6h`, `90m`, `45s`):

```yaml
execution:
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m
  server_overload_pause: 90s
```

`usage_limit_unknown_reset_pause` is the interval between probes: how long a run
sleeps before reissuing the attempt and finding out whether the provider will
serve it now. It applies whether or not a reset time was named, which is the
whole of the polling discipline. A limit reported *without* one is not the same
as having no capacity — an exhausted overage allowance reports this way while
the ordinary rolling window keeps resetting on its usual schedule — so the work
is waitable and simply carries no deadline. A limit reported *with* one is
waitable and carries a deadline that is an upper bound rather than a gate: a
reset time is a claim about the provider, and claims go stale in both directions,
because capacity gets bought mid-wait and a rolling window can free room before
the quoted edge. So a run sleeps this interval or the time left to the deadline,
whichever is shorter, and then asks again; a probe into a window that is still
closed costs one refused request and re-parks on whatever the provider now
reports. Every probe spends the same budget as any other wait, so a provider
that keeps refusing reaches the maximum rather than polling forever.

`usage_limit_max_pause` is the longest a single run will spend waiting **in
total**, across every pause it takes. The budget is per run, not per pause,
because a provider that keeps refusing would otherwise walk a run far past the
configured maximum one individually-acceptable wait at a time. A reset time that
does not fit in what the run has left is treated as no usable reset time: the run
stops and records a blocker naming what it already spent, instead of sleeping on
it. The `6h` default covers the provider's five-hour limit with slack and
deliberately stops short of its seven-day one, because a capacity problem that
would cost days needs a person rather than a timer. Setting it to `0` disables
waiting entirely, so every exhausted limit blocks immediately.

`usage_limit_in_process_pause` is how much of that bound a run will spend
sleeping inside the `yoyodyne` process. It defaults to the same `6h`, so by
default every probe the harness will take is taken here and the run continues on
its own once the limit resets. Lowering it — say to `1h` — makes the process
sleep probes until it has spent that hour on the run and then exit, with the run
still in flight and its deadline recorded; running `yoyo run` on the same item
continues that same run, and that process gets the whole bound again.

It is also what bounds a run parked on an operator pause — `yoyo pause`, which
holds everything the harness would spend at every provider-call boundary. That
pause has no configuration of its own and no maximum: what lifts it is the
operator rather than a clock, and time spent held is accounted separately from
`usage_limit_max_pause`, because the provider never refused the run. What it
shares is this bound on how long a single process will stay open waiting, and
the durable park behind it: a run held past the bound exits with the park
recorded, and `yoyo run` on the same item continues it once `yoyo resume` has
lifted the pause. See the README for the whole of that behavior.

The bound is on how long one process stays open for a run, so it counts every
probe that process has already slept rather than each probe separately. Applying
it per probe would bound nothing: a probe interval of `30m` fits under a `1h`
bound however many times it is taken, so a six-hour deadline would hold the
process open for the whole six hours in half-hour slices. It spans phases for
the same reason — a process that waited half an hour for the developer and half
an hour for the review has been open for an hour.

Both paths record the deadline in durable run state *before* any waiting begins,
so a process that dies mid-wait loses nothing and a restart serves the same
deadline rather than retrying straight back into the limit. What each probe will
spend is committed before it is spent, for the same reason, and the unspent
remainder of a probe cut short is given back — so the recorded total is what was
actually waited. `yoyo reconcile` leaves a paused run alone for the same reason
it leaves a repair loop alone: it is not an interrupted run, it is a run that is
owed the attempt it was refused.

A reset time that is absent, unreadable, already in the past, or beyond what the
run has left of `usage_limit_max_pause` stops the run with a blocker naming what
refused it. A reset that is not in the future is refused deliberately: a limit
still declining work while claiming it has already reset is not describing a
wait, and honoring it would mean reissuing straight back into the same refusal.

`server_overload_pause` is the same discipline on a different clock, for the
other way a provider refuses without judging the work: its own servers are
transiently unable to serve the attempt. That names no reset time at all and
lifts in seconds rather than hours, so a run waits this interval — `90s` by
default — and reissues, rather than parking for the half-hour probe interval a
usage limit uses. Everything else is shared with the paragraphs above: the
deadline is durable before the wait begins, the reissue continues the same
worktree and developer session, and each wait spends `usage_limit_max_pause`, so
an overload that never lifts walks into that maximum and stops with a blocker
instead of reissuing forever. `yoyo resume` releases one of these waits exactly
as it releases a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that itself, and the harness does not duplicate the wait. What the
harness acts on is the terminal result that CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because the provider has stopped
retrying by then and something has to. An overload is the only terminal
`api_error` that becomes a wait; the rest are covered by
[the relaunch budget](#relaunching-a-run-the-provider-killed) below.

`yoyo resume <beads-id>` is the one thing that overrides a recorded deadline,
and it overrides nothing else. It moves the next probe to now, for when the
reset has stopped being true — you raised the account's capacity, say — because
the deadline is a claim about the provider and you are the one who can change
what it is a claim about. It never stops the run: a process asleep on the wait
acts on the release within seconds, and the run keeps its claim, its branch, its
worktree, and its developer session. If the provider still refuses, the run
records the new report and waits again, so a premature release costs one refused
request. See the README for the whole of that behavior.

## Relaunching a run the provider killed

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it dies: the API answers with an error its own retry ladder did not
outlast, or the connection carrying the response goes away before the reply is
finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. The work was never judged and nothing is wrong
with the change. Rather than failing, the run relaunches itself:

```yaml
execution:
  transient_relaunches_before_blocking: 2
```

The dead invocation is reissued in the same worktree and the same developer
session, so an attempt that died mid-response continues the change it had already
started rather than deriving it again. No wait is attached, because there is no
condition to wait out: a dropped connection is already gone, and the provider's
own retries are spent before the harness sees the terminal.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather a single run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer, so a relaunch spends no repair attempt.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.
A run that spends the budget stops and records a blocker on the work item naming
the provider's own last message. Setting the bound to `0` restores the earlier
behavior: the first provider death ends the run.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says plainly that no check failed and no reviewer asked
for repair. A run killed inside its repair loop names the repair attempts it had
spent, the failing check, and the findings it was answering, and says the
provider stopped it rather than that verdict — the evidence is unresolved rather
than dismissed.

A refusal that would stand is never relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — earns the identical answer on the next attempt, so it
fails the run as it always did; so does a 529, which is a wait rather than a
relaunch, and so does any terminal the API did not report at all.

## Triage thresholds

Triage is what looks at work that has stopped moving. Its numbers are
configuration rather than constants, because each one is a judgement about a
project's pace — how long a merge may take before nobody merging it is news, how
many times one change may go round with a reviewer before going round again is
the problem:

```yaml
triage:
  stuck_merge_age: 2h        # how long an approved publication may sit unmerged
  review_rounds_cap: 4       # total review rounds one item may accumulate
  repair_grant_attempts: 2   # what a grant is worth, when triage grants one
```

**These are read by the triage docket**, which is where work that has stopped
moving is collected and delivered to the development manager: `stuck_merge_age`
decides when an unmerged publication is docketed, and the item's budgets —
rounds spent, repair grants, re-runs, merge re-arms — are carried on every
entry so a decision about one is made against what the item is allowed to
spend rather than against the evidence alone. Deciding what becomes
of a docketed item is the development manager's; the caps are what refuse a
decision that would spend more than the item is allowed.

**The docket reads the record the guards enforce, not a count of its own.** Every
figure on an entry — the rounds spent, each decision recorded, and each cap
beside it — comes from the [per-item counters](#what-one-work-item-has-been-given)
a decision spends, and the re-runs already carried out come from the per-stoppage
re-run records under `<state root>/products/<product id>/reruns/`. It
is read as the docket is read rather than written into the entry: the entry is
recorded once as the work stops and every decision about it is made afterwards,
so an entry frozen at docket time could only ever show every decision as absent.
That was the defect — a re-run decided and durably recorded, a resubmission
refused as one of one re-runs spent, and a docket showing nothing decided, which
nearly had one authorized recovery spent twice. An item whose record cannot be
read says so on its entries instead of rendering as an item nobody has decided
anything about.

The docket is built when something scans: `yoyo reconcile`, and the moment a
development manager conversation opens. There is no scheduled process behind it,
so `stuck_merge_age` is a floor rather than a promise — a publication becomes
docketable at that age and is docketed the next time one of those happens.

**All three are read.** The docket above consumes `stuck_merge_age` — an approved
publication older than it is docketed at the next scan — and `review_rounds_cap`
bounds the [per-item counters](#what-one-work-item-has-been-given) below, which
every run writes to and `yoyo status <id>` reports. The development manager's
triage decisions spend them: a decision of `repair` takes a grant of
`repair_grant_attempts` rounds truncated to what the cap has room for, and
`rerun` and `rearm` each spend a budget of their own. Every one of the three is
refused once the budget it spends is gone, and the three do not share one — the
[table below](#what-one-work-item-has-been-given) says which bound refuses
which.

Recording a decision and carrying it out are two steps, and two of the six
decisions have an action for the second. They are the two opposite answers to a
run that stopped: `yoyo triage rerun` starts the item over, and `yoyo triage
repair` continues the run that stopped on the change it already has.

`yoyo triage rerun <run-id> --reason "<the recorded decision>"` starts a fresh
run of the item whose stopped run the docket entry names. It is refused unless that run is terminally recorded with
its blocker standing — read from the run's own record rather than from the
docket entry — and one docketed stoppage is re-run once, whatever the item's
budget still says. It is also refused unless a decision of the development
manager's is there to carry out: the decision spends the item's re-run budget as
it is made, and each one authorizes exactly one re-run, so what has already been
claimed for the item is read back against what was decided. An item whose budget
carries no re-run is one nobody decided this about; an item whose decisions have
all been carried out is refused too, because that counter is a total nothing
clears and reading it alone would let a second stoppage of an already re-run item
start on the strength of a decision that was about the first. A second stoppage
needs a second decision, which past the once-per-item cap is an escalation rather
than a larger budget. It is refused, finally, unless the work item itself is one
a run may start on — open, with nothing it depends on outstanding — which for a
docketed stoppage ordinarily means somebody has put it back, because the run
stopping blocked it. The intake hold applies too, because the harness is the one
choosing the work; a re-run under a hold starts nothing and claims nothing, so the
stoppage keeps its re-run for after the hold is lifted. The fresh run records
the development manager as having chosen it and the reasoning the harness was
given as why, which is what the `selected-work-passes-intake-and-records-why`
invariant requires of anything the harness chooses for itself.

**Every one of those refusals is made before the stoppage's re-run is claimed**,
and the claim is what spends it. A condition asked after the claim would spend
the budget on refusing to use it: the item's status is the one that showed this,
where a re-run of a blocked item was refused for the status and the next attempt
was then refused by the once-only guard, for a run that had never happened. So a
refused re-run leaves the stoppage its re-run and says what would make it stop
refusing, and asking again once that is true carries out the same decision. The
opposite order holds past the claim, deliberately: the claim is taken before the
run is started, so a process that dies between the two has spent a re-run nobody
took rather than taken one nobody recorded.

**A full harness is a state rather than a refusal.** `execution.max_concurrent_developers`
is read before the claim, from the same runs in flight the reservation counts, and
every slot being taken neither refuses the carry-out nor fails it: nothing is
claimed, the decision stands until it is carried out or the development manager
withdraws it, and asking again once a slot frees carries out the same one. The
item is meanwhile the open work the scheduler pulls from, which is the other way
it reaches a developer, and the two agree because this claimed nothing. The last
slot can also go between that reading and the reservation; a claim taken for a
run the reservation then refused for capacity is **given back**, because that run
provably never started — a reservation refused for capacity creates no run
record, claims no work item and runs no agent. It is the one thing that gives a
claim back, and a claim carrying a run is refused rather than removed. A
withdrawal the harness could not write is reported rather than swallowed: the
stoppage has then spent its re-run on a run that never started, which is a thing
to go and correct.

The re-run is recorded beside the counters, one file per docketed stoppage at
`<state root>/products/<product id>/reruns/`, and it carries what the stopped
run preserved. Its branch and worktree are **kept** while the fresh run has not
integrated — that is what a development manager's guidance points at when it says
what to cherry-pick — and **retired** explicitly once it has. Anything that could
not be retired stays kept with the reason recorded: a worktree holding
uncommitted work and a branch whose work nothing promoted are both left exactly
where they are, because nothing else records what they hold. Nothing automated
deletes the record, for the reason nothing deletes a counter file — save the one
withdrawal above, which removes a claim whose run was refused a developer slot
and so never existed.

A retirement is written onto the stopped run itself as well, under that run's own
lease, because its record is what `yoyo status` and the docket read to say
whether its branch and worktree are still there. A stopped run promoted nothing,
so the removal names the run that superseded it — `artifacts_retired_by` on the
run's state — which is the second way a recorded removal is earned beside a
promotion of the run's own. A retirement the harness could not write onto that
run is reported rather than swallowed: the artifacts are gone and its record
still says otherwise, which is a thing to go and correct.

`yoyo triage repair <run-id> --reason "<the recorded decision>"` is the other
half of the same pair, and it starts nothing over. It re-enters the stopped run's
own repair loop: the same branch, the same worktree, the same developer session,
and the reviewer's findings handed back exactly as they were written.

**What it may hand the run is the grant the development manager already
recorded**, and it spends nothing of its own. Deciding `repair` is what takes the
item's grant — `repair_grant_attempts` rounds, truncated there to what the round
cap had room for — so this reads that record for how many attempts it is worth
and hands the run exactly that. An item nobody granted a repair is one nobody
decided this about, and it is refused; so is an item whose grant the harness has
already carried out, which it counts from the continuations the item's runs
record. Past the once-per-item cap a second is an escalation rather than a larger
budget, and an item with no rounds left never gets a grant to carry out at all.

Three more things refuse it. The stopped run has to be really over, terminal with
its blocker standing, read from the run's own record rather than from the docket
entry. The run has to have recorded a repair input — a run whose provider kept
refusing, or whose replay conflicted, never had a failure returned to its
developer, so there is no repair loop to re-enter. And the preserved worktree has
to be as the harness left it: what a continued developer is handed back is
whatever is in that worktree, so a HEAD that moved — an operator mid-surgery, an
agent that committed — is a person's to decide about, and the refusal leaves the
item blocked and says so. The intake hold applies for the reason it applies to a
re-run: this spends on a provider, and the development manager naming the item is
not the operator naming it.

**A repair supersedes the blocker rather than needing somebody to remember to.**
The run that stopped blocked its item and recorded the blocker on its own state,
which `yoyo status`, `yoyo reconcile`, and the docket all read as the fact that
it has stopped. So re-entry clears both at the moment it happens: the item is put
back with the decision recorded on it first, and the run's blocker is cleared onto
the continuation that supersedes it, which keeps the words it was recorded in and
the grant that bought the attempt. The order is the item first, because a run
recorded as running behind an item that still says it is blocked is the one
half-finished state nothing else here would notice, and every refusal is asked
before either write, so a refused re-entry leaves the grant exactly where it was.

The continuations are recorded on the run itself, under `repair_continuations` in
its state file, and they are what the continued run's repair loop adds to
`execution.repair_attempts_before_replan` to know what it may spend. They are
also how the harness knows what a grant has already bought: summed across an
item's runs, they are what a second re-entry is refused against.

The other four decisions still carry themselves out no further than the record:
nothing in the harness repeats a merge request the forge dropped, and a re-scope,
a wait, and an escalation ask for no action at all. The budget is spent when the
decision is recorded, which is the same order every counter here is written in —
an attempt nobody took rather than one nobody counted — so a decision nobody acts
on has still cost the item its budget.

`stuck_merge_age` is how long an approved publication may sit unmerged before it
is docketed. It is an age rather than a deadline because what makes a
publication stuck is that nothing has happened to it, and nothing happening
offers no event to hang a deadline on. It must be positive: an age of no time at
all dockets every publication the instant it is made, which is a docket of
everything and a triage of nothing.

`review_rounds_cap` bounds the review rounds one work item may accumulate in
total — across repairs, across runs — past which triage may no longer hand it
back for another repair. Past the cap triage still has both of its other
actions: escalate the item, or re-scope it. `0` is a choice somebody can mean and
is accepted as one: an item that reaches triage at all is never repaired again.

`repair_grant_attempts` is how many repair attempts triage hands an item when it
decides the work is worth another go. Leave it out and it follows
`execution.repair_attempts_before_replan`, tracking that budget rather than
copying it: raise the budget and the grant rises with it. It may not be zero,
because a grant of nothing leaves the item exactly where granting nothing would
have. A project that configured no routine repair attempts at all still gets a
derived grant of 1 rather than a configuration that fails to load — the grant is
triage's deliberate exception to that budget, not another helping of it.

### What one work item has been given

The thresholds above bound something, and what they bound is a durable record
per work item: the repair grants triage has given it, the re-runs it has caused,
the merge re-arms it has made, and the **review rounds** the item has cost across
every run of it. It lives beside the runs under the state directory, one file per
item, and it outlives them — a run is settled and its worktree and branch are
removed, and what the item has been given is still there.

That it is not on a run is the whole point. Every budget a run spends starts
again at zero in the next run, so an item handed back, run again, and handed back
again is an item nothing was bounding. `yoyo status <id>` reports it under that
item's runs, in text and in `--json`:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
```

At or past the cap — 4 of 4 exactly included, because a grant needs a round and
none remains — the same line reads: `review rounds: 6 spent across every run of
this item — at or past the cap of 4, so no decision that buys a round remains`.
What may still happen is what the budget lines beside it say: waiting,
re-scoping, and escalating spend nothing, and a merge re-arm spends only its
own budget, whatever the rounds say.

```text
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 of 2 permitted
  1 grant(s) were cut down to the rounds the cap still had room for; 1 round(s) were granted in total
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

**The first line counts what has been spent, not how many times triage looked.**
Three of the development manager's six decisions spend a budget here — a repair
grant, a re-run, a merge re-arm — and `wait`, `rescope`, and `escalate` cost
nothing and reach no counter, so an item that was escalated reads `triage has
spent nothing on it`. Whether stopped work has been decided, and what was
decided, is recorded on the work item itself.

**This is also what the docket reports.** Every entry for an item carries these
counters and these caps, read as the docket is read, so what `yoyo status` says
about an item, what a docket entry says about it, and what refuses the next
decision about it are one record rather than three counts of it.

**A round is a reviewer verdict a developer attempt produced**, counted across
every run of the item. A re-review no developer attempt produced is not one, so a
promotion that [loses its race](publishing.md#losing-a-race-for-the-target-branch) and gets a
fresh verdict on the replayed change is not charged for it — counting that would
charge an item for losing a race it did not cause. A review re-asked for after an
interrupted process is the same case and is counted once for the same reason.
Rounds are recorded whatever a cap says, because a round is something that
happened rather than something being asked for.

**Each counter is written before the action it counts takes effect**, so a
process that dies between the two has recorded a grant it did not give rather
than given one it did not record — an unspent attempt rather than a duplicated
one. Concurrent updates are serialized per item, so no increment is lost, and a
record that cannot be read is a refusal rather than an empty budget: an
unreadable budget read as empty is every cap in it stopping to mean anything.
Recovery from one is a decision, not a repair: the record is one JSON file per
item at `<state root>/products/<product id>/triage/`, named by a slugged
rendering of the item id with a digest suffix (so a listing reads which item
each file belongs to, and two ids that render alike still get their own files
— match on the slug). Read it and fix what is malformed if the history is
worth keeping — or delete it, which resets every budget the item had spent,
and is therefore a deliberate re-budgeting to record on the work item in the
same breath, not a cleanup. Nothing automated deletes one, for exactly the
reason nothing reads one as empty.

Which threshold refuses which action:

| Action | Refused by |
| --- | --- |
| another repair grant | one per item, and `triage.review_rounds_cap`, truncated to the rounds it still has room for — one precondition among several: the decision recorded here spends the budget, and `yoyo triage repair` re-enters the stopped run's repair loop on it, which is a claim the harness makes rather than the operator, so `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the run is continued and the reasoning recorded in the run's durable state. That action is bounded again by what the grant has already bought, read back from the continuations the item's runs record, and it refuses a preserved worktree that is not as the harness left it |
| another whole run of the item | one per item, and `triage.review_rounds_cap`, refused outright once none remain — one precondition among several: the invariant `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the claim and the selection reason recorded in the run's durable state. The decision recorded here spends the budget; `yoyo triage rerun` starts the run and carries both, is bounded again by one re-run per docketed stoppage, and reads this counter back — against the re-runs already claimed for the item — as the proof that a decision is there to carry out |
| re-arming a merge the forge dropped | `execution.integration_retries_before_reconciliation` — one precondition among several: a re-arm is an integration retry against the target branch, so `one-promotion-per-target-branch` binds the re-arm action (unbuilt today), which must repeat only the identical already-authorized forge request under the harness's own lease. The decision recorded here spends the per-item integration-retry budget as shipped; the design's once-per-publication counter arrives with the re-arm action, and performing the re-arm is that action's |

The first two buy review rounds, so the round cap bounds them, and a grant is
**truncated** rather than refused where some rounds remain: at the defaults an
item that has been through its repair budget once has spent three rounds of
four, so the configured grant of two attempts is cut to the one round that is
left, and the truncation is recorded. An untruncated grant would promise a round
nothing would let it take, and one that overshot the cap would make the cap
decorative.

**The round cap is not their only bound, and could not be.** Each of those two
is also once per item, which is not configured because it is the workflow rather
than a judgement about pace: triage takes its own decisions about one item once,
and a second is an escalation rather than a bigger budget. The rounds cannot
stand in for it — they bound what an item costs, and an item whose runs stop
before any reviewer verdict, on a provider that kept refusing or a replay that
conflicts, costs no rounds at all. With only the round cap, that item could be
handed back and re-run without bound while every counter read zero.

A merge re-arm buys no round at all, which is exactly why it needs a bound of
its own: an action that costs nothing to take is the one that can be taken
forever. It follows the integration retries a single run is already permitted,
which is the same judgement about the same thing one level up.

**These budgets are per machine.** Two collaborators running their own harnesses
against one repository each hold a full set for the same item, so a cap of one is
a cap of two across the pair. That is a recorded limit rather than a design, and
[`docs/team-mode-scope.md`](../team-mode-scope.md#a-recorded-gap-per-item-budgets-are-per-machine)
states it where the team-mode design will need it.
