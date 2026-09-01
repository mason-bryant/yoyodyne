# Operations and recovery

*For an operator recovering from a stall, a crash, or a provider refusal. Part
of [yoyo's documentation](../README.md#further-reading).*

## Checking the installation

`yoyo doctor` answers one question — can work actually run here — and answers it
before anything is spent rather than at the point a run discovers it cannot:

```sh
yoyo doctor            # everything it looked at, healthy or not
yoyo doctor --quiet    # only what is wrong
yoyo doctor --json     # the same findings, for something automating the repair
```

It looks at the `yoyo` on your `PATH` and whether it is the build you think it
is, Git and whether this project is a repository with something to branch from,
the tracker and whether it answers *here*, the configuration, the deterministic
checks and whether this machine can run the programs they name, each provider
your agents name — installed always, and authenticated where the harness has an
adapter that can ask, which today is Claude Code — forge access when the project
publishes, and, when reporting is on, this project's own Slack secrets and the
sink that is supposed to be using them.

**Every finding that is not healthy carries a remedy, and a remedy is a
command.** That is the whole difference between this and a status listing: what
it prints under a problem is what to run. `--json` carries the same findings with
the same remedies, which is what [the setup and repair
prompt](../skills/yoyo-setup/SKILL.md) has your own agent session act on rather
than parsing any of this.

```text
yoyodyne cannot run work: 2 problems, and 1 warning worth knowing about

problem  tracker                bd is installed but could not read this project's issues
                                fix: bd init
ok       checks                 4 checks configured, and every command resolves here
problem  provider:claude-code   claude is installed but not authenticated, so every agent invocation would be refused
                                fix: claude auth login
warning  slack-sink             no sink is running for this product, so nothing is being reported
                                fix: SLACK_BOT_TOKEN="$(security find-generic-password …
```

Findings come in the order you would fix them in — the tools, then the project,
then what the project turns on — rather than worst first, because the first
problem in the list is usually why the ones under it are problems too. `--quiet`
drops the healthy ones and changes nothing else.

A healthy installation says so in as many words, because an empty list of
complaints and a check that never ran read the same. It exits 1 when something
would stop work running and 0 otherwise.

**A warning is not a small problem — it is something about an installation that
works.** The `yoyo` on your `PATH` having drifted from the one you are running is
one. Every reporting finding is another, and deliberately so: reporting is an
observation and never a gate, so a sink you never started, a workspace that is
down, and a token nobody stored all leave an installation that runs work exactly
as it would have. They are still named, in full, with the command that ends each
one — what the exit status refuses to do is fail a machine that works.

It changes nothing. Nothing here installs, authenticates, restarts, or edits a
configuration, and no credential is ever read: whether a secret is stored is
asked in the form that answers without producing the value.

The two checks worth calling out are the ones that catch an installation that
was working and stopped. **A long-running sink is started from a binary that
keeps moving underneath it**, so the build that is reporting and the build that
is installed drift apart with no event between them — nothing fails, nothing is
logged, and the milestones added since it started are simply never posted, which
in a channel reads as a quiet week. The version is asked first, because that is
what you installed by; the revision behind it is asked second, because on a
harness developing itself the version cannot answer at all. Every unreleased
binary reports the same version, so a sink started last week and the binary
diagnosing it compare as identical — a clean report over exactly the drift being
looked for. Two revisions are two places in one history, so where the versions
agree and the revisions do not, the revisions settle it; and where there is no
pair of revisions to compare, the finding says the comparison was not made rather
than reporting the versions agreeing as if it had been. And **on a machine running more than one
harness, "a Slack token exists" is true for all of them and right for at most
one**, so what is checked is this project's own pair under names that carry the
product, and whether the sink that is running was launched with them. See
[Reporting into Slack](reporting.md#reporting-into-slack).

A stopped sink is the one finding here you need not act on by hand. On macOS,
`yoyo slack ensure` starts one if nothing is reporting for this product, from
this product's own keychain items, and does nothing when a sink is already
running — so it is what an unattended pass calls, once per product checkout on
the machine. Scheduling it is still yours, and only because the pass that would
call it is not here yet: the productized maintenance job is `yoyodyne-ifd.207`,
and nothing `yoyo` installs runs anything on a schedule until it lands.
`yoyo doctor` only diagnoses — it changes nothing, and starting the sink is the
other command's job.

## Pausing everything, and resuming it

`yoyo pause` stops everything the harness would spend on a provider, and
`yoyo resume` starts it again:

```sh
./bin/yoyo pause      # to conserve tokens, or for any other reason of your own
./bin/yoyo resume     # everything parked on it carries on
```

It is one durable switch over the whole machine rather than one item. Every
provider-call boundary reads it before it spends — a developer attempt, each
reissue of one after a refusal, a reviewer invocation, a conversation turn — so
a pause placed while a developer is working reaches that run at its next
attempt rather than only reaching the runs that had not started. The flag lives
at the state root rather than under a product, because what makes you pause is
an account or an afternoon rather than any one project.

A run that meets the pause parks exactly as one waiting out a
[usage limit](#waiting-out-a-provider-usage-limit) does, on the same machinery
with you as its reset instead of a clock: the park is durable before any waiting
starts, and the item stays claimed with its branch, worktree, and developer
session all preserved. A process already parked acts on `yoyo resume` within
seconds and carries on unaided; one that exited while the pause stood is
continued by `yoyo run <beads-id>`. Nothing is cancelled, so nothing has to be
reconciled afterwards — which is the whole difference between this and killing
processes, where the run lands cancelled with its item still claimed and the
work has to be developed again from scratch. A conversation turn is refused
rather than parked, because there is a person in front of it: saying the same
thing again once the pause is lifted takes the turn that was refused. `yoyo
review` is refused for the same reason, having no run to park.

The honest boundary is that a provider call already in flight is not
interrupted. The flag is read before a call, so a generation that is already
streaming finishes and is charged for, and the pause takes effect at the next
boundary — which for a developer attempt can be minutes away. Stopping a
generation mid-flight would throw away what it had already cost and leave the
run needing the same work again, which is the cost that makes a kill the wrong
verb in the first place.

Time a run spends held is accounted under its own kind, separately from what a
provider's refusals are allowed to spend: a hold never eats a run's
`execution.usage_limit_max_pause` budget, and nothing bounds it, because the
thing that lifts it is you. The conversation's `/status` leads with a PAUSED
banner naming when the pause was placed, and `yoyo status` names it on the
[*Needs a human*](#where-the-harness-stands-the-four-lines) line with the same
moment, because a system somebody paused and forgot looks exactly like a system
that died.

This is the broad switch, and there is a narrow one beside it. `yoyo pause` stops
everything including the runs already under way, which park keeping everything
they have; the conversation's [`/hold`](conversation.md#steering-the-work-from-the-conversation)
stops only the harness choosing new work and lets what is running finish. Reach
for the first when the reason is your account or your afternoon, and the second
when the reason is the queue.

`yoyo release` lifts that narrow hold from a terminal:

```bash
./bin/yoyo release   # the harness may choose work from this backlog again
```

It is the same record `/release` lifts — one file under the product — so it does
not matter which surface placed the hold or which lifts it. It is here because a
hold you did not place is the one you are most likely to meet with no
conversation open: the failure-storm brake holds intake itself when runs keep
blocking, and every report of a held intake at a terminal now names this command
beside `/release`. Releasing what is not held is not an error, an item you name
with `yoyo run` was never subject to the hold, and a watching `yoyo work` session
starts choosing again at its next poll. Placing a hold stays in the conversation,
where the reason for it can be recorded with it.

## Waiting out a provider usage limit

None of what follows is specific to one provider. What a provider said is read by
that provider's dialect and reduced to one of six answers — served, retrying,
limit-reached, unavailable, interrupted, refused — and every wait below is driven
by those and by nothing provider-specific. A project that declares a provider of
its own gets exactly this behaviour, including the two reset-time rules, without
restating any of it: see [provider plugins](provider-plugins.md).

When the provider reports that a usage limit is exhausted, the run pauses
instead of failing — for either provider invocation a run makes, the developer
attempt or the review. The reset time the provider named is recorded in durable
run state before any waiting starts, and nothing is cleaned up: the worktree,
the branch, the claimed Beads item, and the developer session are all kept, so
the reissued attempt continues the same change rather than starting it over. A
review that was declined is simply asked for again once the limit resets,
without redeveloping the change or spending a repair attempt.

The recorded reset is an upper bound on the wait rather than a gate on it. A run
sleeps `execution.usage_limit_unknown_reset_pause` — thirty minutes by default —
or the time left to the deadline, whichever is shorter, and then reissues the
attempt: the reissue *is* the probe. A reset time is a claim about the provider,
and claims go stale in both directions — capacity gets bought mid-wait, and a
rolling window can free room before the quoted edge — so a probe into a window
that is still closed costs one refused request and re-parks on whatever the
provider now reports. A run sleeps probes inside this process until it has spent
`execution.usage_limit_in_process_pause` on this run, and then exits with the
run still in flight instead of sleeping the next one; running `yoyo run` on the
same item continues it, with the whole bound available to that process again.
That bound counts every probe this process has already slept rather than each
one on its own, because a bound applied per probe would stop bounding how long
the process stays open at all.
`execution.usage_limit_max_pause` bounds what one run may spend waiting in total
rather than each wait separately, so a provider that keeps refusing cannot walk
a run past it, and what it records is what was actually waited rather than the
span to a deadline the run never reached. A limit reported without a reset time
polls under exactly the same rule, because it is unknown rather than
unwaitable: the monthly overage allowance reports this way while the ordinary
rolling window keeps resetting on its usual schedule, so it waits the same
interval and asks again. Unifying the two was the point — one polling
discipline, whether or not a deadline was quoted. A limit the harness genuinely
cannot wait for — a reset that is not in the future, or one that no longer fits
the run's remaining budget — stops the run and records a blocker rather than
guessing a wait. An exhausted limit is not the only thing a run waits out:
[an overloaded provider](#waiting-out-an-overloaded-provider) below takes the
same machinery on a much shorter clock.

An exhausted limit is not only a run's problem, either. The harness asks a
provider for work in three places: inside a run, which parks as above; a
conversation turn; and an independent `yoyo review`, which uses the same reviewer
with no run around it. The last two have no run to park, so each records the
refusal instead — what was stopped, the limit the provider named, and when it
said it lifts. Nothing waits on it: the turn or the review fails at your terminal
exactly as it did before. What the record buys is that
[reporting into Slack](reporting.md#reporting-into-slack) says it as a `warning` without you
there, and a run that parks on the same limit is said at that weight too. Hours
in which nothing will happen is the one message a channel nobody is watching most
needs to carry, and it must not weigh the same as checks passing.

Selection is not a fourth place. A watching `yoyo work` session reads the tracker
and starts runs and makes no provider call of its own, so a limit it meets is met
by a run it started. That the three above are all of them is checked rather than
asserted — `TestEveryProviderInvocationAccountsForAnExhaustedLimit` sweeps the
tree and fails on a provider invocation with no account of what an exhausted
limit does to it.

## Waiting out an overloaded provider

A provider whose own servers are transiently overloaded refuses the same way an
exhausted limit does — the work is never judged, only declined — so it takes the
same machinery rather than a second one of its own. The difference is the clock.
An overload quotes no reset time and lifts in seconds rather than hours, so a run
waits `execution.server_overload_pause` — ninety seconds by default — and
reissues, instead of parking for the half-hour probe interval a usage limit uses.
Everything else is shared: the deadline is durable before the wait starts, the
wait spends the same `execution.usage_limit_max_pause` budget, and an overload
that never lifts therefore walks into that maximum and stops with a blocker
rather than reissuing forever. [Releasing a wait early](#releasing-a-wait-early)
below covers one of these exactly as it covers a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that on its own, and the harness does not duplicate the wait. What it
does act on is the terminal result the CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because at that point the provider
has stopped retrying and somebody has to. An overload is the only terminal
`api_error` that becomes a wait;
[the rest of them](#when-the-provider-dies-mid-run) become a relaunch.

## Releasing a wait early

Everything above honors the recorded deadline as an upper bound, and a restart
mid-wait serves the rest of it rather than asking again, which is what keeps a
crash from retrying straight back into a window that is still closed.
`yoyo resume` with a work item named is the one thing that overrides that
deadline, and it overrides nothing else:

```sh
./bin/yoyo resume yoyodyne-ifd.53
```

(With no work item named it is the other half of
[`yoyo pause`](#pausing-everything-and-resuming-it) and lifts the operator's
hold over everything instead. Both are the same act — stop waiting and carry on
— and what the argument says is whose decision is being withdrawn: the
provider's refusal of one run, or your own hold over all of them.)

It exists because the deadline is a claim about the provider and you are the one
who can change what it is a claim about. Raise the account's capacity while runs
are asleep against an 18:50 reset and that reset has stopped being true; a run
waiting out a limit its owner has already lifted is autonomy working against
them. The command moves the next probe to now and does nothing else. In
particular it does not stop anything: killing a waiting run leaves a cancelled
run whose item stays claimed, and recovering from that means reconciling,
reopening the item, and developing it again from scratch. Released, the run
keeps its claim, its branch, its worktree, and its developer session, and a
process already asleep on the wait acts on the release within seconds. If the
provider still refuses, the run records the new report and waits again, so the
worst a premature release costs is one refused request. It is refused when the
named item has no run in flight, or has one that is not waiting on the provider
at all, because a release recorded against a run that is not waiting would be
acted on by whatever pause that run took next.

## When the provider dies mid-run

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it simply dies: the API answers with an error its own retry ladder did
not outlast, or the connection carrying the response goes away before the reply
is finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. Nothing was judged and nothing is wrong with the
change; the run just stops existing. That used to fail the run outright and leave
a person to reconcile it, reopen the item, and launch it again — twice in the
week before this was built.

The run relaunches itself now. The dead invocation is reissued in the same
worktree and the same developer session, up to
`execution.transient_relaunches_before_blocking` times — two by default — and
then the run carries on as if nothing had happened. Continuing the session is
what makes this cheap rather than merely automatic: an attempt that died
mid-response had already made part of the change, and the relaunch picks that up
instead of asking a developer to derive it a second time. There is no wait
attached, because there is no condition to wait out: a dropped connection is
already gone, and the provider's own retries are spent before the harness sees
the terminal.

The provider contradicting itself is in the same class. A stream that ends one
invocation twice — two terminal results, where there was only ever one ending —
judges nothing either, and the second of them is quite often the real one: a
subagent's completion carrying a terminal's marks is read as the invocation's,
so the run's own ending arrives looking like the duplicate. Because neither
ending can be told apart from the other, the invocation is not trusted to have
produced an answer at all, and it is asked again in the same session rather than
published. That used to fail the run outright as a malformed stream, which is
how a change that was all but finished came to be recovered by a triage rerun.
Both endings stay in the run's event log, so what the provider's dialect drifted
into is diagnosable afterwards. A stream the harness genuinely cannot read still
fails the run.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather one run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer either way, so a relaunch spends no repair attempt — the change is not
what went wrong.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.
A run that spends the budget stops and records a blocker on the work item naming
the provider's own last message. That is the only case a person sees. Setting the
bound to `0` restores the earlier behavior: the first provider death ends the run.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says so plainly — no check failed, no reviewer asked for
repair, nothing here says the change is wrong — which is what tells you to pick
the work up rather than replan it. A run killed inside its repair loop names the
repair attempts it had spent, the check that was failing, and the findings it was
answering, and says the provider is what stopped it rather than that verdict:
the evidence is unresolved rather than dismissed.

A refusal that *would* stand is not relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — would earn the identical answer on the next attempt, so
it fails the run exactly as it always did. So does a 529, which is
[a wait](#waiting-out-an-overloaded-provider) rather than a relaunch, and so does
any terminal the API did not report at all. The invocation ended twice is the one
thing outside the API's own errors that still relaunches, because it is not a
verdict on anything — it is the provider failing to say what its verdict was.

## When a provider stalls or runs out of budget

A provider invocation is bounded by two separate questions, because one deadline
cannot answer both. Whether it is stuck is answered by activity: the harness
already stamps every event it parses, so a gap of five minutes with no event at
all means nothing is happening, and the invocation is stopped as stalled.
Whether it is worth continuing is answered by a total budget of four hours,
because an agent can stay live and unproductive — retrying, looping, thrashing —
and no liveness signal will ever catch that. An agent that emitted a tool result
seconds ago is demonstrably working, so elapsed time alone never stops it. Both
stops leave the run in flight rather than failing it, exactly as a usage-limit
pause does: the worktree, the branch, the claimed Beads item, and the developer
session are all preserved, and running `yoyo run` on the same item continues
that run — the developer resumes its session, and a stopped review is simply
asked for again without redeveloping the change or spending a repair attempt.
The reason is reported as what it was, a stall or an exhausted budget, and
neither is ever described as the agent having reported a failure, because it
reported nothing. Only a stop with nothing to continue from — no session, no
worktree — ends the run, and it still says the harness stopped the provider.
Short Git commands keep their flat deadlines, which is the right bound for a
command whose duration is known.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got
to. `yoyo reconcile` settles what it left behind, and then converges your local
state onto what the forge has:

```sh
./bin/yoyo reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes
the run's own remaining step or hands the item to you. A run it settled into an
ending that is not success is reported twice over: what the sweep did with it,
and — in the same words `yoyo status` uses — what became of the run and what
remains of its change. Those are different facts, and only the second answers
whether your work is still there. A run whose work landed says only what the
sweep did, because a successful run removes its branch and worktree on purpose
and there is nothing preserved to report. It also builds the triage
docket on the way past, so a run it stopped and a publication the forge quietly
never merged reach the development manager rather than waiting for somebody to
go looking. A run whose work reached
the target branch is closed and its worktree and branch removed, including when
the run died before it could record the promotion. A run stopped anywhere
earlier becomes a durable blocker naming the branch and worktree that were
preserved. A run that finished with its merge queued at the forge is settled
here too: reconcile asks the forge and, once the merge has landed, finishes the
publication — merge commit recorded, remote branch deleted, and your local
target branch caught up onto the merge commit the forge made — and closes the
work item, which the run deliberately left open because a queued merge is a
publication nothing has confirmed. Settling a merge
is complete on its own that way rather than leaning on the sweep below, so a
checkout is never left behind by which command somebody happened to run.

Two settle-path outcomes leave a publication outstanding for a person, each
with its own line on the work item. A merge the forge **dropped** is the
first: something the base branch required went unmet, the harness does not
merge past a requirement, and nothing about that publication is confirmed — so
the item is handed back to you with a blocker rather than closed as integrated,
which is also what puts it where a bounded re-arm of the dropped merge can be
decided. A
merge that **landed but could not be confirmed** is the second: the forge
performed it, and the steps that confirm it — verifying the remote carries the
promotion, recording the merge commit, retiring the consumed branch — failed,
so the record honestly says the publication is not settled even though the
merge is real. In both, your local branch is deliberately left where it is
rather than moved on a publication nothing verified. A catch-up the settle
could not make is neither of these: it is ordinary, the run settles, and the
convergence sweep below finishes it on the next pass. Other reports on this page
still reach you when the evidence demands it — a preserved blocker, a diverged
remote, a catch-up that could not finish — but none of them asks reconcile to
exercise judgement: it reports and leaves the decision where it belongs.
Reconcile never invokes a provider either: a lost process handle is not a
reason to start a second developer for an item.

Every other publication is re-asked about on the same sweep. A run that ended
without its publication settled — one that failed before it integrated
anything, or one whose request the forge merged after the harness had stopped
watching — used to keep whatever the forge last said at the moment the run
ended, for good: a pull request somebody merged days later stayed recorded open
and unmerged, and the triage docket and the status surfaces read that rather
than the truth. Reconcile now asks the forge about each of those and records
the answer — merged, closed, or still open. It only writes the record: nothing
is merged, nothing is closed, no branch moves, and the work item is not
touched, so a request that turns out to have merged outside the harness leaves
its publication outstanding for triage rather than being finished behind your
back. A record the forge agrees with is left exactly as it is, and a merged one
is never asked about again — merged is the one answer a forge does not take
back. A record left alone for a reason, such as a branch the forge answers
about with some other request, is reported and is not a failure; a forge that
could not be reached is, and the next sweep asks the same question again.

Once the runs are settled it converges local state, which is the rest of the
post-merge hygiene you would otherwise do by hand. Every target branch the
harness knows about is caught up onto its remote counterpart — the same
fast-forward the settle paths make, for a target left behind by something no run
is going to finish, or a catch-up that was held at the time — and every settled
run's leftover branch whose work the target already carries is deleted. Both
refuse on evidence rather than on a record: a remote that has diverged from
your local branch is reported for you to decide rather than reconciled — the
steps for deciding it are
[here](#unwedging-a-target-branch-that-diverged-from-the-forge) — a
branch carrying work nothing promoted is
kept, and a branch a checkout still holds is left alone. Catching a branch up
takes that branch's promotion lease, so it never races a run promoting into it.

The same sweep retires the leftover checkouts, which is what makes the worktree
registrations a machine carries live runs plus a bounded tail rather than
something that grows with the harness's history. That growth is not cosmetic: an
agent's sandbox profile denies every registered worktree path on every command it
spawns, so a machine that keeps them all eventually cannot spawn a command in its
next worktree at all — no `make check`, no `go test`, nothing. Settled runs past
the most recent few have their checkout unregistered, and registrations whose
checkout is no longer on disk are pruned, whichever run or person left them
behind. A run still in flight is never a candidate — that is a live developer's
checkout. Each retirement is taken under the run's own lease and written onto its
record, and so is a checkout the sweep finds already gone — removed by you, or by
an external `git worktree prune` — so `yoyo status` and the triage docket stop
advertising a directory that is not there rather than sending you after it.
Because the sweep is part of `yoyo reconcile`, this is owned and recurring rather
than something anybody has to remember.

Nothing is lost by it, including the case that made this worth doing carefully.
Most preserved checkouts belong to runs that stopped without promoting anything,
which is the population most likely to have a half-finished change sitting in the
working tree — and that change is the one thing no branch, commit, or record
holds a copy of. So the sweep moves it rather than declining to act: the tree is
recorded on `refs/yoyodyne/preserved-work/<run-id>` and proven to be there, and
only then does the directory go.

```
/…/worktrees/yoyodyne-ifd-140-a1b2c3d4 retired: run run-4f2a…9c1b is settled
  uncommitted work preserved at refs/yoyodyne/preserved-work/run-4f2a…9c1b
```

That ref is on the run's own record too, which is where to look months later.
Open it as a checkout again with `git worktree add --detach <path> <ref>`, or
read it with `git show` and `git diff`. It is deliberately not a branch: a branch
would be swept by the branch sweep above, listed by `git branch`, and answer the
containment proofs the harness makes about run branches. A capture that cannot be
written leaves the checkout exactly where it was, reported as kept with the
reason — as are the other things the sweep will not touch, a directory Git is not
managing and a registration on a branch its run never recorded. Those are
anomalies rather than a category: a `yoyo reconcile` printing one is telling you
about something that should not be there.

The one thing the sweep costs is `/continue` on a stoppage past the tail, which
needs the checkout it was going to hand back. The branch is still there and so is
the preserved work, so replanning or re-running the item is not affected.

Repeating the whole thing is safe — a settled run is no longer outstanding, a
branch already level with the remote has nothing to catch up to, and cleanup
over artifacts that are already gone does nothing. A run another process still holds
is left to that process, and a run `yoyo run` can continue on its own — one
inside its repair loop, one paused for a provider usage limit, one whose
provider the harness stopped on time, one paused for an [unresolved
directive](conversation.md#directives-and-the-work-they-pause), or one parked on an
[operator pause](#pausing-everything-and-resuming-it) — is left exactly as it is
for that command to pick up.

## Unwedging a target branch that diverged from the forge

Every catch-up and every promotion here is fast-forward-or-nothing, so a local
target branch and the remote's having both moved is the one repository state the
harness will not decide. You see it as the same line on every sweep:

```
main not caught up: main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right
```

and until it is resolved every run that reaches integration for that target
stops with both branch positions named rather than promoting into it. That
refusal is deliberate — the alternative is a promotion nobody can publish and an
item closed as integrated against it — but it does mean the branch does no more
work until you say which history is right. Nothing sweeps it away in the
meantime, and no later `yoyo reconcile` resolves it.

Runs that predate the fix in `yoyodyne-ifd.177` could produce this by losing a
cross-machine race after promoting, and a repository still standing in that state
is what this section is for. A run today cannot produce it that way: it settles
where the remote target stands before promoting, and stops without closing
anything if the remote moves afterwards. Reaching it now takes somebody pushing
to the target directly, or the window a queued merge leaves open. The recovery is
the same either way, and it is yours to run.

**Which side is which.** The remote is the shared truth: the forge has it, and so
does every other checkout of the project. The commits your local branch has that
the remote does not are promotions this repository made and never published —
reviewed and integrated here, and nowhere else. Keeping the remote's history and
preserving those commits on a branch of their own is the only resolution that
discards nothing, and it is the one below. Do not resolve it the other way by
force-pushing your local branch over the remote: that throws away whatever the
remote gained, which is by definition work this repository has never seen.

1. **Stop the harness spending, and check nothing is mid-promotion.**

   ```sh
   ./bin/yoyo pause
   ./bin/yoyo status
   ```

   `pause` keeps new attempts from starting. `status` is what tells you no run is
   in the `integrating` phase: a promotion already under way holds that target's
   promotion lease, and moving the branch underneath it is exactly the race the
   lease exists to prevent. Wait for anything integrating to finish.

2. **See what each side has that the other does not**, so you are deciding about
   named commits rather than two hashes:

   ```sh
   git -C <repository> fetch origin main
   git -C <repository> log --oneline origin/main..main   # promotions the remote never received
   git -C <repository> log --oneline main..origin/main   # what the remote gained meanwhile
   ```

3. **Preserve the local-only commits on their own branch**, so nothing you are
   about to move away from becomes unreachable. Naming it after the commit makes
   the step safe to repeat:

   ```sh
   git -C <repository> branch diverged/main-$(git -C <repository> rev-parse --short main) main
   ```

4. **Put the target back onto the shared truth.** When the primary checkout is not
   on the branch, move the ref as a compare-and-swap on the commit you read in
   step 2, so a branch that moved since loses the race rather than being
   overwritten:

   ```sh
   git -C <repository> update-ref refs/heads/main <remote-commit> <local-commit>
   ```

   When the checkout is on the branch, confirm there is nothing uncommitted first,
   because the move discards changes to tracked files:

   ```sh
   git -C <repository> status --porcelain    # empty, or only your declared exports
   git -C <repository> reset --hard origin/main
   ```

5. **Let the harness go again, and confirm the wedge is gone.**

   ```sh
   ./bin/yoyo resume
   ./bin/yoyo reconcile
   ```

   The held catch-up should be absent from the sweep, and runs for that target
   promote again. That is the state this recovery is for: resolvable, and back
   under the harness.

6. **Decide what happens to the preserved branch.** Its commits carry work a
   reviewer approved and this repository integrated, which the shared remote never
   received; the work items behind them carry a `Publication outstanding` line
   naming the pull request that was never merged. Open a pull request from the
   branch yourself, or file work to redo it, and delete the branch once you have.
   Nothing sweeps it for you: it is preserved work, and the convergence sweep only
   ever removes a branch whose work the target provably carries.

## Where the harness stands: the four lines

`yoyo status` opens with four lines, and prints all four every time:

```text
Running (2 developer runs):
  yoyodyne-ifd.194 — developing, 12m elapsed, $3.41 so far
  yoyodyne-ifd.201 — reviewing, 3m elapsed, cost unknown (its event log is gone)
Working (1 conversation):
  product-manager — product-manager, a turn in flight for 40s after 270 recorded turns
Not startable (2 of 7 admitted items):
  yoyodyne-ifd.200 — waiting on yoyodyne-ifd.199
  yoyodyne-ifd.212 — parked, so no pull selects it however far the queue drains: the design is being reworked
Needs a human (1):
  directive-4f2c… is unresolved: which branch does this land on? — the operator's — the work it affects waits until `yoyo directive resolve` settles it
```

- **Running** is the developer runs in flight, each with its item, the phase it
  reached, how long it has been going, and what it has spent so far. A run whose
  evidence cannot be priced says so; it is never reported as free.
- **Working** is the persona conversations with a turn in flight, which nothing
  counted before this: a conversation is not a run, so a machine spending money
  on six persona turns used to report nothing running at all. The advisory hold
  is what decides, because it is the only thing that actually knows. It is
  observed rather than taken: the process holding a conversation writes down
  which process it is, and a reading checks that the process is still there. A
  status that took the hold to find out — which is how this was first built —
  would refuse a chat that asked for its own conversation in the same instant.
- **Not startable** is each admitted item nothing will pull, with the refusal
  that stops it — the queue's own account where the queue has one, the directive
  where a directive pauses the work, and otherwise what has stopped the harness
  choosing at all. That last one comes from a closed set of named reasons, each
  of which says whose move it is: the operator's hold, a held intake, every
  developer slot taken, a live watch session that has found nothing it can
  start, no watch session running any more, and a product no session has ever
  watched. An idle session and no session are named apart on purpose — telling
  you to start a session you are already running sends you to the wrong place.
  It never comes from a watch session's memory of what it has already tried,
  which is a fact about one process rather than about the product. Work that is
  admitted and would be started next is not listed here at all; the count of
  admitted items beside the heading is where it shows.
- **Needs a human** is always present, and says either `nothing` or the list with
  whose move each one is: the operator's two switches, an unresolved directive, a
  proposed change nobody has decided, a run that ended still owing a step, work
  marked for a conversation rather than for a run, and a queue nothing is pulling
  from — a session sitting idle over it, or no session at all — while admitted
  work waits behind that. A stall over an empty queue is not listed: it is a
  state of the machine rather than something waiting on you.

A line with nothing in it says `nothing` in words, and a line whose records could
not be read says that instead — never `nothing`, which would be a confident
emptiness assembled from a file nobody could open. There is no fifth line and no
residual bucket: a state that will not render into these four is a bug in the
state.

Naming an item leaves the four lines out. They are about the product, and a
question about one piece of work is a different question. `--json` carries the
same derivation under `standing`, so a second surface reads the answer rather
than parsing the rendering.

## What became of the runs, and what remains of them

Under the four lines, `yoyo status` reads back what the runs themselves recorded
— newest first, the work item, the outcome and the phase the run reached, what
remains of it, what it cost, why the item was chosen, and the reasons its record
kept:

```sh
./bin/yoyo status                    # the four lines, then the twenty most recent runs
./bin/yoyo status --failed           # only the ones that did not succeed
./bin/yoyo status yoyodyne-ifd.90    # one item's runs, without the four lines
./bin/yoyo status --limit 0 --json   # every recorded run, for a script
```

The listing below is `./bin/yoyo status --failed --limit 2`:

```text
runs that ended without succeeding, 2 of 9 shown (137 run(s) recorded):
run-19dc9dff153e1eb89a2470f78f02f240 yoyodyne-ifd.1.7 started 2026-08-16T18:02:11Z [stopped, developing, work preserved] $4.62
  selected by the operator: the operator ran this item by name from the command line
  ran under default, configuration cfg-9f2c41ab7e05, harness 9870df6a1b2c
  reason: the provider ended this run without judging the work after 3 of 3 permitted relaunch(es)
  preserved branch: yoyodyne/yoyodyne-ifd.1.7/19dc9dff
  preserved worktree: /Users/you/Library/Application Support/Yoyodyne/state/worktrees/yoyodyne/yoyodyne/yoyodyne-ifd-1-7-19dc9dff
  preserved developer session: 0f2c41ab-7e05-4c3d-9a1b-6e8f0d2a4c71
run-c81f0a4d7c2b41e6a0f9d3b5e7104c22 yoyodyne-ifd.63 started 2026-08-15T11:47:03Z [failed, no artifacts recorded] $12.80
  selected: no reason recorded
  ran under an account the record does not name, configuration a configuration the record does not name, harness a build the record does not name
  reason: create isolated worktree: primary checkout is not ready for integration
7 further run(s) are not listed here; --limit reports more, and 0 reports all of them
each reason is shown as one line; --json carries what the record holds in full
```

The word in the brackets is what became of the *work*, not of the attempt, and it
comes from a small fixed set:

| word | what it means |
| --- | --- |
| `succeeded` | the work landed |
| `stopped` | it ended on a durable blocker: the item carries it, a person decides what happens next, and nothing was discarded |
| `cancelled` | something stopped it rather than judged it — the operator, or a killed process |
| `timed out` | the harness stopped it on time, leaving nobody anything to act on |
| `failed` | it ended without succeeding and without leaving anybody a blocker |
| `pending`, `running` | it has not finished |

`stopped` covers every ending the harness hands to somebody: an unrepaired
review, a check that kept failing, refused protected paths, a replay the target
branch outran, a provider that would not carry the run, and a promotion the
target branch turns out not to carry. The phase beside the word says where it
stopped and the `reason` under it says what stopped it, so the one word never has
to carry all six. This used to be one word — `failed` — for all of them and for
the two below it, which is how three preserved runs came to read as three
discarded ones.

A blocker outranks the run's own status, `succeeded` included. The last of those
endings is the one where that shows: a run promotes its work, records it, and
`yoyo reconcile` then finds the target does not carry the promotion, so the item
goes back into a person's hands while the run's record keeps the status it wrote
for itself before anything contradicted it. `--json` shows both — a `status` of
`succeeded` beside an `outcome` of `stopped` — and the outcome is what became of
the work.

Beside it, every run that did not succeed says what remains: `work preserved`,
`work removed` where the harness recorded removing the artifacts, or `no
artifacts recorded` where the record names neither. The preserved branch,
worktree, and developer session are then named under the run, so looking at the
change is not a trip through the run's JSON for a path. A successful run removes
what it made by design, so it says nothing about preservation at all; a run still
in flight holds everything it has.

The third phrase states an absence rather than claiming the run made nothing —
the same discipline as the `selected: no reason recorded` and `an account the
record does not name` lines below, and for the same reason: a listing that turns
an empty field into a reassurance is the failure this one exists to remove. In
practice it is a run that broke before it got a worktree, which is also why the
second run above has no phase between the two words: the phase is only recorded
once the worktree exists, so any run carrying one has a branch and a worktree and
reports `work preserved` or `work removed` with the paths underneath.

The `selected` line is on every run, including — in those words — a run that
recorded no reason at all. That is deliberate: work the harness chose and cannot
account for is exactly what you most need to see, and a line left out would read
as a reason you had already looked at rather than as one nobody wrote.

The `ran under` line beneath it is the same shape of fact and is printed for the
same reason: which provider account the run spent, the revision of the
configuration that set it up, and the revision of the harness that dispatched it.
A project with one account reads `ran under
default`; a pooled one reads whichever account the pool served that run, which
is what makes a rotation something you can see rather than infer. The revision is
a digest of the effective configuration, so two
runs carrying the same one were configured identically and a run whose
configuration was edited under it is distinguishable from one that was not;
`yoyo config show` prints the revision in force. A run recorded before any of the
three was carried says so, in those words, rather than showing a blank.

The `harness` on the end is a Git object name, shortened here and carried whole
by `--json`. It is there because a process runs whatever binary it was started
with while the harness moves on underneath it, so a run that behaved like a build
from before the fix is otherwise indistinguishable from a fix that does not work —
which is how a week of deployment defects came to be read as code defects. A
binary installed without the stamping records none, and the line says so rather
than inventing one: a comparison nobody can make is an answer, and a comparison
made against the wrong commit is not.

Each of the other reasons is printed under the run it belongs to and named for
what it is, because the records keep them apart deliberately. Only `reason` is the
run's own account of why it ended. An `outstanding publication`, an `outstanding
cleanup`, a `failing check`, and a `completion recorded late` are recorded around the work,
and a run can carry one of them with its change already promoted. The last of
those is the class whose work-item note is itself unreliable — recording that
note is part of what was failing — so the run record this verb reads is its
authoritative home.

`outstanding` in the brackets marks a finished run that still owes somebody a
step, and the `outstanding:` line under it says which — cleanup that is not
recorded as finished, or a merge the forge queued and nothing has settled — so
the marker is never left for you to go and interpret out of the run's JSON.
[`yoyo reconcile`](#recovering-interrupted-runs) is what settles either. The
marker is said only of finished runs: one still in flight owes its own remaining
steps by definition.

Naming an item reports one more thing under its runs, because it is the one
question no run can answer: what that item has cost and what it has been given.
Every budget a run spends starts again at zero in the next run, so an item handed
back, run again, and handed back again is an item nothing bounds. The per-item
counters are what bound it:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 of 2 permitted
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

Every figure here is a budget, and the first line counts what has been spent
rather than how many times somebody looked. Only three of the development
manager's six decisions spend anything — a repair grant, a re-run, a merge
re-arm — so an item it escalated or told to wait shows `triage has spent nothing
on it` and zeroes across the rest. **That is not evidence nobody looked.** The
decision itself is recorded on the work item, which is where to read whether
stopped work has been decided and what was decided; an escalated item is blocked
there as well.

A **round** is a reviewer verdict that sent a developer attempt back, counted
across every run of the item. A re-review no developer attempt produced is not
one, so a promotion that [loses its race](configuration.md#losing-a-race-for-the-target-branch)
and gets a fresh verdict on the replayed change is not charged for it, whichever
way that verdict goes. Neither is a verdict that approved the change: the cap
stops an item buying the same argument another round, and an approval ends the
argument. An approval is still recorded rather than passed over, because the two
exclusions are one mechanism — an attempt already answered about is charged at
most once — and a promotion only ever follows an approval. Rounds are what runs
actually spend, and every run records them.

The lines under it are the budget for what triage can decide about work that did
not land — another go at the change, a re-run, a re-armed merge — and they move
when [the development manager decides one](conversation.md#deciding-what-becomes-of-stopped-work).
Each is recorded before the action it counts takes effect, so a crash cannot
double-grant, and each is refused once its budget is spent. A grant and a re-run
are each once per item and are also refused by the rounds — the grant truncated
to what the cap still has room for, the re-run refused outright once none
remain — and a merge re-arm is bounded on its own because it buys no round at
all. The rounds alone would bound neither of the first two on an item whose runs
keep stopping before a reviewer ever sees them. The
numbers are the `triage` keys and the integration retries in [the configuration
guide](configuration.md#what-one-work-item-has-been-given). An item triage
has spent more than one pass on says so in the first line, which is the fact
worth looking for: work that keeps coming back is usually work where something
other than the change is wrong.

The listing folds each reason onto one line and bounds it at 160 bytes with
an ellipsis, never cutting mid-character, so a reviewer's whole verdict does not become the listing;
`--json` carries what the record holds in full, along with the same figures.

Cost comes from the same recorded evidence [`yoyo cost`](reporting.md#what-the-work-cost)
prices from, so a run still going reports what it has spent so far, and one
whose event log no longer survives reads as `cost unknown` rather than as free.

Reading a run decides nothing about it, so this holds nothing and settles
nothing: a run another process is executing is listed exactly as a finished one
is. Reporting a failure is not itself a failure either — the exit status says
whether the records could be read, so a script can read this without guarding
against the answer.

## What became of `bin/yoyo-status`

`bin/yoyo-status` was a shell script that read the state directory itself and
followed the event stream a run, a conversation, or a
[branch review](work.md#reviewing-what-a-branch-adds-up-to) records. It is a
wrapper for `yoyo status` now — it passes its arguments on and does nothing else.
The architect ruled that it is an operator surface like any other, so
[surfaces-project-one-read-model](decisions/invariants/surfaces-project-one-read-model.md)
binds it, and a script deriving run, conversation, branch-review, and exchange
state for itself in `jq` and `awk` is the second surface that invariant forbids:
two answers to one question is a disagreement only you can settle. The name is
kept because it is the one an operator types daily, so typing it still lands on
the surface that answers now.

```sh
./bin/yoyo-status            # the same thing as: yoyo status
./bin/yoyo-status --failed   # every argument is passed on unchanged
```

The id it takes is a work item's rather than a run's or a conversation's, which
is the one thing that changed shape: naming a run or a chat by an id prefix was
this script's own way of selecting a stream, and there is no stream to select.

Its own flags are gone, and typing one says so and says what answers that
question now rather than failing with an unknown flag:

- `-l` is answered by
  [`yoyo status`](#what-became-of-the-runs-and-what-remains-of-them), which
  reads back every run — one still in flight included, with the phase it has
  reached, how long it has been going, and what it has spent so far — and names
  the conversations with a turn in flight on
  [its *Working* line](#where-the-harness-stands-the-four-lines). Branch reviews
  it does not list.
- `-c` is answered by [`yoyo cost`](reporting.md#what-the-work-cost), which
  prices each work item from the runs made for it and carries what the roles
  spent asking each other on a row of their own.

**Two things it did are done by nothing now, and are worth knowing before you
look for them.** It followed an event stream as the events arrived, which is the
closest thing there was to watching an agent work; `yoyo status` reads the
records back and does not follow. And it priced conversations and branch reviews
beside runs, grouped by the day the money was spent on; `yoyo cost` prices work
items and exchanges, so what a conversation turn or a branch review cost is
recorded but is no longer reported by any surface. Folding both into the binary
is `yoyodyne-ifd.63`, which is blocked rather than landed.

[`scripts/yoyo-status-test.sh`](../scripts/yoyo-status-test.sh) checks that the
wrapper delegates, that a retired flag says where its capability went, and that
no derivation has crept back into it. It stubs `yoyo` rather than building one,
so it needs no provider, no repository, and never reads your real state. `make
test` runs it, so the tool is held to this by the same command as everything else
in the repository rather than by one somebody remembers.
