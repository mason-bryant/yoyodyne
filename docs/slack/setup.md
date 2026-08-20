# Reporting into Slack

Work the harness runs on its own is acceptable only while it is visible, and
today "visible" means a terminal somebody is sitting at. This is the same
account of the work, in a Slack workspace: **one thread per work item, one
message per milestone**, and every report an agent files carried through, as the
agent wrote it, at the severity it was filed under.

Who each message is from is worth being precise about. Each persona speaks under
its own name and face: the developer talks about the change it is making, the
reviewer about the change it is judging, the development manager about the
queue. What no persona did — a promotion, a merge, the operator's own switches —
arrives from **Yoyodyne** itself, because "the harness promoted this" is a real
account of a real act and no agent performs one. What an agent actually wrote —
a report, the argument in a proposal — is carried through word for word under
that agent's name.

None of that costs a model call. Every milestone is rendered from the record by
a fixed line per role, so the channel is deterministic and no model sits between
a fact and its reporting.

It reports and it does not act. A separate process you start reads the harness's
own durable records and posts from them, so:

- Nothing waits on Slack. A workspace that is down, slow, or misconfigured
  changes nothing about any run — no wait, no failure, no parked work.
- Nothing is lost. The process catches up from its own cursors when it comes
  back, so an outage delays messages rather than dropping them. A process killed
  between posting a message and recording that it posted repeats that one
  message; the durable record is the authority and this is a view of it.
- No run and no agent ever holds a Slack token. The tokens live in this one
  process's environment and nowhere else — never in `.yoyodyne`, never in a
  prompt, never in a run.

Replies are one-way for now. The sink acknowledges what arrives in its threads
so Slack stops redelivering it, and does nothing else with it: steering the
harness from a thread is designed and not built.

