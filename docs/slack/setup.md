# Reporting into Slack

Work the harness runs on its own is acceptable only while it is visible, and
today "visible" means a terminal somebody is sitting at. This is the same
account of the work, in a Slack workspace: **one thread per work item, one
message per milestone**, each posted under the name of the role it belongs to,
and every report an agent files carried through at the severity it was filed
under.

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

The manifest says what each scope is for. The two that read messages —
`channels:history` and `groups:history` — are there so the thread replies the
inbound half will read arrive on a connection that already exists rather than
needing a reinstall later. Nothing reads them today, and an operator who would
rather not grant them yet can delete those two scopes and the two
`message.*` events beside them; everything in this document still works.

## 2. Install it and take the two tokens

1. **Install to workspace** on the app's *Basic Information* page, and approve
   the scopes.
2. *OAuth & Permissions* → copy the **Bot User OAuth Token**. It starts `xoxb-`.
3. *Basic Information* → **App-Level Tokens** → **Generate Token and Scopes**.
   Give it any name, add the `connections:write` scope, and generate it. It
   starts `xapp-`. This is the token that opens the Socket Mode connection;
   without it the app has no way to reach your machine.

Keep both somewhere your shell can read them and nothing else can. They are
credentials for posting into your workspace.

## 3. Invite the app to the channel

Create or pick the channel the threads should be opened in, and invite the app
to it:

```
/invite @yoyodyne
```

An app that has not been invited is refused by Slack with `not_in_channel`,
which the sink reports once and does not retry — it is a thing you fix rather
than a thing that clears up.

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

There is a third setting, `operators`, and it does nothing yet:

```yaml
slack:
  enabled: true
  channel: C0123456789
  operators:
    - U01234567   # your Slack user id, from your profile → "Copy member ID"
```

It is the allow-list of people whose thread replies the harness will act on once
the inbound half exists. It is empty by default, and it lives in the
configuration rather than in the environment because a user id is identity
rather than a secret. Adding yourself now changes nothing today.

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

**It reports what happens from when it starts.** A product with two hundred runs
behind it does not get two hundred threads on the day somebody turns reporting
on: work that was already over is left in the records, where `yoyo status` and
`yoyo reports` read it. Work still in flight is caught up on in full, and a run
the sink has already said something about is followed to its end however long
that takes — which is what makes an outage a delay rather than a gap.

**Do not run two.** One sink per product: two of them hold separate thread maps,
so the second opens its own threads and posts everything twice. The second to
start is refused with a message saying so, and the refusal clears by itself when
the first one exits.

## What it posts

Into the work item's thread, as they happen:

- the run starting, **carrying the reason that work item was selected**
- the checks passing or failing
- the reviewer's verdict
- the promotion onto the target branch
- the pull request, a merge the forge queued, and the merge itself
- the run waiting — an exhausted usage limit, an overloaded provider, an
  operator hold, an unresolved directive — and the run carrying on afterwards
- the run finishing, or the blocker that stopped it
- every report an agent filed against that item, as the agent wrote it

At the top level of the channel, unthreaded, goes what is about the whole line
rather than any one item — a report an agent filed from a conversation, with no
work item attached.

Severity is said in words rather than only in colour: a `critical` says
"critical" and a `warning` says "warning", so a client that renders no emoji
still shows them for what they are. An ordinary fact carries no marker, because a
label on everything is a label that means nothing.

Each transition is said once. A thread is a narrative rather than an event log
scrolling sideways, so a restart does not repeat what it already said — the
milestones it has posted are recorded per run and survive the process.

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
| `slack refused apps.connections.open: invalid_auth` | The app-level token is missing, wrong, or lacks `connections:write`. Generate a new one on *Basic Information*. |
| `another Slack sink is already running for this product` | You started a second one. The first is still reporting; nothing was lost. |
| `slack reporting is not enabled` | The project has not opted in. Set `slack.enabled` and `slack.channel`. |
| Nothing is posted at all | Nothing has happened since the sink started that it had not already reported. Run something; work that finished beforehand is deliberately not replayed. |

## Where the tokens must not go

Never put either token in `.yoyodyne/config.yaml`, in a work item, in a prompt,
or in a shell profile that every process on your machine inherits. The sink is a
separate process precisely so that the credential boundary is structural: the
harness posts, and agents have no path to a token because no run process ever
has one in its environment. Exporting the tokens globally would hand them to
every subprocess the harness starts, which is the one thing this arrangement
exists to prevent.
