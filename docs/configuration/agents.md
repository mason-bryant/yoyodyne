# Configuring agents, operators, and reporting

The provider accounts agents run under, the humans this project recognizes and
what each may do, the personas that specialize a role, the sources a role may
research against, and what reaches Slack.

[The configuration index](../configuration.md) lists the other guides.

## Provider accounts

`accounts` is the provider accounts this project runs its agents under, keyed by
the alias each one is known by here. Yoyodyne runs one:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: default
```

The whole mapping is optional. A project that names none runs under the alias
`default`, every agent is assigned to it, and nothing about a single-account
project has to be written down for its runs to say what they ran under.

**An entry is a name and nothing else.** There is deliberately no key here that
selects a login: the harness invokes the provider with the credentials the
machine is already signed in with, so a `credentials` key would be configuration
nothing reads. What the alias buys is that every run record and every surface
that reports one already names the account it ran under — `yoyo status` says it,
and so does the message that opens a run's Slack thread.

**A second alias is refused, for now.** Running work across a pool of accounts is
post-v1, and it arrives here: a second entry, and a rule for which roles run on
which. Everything downstream of that — the per-agent `account`, the run record,
the listings — is already the shape it needs, so nothing recorded between now and
then has to be guessed at afterwards.

**Which roles run where is yours and it is fixed.** An agent runs under the
account its entry names, and nothing chooses at run time.

## Operators

`operators` is the humans this project recognizes. Each entry binds one person's
identifier namespaces and says what that person may do:

```yaml
operators:
  mason:
    git_email: mason@example.com
    forge_account: mason-bryant
    slack_member_id: U0123456789
    grants:
      - own-intent
      - direct-work
  jordan:
    git_email: jordan@example.com
    forge_account: jordan-q
    grants:
      - direct-work
```

The whole mapping is optional, and a project that names nobody recognizes
nobody — which is every project until it names somebody, and is closed rather
than open.

It is **top level rather than under any one surface**, because a human is known
by more than one. An act carries an identifier and never a person: a commit
carries an address, a push carries a forge account, a thread reply carries a
member id. Binding all three to one entry is what lets an authority check
resolve whichever namespace the act arrived through to the same person and then
ask what that person may do. Filing the whole thing under `slack` would have
made the Slack id the identity and the other two an afterthought.

**No new identity machinery, deliberately.** Git and Dolt authorship are the
assertion — the address on a commit is what the author says about themselves —
and the forge's push authentication is the proof, at the one boundary that is
shared. This mapping adds the join between namespaces that otherwise have
nothing to do with each other; it does not add a login.

Each key is a short name for a person, in the same shape as an agent name
(`mason`, `jordan-q`). Every field under it is optional except that at least one
namespace has to be bound: a human bound to nothing is authority attached to
nobody, since no act can arrive carrying an identifier that reaches them.

- `git_email` — the address their commits and tracker writes are authored with.
- `forge_account` — their account on the remote the project publishes to.
- `slack_member_id` — their member id in the reporting workspace, from their
  profile → "Copy member ID". It is identity rather than a secret, which is why
  it is checked in here with the rest.

Addresses and forge accounts are matched without regard to case, because they
are case-insensitive where they live; a member id is matched exactly, because it
is an opaque id the workspace issued rather than something a person types. One
identifier may be bound by one human: an identifier that resolves to two people
resolves to neither, so it is refused when the configuration loads.

`grants` is what the human may do, whichever namespace they arrive through, and
it defaults to empty. Recognizing somebody and authorizing them are two
decisions, so an entry with no grants records who a person is without giving
them anything — which is also how you take authority back without forgetting the
person.

| grant | what it is |
| --- | --- |
| `own-intent` | stating and approving what the product is for: the brief, the goals, and the non-goals. **At most one human may hold it** — several people amending goals concurrently is conflict machinery nobody has designed. |
| `direct-work` | steering work already in flight: the directives that reach a run, and the thread replies the Slack sink acts on. |

The grants are checked where the act arrives rather than where it is recorded,
which is what makes them worth stating: the point of attaching authority to a
person is that `by: operator` becomes a proven human rather than whoever ran the
command.

## Personas

A persona is a Markdown file describing how an agent works. Personas specialize
behavior; they never grant it. The harness invariants — agent authority,
worktree sandboxing, the protected paths a developer's change may not touch, the
review verdict contract, integration preconditions, and cleanup — are enforced in
Go and are not configurable, so a persona cannot weaken them:

- the developer prompt starts with the harness contract verbatim, and the
  persona follows it as subordinate guidance;
- the reviewer's system prompt starts with the immutable review contract, and
  the persona follows it; the decision vocabulary and the JSON response format
  are not negotiable, and a persona cannot authorize approving a change the
  reviewer cannot see;
- untrusted developer output is never treated as configuration, and configured
  text never replaces harness policy.

Persona rules:

- `version` is a free-form revision label recorded in the effective
  configuration, so a change of guidance is visible in diagnostics.
- `path` is relative to the project `.yoyodyne` directory, and must name a
  Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected.
- A persona is limited to 32 KiB. It is role guidance, not a document to paste
  into every prompt.

In a project `init` wrote, every persona is already a file in
`.yoyodyne/personas/`: change how the reviewer works by editing
`personas/reviewer.md`, and bump the `version` label beside it in the
configuration so the change is visible in diagnostics.

```yaml
agents:
  reviewer:
    persona:
      version: house-1            # bumped from v1 after editing the file
      path: personas/reviewer.md