Setting this up takes about five minutes and needs a Slack workspace you can
install an app into.

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
`channels:history` and `groups:history` — are there so the thread replies the
inbound half will read arrive on a connection that already exists rather than
needing a reinstall later. Nothing reads them today, and an operator who would
rather not grant them yet can delete those two scopes and the two
`message.*` events beside them; everything in this document still works.

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
harness's to make. [`docs/configuration.md`](../configuration.md#avatars) has
the whole of it.

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
a member id, and nobody else. Nothing acts on a reply yet, so adding yourself
changes nothing today — but it is the entry the inbound half will read, and the
member id lives in the configuration rather than in the environment because it
is identity rather than a secret.
[`docs/configuration.md`](../configuration.md#operators) has the rest of the
mapping, including the other grant and the namespaces you can bind.

> **Moved:** this used to be `operators` *inside* the `slack` block. It is not
> accepted there any more, and a configuration that still has it is refused when
> it loads, with a message naming the entry to write instead.

## 5. Start the sink

The two tokens go in this process's environment and nowhere else:

```sh
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_APP_TOKEN=xapp-...
yoyo slack
```

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
threads.

**Do not run two.** One sink per product: two of them hold separate thread maps,
so the second opens its own threads and posts everything twice. The second to
start is refused with a message saying so, and the refusal clears by itself when
the first one exits.

## What it posts

Each thread is headed by the item it is about — its identifier and what the item
is called, as `yoyodyne-ifd.118 — Slack thread headers carry the item's title` —
so a channel scrolls as a list of subjects rather than a list of identifiers. The
title comes from the durable record whatever opened the thread was read from, so
work the harness ran before it started recording titles is headed by the
identifier alone, and threads that are already open stay exactly as they are.

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
- the run waiting — an exhausted usage limit, an overloaded provider, an
  operator hold, an unresolved directive — and the run carrying on afterwards
- the blocker that stopped a run, if one did, said as `critical`
- every report an agent filed against that item, as the agent wrote it
- every change an agent proposed to a document it does not own, with the
  argument it made for it

At the top level of the channel, unthreaded, goes what is about the whole line
rather than any one item: the operator holding and releasing intake, the
operator holding and lifting all harness activity, proposed work you turned
down — there is no item, because nothing was created — and anything an agent
filed with no work item attached. Burying those in one item's thread would
misfile them.

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

Each transition is said once. A thread is a narrative rather than an event log
scrolling sideways, so a restart does not repeat what it already said — how far
each record has been read is written down as each message goes out and survives
the process. What can honestly happen twice is said twice: a check that fails
again, differently, after a repair attempt is its own message, and so is a run
that waits out a second usage limit.

One thing to expect is not a thread at all. **Ask exchanges are designed and not
built.** Every persona has words ready for a turn of one and for one closing,
including one closing unresolved at its round cap, but nothing produces them yet,
so no `exchange:` thread is ever opened. When that work lands it adds messages to
this channel and changes nothing about your setup.

## Limits worth knowing

- **The thread map is per machine.** Two people running their own harnesses
  against one shared repository would each open their own threads for the same
  work item, because neither can see the other's map. That is team coordination
  and it is not solved here: one sink per product per workspace is the supported
  shape.
- **A message that is too long is truncated**, with a marker naming the durable
  record that holds the whole of it. Nothing is ever split across a flood of
  messages to fit.
- **Reporting is not an audit trail.** The durable records under the state root
  are; this is a view of them. `yoyo status`, `yoyo reports`, and `yoyo cost`
  read the same records from the command line.

## When it does not work

| What you see | What it means |
| --- | --- |
| `SLACK_BOT_TOKEN is not set` | The tokens are read from this process's environment only. Export them in the shell you start `yoyo slack` from. |
| `slack refused chat.postMessage: not_in_channel` | The app was never invited to the channel. `/invite @yoyodyne` in it. |
| `slack refused chat.postMessage: channel_not_found` | The channel id or name in `.yoyodyne/config.yaml` is not one this app can see. Check it against the channel's About panel. |
| `slack refused chat.postMessage: missing_scope` | The app was installed before the manifest's scopes were complete. Reinstall it from *OAuth & Permissions*. |
| `Your manifest has Socket Mode enabled, which requires additional setup` | Slack cannot mint the app-level token until the app exists. Create the app, then generate that token under *Basic Information* and turn Socket Mode on if it is still off. |
| `slack refused apps.connections.open: invalid_auth` | The app-level token is missing, wrong, or lacks `connections:write`. Generate a new one on *Basic Information*. |
| `Slack will keep refusing this until somebody changes something in the workspace` | One of the four above. It is said once and then retried quietly, so fix it and watch for the line that says messages are being accepted again. |
| `another Slack sink is already running for this product` | You started a second one. The first is still reporting; nothing was lost. |
| `slack reporting is not enabled` | The project has not opted in. Set `slack.enabled` and `slack.channel`. |
| Nothing is posted at all | Nothing has happened since reporting on this product began that it had not already said. Run something; work that finished before that moment is deliberately not replayed, and the first pass prints which moment it is. |

## Where the tokens must not go

Never put either token in `.yoyodyne/config.yaml`, in a work item, in a prompt,
or in a shell profile that every process on your machine inherits. The sink is a
separate process precisely so that the credential boundary is structural: the
harness posts, and agents have no path to a token because no run process ever
has one in its environment. Exporting the tokens globally would hand them to
every subprocess the harness starts, which is the one thing this arrangement
exists to prevent.

Where they should go instead: somewhere only the sink's own launch reads. Two
recipes, best first.

**A keychain-backed launcher** (macOS) keeps the tokens encrypted at rest and
decrypts them into exactly one process:

```sh
# once:
security add-generic-password -s yoyo-slack-bot -a yoyo -w 'xoxb-…'
security add-generic-password -s yoyo-slack-app -a yoyo -w 'xapp-…'
```

```sh
#!/bin/sh
# ~/bin/yoyo-slack — the env assignments are on the exec line, so the tokens
# exist only in the sink's environment, never in your shell's.
SLACK_BOT_TOKEN="$(security find-generic-password -s yoyo-slack-bot -w)" \
SLACK_APP_TOKEN="$(security find-generic-password -s yoyo-slack-app -w)" \
exec yoyo slack "$@"
```

**A `chmod 600` env file sourced only at launch** is simpler and plaintext at
rest — write the two `export` lines into `~/.config/yoyo/slack.env`, then:

```sh
(set -a; . ~/.config/yoyo/slack.env; exec yoyo slack)
```

The subshell keeps them out of your interactive environment. Either way, the
property the design depends on holds: your shells stay clean, runs stay clean,
and exactly one process ever sees the credentials.
