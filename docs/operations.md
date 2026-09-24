# Operations and recovery

*For an operator recovering from a stall, a crash, or a provider refusal. Part
of [yoyo's documentation](../README.md#further-reading).*

## Starting the product, and stopping it

A person starts the product once, with one verb, and stops it with one:

```sh
yoyo start    # the supervisor, and through it every part the configuration enables
yoyo stop     # the supervisor, then every part, in order
```

Slack, the scheduler, the dashboard, and the maintenance pass are parts of one
product rather than tools each started by hand, and the configuration's
[`services`](configuration.md#services) section is where a product says which
of them it runs. `yoyo start` starts the product's supervisor — one process per
product, detached into a session of its own so it outlives the terminal — and
the supervisor reads that section and starts every enabled part the way that
part is started alone: the Slack sink exactly as
[`yoyo slack ensure`](#checking-the-installation) starts it, from this product's
own stored tokens into that one process and nowhere else; the scheduler as
[`yoyo work --watch`](work.md#letting-the-harness-choose-the-work) under its own
watch lease. Each start is lease-checked, so a part that is already running is
taken as it is rather than started twice, and a part started here holds exactly
what it holds started by hand. The verb waits for the supervisor to record what
came up and says so, one line per part:

```text
started the supervisor for yoyodyne as pid 48211, logging to …/products/yoyodyne/supervisor/supervisor.log
  slack: running as pid 48214, logging to …/products/yoyodyne/slack/sink.log
  dashboard: enabled, and not yet a child of the supervisor: its adoption is yoyodyne-ifd.414; until that lands, start it with `yoyo dashboard`
  scheduler: running as pid 48215, logging to …/products/yoyodyne/scheduler.log
  maintenance: enabled, and not yet a child of the supervisor: the periodic pass is yoyodyne-ifd.413; until that lands, `yoyo reconcile` is scheduled by hand
stop it with `yoyo stop`; `yoyo status` says how each part stands
```

Two of the four parts are declared and not yet started by the supervisor, and
the line says which work adopts each: the dashboard is `yoyodyne-ifd.414`, and
the maintenance pass — the resident that replaces the hand-rolled job — is
`yoyodyne-ifd.413`. Until those land, `yoyo dashboard` and a scheduled
`yoyo reconcile` are still yours, and the supervisor says so rather than
starting a part it does not know how to.

**A second start while the product is running says so and does nothing.**
Whether a supervisor is running is its lease's answer, an advisory lock the
operating system drops when its holder dies, so a supervisor that was killed
leaves nothing to clean up and the next `yoyo start` simply starts one.

**The supervisor keeps each part running, within bounds.** It looks at every
part every few seconds. A part that dies is started again after a backoff that
doubles from a second and is capped at thirty; a part that dies within two
minutes of a start five times in a row is, on the sixth, left down and shown as
**degraded** — on `yoyo status`'s "Needs a human" line, with the reason the
supervisor recorded and who it is waiting on, and in the channel's hourly lines,
which read the same model:

```text
Needs a human (1):
  the scheduler service is degraded: died 6 times within 2m0s of being started, most recently at 2026-09-19T12:03:00Z, so it is left down — the operator's — the supervisor has stopped restarting it; fix the cause, then `yoyo stop` and `yoyo start` bring it back, or start the part by hand and the supervisor takes it back
```

A part that cannot be started at all — the Slack service with this product's
tokens not stored — is degraded at once with that reason rather than the bound
being spent finding out five times; `yoyo doctor` names what to store. A part
that ran longer than two minutes and then died is not a part that cannot
start, so its count begins again. `yoyo status --json` carries the whole record
under `standing.services`: whether a supervisor is running, and each part's
state, process, log, and reason.

**The supervisor's own death leaves the parts running.** They are processes of
their own with recorded presence — the sink's presence record, the watch
session's holder stamp — and the next `yoyo start` finds each through its lease
and takes it back rather than starting it again; the line says `running,
reattached`. A part somebody starts by hand while the product is up is taken
back the same way, which is also what brings a degraded part back once its
cause is fixed.

**`yoyo stop` stops the supervisor first**, so nothing restarts a part on its
way down, and then the parts in the reverse of the order they were started in,
waiting for each to let go of its lease. A part that is not running is reported
so, and the parts are stopped whether or not a supervisor was running — a
supervisor that died left them running, and this is what stops them. One thing
to know before typing it: stopping the scheduler cancels the runs it is hosting,
as stopping a watch session always has, and [`yoyo reconcile`](#recovering-interrupted-runs)
settles what that leaves. When what you want is for the runs to keep what they
have and carry on later, [`yoyo pause`](#pausing-everything-and-resuming-it) is
the verb and the product stays up.

Nothing starts the product with the machine yet: `yoyo start` is typed, once,
and the launchd job that runs it at login is the resident item,
`yoyodyne-ifd.413`, whose form is `yoyo start --foreground` — the same verb,
being the supervisor in the calling process rather than detaching one.

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
checks and whether this machine can run the programs they name, Node where the
product ships the dashboard — whose page only Node can draw, so a machine
without it fails the page's render check rather than passing quietly — each provider
your agents name — installed always, and authenticated where the harness has an
adapter that can ask, which today is Claude Code — whether a provider key is
exported in this shell, which is an authentication the harness no longer
carries, whether every agent runs on
one model with nothing to fail over to, forge access when the project publishes,
when reporting is on, this project's own Slack secrets and the sink that is
supposed to be using them, and each part the [`services`](configuration.md#services)
section declares — off, on with what it needs stored, or on with it missing.

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

The tracker finding above is the initialized-here half of two. A machine with no
`bd` on it at all gets the other, and its remedy is the tracker's own installer,
fetched from [the one home Beads has](https://github.com/gastownhall/beads):

```text
problem  tracker                bd is not installed, and every role reads and writes the tracker
                                fix: curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash
```

That is the same repository the README, the install script, and the adoption
walkthrough send you to, and it is deliberately not a `go install` line: the
tracker moved to that home from `steveyegge/beads` and its released modules
still declare the old path, so `go install` of a path under the new home fails
on the mismatch, and a bare `go install` of the old one takes a build its own
documentation calls unsupported — which is what turned every pull request here
red on 2026-09-05. [The diagnosis](diagnoses/yoyodyne-ifd-125-6-beads-home.md)
has the evidence. `yoyo setup` hands you the same command, since setup does not
install tools.

A healthy installation says so in as many words, because an empty list of
complaints and a check that never ran read the same. It exits 1 when something
would stop work running and 0 otherwise.

**A warning is not a small problem — it is something about an installation that
works.** The `yoyo` on your `PATH` having drifted from the one you are running is
one. **Every agent on one model, and none naming an alternate** is another, under
`failover`:

```text
warning  failover               every agent runs on one model, opus on claude-code, and none names an alternate
                                a capacity window closing on that model stops every role at once until it lifts, and nothing fails over; set failover.enabled: true and failover.model on each agent …
                                fix: ${EDITOR:-vi} .yoyodyne/config.yaml
```

Nothing about it stops a run today. What it costs is paid the day that model's
window closes: on 2026-09-08 the seven-day limit on the one model all five
agents ran on closed with a reset five days off, no agent named an alternate,
and the harness waited the whole window out. [Failover](configuration.md#serving-a-turn-from-a-permitted-alternate-model)
had shipped, off by default so that each agent's alternate is a choice somebody
made, and this project had never turned it on — a condition that was in the
configuration the whole time and is one line to state. The finding is healthy
once any agent names an alternate, or the agents run on more than one model,
and the healthy line says how far the cover goes.

**Every reporting finding is a warning too**, and deliberately so: reporting is an
observation and never a gate, so a sink you never started, a workspace that is
down, and a token nobody stored all leave an installation that runs work exactly
as it would have. They are still named, in full, with the command that ends each
one — what the exit status refuses to do is fail a machine that works.

**So is every service finding.** Each part the configuration's
[`services`](configuration.md#services) section declares gets a line of its
own — `service:slack`, `service:dashboard`, `service:scheduler`,
`service:maintenance` — saying it is off, or on with what it needs in place, or
on with what it needs missing: the Slack service without this project's two
tokens stored, the dashboard with a `keychain` or `file` token that is not in
the store its entry names. The remedy is the command that stores it, and a
part that cannot start reports nothing or serves nothing rather than stopping a
run, which is why none of these is a problem.

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
running. With the Slack service enabled in the
[`services`](configuration.md#services) section, that is what
[`yoyo start`](#starting-the-product-and-stopping-it)'s supervisor does for the
sink — the same lease-checked start, made again whenever the sink dies, within
the supervisor's bounds — so on a product that has been started the finding
clears itself. The verb is still there for a product nobody has started, and
for a pass of your own. `yoyo doctor` only diagnoses — it changes nothing, and
starting the sink is the other command's job.

### Which provider authentication is supported

**A provider authenticates by its own login, held in its provider home.** That
is the one authentication an invocation the harness makes receives: `claude auth
login`, `codex login`, or — for a pooled account, which has a home of its own —
the same login with that home named, which is the command the `account:` finding
hands you.

**A key exported in a shell is not it.** `ANTHROPIC_API_KEY`,
`CLAUDE_CODE_OAUTH_TOKEN`, and `OPENAI_API_KEY` each read as a credential, and
every process the harness launches is given an environment built from an
allowlist with credentials dropped from it — so none of the three reaches a
provider invocation, a check, or a Git command. It used to: before
`yoyodyne-ifd.408` an invocation inherited the harness's whole environment, so a
key in a shell profile authenticated every one of them. An installation relying
on that does not degrade when it stops working; the provider refuses its next
run.

So the state is named before that run happens, under `provider-authentication`:

```text
warning  provider-authentication  ANTHROPIC_API_KEY is exported here and reaches no invocation the harness makes
                                  every invocation is built from an allowlist and a name that reads as a credential is dropped from it, so an installation authenticating a provider this way is refused by that provider rather than degrading; provider authentication is the provider's own login, held in its provider home
                                  fix: claude auth login
```

It is a **warning** rather than a problem, by the question this command separates
the two with. A key exported beside a provider that is signed in stops nothing —
the login is what authenticates and the key is dead weight. A key exported
*instead* of one stops everything, and that is already the `provider:` finding's
problem; naming it twice would count one broken installation twice. Read the pair
together: `provider:claude-code` saying the provider is not authenticated, with
this beside it, is the whole story — you can run `claude` in your own shell, and
the harness cannot.

Two other surfaces say the same thing, so it is not only found by somebody who
thought to run a diagnosis. `yoyo config validate` says it beside the validity
answer, on standard error, without changing the exit code, and carries the
variable names under `provider_keys` in its `--json`. And `yoyo slack` says it
once when the sink starts — the shell that starts a sink is usually the shell the
harness was started from, and that process is the one an operator leaves running.
All three name the variables and never their values.
[The environment a check runs in](configuration.md#the-environment-a-check-runs-in)
is where the allowlist itself is stated.

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
processes, where the run lands cancelled and the work has to be developed again
from scratch once
[the claim it left behind](#claims-with-nothing-working-on-them) is given back. A
conversation turn is refused
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
thing that lifts it is you. `yoyo status` — on its "Needs a human" line, and as
a banner over every one of [its live modes](#following-a-run-a-conversation-or-a-branch-review)
— and the conversation's `/status` all say when the pause was placed, because a
system somebody paused and forgot looks exactly like a system that died.

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
blocking, and every report of a held intake at a terminal names this command
beside `/release`. Releasing what is not held is not an error, an item you name
with `yoyo run` was never subject to the hold, and a watching `yoyo work` session
starts choosing again at its next poll. Placing a hold stays in the conversation,
where the reason for it can be recorded with it.

**These three verbs, and `yoyo artifact approve`, are a person's, and a
process an agent started is refused them.** Every process the harness launches
for a role carries the role it was launched for in its environment, as
`YOYODYNE_AGENT_ROLE`, on top of [the explicit environment](configuration.md#the-environment-a-check-runs-in)
every invocation is built from; the variable is under the harness's own
prefix, so a shell the agent opens and every `yoyo` that shell runs carry it
too. `yoyo pause`, `yoyo resume` in both its forms, `yoyo release`, and
`yoyo artifact approve` read it before they read anything else, and a process
that carries it is told, in a sentence rather than a permission error, whose
act this is:

```text
yoyo release is refused from a process the harness launched for the developer: a person releases intake, and an agent's process is not one
```

Nothing is written by a refused verb, and `--json` carries the same sentence
under `error`. What that stops is a mistake the system could make on its own —
a developer told to unblock the queue and reaching for `yoyo release`, or
approving the goals it was about to admit work against — which until it
existed was stopped by nothing but this page saying nobody does that. The
honest boundary is that an environment variable identifies an agent that is
being one: a shell can strip its own environment, and the refusal is then
gone. What stands behind it is different for the two kinds of record. An
approval is written into the goals document, and that document is a protected
path: a run's change carrying it is refused before any check runs or any
reviewer sees it, whatever wrote it, and that is tested end to end
(`TestAChangeRewritingTheProductsGoalsIsRefusedWithTheGoalsDocumentNamed` in
`internal/orchestrator`, with the join to the path `approve` writes pinned
beside the refusal tests in `internal/cli`). The holds live under the state
root, outside any worktree, and nothing the harness tests stands between a
stripped environment and a write there: a developer run enables Claude Code's
OS-level sandbox over its shell (`sandbox.enabled`, failing if it is
unavailable), and that sandbox's write policy is the provider's own rather
than anything the harness declares or verifies, so it is not counted on here.
A person at a shell an agent opened clears the variable and the verbs are
theirs again. The same refusal is what `yoyo gate record` will give, once the
human gate (`yoyodyne-ifd.209.20`) lands.

**A hold the brake placed asks a person for nothing until it is escalated —
by the development manager, or by the harness at the bound on its own loop.**
That was not always so: the brake tripped on
2026-09-02, 09-05, 09-13, 09-17, and 09-19, and each time the line sat held
until somebody noticed — on the last of them for about two hours, with a free
developer slot idle. The operator's decision that day, recorded as a directive,
was that the brake may trip so long as the development manager is always
invoked at once to sort it out and nothing waits. So the poll that trips the
brake also summons her [sweep](configuration.md#recurring-tasks) out of its
cadence, with the runs that blocked and the reason each blocked in the message
that wakes her, and she decides what happens to the hold: release it, keep it
and probe the line, or keep it and escalate it to you. The watching session
acts on her decision at its next poll. Where she records none by
`execution.brake_cooldown` — thirty minutes by default — the session decides on
evidence instead: it starts one probe run under the hold, and the probe landing
reopens intake while the probe blocking keeps it held, restarts the cooldown,
and puts the question to her again with the probe's own stoppage. A broken
machine is therefore probed once per cooldown and put to her each time, and a
machine that was fine is choosing again within a cooldown of the trip whether or
not anybody answered. That loop is bounded: after
`execution.brake_escalation_cycles` of those summons-and-probe cycles — four by
default, which is two hours at the default cooldown — with her not escalating
the hold, the harness escalates it to you itself, sends you one direct message
naming the cycles spent and what stopped the last probe, and starts no further
probe. So the brake hold that waits on you is one she escalated, which she
does by recording the decision and reporting it at `warning` severity so it
reaches you, or one the harness escalated at that bound. Only verdicts and
check failures against a change that was present count toward the trip — an
environmental stop, a dirty checkout or a transport that did not answer, is a
verdict on nothing and counts toward nothing, and neither does a provider
answering nobody. A
promotion refused because the target branch
[diverged from the forge](#unwedging-a-target-branch-that-diverged-from-the-forge)
is in the same class: a catch-up the harness will not make is a stop the
harness made, every run that reaches integration meets the same one until a
person settles the branches, and the brake does not trip on any number of them.
What tells you is the run's own blocker — the item is stopped with both branch
positions and the recovery steps, said in its thread at `critical` and put on
the development manager's docket, once per run rather than once per trip. On
2026-09-21 three identical diverged-target refusals tripped the brake, which
held the line a second time over the one cause the first blocker had already
put in front of a person.

The hold's own record says where it stands, and every surface reads it from
there: `yoyo status` names the hold on its "Needs a human" line with who it is
waiting on — the development manager's while she decides, with when the probe
starts if she has not; the harness's while a probe runs, naming the probe;
either of those with which summons-and-probe cycle it is and at what cycle the
harness stops asking; and yours only once it is escalated, saying whether she
did or the harness did — the watch log and the channel say the same,
`yoyo sweeps` shows the summoned pass as summoned, and the run the probe made
records the brake as what chose it. `yoyo release` still lifts a brake
hold sooner, and says what the harness was in the middle of when it did.

## Waiting out a provider usage limit

None of what follows is specific to one provider. What a provider said is read by
that provider's dialect and reduced to one of nine answers — served, retrying,
limit-reached, unavailable, interrupted, model-unavailable, unauthenticated,
unreachable, refused — and every wait below is driven
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
run still in flight instead of sleeping the next one. Two things continue it,
each with the whole bound available to its process again: running `yoyo run`
on the same item, and [`yoyo reconcile`](#recovering-interrupted-runs), which
continues such a run itself once its recorded deadline has passed and no
process holds its lease — in the same worktree and developer session, through
the same continuation `yoyo run` makes, with the run's record saying the sweep
did it. Until yoyodyne-ifd.428.5 the verb was the only continuation, and a run
nobody typed it for had exactly the shape of
[a stopped provider nothing continued](#when-a-provider-stalls-or-runs-out-of-budget):
no live process, no ending, a developer slot held and the in-flight guard
refusing every item beside it — indefinitely, because neither that settlement
nor the claim audit touches a run that is only waiting. A run whose deadline
has not passed is left as the wait it is, whether a process is asleep on it or
not, and one a live process holds is left to that process whatever the clock
says. The sweep hosts the continued run to its end exactly as `yoyo run` would,
so `yoyo reconcile` typed by hand with such a run waiting stays open for the
length of a developer attempt, saying which run it is continuing and why on
standard error before it does; `--json` writes its one document once the
continued runs have finished, with what each came to under `continuations`.
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
discipline, whether or not a deadline was quoted. A reset that is not in the
future is one the harness genuinely cannot wait for, so it stops the run and
records a blocker rather than guessing a wait. A reset that no longer fits the
run's remaining budget is different: the wait is well defined, only longer than
the harness will take, so nobody has anything to decide. The run ends cancelled
as an environmental stop of cause `usage-window` naming the reset, gives its
claim back, and keeps its branch and worktree. It spends nothing — no brake
count, no review round, repair grant, or re-run — and a watching session holds
the item under "waiting on the provider's usage window" until the reset passes,
then pulls it again by itself. An exhausted limit is not the only thing a run waits out:
[an overloaded provider](#waiting-out-an-overloaded-provider) below takes the
same machinery on a much shorter clock.

### A provider refusal outside a run

An exhausted limit is not only a run's problem, either. The harness asks a
provider for work in three places: inside a run, which parks as above; a
conversation turn; and an independent `yoyo review`, which uses the same reviewer
with no run around it. All three record the refusal — what was stopped, the limit
the provider named, when it lifts, and the model the turn was refused on, which
is the alternate where failover had already moved the turn there — and the last
two have no run to park in. What the record buys is that
[reporting into Slack](reporting.md#reporting-into-slack) says it as a `warning`
without you there, and a run that parks on the same limit is said at that weight
too. Hours in which nothing will happen is the one message a channel nobody is
watching most needs to carry, and it must not weigh the same as checks passing.

**A turn you typed waits anyway**, under the bounds above and on the same polling
discipline: it sleeps the probe interval or the time left to the quoted reset,
whichever is shorter, and asks the refused invocation again on the same provider
session. Nothing the turn had already done is done twice — tracker actions
applied by a round that finished stay applied, because what is reissued is the
single invocation the provider declined, with the prompt it was declined with.
In an interactive conversation the wait is on the activity line, saying what is
being waited out and when the turn will ask again — the end of this probe, which
is the quoted reset only where that comes sooner; `yoyo chat --message` waits the
same way, because an unattended caller is exactly the one who cannot retry by
hand. The budget covers the message you are waiting for rather than each wait
inside it, so a provider that keeps refusing reaches the configured maximum
instead of walking one message past it a wait at a time. The refusal is written
down once for as long as one message is waiting it out, so a wait is one warning
in the channel rather than one per probe — a limit whose quoted reset moves is
written down again, because that is the provider saying something new about it.
A `yoyo pause` placed while the turn is waiting is read before it asks again, so
the wait ends on your hold rather than spending through it.

What a conversation cannot do is what a run does when the wait is one the harness
will not take — a reset already behind us, or a wait past
`execution.usage_limit_max_pause` or `execution.usage_limit_in_process_pause`. A
run leaves its deadline in durable state and a later invocation continues it; a
turn has no such record to be continued from, so it fails at your terminal with
the limit and its reset time stated, the conversation stays open, and saying the
same thing again takes the turn that was refused. Setting
`execution.usage_limit_max_pause` to `0` turns the waiting off for conversations
exactly as it does for runs.

The turns the harness takes for itself do not wait: a stopped run delivered to
the development manager, a recurring task's firing, a correction, and the Slack
sink's turn each fail on the refusal exactly as before, because every one of
those already paces itself on it — the next pull, the next cadence, the thread
told what happened — and a turn that slept through the window would hold the
`yoyo work` session that took it for hours. An independent `yoyo review` still
fails on a refusal exactly as it did before too.

What those refusals add up to is read as well as each one on its own, and the
runs parked on a limit are read with them — a run's park is in its own record
rather than in the log above, and a product whose only refusals are runs
parking is held all the same. When the refusals standing cover the model every
agent's turn ends on, and at least one of them stopped a turn or parked a run
rather than being served through by an alternate, **the provider is holding
every role**, and that is said as a state rather than as one more refusal: it
heads [the four lines](#where-the-harness-stands-the-four-lines)
with the reset the provider named, it is on the attention line as your move,
and the channel [says it again while it stands](reporting.md#the-provider-holding-every-role).
It is the message that was missing between 2026-09-08 and 09-13, when 134
refusals were each said once and nothing said that all five agents were on the
one model being refused, with nothing to fail over to, for five days.

### A watch session inside a recorded window

The same two records hold intake by themselves. At every pull a watching
`yoyo work` session reads the usage-limit log and the runs parked on a limit,
and while a refusal the provider named a future reset for covers the model
every developer's turn ends on — the developer's alternate where failover names
one, and each model `execution.developer_models` maps a label to as well — it
chooses nothing: no item is pulled and no recorded triage decision is carried
out. It reads the record rather than remembering it, so a session started inside
a window honours it from its first poll. That is what was missing on
2026-09-23, when the maintenance pass restarted the watch repeatedly inside a
`seven_day` window and each fresh session pulled new items into the same
refusal: one limit, twelve failed runs.

The session records the poll as one made inside the provider's window, so
`yoyo status` opens with `Paused on the provider's usage window until …`, the
channel says the same, and the watch log's idle line carries the window and its
reset — which is what a maintenance script reads to stand its idle check down.
It is never reported as a hold: nothing needs releasing, and the first poll past
the reset pulls again. A limit with no reset named does not hold intake this
way; a dispatch is how that one is asked about. A developer model the record
does not refuse — a label mapped to a model the provider still serves — holds
nothing, because work can run on it. A pass that is not watching stops on the window
instead of waiting it out.

Selection is not a fourth place. A watching `yoyo work` session reads the tracker
and starts runs, so a limit it meets is met by a run it started, bar the turn it
takes delivering a stopped run to the development manager. That the three above
are all of them is checked rather than asserted —
`TestEveryProviderInvocationAccountsForAnExhaustedLimit` sweeps the tree and
fails on a provider invocation with no account of what an exhausted limit does to
it.

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
Setting the bound to `0` buys no relaunches at all: the first provider death is
the last. It does not turn off
[waiting a dropped connection out](#waiting-out-a-network-that-dropped) — that
is a different rule, it is not configured, and it applies at `0` exactly as it
applies at `2`.

**What happens once the budget is spent depends on what killed the invocation.**
A death nothing can classify stops the run there and records a blocker on the
work item naming the provider's own last message. A death that is plainly a
dropped connection does not: it is
[waited out and asked again](#waiting-out-a-network-that-dropped) past the
budget, on the same backoff every other transport failure gets, and only a run
that spends that whole window stops. The budget is the right bound for weather
nobody has classified; a reset connection is not that, and stopping on one is
what cost four runs their finished work.

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
any terminal the API did not report at all. Two of the API's own errors are
neither: a login the provider will not accept (`Not logged in`, a 401) and an
API nothing reaches (`Can't reach the API server`, a name that does not resolve)
are [a wait that spends nothing](#waiting-out-a-provider-nobody-can-reach)
rather than a relaunch or a refusal. The invocation ended twice is the one
thing outside the API's own errors that still relaunches, because it is not a
verdict on anything — it is the provider failing to say what its verdict was.

## Waiting out a provider nobody can reach

The operator's directive of 2026-09-18, verbatim: *"I don't want a run killed
just because the network is flaky, the laptop is asleep, or I need to re-auth a
session."* Until then a run whose provider invocation failed on an expired login
or an unreachable API was read as a transient death: it spent its two relaunches
on an answer no relaunch could change, was recorded as blocked with its work
preserved, and went on the development manager's docket. A dispatch refused at
the availability check failed outright and counted toward the intake brake.
From 2026-09-17 18:17 local the Claude Code login on the operator's machine had
expired; three runs blocked in a row, the brake tripped, every recurring pass
recorded 0 turns, and the maintenance job restarted the watch 158 times. Nothing
told him. He learned by asking, three days later.

**A provider that is not authenticated or cannot be reached is a named wait
that spends nothing.** Two conditions earn it, and they are told apart only by
what you do about them:

- **Not authenticated** — the provider will not accept the account the harness
  asks under. `claude auth status` says so before a dispatch; inside a run the
  terminal says `Not logged in`, `Login expired`, `OAuth token revoked`, or
  `Please run /login`, or quotes a 401. The remedy is you logging in.
- **Unreachable** — nothing answers at the provider's API: the machine is
  offline or asleep, or a name does not resolve. The terminal says `Can't reach
  the API server` or carries the transport's own error. The remedy is the
  network coming back, which the harness finds by asking again.

Both are read off the terminal the provider ends its stream with, and off the
process's prose when there is no terminal — its stderr, or plain text on stdout
where the stream should have been: a CLI that refuses an expired login before
writing any envelope says so there, and an attempt that died so used to end as
a process failure nobody classified — relaunched, spending the budget, and
blocked, the 2026-09-17 stall replayed. Prose is read by both adapters, only
for those two refusals, and only when the stream ended without a terminal,
stderr first; a terminal the provider did write is never second-guessed by its
diagnostics, and a process that died any other way stays the failure it was.
The channel the refusal came on is recorded on the run
(`provider_outage_channel`: `envelope`, `stderr`, or `stdout`, kept as evidence
after the wait, like the limit's kind) and on the product's outage record
(`channel`), so a run that waited says whether its provider wrote an ending or
died first.

What the wait costs is nothing, and that is the whole of the rule:

- **A run keeps everything and waits.** Its claim, its branch, its worktree, and
  its developer session are all kept. No relaunch is counted, no repair attempt
  is charged, the usage-limit pause budget is untouched, and nothing is docketed
  or blocked. The run asks again every `execution.usage_limit_unknown_reset_pause`
  — the one interval the configuration already states for "ask again rather
  than being told when" — and carries on from exactly where it stopped when the
  provider answers, with its relaunch and repair counters exactly as they were.
  There is no in-process bound and no maximum: a login you renew in an hour is a
  run that waited an hour. The next probe is durable on the run, so a process
  that dies mid-wait leaves a run `yoyo run <beads-id>` resumes rather than one
  that failed, and `yoyo resume <beads-id>` asks again now rather than at the
  next probe, exactly as it does for a limit.
- **The scheduler dispatches nothing into it.** A dispatch the provider turned
  away counts toward nothing — not the brake, not the docket, not the session's
  exclusion of the item — and `yoyo work --watch` chooses nothing while the wait
  stands, saying why. It asks the provider at every poll whether the login has
  been renewed, and the poll that finds it renewed resumes the line by itself:
  **nothing is released and nothing is restarted.** A provider nobody can reach
  is asked about by pulling into it again once the probe interval has passed,
  because nothing cheaper says whether the network is back. A drain (`yoyo work`
  without `--watch`) stops on the wait instead, since it is a command you are
  waiting on the return of.
- **A recurring task records the wait rather than a failed turn.** A firing due
  while it stands moves its cadence, asks the role nothing, and its sweep record
  says the provider is not authenticated (or cannot be reached) — so `yoyo
  sweeps` over the outage reads as the outage rather than as a column of zero
  turns. Once the probe interval has passed since the provider was last met
  refusing, a due firing is made into it anyway: the firing is the one probe
  this path has, so a machine with nothing in its backlog and no watch running
  still finds the network back on its own. A served turn ends the wait; a
  refused one re-records it, and the next firing waits the interval again.
- **The brake does not trip.** The failure-storm brake counts runs that blocked
  with nothing landing between them, and a dispatch or a run the provider turned
  away is neither. A brake tripped on this would summon the development manager
  over a change nobody judged, and prescribe a probe into a provider that is
  still away, which is why tripping it on this turned one hand step into two.

Where it stands is one record under the product, `provider-outage.json`,
written by whatever meets the provider refusing everybody — a dispatch, a run,
a conversation turn — and cleared by the first thing the provider serves again:
a developer attempt, a review, a conversation turn, or the watch's own login
check finding the machine signed in.
It is what `yoyo status` names the wait from: the banner above the four lines,
and an entry on the attention line that says who it is waiting on.

```text
The provider is not authenticated; the operator must log in: every role is waiting on it, and the harness asks again on its own until it answers; 3 turns refused since 2026-09-17T15:17:00Z (claude-code, account default)
Running: nothing
...
Needs a human (1):
  The provider is not authenticated; the operator must log in: … — the operator's — log in to the provider, or wait for the network; the harness resumes on its own once it answers, and nothing is released or restarted
```

The channel says it once the moment it is seen, tagged to the operators by
member id, and once more when the provider answers again: see
[reporting](reporting.md#a-provider-nobody-can-reach). The stall alarm does not
fire over it — a line of runs each waiting on the same login is not a machine
that died — and it does not repeat while it stands, because the banner carries
it and a message repeated about a wait you have been told about is the nagging
that gets a channel muted.

## Waiting out a network that dropped

A run touches somebody else's network at its most expensive moments: it pushes
the run branch, opens and updates the pull request, reads where the remote target
branch stands, asks the forge to merge, confirms the merge, deletes the merged
branch, catches the local branch up, and makes every provider invocation over
it. It ends by writing to the tracker, which is not a network but is a store
other processes are writing to, and a `bd` too busy to run judges the work no
more than a reset connection does. **A run in flight reads that same store at
each of its gate boundaries**, to find out what its work item waits on: at the
start of every repair round, and once more before the promotion. A `bd show`
killed under load there is the same non-answer as the write, and it reaches the
run at the moments it has most to lose — yoyodyne-ifd.436.4's change was already
approved and yoyodyne-ifd.117.1's files already lifted when the read that ended
each of them timed out. One read sits before all of those and is
[a different thing](#the-read-a-dispatch-makes-before-there-is-a-run): the one a
dispatch makes to load the item at all, before it claims anything or adopts a
run in flight.
On 2026-09-03 four runs died at those boundaries in one day, each on a single
connection reset the next attempt would have survived — completed and sometimes
already reviewed work recorded as failed — and the intake brake then held the
whole line three times because the blocked runs came one after another.

**The harness no longer fails outright on anything that can recover.** A failure
whose class says the next attempt may well succeed — a connection reset, a
network drop, a transport-level refusal — is waited out and asked again, at every
one of the boundaries above.

- **The waits are Fibonacci seconds, capped at half an hour**: 1s, 1s, 2s, 3s,
  5s, 8s, 13s and so on, reaching the cap after about seventy minutes. Cheap
  while a reset connection is still the likeliest explanation, and a probe every
  half hour after that.
- **Each boundary gets its own two-hour window**, because a network that dropped
  a push says nothing about a merge. Roughly twenty attempts fit in one.
- **None of it is configured.** The intervals are the harness's and the same for
  every product, exactly as the watching session's retry of an unreadable tracker
  is: what they measure is how long a connection that comes back takes, rather
  than anything about a project.
- **Nothing that is an answer is waited on.** An authentication failure, a merge
  the forge refused, a protected branch, a conflict, and any 4xx earn the
  identical answer on the next attempt, so they are reported as promptly as they
  always were. So is a failure whose class the harness does not recognize: the
  set is deliberately small, and anything outside it keeps the behavior it had.
  The full recoverable-versus-terminal taxonomy is the architect's, and this does
  not wait on it.

**Every wait a run takes is recorded before it is taken**, on the run itself,
with the boundary, which attempt it was, the interval, and the failure it waited
out. Two things follow. A process that dies mid-wait comes back to the window it had
already spent rather than to a fresh one. And a run that waited a network out and
finished says so on the work item — `Waited out a recoverable failure while
merging the pull request: 3 retr(ies) over 4s, waiting 1s, 1s, 2s` — which is the
only sign that anything happened at all, and the thing to read when a machine's
network is degrading before it starts costing runs.

**A window that runs out escalates rather than going quiet.** What the boundary
would have produced is produced — an outstanding publication, a blocker on the
item — with the attempts and the time in front of it, so a run handed to a person
says the network was retried and for how long instead of reporting the last reset
as though it were the first.

**The gate-boundary dependency read is the one that parks instead.** What the
other boundaries would have produced is a blocker, because a push that never
landed or a merge the forge never made is a step somebody has to decide about; a
store that was busy for two hours is not, and the run has nothing wrong with it.
So a read that spends its whole window leaves the run in flight, parked, keeping
its claim, its branch, its worktree, and its developer session — exactly as a
run waiting on an unresolved directive or on work its item depends on does. It
says so on the item, naming the read, the attempts, the time, and the last thing
the store said; `yoyo status` reads it as a parked run, `yoyo reconcile` leaves
it resumable rather than settling it, the claim audit leaves its claim alone, and
the channel says it as a `warning`, since nobody chose it. The store answering is
what lifts it: `yoyo run <beads-id>` continues the same run from the boundary it
stopped at, and the window goes with the park, so the re-entered gate asks again
rather than finding its window already spent. Before yoyodyne-ifd.428.6 this
boundary ran under a flat deadline and ended the run on the first timeout, which
in two days killed three runs — two of them holding an approved change and a
lifted worktree.

### The read a dispatch makes before there is a run

Both of the reads above are a run's own, and everything this section says about
recording and parking follows from there being a run to record on. **The read a
dispatch makes before either — `yoyo run` and `yoyo triage`'s continuation
loading the work item, before anything is claimed and before a run in flight is
adopted — has neither.** It is waited out on the same series and the same
two-hour window, because it is the same store contended by the same processes
and a `bd` killed there turned away a dispatch that had nothing wrong with it.
What it does not do is the other two halves:

- **Nothing is recorded on a run**, because there is no run to record it on. No
  run has been reserved, and the run a resume is about belongs to whichever
  process holds its lease rather than to the one asking. So a dispatch that dies
  mid-window comes back to a whole window rather than to the one it had spent —
  the opposite of the rule a run's own boundaries follow, and it costs nothing,
  because a dispatch that died claimed nothing and left nothing behind.
- **The wait is recorded on the watch log instead**, where a watch session
  started the dispatch — an item it pulled, or a triage decision it carried out.
  Each wait is written as it is taken, naming the item, the boundary (`reading
  what this item waits on`), which retry it is, when it asks again, and the
  failure it is waiting out. Such a dispatch holds a developer slot with no run
  record for up to the whole window, and until yoyodyne-ifd.428.14 every surface
  read that as a hung process. Now `yoyo status`'s running line says it —
  `Running: no run yet, and 1 dispatch waiting out a tracker failure before
  claiming anything:`, with the wait under it — and so does the brief form the
  channel carries. The line the channel repeats while nothing is chosen names the
  wait rather than an idle session, as nobody's move, and the stall alarm reads it
  as an account of the quiet rather than paging. A wait accounts for the quiet
  until a minute past its own end, time enough for the retry it precedes, so a
  dispatch that died mid-wait stops accounting for it within minutes and the
  alarm is free to fire. These entries are notes about a dispatch rather than
  changes of the session's state, so `yoyo status`'s session line still names
  what the session itself last did. A dispatch started by `yoyo run` or `yoyo
  triage` at a terminal records nothing; whoever typed the command is watching it.
- **Nothing is parked**, for the same reason. A window that runs out there
  refuses the dispatch with `load work item: the tracker kept failing on
  something a later attempt could have survived`, naming the attempts and the
  time, and writes nothing on the work item. Where the dispatch was a resume, the
  run in flight is left exactly as it was — still claimed, still preserved, still
  resumable — rather than given a park of its own.

The practical difference is what you go and look at. A run parked on this says so
on its item and on every surface; a dispatch refused by it says so to whoever
typed the command, and to the
[docket entry](#recovering-interrupted-runs) for an attempt that never became a
run where the scheduler made it.

**A conversation's tracker calls are under the same rule.** Every decision a
role makes in conversation — a triage decision, an admission, a note, a closure
— is a write to the same store, and the reads that gate those writes go to it
too; until yoyodyne-ifd.366 not one of them was retried, which is how a triage
re-run of yoyodyne-ifd.142 came to be reported as unrecorded on a single `bd
update` that timed out. Now a call that fails the way a killed or contended
`bd` does is waited out on the same series and asked again, and only a call
that has spent the window is reported the way it always was, with the attempts
and the time in front of it and then the account of what a timed-out write left
behind. The window is per operator message rather than per boundary, and every
call in the message shares it — forty calls each waiting a whole window is a
message nobody gets an answer from. Each wait is recorded on the conversation
before it is taken, as a `tracker.retried` event, and an interactive turn shows
it on screen — `waiting out a tracker failure a later attempt may survive; asking
again at 3:04PM (attempt 3, after 2s)` — so a turn waiting out a contended store
is distinguishable from one that has hung. Stopping the turn ends the waiting,
all of it: the call is left as it failed, the window is closed for every later
call in the message — including the read that settles what a timed-out write
left behind, which runs under a context nothing can cancel — and what is
reported says the turn was stopped rather than that the window ran out. An
action that landed after waiting says so on its own line. One guard changed
with it: the duplicate check an admission makes used to let the admission
through when its listing failed, on the argument that the tracker was briefly
unavailable. Now that the listing is retried, a listing that still fails refuses
the creation with the reason, and puts a proposal the goals would have admitted
unasked to the operator instead, rather than admitting work on the strength of a
guard that never ran.

**One consequence is worth knowing before you raise
`execution.max_concurrent_developers`, and it is not free.** Five of these
boundaries run under the target branch's promotion lease, which is what keeps
promotions serial: re-reading the remote target, the merge, confirming it,
deleting the merged remote branch, and catching the local branch up. A run
waiting a forge out holds that lease while it waits, and each of those boundaries
has a two-hour window of its own — so the worst case is not two hours but the sum
of them, and a forge that is down for a day holds the lease for as long as the
windows last rather than for an hour.

**Other runs promoting into that branch do not wait it out.** The promotion queue
is bounded at fifteen minutes, so a run that reaches integration while the lease
is held waits that long and then stops, saying that another promotion held the
lease for the whole wait. Before these waits existed the holder failed fast and
the queue drained behind it; now a forge outage longer than fifteen minutes can
stop the runs queued behind the one that is waiting. The trade is deliberate at
one developer, where there is no queue at all — waiting is what stops reviewed
work being recorded as failed — but above one it converts a long outage into
stopped runs on the branch rather than one slow one, and the fix while it lasts
is `yoyo pause` rather than waiting for the windows to run out.

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
Short Git commands keep a deadline of their own rather than a liveness signal,
which is the right bound for a command whose duration is known — known on an
idle machine, that is. A local Git command is given thirty seconds, scaled by
how far the machine's one-minute load average exceeds its cores, read for each
command and capped at ten times: at a load average near forty, a flat thirty
seconds was killing `git worktree list` under two race suites and the provider
processes beside them, and the run that asked failed on the machine rather than
on its work.

One local Git command is budgeted to the work rather than to the command, and it
is the one that does work: `git worktree add` writes the whole tree out, so its
budget is the figure above plus fifty milliseconds for every file in the commit
being checked out, load-scaled like the rest. A checkout bounded by the
command's own figure is what killed three runs of yoyodyne-ifd.441 in three
hours on 2026-09-22, each of them with no Git error at all — the runner's exit
code, and a stderr holding nothing but the checkout's progress meter, one
stopped at 87% of 1099 files — each leaving a half-written registration, holding
a developer slot until the claim audit gave the item back half an hour later,
and one of them spending a recorded re-run on a run no developer ever saw. A
creation that is ended anyway is [an environmental refusal](work.md): the run's
record says so in one sentence naming the tree and the budget rather than
reprinting the progress, the item is charged neither a round nor the re-run that
started it, and the stoppage is docketed like any other.

Sizing that budget costs one `git ls-tree` the creation did not used to run, and
it cannot become a new way for a creation to die. A count that fails for its own
reasons never stops the creation: the add is budgeted for a stand-in tree of two
thousand files instead, and the run says on standard error that its checkout was
bounded as an uncounted tree — so an operator reading a creation that dies later
knows the bound was a stand-in rather than the tree's own size. A count the
harness itself ended is the one that does stop it, and it stops it in exactly the
class above: a count killed by the load is the same machine-too-busy death as an
add killed by it, refused environmentally and charged nothing.

**The run is left in flight for half an hour, and then it is settled.** Nothing
in the harness continues a stopped run on its own — the scheduler chooses from
what the tracker calls ready, and a claimed item is not — so a stop nobody typed
`yoyo run` for used to leave a run that read as running for good, with no live
process behind it and no ending ever recorded. Each one held a developer slot,
the in-flight guard refused every item beside it as a race, the claim audit left
it alone as a wait, `yoyo reconcile` reported it resumable on every pass, and the
development manager's repair-continue about it was refused for want of a
docketed stoppage, because the run never recorded one. Two runs stood that way
from 2026-09-20 07:20 until somebody asked, a day and a half later. So a sweep
that finds a stopped run nothing has continued for thirty minutes — measured
from the stop, which is the last thing the record wrote — settles it as
[a run whose process vanished](#recovering-interrupted-runs): the item is
blocked with what the sweep observed, the stoppage is docketed, and the slot is
free. Inside the half hour the sweep still reports the run resumable and says
when the grace ends.

**What the settled stoppage is owed is the attempt it was stopped in, and the
docket entry says so.** A stall judges nothing: no reviewer saw the change, no
check ran, and nothing was returned to the developer — so the entry the
development manager reads carries the fact that decides between her two verbs.
It names the developer session the run stalled in, says the session is preserved
with whatever that attempt had written still in the worktree, and prints
`yoyo triage repair <run-id>` as what continues it. `yoyo triage repair` then
does exactly that: the same run goes on, in the same worktree, branch, and
session, resuming at the point the provider was stopped. It counts no review
round and no repair attempt, because a stall returned no failure for an attempt
to answer — what the item's budget records is the grant the decision spent, and
what the run records is a continuation with no attempt against it, marked as the
stall it carried on. The one condition a repair of a change is held to and this
is not is that the worktree hold a change already: a first attempt stopped early
may never have written anything, and an empty worktree is exactly what the
attempt it is owed starts from.

Until 2026-09-23 the repair was refused for a stoppage like that, for want of a
repair input, which left a re-run as the only decision that could be carried
out — and a re-run starts the item over from the target branch, discarding the
session and whatever the stalled attempt had left uncommitted in the preserved
worktree. `yoyo triage rerun` is still the right verb where the ground has moved
and the work is to be done again; it is no longer the only one available.

## When a run says more than the harness keeps

There is a third bound beside those two, and it is not a deadline: how much of a
process's output the harness holds in memory. It is 8 MiB, and what it bounds is
the copy — never the process, and never the run. A run that bursts its output
diagnosing something is a run doing its work, and a parity harness that diffs
execution traces is exactly that workload.

Output past the bound is truncated with a marker, and the process carries on to
its own end:

```text
[output truncated at 8388608 bytes; the whole of it is in the event log of run-32e3f059…]
```

The marker follows the rule a cut Slack message already follows: it names the
durable record holding the whole, so nobody reads a cut copy as everything the
process said. For a check and for a provider invocation that record is the run's
own event log, because every line goes into it on the way past — the retained
copy is the diagnostic beside it rather than the original. A command that keeps
no such record says the rest was not retained instead, in those words, rather
than sending you after a file nobody wrote.

The truncation is said out loud in the same event stream `yoyo status --follow`
follows, and it is on the result the record keeps, so it is visible both to
somebody watching and to somebody reading back months later.

This used to end the run. The output bound was a read error, so a verbose run
failed with its provider's own result event never parsed — no cost recorded, no
verdict, and a claimed work item left sitting behind a process that was not
coming back. `run-32e3f059` died that way on 2026-09-03, with zero dollars
recorded and six hours of silence after it. Keeping traces in files was the
workaround; nothing needs it now.

### One line that is too long

There is a second bound underneath that one: how much of a single line the
harness holds, which is 1 MiB. It exists because output is read a line at a time
and a line has to be complete before anything can be done with it, and it is hit
by different output than the 8 MiB total — a provider stream puts one tool result
on one line, so a single large file read can reach it while the invocation as a
whole is nowhere near verbose.

It follows the same rule. The line is cut, the rest of it is read and thrown away
so the process is never blocked writing it, and the cut line ends with a marker:

```text
…[line truncated at 1048576 bytes; 3407872 further bytes were not retained]
```

The marker names no record holding the rest, because there is none — unlike the
8 MiB bound, which cuts a copy while every line still reaches the event log, this
one drops what it cuts. What follows the long line is read normally.

For a provider stream a cut line is no longer an envelope, so nothing is read off
it: it is recorded as a `truncated_stream_line` anomaly in the run's event log
and the stream carries on to its own result. A stream the harness genuinely
cannot read still fails the run with a decode error, which is why the runner
marks the line rather than leaving that to be guessed from the failure. If the
line that was cut happened to be the invocation's terminal, the invocation ends
without one and is answered the way any other lost terminal is — the anomaly in
the log is what says why it is missing.

This used to end the run too, in the harder way: the process was killed on the
spot with `token too long`, so the work in the worktree and the invocation's cost
went with it.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got
to. `yoyo reconcile` settles what it left behind, and then converges your local
state onto what the forge has:

```sh
./bin/yoyo reconcile --json
```

**Run it from wherever you are standing, the preserved worktree included.**
Recovery and inspection happen inside the worktree a failed run left behind,
and a project whose `.yoyodyne` is checked in gives that worktree a copy of the
configuration — so every verb here, run from inside it, used to resolve the
repository to the worktree itself, a directory under `execution.worktree_root`,
and refuse with `repository and worktree roots must not contain one another`
(reported twice on 2026-08-19 and fixed in `yoyodyne-ifd.335`). Now `yoyo
reconcile`, `yoyo cost`, `yoyo directive`, `yoyo pause`, `yoyo resume`, `yoyo
run`, `yoyo review`, and `yoyo chat` run from inside a worktree the harness
manages address the checkout that worktree was added from — the primary
checkout, which is what every one of them means by "the repository" — exactly
as they do run from that checkout; `yoyo status` and `yoyo reports` never
built the manager that refused and were never caught by it. The one thing
still refused is a repository that sits under the worktree root and is not a
worktree of a checkout outside it, and that refusal says what to do: run
`yoyo` from the checkout the worktrees were added from, or point
`execution.worktree_root` outside the repository.
`TestVerbsRunFromInsideAManagedWorktree` in `internal/cli` drives the verbs
from a worktree it creates, and [the configuration
guide](configuration.md#discovery) says how the resolution is made.

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
go looking. A run that died holding its change — a push the remote refused, a
backend that broke mid-attempt — is docketed too, but by the run itself as it
ends rather than by this sweep: the sweep re-derives blockers from the whole
recorded history, and every terminal failed run with a surviving branch has the
shape of a death, so re-deriving those would put months of settled work on the
docket at once. A death from before this existed is therefore not on the docket
and will not appear on one. **A run that died before it claimed its item** — a
dispatch the tracker refused, anything that failed before the first thing a run
changes outside itself — is docketed the same way and for the same reason, as a
*run that died before it started*. It is the one failure that leaves nothing at
all: no blocker on the item, because the item was never taken, and no branch,
because no worktree was cut. Every other rule here reads that as nothing having
happened, which is exactly how one item was dispatched twenty-nine times in
twenty hours with no surface saying a word. Its entry names the run and the item
it tried to claim, says the item is untouched, and carries the failure. Like the
death above it is recorded where it happens and never re-derived by the sweep,
so a pre-claim death from before this existed is not on the docket either.
**A dispatch that failed before any run was reserved** is one layer earlier
still, and is docketed as an *attempt that never became a run* by the
[watching session](work.md#letting-the-harness-choose-the-work) that made it. There is no run
to name — nothing wrote a record, which is why nothing else could ever find it —
so the entry carries what the record would have: the item, why the scheduler
selected it, what stopped the dispatch, and that the session will not try it
again until the item changes. It is keyed to the item and the failure rather
than to a run, so the same dead dispatch is one entry however many sessions meet
it. A run
whose work reached
the target branch is completed — its item closed where the run's landing
discharged it, put back in the backlog parked or waiting where it did not — and
its worktree and branch removed, including when
the run died before it could record the promotion. A run stopped anywhere
earlier becomes a durable blocker naming the branch and worktree that were
preserved. A run that finished with its merge queued at the forge is settled
here too: reconcile asks the forge and, once the merge has landed, finishes the
publication — merge commit recorded and your local target branch caught up onto
the remote target, which carries the forge's merge and whatever landed after it
— and settles the work item, which the run
deliberately left open because a queued merge is a
publication nothing has confirmed. Where it goes is what the run's own landing
says: closed where the landing discharged the item, back in the backlog parked
or waiting where it did not. Settling a merge
is complete on its own that way rather than leaning on the sweep below, so a
checkout is never left behind by which command somebody happened to run.
The branch the merge consumed is deleted **after** the item is settled, and its
removal cannot hold the settlement up: it is hygiene on the forge rather than
part of the publication, so a connection that drops at that last step leaves a
dead branch and a settled item rather than an item that reads as unfinished
work. It
is asked again on the recoverable-failure backoff before it gives up, and what
it leaves if it does is recorded below.

Three settle-path outcomes leave a publication outstanding for a person, each
with its own line on the work item. A merge the forge **dropped** is the
first: something the base branch required went unmet, the harness does not
merge past a requirement, and nothing about that publication is confirmed — so
the item is handed back to you with a blocker rather than closed as integrated,
which is also what puts it where a bounded re-arm of the dropped merge can be
decided — once per publication, carried out by `yoyo triage rearm`, after which a
further drop of the same publication is recorded as an escalation rather than
re-armed again. The moment the drop is found out is recorded on the run as well,
so it is announced in the item's thread as a `warning` rather than waiting for
the next person who runs a status command — and until the publication is
settled, it is counted in the [heartbeat](reporting.md) as a promotion awaiting
the forge. A
merge that **landed but could not be confirmed** is the second: the forge
performed it, and the steps that confirm it — verifying the remote carries the
promotion and recording the merge commit — failed,
so the record honestly says the publication is not settled even though the
merge is real. In those two, your local branch is deliberately left where it is
rather than moved on a publication nothing verified. A **merged branch that
could not be deleted** is the third, and is the mildest: the item is already
settled — closed, or back in the backlog, as its landing said — and your local
branch already caught up, and what is left is a branch on the forge. It says so
in a second line on the item naming the branch.

All three are on that docket, and all three hold their item out of the pull for
as long as they stand — which is the point: the promotion has already put the
change on your local target branch, which is the authoritative one, so an item
whose only outstanding state is a publication is not implementable work and a
run started against one can only rediscover that. What holds it says which of
the three it is, because they are not the same thing to act on: a confirmed
merge means nothing at all is left to do about the work, and a merge the forge
never made means the merge itself is still to be decided. Neither ever claims
the other, and where a run also left a branch behind, an unconfirmed merge reads
as that branch — the thing somebody can still act on — rather than as its
publication. A catch-up the
settle could not make is none of these: it is ordinary, the run settles, and the
convergence sweep below finishes it on the next pass. Other reports on this page
still reach you when the evidence demands it — a preserved blocker, a diverged
remote, a catch-up that could not finish — but none of them asks reconcile to
exercise judgement: it reports and leaves the decision where it belongs.
Settling never invokes a provider either: a lost process handle is not a
reason to start a second developer for an item. The one provider invocation the
sweep makes is the last thing it does, and it starts no second developer: a run
[paused on a usage limit](#waiting-out-a-provider-usage-limit) whose process
exited on the in-process bound is continued by the sweep itself once its
deadline has passed, in the run's own worktree and developer session, which is
the same run's own attempt reissued rather than a new one.

**None of the three stands forever.** Every sweep asks the remote again about
each publication the record says is merged and unfinished, and finishes the ones
the remote now confirms — the promoted commit on the remote target, unrewritten.
The merge commit recorded is the one the forge names for the request where it is
the merge of that promotion, or otherwise the one the sweep finds in the remote
history with the promoted commit as a parent; the forge's record never decides
the confirmation. Finishing is
the settle path's own work in the settle path's order: the merge commit recorded,
your local branch caught up, the item settled by its own landing where the drop
had handed it back, the consumed branch deleted, and the docket entry closed as
`settled` by the harness — so the hold, the heartbeat's count, and the
`Publication outstanding` line on the item all clear together, and the item gets
one note saying what was settled and which line it replaces. That is the lever
behind the sentence in [how work flows](work.md#letting-the-harness-choose-the-work)
that a hold lifts by the publication being settled, which until yoyodyne-ifd.357
had nothing behind it. A publication the remote still refuses stays exactly
where it was — the record keeps the account the run wrote, which is the line on
the item, and nothing is written on either — and the sweep says what the remote
answers now on every pass it stands. The eight held requests PR 497 merged on 2026-09-13 are
the case this was built on: confirmation then required the remote tip to carry
exactly the promotion's content, which only the last merge of a batch does, so
all eight settled as unconfirmed and stayed that way until this could re-ask.

Every other publication is re-asked about on the same sweep, before that. A run
that ended without its publication settled — one that failed before it
integrated anything, or one whose request the forge merged after the harness had
stopped watching — used to keep whatever the forge last said at the moment the
run ended, for good: a pull request somebody merged days later stayed recorded
open and unmerged, and the triage docket and the status surfaces read that rather
than the truth. Reconcile asks the forge about each of those and records the
answer — merged, closed, or still open. It only writes the record: nothing is
merged, nothing is closed, no branch moves, and the work item is not touched by
the asking. A request that turns out to have merged outside the harness — a
dropped merge you made by hand on the forge — is recorded as merged here, and the
finishing above then confirms it on the remote and settles the item, so a hand
merge is settled by the sweep that finds it rather than staying handed back for
good. A record the forge agrees with is left exactly as it is, and a merged one
is never asked about again by this half — merged is the one answer a forge does
not take back. A record left alone for a reason, such as a branch the forge
answers about with some other request, is reported and is not a failure; a forge
that could not be reached is, and the next sweep asks the same question again.

Before either of those, the sweep looks for the one publication neither can see:
**a promoted run whose record names no pull request at all.** Everything above
starts from the request on the record — the docket keys a publication entry to
it, the heartbeat counts what awaits the forge from it, the re-arm repeats it —
so a publishing run that promoted a change and recorded no request would be a
change the forge holds that no surface reports. Three things close that. The run
itself refuses to be that record: a publishing run that reaches its promotion
with no request on its record writes a `Publication outstanding` line naming the
branch and saying nothing was asked of the forge, rather than finishing quietly,
and a run whose summary names a request its durable record does not hold — or
holds in a different arming state — is refused completion outright and recorded
as failed. That line is what the docket and the status line then read, before
any sweep has asked the forge: the promotion is docketed as a publication keyed
to the run alone, since there is no number, naming the branch and carrying the
account, and it is counted as awaiting the forge with the harness named as the
mover — so a forge that turns out to hold no request for the branch leaves a
promotion every surface still names, not one only a sweep's stderr does. And the
sweep asks the forge by the run's branch, which is the one durable handle it has
left, writes the request the forge holds onto the record — number, state, and
whether a merge is queued for it — and then makes the merge request the run
itself never made. That is the run's own merge made late, on the run's own
evidence and through the run's own gate: the record has to carry the promotion
and the approving verdict, and the verdict is read off the record by the sweep
before it asks rather than inferred from the promotion beside it; the request's
head has to be the promoted commit; the remote target has to pass the same
pre-merge check the run's merge makes; and the request is pinned to that commit,
made by the same method, under the target branch's promotion lease.
The forge's answer is recorded as a queued merge on either answer, exactly as a
re-arm records one — and the record is written only with that answer, so a
sweep interrupted between finding the request and arming it leaves the record
as the run wrote it for the next sweep to ask again, rather than a request
beside a line saying none is held — and the next sweep settles the run on what
the forge does with it — confirms the merge, records the merge commit, catches your local
branch up, deletes the consumed branch, and closes the docket entry the
promotion had open. A request the forge has already merged, or already holds a
merge for, needs no arming: something has asked the forge, so the account of
the loss is replaced in the same write that records the request — with nothing,
for a merge the forge holds, since settling it writes what became of it; and for
a merge the forge has performed, with the line every unconfirmed merge carries,
so the finishing above confirms it on the remote and records the merge commit
exactly as it finishes a merge the run itself could not confirm; a request
whose head has moved, a remote target that no longer passes the check, or a
merge the forge refuses is recorded as the dropped merge it is, which puts it on
the docket for triage and holds the item exactly as a drop the run itself met.
The sweep never repeats a merge the forge dropped: that is still `yoyo triage
rearm`, a decision, once. A forge that holds no request for the branch leaves the
record as the run wrote it, and the sweep says so on every pass it stands. What
the sweep, the docket, and the status line select on is that account and only
that account — a promoted run that recorded it, with the approving verdict
beside it — and not the bare shape of a promotion with no request on its
record: the record carries nothing else that tells a local promotion from a
publishing one, and every promotion of that bare shape the store held when this
was built was a local one from before publishing existed. A record that lost its
request by some path that wrote neither the account nor went through the run's
own completion is therefore not recovered by this, and is not claimed to be.
docs/diagnoses/yoyodyne-ifd-402-publication-record-not-lost.md is the account
of the two runs this was built on, neither of which turned out to have lost
anything.

The same sweep recovers the [exchanges the roles have put to each
other](conversation.md#roles-asking-each-other-things), for the reason it settles
the runs: a process died holding something, and this is what finds out. Each
exchange is taken under its own lease, so one a live process is carrying is left
to that process — the lease is also what says the carrier is gone, since the
operating system drops it when a process exits. A round a dead process asked and
never got an answer to is closed saying which process was carrying it, with the
round still spent and the thread still open; a thread that spent every round it
was given and was never asked again is closed as unresolved and reported to you
at warning severity, which is the ending the round cap would otherwise never
reach on a thread nobody came back to. Nothing is put in front of a role by this:
recovering from a lost process is never a reason to start a round nobody asked
for, so a sweep that finds a thread simply waiting its turn leaves it waiting.

Those two endings are the whole of what it prints, because they are the whole of
what it changed. Everything else it comes back with is a description of what it
found rather than something it did — a thread another process is carrying, one
waiting its turn, one the [in-flight bound](conversation.md#roles-asking-each-other-things)
would hold back, one whose references have moved — and those are in `--json`
under `supervision`, as the branches and publications it leaves unprinted are.
That division is worth knowing before you go looking: a product with several open
threads has a line about each of them on every sweep, and printing those would
bury the one or two that say a record was changed.

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
A deletion is written onto the run it belonged to, under that run's own lease, as
a retired checkout is: `yoyo status` and the triage docket read the run's record
for whether its change survived, so a branch deleted with nothing written down
leaves the run advertising one that is not there.

The same sweep retires the leftover checkouts, which is what makes the worktree
registrations a machine carries live runs plus a bounded tail rather than
something that grows with the harness's history. That growth is not cosmetic: an
agent's sandbox profile denies every registered worktree path on every command it
spawns, so a machine that keeps them all eventually cannot spawn a command in its
next worktree at all — no `make check`, no `go test`, nothing. Settled runs past
the most recent few have their checkout unregistered, and registrations whose
checkout is no longer on disk are pruned, whichever run or person left them
behind. A registration a killed `git worktree add` never finished filling in is
cleared on the same pass, and named — see
[a registration a run never finished writing](#a-registration-a-run-never-finished-writing)
for what that shape is and why nothing else clears it. A run still in flight is
never a candidate — that is a live developer's checkout. Each retirement is taken under the run's own lease and written onto its
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

That ref is on the run's own record too, and the sweep writes it onto the work
item as well, naming the checkout it retired and the command that opens the ref.
The item is where somebody picking the work up actually reads, and what it
already carries is that run's own failure note — written while the checkout was
still there and naming it. Without the correction, that note goes on pointing at
a directory that is gone: run-48216ea9's 23 files were reported destroyed on
exactly that gap, while the ref holding them was two commands away. A note the
tracker refuses is reported on the sweep rather than failing it, because the
work is on the ref either way.

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

The last reading the sweep takes — after every settlement above and before the
one thing it continues, below — is whether anything is happening at all. When
no developer run has started or ended for `--stall-after` — ten minutes by
default — the tracker reports work ready, and no hold, no still-moving run and no provider usage window
accounts for it, that is recorded against the product as a stall and said here:

```
nothing has started on this product for 2h14m0s, with 3 item(s) ready
  the session choosing work last recorded watching at 2026-09-01T06:05:00Z, and has said nothing since
```

The second line is the one to act on: a session whose last word was `stopped`
wants starting, and one still claiming to be `watching` wants killing first. A
machine that is behaving says nothing here at all. Where the record goes and why
this is the sweep that writes it is
[when nothing happened at all](#when-nothing-happened-at-all); what it costs is
one tracker read per sweep, and only on a sweep where nothing else already
accounts for the quiet.

Repeating the whole thing is safe — a settled run is no longer outstanding, a
branch already level with the remote has nothing to catch up to, cleanup over
artifacts that are already gone does nothing, and a stall already standing is not
recorded twice. A run another process still holds
is left to that process, and a run `yoyo run` can continue on its own — one
inside its repair loop, one paused for a provider usage limit whose deadline
has not passed, one whose
provider the harness stopped on time and the half hour below has not passed
for, one paused for an [unresolved
directive](conversation.md#directives-and-the-work-they-pause), or one parked on an
[operator pause](#pausing-everything-and-resuming-it) — is left exactly as it is
for that command to pick up. A run paused for a usage limit whose deadline has
passed with no process serving the wait is the one the sweep
[continues itself](#waiting-out-a-provider-usage-limit), as the last thing it
does.

**A run whose process vanished is settled here, and nobody edits its record by
hand.** A run whose provider [the harness stopped on time](#when-a-provider-stalls-or-runs-out-of-budget)
is left in flight to be continued, and nothing continues one on its own; a run
nobody typed `yoyo run` for therefore goes on reading as running with no live
process behind it and no ending ever recorded. The sweep settles one of those
once thirty minutes have passed since the stop with nothing continuing it —
the lease it takes to settle a run is what says no process holds it, since a
continuation somebody did start would be holding that lease — and settles it as
an environmental stop rather than as a verdict on anything. The run's record
and the work item both carry what the sweep observed and nothing more: that no
live process held the run, that no ending was recorded, when the record last
moved, and why the provider was stopped. The branch and worktree are left
exactly as a stopped run's are, the item is blocked with that account, and the
stoppage goes on the triage docket, so a repair-continue the development
manager decides about it carries out as it does for any stopped run — on the
change the run already has, in its own worktree and developer session. Both
shapes of stoppage are carried out: a run stopped inside its repair loop, with a
failing check or the reviewer's findings handed back to it, is continued on that
failure; one stopped in its first attempt, with nothing handed back, is
continued at the attempt it was stopped in, and counts no review round and no
repair attempt because a stall judges nothing — the entry says so, and
[what a stall is owed](#when-a-provider-stalls-or-runs-out-of-budget) is the
whole of it. The
slot the run was holding and the in-flight guard's hold over the items beside it
release with the record going terminal. `yoyo status <item>` reads the run as
`stopped` with that reason under it. Whether the round it ends spent anything
is decided as every environmental round is: one that left a change behind spent
what it spent, and one that left nothing gives back the repair grant that bought
it. What you never do is open a run's JSON and change `status` yourself: a
record edited by hand carries no account of who ended the run or why, the
docket and every status surface are derived from the record rather than from
the edit, and the sweep already writes the whole of it on the next
`yoyo reconcile`.

## Git maintenance, and the one prune that is still yours

Git prunes worktree registrations as part of its automatic maintenance, and it
judges one stale by whether its administrative files are there — which is
exactly what a `git worktree add` has not written yet while it is still filling
the entry in. A prune reaching that window deletes the registration out from
under the add, the add fails with

```text
fatal: could not open '.git/worktrees/yoyodyne-ifd-334-0db8dc56/locked' for writing
```

and the run is lost to nothing but timing. Every worktree the harness cuts
shares the repository's common Git directory, so the prune does not have to
start anywhere near the run it takes down.

The harness holds this off in the two places it can. It never asks for
maintenance in the Git commands it composes itself, and every process it
launches — the agent, each configured check, and anything those go on to
start — carries `gc.auto=0` and `maintenance.auto=false` in its environment. So
a Git command an agent runs, or one a project's own build tooling runs inside a
worktree, cannot start a maintenance run either, without either of them having
to know that.

Nothing is written into your repository's config for this. The repository is
yours, its maintenance is yours to configure, and object GC turned off for good
in a repository that keeps growing is a cost the harness would be imposing on
your machine rather than on a run. The fence lasts exactly as long as the
process it was given to.

What that leaves is a Git command nobody here launched: `git gc` or
`git maintenance run` typed in the checkout, or a tool you started yourself.
That one is yours. Run it when nothing is in flight — `yoyo status` says what is
running — and a run cannot be caught mid-creation by it.

If runs are still being lost this way, the full fence is available and is one
command in the managed repository:

```sh
git config maintenance.auto false
git config gc.auto 0
```

That closes the residual for every command in the repository, at the price of
packing and pruning objects becoming something you run by hand.

## A registration a run never finished writing

`git worktree add` registers an entry under the common Git directory's
`worktrees/` and then fills it in, one file at a time: a `locked` file saying
`initializing` first, then `gitdir`, `commondir` and `HEAD`, then the checkout
that writes the `index`, and last of all it removes the lock. Anything that
walks the bookkeeping in between reads a file that has been created and not yet
written, and Git refuses the whole command rather than skipping the one entry:

```text
fatal: failed to read .git/worktrees/yoyodyne-ifd-334-0db8dc56/commondir: Result too large
```

The listing is the obvious walker — every inspection, cleanup and sweep the
harness makes is one — but it is not the only one. A rebase, a checkout, a
branch deletion and `git worktree add` itself each walk the registrations to
check that a branch is not checked out somewhere else, and each dies over the
same entry with the same words.

**Within one harness, such a command does not cross that instant at all.** Every
Git command the harness runs that walks the registrations takes the same lease
its own creations take, in a shared mode every other reader may hold at once and
no creation may hold beside — so a rebase, a checkout or a listing waits for a
creation in flight instead of meeting the entry it has not filled in yet, and
readers never queue behind each other. A creation reads what it is writing under
the lease it already holds, so it never waits for itself. Commands that open no
registrations — the great majority, every `diff`, `status`, `log` and `rev-parse`
— take nothing and are not held back by a creation.

What the lease cannot cover, the harness runs *any* Git command again over: a
second harness on an older binary, a Git command somebody ran by hand, and a
platform with no advisory lock to take at all. The instant passes in the time
Git takes to write a handful of small files, so running the command again is
usually enough. Only that one refusal is run again: every other answer Git gives
is believed the first time.

What neither can cover is the entry that stays that way. An add whose
process was killed leaves the entry exactly as it stood, and nothing Git has
clears it: `git worktree prune` skips an entry that is locked and judges an
unlocked one by its `gitdir` file, which such an entry usually has, and
`git worktree remove` finds its entry through the listing the entry breaks.
Where the file it was killed writing was `commondir`, every walk on the
repository then fails, creation included — which is how one killed add used to
stop every later run on the repository until a person removed a directory by
hand.

**The harness clears such an entry itself now, and says so.** Two things do it:
every worktree creation, just before its own `git worktree add`, and the
convergence sweep [`yoyo reconcile`](#recovering-interrupted-runs) runs. Both
hold the registry lease the harness's own creations take, so under it an
unfinished entry cannot be a creation of the harness's own still writing; and
both leave alone an entry younger than a minute, because an add somebody else
started — a person's, or one an agent ran inside a checkout — takes no lease and
is told from a dead one only by having stopped writing. A creation waits such an
entry out rather than failing over it, and the minute is the bound on that wait
rather than its length: what it is really waiting for is the other add getting
past the one file a walk dies on, which takes milliseconds, so a creation that
meets a live neighbour is held up for about as long as that neighbour takes to
register. Only an add that has genuinely stopped costs the whole
minute, once, and is then cleared. What is cleared is the
registration alone: the branch the add made first is a branch like any other,
and a directory it left on disk is left where it is, named in the line so you
know it is not a worktree any more.

```text
cleared the worktree registration yoyodyne-ifd-334-0db8dc56, left by a git worktree add that never finished: its lock still says initializing, which the add removes only once it has checked the worktree out; the directory it named at /…/worktrees/yoyodyne-ifd-334-0db8dc56 is not a worktree any more, and is left where it is
```

A creation says it on standard error; the sweep prints it and carries it in
`--json` under `convergence.registrations.unfinished`, with an entry it kept and
why beside any it cleared. An entry is judged a killed add by two things
together: it has no `index`, because the checkout is what writes one, and it
either still carries Git's own `initializing` lock or is missing one of the
files the add writes before the checkout. A worktree added with `--no-checkout`
has no index either and is neither of those, so it is never touched.

A listing that meets an unfinished entry before it has been cleared — inside
the minute, or one Git refused over that was never a killed add — still
describes the repository without it rather than failing, saying so on standard
error:

```text
the worktree listing left out yoyodyne-ifd-334-0db8dc56, registered and not yet filled in, which Git refused the whole listing over: list worktrees failed with exit code 128: fatal: failed to read .git/worktrees/yoyodyne-ifd-334-0db8dc56/commondir: Result too large
```

That line means runs are not being lost to the entry, not that it has gone. If
it keeps appearing across sweeps, the entry is the one shape nothing clears: a
registration that *does* have an index — a finished worktree whose `commondir`
was emptied by something other than a killed add, a crash that zeroed the file,
say — and that one is yours to look at, because the checkout it names may hold
somebody's work. `yoyo status` says what is running; when nothing is:

```sh
rm -r .git/worktrees/yoyodyne-ifd-334-0db8dc56
git worktree list --porcelain   # describes the repository again
```

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
item settled as integrated against it — but it does mean the branch does no more
work until you say which history is right. Nothing sweeps it away in the
meantime, and no later `yoyo reconcile` resolves it.

Runs that predate the fix in `yoyodyne-ifd.177` could produce this by losing a
cross-machine race after promoting, and a repository still standing in that state
is what this section is for. A run today cannot produce it that way: it settles
where the remote target stands before promoting, and stops without closing
anything if the remote moves afterwards. Runs before `yoyodyne-ifd.429.5` could
also produce it on a target the forge protects, by promoting onto the local
branch and then having the forge refuse or hold the merge: the local branch was
left ahead of the remote with the run's commits, which is how the product stalled
on 2026-09-20 and again on 2026-09-24. A run today lands a protected target
through its pull request and moves the local branch only by a fast-forward onto
the remote ([configuration](configuration.md#a-protected-target-lands-through-its-pull-request)).
Reaching this state now takes somebody pushing to the target directly. The
recovery is the same either way, and it is yours to run. (A queued merge landing among others used to reach this page too — as a
publication reported unconfirmable for good rather than as a wedge — until
confirmation asked whether the remote contains the promotion rather than whether
its tip carries exactly the promotion's content.)

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
Not startable (4 of 7 admitted items; 1 awaits the development manager's decision, 1 awaits the harness carrying out a decision already recorded):
  yoyodyne-ifd.200 — waiting on yoyodyne-ifd.199
  yoyodyne-ifd.212 — parked, so no pull selects it however far the queue drains: the design is being reworked
  yoyodyne-ifd.153 — run run-5035c832 stopped on it and its change is preserved (branch and worktree checked and there), so a fresh run would start over on top of work that is still there; the development manager decides what happens to it, and nothing pulls it until she has
  yoyodyne-ifd.150 — run run-a17c9b40 stopped on it and its change is preserved (branch checked and there), so a fresh run would start over on top of work that is still there; the development manager has already decided what happens to it, so what is outstanding is the harness carrying that decision out rather than a decision
Needs a human (3):
  directive directive-4f2c… is unresolved: which branch does this land on? — the operator's — the work it affects waits until `yoyo directive resolve` settles it
  1 admitted item awaits the development manager's decision — the development manager's — nothing pulls a stopped item until she decides what happens to it
  1 admitted item awaits carry-out of a decision already recorded — the harness's — the decision is made, and what is outstanding is the harness acting on it
```

- **Running** is the developer runs in flight, each with its item, the phase it
  reached, how long it has been going, and what it has spent so far. A run whose
  evidence cannot be priced says so; it is never reported as free. Where any
  [developer slot prefers a label](configuration.md#a-developer-slot-that-prefers-a-label),
  each run also says which slot it is in and what that slot prefers — `, in
  developer slot 1 (prefers the dashboard label)` — and each free slot is named
  under the runs with its preference, `developer slot 3 is free and prefers no
  label`, so the next pull's first choice is readable before it is made. Which
  slot a run is in is read off the labels the run recorded at its claim, by the
  same derivation the scheduler fills the free slots from, so the slot this
  line calls free is the slot the scheduler will fill. Where no slot prefers a
  label the line reads exactly as above.
- **Working** is the persona conversations with a turn in flight, which nothing
  counted before this: a conversation is not a run, so a machine spending money
  on six persona turns used to report nothing running at all. The advisory hold
  is what decides, because it is the only thing that actually knows. It is
  observed rather than taken: the process holding a conversation writes down
  which process it is, and a reading checks that the process is still there. A
  status that took the hold to find out — which is how this was first built —
  would refuse a chat that asked for its own conversation in the same instant.
- **Not startable** is each admitted item nothing will pull, with the refusal
  that stops it — the queue's own account where the queue has one, the children
  where an item's unfinished children already carry its execution, the directive
  where a directive pauses the work, and otherwise what has stopped the harness
  choosing at all. The coverage is the scheduling pass's own derivation rather
  than a second reading of it, so a decomposed epic is refused here in the same
  words the pass passes it over in, with the covering children named: the
  tracker reports an epic and the child doing its work as equally pullable, and
  a status that only asked the tracker showed the epic as work about to be
  started and merely stalled, which sent whoever read it after a stall that was
  not one. That last one comes from a closed set of named reasons, each
  of which says who it is waiting on: the operator's hold, a held intake, every
  developer slot taken, a session waiting out the provider's usage window, a live
  watch session retrying a read of the harness's store that failed, a live watch
  session that has found nothing it can start, no watch session running any more,
  and a product no session has ever watched. A retried read is the harness's move
  and is never said as idle: the queue was not read, so nothing is known about
  it, and the session line above the runs says `retrying a failed read of the
  harness's store` rather than `idle` for as long as the read goes on failing.
  An idle session and no session are named apart on purpose — telling you to
  start a session you are already running sends you to the wrong place. A
  provider window is named apart from both for the same reason and says `Paused
  on the provider's usage window until 13:43Z`: nobody has a move, the window
  lifts on the provider's clock, and
  reporting it as a session finding nothing to start sends you to look at a queue
  that is fine.
  It never comes from a watch session's memory of what it has already tried,
  which is a fact about one process rather than about the product. Work that is
  admitted and would be started next is not listed here at all; the count of
  admitted items beside the heading is where it shows.

  One of the queue's own accounts is an item **held**, which is the third and
  fourth not-startable lines in the example above: a run stopped on it and its
  change is still on a branch or in a checkout, its stoppage is in front of the
  development manager and nobody has decided about it, a decision about its
  stoppage is recorded and not yet carried out, an approved change the
  environment stopped short of its promotion is waiting on `yoyo triage resume`,
  or a run promoted its change and could not finish publishing it. A run that
  ended `failed` holding its change is the first of those exactly as a run that
  ended `stopped` is: a run that fails inside its own process hands nobody a
  blocker, so its record ends `failed` while its branch sits there — which is
  what left yoyodyne-ifd.436.4 reading as an item with nothing holding it while
  its approved change waited on a branch. The first is stated from the repository rather
  than from the run's record — the parenthesis says what was found, `branch and
  worktree checked and there` or only one of them — because the record's removal
  flags are what a sweep remembered to write, and on 2026-09-19 a hold read off
  them released yoyodyne-ifd.372 as no longer preserved. A look that could not
  be made holds the item as preserved and says why. The last of those five
  accounts is not a stoppage at all and is held for the opposite reason: the
  change is on the target branch already, so there is nothing left to implement
  and a run started against it can only find that out again — which is what
  yoyodyne-ifd.295 cost, three developer runs and three reviews deep. It says
  whether the forge merged the publication or not, and never guesses: the two
  are different things to settle. Every one of the five names the run and what
  has to be decided rather than leaving the item to its `blocked` status, because
  a status says the same word about a stoppage nobody has answered and about work
  whose every blocker closed months ago — and reading that word as a refusal is
  what hid two-thirds of the backlog on 2026-09-04.

  A held item says which of two waits it is in, because they are two different
  people to go to. **Awaiting a decision** is a stoppage the development manager
  has still to settle. **Awaiting carry-out** is one she has settled into
  something the harness then has to do — a repair handed back, a re-run, a merge
  re-armed — and has not yet acted on. The other three decisions she can make
  settle the stoppage and leave the harness nothing: an item told to wait, to be
  re-scoped, or escalated is held by what she decided rather than by anything
  outstanding, so it is not in the carry-out wait and nobody is watching for a
  run that is not coming. An approved change the environment stopped is in the
  carry-out wait with her having decided nothing, because the wait is named for
  whose move it is rather than for what was decided: the reviewer decided, and
  what is outstanding is the harness resuming the promotion. One rule answers that for every surface — the status
  head, the attention line, the alarm, and the next mover on the development
  manager's own docket — so one piece of work cannot be given two next movers.
  The counts are in
  the head of the line as well as against each item, so the hourly channel
  message, which prints the heads and drops the entries, still says which of the
  two the queue is full of. Reporting both as one thing is what cost 2026-09-07:
  thirty-three items read as a decision backlog for days while the development
  manager had decided every one of them and the gap was the carry-out.
- **Needs a human** is always present, and says either `nothing` or the list with
  who each one is waiting on: the operator's two switches — a held intake with who
  it waits on, which for [a hold the brake placed](#pausing-everything-and-resuming-it)
  is the development manager's or the harness's rather than yours until she
  escalates it, and names the probe run while one is in flight — an unresolved
  directive, a
  proposed change nobody has decided, a run that ended still owing a step, a
  promotion the forge has not published, work
  marked for a conversation rather than for a run, a queue nothing is pulling
  from — a session sitting idle over it, or no session at all — while admitted
  work waits behind that, the provider holding every role at once (below), a
  part of the product [its supervisor has left down](#starting-the-product-and-stopping-it)
  as degraded, with the reason, and a
  [pile of collected reports](reporting.md#whether-the-pile-is-draining) whose
  oldest undecided entry has been waiting more than a week. A stall over an empty
  queue is not listed: it is a state of the machine rather than something waiting
  on you, and neither is a report pile that is being worked through — what is
  listed is one that is not. The unpublished promotions are the same set the
  channel's hourly line counts as awaiting the forge, read by the same
  derivation, and each says who it is waiting on: the forge's while it holds the
  merge queued, the development manager's once it has dropped one, the
  operator's for a request nothing ever asked it to merge, and the harness's for
  a promotion whose record holds no request at all — the next
  [`yoyo reconcile`](#recovering-interrupted-runs) looks the request up by the
  run's branch and arms its merge. All four leave the line the moment the forge
  records the merge and `yoyo reconcile` settles it.

A line with nothing in it says `nothing` in words, and a line whose records could
not be read says that instead — never `nothing`, which would be a confident
emptiness assembled from a file nobody could open. There is no fifth line and no
residual bucket: a state that will not render into these four is a bug in the
state.

**One thing is printed above them, and only one.** While the harness is waiting
out the provider's usage window, the reading opens with that and nothing else:

```text
Paused on the provider's usage window until 13:43Z
Running: nothing
Working: nothing
Not startable (3 of 7 admitted items):
  ...
```

It is a banner rather than a fifth line — the four are unchanged and are all
still printed — and it is there because a reading is one of the messages that
reaches you when you want to know why nothing is happening, and the reason for it
should be the first thing you read rather than the third line down beside one
item. It is the same sentence the channel says and the same one the refusals
carry, from the one derivation, and it is off the moment the window lifts.

The same place carries the other capacity state, which the session choosing
work never records because it is not the thing being refused: **the provider
holding every role at once**.

```text
Every role is paused on the provider's usage window until 2026-09-13T03:00:00Z: all 5 agents run on opus and none names an alternate, so nothing fails over; 134 turns refused since 2026-09-08T07:38:40Z
Running: nothing
...
Needs a human (1):
  every role is held by the provider's usage window, since 2026-09-08T07:38:40Z, until 2026-09-13T03:00:00Z — the operator's — the window lifts on the provider's clock, and enabling failover on the agents is what would move the work onto another model before it does
```

It is read from the [refusals the harness records outside a run](#a-provider-refusal-outside-a-run)
and from the runs parked on a limit — each run's own record, since a park is
written there and not in the log — against what each agent is configured to
ask for and to fail over to: a hold stands while a refusal the provider has
not said lifts yet covers the model every agent's turn ends on — its alternate
where it names one, its own model otherwise — and at least one of those
refusals was a turn that actually stopped, or a run that actually parked,
rather than one an alternate served through. The sentence counts the two as
what they are, `2 runs parked and 20 turns refused since …`, and a run is one
refusal however many probes it makes while it waits. A refusal that names no model,
which is every one recorded before 2026-09-13, counts only where every agent
asks for the same thing, because on a project whose agents differ it cannot be
attributed — and it counts as a refusal of the model they ask for first, never
of an alternate, so enabling failover after such refusals were written ends
the hold they made rather than turning it into one over an alternate that is
being served. Unlike the session's window it is on the attention line as well,
because it is the one capacity state with a move in it: the window is the
provider's, and the configuration that let one window hold every role is yours.
Between 2026-09-08 and 09-13 the harness recorded 134 of these refusals and
said nothing about what they added up to; this is what says it. Where the
session's own window and this are both true, the session's is the banner —
it is the same fact with less inference — and the hold is still on the
attention line. Nothing else is ever put above the four lines: every other
reason the harness is choosing nothing is inside them.

Naming an item leaves the four lines out. They are about the product, and a
question about one piece of work is a different question. `--json` carries the
same derivation under `standing`, so a second surface reads the answer rather
than parsing the rendering. Two things it carries are not printed, because the
lines say them by omission: `standing.startable` is how many admitted items
nothing refuses — the work the harness pulls next, counted over the same
entries as the refusals, and zero whenever the pass-level stall stands — and
each running run's `stage` is its phase folded onto `developing`, `reviewing`,
or `integrating`. Both are there for the dashboard's pipeline, so it reads the
model's count and the model's fold rather than making its own. Beside the
counts, `standing.admitted_items` and `standing.startable_items` name the
items each count counts, by id and title, in the product manager's order — so
the page's pop-up on a grouping lists what the figure counted rather than a
list assembled from the other lines — and both are absent, like the refusals,
where the queue could not be read.

Each entry under `standing.needs_human` is the thing waiting rather than a
sentence about it: its `kind`, from a closed set — `amendment`,
`conversation-carried-item`, `report`, `owed-step`, `publication`,
`degraded-service`, `hold`, `directive`, `outage`, `stall`, `held-work` — the
`id` of the record it is about (an amendment's, a directive's, a run's, a work
item's, a service's name, or which switch a hold is: `operator`, `intake`, or
`capacity`), the `mover` whose move it is, in the same closed vocabulary the
page counts by (`operator`, a role such as `architect` or
`development-manager`, `harness`, `forge`, `nobody`, or `unnamed-role`), and
the record itself, whole, under a field named for the kind — `amendment`,
`directive`, `outage`, `stall`, `reports`, `service`, `owed_step`,
`publication`, `held_work`, and for a hold `operator_hold`, `intake_hold`, or
`capacity_hold`, whichever switch the `id` names. An `amendment` carries the
target document, the proposer's role, agent, run, and work item, the proposed
change, and why, none of it cut to a line. An entry about one admitted work
item — the carried item, the item a run was carrying, the item an amendment's
proposer was working on — names it under `work_item_id` as well; a carried
item carries its `executor` marker rather than a record of its own. Two kinds
carry no `id`, because each is about a set rather than a record: `report` is
the pile, and `held-work` is how many items are in one of the two waits. The
`what` and `whose` the terminal prints are there beside them, and they are
derived from those fields at the moment the answer is written rather than
stored, so a record and the line about it cannot disagree; a document whose
`what` says something its fields do not, or whose `kind` or `mover` is outside
its vocabulary, is refused when the model reads it back.

One thing is carried there that the four lines do not print: what is parked or
held on provider capacity, one run and one conversation at a time, under
`standing.capacity_blocked`. The hold above is every role refused at once; this
is each thing the provider has stopped on its own. `runs` lists each work item's
latest run that is either `waiting` — in flight and asleep on a recorded
deadline, still counted on the running line — or `capacity-blocked`, which is a
run the provider refused and the harness would not wait for. One stopped by a
usage window resetting past the maximum pause gave its item back to the queue
and names the reset it is pulled again after; any other stopped with a blocker
on its item. Each says what refused it, since when, the reset it is
waiting out or none, how much of `execution.usage_limit_max_pause` it has spent
(`waited_seconds`), whether its change is preserved, and what a person can do about it — for a
waiting run, that nothing needs doing. `conversations` lists each conversation
the provider is still refusing, read from
[the refusals recorded outside a run](#a-provider-refusal-outside-a-run): one
entry per conversation however many turns were stopped, since the earliest
standing refusal, until the latest reset any of them named, with the turns an
alternate served through not counted. A run waiting on a login or a network is
not capacity and is not here; the outage banner says it. Both lists are always
present, and each says under `runs_problem` or `conversations_problem` when
its records could not be read rather than reporting an empty list. It is not
printed as a fifth line: it is the read model's capacity query, carried for the
capacity panel and for scripts.

## When nothing happened at all

Under the four lines, `yoyo status` reads back every stretch in which this
product went quiet: nothing started at all, while the tracker reported work
ready, and no hold, no full machine, no still-moving run and no provider usage
window accounted for it.

```text
nothing started on this product for 7h30m0s from 2026-09-01T06:05:00Z, with 3 items ready; it cleared at 2026-09-01T13:35:00Z
  the session choosing work last recorded watching at 2026-09-01T06:05:00Z, and has said nothing since
  cleared by: 1 developer run(s) are in flight and still moving
```

The second line is the one to act on. A stall cannot say why it happened —
it is precisely the absence of anything having been written down — so what is
recorded beside it is the last thing the watch log holds, and that is what tells a
scheduler that died from one that is wedged: a session whose last word was
`stopped` wants starting, and one still claiming to be `watching` wants killing
first.

**The silence is measured from the last moment anything held a developer
slot**: the later of the last run start and the last run end. That is the
`from` in the first line. A slot is held for the whole of a run, so a run
ending moves this moment on just as a run starting does. Until
yoyodyne-ifd.428.19 it was measured from the last start alone, and that was
wrong in one repeating way. The watch fills every free slot in one pull, and
runs take more than an hour. So when a batch ended there was an instant with
nothing in flight and a last start over an hour old. The watch read a stall at
exactly that instant, a second before the pull that refilled the slots. On
2026-09-24 the last twenty alarms were all that shape. A slot free for seconds
before a pull is not a stall now. Slots free for the whole threshold still are.
One kind of end does not count. A run whose process was already gone is ended
by the harness: `yoyo reconcile` settles it, or the claim audit cancels its
claim. That end is when the harness noticed, not when the run let its slot go.
So such a run counts as holding its slot only until its record last moved,
which the settlement keeps on the run as `settled_quiet_since`. A sweep
therefore still opens the stall right after it settles a dead line, dated from
when the line went quiet rather than from the settlement.

What the message that wakes somebody says beside that is the last poll's own
account of the queue — "33 of the 47 admitted items are awaiting carry-out of
decisions already recorded", and the next mover with it — which it reads from the
watch log rather than from the stall, and only where that poll was made after the
silence began.

What that bound refuses is an account a start overtook: something ran after the
poll and the line then went quiet, so the queue has not been read since it moved.
A session that died carrying a run is the usual way that happens, and there the
message names no cause and points at the chooser. It does not refuse the account
of a session that died while idle, which polled after the last start — that
message names the cause, because the items really are held, and a named cause is
therefore no evidence that the session is alive. The chooser's last word is what
says that, in the message exactly as in this listing: a session that last recorded
something and has said nothing since wants looking at whatever the queue is
holding. This listing keeps that last word and no cause, because the stall record
is the absence and the account of what was in the way of the queue belongs to the
session that read it.

This is the one history in the harness that nothing else keeps, and the reason it
exists is that the process which would have recorded a stall is the process a
stall means has died. A session that crashes writes no stop, so every other
surface reads a dead machine as a quiet one — which is exactly what happened on
2026-09-01, for seven and a half hours, until a person noticed.

**Two things notice, and between them they cover the two ways it happens.**
[`yoyo work --watch`](work.md#letting-the-harness-choose-the-work) takes the
reading as it polls, at most once per `--stall-after`: that is the harness's own
loop, and it catches the session that is alive and has stopped starting anything —
a queue whose ready items are all claimed by runs that died, say. A session that
died itself writes nothing at all, so [`yoyo reconcile`](#recovering-interrupted-runs)
takes the same reading as the last reading of the sweep that settles what a dead
process left behind — after every settlement, and before the one step that
follows it, [continuing a usage-limit wait](#waiting-out-a-provider-usage-limit)
whose deadline has passed. That ordering is why it is that sweep and not another: a
killed run goes on saying it is in flight until the settling, and a phantom run
counted as activity would silence this for exactly the crash it exists to catch.
The two readings can land at the same moment. Each one reads the stall log and
appends to it under one lock shared across processes, so a stall is opened by
exactly one of them. The log then holds one line when a stall opens and one
more when it closes, with the same event id. `yoyo status` and the channel read
that pair as one stall.

Reporting has nothing to do with either. A product that never turned Slack on
records its stalls and reads them back here, and a product that did gets the same
record [taken to the operators](reporting.md#reporting-into-slack) — again every
heartbeat and louder as it stands — by a sink that reads it rather than produces
it. That was the other way round until
`yoyodyne-ifd.295`, and it meant the products least able to notice a stopped
harness were the ones with no stall history at all.

How promptly a stall is noticed is `--stall-after` — ten minutes by default, and
the same flag on both commands — and, for the sweep, the cadence of whatever runs
it. Nothing `yoyo` installs runs the sweep on a schedule yet: the maintenance
pass is the supervisor's periodic pass, `yoyodyne-ifd.413` (which absorbed
`yoyodyne-ifd.207`), and until it lands scheduling it is yours —
[`yoyo start`](#starting-the-product-and-stopping-it) says so on the
maintenance line. A machine
running neither a watch session nor a sweep records no stalls, so this listing is
empty on one; the sink says so when it starts, because that is the state nobody
would think to check for.

A product that has never gone quiet says nothing here at all. The five most
recent stalls are printed, newest first, and `--json` carries every one of them
under `stalls`; naming an item leaves them out, because a stall is about the
product rather than about any piece of work.

## Claims with nothing working on them

The reading above catches a line that has stopped over a queue with work in it,
and it is blind by construction to the case beside it. The harness claims a work
item at the start of every run, and nothing gives the claim back when the process
holding it dies — so what a killed run leaves is an item the tracker calls in
progress, a run record nobody is writing to, and a scheduler that will never
choose the item again, because it chooses from what the tracker calls ready and a
claimed item is not ready. An item under a dead claim has left the ready queue,
so a machine whose only startable work is sitting under one reads from every
surface here as a drained queue: nothing held, nothing running, nothing ready,
nothing wrong.

That cost four nights of throughput in the week of 2026-09-01, twice inside an
hour on the last of them, and each time the line was idle until somebody looked
in the morning.

A watching session now audits the claims against the runs on every pull, before
it consults the intake hold or its own capacity, and before it sits out
[a provider nobody can reach](#waiting-out-a-provider-nobody-can-reach) — which
is the placement that matters, because a held queue is often the session's own
brake, placed exactly when runs are failing one after another, a machine whose
every slot is held by a run that died never gets as far as reading the queue at
all, and a run killed before a login expired is exactly as dead through the wait.
A claim with no run alive behind it for half an hour is given back:

```text
yoyodyne-ifd.264 was claimed with nothing working on it and was given back to the queue: its run run-264 ended failed at 2026-09-03T22:14:00Z and the claim outlived it
```

Giving the claim back is only half of it, and the other half is what makes the
item actually startable. A killed process leaves a run record that still says it
is in flight, so it goes on filling a developer slot and the scheduler goes on
passing the item over as already running however open the tracker says it is. So
the audit ends that record too, under the run's own lease — which is also the
only test of liveness that is not a guess, because a lease belongs to a live
process and the operating system drops it when that process dies, so a lease the
audit can take is a process that is gone. The ending is recorded as **cancelled**:
nothing here judged the change, and the branch and worktree the killed run left
are untouched, exactly where they were. The item is then pulled again on the same
pass, with nobody having typed anything.

This settles a much narrower set than [`yoyo reconcile`](#recovering-interrupted-runs)
does and never overlaps it: it moves no refs, removes nothing, closes nothing,
and touches only runs that have gone quiet past the threshold with no wait
recorded on them.

A tracker that will not answer costs the audit and nothing else. It is the one
reading in the pass that is reported rather than retried, because the pass has
answers that need no tracker at all — a held intake is read from a switch — and
an operator running `yoyo work` on a machine whose tracker is down should still
be told what is holding it.

Alive means the run's own record still moving, not the status field saying it is
in flight: a killed process leaves a record that goes on claiming to be running,
so the test is whether anything has been written to it within the hour — and then
whether its lease can be taken, which is the answer the timestamps were standing
in for.

Four kinds of claim are never given back. A run that stopped short and is owed a
continuation keeps its claim however quiet it has gone — one waiting out a
[usage limit](#waiting-out-a-provider-usage-limit) or an
[overloaded provider](#waiting-out-an-overloaded-provider), which the
reconciling sweep continues itself once the deadline has passed and no process
is serving the wait, one waiting out
[a provider nobody can reach](#waiting-out-a-provider-nobody-can-reach), one parked by
[`yoyo pause`](#pausing-everything-and-resuming-it), one held up by an
unresolved directive or by work its item depends on, one parked because
[the tracker would not answer](#waiting-out-a-network-that-dropped) the read a
gate boundary makes, and one whose provider
[the harness stopped on time](#when-a-provider-stalls-or-runs-out-of-budget) —
which the audit leaves as a wait and the reconciling sweep, not the audit,
settles once nothing has continued it for half an hour. Each of those returns and
lets its process exit, so its record goes as still as a killed one's, and its
item is claimed on purpose with the worktree and developer session that
continuation needs. Every one of those is a wait that is still pending, which is
the whole rule: how many repair rounds a run has already been through is history
rather than a wait, so it says nothing about whether the run is coming back and a
run killed after one is a dead claim like any other. The question is asked again
under the run's lease before anything is written, so a run that parks between the
two keeps its claim.

And a claim with no recorded run behind it at all is left alone: the harness
reserves a run before it claims anything, so such a claim is somebody's own and
not the harness's to take back.

An item whose latest run already promoted its change is left alone too, and that
one is left for `yoyo reconcile` rather than for a person: the change is on the
target branch and the item wants closing rather than developing a second time,
which is what the sweep does from the promotion the record holds.

**And an item whose latest run ended holding its change is left alone, whatever
the claim says.** A claim there is not a claim nothing is working on: it is the
one thing saying an item whose change is sitting on a branch is spoken for, and
giving it back buys a fresh run started over the top of that change. Three
endings are in the class, and they are the same three the pull's own
[hold](work.md#letting-the-harness-choose-the-work) reads, so the two cannot
disagree about one run:

- **An approved change the environment stopped** short of its promotion, which
  `yoyo triage resume` finishes. The audit leaves it for that verb, as it leaves
  a wait for the sweep.
- **A run that left somebody a blocker** with its change still on its branch — a
  replay that conflicted against a target that moved, first among them.
- **A run that died inside its own process** with its change still on its
  branch. This one is the ending nothing announced: such a run deliberately
  hands nobody a blocker, so its record ends `failed` rather than `stopped` and
  every rule that looked for a blocker read it as an item with nothing holding
  it.

That last shape is what this rule was written for. On 2026-09-22 run-b0b6d18d's
change on yoyodyne-ifd.436.4 was approved and then stopped at the promotion by a
tracker read that timed out; the run ended `failed` at 04:04Z with its branch
preserved and its pull request open, and the docket named the harness and `yoyo
triage resume`. At 05:00Z the audit read the item as a claim with nothing
working on it and gave it back, the next pull started a run from scratch, and
that run spent a developer and a reviewer re-deriving the change that was
already on the branch and integrated it a second time.

A run whose process was killed is none of these, and is a dead claim exactly as
it was however much of its half-finished change is on disk: nothing ended it and
nothing owes it a move, which is the whole failure the audit exists for. What
tells them apart is the ending on the record rather than what is in the
worktree.

Whether the change is still there is asked of the repository as the audit reads
the claim — the run's branch and its checkout, looked for — and never of the
run's removal flags, which are what a cleanup remembered to write. A look that
could not be made keeps the claim. And every release says on the item what the
look found, because "nothing was working on it" is a sentence about processes
and was read as one about the change: on 2026-09-23 the development manager
crossed yoyodyne-ifd.432.10's re-run cap reasoning that run-838ffc48 had left no
preserved change, from a release that said nothing either way, while the run's
branch held the approved change. A release now reads:

```text
The harness gave this item back to the queue at 2026-09-23T01:43:04Z: its run run-838ffc48… ended cancelled at 2026-09-23T01:01:32Z and the claim outlived it. Nothing was working on it, so it was released to be pulled again.
What the run left: branch yoyodyne/yoyodyne-ifd-432-10/838ffc48 (checked and there at 2026-09-23T01:43:04Z); worktree …/yoyodyne-ifd-432-10-838ffc48 (checked and NOT there at 2026-09-23T01:43:04Z). That change is still there; a run started for this item should pick it up rather than derive it again.
```

A release written before the audit looked says none of that, so
[the convergence sweep](#recovering-interrupted-runs) corrects it: where a
released claim's run still has its branch standing and the release did not say
so, the sweep appends a correction to the item naming the branch, the commit it
is at, and why it is kept, and records on the run that it did, so the item is
told once. `yoyo reconcile` prints the branch as kept with its item corrected.

Each release is
[sent to the operators once](reporting.md#reporting-into-slack), in the item's
own thread, as the degraded harness it is. It is said once and never repeated:
what follows a release is the item being pulled again, which says so itself.

The two deaths are worded apart because they are two different histories and a
reader acts on the difference: a run that ended left the claim behind by
finishing without giving it back, which is a hole in the pipeline, and a run
still recorded as in flight is a process something killed. What is done about
them is the same either way, so neither line hands you a chore.

## Conversation-carried work that has landed

The other thing a pull closes for you is work no run ever carried. An item
admitted for a role's conversation — `executor: conversation:architect` — lands
as a revision in a document that role owns, and until 2026-09-20 nothing read
that revision back to the tracker: the product manager closed such items on
evidence some turns later, and one of them had a developer run spent on it
first. Now every pull reads the documents each marked role owns, and an item
whose identifier opens the reason of a revision in one of them, made by that
role, is closed at that pull:

```text
yoyodyne-ifd.330 was closed: its architect landed as the 2026-09-07 05:30:00Z revision of docs/designs/management-and-supervision.md
```

The close reason on the item names the document, the revision, the convention
it was read by, and the revision's own words, so a close you disagree with is
one you can read and reopen with a note — and the reopen holds, because the
revision is recorded on the item ahead of the close and a pull that finds the
item open and still carrying it leaves it alone until a later revision lands.
A revision that mentions an item further into its reason closes nothing, which
is what keeps an item with two deliverables open on a revision that carries
one; a landing the tracker will not close is reported on the pass and left for
you, and so is the one case a person has to clear by hand — a close the tracker
refused after the revision was recorded and the record could not be taken back
off, which the pass names with the metadata key to clear. [How work
flows](work.md#letting-the-harness-choose-the-work) says what is read and why,
and [the artifacts manual](artifacts.md#artifact-identity) says how to write
the revision so it is read.

## What became of the runs, and what remains of them

Under the stalls, `yoyo status` reads back what the runs themselves recorded
— newest first, the work item, the outcome and the phase the run reached, what
remains of it, what it cost, why the item was chosen, and the reasons its record
kept:

```sh
./bin/yoyo status                    # the four lines, then the twenty most recent runs
./bin/yoyo status --failed           # only the ones that did not succeed
./bin/yoyo status yoyodyne-ifd.90    # one item's runs, without the four lines
./bin/yoyo status --limit 0 --json   # every recorded run, for a script
```

A run's cost is read from its whole event log, so the listing prices only the
runs it shows: the default twenty cost twenty log reads however many runs the
product holds. `--limit 0` is the exception you ask for — it shows every run,
so it reads every event log the product has written, and its cost grows with
the product's history.

The listing below is `./bin/yoyo status --failed --limit 2`:

```text
runs that ended without succeeding, 2 of 9 shown (137 run(s) recorded):
run-19dc9dff153e1eb89a2470f78f02f240 yoyodyne-ifd.1.7 started 2026-08-16T18:02:11Z [stopped, developing, work preserved, checked] $4.62
  selected by the operator: the operator ran this item by name from the command line
  ran under default, configuration cfg-9f2c41ab7e05, harness 9870df6a1b2c
  reason: the provider ended this run without judging the work after 3 of 3 permitted relaunch(es)
  branch (checked and there at 2026-08-16T19:40:02Z): yoyodyne/yoyodyne-ifd.1.7/19dc9dff
  worktree (checked and there at 2026-08-16T19:40:02Z): /Users/you/Library/Application Support/Yoyodyne/state/worktrees/yoyodyne/yoyodyne/yoyodyne-ifd-1-7-19dc9dff
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

Beside it, every run that did not succeed says what remains, and it says it
from the repository rather than from the run's removal flags: the listing looks
for the run's branch and its checkout as it is printed. `work preserved, checked`
is either of them found there, `work gone, checked` is neither, and `no
artifacts recorded` is a record that names neither. Where the repository could
not be asked, the listing says so — `work possibly preserved, not checked`, with
the reason on the artifact lines — rather than reading the flags out as though
they were a look; a flag is what a cleanup remembered to write, and on
2026-09-23 run-838ffc48's flags said removed while its branch held the approved
change. The branch, the worktree, and the developer session are then named under
the run, each with what the look found and when, so looking at the change is not
a trip through the run's JSON for a path. `--json` carries the same answer as
each run's `found`. A successful run removes what it made by design, so it says
nothing about preservation at all; a run still in flight holds everything it
has.

The third phrase states an absence rather than claiming the run made nothing —
the same discipline as the `selected: no reason recorded` and `an account the
record does not name` lines below, and for the same reason: a listing that turns
an empty field into a reassurance is the failure this one exists to remove. In
practice it is a run that broke before it got a worktree, which is also why the
second run above has no phase between the two words: the phase is only recorded
once the worktree exists, so any run carrying one has a branch and a worktree and
reports what the look found of them with the paths underneath.

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
`yoyo config show` prints the active revision. A run recorded before any of the
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
  merge re-arms: 1 across every publication of this item, 1 permitted per publication
    publication:run-a#92: 1 of 1 permitted
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

Every figure here is a budget, and the first line counts what has been spent
rather than how many times somebody looked. Only three of the development
manager's seven decisions spend anything — a repair grant, a re-run, a merge
re-arm — so an item it escalated or told to wait shows `triage has spent nothing
on it` and zeroes across the rest; a cap it crossed moves a ceiling rather than
a count, and is reported on the crossing lines beside these. **That is not evidence nobody looked.** The
decision itself is recorded on the work item, which is where to read whether
stopped work has been decided and what was decided; an escalated item is blocked
there as well.

A **round** is a reviewer verdict that sent a developer attempt back, counted
across every run of the item. A re-review no developer attempt produced is not
one, so a promotion that [loses its race](configuration.md#losing-a-race-for-the-target-branch)
and gets a fresh verdict on the replayed change is not charged for it, whichever
way that verdict goes. Neither is a verdict that approved the change: the cap
stops an item buying the same argument another round, and an approval ends the
argument. Neither is a repair whose whole residue is one minor finding — the
reviewer said the work is right and named one small thing beside it, which is the
same ending with a note attached; the work still goes back to the developer and
still spends one of the run's own repair attempts. An uncharged verdict is still
recorded rather than passed over, because the exclusions are one mechanism — an
attempt already answered about is charged at most once — and a promotion only
ever follows an approval. Rounds are what runs actually spend, and every run
records them.

The lines under it are the budget for what triage can decide about work that did
not land — another go at the change, a re-run, a re-armed merge — and they move
when [the development manager decides one](conversation.md#deciding-what-becomes-of-stopped-work).
Each is recorded before the action it counts takes effect, so a crash cannot
double-grant, and each is refused once its budget is spent. A grant and a re-run
are each once per item and are also refused by the rounds — the grant truncated
to what the cap still has room for, the re-run refused outright once none
remain — and a merge re-arm is bounded on its own because it buys no round at
all. It is the one budget here that is not the item's: what a re-arm repeats is
one merge request the reviewer's verdict already authorized, so it is bounded
once per publication, and the line above names each publication that has spent
any of it. An item that published three times has three separate budgets, and a
second drop of one publication is an escalation rather than another re-arm. The
rounds alone would bound neither of the first two on an item whose runs
keep stopping before a reviewer ever sees them. The
numbers are the `triage` keys in [the configuration
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

## Watching from a browser: the dashboard

`yoyo dashboard` serves what `yoyo status` reads — the four lines, the
capacity state carried under them, and what landed and what it cost — to a
browser on this machine, as one page of five sections, and keeps serving it
until you stop it:

```sh
./bin/yoyo dashboard              # a port the operating system chooses
./bin/yoyo dashboard --port 8765  # one you can bookmark
```

The configuration's [`services.dashboard`](configuration.md#services) entry
declares the dashboard as a part of the product — its port, the address it
binds, the hosts a request may name, and where a supplied token comes from —
for the supervisor that will start it with the rest. The supervisor is here
([`yoyo start`](#starting-the-product-and-stopping-it)) and the dashboard is
not yet its child: with the entry enabled, `yoyo start` says so and names the
work that adopts it, `yoyodyne-ifd.414`. Until that lands, this command is
started by hand and still binds loopback and serves on `--port`, exactly as
below; the one thing it reads from the entry is `token`, and it reads that
whether or not the entry is enabled.

With `services.dashboard.token` at its `generated` default it prints two things
when it starts, and the second of them once:

```text
dashboard for yoyodyne serving at http://127.0.0.1:52341/
token: 9f2c41ab7e05…
the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, and /api/items/<work-item-id>
it is printed here and nowhere else, and a restarted dashboard prints a new one; stop with ctrl-c
```

**A configured token outlives a restart.** Set `services.dashboard.token` to
`keychain` or `file` and the command reads the token from the store the
setting names — the keychain item `yoyo-dashboard.<product id>` under the
account `yoyo`, or the file `<state root>/products/<product id>/dashboard.token`
— and serves under it, so a restart serves under the same token and nobody is
handed a fresh one to paste. It prints where the token was read from and never
the value:

```text
dashboard for yoyodyne serving at http://127.0.0.1:8765/
the token was read from the keychain item yoyo-dashboard.yoyodyne under the account yoyo, as services.dashboard.token names, and is not printed
the page asks for the token and keeps it in the tab's session storage; a tool sends it as `Authorization: Bearer <token>` to /api/standing, /api/throughput, and /api/items/<work-item-id>
it outlives a restart: a restarted dashboard reads the same one; stop with ctrl-c
```

A store that does not hold the token refuses to start, before anything is
bound, with the command that stores it — the same one
[`yoyo doctor`](#checking-the-installation) prints under `service:dashboard`:
`security add-generic-password -s yoyo-dashboard.<product id> -a yoyo -w` for
the keychain, which prompts for the token so it never reaches a shell history,
and `mkdir -p … && (umask 077 && openssl rand -hex 32 > …/dashboard.token)` for
the file. A stored token is accepted from the bearer header alone, exactly as a
generated one is. The keychain is macOS's; on another platform the `keychain`
source refuses by name and says the file is the store that platform has.

Open the URL, paste the token into the page, and the page shows where the
harness stands and asks again every ten seconds. The same answers are served as
JSON to anything that sends the token as a bearer header: at `/api/standing`,
the `standing` object `yoyo status --json` carries, from the same derivation,
so the page and the terminal cannot disagree about a number; and at
`/api/throughput`, what landed and what it cost over today and the last seven
days, counted from the run records `yoyo status` derives each run's outcome
from and priced by the same reading `yoyo status --spend 7` prints. The second
reading prices every event log a week holds, which is seconds of work, so the
page asks for it once a minute rather than every ten seconds. A third answer is
served one item at a time, at `/api/items/<work-item-id>`: the work item whole
— the tracker's own fields, and the run the harness last made for it as
`yoyo status <item>` lists it — which is what the page's
[card on an item](#opening-a-work-item) reads. It costs a tracker command, so
the page asks for it when a card is opened and not before, and never reads the
tracker itself. An id the tracker holds nothing under is refused as not found,
in fixed words that do not name the id back; an id that is not the tracker's
shape is refused before anything is asked.

### What the page presents

Five sections, top to bottom, each drawn from the read model and from nothing
else. Above them, one banner and only one, while it stands: the same sentence
the terminal prints above the four lines when the harness is paused on the
provider's usage window, when every role is held by one, or when the provider
is answering nobody. Beside the product's name the page says when the reading
was taken and that it asks again; a poll that fails after one that succeeded —
of either reading, the standing or the throughput — marks the page **stale**,
says which reading failed and which it is still showing, and the throughput
section says the same under its own figures, rather than going blank on one
dropped request.

1. **Where the harness stands** — a tile for each of the four lines: running
   developer runs, conversations with a turn in flight, admitted items nothing
   will pull (out of how many are admitted, and how many await a decision or
   the carrying out of one), and what waits on a person, which says `nothing`
   in words when it is nothing. That last tile counts per mover, in the read
   model's own vocabulary for who each entry is waiting on: its figure is what
   waits on the operator, and beside it, out of the line's whole count — the
   figure the terminal prints — what waits on the product manager, the
   architect, the development manager, and the harness, in the model's order
   and each named only where it is not zero. The tile asks for attention when
   something waits on the operator, not when sixty things wait on a role. Its
   label is a button that opens [the list of what is waiting](#opening-what-waits-on-a-person),
   each entry of which opens a card. Two
   more tiles carry what landed today and in the last seven days, and what it
   cost, the latter prefixed `≥` or `at least` where a record that should be
   in it could not be read.
2. **Running now** — a card for each developer run and each conversation with a
   turn in flight: the work item's title and id, the phase (or `approved,
   resuming integration` where that is what the run is doing), how long it has
   been going, what it has spent so far or `cost unknown` and why, and the
   provider, model, and account alias it is spending. The title and the id
   each open [the item's card](#opening-a-work-item). A conversation card says
   the agent, its role, how long the turn has been in flight, and how many turns
   are recorded before it.
3. **Where the work stands** — the pipeline, read left to right: admitted items;
   how many are held back, split into the piles the queue itself names — held
   for a person (awaiting a decision or awaiting carry-out), paused by a
   directive, pullable with nothing choosing, parked, waiting on other work,
   covered by its own unfinished children,
   carried by a conversation rather than a run, and not offered for a reason
   nothing here can read — each with who it is waiting on, and the largest marked
   `(most)`; how many are startable and next to be pulled — or, while a stall
   holds every pullable item, that the harness is choosing nothing and why;
   how many are running, by stage; and how many landed today and this week.
   Under it, in words, how many things wait on a person, and then how many of
   them wait on each mover, the operator's first. Every stage's label
   and every pile's label is a button that opens [the list of the items in it](#opening-a-work-item).
4. **Throughput and cost** — two columns, today and the last seven days, each
   labeled with the local days it covers: how many runs landed their work on
   the target branch; the other endings, in the run history's own words
   (stopped for a person, cancelled, timed out, failed, and succeeded without
   promoting anything); how many runs started; and what every priced invocation
   cost, split into runs, conversations, branch reviews, and exchanges, with the
   count of records that could not be priced named beside the figure whenever
   there is one, because a cost with a hole in it is a floor rather than a
   total.
5. **Provider capacity** — the capacity-blocked state under
   `standing.capacity_blocked`: each run parked or held on provider capacity,
   with what refused it, since when, the reset it is waiting out or that none
   was named, how much of its pause budget it has spent, whether its change is
   preserved, and what to do about it — `nothing needs doing` for a run that is
   only asleep, and the remedy for one that stopped; each conversation the
   provider is still refusing, with its model, its refusals, and its reset; and,
   when every role is held at once, a line saying so with the agents, the
   models, the alternates or the lack of them, the refusals, and the reset.

Every section has four states and shows exactly one. **Loading** says it is
reading, and for the throughput section that it is pricing the week, with one
slow pulse that stops for a reader who asked for reduced motion. **Empty** says
in a sentence that there is nothing — the harness is idle, nothing is running,
the backlog is empty, nothing ran and nothing was spent in the last seven days,
no run or conversation is waiting on capacity — because a panel with nothing in
it and a panel nobody filled look the same. **Error** says what could not be
read, in the read model's own words, and what to do: which command says the
same thing with more room, and that the page keeps asking. **Ready** is the
content above. A section whose sources could only partly be read stays ready
and lists each unreadable source under its content, and a tile or a stage
whose source could not be read shows a dash and the words `could not be read`
in the figure's place — the pipeline with the tracker unreadable still draws
what is running and what landed, and its first three stages say they could not
be read; nothing on the page ever shows a zero for a line the model did not
answer.

Every distinction survives without colour. A state is a word in a badge as
well as a tint, a problem is `Could not be read` as well as a red rule, a
waiting run and a blocked one differ in the word and in a solid against a
dashed rule, the largest pile says `(most)` as well as being bold, and the
stages are joined by an arrow character rather than by a coloured bar. The page
follows the reader's light or dark setting and their reduced-motion setting,
and holds its badges' edges under forced colours.

The words are the terminal's wherever the terminal has them — `no developer
runs`, `12m elapsed`, `$3.41 so far`, `cost unknown (its event log is gone)`,
the refusal each item carries, the remedy each parked run carries — because the
page and `yoyo status` are two projections of one model and a reader moving
between them should not have to translate.

### Opening a work item

The sections show counts and names; the items behind them open in two pop-ups,
each a dialog over the page that closes on its **Close** button, on a click
outside it, or on Escape, and puts focus back on what opened it — on the
button now carrying that item or grouping where a poll has redrawn the page
in between.

**A grouping's items.** In *Where the work stands*, the label of each stage —
Admitted, Held back, Startable, Running, Landed — and of each pile under one
(`held for a person`, `developing`, and the rest; the week's landed line is a
grouping of its own beside today's) ends in a chevron and opens a list of the
work items in it, by title, with the id under each and the pipeline's own word
for it beside: the refusal for a held-back item, the phase and elapsed time for
a running one, when it landed for a landed one. The list is read from the
readings the page already holds — the standing names the admitted, startable,
and refused items and the throughput names the landed runs, so it is what the
figure counted rather than a list assembled on the page — and is drawn again
on every poll while it is open. It has the four states a section has:
**loading** while the landed figure is still being priced, **empty** saying in
a sentence that no item is in the grouping, **error** with the reason the
grouping's source could not be read — a stage showing a dash still opens, so
the reason is readable in full — and **ready**. Each title in it opens the
item's card, over the list.

**One item's card.** Opened from a running item's title or id, or from any
entry of a grouping, the card shows the item whole under plain labels: **Id**,
**Title**, **Status**, **Priority** (bd's `P0` to `P4`, 0 the most urgent),
**Labels**, **Parent**, **Description**, **Design**, **Acceptance criteria**,
**Notes**, and **Run**. A field the item has nothing in says `none`, so a blank
is never mistaken for a field the page did not read, and prose keeps its line
breaks. **Run** is the run the harness last made for the item, in the words
`yoyo status <item>` lists it in: `in flight — developing, 12m elapsed, $3.41
so far` for one still going; `preserved: stopped, reviewing — work preserved,
checked` for one that ended with its change still on a branch or in a checkout;
and otherwise that nothing is in flight or preserved and what the latest run
came to, `work gone, checked` or `no artifacts recorded`. What survives is
looked for in the repository as the card is read, exactly as `yoyo status` looks
for it, rather than read off the run's removal flags; a look that could not be
made says `not checked` and why. Under the line are the run's id
and when it started and ended, its cost or `cost unknown` and why, the reason it
gave for ending, and the branch, worktree, and developer session it preserved.
An item never run says `none is recorded`; run records that could not be
opened say why in the run's place rather than the card showing an item nothing
ever touched. The card is read once, when it is opened, from
`/api/items/<id>`; it has the same four states, and its **empty** state is the
tracker holding nothing under the id — an item closed or removed since the
page last read the standing — which is a different answer from the item not
being readable, and is said as one. Every value on it reaches the page as JSON
and is written as text, under the same policy as the rest of the page.

### Opening what waits on a person

The **Needs a human** tile counts; its label opens the list of what it
counted, and each entry of the list opens a card, in the same two pop-ups as
above. Both are drawn from [the structured entries the standing
carries](#where-the-harness-stands-the-four-lines) under
`standing.needs_human` and from nothing else — the page never reads the
tracker or the amendment store for them — and both are drawn again on every
poll while they are open, so they stay as live as the tile.

**The list.** Each entry is the thing waiting, in the sentence the terminal
prints for it, with its `kind` under it and the terminal's second sentence
beside — who it is waiting on and what settles it, `the architect's — nothing
reaches the document until they or the operator decide it` — in the movers'
order, the operator's first, so the list opens on what the tile's figure
counted; within one mover the entries are in the terminal's order. It has the
four states the grouping list has: **empty**
saying `Nothing waits on a person.`, **error** with the reason the line could
not be read — the tile showing a dash still opens it, so the reason is
readable in full — **loading** while the standing has not arrived, and
**ready**. Each sentence is a button that opens the entry's card.

**An entry's card.** Headed by what sort of thing it is, in plain words — *A
change proposed to a document*, *A run that still owes a step*, *A work item
carried by a conversation* — with the entry's key and when the standing was
read under the heading. Every card opens with the terminal's two sentences,
**What** and **Waiting on**, then the entry's **Kind** and **Mover**, and
then the record the kind names, whole, under plain labels:

- an amendment's card shows the **Document**, its **Document kind** and
  **Owner**, who it was **Proposed by** (the role, and the agent), **In run**
  which run, **Working on** which item, the **Proposed change** and **Why** in
  full with their line breaks kept, when it was **Raised**, and its **Id**;
- an owed step's card shows the **Run**, its **Work item**, and where it
  stopped — **Ended** with the run's recorded status, and its **Phase** — and
  the command that settles it is in the *Waiting on* sentence above them,
  `yoyo reconcile`;
- a conversation-carried item's card shows the **Work item**, its **Executor**
  marker, and the **Role** the marker names;
- the other kinds — a publication, a degraded service, a hold, a directive, an
  outage, a stall, the report pile, held work — show their own record's fields
  the same way.

A work item the entry names is a button on the card that opens the item's own
card in its place, over the list; closing that puts focus back on the list
entry that opened the entry card. The card has the four states: **ready**;
**empty** when a poll finds the entry gone from the line — settled since the
card was opened — which it says in a sentence with the key it was opened on;
**error** when a poll finds the line unreadable; and **loading** while the
standing has not arrived. Every value on it reaches the page as JSON and is
written as text, and no button on it does anything but open or close a pop-up:
acting on an entry from the page is its own item (`yoyodyne-ifd.432.8`),
behind the architect's ruling on whose identity the page would act as.

**Seeing every state without a harness behind it.** `internal/dashboard/testdata/renders`
holds the page as its own script renders it from the fixtures under
`internal/dashboard/testdata/fixtures` — the document as the script left it,
keeping the one page state and the one state per section a browser would show
and dropping the hidden ones — one file per scenario — `quiet`, `busy`,
`held`, `degraded`, `unreadable`, `loading`, `throughput-pending`,
`throughput-refused`, `throughput-stale`, `refused`, `unreachable`,
`wrong-token`, `stale`, and `signin` for the page, and `card`, `card-loading`,
`card-missing`, `card-refused`, `grouping`, `grouping-landed`,
`grouping-empty`, `grouping-error`, `grouping-loading`, `grouping-card`,
`closed`, and `closed-after-poll` for the pop-ups on work items, and
`attention`, `attention-empty`, `attention-error`, `attention-amendment`,
`attention-owed-step`, `attention-carried-item`,
`attention-carried-item-card`, `attention-settled`, `attention-unreadable`,
and `attention-closed` for the list of what waits on a person and the cards
opened from it, each opened by clicking what a reader would click on
one of the pages and holding the pop-ups alone, over the page render it names
— which together show every section and each pop-up in each of its four
states. They are golden files:
`TestThePageRendersEverySectionInEveryState` runs the page's script under Node
against the fixtures, checks that each section and each pop-up reaches each
state and that the fixtures' words land on the page as text, and fails when a
render differs from what is recorded; `go test ./internal/dashboard -run TestThePageRendersEverySectionInEveryState -update-renders`
rewrites them after a deliberate change. Each render opens in a browser beside
the real stylesheet. To look at the live page in each state, with the real
server and the real policy in front of it, `go run ./internal/dashboard/fixtureserver`
serves one dashboard per scenario on a loopback port of its own and prints each
URL with its token.

**It is a projection and nothing else.** It reads the same durable records the
terminal reads and writes none of them; the only form is the one that takes the
token, every button on the page opens or closes one of its own pop-ups and
nothing else, and nothing but `GET` and `HEAD` is answered at all. Restarting it
changes nothing about the harness and loses nothing, because the history it
shows lives in the records rather than in the page. It is not a second control
plane, and work is still directed from the conversation and the commands above.

**What it will not do** is the part worth reading before leaving it running:

- **It answers only on this machine.** It binds `127.0.0.1` and nothing else,
  so nothing off the machine can reach it, and being on the machine is not
  enough on its own: every request for the read model has to carry the token.
  What is served without one is the page shell and its own script and
  stylesheet — static text compiled into the binary, with nothing of the read
  model in it, which a browser needs before it can present a token at all.
  Everything that reads state is behind the token.
- **The token is never in a URL, and never in a cookie.** A token in a URL
  reaches the browser's history, the referrer of every link on the page, and
  every log a proxy keeps, which is why the URL it prints carries none and the
  page asks for it instead. A cookie would be worse than it looks: browsers key
  cookies on the host and not the port, so a cookie on `127.0.0.1` is sent to
  every other service on every other port of `127.0.0.1` the browser visits,
  and two dashboards for two products would overwrite each other's. So the page
  keeps the token in the tab's session storage, which is scoped to the origin
  with its port, and sends it as `Authorization: Bearer` on each fetch; the
  server accepts it from that header and from nowhere else. Session storage
  ends with the tab, so a new tab asks again. A restarted dashboard under the
  `generated` source generates a new token, so a bookmark outlives the token
  and the page simply asks again; under `keychain` or `file` it reads the same
  one, and what the page asks for after a restart is the token it already had.
- **It refuses anything that did not come from its own address.** A request
  whose `Host` is not the address it bound — a name somebody pointed at
  loopback — and a request whose `Origin` is some other page scripting calls
  at the port are both refused before anything is served, the shell included.
- **It loads nothing from anywhere else.** Every response carries a
  content-security policy that allows script and style from this origin only:
  no CDN, no inline script, and no framing by another page. A value that reached
  the page unescaped would have nowhere to run, and none does: the product's own
  name is the one value the server writes into the page, escaped, and everything
  the read model says — work-item titles, an item's description and notes,
  refusals, the reason a source could not be read — reaches the page as JSON
  and is written by the page as text.
- **Every failure is a refusal, never part of an answer.** No token, the wrong
  token, a foreign `Host` or `Origin`, and durable state that cannot be read
  each get a status and a one-line reason, and nothing of the read model beside
  it; the page shows that reason in its error state and keeps asking. What the
  read model could read with one source missing is a different thing, and is
  said inside the answer line by line — the page says that line could not be
  read in place of its count, as the terminal does, and lists the reason under
  the counts, rather than counting an unreadable line as empty.

`internal/dashboard`'s tests drive each of those refusals from the outside and
are the evidence a reviewer is handed for the conventions; the
[observability-and-dashboard design](designs/observability-and-dashboard.md)
is where they are established, as the repository's first web-service
conventions. The same tests hold the page to them: the shell, the script, and
the stylesheet carry no inline script, no inline style, and nothing loaded from
anywhere but this origin, and the renders above are made by a driver that
refuses a render in which the script set a style or sent the token anywhere but
as a bearer to this origin.

## Following a run, a conversation, or a branch review

`yoyo status --follow` follows the normalized event stream a run, a
conversation, or a [branch review](work.md#reviewing-what-a-branch-adds-up-to)
records, which is the closest thing there is to watching an agent work. It is
the other half of the verb above: the recorded mode reads back what the records
hold now — a run still in flight as readily as one that finished — and these
modes follow a run's events as they arrive, list what has been recorded lately,
and price it. They ship with the binary, so `go install` and a release download
carry them; there is nothing to clone. Until yoyodyne-ifd.63 this was
`bin/yoyo-status`, a shell script that lived only in a checkout of this
repository, which for anybody who had never seen the internals meant the surface
did not exist. The script is retired rather than kept as a wrapper: a wrapper
would have to be installed to be useful, which is the gap the fold closes, and
kept in the checkout it would be a second copy of every sentence the verb says,
drifting from it — its banner had already drifted from the harness's own wording
once. Nothing it did is missing from the verb, and two things it could not do are
here: it needs no `jq`, and it prices a failed invocation, which cost money like
any other. There is nothing at the old path either, on purpose: the script's
flags were its own — `-L` for following the newest, `-l` to list, `-c` for
spend, `-n` for the replay — and a one-line `exec yoyo status "$@"` under the
old name would have turned every one of them into an unknown-flag refusal and a
bare `bin/yoyo-status` into the recorded report instead of the follow it always
was, which is the old name answering a different question rather than muscle
memory kept. So `bin/yoyo-status -L` is `yoyo status --follow --latest` now,
`-l` is `--list`, `-c` is `--spend`, `-n` is `--lines`, and `--runs`, `--chats`,
and `--reviews` are `--kind runs`, `--kind chats`, and `--kind reviews`.
[The retirement's own record](diagnoses/yoyodyne-ifd-239-yoyo-status-already-retired.md)
says where each of those is pinned in the binary.

```sh
yoyo status --follow             # follow the newest of any kind
yoyo status --follow --latest    # follow the newest, and move on when a later one starts
yoyo status --follow 40b68275    # follow one by id or unique id prefix
yoyo status --events             # print the newest stream's recent events and exit
yoyo status --list               # list recent runs, conversations, and reviews and exit
yoyo status --spend              # report the last 7 days of spend, by day and in total
yoyo status --spend 30           # report that many days instead of 7
yoyo status --spend 40b68275     # report spend for one run, conversation, review, or exchange, any day
```

A conversation and a branch review each record the same kind of event stream a
run does, and "is this alive" is the same question asked of all three, so every
mode covers all of them and the default never asks which kind you meant.
Selecting one by id or by a unique id prefix works the same for each. `--kind
runs`, `--kind chats`, `--kind reviews`, and `--kind sides` narrow it to one kind
when that is what you want. `--lines` says how many recorded events to replay before
following, fifty by default and `0` for the whole log; `--raw` emits each event
exactly as it was recorded, and `--all` keeps the thinking-token pings the
default leaves out. An option that belongs to the other half of the verb —
`--failed`, `--limit` outside a listing, `--lines` outside a follow — is refused
rather than ignored, because a narrowing silently dropped reads as an answer to
the question that was asked.

A side thread — a bounded conversation an agent holds beside its main one —
records an event stream of its own under `sidestreams/`, so it is listed,
followed, and priced like the other three. Its terminals are priced by the same
reader as a run's and a conversation's, and every spend row it contributes names
the conversation it was opened beside (`open, beside chat-…`), which is whose the
money was; `yoyo cost` carries the side threads into its total on a `SIDE
THREADS` row, with each one's cost listed under the table against that
conversation.

A [recurring task](configuration.md#recurring-tasks)'s passes are turns of the
role's own conversation, so their cost is already in the conversations figure.
What the spend report adds under its totals, whenever it covers conversations
and names nothing in particular, is that figure's recurring part: each task's
passes, turns, and cost by the model the passes ran on, read from the passes'
own records over the same window. It is a split rather than an addition, and it
is how a sweep moved onto a cheaper model with
[`model`](configuration.md#a-tasks-own-model) is told from the decisions beside
it on the role's own. The JSON carries it as `sweeps`.

An [exchange](conversation.md#roles-asking-each-other-things) is priced beside the
streams and is the only thing that is never followed: its record is the thread itself,
revised as it goes, rather than a stream of events, so it appears in the spend
report and in no other mode. Naming one by id prices it like anything else,
`--kind exchanges` prices them alone, and narrowing to any of the followed
kinds narrows the exchanges out along with the kinds it excludes — somebody who
asked what the runs cost is asking about the runs.

A run's listed status is the status it recorded. A conversation has no such
record of its own, so its status is derived and says what an operator is
actually asking: `answering` while an agent is working on a turn, `waiting`
between turns, and `ended` once the role has moved on to a later conversation.
Whether a turn is in flight is read from the same observed hold the four lines'
Working line reads — the process holding the conversation writes down which
process it is, and the listing checks that it is still there — rather than from
the event log, which cannot tell a turn in flight from one whose process died
before it wrote a terminal; so `yoyo status` and `yoyo status --list` cannot
disagree about the same conversation. A
branch review has no state file either — its verdicts share one log rather than
having a record each — so its status comes from its own events: `reviewing`
while the verdict is being made, and `reviewed` once it has been. A side
thread's status is read from its own record: `open` while its agent holds it,
and the outcome it ended with — `concluded` or `spent-its-budget` — once it has
ended.

Every live mode leads with a PAUSED banner while
[activity is paused](#pausing-everything-and-resuming-it), naming when the pause
was placed, and an INTAKE HELD banner while intake is held, naming who held it
and why: a quiet machine somebody paused and a quiet machine that died look
identical, and this is the one place an operator is already looking. The banners
go to standard error, so `--json` on standard output stays machine-readable and
carries both holds as fields instead. The recorded mode carries the same two
switches on its "Needs a human" line.

A listing chooses from the directory and opens only the logs it prints — the
newest twenty by default, one for `--follow --latest`'s look every few seconds —
so a state directory holding hundreds of streams is not read through to print
a screenful. It resolves the state directory the same way every other verb
does, so it keeps working under `YOYODYNE_STATE_HOME` or `XDG_STATE_HOME`, and
an empty answer
names the directory it read and the kinds it was asked about — a machine with
fifty runs and no branch reviews is told no branch reviews are recorded, never
that nothing is. `yoyo status --help` lists the rest of the options. What
`--spend` prices is every run, every conversation, every branch review, and
every exchange, and a mixed total says how much of it was each — a conversation
turn, a branch review, and a round of one role asking another something are each
a provider invocation like any other, and leaving any of them out understated
every total it belonged in. An exchange counts each round on the day it was
answered, so a thread that ran over two days lands in both of their totals; its
record keeps what the provider charged and not what it used, so its rows say
nothing in the token columns rather than saying none. An exchange record that
cannot be read is counted and named under the report rather than dropped: every
exchange beside it is still priced, and the total it is missing from is marked
`≥`. [`yoyo cost`](reporting.md#what-the-work-cost) answers the same way for the
same record — it counts it in the ask row's `unpriced` column, prices the rest,
and marks its own total — because two surfaces disagreeing about what an unknown
figure is would be a disagreement only you could settle.

The rows are grouped by the local-timezone day the money was spent on, each
day's group closing with that day's spend and today's group coming last: what an
operator budgets against is what today cost, and the day they mean is the one
their own clock is keeping. What counts on a day is each invocation rather than
the log it was recorded in, so a conversation that has been open for a fortnight
appears under today for the turn it was asked this morning and under each
earlier day it spent on — one row per day it spent, each with the shape a row
has always had. A report covers the last seven such days, today counting as the
first of them. A number asks for a different count — `--spend 30` — and naming a
run, a conversation, a review, or an exchange prices that one whatever day it
ran on, because an id has already chosen what to show; an id prefix that is all
digits has to carry its `run-`, `chat-`, `review-`, or `exchange-` prefix to be
read as an id rather than as a count of days. A window with nothing in it says so
and says since when, rather than reading like a machine that spent nothing.
`--json` carries the same rows and the same window, so a script reads the figures
rather than the columns.

Under the total, a table per role says what each role's invocations paid to
write the provider's cache and what they paid to read it, beside the tokens
each way and the role's cache-read share. The one share on the `tokens:` line
above it is decided by whichever role reads the most — a developer session
re-reading its own conversation — and hides a role that writes its whole prompt
into the cache at the write premium and reads none of it back, which is what
every review did before yoyodyne-ifd.205 and what the table exists to show. The
dollars are the report's own apportioning of each invocation's reported cost
across what it was billed for, at the provider's rate multiples (fresh 1×,
cache read 0.1×, five-minute write 1.25×, one-hour write 2×, output 5×), so a
role's parts add up to the provider's figure and no price of the harness's
enters it; a model priced off those multiples shifts a role's split and never
its total. `--json` carries the same split on every row, under `roles`.
[`docs/diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md`](diagnoses/yoyodyne-ifd-424-one-shot-cache-reads.md)
is the measurement the table was built for.

[`yoyo cost`](reporting.md#what-the-work-cost) is the same run spending grouped by the work
item the runs were for, which is what answers "what did that piece of work
cost"; it leaves conversations and branch reviews out of the *items*, deliberately
and for the same reason — a conversation that discussed five items, and a review
of a branch that carried a dozen, cannot be attributed to any one of them. What it
does not leave out of its total is what the roles spent asking each other, which
sits on a row of its own above it.

The Go tests in `internal/cli` and `internal/runstate` check these claims against
a fabricated state directory holding runs, conversations, branch reviews, and
exchanges, without a provider or a repository and without reading your real
state, so the verb is held to them by `make test` like everything else in the
repository.

## Reading what the recurring tasks found

A [recurring task](configuration.md#recurring-tasks) wakes a role on a cadence to
look at its own domain. Nobody is watching those turns, so each firing ends in a
durable report, and `yoyo sweeps` is where they are read:

```sh
yoyo sweeps                                  # the 20 most recent passes, newest first
yoyo sweeps --limit 200                      # read further back
yoyo sweeps --limit 0                        # every pass recorded
yoyo sweeps --task development-manager-sweep # one task's passes
yoyo sweeps --json                           # the whole log, machine-readable
```

**The default is twenty passes, which for an hourly task is under a day.** That
is a bound on what fits a terminal rather than on what the log holds, and it is
worth knowing which you are looking at: the question these reports exist to
answer — whether a week of fixes filed root-cause work, or quietly repaired the
same thing seven times — needs the week. `--limit` widens it, `--limit 0` reads
all of it, and a listing showing part of the pile says so and says how to see the
rest. `--json` is never bounded and always carries the whole log.

It is read-only. A sweep is written once and never revised, and nothing here
fires one, retires one, or decides anything about what a pass found.

Each pass's header names the model its turns ran on — the task's own
[`model`](configuration.md#a-tasks-own-model) where it names one, the role's
configured model where it does not, and the alternate where a failover answered
— and `--json` carries it as `model`. A pass recorded before passes named their
model, or one that took no turn, has none.

Each entry leads with **the questions the pass could not settle itself**, because
that is the one part of a report that asks for anything: a report with no
questions needs no attention, which is what makes reading these at leisure
possible. Below them come the pass's summary and what it found, each finding with
what the role did about it — `fixed`, `filed`, `consulted`, or `left` — and the
work filed for its root cause.

**A fix that filed nothing is named as one.** That is the whole of what a week of
these reports is read for: a repair that leaves its cause in place is a repair
the next pass makes again, and a listing that could not tell the two apart could
not show it either way.

**Some findings on a development manager's pass are the harness's own.** Beside
what the role reported, the harness lists the forge's open pull requests on every
firing of that role's task and states each one held open for nothing: a request
whose work item is closed, or whose head branch its target branch already
contains. Those findings are always `left`, because noticing is all the harness
does — it closes nothing — and each request is stated once, on the first pass
that finds it, rather than once an hour. The `--json` form carries them a second
time as `pull_requests` on the record, by number, which is what the next pass
reads to know what was already said. [Recurring
tasks](configuration.md#recurring-tasks) says when the reading is taken.

Three outcomes look similar in a listing and are not the same thing:

- **A pass that found nothing** shows its own summary and no findings. On a
  healthy harness that is most of them, and a run of passes that keeps finding
  things is itself a signal about the harness rather than about the sweep.
- **A pass that produced no account** says so and names what stopped it — a
  conversation nothing could open, a turn that failed, a role that answered in
  prose without the block the harness reads. It is never shown as a quiet pass.
- **A pass stopped by its turn bound** is recorded as partial, naming the bound,
  so a truncated pass is never mistaken for a finished one.
- **A pass whose reply carried more than one block** shows the last block as its
  account and says beside it that more than one was sent. It is an account, not
  a lost pass: the role slipped on the one-block contract, and the decisions it
  took are on the record rather than thrown away over the shape of the reply.

One turn may report at most twenty findings and five questions, and a whole
firing holds what its turns come to. A pass that ran past even that says so in
its own summary, naming how many entries are not listed — a shortened list that
said nothing would read as a pass that found less than it did.

The reports live beside the run state, under
`<state root>/products/<product id>/sweeps/`, with each task's cadence recorded
in its own file there. Nothing in the repository holds them: like the collected
reports, a sweep outlives the session that produced it.

The log is appended to once per firing and never rewritten, and a write of a
whole pass is not atomic, so a process killed partway through one can leave a
torn line behind. A listing names that line and carries on rather than failing:
one interrupted write must not cost every report around it, on the only surface
those reports are read from. What it will not do is drop the line quietly — a
listing short by a record it never mentioned is a worse answer than the failure
it replaced.

A log that cannot be read **to the end** is the same rule one step further. A
line too long for the reader stops the reading dead rather than being set aside,
so nothing past it is seen at all — and what was read before it is still shown,
with a line after the listing saying it stops where the reading stopped rather
than where the log does. The command still exits non-zero, and `--json` carries
the same sentence in its `error` field beside the passes it did read. The reports
before such a line are ordinary records and worth having; what a reader must not
be left with is a listing that looks complete and is not.
