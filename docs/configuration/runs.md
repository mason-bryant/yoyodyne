# Configuring checks, scheduling, and what a run may spend

The gate every run passes before it reaches review or integration, how ready
work is picked up and how much of it runs at once, and the bound on one role
asking another.

[The configuration index](../configuration.md) lists the other guides.

## Checks

Each entry runs through `/bin/sh -c` in the run's worktree, so shell syntax is
available. A check must be non-interactive and must exit non-zero on failure: a
failing check ends the run before any reviewer is asked and before anything can
be integrated. Checks are the project's own — the bundle supplies none — and the
list is replaced wholesale rather than merged.

```yaml
# Go
checks:
  - go test ./...
  - go vet ./...
  - gofmt -l . | (! grep .)

# TypeScript / Node
checks:
  - npm ci
  - npx tsc --noEmit
  - npm test -- --run
  - npx eslint .

# Python
checks:
  - python -m pytest -q
  - python -m ruff check .
  - python -m mypy .

# Java (Maven)
checks:
  - mvn --batch-mode --quiet verify

# Java (Gradle)
checks:
  - ./gradlew --no-daemon check
```

Note the shape of the Go formatting check. `gofmt -l` exits 0 even when it
lists unformatted files, so `gofmt -l .` on its own is not a gate: it reports a
problem and then passes. A check has to turn that output into a non-zero exit,
as above or in a Makefile target. This repository learned it the ordinary way,
by integrating an unformatted file through a green check run.

Prefer the non-interactive, non-daemon, pinned-install form of each tool. A
check that prompts, starts a watcher, or resolves dependencies differently
between runs makes the integration gate nondeterministic.

### What `init` proposes for `checks`

A project does not start from the empty list unless it has to. `yoyo init` reads
what the repository already announces about its own toolchain and writes the
commands that follow into `checks`, each under a comment naming the artifact it
was derived from:

| What is there | What is proposed |
| --- | --- |
| a Makefile with a `check` target, or with `test` and no `check` | `make check` / `make test` |
| `go.mod` | `go test ./...`, `go vet ./...` |
| `package.json` with a test script and exactly one lockfile | the lockfile's install, `npm`/`yarn`/`pnpm test`, and `tsc --noEmit` where there is a `tsconfig.json` |
| `pyproject.toml`, `pytest.ini`, `setup.cfg`, or `tox.ini` naming pytest | `python3 -m pytest -q` |
| `pom.xml` | `mvn --batch-mode --quiet verify` |
| a `gradlew` wrapper | `./gradlew --no-daemon check` |

**Nothing is executed.** Detection is by artifact presence and by reading those
artifacts, because running a stranger's build to discover what it is is not a
first impression worth making, and because a command that has to run to be
proposed is one that runs before anybody has reviewed it. This is a convenience
default derived from the project's own files rather than an understanding of
toolchains in the harness: what runs is still only the shell commands this list
declares, judged by their exit codes.

**Whatever is not written into `checks` is written beside it, commented out,
under a heading that says what it wants from you.** There are three, and only the
first asks for anything:

| Heading | What it means | What you owe |
| --- | --- | --- |
| `YOU MUST CHOOSE` | detection could not tell which command is the gate, and `checks` is empty | a choice: a run is refused until there is one |
| `ALSO FOUND, AND NOT DECIDED` | the same, except `checks` was written from something else and works | nothing; the question is open, not blocking |
| `ALSO FOUND, AND NOT NEEDED` | commands detection read and decided against, because what it wrote covers them | nothing |

The distinction is the point. A demand to choose is worth reading only where a
run cannot happen until somebody does; putting it over an already-runnable file
teaches an operator to scroll past it.

Taking any of them is the same gesture: delete the leading `#` and nothing else,
and open the list above with `checks:` if it is still `checks: []`. Each carries
the reason it is where it is.

**A Makefile supersedes the language-native commands**, which is the ordinary way
into the third heading. A project with a `check` target and a `go.mod` gets
`make check`, and `go test ./...` and `go vet ./...` appear under
`ALSO FOUND, AND NOT NEEDED` rather than being added, because two gates running
the same suite is the suite run twice. Nothing about that is undecided, so
nothing about it demands a decision.

**What cannot be settled is not settled**, which is the first two headings. The
cases that reach them today are:

