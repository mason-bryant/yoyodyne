# The conversation

*For an operator driving work from `yoyo chat`. Part of
[yoyo's documentation](../README.md#further-reading).*

The product manager reads the product specifications — every Markdown file under
`product.specifications`, which defaults to `docs/product` — plus the open Beads
items, and discusses product intent with you. It owns the queue that serves that
intent, and it manages it directly rather than dictating changes for you to
type.

```sh
yoyo chat
yoyo chat --message "What is missing from the brief?" --json
```

That queue is a backlog with an order, and the order is the product manager's.
What is admitted to it and what comes before what are product decisions — the
same intent it already owns, expressed as what to do next — while decomposition,
dependencies, and assignment stay a development manager's, which takes work from
the top of that order rather than choosing for itself. A role that disagrees
with the ordering proposes a change to it exactly as it would propose a change
to a goal; none of them reorders it or admits work to it. The order is written
down as Beads priority — 0 first, 4 last — so there is one place it lives rather
than a second copy that could disagree with the tracker, and items left at the
same priority are in no order anybody decided: the product manager says which
comes first by giving it a higher one. Admitting work says where it goes as part
of admitting it, because a new item has no identifier until the tracker answers
and an ordering left for a later step is an item sitting at the tracker's
default in the meantime — including for an item you approved from a proposal,
which is admitted at that default until the product manager places it.
`/backlog` shows it to you in order, with what is holding each unready item back
and what would be pulled next.

The harness also pulls from that order on its own, and the difference is worth
stating plainly: `/work` is you naming an item, while `yoyo work` reads the same
order, takes what the tracker calls ready, and starts as much of it at once as
your configured capacity leaves free. `/hold` is what stops it choosing while
what is already running finishes, and every run it chooses records why that item
was the one — so work that started without you asking still says what it was
doing there. [Letting the harness choose the
work](work.md#letting-the-harness-choose-the-work) is the whole of it.

Work leaves the backlog in one of two ways, and both are recorded on the item.
`close` says the work is done. `retire` says it will not be done, and is the
only way admitted work leaves the queue without being done: there is no delete
and no third way, so scope you asked for cannot quietly disappear from the
queue — it is withdrawn in the open, with a reason, and the item afterwards says
it was retired rather than finished.

A specification opens with an introduction saying what the thing is and why it
exists, and states the goals that serve it after that introduction. That shape
is the contract, and the harness checks it: one that has no goals, no
introduction before them, or an empty goals section is named on stderr when the
conversation opens and listed for the product manager alongside the
specifications themselves — and still read, because refusing to load it would
silently lose intent somebody wrote down. A directory with nothing in it is
reported the same way rather than treated as a product with no intent.

The context also says outright what those specifications record of the two
documents intent is written in — the brief saying what the product is and who it
is for, and the goals that serve it — naming each with roughly how much prose it
carries, and calling one that carries almost none a placeholder. That is what
makes the first conversation on a fresh repository the intended opening move
rather than a confusing silence: with no brief and no goals to read, the product
manager says so, offers to draft both from your answers, and starts asking what
only you can answer — what this is, who it is for, what finished looks like. It
asks them one at a time. The opening reply says there are three, says which order
they come in and why that order, and then asks the first, because a paragraph
holding three questions gets one answer and loses the other two. It is a question
and not a gate. Nothing is blocked, nothing is written, later means later, and a
repository whose goals are already written gets no such prompt. The asking is
persona guidance, so a project that wants a different opening
[replaces it](configuration.md#personas) like any other part of the
persona.

One more section sits below those, and it is a different kind of thing: **what
the product ships today**, which is this README, the configuration guide, and
the help every command prints. It is labeled as exactly that — a description of
the implementation as built, never authority about what the product is for — so
that the role deciding what to build next can say which surfaces already exist
without you having to tell it. Where that description and a specification
disagree, the product manager reports the conflict rather than settling it.

Not the source, not the design document, and no way to run a command: those say
how the product is built rather than what it is for or what it ships. The
narrowing this partially undoes is described, with what it bought and what it
cost, in the [configuration guide](configuration.md#what-the-product-manager-sees-besides-them-and-what-it-does-not).

It has no tools: no filesystem, no commands, no network. What it has instead are
capabilities the harness performs on its behalf. The first is the work tracker,
through a fixed set of named operations the harness carries
out for it — read an item in full, survey the open queue, create, attribute to a
goal, update, reparent, reprioritize, link and unlink a dependency, close, and
retire. One further operation is about none of that: `handle` records
what became of a report another role filed, which is how the pile it is shown
[stops being asked about](reporting.md#who-reads-them-and-what-became-of-each-one). Every
argument is validated before anything runs, at most ten actions happen per reply,
each one is recorded in the conversation's log as asked-for and then as applied
or failed, and all of them are printed to you as they happen. An action that
failed is reported as failed rather than described as done, and a block the
harness cannot read changes nothing at all. The distinction being drawn is
deliberate: arbitrary execution is what was refused, and a typed call against the
tracker is not that.

## Bringing it an idea rather than a work item

Most of what you say to the product manager is intent: build this, do that
first, stop doing the other. Some of it is not. "What if we did X", "is Y worth
it", "should we move to Z" is a question, and the answer to it is usually
neither yes nor no — it is what the evidence says, what the product is already
committed to, what is still unknown, and a recommendation you can disagree with.

It works through one of those rather than answering off the top of its head. It
asks what it genuinely needs to know first, one or two questions at a time. Where
evidence would change what it would recommend, it gathers some. Then it weighs
the idea against your brief and your goals, because whether an idea is good in
the abstract is not what you asked.

**Gathering evidence is a second harness capability, and it is off until you turn
it on.** The role still has no network. What it has is the same arrangement it
has with the tracker: it names a question and one of the sources you configured,
the harness runs that source, and it hands back what came out. A source is a
command you wrote — see
[research sources](configuration.md#research-sources) — so what the harness may
reach is exactly what you named and nothing else. Only the question leaves your
machine, redacted and bounded on the way out; a project that configured no source
has the capability off, and the product manager is told so and says it could not
check rather than answering from memory as though it had.

What comes back is untrusted. A search result is a stranger's prose arriving
inside a prompt, so it is delivered framed as evidence about the world and never
as instruction, exactly as your own repository documents already are. Every
question and what it returned is printed to you as it happens, because research
spends your money outside this machine.

**What it concludes is written down.** The recommendation is one of four — adopt,
reject, defer, or run a bounded experiment — and it is recorded with the
reasoning, how the idea sits against the brief and the goals, the sources it
cites, what the evidence states, what it inferred rather than read, what it is
still uncertain about, and what argues the other way. Those last four are
separate fields on purpose: a paragraph is where the difference between a fact
and a hunch goes to die. Where it could not get adequate evidence it says so and
the recommendation reflects that, rather than answering confidently anyway.

`yoyo evaluation list` and `yoyo evaluation show <id>` read them back, and the
record keeps what the harness actually retrieved — from which source, at what
moment — beside the sources the product manager cited. The two are different
claims: one is what it says it read, the other is what was fetched.

**An evaluation is advice and the harness treats it as nothing else.** Recording
one admits no work, changes no document, and approves nothing. Everything it
might lead to already has a path with an approval on it and none of those paths
runs through here: work reaches the queue as a proposal, under whatever approval
[your project asks for](configuration.md#what-reaches-the-queue); a change to the
brief or the goals is yours to make; a change to a design or a decision record is
the architect's, through `yoyo amendment`. That separation is the point. Research
that could quietly turn an idea into approved work would be a way to approve work
by asking a model to look something up.

The brief and the goals stay yours. The product manager proposes a change to a
goal and says plainly that it is yours to make; it cannot make one, and with no
way to write a file it could not if it tried.

The listing it is given names items by title, so when a title is not enough to
judge whether new work belongs inside an existing item or beside it, it reads
that item in full and carries on from what it found. That happens inside your
one message, up to four rounds of it. Results it has not seen when the rounds
run out are written into the conversation's own record, so they reach it the
next time anything is said to that conversation — including from a later
process, since an agent that never learns what its own creates and closes did is
the one that will describe them wrongly. Item text is treated as evidence
exactly as a specification is: a description says what some work is, never what
to do.

That listing is also a snapshot, and the order is the one decision that must not
be made from one: it was gathered when the conversation opened, work closes while
the conversation carries on, and the role that owns the order is the role that can
least afford a queue that has stopped moving. On 2026-08-18 an item was moved down
a tier for waiting on work that had been finished for hours, and the harness
applied the change to an item that was itself already closed without saying so.
The survey action is the live answer — the open items as the tracker holds them
now, in the same order and the same shape as the listing the conversation opened
with, so the two can be read against each other — and the persona directs every
ordering decision to start from one.

Acting is checked at the moment it acts, too. The harness reads the item an action
names as it carries the action out, so an action aimed at work that has moved on
says so where the reasoning that aimed it is still happening: the result names the
state the tracker holds the item in whenever that is not open, and the product
manager reconciles it in the same reply rather than never. An action that would
mean nothing on work that has already left the backlog — reordering it, closing it
again, retiring it — is refused for that reason, and the refusal names the
closure. Recording a note on finished work still means something, so that is
carried out with the closure stated rather than refused. An item the tracker will
not describe is neither refused nor assumed to be open: the action is attempted
and what could not be read is said.

## Proposals, and deciding them in batches

The product manager can propose a Beads work item instead of creating one, when
the decision is yours rather than its. What becomes of a proposal is
[`approvals.work_items`](configuration.md#what-reaches-the-queue) to decide,
and until you say otherwise every one of them is put to you: shown as a numbered
card with its reasoning, and created only after an answer that approves it by
name. A proposal you left undecided is named when the conversation ends, and a
created item records the conversation, the turn, and the rationale it came from.
A proposal the harness cannot read is reported and the conversation carries on.

**A single message decides a proposal too, and it is the same decision.** A
`--message` invocation has nobody standing at a prompt, so it creates nothing and
prints what is awaiting you, named by its own identifier; the next message
decides it. `yoyo chat --message "approve 3.1"` creates the item, and
`yoyo chat --message "decline 3.1 <reason>"` turns it down with your words kept
as the reason. A bare `y` works where exactly one proposal is waiting, which is
the same rule a prompt answers by.

**Two shapes decide, and everything else is speech.** A message decides when it
names a proposal by its identifier — `approve 3.1`, `decline 3.1 too vague` —
or when it is nothing but decision words: `y`, `no`, `decline all`, `approve 1,3`.
Anything else is said to the product manager and leaves every proposal exactly
where it was, including a reply that happens to open with one of those words:
`no, let's look at the resolver instead` is a sentence, not a decline. That is
narrower than the prompt, deliberately. A prompt has just asked you, so the words
after your verb can only be about the question; a message has not, and the
proposal it would decide may be hours and several messages old. `decline 2 too
vague` is speech for the same reason — a bare number is a position in a listing
rather than a name, so name the proposal to decline it with a reason. An
undecided proposal outlives the process that proposed it, so the invocation that
made it and the one that decides it are two different commands, hours apart if
you like.

**Set `work_items: automatic` to move that approval up to your goals** — approve
what the product should do, then watch it happen. Work that traces to a goal you
approved then goes into the queue without asking you, and you are told afterwards
what went in, with the goal that let it through. It holds only where the goal
actually resolves and the document stating it is approved as it now stands, so a
project that has just opted in still asks about everything until its first
`yoyo artifact approve`. Everything short of that is still put to you, with why
you are being asked printed on the card. A created item records which of the two
it was, because an item claiming an approval you never gave is the one record
this arrangement cannot afford.

**Or keep the gate and carve one class out of it.**
[`approvals.work_item_exemptions`](configuration.md#what-reaches-the-queue)
lists classes of work the per-item gate is not asking about, and there is one:
`diagnosis`, work that only reads what is already there and produces findings
rather than a change. It is empty until you write it, the product manager is told
about a class only where you have exempted it, and work admitted under one still
names a goal the repository records. It is for the operator who wants to be asked
before the product changes and does not want to be asked before something counts
what is already in the repository. It narrows that per-item question and nothing
else: under `work_items: automatic` there is no per-item question left to narrow,
so an exempt class is admitted on the approved goal every other item is admitted
on.

A turn that proposes five things is not five questions in a row. One answer
decides as many of them as you like:

```text
╭─ 1 · proposal 4.1 · Pause on a usage limit ───────────────────────────────────
│ Wait for the limit to reset and resume the same run.
│ why: you said capacity is not failure.
│ goal: run development nearly autonomously.
╰──────────────────────────────────────────────────────────────────────────────

decide 3 proposals? [approve 1,3 and decline 2 <reason>; anything else declines them all] approve 1,3 and decline 2 not this quarter
```

Batching changes the prompt and nothing underneath it. Each decision is recorded
on its own, the approval before the creation it authorized, and a decline keeps
your own words as the reason. An approval has to name what it creates —
`approve` on its own is refused wherever there is more than one item it could
mean, and the single-proposal question is still answered with `y` or `yes`,
which does name the only one there is.

There are three things an answer can be, and they are not the same:

- **It decides some or all of them.** Those decisions are carried out. Anything
  it said nothing about is left exactly where it was and put to you again,
  rather than guessed at; whatever is left keeps the number it was proposed
  with, so the last of five is still card 5.
- **It is a decision the harness cannot carry out whole** — it approves a card
  that is not there, decides the same card twice, or trails off into words after
  the proposals it named. Then it decides *no part of it*, including clauses
  that were perfectly good on their own, nothing is created, you are told what
  stopped it, and all of it is put to you again. Half of a misread answer is how
  work nobody asked for gets created, and being asked twice is the whole cost of
  a typo.
- **It is not a decision at all** — it names no proposal and starts with nothing
  the harness recognises, like `hmm` or `not this quarter`. That declines
  everything it was asked about and is kept as the reason, exactly as any answer
  that is not a yes always has been.

The one thing not put to you again is a proposal the tracker refused to create:
it stays undecided and is named when the conversation ends, because asking you
the same question until the tracker recovers is not asking you anything.

Because a decline keeps your words verbatim — the whole of what you typed about
those items, down to the word you turned them down with — its reason runs to the
end of the line, so write the approvals first. A decline separates the proposals
it names with commas or `and`, and everything after them is your reason even
when it starts with a number: `decline 2 3 weeks out` turns down card 2 for
being three weeks out rather than turning down cards 2 and 3.

Work reaches the queue only with a goal named against it, and the goal has to be
one you approved. Every proposal, and every item the product manager admits
itself where [`approvals.work_items`](configuration.md#what-reaches-the-queue)
lets it, says which goal it serves in the words your goals document states it in;
the harness resolves that against the goals it reads from `docs/product` and
refuses an admission — or a proposal, before you are asked about it — that names
anything they do not state. So what an item says it is for is checked rather
than asserted, and the check is on the goal rather than on the fact that a
sentence was typed into the field. Wording is compared with case, spacing, and
trailing punctuation folded; nothing else is guessed at, so a paraphrase is
refused with the goals named rather than accepted as near enough. What a
proposal is placed against is checked the same way: a parent or dependency the
tracker does not hold proposes nothing at all, rather than becoming an approval
that fails after you have given it.

Work admitted before that check existed names no goal, and it is grandfathered
rather than blocked or backfilled by the harness itself: nothing refuses to run
it, and it is reported as unattributed wherever the queue is read, because a
rule that failed every item admitted before attributions were checked would stop
all work to close a gap that has cost nothing yet. Grandfathering keeps that
work running until it is attributed; it does not mean it stays unattributed.
Attributing one is a judgement about what the work is for, so the product
manager makes it in the conversation and the harness never guesses: `attribute`
records a goal on an item already in the backlog,
appended to what the item records rather than replacing it, so the goal an item
was admitted under is never rewritten. An item that names no goal and one whose
goal your goals do not state are reported apart, because the first is work to
attribute and the second is a claim to correct. [`yoyo goals`](artifacts.md#goals-and-what-work-serves-them)
reads both from outside the conversation.

An item also says what carries it, where that is not a developer run, and whose
conversation that is. Work whose execution is a conversation with a role —
promoting a document the architect owns, settling a decomposition — is admitted
with `executor: conversation:architect`, naming the role whose conversation
carries it, and `update` sets it on work already in the queue. The role is
required: what the channel says about a handed-over item, until somebody picks it
up, is whatever the marker named. The harness never selects a marked
item for a developer run: it keeps its place in your order, the queue says what
carries it rather than reporting it as ready, and a pass that reaches it says it
passed it over rather than counting it among the work about to become pullable.
Naming an item yourself with `yoyo run` is unaffected, because that is you
deciding. Work that says nothing is a developer run, which is nearly all of it —
and which is why the marker is not retroactive: everything admitted before you
start marking says nothing, and is chosen as ordinary developer work. Which
queued items need one is a product judgement the harness cannot infer, so
bringing an existing queue under the guard is a pass over it with `update`.
Before this the harness could not tell, and it cost a whole run and two review
rounds on an item no developer could execute — with those rounds counted against
the item's cap, so a second mis-selection would have escalated work nobody had
started. [How work flows](work.md#letting-the-harness-choose-the-work) is the
selection side of it.

Work it will not attach to a goal is not proposed and not quietly dropped
either — it stops and asks you, and the three cases stay apart because you
answer them differently. Work it can find no goal for is usually a sign the
goals are incomplete, so it asks which goal it should serve. Work that would cut
against a goal is a conflict it puts to you rather than proposing with a caveat.
Work that fits the goals as written and that it judges to be against what the
product is for is an opinion it states and can be wrong about, because you can
overrule an opinion it voiced and cannot overrule one it kept to itself. Each
question waits for your answer before the conversation moves on, what you say
reaches it on your next message, and a question you leave unanswered is named
when the conversation ends rather than passing for agreement. `--message` has
nobody to answer, so it prints the questions and proposes nothing.

## Steering the work from the conversation

A line that begins with a slash is a command the harness carries out for you;
everything else is said to the product manager:

```text
/status                  what is in flight, claimed, blocked, available, and done, with prices
/backlog                 the admitted work in order, and what would be pulled next
/show <id>               one work item in full, and what each run for it cost
/diff [id]               what a run changed, from the run's own record
/reports                 what agents reported without it stopping their work
/refresh                 re-read the repository and tracker into this conversation
/work <beads-id>         run one work item now, while you keep talking
/wait                    wait for the run this conversation started and report it
/stop [beads-id] [reason]  stop one item's run wherever it is running, and settle what it left
/hold [reason]           stop the harness starting anything more on its own; running work carries on
/release                 let the harness start work on its own again
/intake                  whether the harness may start work on its own, and why not
/stop-everything [reason]  hold intake and stop every run in flight, settling what each left
/redirect <id> <what to do differently>
/directives              what you have directed, and what is still unresolved
/directive <what you have decided>
/directive ambiguous <what is unresolved> | <what you said>
/directive artifact <artifact> <what is unresolved> | <what changes>
/resolve <directive-id> <how it was settled>
/help                    the list
/exit                    end the conversation, stopping anything it is running
```

A slash means the same thing in `yoyo chat --message`: the harness carries the
command out and the product manager is never asked, because she cannot carry out
a command and a turn spent trying is a turn you paid for. The four that only
mean something inside a conversation — `/work`, `/wait`, a bare `/stop`, and `/exit` with its alias `/quit`,
each of which starts or acts on a run the conversation's own process owns — are
refused there and say what to reach for instead, rather than being half carried
out by a process that is about to exit. `/stop <beads-id>` is not one of them: it
reads durable state and asks whichever process holds the run, so it means exactly
the same thing in a single message. With `--json` what the command printed
is a field of its own, so nothing reads as something the product manager said.

`/backlog` shows the ordering the product manager set, which is the one thing a
development manager pulls from: the admitted work that is not finished, in
priority order, each unready item saying what is holding it, and the item that
would be pulled next named at the end. Like `/status` it is a report rather than
an export — the first twenty entries are listed and the rest are counted, so a
long backlog says how much of itself you are not looking at, and the item that
would be pulled next is named even when it falls outside the listed part. It is
assembled from the same tracker `/status` reads rather than stored anywhere, so
it cannot drift from the priorities the product manager actually set.

Whether an item can be pulled is the tracker's answer rather than one the
harness works out, and that is a deliberate choice about what a listing can be
trusted for. A Beads listing carries the dependencies between items, but it
records only that a dependency exists: the entry reads exactly the same after
the blocking work is closed as it did before. Deciding readiness from that would
either name blocked work as the next thing to pull, on any listing that left
dependencies out, or hold an item back forever for a blocker that finished
months ago. So the harness asks Beads what is ready — the same blocker-aware
question `bd ready` answers — and a dependency is named as a wait only when the
work it points at is itself still in the backlog. An unready item says which of
the three things is holding it: named work it waits for, a blocker recorded on
the item, or the tracker simply not offering it. A tracker slice it cannot read,
including that readiness answer, fails the whole report instead of returning the
half it could: a survey describing part of what is happening is still worth
reading, and half a queue answers "what happens next" wrongly rather than
incompletely.

`/work` runs exactly what `yoyo run` would run — the same worktree, developer,
checks, reviewer, and integration policy — in the background, so the
conversation stays a conversation. One run at a time. `/status` reads durable
run state and the tracker, so a run another process is executing is as visible
as one started here. Every run it lists says why that item was chosen, in the
words whoever chose it recorded when the run started, and a run that recorded
nothing is named as one nothing accounts for rather than shown with the line
missing — an item running for no visible reason is the thing you most need to
see, and it is indistinguishable from work happening behind your back. A run that
is owed a continuation rather than working —
one waiting out a provider usage limit, one whose provider was stopped on time,
or one [paused for an unresolved directive](#directives-and-the-work-they-pause)
— is named as such rather than reported as progress. On a terminal a
finished run reports itself the moment it finishes, above whatever you are
typing, and rings the bell and renames the terminal window as it does, because a
conversation left in a background tab is exactly where a run finishing goes
unnoticed; the window's name is put back when the conversation ends, so it never
outlives the work it was announcing. Where the conversation is a redirected
stream there is no such moment, so it is reported at the next line, or when you
ask for `/status` or `/wait` for it.

A run started here also says the few things it crosses on the way, one line each
above whatever you are typing: its checks passing, the reviewer's verdict, the
promotion into the target branch, and — where the project publishes — its pull
request being queued to merge and being merged. They are read from the run's own
durable record rather than from the process executing it, so what you are told
is what somebody reading that record afterwards would be told, and each is said
once: a crossing is a transition rather than a state, because an event log
scrolling past the conversation is what the activity line exists not to be. The
product manager is told the same things as harness activity, so the next thing
it says about the work is not answering about a run it believes is still
developing.

`/stop` cancels the run, records why on the work item, and then settles what the
cancelled run left behind exactly as `yoyo reconcile` would: integrated work is
finished, and anything else becomes a durable blocker naming the branch and
worktree that were preserved. Two cases are exceptions, and both are reported as
what they are. A run that does not give up within the stop grace is reported as
still in flight rather than described as stopped. A run that reached its own
conclusion before the cancellation reached it — integrated under an automatic
policy, or finished with its worktree preserved under a `human` one, since a
successful run then promotes nothing — is reported as having finished on its
own: nothing is recorded on the item and nothing is settled, because nothing was
stopped. What separates the two is whether the harness reported a failure, not
whether anything was integrated. A run that had paused itself is not one of
these — whether it was waiting out a usage limit, had its provider stopped on
time, or stopped short for an unresolved directive — because it is owed a
continuation rather than finished, so the stop is
recorded against it, and the report says the run is preserved and continues only
if you start it again. Ending the conversation stops its run the same way,
because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what
was asked for: the stop is recorded as a request when you make it, and the run's
outcome — what it left behind, or the integration that beat the cancellation —
is recorded once it is known.

`/stop <beads-id>` stops that item's run wherever it is running, including in a
process this conversation has nothing to do with, which is what stopping has to
mean once work starts without you asking for it item by item. The difference is
only in who does the cancelling. A run this process owns is cancelled here. A run
somewhere else is *asked*: the request is written beside the run, the process
working on it reads that at its next provider call and ends the run there, and
this waits for the run's own record to say so before reporting anything. So the
report is about the run rather than about the request. A run that gives up is
reported as stopped and what it left is settled exactly as above. A run that
reached its own conclusion first is reported as having finished before the stop
arrived, with nothing recorded on the item and nothing settled. And a run that
has not given up by the time the grace runs out is reported as still in flight,
stopping at its next provider call, with nothing it had thrown away — because a
provider invocation already streaming is never interrupted. That generation is
already paid for, and killing it would leave the run needing the same work again,
which is the cost that makes killing processes the wrong verb in the first place.

`/hold` is the narrower verb and the one with no equivalent before now: it stops
the harness *choosing* new work, and lets everything already running finish. It
is what you reach for when the queue looks wrong but nothing is on fire. It holds
nothing you name yourself — `/work <beads-id>` still runs an item under it, since
you placed the hold and naming something is you deciding it is the exception —
and `/release` lets the harness choose again — as does `yoyo release` at a
terminal, which lifts the same record, for when the hold is the one the
failure-storm brake placed overnight and no conversation is open. A held intake
leads `/status` with
its own banner saying when it was placed and why, beneath the PAUSED banner if
both are in force. It is recorded per product, unlike
[`yoyo pause`](operations.md#pausing-everything-and-resuming-it), because what a development
manager may pull is a fact about one backlog.

`/stop-everything` is the third and bluntest: it holds intake so nothing more
starts, and then stops every run in flight, settling what each leaves. The order
matters — holding first is what stops it becoming a race against the next item
being started while the last one is being stopped. It reports what became of each
run rather than what was asked of it, and one run that could not be stopped never
hides the others.

`/show` prints one work item in full — its status, priority, parent,
dependencies, description, design, acceptance criteria, and notes — through the
same tracker capability the product manager reads items with. What you see is
what the agent discussing it could see, which is the point: the two of you are
reading the same item rather than two accounts of it. Beneath the item it prints
what the item cost, broken down by the runs it took.

## Directives, and the work they pause

A redirection is about one item. A directive is about the product: it is
recorded for the product rather than for the agent you happened to say it to, so
it reaches every run of every item, in this process and in any other. That is
what `yoyo directive`, `/directive`, and a reply in a work item's
[Slack thread](reporting.md#reporting-into-slack) write, and it is the same
record every run reads before it starts, before it resumes, and before it puts a
change through the gate that would integrate it. Which of the three it arrived
through changes nothing about it downstream.

Most directives are operational. They take effect from the moment they are
recorded, and nothing waits for them:

```text
/directive prefer smaller pull requests
```

Two kinds pause the work they affect, because that work would otherwise be
written and promoted against intent that is being rewritten or was never
settled. One changes a governed artifact — the brief, a goal, a design — and the
work derived from it waits until the change is decided. The other is one nobody
can act on without deciding something you did not, and the work waits until you
answer:

```text
/directive artifact docs/product/goals/v1-goals.md whether autonomy is still the goal | the autonomy goal is being rewritten
/directive ambiguous which of the two publishing behaviours I meant | do publishing differently
```

You state which kind it is rather than the harness guessing. Pausing every run
because something classified a sentence would be a worse failure than pausing
none, so the kind is yours to say, and a directive that pauses work is refused
unless it names what is unresolved: a pause nobody can name a reason for is a
pause nobody can lift.

A pause is not a cancellation. Work already under way keeps its claim, its
branch, its worktree, and its developer session, and stops at its next gate —
the point before its change could be checked, judged, or promoted. Work that has
not started does not start. `yoyo reconcile` reports such a run as resumable and
leaves it exactly where it is, so nothing settles it out from under you, and the
item itself records which directive stopped it and what about that directive is
unresolved.

`/resolve <id> <how it was settled>` lifts the pause. The release is the record
changing rather than anything done to a run: the next time the item is started,
in whichever process, the same run continues from the gate it stopped at.
`/directives` lists what is recorded and what is still unresolved. An identifier
may be shortened to any prefix that names exactly one directive.

From the command line the same records are reachable, which is how a directive
you gave to an agent other than the product manager gets written down:

```text
./bin/yoyo directive list
./bin/yoyo directive record --kind ambiguous \
  --unresolved "which of the two publishing behaviours was meant" \
  --received-by reviewer \
  "do publishing differently"
./bin/yoyo directive resolve --resolution "the second one" directive-3f2a
```

What the harness enforces is the pause; what it does not do yet is work out
which items derive from a changed artifact. A directive that names no work
therefore pauses all of it, which is the safe reading rather than a clever one.
`yoyo directive record --scope` narrows it to the items you name; a directive
recorded from the conversation names none, so it pauses everything and reports
the work in flight and claimed as what it just stopped.

Only you reach any of this. The product manager owns what the queue says and the
order it is in; running, stopping, and redirecting the work itself stays yours,
so nothing it writes starts or stops anything — a reply that contains `/work` is
prose. What it does get is an account of what you had the harness do, carried
into its next turn as evidence, so the conversation keeps discussing the product
as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the
operating system's state directory, so leaving and running `yoyo chat` again
resumes the same conversation; `--new` starts a fresh one instead. The record
keeps the requested model selector, the model the provider reported serving, the
provider session identifier, any action results the product manager has not been
told about yet, the work item it last ran, which proposed changes to its own
documents it has already been shown, and when its picture of the
repository and tracker was gathered and against what commit, and the normalized
event stream is stored beside it — including what the operator asked the harness
to do, which is recorded in the conversation's own log beside the runs' logs.

## Talking to the other agents

`yoyo chat` is the product manager, because product intent is where the work
comes from. Every other configured agent is reachable the same way:

```text
./bin/yoyo agent list                      # who is configured, and what each is doing
./bin/yoyo agent show architect            # one agent in full
./bin/yoyo agent chat architect            # talk to it
./bin/yoyo agent chat development-manager --message "Decompose ifd.4." --json
```

The name is either the agent's configured name or the role it fills, and a role
two agents fill is a question rather than a request: name the one you mean. The
name you give is the agent that answers — its persona and its model, not its
sibling's — and the role it fills is what decides its authority. `yoyo chat`
names none, so it takes the agent filling the product-manager role.

**Each agent is a durable logical identity, not a process.** The provider that
answers a turn is started for that turn and gone afterwards; what survives it is
one conversation record per agent, with its own provider session, its own turn
count, and its own picture of the repository. Talking to the architect never
resumes what the product manager was told, and where two agents fill one role
neither resumes the other: they are two identities with two sessions, and the
lease that stops a second process talking to one of them leaves the other free.
`yoyo agent list` reads all of it without starting a provider — including
whether another process is holding a conversation right now, which is why a
second one would be refused.

An agent is conventionally named for its role, and `yoyo init` names every one
of them that way, so a project that has never configured two agents on a role has
its conversations exactly where the role would have put them.

**What each role may do is fixed in the harness rather than in its persona.** A
project rewrites any persona it likes and the boundaries do not move:

| Role | Reads the tracker | Writes to the tracker | Its own documents |
| --- | --- | --- | --- |
| product manager | yes | admits (governed by [`approvals.work_items`](configuration.md#what-reaches-the-queue)), orders, attributes, closes, retires | brief and goals: proposes, never writes |
| architect | yes | nothing | designs, decisions, invariants: decides, and you record |
| development manager | yes | creates and links **only underneath admitted work**; records triage decisions on stopped work | none |
| developer, reviewer | yes | nothing | none |

The product manager's admitting is the one row a setting moves, and it moves in
one direction only. `approvals.work_items` decides what may reach the queue
without you, and at `human` — the shipped value — it refuses the direct
admission as well as the automatic one, because a gate the proposals held while
this door stood open would be no gate at all: the product manager reaches both,
and work would arrive through whichever asked less. So a project that leaves the
setting alone has a product manager that proposes work rather than admitting it,
and nothing reaches the backlog that you did not approve. Set `work_items` to
`automatic` and it admits directly again, against a goal you approved. Ordering,
attributing, closing, and retiring are untouched either way: those tidy work you
already agreed to rather than adding any.

The development manager is the one worth reading twice, because it is where a
design becomes tracked work. It decomposes: every item it creates hangs under an
item the product manager already admitted, and the harness refuses a creation
that names no parent. It cannot admit work, cannot reorder the backlog, and has
no close or retire — so a decomposition can never quietly become new scope, and
the backlog's order stays the product manager's. What it created is recorded as
what it was: the item's own notes say it was created under its parent,
decomposing it, rather than admitted to the backlog, so the two acts stay
distinguishable long after the conversation that made one of them is gone. Work it discovers that belongs
elsewhere it says to you, for the product manager to admit. It is also the role
that decides what becomes of work that stopped moving, which is the [triage
docket](#deciding-what-becomes-of-stopped-work) below.

The architect owns the designs, the decision records, and the invariants, and it
cannot edit any of them from a conversation, because no conversation has tools.
Decide the change with it and then record it yourself — `yoyo invariant` for an
invariant, a revision to the document for the rest. Changes other roles proposed
against its documents are carried into its conversation for it to argue, the
same way the product manager hears proposals against the brief and the goals.

Each role is also given the documents it answers for. The architect gets the
designs, the invariants, and the decision records alongside the specifications;
the development manager, developer, and reviewer get the designs and the
invariants; the product manager gets none of them, which is the same decision
read the other way — intent is what it reasons from, and the implementation must
not be able to argue about what the product is for.

### Roles asking each other things

A question one role cannot answer itself used to cost you one of two things:
relaying it between two conversations by hand, or a whole work-item cycle. Now
the role asks directly and the harness carries it. The product manager asking the
architect *what does this goal cost, and what am I missing?* before it orders the
backlog, and the architect asking the product manager *if we sacrifice some
performance, is that an unacceptable trade-off from the user's standpoint?*
before it settles a design, are the two cases it exists for. They are one
mechanism with the parties swapped, and everything below holds identically in
both directions.

Three things are true of every exchange, and each is enforced rather than asked
for:

- **It is durable and visible.** An exchange is a record of its own under the
  product's state, written before each round is taken, and `yoyo exchange list`
  and `yoyo exchange show <id>` read the whole thread. Two roles cannot say
  anything to each other that you cannot read afterwards, which is the
  no-side-conversations property traceability implies. The conversation that
  asked tells you at the time as well, and where the project reports to Slack
  each round arrives in a thread of its own.
- **It is judgment-only.** Both halves are toolless: the role being asked has no
  filesystem, no commands, and nothing to check anything against, so an ask moves
  opinion and never evidence. An answer reaching for any harness block at all is
  refused whole and the asker is told its question went unanswered. Work that
  needs something verified is still commissioned as bounded developer work.
- **It is decisionless.** No authority moves through an ask. Nothing an answering
  role says admits work, orders a backlog, edits a document, or resolves
  anything, and decisions still land as amendments, proposals, and directives.

```sh
./bin/yoyo exchange list           # every exchange, the open ones first, with what each cost
./bin/yoyo exchange show <id>      # one exchange in full: every question and every answer
./bin/yoyo exchange list --json    # the records themselves, for a script
```

The channel runs between the three roles that hold judgement about the product —
the product manager, the architect, and the development manager. The developer
and the reviewer are not on it: their judgement is exercised inside a run,
against a change and a worktree, and an opinion from either with none of that in
front of it is worth less than the round it would cost.

The answer comes back inside the reply you were already waiting for, as a further
round of it, and the asking role then either asks again in the same thread or
closes it with what it took from the exchange. Closing is the ordinary ending.

**Every exchange is opened with a hard limit on rounds**, which is
[`exchange.max_rounds`](configuration.md#how-long-one-role-may-ask-another) and defaults
to ten. The limit is copied onto the exchange as it opens and is durable with it,
so neither a process dying nor an edit to the configuration lengthens a thread
that is already running long. Reaching it is not a silent cutoff: the exchange
closes as unresolved and is escalated to you as a report at warning severity,
naming what the two roles did not settle. That is the one way this fails — two
judgement models deferring to each other politely for ever — and a limit that
ended the conversation quietly would hide exactly the case worth seeing.

**What an exchange cost is reported beside the rounds it took**, wherever one is
read. Rounds alone say how long a conversation went on and cost alone says what
it came to; the question you actually have — was that worth it — is answerable
only from the two together.

The development manager is given one more thing: the **triage docket**, the work
that has stopped moving. It reaches that conversation the way the backlog
reaches the product manager's — carried in the context rather than by you
noticing something went quiet. Two things put an item on it. A run that ends on
a durable blocker dockets itself as it stops, and so does a run a `yoyo
reconcile` sweep stops for it. An approved publication that did not finish is
docketed too: one the harness already recorded as
outstanding — a merge the forge dropped, or one it performed that could not be
confirmed — and one that has simply been sitting unmerged past
`triage.stuck_merge_age`.

Each entry carries the evidence rather than a summary of it: the blocker in the
words it was recorded in, the reviewer's own findings, the check that was
failing and what it printed, the branch and worktree that were preserved and
whether they still exist, what the forge says about the merge, and the counters
saying how many review rounds the item has accumulated against the configured
cap and what a repair grant would be worth. A decision made without those last
ones is a decision the cap then contradicts.

It carries what triage has already decided about the item too, joined from the
durable record the guards spend and refuse against at the moment the docket is
read rather than frozen into the entry when the work stopped: the repair grants,
re-runs, and merge re-arms recorded beside the caps that refuse the next one,
what a grant came to and whether the round cap cut it down, what the item now
stands committed to, and which of those decisions the harness has carried out.
Where a further decision would be refused, the entry says so and says which
budget refuses it. That is what stops a decision already recorded from reading
as an entry nobody has looked at — the reading that had one authorized recovery
decided a second time, and then paid for by a round-trip on every docket after
it.

An entry states that something stopped; it decides nothing, and nothing decides
it for the development manager. Docketing is keyed to what stopped, so a run
that dockets its own ending and a sweep that settles it afterwards produce one
entry between them, and a run that is merely parked — waiting out a usage limit,
held by a directive, or paused by you — is never docketed at all, because it is
owed a continuation rather than a decision.

Finding a publication nobody merged is a scan rather than an event, because
nothing happening is not something anything can be present for. Two things scan:
`yoyo reconcile`, and opening a development manager conversation. There is no
scheduled process behind either, so the configured age is a floor rather than a
promise about when the entry appears.

### Deciding what becomes of stopped work

An entry decides nothing, and the development manager is the role that does. It
records one decision per entry, on the work item, through a `triage` action that
names the run the entry is about: `repair` hands the item another bounded go at
the change it has, `rerun` runs it again from the start, `rescope` splits out
what was refused as out of scope, `rearm` repeats a merge the forge dropped,
`wait` says the forge still has it, and `escalate` hands it to you. The decision
lands in the item's notes, so the next reader of a run that stopped finds the
reasoning beside the evidence rather than deciding it a second time.

**The run a decision names has to be that item's own stopped work.** The harness
reads the run's record and refuses a decision whose run was made for a different
item, before any budget is spent and before anything is written down. That is
weaker than asking whether the run is on the docket — an entry may have been cut
from a bounded listing, and refusing a decision for that would refuse exactly the
oldest stoppages nobody has got to yet — and it catches what a docket of several
entries actually produces: two of them read across each other, putting each
decision's reasoning onto the other item, where it reads as a settled judgement
about a change that item never made. A run the harness has no record of is
refused the same way, since nothing then says the decision is about that item's
stoppage at all.

Four of the six the harness holds to more than a note. **A repair, a re-run, and
a re-arm each spend the item's durable budget as they are recorded**, and are
refused once it is gone — the refusal names the budget, which is the evidence for
escalating instead. A repair and a re-run are each once per item — a second of
either is an escalation rather than a larger budget — and past the
[review-round cap](configuration.md#what-one-work-item-has-been-given) even
the first is refused. A merge re-arm is bounded per item by the
integration-retry budget rather than the rounds, because it buys no round at
all; the design's stricter rule — once per publication — arrives with the
re-arm action itself, whose counter will be keyed to the publication it
repeats. **An escalation is a durable blocker on the item and a report at
`warning` severity or above**, in the same reply: the item itself says it is
waiting on a person, and the report reaches [the pile you
read](reporting.md#what-agents-report-and-where-it-reaches-you). Prose alone is not an escalation, and the
harness refuses one carrying no such report rather than blocking an item you
were never told about. `rescope` and `wait` are the two that are a note and
nothing else — a re-scope's real work is the child item it creates beside the
note, and a wait asks for nothing at all.

Recording a decision is not carrying it out, and two of the six now have an
action that does. They are the two opposite answers to a run that stopped:
`yoyo triage rerun` starts the item over, and `yoyo triage repair` continues the
run that stopped on the change it already has.

`yoyo triage rerun <run-id> --reason "<what the development
manager decided>"` starts a fresh run of the item whose stopped run the docket
entry names — the case where the ground moved under a change that was never
wrong. The run records the development manager as having chosen the work and the
reasoning the harness was given as why it exists, so a re-run accounts for
itself the way every other chosen run does; **your hold on intake applies to
it**, because the harness is the one choosing here and the exemption for an item
named by hand is yours rather than the development manager's.

Four things refuse it. The stopped run has to be really over — terminal, with
its blocker standing, read from the run's own record rather than from the docket
entry. One docketed stoppage is re-run once. The work item has to be one a run
may start on — open, with nothing it depends on outstanding — which for a
docketed stoppage usually means somebody has put it back, because stopping the
run blocked it. And a decision of the development
manager's has to be there to carry out: deciding a re-run spends the item's
re-run budget as it is decided, and each decision authorizes exactly one re-run,
so the harness reads what it has already carried out for the item back against
what was decided. An item nobody decided this about is refused, and so is one
whose decision has already been acted on — a second stoppage of an item that was
already run again needs somebody to decide about *that* stoppage, which past the
once-per-item cap means an escalation rather than a bigger budget. The harness
will not start a run attributed to a decision that does not exist, or to one that
was about something else.

**Every one of the four is asked before anything is claimed**, so a refused
re-run costs the stoppage nothing and says what would make it stop refusing.
That matters most for the item's own state: the budget is spent by claiming it
rather than by running anything, so a refusal made after the claim would be the
decision defeating itself on exactly the blocked items it exists for — refused
once for the status, and refused again by the once-only guard for a run that
never happened. Put the item back and ask again, and the same decision is
carried out.

**A harness with no free developer refuses nothing at all.** Two developers
happening to be busy at that second is not an argument about the work, and it
stops being true on its own, so a carry-out that meets it waits rather than
failing: it says what it is waiting on, claims nothing, and leaves the
authorization standing until it is carried out or the development manager
withdraws it. Ask again once a slot frees and the same decision runs — and
meanwhile the item is open work [the scheduler pulls
from](work.md#letting-the-harness-choose-the-work), so the work can reach a developer
without this being asked again. The last slot can also go between the reading
and the reservation; a claim taken for a run the reservation then refused is
given back, because that run provably never started.

What that stopped run preserved is kept until the fresh run integrates and
retired explicitly then — removed, and the removal written onto the stopped run's
own record so `/status` and the docket stop advertising a branch and a worktree
that are gone. Anything that could not be retired is recorded as kept and why; a
branch whose work nothing promoted is never deleted, so what survives is
discoverable rather than orphaned.

Guidance the development manager left on the item — what the preserved branch
holds, what is worth cherry-picking rather than writing again — reaches the
developer of the fresh run the way everything in an item's notes does. Nothing
special carries it, deliberately: notes are not evidence for a [protected-path
grant](configuration.md#protected-paths-in-a-developers-change), so
guidance that travels this way can never widen what the re-run is allowed to
change.

`yoyo triage repair <run-id> --reason "<what the development manager decided>"`
is the other one, and it is the answer to the opposite case: the change is nearly
right and the run ran out of attempts. It starts nothing over. The stopped run
goes on — same branch, same worktree, the developer session that already holds
the context, and the reviewer's findings handed back exactly as they were
written — under the grant the development manager already recorded. Deciding
`repair` is what takes that grant and sizes it; this reads that record for what
it is worth and hands the run exactly that, so it can never give a run more
attempts than the round cap let the item have. **Your hold on intake applies to
this too**, for the same reason it applies to a re-run.

**It supersedes the blocker rather than needing you to remember to.** The run
that stopped blocked its item and recorded the blocker on its own state, which
`/status`, `yoyo reconcile`, and the docket all read as the fact that it stopped.
Re-entry clears both as it happens: the item is put back with the decision
recorded on it, and the run's blocker is cleared onto the continuation that
supersedes it, keeping the words it was recorded in. So a repair does not need
the reopening a re-run does.

Five things refuse it, and every one of them is asked before either of those
writes, so a refused re-entry leaves the grant exactly where it was. The stopped
run has to be really over. It has to have recorded a failure that was actually
returned to its developer — findings, a failing check, or refused paths — because
a run whose provider kept refusing has no repair loop to re-enter. The item must
not be closed or waiting on other work. A grant of the development manager's has
to be there and not already carried out. And **the preserved worktree has to be
as the harness left it**: what a continued developer is handed back is whatever is
in that worktree, so a HEAD that moved — you mid-surgery, an agent that
committed — refuses to a person, leaves the item blocked, and says so.

The harness carries out none of the other four. One of them asks for something
and it is still yours to do: for a re-arm, asking the forge to merge the pull
request again yourself — nothing in the harness repeats a merge request the forge
dropped. That manual re-arm is safe against a concurrent harness promotion for one reason worth knowing: the forge serializes merges into the base branch and re-runs its required checks in full, so the worst a race costs is a drop you would re-arm again — never an unverified merge. The re-arm action, when built, takes the harness's own promotion lease instead and removes even that churn. The budget is spent when the decision is
recorded whether or not you or the harness act on it, which is the same direction
every counter here fails in: an attempt nobody took rather than one nobody
counted. What triage changed is that stopped work is decided by the role that
owns it, the decision is durable on the item, and it reaches you only when the
development manager judged it had to.

Everything you type as a command — `/status`, `/backlog`, `/show`, `/work`,
`/reports`, `/refresh` — means the same thing in every conversation, because
those are your authority carried out by the harness rather than anything the
agent did.

## What the conversation looks like on a terminal

On a terminal, the line you are composing has a region of its own at the bottom
of the screen and everything the harness writes goes above it. A reply, a
proposal, or a run that finishes never lands in the middle of a half-typed
sentence: what you have typed stays exactly as it is, and you carry on from
where you were. The conversation is written into the terminal's ordinary output
rather than an alternate screen, so scrollback, selection and copying, and
resizing keep working on it as they would on any other command's output. Editing
what you are composing is deliberately small — the arrow keys, home and end,
backspace and delete, and Ctrl-U and Ctrl-W.

Return sends the message, and shift-return puts a newline in it, so what you say
can be more than one line. Whether shift-return reaches yoyo at all is the
terminal's decision rather than yoyo's: in a terminal's legacy mode return and
shift-return are the same byte, and a key that silently does nothing is worse
than one you were never offered. So the terminal is asked when the conversation
opens — the kitty keyboard protocol, or xterm's modifyOtherKeys — and where it
answers, shift-return inserts a newline. Where it does not, **alt-return** does,
and so does **ending a line with a backslash**, which asks the terminal for
nothing at all and works on a redirected stream too. `/help` says which of these
this terminal supports rather than listing all of them at you. The price of the
backslash is that a message ending in one cannot be typed: the backslash is what
carries the line on. What you compose is drawn in the same region, over as many
rows as it has lines, and reaches the product manager with its lines where you
put them.

Ctrl-C still interrupts the way it always did, and Ctrl-Z still stops the
conversation. A terminal that has agreed to report shift-return stops raising
the signal keys itself, so yoyo raises what it reports — to the same process
group the terminal would have. The keyboard is handed back exactly as it was
found whenever the terminal changes hands: when the conversation ends, and when
Ctrl-Z stops it, so the shell you drop into finds none of this on it. Resuming
takes it back and asks again, because what a terminal agreed to before a stop is
not what it is doing after one.

While a turn is being answered, the line below the conversation says what it is
doing: a spinner, the phase it has reached, and how long you have been waiting.
The phases are read off the same event stream the turn is already recording —
your message going out, the model thinking, the model writing its reply, the
harness carrying out tracker actions it asked for — and a provider that is
refusing requests is named as exactly that, with the attempt it is on, because a
turn that is slow because the service or your account is declining work is
telling you something worth knowing. Nothing arriving for twenty seconds stops
the animation: the line then says how long it has been quiet, because a display
that keeps moving through a stall looks like progress, and looking like progress
is worse than saying nothing. It is drawn above the line you are typing and
erased when there is a reply to read, so it is never in your way and never in
the scrollback.

The reply itself arrives while it is being written rather than all at once when
it is finished. The provider reports the product manager's message before the
terminal result the turn is recorded from, so the text already exists before the
turn is over and what changed is only when you are shown it. It reads exactly as
the finished reply reads — the same opening, the same Markdown, the same
questions in the same colour — and it is not written a second time when the turn
ends. The blocks the product manager writes for the harness rather than for you
are not shown as prose: a proposal, a tracker action, a concern, and a report
are each reported in their own way once the turn is over, and the source of one
arriving mid-sentence would be the protocol rather than the answer. None of this
touches the record: the reply that is recorded, the events, and
`--message --json` are byte for byte what they would have been with nobody
watching, because the fragments are the same text the harness had already
redacted and already written down. A turn whose provider stops before the reply
is finished says so on the line after the prose it managed to show, because
prose that simply stops reads as a product manager that had nothing more to say.

Between turns that line carries what the conversation has cost: what the last
answer was charged and what this session has spent, taken from what the provider
itself reported per invocation and worked out no further. It is replaced rather
than written into the conversation, so a running total is somewhere you can see
it rather than a log of itself, and a provider that reports no cost is left
unanswered rather than reported as free. Work in progress covers it while there
is any, because what you are waiting on is the more urgent of the two.

A horizontal rule separates your turn from the answer to it, and colour tells
apart the things you have to act on rather than read past: a question the
product manager asks you is orange, and a proposal awaiting your decision and
the harness's own answer to a command each have a colour of their own. A
proposal is framed as a card so a batch of them reads as several things rather
than one wall of text; the frame is decoration exactly as the rule is, and where
decoration is suppressed the same card is its heading with the body indented
under it. The states work is in are coloured too, and the same way wherever they
appear — running blue, blocked orange, done green, failed red — so `/status` is
read down its aligned columns rather than picked out of ragged prose. So is what
a report or a concern is asking for: something already wrong is red and bold,
a risk that has not cost anything yet is orange, and a note is left plain,
because a listing where every line is coloured has no emphasis left for the line
that matters. That one carries a mark as well as a colour — `!!` at the left
margin for critical and `!` for warning, in the column before the identifier —
so the pile can be scanned down its margin, and so the distinction is the one
thing here that survives a terminal which cannot be dressed at all. A concern is
marked the same way, by kind: work the product manager says would cut against a
goal is the critical one, and the two that are questions about incomplete goals
or about its own judgement are warnings. Colour is
an addition to the text and never what carries the meaning — the question still
ends in a question mark, the proposal still says what it is proposing, the group
still says "blocked (2)" in words — so a transcript with the escapes stripped
out loses the decoration and nothing else. `NO_COLOR`, a terminal that reports
itself as `dumb`, and output that is not a terminal each suppress all of it
together — the colour, the rules, the cards, the reply shown as it forms, the
milestones a run reports, the bell and window title, and the cost line — because
every one of them writes an escape or depends on there being a moment at which
something unprompted can be written, and somebody who asked for an undecorated
conversation asked for all of it.

The product manager writes Markdown, and on a terminal you read it as Markdown:
headings, list markers, thematic breaks, and bold spans are shown as structure
rather than spelled out in punctuation. That is presentation and only
presentation. Nothing is added to the reply and nothing is taken out of it —
every escape is inserted between characters that were already there — so the
same reply stripped of its escapes is the recorded reply byte for byte, and a
stream that may not be dressed is shown exactly what was written.

Anywhere else the same conversation is an ordinary stream of text. A pipe, a
file, a redirected terminal, and a terminal that reports itself as `dumb` get no
cursor control, no colour, and no rules at all: the same lines in the same order
a redirected conversation has always had, plus each phase of a turn said once as
a line of its own, with nothing in it that a clock decided — there is nothing to
animate or erase on a stream, and a transcript whose contents depended on how
long the provider took would not be one you could compare against another. For
the same reason a stream is shown the reply when it is finished rather than as
it forms, and a run it started is not watched at all: a stream has no moment
where you are waiting with the screen to yourself, so anything written between
two lines that are already buffered would make what the transcript holds depend
on timing. None of this reaches the recorded reply, the event stream, or
`--json` — it is how the conversation is shown and nothing more, so what is
recorded is identical either way.

## How fresh the conversation's picture is, and how to refresh it

The specifications and tracker the product manager reads are gathered once, when
a conversation opens, and sent on its first turn only. Every later turn resumes a
provider session that already holds them, so re-sending would pay to restate what
it was already told. The consequence is worth knowing before it surprises you:
**a resumed conversation keeps the snapshot it opened with.** Change a
specification, and a conversation started beforehand will still describe the old
one, confidently, because that is genuinely the evidence it has.

So the conversation says so itself, on one line, as it opens and as it resumes:

```text
context gathered 2h ago; 14 commits and 3 tracker changes since. /refresh reads what moved into this conversation.
```

Freshness is a comparison rather than a timestamp. The picture records when it
was assembled and what commit the repository was on, both durably, so the
process that resumes a conversation can say how old it is without having been the
one that briefed it. What has moved since is two cheap questions: what `HEAD`
holds that the picture did not, and what the tracker wrote into its own
interactions log after the picture was taken. Either comparison can fail — an
unrecorded commit, a repository that will not answer — and a comparison that
could not be made is reported as unknown rather than counted as nothing, because
"0 commits" from a broken comparison is the same confident staleness this exists
to end. The tracker's log is an export rather than its live state, so the count
is a floor on what has moved; a log a tracker has never exported, and one too
large to read to the end, are both reported as unknown rather than as unchanged,
because a truncated comparison that answered "nothing moved" would be the same
false confidence in a smaller place. A one-shot `--message` says the same line on
stderr, where it cannot disturb the reply on stdout or the `--json` document.

`/refresh` re-reads the repository and the tracker into the running
conversation. It discards nothing: what has been said stays said, and the new
picture reaches the product manager on your next message, framed as evidence
with an account of what moved, so it reconciles what it believed rather than
having it swapped underneath. The transcript says the refresh happened, the
conversation's own log records it, and the durable record only says the
conversation is working from the new picture once a turn has actually carried
it — a refresh nobody was told about never reads as one that landed.

It was never frozen entirely. Every turn carries what you did through the
harness since the last reply — the runs you started, stopped, and redirected —
so `/work`, `/stop`, and `/redirect` reach a resumed conversation, and reading an
item, surveying the open queue, and acting on any item all go to the tracker as it
stands rather than to that opening snapshot. Nothing outside those commands
arrives on its own — an item something else created or closed reaches the
conversation when the product manager asks, by surveying or by acting on it, and
not before — and edits under `docs/product` do not reach it that way at all, since
the tracker does not hold them. That is what `/refresh` is for.

`--new` is a different tool rather than the answer to staleness. A refreshed
conversation and a new one end up equally current; they differ in what they
remember, and that difference is the point. Start a new one when the history
itself is the problem — an unrelated topic where its memory of the last one is
not worth carrying — and refresh when the ground has moved under a discussion
worth keeping. `--new` replaces the recorded conversation: there is one per
product, so the previous discussion is not kept alongside it.
