# What comes back to you

*For an operator asking what the work cost and what came back. Part of
[yoyo's documentation](../README.md#further-reading).*

## What the work cost

Ask what is done and the completed items come back with a price tag:

```text
completed (3):
  [yoyodyne-ifd.2.7] p1  $27.93 Resume an interrupted run
  [yoyodyne-ifd.12]  p2 ≥ $4.50 Pause on a provider usage limit
  [yoyodyne-ifd.13]  p2         Publish a pull request
  ≥ marks a floor: some runs of that item have no surviving record and could not be priced.
  1 completed item(s) carry no price: work the harness did not run has none, and work it did is priced by `yoyo cost --record`.
```

The price is of the item rather than of a run, which is the only figure that
answers what a piece of work cost: every run made for it counts, including the
attempt that was rejected, the repair attempts, and the reviewer's invocation
beside the developer's. One item in this repository cost roughly twenty-eight
dollars across a rejected attempt and a successful one; a per-run view shows two
numbers and this shows the truth.

Every figure is the provider's own report of what an invocation cost, read from
the run's event log, never an estimate from a price table that drifts the moment
a provider changes what it charges.

A run finishing writes the item's total onto the item in the tracker, and that
recorded total is what travels with the work: `/status`, the product manager's
briefing, and `bd` itself all read the one number the tracker holds, rather than
each assembling a price of their own. `/show` is the exception, deliberately: it
prices the item from the run records themselves every time it is asked. That is
what lets it answer for an item nothing has recorded a price for yet — anything
finished before this existed, or an item whose run could not write its price
down — and it is why the two can differ. Where they do, `/show` is the current
one and the tracker is what was last recorded; `yoyo cost --record` makes them
agree.

Three things it deliberately will not do. A run whose event log no longer
survives is priced as unknown rather than as nothing — it is counted, left out
of the total, and marked with `≥`, because a zero meaning "no record" would
quietly understate every total it entered. An item the harness has never run
carries no price at all rather than a price of nothing. And what is priced *per
item* is runs: the conversations that steer them cost money too and are recorded
just as durably, but attributing a conversation that discussed five items to any
one of them is a judgement rather than a join, so it is left out here and said
to be left out. It is not left out of what the harness has spent altogether —
[`yoyo-status -c`](operations.md#following-a-run-a-conversation-or-a-branch-review) prices
conversations and branch reviews beside runs, because a total that skipped
either would be wrong rather than merely unattributed.

`/show` breaks one item's price down by attempt, which is what a single total
invites:

```text
cost: at least $27.93 across 3 run(s)
  run-0123…  started 2026-08-10T09:14:02Z [failed, reviewing] $8.91 from 3 invocation(s)
  run-89ab…  started 2026-08-10T11:02:41Z [succeeded, complete, integrated] $19.02 from 2 invocation(s)
  run-cdef…  started 2026-08-09T18:30:00Z [failed, developing] unknown: the run's event log is no longer recorded
```

From the command line, `yoyo cost` prices items from the same recorded runs —
one line per item, or a run-by-run breakdown when you name one — and
`yoyo cost --record` writes those prices onto the items. That is also the
backfill: the run state and event logs of everything already finished are still
under the state directory, so items closed before any of this existed can be
priced retroactively rather than the ledger starting today.

```sh
./bin/yoyo cost                     # every item the harness has run, and the total
./bin/yoyo cost yoyodyne-ifd.2.7    # one item, broken down by run
./bin/yoyo cost --record            # write each price onto its work item
```

`/diff` says what a run changed. It reads the run's own durable record rather
than shelling out to git, and that is what makes it survive success: a run is
cleaned up once it integrates, its worktree removed and its branch deleted, so
anything that answered by diffing a tree would stop having an answer exactly
when the work landed. The record keeps the file listing and the diff stat the
harness took while the worktree existed, along with the branch, the promotion,
and the pull request the work was published through — all still there to point
at after the tree is gone. Naming nothing asks about the run this conversation
last started — still going, already collected, or started by an earlier process
this conversation was resumed from, since the item it ran is written into the
conversation's own record rather than kept in whichever process happened to
start it. Naming an item asks about the most recent run of that item, whoever
started it. A run whose record holds no summary says so rather than printing an
empty listing that reads like one.

`/redirect` records your direction in the item's notes, where the developer's
context reads it on the next attempt, and stops the run first when the item you
are redirecting is the one running. It never changes the item's status: saying
what to do differently is not deciding that the work is done or blocked. Start
it again with `/work` when you want it retried.

## What agents report, and where it reaches you

An agent used to be able to reach you only by failing. A spent repair budget
becomes a durable blocker, a failed run is reported where you are already
looking — and everything an agent noticed while its work *succeeded* survived
only as prose in a run summary copied into an item's notes, where nothing
surfaces it. Two real examples from a single session reached the operator only
because a person happened to be reading: a reviewer's observation that the
built-in bundle's declared version had gone inert, and a developer's report that
`bd lint` could not run in its sandbox.

Every role can say such a thing without stopping: the developer, the
reviewer, and the product manager each end what they say with one small block,
and the harness collects it. `/reports` shows you the pile, newest last, with
the twenty most recent listed and the rest counted.

`yoyo reports` shows the same pile without opening a conversation, which is what
a run finishing overnight needs: it says it reported something, and reading it
must not cost an interactive conversation with a provider behind it. It prints
the whole pile rather than the most recent twenty, because a command's output
can be paged and piped where a listing beside a conversation cannot, and
`--json` hands a script the collected records themselves. Both listings are
read-only, and both say which reports somebody has already decided about: a
report is written once and never revised, and neither of them retires one,
handles one, or decides anything at all.

```sh
./bin/yoyo reports                 # the whole pile, oldest first
./bin/yoyo reports --json          # the collected records, for triage or a script
```

A report is deliberately not a blocker, and nothing about it behaves like one.
The run carries on exactly as it would have: an approving verdict that mentions
something still approves, a developer that reports something still finishes, and
a report the harness cannot read or cannot store costs its run nothing at all —
it is named on the outcome instead, because a report nobody kept would otherwise
be silence. That is the property worth relying on, since a channel that could
cost an agent its run is one agents learn not to use.

Each collected report carries the role and the configured agent that made it,
the run or conversation it came from, the work item where there is one, a
severity — `critical`, `warning`, or `note` — and the text. That is enough
structure to filter the pile later without deciding now how it should be
filtered; an agent that judges which of its own observations are worth your
attention is a later question, and nothing here does it. The severities are
deliberately not the reviewer's `blocker`/`major`/`minor`: a finding decides
whether a change is repaired, and a report decides nothing.

Volume is the risk this design has, and the answer to it is in the role
contracts rather than in a filter. Every contract says what merits a report — a
risk worked around, an assumption that may not hold, a defect or a stale
document outside the assigned work, something in the environment that stopped a
check being run — and says plainly that most replies should carry none, because
a channel full of routine observations is worse than nothing: it looks like
coverage. That guidance is in Go, alongside the rest of each contract, so no
persona can loosen it.

The pile lives outside the repository under the operating system's state
directory, beside the run and conversation records rather than among them. It
outlives them: a run is settled and its worktree and branch are removed, and
what it reported is still there for you to read.

### Who reads them, and what became of each one

A report that only you can read is a report that reaches triage when you happen
to be reading. That was the whole of it until recently — you read the channel,
noticed something, and repeated it to the product manager yourself — which routes
an agent's escalation through you rather than through the role the goals put in
front of it. The product manager could not have read the pile if it wanted to:
its evidence is the specifications, Beads state, and the documentation of what
ships, and the pile is none of those.

So the reports nobody has decided about are carried into its conversation, the
way changes proposed to its documents already are. They arrive worst first —
`critical`, then `warning`, then `note`, and the most recent first inside each —
each naming itself, the role and agent that filed it, the work item where there
was one, and the run or conversation it came out of. That is enough to act on
without going and fetching anything. They are bounded: at most ten in one turn,
with the rest counted and offered later, because a pile nobody has worked through
must not become the whole of a turn.

Deciding what becomes of one is a product decision and it is the product
manager's: work to admit, a proposal to put to you, a concern to raise, or
nothing at all — a report that asks for nothing is handled by saying so. It
records the decision with a `handle` action, the same bounded, recorded mechanism
it acts on the queue through, and that record is the only thing that takes a
report out of the pile. A report it read and did not handle comes back to the
next conversation. No other role has the action.

What is recorded is a second file beside the pile rather than a change to it. The
report stays exactly as its author wrote it — that is what makes the pile evidence
rather than a worklist somebody has been editing — and the handling beside it
carries who decided, when, in which conversation, and why. Deciding twice is two
records and the later one is what is read. There is no vocabulary of outcomes:
"admitted as ifd.150", "already fixed", "not worth doing" are the same fact to
everything that reads this, and the reason says which.

That is what `/reports` and `yoyo reports` are showing you when they count the
unhandled ones and print what was decided under the rest. It is also the honest
limit of it: the harness carries reports to the role that decides, and nothing
here judges whether it decided well.

## What agents propose changing, and who decides

The canonical documents each belong to one role, and that boundary is enforced
rather than asked for. A role that meets it and has nothing else to say has two
moves left, both bad: build against intent it believes is wrong, or edit the
document anyway. So it has a third — it proposes the change, in one small block
like the report block, and the harness carries it to the role that owns the
document and to you. The developer carries that block today, being the role that
meets the boundary while implementing against a document; the reviewer says what
is wrong with a change as a finding instead, and the product manager stops and
asks you.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

Who is being asked follows from the document rather than from anything the agent
says: the harness resolves the artifact it names to its kind, and the kind to its
owner. A proposal about a document nobody records is refused, because there is
nobody to decide it, and a proposal from the role that owns the document is
refused too — that role amends it.

**A proposal is never a deferred edit.** It carries what should become true and
why, not replacement prose, and nothing in one ever reaches the document —
approved or not. Approving records that the owner's authority came down in
favour of the change; the change is then made by the owner, in the document, in
a revision recorded under that role. That is what keeps this from becoming the
slow path by which a downstream role redefines upstream intent: the only thing a
proposal can produce on its own is a decision.

Like a report, it costs the run nothing. The run integrates exactly as it would
have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with — that naming reaches
you and not the agent, so a role that misnames a document is not told and
repeats the mistake. It is durable in the same place and for the same reason:
the run that argued the design was wrong is long finished before anybody decides
what to do about it. A developer that makes the same argument again on a repair
attempt raises one proposal rather than one per attempt.

The owner hears it where it works, and you are the one who decides. Proposals
against the brief and the goals are carried into the product manager's
conversation and proposals against the designs, the specifications, and the
decision records into the architect's, and each argues for or against them and
can decide or edit nothing. So every decision is recorded by you through
`yoyo amendment` — the same override path `yoyo invariant` takes — and the record
says you exercised the owner's authority rather than that the owner answered.

## Reporting into Slack

Everything above needs you at the terminal, which is the wrong requirement for
work that runs while you are not. `yoyo slack` is the same account of the work in
a Slack workspace: one thread per work item, one message per milestone, and every
report an agent filed at the severity it was filed under. The backlog moving is a
milestone too — work admitted with the goal it serves, decomposed, attributed, or
reordered — so the queue changing is as visible as the runs it feeds. Each role
speaks under its own name and in its own voice, and what no persona did — a
promotion, a merge, your own holds — arrives from the harness itself. It is a process you
start and leave running, and it needs your project to have opted in:

```yaml
# .yoyodyne/config.yaml
slack:
  enabled: true
  channel: C0123456789
```

```sh
export SLACK_BOT_TOKEN=xoxb-...   # this process's environment, and nowhere else
export SLACK_APP_TOKEN=xapp-...
./bin/yoyo slack                  # or --once to make a single pass and exit
```

That is the shape of it, and it is not the shape to leave running. Tokens
exported into a shell are inherited by everything started from it, and on a
machine running more than one harness the sink you start second reads whichever
pair that shell happened to have — it connects, authenticates, and posts this
project's work into another project's channel. So the supported arrangement is a
launcher that reads **this project's own** secrets, stored under names that carry
the product, into exactly one process:

```sh
SLACK_BOT_TOKEN="$(security find-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w)" \
SLACK_APP_TOKEN="$(security find-generic-password -s yoyo-slack-app.<product id> -a yoyo -w)" \
YOYO_SLACK_SECRET_NAMESPACE=<product id> \
exec yoyo slack
```

`YOYO_SLACK_SECRET_NAMESPACE` is not read as a credential and is not one: it is
how the sink records whose secrets it was launched with, so
[`yoyo doctor`](operations.md#checking-the-installation) can tell a sink that is merely
running from one that is running for this project. Leave it out and the sink
still works; what is lost is anything being able to notice when it is wrong.

**The top of the channel reads as a status board.** Each thread's opening message
carries one reaction saying what that item is doing now — working, with the
reviewer, blocked, or landed — replaced as the record moves and taken off when it
stops being true. So which threads need you is answerable by scanning the channel
rather than by opening them. Those four are the whole vocabulary: a status is
about the item where a severity is about one message, and the two never share a
symbol. It needs the `reactions:write` scope the checked-in manifest asks for, and
a workspace that refuses it costs the board and not one message.

One message there is a state rather than an event, and it is the one an overnight
asked for. A line that is **choosing nothing while work is ready** — intake held,
everything held, the watch session idle, or no session running — says so again
every `--heartbeat`, an hour by default, naming what stopped it, how long that has
been true, and how much the tracker calls ready behind it. Everything else is a
transition and is said once, which is right for a thread and wrong for a night:
"intake is held" posted at 00:02 is ten hours stale by the time anybody reads it,
and the silence after it is indistinguishable from a healthy queue or a dead sink.
It stops the moment the state clears, says nothing while a run is in flight, and
stays completely silent on an idle line with nothing ready — silence has to keep
meaning nothing to do, which is what makes the times it does not worth reading.

Every message ends by saying whose move follows it. A thread is a narrative and a
narrative goes quiet — a run takes an hour, an item sits in the queue overnight,
work routed to a role waits on somebody opening a conversation — and the silence
after the last message reads the same whether somebody is working, somebody is
waiting to be asked, or nobody at all holds the ball. So each message closes on
one clause: `Next: the reviewer's — a verdict on the change.` It is the same
clause whoever is speaking, because whose move follows a promotion is a fact about
the state of the work rather than an opinion a persona has about it, and it is on
every message rather than only the ones that look final — which message turns out
to be a thread's last is not knowable when it is written.

The work that most needed it is the work no run ever touches. An item
[marked for a conversation](work.md#letting-the-harness-choose-the-work) is never
selected, so no run reports anything about it, and its thread used to show the run
that could not carry it and then nothing for the rest of the item's life. Three
messages carry that journey now: the item being handed to a role's conversation,
that role's first act on it, and the close that finishes it. The handoff names
which role's conversation, taken from the marker on the item, so the wait between
being handed over and being taken up belongs to somebody by name rather than to
the anonymous role that carries it. Work marked before the marker named a role
still says only that a conversation carries it, because that is all its record
holds and a thread that guessed would name the wrong person. The close is reported
here and nowhere else, because everywhere else the run that landed the work
already says it, and this is the work with no run to say anything.

[`docs/slack/setup.md`](slack/setup.md) takes you from an empty workspace to
live reporting, and the app it asks you to create is the checked-in manifest
beside it rather than a list of checkboxes to work through by hand.

It is an observation and never a gate. Nothing waits on it: a workspace that is
down delays messages rather than losing them, because the sink reads the same
durable records the verbs above read and catches up from its own cursors when it
returns. The moment its history starts from is written down the first time you
ever run it and never taken again, so time the sink itself spent stopped is a gap
it reads across rather than a gap in what it says. It is also the reason no run
holds a Slack token — one separate process posts, so no agent's subprocess tree
ever has a credential for your workspace in it. Replies are acknowledged and
nothing acts on them yet; steering the harness from a thread is designed and not
built.