```

In a project that uses `extends`, the same block is how one inherited persona is
replaced without changing anything else.

## Research sources

The product manager can have the harness find something out for it, so an idea
you bring it is evaluated against evidence rather than against what a model
remembers. **The capability is off until you name a source**, and a project that
names none has a product manager that says it could not check rather than
answering from memory as though it had.

```yaml
research:
  max_queries_per_turn: 4    # how many questions one reply may set off
  timeout: 60s               # how long one source has to answer
  sources:
    - name: web              # what the role names, and what every record cites
      command: my-search     # run with the question on standard input
      describes: public web search, no login
```

**A source is a command you wrote.** The harness runs it with the question on
standard input and reads its standard output as the evidence — nothing else is
passed, and the question is never part of a command line the shell parses. That
is the same arrangement `checks` uses, and for the same reason: what the harness
may run is a thing you write down in the file you write everything else in, so
what it can reach is exactly what you named. There is no built-in provider and no
default source, deliberately. A conversational role reaching the network is
something you turn on, not something you acquire by extending a bundle or
upgrading the executable.

**The role still has no network.** It names a question and one of these sources;
it does not choose what runs, where the command reaches, or how often. Only the
question leaves your machine, redacted with the same values every other
provider-facing path is redacted with and bounded at 512 bytes — generous for a
sentence somebody would type into a search box, and far too small to carry a
document out inside one. Which sources exist is delivered to the role with each
turn rather than written into its contract, so a source you add or remove is in
force on the next thing you say.

**What comes back is untrusted.** It is delivered framed as evidence about the
world and never as instruction, exactly as your repository documents and your
work items already are, and it is bounded at 4KB per answer with any cut
declared. A source that fails, times out, or answers with nothing produces a
finding that says so rather than silence — a role that gets silence for an answer
concludes there was nothing to find, which is the one conclusion it must never
draw from a source that broke. Every question and what it returned is printed to
you as it happens.

**The bounds are yours and the protocol has its own.** `max_queries_per_turn`
narrows how many questions one reply may ask and cannot widen it past four, which
is what the block itself permits; `timeout` is per question. Both take a harness
default when you leave them out, so naming a source is enough to have the
capability rather than something you configure twice. Zero is a choice for each —
it takes the default — and a negative number is refused. One further bound is the
harness's rather than yours: one thing you say sets off at most two rounds of
gathering, so a message cannot spend itself searching its way around a question.

What the product manager does with the evidence is an evaluation, which is
advice and nothing else: recording one admits no work, changes no document, and
approves nothing. That path, and how to read the evaluations back, is described
in [the conversation guide](../conversation.md#bringing-it-an-idea-rather-than-a-work-item).

## Reporting to Slack

`yoyo slack` reports what the harness is doing into a Slack channel: one thread
per work item, one message per milestone, and every report an agent filed at the
severity it was filed under. The project says where to report and what each
speaker looks like; nothing else about reporting is configurable here.

```yaml
slack:
  enabled: true
  channel: C0123456789   # a channel id, or a #name