- Python tests with no runner named anywhere. unittest discovery over
  pytest-style tests collects nothing and exits 0, which is a gate that passes
  everything, so neither runner is written.
- A `package.json` with no lockfile beside it, or with more than one, which
  leaves how the project installs unsettled.
- A `package.json` that declares no `test` script at all, or whose only one is
  npm's `exit 1` placeholder: nothing there says how the project is tested.
- A Gradle build script with no `gradlew` wrapper to pin the version a check
  would run under.

Which of the two headings they land under depends only on whether anything else
in the project produced a `checks` list to stand on.

A repository that announces none of this keeps `checks: []` and the commented
per-language examples above, which is what it always did.

### How long a check may take

Each check gets a budget, and a check that exceeds it is killed and ends the run:

```yaml
execution:
  check_timeout: 30m   # the default; per check, not for the list
```

It is the *total* time a check may run rather than the time it may stay quiet: a
suite printing a result every second is spending it just as fast as one that has
gone silent. The `30m` default is deliberately generous, because a check stopped
at this bound is not a check that judged the change — the work may have been
passing the whole way, and killing it costs a run that had nothing wrong with it.

**Concurrency multiplies what a suite takes, so this has to scale with it.**
`max_concurrent_developers: 2` does not give each run its own machine: two suites
contend for the same cores, and each one's wall clock grows accordingly — about
twofold for this repository's own suite, and further under whatever else the
machine is doing, including the provider processes the runs themselves keep busy.
The budget is spent in wall clock, so N concurrent runs need a budget set against
what the suite takes with N of them running, not against what it takes alone.
Either raise `check_timeout` to match, or lower `max_concurrent_developers` so
the suites serialize; leaving both at values chosen independently is how a
passing suite gets killed. This is the failure that produced the setting: a flat
ten minutes, a suite past forty packages with real Git integration tests, and two
concurrent runs — the tests were passing package by package when the bound
stopped them.

Every check reports what it spent against what it was allowed, whether it passed
or not. The completion event carries `elapsed` and `timeout`, and the run's notes
on the work item carry the same pair per check, so a suite growing toward its
ceiling is visible run after run rather than only in the run the ceiling finally
stops. When one does time out, the failure names both numbers and the two
settings that move them.

A budget of `0` is refused rather than read as "unbounded": nothing else bounds a
check, so one that never returns would hold a worktree, a claim, and a run open
indefinitely.

## Scheduling ready work

`yoyo run <id>` is you naming an item. `yoyo work` is the harness choosing:

```yaml
execution:
  max_concurrent_developers: 1   # the default
```

