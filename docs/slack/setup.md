# Reporting into Slack

Work the harness runs on its own is acceptable only while it is visible, and
today "visible" means a terminal somebody is sitting at. This is the same
account of the work, in a Slack workspace: **one thread per work item, one
message per milestone**, and every report an agent files carried through, as the
agent wrote it, at the severity it was filed under. Each thread's opening message
also carries **what its item is doing right now**, as a reaction, so the top of
the channel reads as a status board — see *The channel as a status board* below.

Who each message is from is worth being precise about. Each persona speaks under
its own name and face: the developer talks about the change it is making, the
reviewer about the change it is judging, the development manager about the
queue. What no persona did — a promotion, a merge, the operator's own switches —
arrives from **Yoyodyne** itself, because "the harness promoted this" is a real
account of a real act and no agent performs one. What an agent actually wrote —
a report, the argument in a proposal — is carried through word for word under
that agent's name.

Every one of those names says which product it is speaking for, taken from
`product.id`: **Development Manager (yoyodyne)**, **Yoyodyne (yoyodyne)**, and so
on for every speaker. An operator with a second product in development is reading
a second harness, and the name is the only thing a message carries that says
which one is talking.

None of that costs a model call. Every milestone is rendered from the record by
a fixed line per role, so the channel is deterministic and no model sits between
a fact and its reporting.

Nothing it posts is an act of its own. A separate process you start reads the
harness's own durable records and posts from them, so:

- Nothing waits on Slack. A workspace that is down, slow, or misconfigured
  changes nothing about any run — no wait, no failure, no parked work.
- Nothing is lost. The process catches up from its own cursors when it comes
  back, so an outage delays messages rather than dropping them. A process killed
  between posting a message and recording that it posted repeats that one
  message; the durable record is the authority and this is a view of it.
- Catching up does not flood the channel. Messages go at about one a second,
  which is what Slack keeps accepting indefinitely, and a backlog too deep to
  post one message at a time is summarized per thread instead of replayed. See
  *Coming back from a long gap* below.
- No run and no agent ever holds a Slack token. The tokens live in this one
  process's environment and nowhere else — never in `.yoyodyne`, never in a
  prompt, never in a run.

Replies go the other way, and only for the people you say. A reply in a work
item's thread, from somebody this project granted `direct-work`, is recorded as a
directive against that item and reaches the work exactly as one typed at a
terminal does; anybody else the `operators` mapping names is answered saying it
was not acted on, and anybody it does not name is told once that this app does
not know them and who to reach out to instead. A project that has named nobody is
steered by nobody, which is what every workspace is until you add yourself. See
*Steering the work from a thread* below.

Setting this up takes about five minutes and needs a Slack workspace you can
install an app into.

**`yoyo setup` offers to do steps 4 and 5 for you**, which are the two that
happen on your own machine: it asks which channel to report into, writes the
configuration block, and then hands you the keychain's own prompt for each
token, under the same namespaced names step 5 describes. It will not overwrite a
pair that is already stored. Steps 1 to 3 are on Slack's screens and step 6
starts a process, so those stay here and stay yours; read them either way, since
what setup asks you to confirm is that you have done them.

## 1. Create the app from the checked-in manifest

The app is described by [`manifest.yaml`](manifest.yaml) beside this document.
Creating it from the manifest rather than by hand is what makes two workspaces
set up a month apart the same app, with the same scopes.

1. Go to <https://api.slack.com/apps> and choose **Create New App** → **From an
   app manifest**.
2. Pick the workspace.
3. Paste the contents of `docs/slack/manifest.yaml`, review the summary Slack
   shows you, and create it.

Slack will warn that Socket Mode needs additional setup in App Settings, and
that you can create the app first. That is not a refusal. Socket Mode is how
events reach a machine behind NAT, and it needs an app-level token; there is
no app yet to hang one on. Create it. The token is the next step.

If Create itself is blocked — some workspaces treat the warning as a hard
stop — paste the manifest with `socket_mode_enabled` set to `false` and the
`event_subscriptions` block removed, create the app, then continue from
step 2 and turn Socket Mode on there. Events without a request URL require
Socket Mode, so they cannot go in until it is actually on. Once it is, paste
the original manifest back in under *App Manifest*.

The manifest says what each scope is for. The two that read messages —
`channels:history` and `groups:history` — are what carries a message in the
channel back to your machine: a thread reply, which is how the harness is
steered, and an @-mention of the app, which is answered. An operator who would
rather not do either from Slack can delete those two scopes and the two
`message.*` events beside them: everything else in this document still works, and
nothing typed in the channel ever arrives.

`im:write` is used for two classes of message and only those two, sent directly
to whoever step 4 grants `direct-work`. The first is **the harness reporting
itself degraded**: a session choosing work from a build the harness has moved
well past, and the harness having started nothing at all while work was ready.
The second is **advisory-once** — a fact said exactly once and never repeated,
which today is one value the project's template has improved that this project
never edited. Every one of them is sent once rather than repeated. Removing the
scope costs those direct messages and nothing else: the stale-build message and
the improvement are in the channel either way, and the stall is in the durable
record `yoyo status` reads back.

## 2. Install it and take the two tokens

1. *Basic Information* → **App-Level Tokens** → **Generate Token and Scopes**.
   Give it any name, add the `connections:write` scope, and generate it. It
   starts `xapp-`. This is the token that opens the Socket Mode connection;
   without it the app has no way to reach your machine. If *Socket Mode* in
   the left nav is still off after this, turn it on — that is the additional
   setup Slack asked for when you created the app.
2. **Install to workspace** on the app's *Basic Information* page, and approve
   the scopes.
3. *OAuth & Permissions* → copy the **Bot User OAuth Token**. It starts `xoxb-`.

Keep both somewhere your shell can read them and nothing else can. They are
credentials for posting into your workspace.

## 3. Invite the app to the channel

Create or pick the channel the threads should be opened in, and invite the app
to it:

```
/invite @yoyodyne
```

An app that has not been invited is refused by Slack with `not_in_channel`. The
sink says that once and then waits it out quietly, retrying every few minutes
without repeating itself: what clears it is you inviting the app, and a log line
every fifteen seconds until you do would not make it clear any sooner. Nothing is
lost while it stands — the cursors do not advance past a message that was never
posted — and the sink says so again when the workspace starts accepting messages.