```

The whole block is optional, and a project that omits it reports nothing — which
is every project until it opts in. `channel` takes a channel id or a name;
an id is worth preferring because renaming the channel does not break it.

### Avatars

Each speaker posts under its own name and picture, and the picture is the
project's to choose:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    harness: ":gear:"
    developer: ":ship-it:"                              # a custom emoji works
    reviewer: https://example.com/faces/reviewer.png
```

Keys are roles — `product-manager`, `architect`, `development-manager`,
`developer`, `reviewer` — or `harness` for what no persona did. A value is
either an **emoji shortcode**, including a custom emoji this workspace added
itself, or the **https URL of an image** Slack fetches. Both shapes need the
`chat:write.customize` scope the [app manifest](../slack/manifest.yaml) already
declares, so neither costs a reinstall.

The mapping is optional and so is every entry in it. A speaker with no entry
keeps the avatar the harness ships, so naming one persona's picture does not
blank the rest. An avatar that is neither shape is refused when the
configuration loads, whether or not reporting is switched on — Slack accepts an
unknown shortcode or an unreachable image without complaint and quietly shows
the app's own icon, so nothing downstream would ever say so.

Entries **merge across layers** rather than replacing each other, the way agents
do: a project that extends a bundle and changes the developer's picture keeps
every other one it inherited.

**Only the picture is configurable.** The name a message appears under, and
whose account it is, are not here and are not meant to be — who speaks is a
claim about who did the work, and a project that could rewrite it could
attribute a promotion to a developer. The avatar carries none of that:
everything it distinguishes is already distinguished by the name beside it and
the voice below it, so a reader whose client renders no picture loses nothing.

**Every name says which product it speaks for**, from
[`product.id`](setup.md#layout): `Development Manager (yoyodyne)`,
`Yoyodyne (yoyodyne)`, and a project that configured a second agent for a role
reads `Developer (opus) (yoyodyne)` — the product is last on every name, in the
same shape, for every speaker including the harness. It is applied by the voice
layer from the id the configuration already carries, never authored per message
and not configurable beside the avatars, because it is a fact about which
harness is talking rather than a claim about who did the work. An operator with
two products in development is running two harnesses, and where both are read in
one channel this is the only thing a message carries that tells them apart.

**Who may steer the harness from a thread is not configured here.** The
allow-list is derived from [`operators`](#operators): the humans granted
`direct-work` who have bound a `slack_member_id`, and nobody else. It is a
derivation rather than a second list because a list maintained beside those
grants is a list that disagrees with them — silently, and about authority. A
human granted `direct-work` who has bound no member id simply is not on it: they
hold the authority, and Slack is not a boundary they can reach it through.

A reply from somebody on that list is recorded as a directive against the item
whose thread it was said in, and reaches the work exactly as one typed at a
terminal does. A reply from anybody else is answered in the thread saying it was
not acted on — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](../slack/setup.md#steering-the-work-from-a-thread).

An earlier shape put this list under `slack` as `slack.operators`. It is gone,
and a file that still carries it is refused when the configuration loads, with a
message naming the entry to write instead.

**The credentials are not here and must never be.** The sink reads
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own process environment and
from nowhere else: never from this file, never from a work item, never from a
prompt. That is what keeps the boundary structural rather than behavioral — one
separate process posts, so no run process, and therefore no agent's subprocess
tree, has a Slack token in its environment at all. Exporting them in a shell
profile every process inherits would undo exactly that, so they are read from a
store only the sink's own launch looks at, under names that carry the product —
`yoyo-slack-bot.<product id>` and `yoyo-slack-app.<product id>`. The product is
in the name because a machine running more than one harness has more than one
pair, and a sink launched from a shell holding the wrong one connects,
authenticates, and posts this project's work into another project's channel.
`yoyo doctor` asks whether this project's pair is stored, and whether the sink
that is running was launched with it; [`docs/slack/setup.md`](../slack/setup.md#5-store-the-two-tokens-under-this-projects-names)
has the launcher.

Reporting is an observation and never a gate: a workspace that is down, slow, or
misconfigured changes nothing about any run. [`docs/slack/setup.md`](../slack/setup.md)
takes a workspace from nothing to live reporting, and the app manifest it asks
for is checked in beside it.
