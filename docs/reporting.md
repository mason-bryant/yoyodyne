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
to be left out. The same holds for an
[exchange](conversation.md#roles-asking-each-other-things): its record names the
product, the repository, the two roles in it, and the conversation the asker
spoke from, and nothing that identifies a piece of work, so there is nothing to
attribute it to rather than a judgement declined. Neither is left out of
what the harness has spent altogether —
[`yoyo-status -c`](operations.md#following-a-run-a-conversation-or-a-branch-review) prices
conversations, branch reviews, and exchanges beside runs, and `yoyo cost` carries
the exchanges into its total on a row of their own, because a total that skipped
any of them would be wrong rather than merely unattributed.

`/show` breaks one item's price down by attempt, which is what a single total
invites:

```text
cost: at least $27.93 across 3 run(s)
  run-0123…  started 2026-08-10T09:14:02Z [stopped, reviewing] $8.91 from 3 invocation(s)
  run-89ab…  started 2026-08-10T11:02:41Z [succeeded, complete, integrated] $19.02 from 2 invocation(s)
  run-cdef…  started 2026-08-09T18:30:00Z [failed, developing] unknown: the run's event log is no longer recorded
```

The word in the brackets is the same fixed vocabulary
[`yoyo status`](operations.md#what-became-of-the-runs-and-what-remains-of-them) uses,
read from the same records: `stopped` ended on a blocker somebody has to decide
about and left its change intact, `failed` left nobody anything to act on, and a
run met in both places is described the same way in both.

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

### Where the money went

Every price `yoyo cost` reports is split by what the money bought, per run, per
item, and across everything the harness has run:

```text
item                                     runs  unpriced      develop       review       repair         cost  cached    waited
yoyodyne-ifd.1.5                            4         0       $29.18        $5.78       $14.47       $49.43   68.4%     3h37m
ASKS BETWEEN ROLES                          -         0            -            -            -        $4.12       -
TOTAL                                     176         1   ≥ $1764.42    ≥ $234.41    ≥ $732.75   ≥ $2735.69   61.2%    21h01m
```

**ASKS BETWEEN ROLES** is what the roles spent asking each other, summed over
every recorded exchange. It is a row rather than a note under the table because
the total has to add up: an ask is a provider invocation the harness made and it
belongs in what the harness spent, and a figure visible only in the total would
be a difference the reader has to take on trust. It is one row for the product
rather than a figure per item because the record names no item — and because the
channel runs between the roles that own documents and queues, with the developer
and the reviewer, the two that work inside a run, off it. If an exchange ever
records the work it was taken for, it belongs in that item's price beside the
runs instead. The columns that split a run's price are left empty rather than
filled with zeros — an ask is not development, review, or repair — with one
exception: **unpriced** means on this row exactly what it means on an item's. An
exchange record that cannot be read is counted there, left out of the figure
beside it rather than counted as nothing, and every exchange that could be read
is still priced; the total carries the same `≥` a run's unpriced attempt puts on
it. That marker goes on the total column alone, since money nobody could read is
money that belongs to no phase.

Exchanges that cannot even be listed are the one case with no floor to state:
how much is missing is unknown, and so is how many records it is missing from,
so the row reads `unknown` rather than a figure.

**develop** is each run's first developer attempt, **review** is every reviewer
invocation it made, and **repair** is every developer attempt after the first —
the failing check, the refused path, and the reviewer's findings handed back are
all repair, because from the money's point of view each is the same thing: the
change being made again because it was not right the first time. Every priced
run invocation that says which part of the run it served lands in exactly one of
the three, so nothing is missing from them and the split is a decomposition of
the price rather than a second opinion about it. An invocation the provider
refused or killed and the harness reissued is
charged to the attempt it was reissuing, not counted as a repair nobody asked
for: what an attempt cost is what it took to get it made.

An invocation that ends in a run's log without saying which part of the run it
served is the one thing none of the three will take. It is counted in the total
and named in a line under the table instead — how many, and what they cost —
because charging it to a phase would put somebody else's money in a column the
operator reads to decide something. That line is absent from a healthy record:
anything in it is a defect in whatever wrote the log rather than a cost of the
work. What "without saying" means exactly, and why the runs recorded before
anything could say are unaffected, is below.

**waited** is time rather than money — a provider that would not serve the
account, and the harness parked on the operator's hold — and it is counted apart
for that reason, since adding it to the money would make a run that waited
overnight read as expensive when what it was is slow. It comes from the run's
own record rather than from its event log, which is why a run nothing can price
still says how long it was held up.

**cached** is the one column that is not money: the cache-read share of input
tokens, `cache_read / (input + cache_read + cache_creation)`, read off the same
terminals the price is. It is here because the money on its own cannot answer
the question a token-efficiency change asks of itself — a run that got cheaper
because the provider changed its prices and a run that got cheaper because more
of its prompt was already cached are the same figure in dollars and opposite
facts about the harness. It is the measure a change made to share a longer
prompt prefix is kept or reverted on, and the first instrument any later
input-token lever has.

The denominator is every input token however the provider billed it, not the
fresh input alone: a prompt served entirely from the cache is reported with
almost no fresh input, and dividing by that would make the emptiest prompt read
as the best cached one. An invocation whose terminal carried no usage object at
all is counted apart and named under the table rather than added in as nought —
a run nobody measured and a run measured at nothing are the same figure and
opposite facts, and folding the first in would read as a caching change that
achieved nothing. The same distinction runs the other way: an invocation the
provider really did report as reading nothing keeps its `0.0%`, because that is a
reading, and a window where nothing reported usage says so instead of showing
one. What separates them is the count of invocations behind the share, which is
why every line carries it. The column carries no `≥`: it is a share rather than
a sum, so an unpriced run does not leave it short, and the ask row leaves it
empty because an exchange record carries what its rounds cost and no token counts
at all.

The share is over provider invocations rather than over runs, so a window is
whatever set of runs you take it across — which is what makes it answer a
before-and-after question about a change to what the harness sends. Whether it
can answer one is a separate matter, and the ifd.84 experiment is the worked
example of it not being able to: most of the cache-read in any window is a
developer session re-reading its own conversation, so a lever worth a few
thousand tokens of shared prefix does not move an aggregate built from tens of
millions. See
[`docs/experiments/yoyodyne-ifd-84-prompt-prefix-stability.md`](experiments/yoyodyne-ifd-84-prompt-prefix-stability.md)
for the numbers and what they did and did not establish.

The line under it is that experiment's lesson made into an instrument. It says
the same share once per phase:

```text
cache-read share by phase: development 98.4% over 296 invocation(s), review 0.5% over 534 invocation(s), repair 95.8% over 252 invocation(s)
```

The phases neither assemble their prompts alike nor cache alike. A developer
session resumes and re-reads its own conversation on every turn; a review is one
short invocation with no session to resume, whose only cacheable part is the
prefix it shares with every other review. Summed, the larger decides the column,
and a review reading nothing at all leaves that column at ninety-seven per cent —
which is exactly what it did, unnoticed, for as long as the harness has kept
usage. So a change to what one phase sends is read here, and the column above is
what the harness costs altogether. Each phase carries the count of invocations
behind its share for the reason every other line does, and a phase nothing
measured is a dash rather than a nought; a window where nothing measured anything
has no line at all, because the one above it already says so.
[`docs/experiments/yoyodyne-ifd-205-review-prompt-cache.md`](experiments/yoyodyne-ifd-205-review-prompt-cache.md)
is the finding it was built for.

Naming an item says the same thing per run, under each attempt:

```text
yoyodyne-ifd.1.5: $49.43 across 4 run(s)
  development $29.18 from 4 invocation(s), review $5.78 from 4, repair $14.47 from 3; waited 3h37m for the provider
  cache-read share 68.4% of 41905311 input token(s) over 11 invocation(s): 28663234 cached, 12984077 fresh, 258000 written to the cache; 194422 output
  cache-read share by phase: development 71.2% over 4 invocation(s), review 0.0% over 4 invocation(s), repair 69.8% over 3 invocation(s)
  run-c25525d6…  started 2026-08-18T14:31:34Z [cancelled, developing] $26.93 from 3 invocation(s)
    development $22.84 from 1 invocation(s), review $0.96 from 1, repair $3.13 from 1; waited 3h37m for the provider
    cache-read share 71.0% of 19218662 input token(s) over 3 invocation(s): 13645250 cached, 5461412 fresh, 112000 written to the cache; 88104 output
    cache-read share by phase: development 74.1% over 1 invocation(s), review 0.0% over 1 invocation(s), repair 66.3% over 1 invocation(s)
```

The split is read out of the run's event log, which is what makes it answer for
runs that finished long before it existed. Each invocation's terminal names the
role it was made as, so the reviewer's invocations are the reviewer's because
they say so, the developer's are the developer's for the same reason, and they
group into attempts by how each one ended. An invocation naming any other role,
or naming none at all, is left unattributed rather than placed.

Runs recorded before event schema version 2 had no role on their terminals to
omit, and are read the way they were written: a review announces itself with a
`review.started` and then makes exactly one invocation, so the terminal after
that announcement is the reviewer's and every other terminal is a developer's.
The schema version on each event is what confines that reading to those runs, so
a terminal written today with no role is never read positionally — it is an
invocation that could have said whose it was and did not, which is unattributed
money rather than a phase.

That older inference was sound for the runs it covers, and this is the evidence
it rests on. Only two things have ever written a terminal into a run's log: the
developer's attempts, and the reviewer, which always announces itself with a
`review.started` first. Every other provider invocation the harness makes writes
somewhere else — a conversation turn to its conversation's log, an inter-role ask
to the exchange record, and a branch review, **including every shadow review of
the ifd.92 experiment**, to its own log under `branch-reviews/` rather than under
`runs/`. So the closed shadow experiment polluted no run's phase data, and there
are no affected runs to name: not because its invocations announced themselves,
but because none of them was ever written into a run's log at all. The sweep in
`internal/execution/terminal_test.go` is what keeps that set of writers closed.

### Every provider spend, one line

Beside all of that there is an append-only cost log, one line per provider
invocation, written the moment the provider says what that invocation cost. It
lives at `<state root>/products/<product id>/spend.jsonl`, and every process the
harness runs appends to it: the developer's first attempt and every repair after
it, every review including a branch review, every management-conversation turn,
and every round of an inter-role exchange.

Each line carries the role and the configured agent that spent it, the phase, the
amount and its classification, the account alias and the configuration revision
in force, the revision the harness binary that made the call was built from, the
one thing the invocation belonged to — and the work item, where the invocation was
made for one — the backend, the requested and resolved models, and when it
happened.

That last pin is the build, and it is taken by the metering itself rather than
supplied by whatever is invoking: a build a call site could pass in is one a call
site could forget, and the line would then say whose account paid for an
invocation without saying what code made it. It is the only pin that can be
absent on a line the harness wrote — the harness always knows the account and the
revision, and a binary installed from the module cache carries no revision of its
own. An absence is recorded as one rather than guessed at from the version,
because a comparison nobody can make is an answer and a comparison made against
the wrong commit is not.

That one thing is a run, a conversation, an exchange, or a branch review, and a
line names exactly one of the four. A branch review has a field of its own rather
than borrowing the run's: it is not a run and nothing ever made one for it, so a
line that carried its identifier as a run id would hand anything joining these
lines back to run records an id naming no run.

Three things it does deliberately. An invocation the provider ended without
pricing is classified `unknown` rather than recorded as zero or left out, because
a zero meaning "nobody was told" understates every total it enters by however
much was really spent. Nothing here aggregates: adding the lines up is yours, and
any later query builds on the same lines rather than on a rollup something
decided for you in advance — which is also what makes the log evidence about what
each model charges rather than only about what they charge together. And a line
that cannot be made durable fails the invocation it belonged to, the same weight
an unrecordable event log carries, with one exception: a conversation turn comes
back anyway and says on the reply what could not be recorded. The provider has
already written that answer and already charged for it, and losing the answer to
report that the bookkeeping missed would cost you both.

None of the prices above change with it. `yoyo cost` still reads a run's event
log, which is what lets it answer for runs that finished long before this log
existed; this is the record that does not have to be reassembled from one.

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

The severity is the one recorded signal of importance, so every surface that
shows you a report renders it rather than merely printing the word. In a listing
it is a mark in the column before the identifier — `!!` for critical, `!` for
warning, nothing for a note — and a colour where the terminal permits one, red
and bold for critical and orange for warning. The mark is the part that always
holds: piped to a file, read under `NO_COLOR`, or shown on a terminal that says
it is `dumb`, a critical report still does not read like a note. `--json` carries
none of it, because there the severity is a field.

A run's closing lines carry the same signal without listing the pile. Instead of
"reported 3 thing(s)" they say how many of what and mark themselves by the worst
of them — `!! reported 3 thing(s) without stopping the run (critical 1, note 2)`
— which is the difference between a line an operator can skip and one they
cannot. What became of a report is printed under it undressed, whatever it was
filed at: a plain line beneath a loud one is what says somebody has already dealt
with that one.

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

One entry in the pile is filed by the harness rather than by an agent: an
[inter-role ask exchange](conversation.md#roles-asking-each-other-things) that reached the
round limit it was opened with closes as unresolved and escalates itself here, at
`warning` severity, naming the two roles, the question, the rounds it spent, and
what it cost. It is a report rather than a blocker for the same reason everything
else here is — nothing was stopped, and two roles simply did not settle something
one of them needed — and it is filed at all because an exchange that ended in a
silent limit is exactly the failure nobody would otherwise see.


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
exported into a shell are inherited by everything started from it bar the agent
invocations, which are given an environment with the pair removed, and on a
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

On macOS, `yoyo slack ensure` is that launcher done by the harness: it starts a
sink only if nothing is reporting for this product, reading this product's own
keychain items into that one process. It is safe to run on a schedule — an
unattended pass every few minutes meets a running sink and does nothing — and it
is safe on a machine running several harnesses, because whether a sink is
running is asked of this product's lease rather than of the process table, where
one project's `yoyo slack` answers for every project's. Putting it on a schedule
is yours until the productized maintenance job (`yoyodyne-ifd.207`) lands and
calls it: the harness ships the step and, for now, no timer.
[`docs/slack/setup.md`](slack/setup.md#6-start-the-sink) has the rest of it.

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

Under that sentence it carries [the four lines](operations.md#where-the-harness-stands-the-four-lines)
— Running, Working, Not startable, Needs a human — from the same derivation
`yoyo status` prints them from, so the channel and the terminal answer one
question one way. Before this the message said that choosing had stopped and
nothing whatever about what the machine was doing instead, which is exactly what
somebody woken by it at three in the morning then had to reconstruct. A sink
assembled without a way to read them says so in the message rather than leaving
them out: a message that simply lacked the lines is indistinguishable from a
harness with nothing in any of them.

Beside it is the **session's own age**, which is the same shape of message about
the opposite situation. A `yoyo work --watch` session runs whatever binary it was
started with, so a fix that lands after it started is not in it until that build
is installed over it — and nothing else says so, because the session goes on
choosing work and the runs it starts go on looking ordinary. What it actually
produces is rounds spent against defects that were fixed on the main line hours
earlier, which reads as agents failing rather than as a process running an old
build. So the session records the revision it was built from, and while the
repository has moved past it the channel says so every `--heartbeat`: how many
changes have landed since, which build it is on, and that installing the build is
all that is left — [the session takes it up itself](work.md#letting-the-harness-choose-the-work),
between the runs it is carrying and without interrupting one. Unlike the waiting
line it is said the first time it is seen rather than armed silently — nothing
else said it as it happened, and being told after the first round has been spent
is being told too late. It is silent on a session running what is deployed, and on one whose
binary recorded no revision at all, which is a comparison nobody can make rather
than a session that is current.

Two records answer which build that is, and they are asked in that order. The
watch log is the direct one: that process is the resident, and it stamps what it
was started with on every transition it writes. Where no live session names one,
the **runs still in flight** do — a run's record pins the harness that reserved
it, so a resident whose own log predates the stamping is still visible through the
work it is dispatching, and so is a dispatcher that is not a watch session at all.
Neither is inferred from anything else: both are stamps the process wrote about
itself.

A live session's stamp settles it outright, and the runs are consulted only where
no live session carries one — including where a run in flight was reserved by a
different binary and started later. That is a precedence rather than a contest of
which record is newer, because a live watch session is the resident by definition
while a run reserved by some other binary is usually an operator's `yoyo run` or a
triage carry-out: a process that has already ended or is about to, whose build is
not the one that will go on choosing work. Within each source the most recent is
taken — the newest live session that recorded a build, and the latest-started run
still in flight. A run that has ended says which build made it and is no longer
evidence about what is running now.

**It is a self-hosting line, and it says so rather than assuming it.** The
revision is the one the `yoyo` binary was built from, and the repository it is
counted against is the product's — the same one the tracker and the worktrees use.
Those are one history only where the product under management is the harness's own
source, which is how Yoyodyne develops itself and is not true of any other
product. Nothing asserts it: the sink asks the repository whether it holds that
revision before it counts anything, and where it does not there is no number to
say, so the channel hears nothing and the sink's log says why once per build. A
count taken from an unrelated history would be a number an operator would act on,
which is worse than the silence. Measuring a resident against a harness repository
that is not the product's would need that repository named in configuration, and
nothing names it today.

Far enough behind — twenty changes — it stops being a line worth reading and
becomes a degraded harness, said as a warning and sent as a direct message to each
person the project granted direct-work, once per build. That needs the `im:write`
scope the checked-in manifest asks for; a workspace that refuses it costs the
direct message and not the channel's copy. What is left for it to say is
narrower than it was: a watching session now restarts itself into a build
installed over it, so the ordinary case clears itself within one run and the line
is a transient rather than a standing chore. It still has the cases the session
cannot answer for — a build nobody has installed yet, a session started before it
could do this, and a dispatcher that is not a watch session at all — and it stays
until those have an owner too.

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
it reads across rather than a gap in what it says. It is also the reason no agent
holds a Slack token — one separate process posts, and an agent invocation is
given an environment with the pair removed, so no agent's subprocess tree ever
has a credential for your workspace in it.

Replies go the other way. A reply in a work item's thread, from somebody this
project granted `direct-work` with a bound Slack member id, is recorded as a
[directive](conversation.md#directives-and-the-work-they-pause) against that item
— the same record `yoyo directive record` writes, with the same pause semantics
and the same resolution, so a run meets it whichever way it arrived. Every reply
is answered in its own thread, tagging whoever wrote it, with what was recorded
or why nothing was; the reply itself is marked with where its directive stands,
recorded and open or settled; and when the record later says the directive was
settled, that is said in the same thread, tagged the same way, and the mark on
the reply moves with it. A project that has granted nobody is steered by nobody.
What a reply may say is in
[`docs/slack/setup.md`](slack/setup.md#steering-the-work-from-a-thread).

Outside those threads the sink is silent, with one exception: **a message that
@-mentions the app is always answered**, wherever it can see one — at the top of
the channel or in a thread it never opened. A question about where things stand
is answered with the same four lines `yoyo status` prints, read from the same
place rather than assembled a second way; anything else gets one sentence saying
that is the only question it answers here yet and where the work is driven from
instead. No directive is recorded from one and nothing about the work changes,
because a message at the top of the channel names no item to scope a directive
to — but every message addressed to the app goes into the sink's own log, with
what was asked in it and before the answer is posted, so being heard does not
depend on the workspace carrying the answer. It exists because the alternative
was silence, and a reporting process that answers nothing is indistinguishable
from one that has died.