Take the channel's id while you are there: **channel name → About → the id at
the bottom**, which looks like `C0123456789`. A name works too, but an id
survives somebody renaming the channel.

## 4. Tell the project where to report

In your project's `.yoyodyne/config.yaml`:

```yaml
slack:
  enabled: true
  channel: C0123456789
```

**It is already in the file, commented out.** `yoyo init` scaffolds this block,
and an `operators` example beside it, under a paragraph pointing back at this
document — so the work here is deleting the leading `# ` from each line and
putting your own channel id in. A configuration written before that existed has
no such block; adding one is the same three lines.

A project that enables reporting without naming a channel is refused when the
configuration loads, before any work is claimed. A project that says nothing
about Slack reports nothing, which is every project until it opts in.

Each speaker has a face as well as a name, and the face is yours to change —
including to a custom emoji this workspace already has:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    developer: ":ship-it:"
    harness: https://example.com/faces/yoyodyne.png
```

Keys are roles, or `harness` for what no persona did; values are an emoji
shortcode or the https URL of an image. Leave a speaker out to keep the picture
the harness ships. Both shapes work with the scopes the manifest already asked
for, so neither costs a reinstall. **The names are not configurable** — only the
picture is: who speaks is a claim about who did the work, and that stays the
harness's to make. The product each name carries is not a choice either; it is
read from `product.id`.
[`docs/configuration.md`](../configuration.md#avatars) has the whole of it.

Who may steer the harness from a thread is not part of this block. It comes from
the top-level `operators` mapping, which is where the project says which humans
it recognizes — and a human is bound there by all of their identifiers rather
than by their Slack one:

```yaml
operators:
  your-name:
    git_email: you@example.com
    slack_member_id: U01234567   # your profile → "Copy member ID"
    grants:
      - direct-work
```

The allow-list is then derived: the humans granted `direct-work` who have bound
a member id, and nobody else. Until somebody is on it, every reply is answered
saying it was not acted on, which is what a workspace gets by default. The member
id lives in the configuration rather than in the environment because it is
identity rather than a secret.

The mapping is also who this app *knows*, which is the wider list: everybody with
an entry here, whatever you granted them. Somebody with no entry at all is told
once per thread that this app does not know them, and the names in this mapping
are who they are told to reach out to — so an entry with no grants is worth
writing for a colleague who should be recognized without being able to steer
anything. A mapping that names nobody says this to nobody.
[`docs/configuration.md`](../configuration.md#operators) has the rest of the
mapping, including the other grant and the namespaces you can bind.

> **Moved:** this used to be `operators` *inside* the `slack` block. It is not
> accepted there any more, and a configuration that still has it is refused when
> it loads, with a message naming the entry to write instead.

## 5. Store the two tokens under this project's names

They go in the sink process's environment and nowhere else, and they are read
into it from somewhere only its own launch looks. On macOS that is the keychain,
which keeps them encrypted at rest:

```sh
# once, with <product id> as the `product.id` in .yoyodyne/config.yaml:
security add-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w
security add-generic-password -s yoyo-slack-app.<product id> -a yoyo -w
```

`-w` with no value makes the keychain prompt for the token, so it never reaches
your shell history. Elsewhere, a `chmod 600` file this project's launch sources
does the same job in plaintext at rest — write the two `export` lines into
`~/.config/yoyo/<product id>/slack.env`.

**The names carry the product deliberately.** A generic pair is
indistinguishable between projects, and indistinguishable is how a sink ends up
posting one project's work into another project's channel: running more than one
harness on a machine is the ordinary case, not the exotic one, and under a shared
name every check of the form "a Slack token exists" passes for all of them while
at most one of them is right. Under these names, `yoyo doctor` can ask whether
*this project's* secrets are stored.

## 6. Start the sink

On macOS, the harness does this for you:

```sh
yoyo slack ensure
```

It starts a sink only if nothing is reporting for this product, reads this
project's own keychain items into that one process, and returns either way — so
it is what an unattended pass can run every few minutes as well as what you type
once. It prints what it did, in one of four ways: a sink already running, a sink
it started and the pid it started as, the stored items it could not read, or
reporting turned off for this project so there is no sink to run. Nothing it
prints is a token. It fails only on the third, which is the one outcome somebody
has to do something about; a project reporting nowhere is healthy and says so.

**Nothing schedules this for you yet, and that is a gap rather than a design.**
The step is what the installed maintenance pass is meant to call, once per
product checkout, in place of the hand-rolled `pgrep`-and-one-namespace step it
replaces. That pass is the productization of the operator's own script, tracked
as `yoyodyne-ifd.207`, and it is not in this tree: nothing `yoyo` installs runs
anything on a schedule. Until it lands, the timer is yours — put `yoyo slack
ensure` in whatever already runs unattended on that machine, a `launchd` job, a
`cron` line, or the pass you keep, once per product.

Whether a sink is running is asked of **this product's lease**, which is the
same lease the sink itself takes and one per product. That is what makes it
right on a machine running more than one harness: a `pgrep` for `yoyo slack`
matches the sibling project's sink, so a pass built on one would decide this
product's sink is running when it is the other product's, and this project would
report nothing indefinitely. The tokens come from this product's names for the
same reason. Run it in each product; the products do not see each other.

The sink it starts is in a session of its own, so it stays up when the pass, the
terminal, or the job that started it goes away, and it says what it is doing in
`sink.log` beside that product's own sink state. `--json` is the same account
for something that reads it rather than somebody.

Underneath, that is exactly the launcher below, which is what to write where
there is no keychain to read — or where you want the sink in front of you rather
than behind you.

The launcher reads this project's pair into exactly one process:

```sh
#!/bin/sh
# ~/bin/yoyo-slack-<product id> — the assignments are on the exec line, so the
# tokens exist only in the sink's environment and never in your shell's.
SLACK_BOT_TOKEN="$(security find-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w)" \
SLACK_APP_TOKEN="$(security find-generic-password -s yoyo-slack-app.<product id> -a yoyo -w)" \
YOYO_SLACK_SECRET_NAMESPACE=<product id> \
exec yoyo slack "$@"
```

With the environment file instead, the subshell does the same:

```sh
(set -a; . ~/.config/yoyo/<product id>/slack.env; YOYO_SLACK_SECRET_NAMESPACE=<product id> exec yoyo slack)
```

`YOYO_SLACK_SECRET_NAMESPACE` is not a credential and is not read as one. It is
how the sink records whose secrets it was launched with, so something other than
the sink can tell one that is merely running from one that is running for this
project. Leave it out and reporting works exactly as before; what is lost is
anything being able to notice when it is wrong.

It prints the workspace and channel it connected to, and then stays open until
you stop it with Ctrl-C. Leave it running in a terminal, a `tmux` window, or
whatever you use to keep a long-running process around.

To check the setup without leaving anything running, ask for a single pass:

```sh
yoyo slack --once
```

That posts whatever is due and exits.

**It reports what happens from the first time you ever start it.** A product
with two hundred runs behind it does not get two hundred threads on the day
somebody turns reporting on: work that was already over is left in the records,
where `yoyo status` and `yoyo reports` read it. Work still in flight is caught up
on in full, and the first pass prints the moment it is reporting from.

That moment is written down once, on that first pass, and every later start
reads the same one back rather than taking a new one. It matters more than it
sounds: a sink that started its history at each launch would treat everything
that happened while it was stopped as work from before it cared, and the
`critical` filed overnight is exactly the message that would go missing. Because
the moment is fixed, downtime is a gap the sink reads across — a report filed
while it was stopped, and a run that began and ended while it was stopped, are
both posted when it comes back.

If you do want to start the history over — a channel you have wiped, a product
you are re-pointing at a new workspace — stop the sink and delete
`products/<product id>/slack/cursors.json` under your state root. That is
`$YOYODYNE_STATE_HOME` if you set it, `$XDG_STATE_HOME/yoyodyne` if you set that,
and otherwise `~/Library/Application Support/Yoyodyne/state` on macOS or
`~/.local/state/yoyodyne` elsewhere. The sink takes a new moment on its next pass
and says which one. Leave `threads.json` beside it alone unless you also want new
threads, and `steers.json` — which is what it remembers about replies that
steered the work — alone either way: a directive settled before the new moment is
read past on its age, so starting the cursors over does not answer you again for
everything you ever steered. `refusals.json` beside them is the threads it has
already told somebody it does not know them in; deleting it says that sentence
once more in each of those threads, which is the whole of what it costs.

**Do not run two.** One sink per product: two of them hold separate thread maps,
so the second opens its own threads and posts everything twice. The second to
start is refused with a message saying so, and the refusal clears by itself when
the first one exits.

## 7. Ask whether it is actually reporting

```sh
yoyo doctor
```

A sink fails quietly — a channel that says nothing and a harness with nothing to
report look identical — so this is the verb that asks the questions the channel
cannot answer: are this project's secrets stored under the names above, is a sink
actually holding this product's lease or is there only the record of one that
died, is the sink that is running **this build**, and was it launched with **this
project's** secrets. Every finding it makes carries the command that fixes it.

The build question is the one that catches an installation that was working and
stopped. The sink is a long-lived process started from a binary that keeps moving
underneath it, so the build that is reporting and the build that is installed
drift apart with no event between them: nothing fails, nothing is logged, and the
milestones added since it started are simply never posted. In the channel that
reads as a quiet week.

Everything doctor says about reporting comes back as a **warning**, and it still
exits 0. That is the same rule as everywhere else here: reporting is an
observation and never a gate, so a sink you never started, a workspace that is
down, and a token nobody stored all leave a machine that runs work exactly as it
was. Do not wire `yoyo doctor`'s exit status up as a check on whether reporting
is healthy — read the findings, which name every one of these in full and carry
the command that ends it.

## What it posts

Each thread is headed by the item it is about — its identifier and what the item
is called, as `yoyodyne-ifd.118 — Slack thread headers carry the item's title` —
so a channel scrolls as a list of subjects rather than a list of identifiers. The
title comes from the durable record whatever opened the thread was read from.
Where that record carried none — an item whose first appearance in the channel is
its priority changing, which is every item admitted before you had a channel —
the tracker is asked what the item is called, once, as the thread is opened. A
tracker that will not answer costs that header its title and nothing else: the
thread opens either way. Threads that are already open stay exactly as they are.