It reads the admitted work in the order you set — highest priority first — takes
the items the tracker itself reports as ready to pull, and starts as many of them
at once as this leaves free. Each run gets a worktree and a branch of its own,
and the command returns once every run it started has ended. `--limit <n>` stops
it after that many runs; without one it drains what is ready, and
[`--watch`](#watching-instead-of-draining) keeps it open instead.

Nothing about running several at once relaxes anything. Capacity is enforced at
the reservation rather than by the scheduler, so two schedulers, or a scheduler
and a `yoyo run` beside it, share one limit rather than getting one each — a run
that loses the race for the last slot is reported as declined, not as a failure.
Integration stays serial: at most one promotion into a given target branch
happens at a time, and a change whose target moved while it was being reviewed is
replayed onto where the target went and promoted by fast-forward, or blocked if
it will not replay. Nothing is ever forced.

Six things keep an item out of a pass, reported at two different grains. Three are
named against the item, because nothing else would report that this item was
passed over. An unresolved directive is named with the directive's own words: it
needs a person. An item whose unfinished children already carry its execution is
named with those children: a decomposed epic and the child doing its work are
both reported as ready, and starting both buys the same change twice — two
developers over one file, the second of them guaranteed a conflict at
integration. A child covers whether it is queued, blocked, or already claimed,
and the container is ordinary work again once its last unfinished child leaves
the backlog. And an item that would race work already in flight is sequenced
behind it rather than started beside it, named with the run it would have raced
and what the two share — the siblings of one epic, or overlapping files. That one
is a wait rather than a refusal: the conflicts are re-read at every pull from
what is actually in flight, so the item is pulled at the first pull where the run
it would have raced has ended, and the slot the hold freed is spent on the next
item down the order that races nothing. An item says which files it will change
by naming them after `conflict-surface:` on a line of its own, in its title,
description, design guidance, or acceptance criteria; an item that declares
nothing has those same fields read for the files it plainly names, and that
inference takes only a path with a separator and an extension on the end, because
a surface invented out of prose would hold unrelated work back. The tracker not
reporting an item as ready, a run for it
already being in flight anywhere, and there being no free slot are facts about
the pass rather than about any one item, so the pass reports them as such — the
stop reason names which of them ended the choosing, and a pass that got as far as
reading the queue prints how many items were admitted, how many the tracker
called ready to pull, and how many slots were taken. Those are counts rather than
a list on purpose: naming every unready item would print a line per backlog entry
on every pass and bury the deferrals worth reading. A pass that stopped before
reading the queue at all — held intake, or every slot already taken — says
nothing about the backlog rather than reporting zeroes it never looked up.

A seventh thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, and what changed
goes into the run's recorded reason instead. See
[what a change upstream leaves stale](goals.md#what-a-change-upstream-leaves-stale) for
why staleness reports rather than decides.

`max_concurrent_developers` cannot exceed the number of developer `instances` you
configured, and the default of `1` is deliberate: raising it is a decision about
your machine, and [how long a check may take](#how-long-a-check-may-take) is the
setting that has to move with it.

### Watching instead of draining

`yoyo work` returns when nothing more is ready. `yoyo work --watch` does not: it
waits out an interval and reads the queue again, until you stop it.

```yaml
execution:
  work_poll: 60s                       # the default
  blocked_runs_before_intake_hold: 3   # the default
```

Nothing else about the pass changes, and nothing needed to. Every pull already
re-reads the configuration, re-reads the intake hold, takes the queue in the
order you set, and records why it chose what it chose — so work you admit is
picked up at the next poll, a reprioritization is honored at the next pull, and
an item whose dependency landed becomes pullable because the tracker says so.
There is no change detection anywhere in it, because nothing between the readings
is cached. A run already in flight is never preempted by any of that.

An idle session costs one local tracker read per `work_poll` and asks no provider
anything, so a queue that is empty overnight spends nothing.

**The intake hold is the remote brake.** Holding intake does not stop a watching
session; it brakes it in place. The session keeps polling, chooses nothing, and
resumes where it was when you release it. `yoyo pause` — the wider switch — parks
the runs too, and lifting it resumes them from their own records.

**Three guards, because the loop no longer ends.**

**A watching session does not start the same item twice unless the item has
changed.** The case that forces this is a run that fails *before it starts* —
unreadable acceptance criteria, a provider that is not authenticated, a context
bundle that will not assemble. Nothing is claimed and nothing is recorded, so the
item is left exactly as ready as it was: a drain tries it once and returns, and a
watch with no memory would retry it every interval forever.

The rule covers every item the session has started, not only the ones that failed
that way, because the other cases that leave an item pullable with nothing
recorded — a run the intake hold or your `yoyo pause` stopped before it claimed
anything — would spin the same way. What lifts it is the item changing: what the
work says, what it is for, its priority, its status, what it depends on, and its
notes. The notes are what make the ordinary recovery work. A run that stops on a
blocker takes the item out of the ready queue and writes the blocker into its
notes, so when you release that item without editing anything else, the session
sees an item it has not tried and pulls it. Nothing the harness writes can clear
the cooldown of an item that stayed pullable, because it only ever appends to the
notes of an item it has claimed, blocked, or closed.

An item this session has already run and that nothing has touched since is
therefore left alone for the life of the session. Restarting the session, or
touching the item, is what asks for another attempt.

`blocked_runs_before_intake_hold` is the failure-storm brake, and it is a
different thing from that cooldown: it is aimed at a broken machine rather than a
broken item. That many runs blocking one after another, with nothing landing
between them, holds intake — the same hold you would place — and it stays held
until you release it, with `yoyo release` or the conversation's `/release`. Any run that lands clears the count, and `0` turns the
brake off entirely, leaving you as the only thing that holds intake.

And the session says what it is doing, because an idle session and a dead one are
otherwise the same silence. Each transition — watching, idle, braked, resumed,
stopped — is recorded once, where `yoyo status` prints it and the Slack sink
posts it. A session idling all night writes one line rather than one a minute.

**`--budget <usd>`** caps what one session spends, from the same recorded run
evidence `yoyo cost` prices items from. It is checked between pulls, never during
a run: the money a running run has spent is already spent, and what stopping it
would lose is the work it bought.

A budget the harness cannot measure is not a smaller budget, it is no budget, so
it fails closed at both ends. A pass given `--budget` with no way to price itself
is refused before anything starts. A session that has started and then meets a
run whose recorded evidence will not price — the run's event log gone, or a
record it cannot read — stops there and says which run it was, rather than
counting it as free and carrying on inside a bound it can no longer hold. The
stop is announced like every other transition, so you find out while it matters
rather than in the morning.

**The default is still the drain**, and `--until-drained` says so explicitly.
That is deliberate: watching is the shape this loop is meant to have, and turning
it on by default is a decision to make once stopped work reliably reaches
somebody, rather than a side effect of the flag existing.

What changes when you watch is what bounds the spend. A drain is bounded by the
queue emptying; a watching session is bounded by what you admit to the queue. The
backlog's order stops being a schedule and becomes the throttle.

### When a configuration change takes effect

**At the next selection.** `yoyo work` re-reads the configuration before every
pull, not once when it starts, so a capacity you raise or a priority you reorder
while it is running is picked up the next time it chooses something. That is the
same answer every other command gives — each one loads the configuration fresh —
and it is what makes reordering the backlog steer the work rather than steering
the work after a restart.

A run already in flight keeps the configuration its own pull read. Its capacity,
its check budget, and its repair budget were fixed when it was reserved, and
changing them under a running developer would mean a run judged by rules it was
never started under.

A watching session is the same answer said again: `work_poll` and
`blocked_runs_before_intake_hold` are re-read at every pull too, so an interval
you shorten or a brake you loosen takes effect at the next wait rather than at
the next restart.

### Why each run says why it was there

Every run `yoyo work` starts records, in durable state, why that item was chosen:
where it sat in the order, how much of the queue was pullable, how much of the
machine was free, and anything upstream of it that had changed since it was
admitted. `yoyo status` and a conversation's survey both read it back.

This is not bookkeeping. Work the harness chose and cannot account for looks
exactly like work happening behind your back, and holding intake — which stops
`yoyo work` choosing anything more while what is running finishes — is worth
having only if the thing that chooses actually consults it. Both halves are
enforced rather than conventional: an item you name yourself is exempt from the
hold, because naming it is you deciding it is the exception.

## How long one role may ask another

Roles can put a question to each other through the harness — the product manager
asking the architect what a goal costs before it orders the backlog, the
architect asking the product manager whether a trade-off is one a user would
accept before it settles a design. Every exchange is recorded where you can read
it with `yoyo exchange`, both halves are toolless so an ask moves opinion and
never evidence, and no authority moves through one. What is configurable is how
long a single exchange may go on:

```yaml
exchange:
  max_rounds: 10             # the hard limit on rounds in one exchange thread
```

**It is a hard limit and it is durable with the exchange.** The number is copied
onto an exchange as it opens rather than read afresh each round, so a process
dying part way through, a second process picking the thread up, and an edit to
this setting all leave a thread already in flight bounded by what it started
with. A cap a crash could reset is not a cap.

**Reaching it is not a silent cutoff.** The exchange closes as
`unresolved-after-rounds`, and it is escalated to you as a report at warning
severity naming the two roles, the question, the rounds, and what the exchange
cost — so it reaches [the pile you read](../reporting.md#what-agents-report-and-where-it-reaches-you)
rather than ending in a record nobody opens. The failure this bounds is two
judgement models deferring to each other politely for ever, which is rare,
expensive, and invisible without the number.

**Zero is refused**, unlike the
[triage caps](recovery.md#triage-thresholds). An exchange allowed no round
at all is a channel that is off, and turning the channel off is a matter of
nobody using it rather than of configuring a limit nothing can be spent against.
One is the floor.

One further bound is the harness's rather than yours: a single thing you say to a
conversation sets off at most as many rounds of asking as one exchange is
allowed, however many exchanges it spreads them over. That bounds a reply
opening thread after thread, which is a different question from how long one
thread may run.