The header is the only place a work item's identifier appears. **Every message
inside a thread names the work in words** — `Re-arm the dropped-merge check`, not
`yoyodyne-ifd.102.7` — because the header above it already carries the
identifier, and an item nothing has ever recorded a name for is said as *this
item* rather than as its slug. That holds for every item a message mentions: a
decomposition lands in the thread of the item it created, and says that item was
cut out of a larger one rather than naming that larger item's slug, because the
record keeps its identifier and nothing that says what it is called. Which item
that was is in the tracker and in the durable record. **No conversation identifier is posted at all**: the
record still carries it and `yoyo status` still reads it, but it is not something
anybody reading a channel does anything with. **No directive identifier is posted
either**: what you get for a reply you typed is the reply read back to you and
what it does to the work, and a slug in an acknowledgment is the one thing in it
you would have to go and resolve. The record still carries it, and
`yoyo directive list` is where you read it. What a message does carry — in words
where the sentence needs it, and in italics under it — is what you would follow
rather than look up: the run, an exchange, the pull request. And **the reasoning
a role wrote into the
tracker is one sentence here**, with the message saying that the rest of it is in
the item's record: the argument is written for somebody weighing the decision in
the tracker, and a paragraph of it under a one-line fact is what makes a channel
go unread.

Into the work item's thread, as they happen:

- the item arriving in the backlog: **admitted, with the goal it serves**, or
  decomposed out of the item above it, said by the role that did it
- work you approved from a proposal, admitted with the goal it was proposed under
- a goal recorded on an item already in the queue, and an item's priority changed
- the run starting, **carrying the reason that work item was selected**
- the checks passing or failing
- the reviewer's verdict, approved or sent back for repairs
- the promotion onto the target branch
- the pull request, a merge the forge queued, and the merge itself
- a merge that is not going to happen — the forge refused it, usually because
  the remote target moved under the run, or gave up on one it had queued — said
  as a `warning`, because nobody chose it and nothing else in the record says it:
  the change is promoted, the thread reads as landed, and what is left is a
  publication waiting on a person
- the run waiting — an exhausted usage limit, an overloaded provider, an
  operator hold, an unresolved directive — and the run carrying on afterwards. A
  run waiting out an exhausted usage limit is said as a `warning`, because it
  means hours in which nothing will happen for a reason nobody chose; the other
  three are ordinary facts, since an overload lifts in seconds and a hold or a
  directive is waiting on the person reading the channel
- the blocker that stopped a run, if one did, said as `critical`
- the run ending any other way — failed, cancelled, timed out — said in that word
  rather than in one word for all of them. A run the harness could not carry and
  one it stopped on time are `warning`s, because nobody chose either; a
  cancellation is an ordinary fact, since somebody did. Every one of these lines
  and the blocker line above also say what remains of the change — work
  preserved, work removed, or no artifacts recorded — because the attempt being
  over says nothing about whether the work is
- every report an agent filed against that item, as the agent wrote it
- every change an agent proposed to a document it does not own, with the
  argument it made for it

At the top level of the channel, unthreaded, goes what is about the whole line
rather than any one item: the operator holding and releasing intake, the
operator holding and lifting all harness activity, proposed work you turned
down — there is no item, because nothing was created — a block of tracker actions
the harness refused whole, said as a `warning` with the role that asked, how many
actions it asked for, and the refusal itself, because none of them happened and
the role that asked for them believed they had — and anything an agent filed with
no work item attached. Burying those in one item's thread would
misfile them. That list is what is *addressed* to the channel rather than
everything that appears in it: a thread reply asking for attention is shown there
as well, which is what the severity rule below does.

A provider refusing the harness for want of capacity goes there too, wherever it
happened. The harness asks a provider for work in three places, and each one
accounts for a refusal:

- **inside a run**, for the developer attempt or the review — the run parks, and
  the park is what is posted, in that item's thread
- **a conversation turn** with any role, which has no run to park
- **an independent `yoyo review`**, which uses the same reviewer with no run
  around it

The last two record the refusal themselves, and it is posted here as a `warning`
naming what was stopped and, when the provider quotes one, when the limit lifts.
Without it an exhausted limit reaches only whoever typed the command, and hours
of silence with a known cause look exactly like a quiet queue.

A watching `yoyo work` session is not a fourth place. It reads the tracker and
starts runs, and makes no provider call of its own, so a limit it meets is met by
a run it started and said by that run parking. That the list above is the whole
list is checked rather than asserted:
`TestEveryProviderInvocationAccountsForAnExhaustedLimit` sweeps the tree for
every provider invocation and fails on one that has no account of what an
exhausted limit does to it — so teaching selection, or anything else, to ask a
provider something arrives with a failing test rather than with a process that
goes quiet and says nothing.

What a watching `yoyo work` session is doing goes there too, and it is the one
thing here that is news precisely because nothing is happening: a session that
has gone quiet and a session that has died are the same silence otherwise. It
says when it opens, when a poll starts nothing — with the runs it can see going
and the items it passed over grouped by why, so an idle slot beside a working one
does not read as a stopped line — when a held intake brakes it, as a `warning`,
because that one needs somebody, when it resumes, and when it ends. Each of those
is said once, so a session idling overnight posts one message rather than one a
minute.

**And one thing is a state rather than an event.** Everything above is said once,
when it happens, which is right for a narrative and wrong for a night: intake
held at 00:02 and a session that stopped at its budget both said so, correctly,
and then nothing said anything for ten hours that could be told from a healthy
quiet queue or a dead sink. So a line that is **choosing nothing while work is
ready** says so again while it stands — every `--heartbeat`, an hour by default —
naming what stopped it, how long that has been true, how much ready work a
run could have been started for behind it, and how many promotions are waiting on
the forge to publish them:

> Nothing is being chosen on this product: intake is held — the harness held
> intake after runs kept blocking, for 10 hours now, with 4 items ready to pull
> and one promotion awaiting the forge.

Four states count: the operator holding all harness activity, a held intake
(whoever held it), a watch session that has found nothing it can start, and no
watch session running at all. It stops the moment the state clears, and says
nothing about the clearing — the release, the session opening, or the run it
starts says that itself.

The count of promotions is the second thing that makes it speak, and it is there
because a **dropped merge** is said once, as it happens. A reader who was away
for that message has nothing else that would ever tell them: the change is
promoted, the thread reads as landed, and the pull request sits on the forge. So
the count comes back with the line while the publication stands, and a line with
nothing ready at all says so as long as there is one.

It is otherwise deliberately narrow about when it speaks. A run in flight is not
a stalled line, so nothing is said while work is visibly moving. A product nobody
has ever watched is not one either: running items by name is a queue you are
choosing to keep, not a harness waiting on you. And **an idle line with nothing
ready and nothing waiting on the forge stays completely silent**, which is the
whole point — silence has to keep meaning nothing to do, so that the times it
does not are worth reading. Turning it off is not offered, because what that buys
is silence that means waiting on you; how often is `--heartbeat`.

**And one thing is the absence of a state.** All four of those are read from
something a process wrote down, which works only while that process is alive to
write it: a watch session that crashes writes no stop, and one that wedges goes on
recording that it is watching. So the sink also watches for nothing having
happened at all — no run started for half an hour, work a run could be started
for, and no hold, full machine or run in flight to account for it. That is
recorded against the product and sent to whoever you grant `direct-work` as a
direct message, once per stall and never once per check:

> Nothing at all has started on this product for 7 hours, with 3 items ready to
> pull and nothing accounting for it. The session choosing work last recorded
> watching at 2026-09-01T06:05:00Z, and has said nothing since.

That last sentence is what to act on: a session whose last word was `stopped`
wants starting, and one still claiming to be watching wants killing first. When it
clears the record closes and the channel hears nothing — the run that started says
that itself — and
[`yoyo status`](../operations.md#when-nothing-happened-at-all) reads the whole
history back afterwards, which is the only place it exists.

**The provider's usage window is not a stall, and says so instead.** When a run
comes back parked on an exhausted usage limit, the watch session records the
polls it makes inside that window and the time the provider said it lifts, and
every surface reads that as the accounting it is. The channel gets one note per
window rather than the alarm above, and nobody is messaged directly:

> Nothing is being chosen on this product for 30m: waiting on the provider's
> usage window until 13:43Z. Nothing has stopped and nothing is waiting on
> anybody — the harness asks again when the window lifts.

That distinction was bought: on 2026-09-05 a session waited a window out from
12:13Z to 13:43Z, the alarm fired at half an hour saying nothing accounted for
it, and somebody was paged for a machine doing exactly what the provider had
told it to. The window accounts for the quiet only until the time the provider
named — a session still choosing nothing after that is a stall again, which is
what keeps this from being a way to switch the watchdog off. `yoyo status` says
the same clause against the work it is holding back.

A run in flight is the one thing that quiets it, and only while that run is
still moving. That distinction is the difference between this working and not:
a killed run leaves a record saying it is in flight until
[`yoyo reconcile`](../operations.md#recovering-interrupted-runs) settles it, so
reading "in flight" as "working" would silence the watchdog for exactly the
crash it is watching for. What separates the two is the run's own record — a
working run stamps every provider event onto it as it goes — so a run whose
record has not moved for an hour stops accounting for the quiet. The hour is
well clear of anything a live run does: an invocation that emits nothing for
five minutes is stopped as stalled, and the slowest legitimate wait, a provider
usage limit, probes every half hour.

Reading what is ready costs one local tracker (`bd`) read per heartbeat, and
never on the path of any run. Two things here want that number — the waiting
line above and the stall watchdog — and they share one read rather than taking
one each: it is asked at most once a pass, and each of them asks at most once a
heartbeat. The watchdog needs its own interval because the state it is looking
for is partly a drained queue, and nothing but the tracker can say the queue is
drained; without one it would ask on every poll of a perfectly healthy idle
product, which is the one machine that should cost nothing. A tracker
the sink cannot read — no `bd` on the machine it runs on, say — costs that one
message: the sink says so in its own log and asks again at the next interval,
rather than guessing a number in either direction.

What that interval costs the watchdog is promptness rather than the stall: a
stall is noticed at the first reading after the threshold has passed, and it
closes at the first reading after it clears. The moment recorded against it is
when the harness last started something rather than when anybody noticed, so
the event says how long nothing happened whatever the noticing cost.

**And one thing is not about the work at all.** A project generated from a
built-in template records what that template supplied, and
[`yoyo config drift`](../configuration.md#extending-a-built-in-bundle) reports
every value the template has improved since that this project never edited.
`doctor` and `config validate` say the same thing as an aside — but all three are
commands somebody runs, and a harness left running for a fortnight runs none of
them, so a fix the template has since made to a persona sits unheard. So the sink
says it: one message per newly-available improvement, in the channel and as a
direct message to whoever you grant `direct-work`.

> builtin:v1 has improved agents.developer.model, a value this project has not
> edited: it was "sonnet" and is "opus" now. Nothing has changed and nothing is
> waiting on anybody: `yoyo config drift` shows agents.developer.model beside
> everything else the template moved, and it is adopted by hand or not at all.

It is said **once per improvement and never again** — marked in the sink's own
durable state as it is sent, so a restart says nothing about one it already sent.
A template that improves the same setting again later is a second improvement and
is said again; nothing is ever adopted for you, and the message says outright
that the next move is nobody's. The comparison costs one reading of your
configuration per `--heartbeat` rather than one per poll.

The queue changing comes from the conversations you hold with the product
manager and the development manager, read from the same durable records `yoyo
status` reads. A conversation's log is mostly the turn itself, and none of that
is posted: what reaches the channel is the few points where the backlog actually
moved. A conversation you replace with a new one stops being read from, so a sink
that was down while you replaced one may miss the tail of what the old one did —
the durable records still have it.

Severity is said in words rather than only in colour: a `critical` says
"Critical" and a `warning` says "Warning", so a client that renders no emoji
still shows them for what they are. An ordinary fact carries no marker, because a
label on everything is a label that means nothing.

**Severity also decides where a message is seen.** Slack's main channel view
hides thread replies, which is right for a routine note and wrong for a warning:
a run parked out of tokens can sit unseen inside a thread while the channel looks
quiet. So a `note` stays thread-only, and a `warning` or a `critical` is also
sent to the channel — Slack's own also-send-to-channel — while still being a
reply in its item's thread, so the narrative there is unbroken. Nothing new
judges this: it is the severity the record was already filed under, so the
channel view shows exactly the messages whose severity says somebody should see
them without opening threads.

Each transition is said once. A thread is a narrative rather than an event log
scrolling sideways, so a restart does not repeat what it already said — how far
each record has been read is written down as each message goes out and survives
the process. What can honestly happen twice is said twice: a check that fails
again, differently, after a repair attempt is its own message, and so is a run
that waits out a second usage limit. The heartbeat above is the one thing here
that is not a transition, and it repeats on purpose: what somebody coming back in
the morning needs is not that the line stopped, which was said at midnight, but
that it is still stopped now.

One thing to expect is not a thread at all. **Ask exchanges are designed and not
built.** Every persona has words ready for a turn of one and for one closing,
including one closing unresolved at its round cap, but nothing produces them yet,
so no `exchange:` thread is ever opened. When that work lands it adds messages to
this channel and changes nothing about your setup.

## The channel as a status board

Messages say what happened; they cannot say what is happening. Each transition is
said once — correctly, because a thread is a narrative — so the state an item is
in right now is somewhere inside a thread rather than on it, and finding which of
twelve threads needs you means opening twelve threads.

So the message each thread hangs from carries one reaction, and it is the item's
current status. There are four, and they are the whole vocabulary:

| Mark | Status | What it means |
| --- | --- | --- |
| :hammer_and_wrench: | working | A run is in flight on the item — developing, at the checks, repairing, being integrated. |
| :eyes: | in review | The change is with the reviewer. |
| :octagonal_sign: | blocked | The run stopped: failed, cancelled, timed out, or waiting on a provider, on your hold, or on a directive nobody has resolved. |
| :white_check_mark: | completed | The run finished and succeeded. |

The mark is replaced as the record moves and the one that has stopped being true
comes off, so a scan of the channel's top level answers what is working, what is
blocked, and what landed without a thread being opened. Nothing else is ever
added to an opener: **a status is about the item and a severity is about one
message**, and they never share a symbol, so a reader never has to work out which
of the two a mark is talking about. Anyone reacting to a thread themselves is
untouched — the sink only ever adds and removes those four.

Three things are worth knowing about it:

- **It is read from the item's latest run.** An item whose second attempt is in
  flight reads as working, whatever the first attempt did; what the first one did
  is in the thread. An item with no run yet — a thread opened by the backlog
  moving, or by a report — carries no mark at all.
- **It is reconciled rather than posted.** The status is worked out afresh from
  the durable records on every pass, so a run that finished with nothing left to
  say still stops reading as working. A change takes the other three marks off
  before putting the true one on — the opener wears at most one of them, so the
  rest of those calls hit nothing — which is what makes a sink killed mid-change
  settle instead of leaving a mark behind: whatever the opener is wearing when
  the next change comes, it is one of the four and it is not the one being set,
  so it comes off.
- **It is never a gate, and not even a message.** A workspace that refuses the
  reaction costs the channel its status board and not one message: the sink says
  so once in its own log and keeps posting.

That last one is the case to expect if you installed the app before this existed:
the marks need the `reactions:write` scope, which the checked-in manifest asks
for. **An app installed from an older manifest keeps reporting and silently marks
nothing** until you reinstall it from *OAuth & Permissions*.

## Steering the work from a thread

A reply in a work item's thread is a **directive** against that item: the same
record [`yoyo directive record`](../conversation.md#directives-and-the-work-they-pause)
writes, kept where every run of that item reads it before it starts, before it
resumes, and before it puts a change through the gate. Nothing about the channel
is a second kind of instruction — it is one more way into the record you already
have, which is why steering from a phone is as enforceable as steering from the
terminal the harness is running on.

Reply with what you want, and it is recorded as an operational directive: in
force from that moment, with nothing waiting on it.

```text
prefer the smaller change here — don't refactor the store as well
```

Two kinds pause the work instead, and **you say which**, because a classifier
deciding that a sentence stops work is worse than one that never stops it. Open
the reply with the word, and say what is unresolved — a pause nobody can name a
reason for is a pause nobody can lift:

```text
ambiguous: which of the two publishing behaviours did you mean
artifact: slack-reporting-design whether product threads may carry directives
```

`ambiguous:` is one nobody can act on without deciding something you did not.
`artifact: <name> <what has to be decided>` is one that rewrites a governed
document — the brief, a goal, a design — so work derived from it waits until the
change is decided. Either one stops the item at its next gate without cancelling
anything: the run keeps its claim, its branch, and its worktree.

Lift it the same way you would anywhere else, by naming the directive and how it
was settled. Any prefix of the identifier that names exactly one will do:

```text
resolve directive-3f2a the second one, and say so in the design
```

The identifier is not in the channel — no message posted here carries one — so
`yoyo directive list` at the terminal is where you read it, or
`yoyo directive resolve` settles it there without a reply at all.

Open the reply with `@developer`, `@reviewer`, `@architect`,
`@development-manager`, or `@product-manager` to record who you told. That is
attribution rather than routing — the record reaches every run of the item
whichever role it names — and a reply that mentions nobody is the product
manager's.

Every reply is answered in its own thread, **tagging you**, with the directive as
recorded and what it does to the work — *Recorded, for the Product Manager:
prefer the smaller change here — it applies from now on, and nothing waits on
it.* — or with why nothing was recorded. What that answer says is the whole of
what happened at that moment: there is no other
confirmation, and a reply that stopped work is shown at the top of the channel as
well, because work stopping is what somebody who has opened no threads most needs
to see.

Your own message then carries where that directive stands, as a reaction, so
scrolling back through what you said says which of your replies is still open and
which has been answered without any of them being read:

| Mark | What it means |
| --- | --- |
| :thinking_face: | Recorded, and not settled yet. It goes on as the reply arrives and stays while the directive stands. |
| :white_check_mark: | The directive is settled — carried out, decided, or answered. It lands when the outcome is said in the thread, not when the directive was written down. |
| :no_entry_sign: | Nothing was recorded. The thread says why. |

The mark is about the directive rather than about the harness having read you:
recording one is not disposing of it, so a directive nobody has settled keeps
saying so however long that takes. Those three are the whole vocabulary, they are
only ever on a reply, and the four status marks are only ever on a thread's
opener, so no message carries both.

**What later becomes of it is said in the same thread, tagging you** — and that
is the moment the mark on your message moves. A directive you asked for from a
thread is remembered against that thread, so when the record says it was settled
— by you at a terminal, in a conversation, by anybody — the settlement and what
it was are said where you asked rather than only where it was typed. A directive
recorded at a terminal has no thread and nobody to tag, so nothing is said about
it here.

An ordinary reply records an operational directive, which pauses nothing and so
has nothing to resolve. What settles one of those is somebody carrying it out,
and the case that reaches most replies is the product manager admitting the work
you asked for: the item it admits names your directive, and the directive's own
record is told which item it became. That is what the thread then says back to
you — the identifier of the work, not just that you were heard — so a reply that
turned into a work item stops wearing :thinking_face: at the moment there is
something to go and read.

The check mark says you have an answer, not that your instruction has lapsed.
An operational directive is in force from the moment it is recorded and stays
there; carrying it out records what it produced and withdraws nothing, so it is
still listed by `yoyo directive list` as in force, with what it became under it.
What ends one is you withdrawing it — `yoyo directive withdraw --by <who>
--reason <why> <id>`, or `/withdraw` in a conversation — which takes it out of
force without deleting it. There is no way to do that from a thread: the reply that recorded a
directive keeps its check mark, and the listing is where a withdrawn one reads as
withdrawn.

Four things are refused, visibly:

- **a reply from somebody without `direct-work`**, or with the grant but no
  `slack_member_id` bound. The list defaults to empty, so a workspace steers
  nothing until you add yourself in step 4.
- **a reply from somebody the `operators` mapping does not name at all**, which
  is refused differently: they are told once in that thread that this app does
  not know them and who to reach out to instead, and everything they say in it
  after that is written to the sink's log and answered with nothing. See *Somebody
  this project does not know* below.
- **a stated kind that says nothing unresolved** — `ambiguous:` on its own, or
  `artifact:` without a document and what to decide about it.
- **a reply in a thread that is not a work item's.** A directive from a thread is
  scoped to the item the thread is about, and one recorded against no item would
  pause the whole product. `yoyo directive record --scope` is how a wider one is
  recorded.

Everything else in a channel is left entirely alone: a message that is not in one
of these threads, a thread this sink never opened, and anything the app itself
posted — with the one exception in *Asking the app directly* below, which
answers and never records. The last is not a nicety: the sink's own messages
arrive back on the same connection, and reading one as an instruction would be
the harness directing itself.

`yoyo directive list` shows what is recorded whichever way it arrived, and
`yoyo directive resolve` settles one from the terminal. The two surfaces are the
same record.

## Asking the app directly

**@-mention the app and you always get an answer.** Anywhere the sink can see
you — the top of the channel, or a thread it never opened — a message that names
the app is answered where you said it, in a reply hanging from your own message
and tagging you. This is the one thing the sink says outside its own threads, and
it exists because the alternative was silence: a question at the top of the
channel had no handler at all, which reads exactly like a sink that has died.

This is for the humans your `operators` mapping names, whatever you granted
them. Somebody with no entry there gets the sentence in *Somebody this project
does not know* below instead of an answer.

Ask where things stand and you get the four lines — the same four
[`yoyo status`](../operations.md) prints and the same four the heartbeat posts,
read from the same place so a channel and a terminal cannot answer one question
two ways:

```text
@yoyodyne what is running?
@yoyodyne status
```

Words like `status`, `sitrep`, `what is running`, `what are you doing`, and
`where do things stand` all ask for it. Anything else gets one sentence saying
that this is the only question the app answers here yet, and where the work is
actually driven from — a work item's thread for a directive, and `yoyo` at the
terminal for everything else.

Two things it is not. **No directive is recorded**: a mention changes nothing
about the work, so a question at the top of the channel — where there is no item
to scope a directive to — is answered rather than refused. It is not lost either:
the sink's own log gets a line for every message addressed to the app, with what
was asked in it, written before the answer goes out, so a question the workspace
would not let it answer still leaves a record that somebody asked. And **nothing
is disclosed**: the four lines are already posted to this channel by the
heartbeat, so the answer tells a reader nothing the channel was not already
telling them.

A message that does not name the app is still left entirely alone, which is what
keeps a reporting channel a reporting channel rather than a participant in
everybody's conversation.

## Somebody this project does not know

A channel has other people in it. Somebody whose Slack member id is bound to
nobody in your [`operators`](../configuration.md#operators) mapping is told so,
once, in the words:

```text
I don't know you. Please reach out to mason-bryant if you need something.
```

The names are the entries in your mapping, said the way the mapping files them
rather than as Slack mentions — so telling one person who to ask does not notify
everybody in it. It is said in the two places the app is spoken to at all: a
thread this sink opened, and a message that @-mentions the app anywhere it can
see.

**Once per thread, and no more.** The next thing the same person says in that
thread is written to the sink's own log — with what they said, so you can see
who wanted what — and answered with nothing. An app that can be made to talk by
repeating yourself is one you would turn off, and this channel is a report on
your work rather than a conversation with the workspace.

Three things follow from that, worth knowing before somebody asks you about
them:

- **The mark, not the sentence, is what a second attempt gets.** Nothing is
  posted, so a person watching the channel sees one refusal in a thread however
  many times it was tried.
- **It survives a restart.** The thread is remembered in the sink's own state
  beside the cursors, so a sink restarted overnight does not greet the same
  person again in the morning.
- **A mapping that names nobody says this to nobody.** There is no boundary to
  be outside of, and nobody to name as a contact, so a workspace that has not
  filled the mapping in behaves exactly as it did before: every mention is
  answered, and a reply that would steer is refused for the grant it is missing.

If a colleague is getting this and should not be, they need an entry in
`operators` — a `slack_member_id` and nothing else is enough to be recognized,
and granting them `direct-work` is the separate decision that lets them steer.

## Coming back from a long gap

A sink that was off overnight comes back to everything the harness recorded while
it was down. Two things shape how that reaches the channel.

**It is posted at the rate Slack sustains** — roughly one message a second. Slack
does not delay an application that posts faster than it tolerates: it suppresses
the overflow, tells the application `due to a high volume of activity, we are not
displaying some messages sent by this application`, and those messages are hidden
for good rather than late. Pacing is what keeps a catch-up late instead of
invisible, and it is why a twelve-hour gap takes a few minutes to appear rather
than arriving at once.

**A deep backlog is digested per thread.** When one pass has more than about a
minute of messages in it, everything older than the last half hour is collapsed
into a single line in each item's thread — how many events accumulated, over what
span, and the reminder that the durable record holds every one of them. Three
things are never collapsed: anything in a backlog shallow enough to post in full,
anything from the last half hour, and anything **critical**, which is always said
in its own words. `yoyo status` and `yoyo reports` read the full record from the
command line whenever the digest is not enough.

## Limits worth knowing

- **The thread map is per machine.** Two people running their own harnesses
  against one shared repository would each open their own threads for the same
  work item, because neither can see the other's map. That is team coordination
  and it is not solved here: one sink per product per workspace is the supported
  shape.
- **A message that is too long is truncated**, with a marker naming the durable
  record that holds the whole of it. Nothing is ever split across a flood of
  messages to fit.
- **A deep backlog is summarized rather than replayed**, so the individual
  messages behind a digest line are in the durable records and not in the
  channel. What is recent, and anything critical, is always said in full.
- **Reporting is not an audit trail.** The durable records under the state root
  are; this is a view of them. `yoyo status`, `yoyo reports`, and `yoyo cost`
  read the same records from the command line.
- **A reply is acted on by the sink that is running.** One killed between reading
  a reply and recording it leaves nothing behind — Slack considers the message
  delivered — so a directive you sent and saw no answer to was not recorded.
  `yoyo directive list` is the check, and the reply can simply be sent again.
- **Who may steer is read when the sink starts.** Granting somebody
  `direct-work` reaches the channel when the sink is next restarted, not while it
  is running.

## When it does not work

| What you see | What it means |
| --- | --- |
| `SLACK_BOT_TOKEN is not set` | The tokens are read from this process's environment only, and the launcher in step 6 is what puts them there. Check that the store in step 5 has this product's pair. |
| `slack refused chat.postMessage: not_in_channel` | The app was never invited to the channel. `/invite @yoyodyne` in it. |
| `slack refused chat.postMessage: channel_not_found` | The channel id or name in `.yoyodyne/config.yaml` is not one this app can see. Check it against the channel's About panel. |
| `slack refused chat.postMessage: missing_scope` | The app was installed before the manifest's scopes were complete. Reinstall it from *OAuth & Permissions*. |
| `a reply could not be marked as <mark>` | The same missing scope, on a reply rather than on a thread's opener: the answer in the thread said what happened and the reaction saying where the directive stands could not go on. Reinstall from *OAuth & Permissions*. A mark that is missed is not set later — what carries the account is the thread. |
| `the reply that asked for this could not be marked as settled` | The outcome was said in the thread and tagged to whoever asked; only the mark on their own message could not be moved. Same remedy, same reason it costs nothing else. |
| `a direct conversation with <member> could not be opened` | Usually `conversations.open: missing_scope` on an app installed before the manifest asked for `im:write`, or a member id that is not in this workspace. The messages this affects are the two that report the harness itself degraded — a stale session build, and the harness having started nothing at all — and both are recorded either way; reinstall from *OAuth & Permissions* and the next one reaches them. |
| `the watch session's build <sha> is not a revision this product's repository holds` | Said once per build, and not a fault. How old a `yoyo work --watch` session is is measured by counting what has landed in the repository since its binary was built, and that only means anything where the product this sink reports on is Yoyodyne's own source. For any other product the comparison is not this sink's to make, so it says so once and stays quiet. |
| `the status mark on <item> could not be set` | Usually `reactions.add: missing_scope` — an app installed before the manifest asked for `reactions:write`. Reinstall it from *OAuth & Permissions* and the marks appear on the next pass, without the items having to move again. The messages are unaffected either way, and this is said once rather than every pass. |
| `Your manifest has Socket Mode enabled, which requires additional setup` | Slack cannot mint the app-level token until the app exists. Create the app, then generate that token under *Basic Information* and turn Socket Mode on if it is still off. |
| `slack refused apps.connections.open: invalid_auth` | The app-level token is missing, wrong, or lacks `connections:write`. Generate a new one on *Basic Information*. |
| `Slack will keep refusing this until somebody changes something in the workspace` | One of the four above. It is said once and then retried quietly, so fix it and watch for the line that says messages are being accepted again. |
| `another Slack sink is already running for this product` | You started a second one. The first is still reporting; nothing was lost. |
| Slack says it is `not displaying some messages sent by this application` | Slack suppressed messages for volume, and suppressed ones are hidden rather than delayed. The sink paces itself below that threshold, so seeing this means something else is posting as the same app into the same channel — a second sink, or another integration sharing the app. What was suppressed is still in the durable records. |
| `slack reporting is not enabled` | The project has not opted in. Set `slack.enabled` and `slack.channel`. |
| `replies in these threads are acknowledged and not acted on` | Said once when the sink starts: nobody in this project holds `direct-work` with a bound `slack_member_id`, so no reply steers anything. Step 4 is where that is written. |
| A reply is answered `the reply is from somebody this project has not granted direct-work` | Your member id is not bound to a human with that grant, or is bound to a different one. Your profile → *Copy member ID*, and check it against `operators` in `.yoyodyne/config.yaml`. |
| A message is answered `I don't know you` | Your member id is bound to nobody in the `operators` mapping. An entry with a `slack_member_id` and no grants is enough to be recognized; `direct-work` is the separate grant that lets you steer. |
| A reply gets no answer at all | It was not in a thread this sink opened, or it was not a reply — a message at the top of the channel addresses no work item. Reply inside the item's thread. It is also what a second message gets from somebody this project does not know: they are told once per thread, and read after that. |
| Nothing is posted at all | Nothing has happened since reporting on this product began that it had not already said. Run something; work that finished before that moment is deliberately not replayed, and the first pass prints which moment it is. |

Every row above is something you saw. What a stopped, stale, or misdirected sink
gives you is silence, so those are asked for rather than watched for:

```sh
yoyo doctor
```

| What it says | What it means |
| --- | --- |
| `this project's Slack secrets are not stored` | No pair under this product's names. The remedy is the two `security add-generic-password` lines, filled in for you. A generic pair, or a sibling project's, does not count and deliberately does not pass. |
| `no sink is running for this product, so nothing is being reported` | Nobody holds this product's lease. If a sink recorded itself here before, the line names which build it was and when it started, because a sink that died and a quiet week are otherwise the same silence. |
| `the running sink is an older build than the installed one` | The binary moved and the process did not. It is still posting what its own build knew how to post and dropping everything added since. The remedy stops it by the pid it recorded and starts the right one. |
| `the running sink holds <other>'s secrets, not <this>'s` | It was launched from a shell carrying another project's pair, and is posting this project's work through that project's Slack app. The workspace it actually authenticated into is named beside it. |
| `the running sink was started from a shell rather than from this project's launcher` | It may well be right; nothing recorded whose tokens it holds, so nothing can say. Restart it through the launcher in step 6. |

## Where the tokens must not go

Never put either token in `.yoyodyne/config.yaml`, in a work item, in a prompt,
or in a shell profile that every process on your machine inherits. The sink is a
separate process precisely so that the credential boundary is structural: the
harness posts, and agents have no path to a token because no run process ever
has one in its environment. Exporting the tokens globally would hand them to
every subprocess the harness starts, which is the one thing this arrangement
exists to prevent.

Steps 5 and 6 above are where they should go instead: a store only the sink's own
launch reads, under names that carry the product. The launcher form matters as
much as the store — the assignments are on the `exec` line, and the environment
file is sourced inside a subshell, so the tokens exist in the sink's environment
and never in the shell you started it from. Your shells stay clean, runs stay
clean, and exactly one process ever sees the credentials.

The plain `export SLACK_BOT_TOKEN=…` form works and is the wrong thing to leave
running. What it costs is not only the exposure: everything started from that
shell inherits the pair, so the second harness on the same machine gets whichever
project's tokens that shell happened to have, and posts one project's work into
another project's channel while looking entirely healthy.

Nothing else on your machine ever reads these secrets. `yoyo doctor` asks whether
they are *stored*, in the form that answers without producing the value — the
keychain is queried for the item rather than for its password — because a
diagnostic that helpfully printed a token would put it in a terminal, a
scrollback, and whatever collects them.
