# Configuring agents, operators, and reporting

The provider accounts the agents run under, the humans this project recognizes
and what each may do, what reaches Slack, the parts of the product that run
beside the work, the tasks a role takes on a cadence, the personas that
specialize a role, and how the conversational roles ask one another, keep their
picture current, research, and read the repository.

[The configuration index](../configuration.md) lists the other guides.

## Provider accounts

`accounts` is the provider accounts this project runs its agents under, keyed by
the alias each one is known by here. One is the ordinary case:

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

**An entry names an account and never a credential.** There is deliberately no
key here that holds a secret or a path: authentication is the provider's own and
lives on this machine, and the alias is what everything else refers to. Every run
record and every surface that reports one names the account it ran under — `yoyo
status` says it, and so does the message that opens a run's Slack thread.

**A project with one account authenticates where this machine already is**,
whatever that account is called. Nothing about a single-account project changes
because pooling exists: `claude` reads the home it always read, and the alias is
still only a name for the record.

**Under a pool, where an alias authenticates follows from the alias.** `default`
stays the machine's own home. Every other alias has a provider home of its own,
at `<state root>/accounts/<alias>`, which the harness sets that provider's home
variable to when it invokes under that account — `CLAUDE_CONFIG_DIR` for Claude
Code, `CODEX_HOME` for Codex. That is one rule, and the harness, `yoyo doctor`,
and `bin/yoyo-account` all read it the same way. It is a rule rather than a
setting because this file is versioned with the repository, and a directory
belonging to one machine has no business in it.

**An account names the provider whose authentication its home holds.** A
provider home is one provider's: an invocation pointed at another provider's home
authenticates as nobody and is refused. So an account that is not Claude Code's
says so:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to
  on-codex:
    description: the ChatGPT subscription
    provider: codex
```

`provider` is optional, and what leaving it out means depends on whether the
account has a home of its own:

- An account that authenticates **where the machine does** — a project's single
  account, and the `default` alias under a pool — serves whichever provider is
  asking, because each provider reads its own home there. This is every project
  that pools nothing, and nothing about provider-scoped accounts reaches one.
- An account with a **home of its own** under the state root is a Claude Code
  home when it says nothing, because that is what every one of them is:
  `bin/yoyo-account` makes them with `CLAUDE_CONFIG_DIR=… claude auth login`, and
  so does the login `yoyo doctor` hands back. A pool of Codex accounts states
  `provider: codex` on them.

Two providers reached by one adapter — Claude Code and a [declared
provider](../provider-plugins.md) whose `adapter` is `claude-code` — authenticate in
the same shape of home, so an account holding either serves both.

**An account that cannot sign an agent's provider in is refused before anything
is claimed.** A run is served by an account that holds its developer's provider,
and a pool that holds none for it refuses at the point the account would have
been chosen — before a work item is claimed and before a worktree is cut — naming
what each account holds. The same project is refused when its configuration is
read, so the ordinary way to meet this is an edit rather than a run. In a mixed
pool the rotation simply skips the accounts of other providers, which is what
lets one pool serve a Claude Code developer and a Codex one.

`yoyo doctor` asks each account about its own provider: a Codex account is asked
by `codex` whether it is signed in, in `CODEX_HOME`, and the login it hands back
is that provider's own.

The consequence worth knowing is at the moment you declare the second account,
not before it. A project whose single account was aliased `work` was
authenticating in this machine's home; adding a second account gives `work` a
home of its own, which nobody has signed in to yet. `yoyo doctor` reports it as
`account:work` with the exact login to run, and `bin/yoyo-account` is the other
way to settle it. Aliasing the account you are already signed in as `default`
avoids the step entirely.

### Pooling work across several accounts

A second entry pools the work:

```yaml
accounts:
  default:
    description: the account this machine was signed in with
  second:
    description: the other subscription
    pool: active
    weekly_budget_usd: 100
  spare:
    pool: reserved
```

- **`provider`** is whose authentication this account's home holds, and defaults
  as [above](#provider-accounts): the machine's own home serves whichever
  provider asks, and a home of its own is Claude Code's unless the entry says
  otherwise. An account of another provider is skipped by the rotation for an
  agent it could not sign in, rather than handed a run that would die
  unauthenticated.
- **`pool`** is `active` or `reserved`, and defaults to `active`. The active
  accounts are round-robined, one account per run; a reserved one is served from
  only when no active account can be. A mapping whose every account is reserved
  is refused, because a pool with an empty active half is one every run falls out
  of.
- **`weekly_budget_usd`** is optional. It stands an account down once **this
  product's** runs that named it have cost that much over the seven days behind
  now, read from what those runs actually cost rather than from a price table.
  The scope is worth knowing: the figure comes from this product's run records,
  so two Yoyodyne products on one machine sharing a subscription each bound it
  separately and the account can be spent to twice the stated figure. Budget for
  the product rather than for the subscription, or state the budget in only one
  of them. Leaving it out is unbudgeted on purpose: spend on that account until
  the provider's own limit stops us.

  Writing `weekly_budget_usd: 0` is not the same as leaving it out, and means
  what it reads as — nothing may be spent on this account — so it is how an
  account is stood down while it stays in the mapping and keeps its login. A
  negative budget is refused, because it says nothing the zero does not say more
  plainly.

**The rotation's cursor is the run records.** Each run already writes down the
account it spent, so the pool takes the first active alias after the one the last
run recorded. Nothing else is kept, which is why the rotation survives a crash, a
second process, and a machine that was off for a week.

The cursor is read when a run starts and written when that run's record is
reserved, and those are one step. A start holds the pool's rotation lease across
both, so runs beginning in the same moment queue for the choosing and are served
by different accounts rather than all by the same one — which is the case pooling
exists for. The lease is a file lock in the run state directory, held for the
choosing alone and dropped the moment the record exists, so a start whose process
dies leaves nothing for anybody to clear. A start that never reaches the front of
that queue within two minutes is refused rather than held forever.

The lease is only taken by a project with more than one account. A project with
one account is not rotating anything and starts exactly as it did before pooling
existed.

**A run is affined to the account it started on.** The account is chosen once,
before the work item is claimed, and recorded on the run. Every invocation that
run goes on to make — each repair attempt, the review of the change, and anything
a later process resumes — reads the alias back off the record. A run that moved
between accounts mid-flight would leave half its spend on one subscription and
half on another with nothing saying so.

**Conversations sit still while runs rotate.** A conversation belongs to its
agent and lasts for weeks, so it is held under the account that agent's entry
names, or under the first active account where it names none. An agent that moved
between accounts each turn would have no provider session left to resume.

**A per-agent `account` governs that agent's own invocations, not the runs it
serves.** Under a pool the split is by what the invocation belongs to rather than
by which role makes it: a conversation, an exchange round, and a branch review
belong to their agent and are made under the account that agent's entry names, or
under the first active account where it names none. A run belongs to the work
item, so it is served by the rotation whichever agent's entry the developer and
reviewer invocations came from.

That is deliberate rather than an omission. `yoyo init` writes `account: default`
onto every agent it generates, so honouring the per-agent entry for runs would
mean that adding a second account to a project the harness scaffolded rotated
nothing at all — pooling would read as configured and do nothing, which is the
one failure that looks exactly like success. Taking an account out of the
rotation is what `pool: reserved` is for, and standing one down is what
`weekly_budget_usd` is for.

**A pool with nothing left to spend refuses before it claims anything.** When
every account is over its weekly budget, the run is refused at the point the
account would have been chosen — before a work item is claimed and before a
worktree is cut — and the refusal names what each account has spent against what
it was budgeted. A pool holding no account for the developer's provider is
refused in the same place and reads as the different fact it is: nothing is
exhausted, and no amount of waiting makes one of those accounts able to sign this
agent in.

**Setting the second account up** is [in the
README](../../README.md#running-several-claude-accounts), and `bin/yoyo-account`
asks the questions and runs the login. `yoyo doctor` then reports each configured
alias by name — `account:second` — saying which provider's authentication it
holds, whether it is authenticated, and which half of the pool it is in.
`bin/yoyo-account` signs an account in with Claude Code; an account on another
provider is signed in with that provider's own login, which the diagnosis prints.

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
than open. `yoyo init` writes an example of it commented out, beside the
[`slack`](#reporting-to-slack) block, so a generated file shows what an entry
looks like without recognizing anybody.

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

**One grant is checked today, and it is worth being exact about which.** The
Slack sink's allow-list is derived from the `direct-work` holders who bound a
member id, so a thread reply is acted on or refused by asking this mapping who
sent it. That is a grant checked where the act arrives, and it is the only one —
a thread reply is also the only act that carries an identifier the harness can
resolve, because the workspace issued that identifier and put it on the message.

**`own-intent` is checked by nothing, and `by: operator` proves only that a
command was run.** A terminal carries no identifier at all, so `yoyo artifact
approve` records `by: operator` on the strength of whoever ran it, and so do
`yoyo amendment approve`, `yoyo directive record`, and the `--by` on `yoyo triage
override`, which is a string somebody types. Nothing in any of those records
distinguishes the operator from anything else with a shell. Read an `own-intent`
grant as this project's record of who owns intent — which is what refuses a
second holder when the configuration loads, and what a person auditing the
mapping reads — rather than as a gate an act passes through.

**What keeps an agent out of the goals is two enforcements that do not depend on
the signature.** A conversation runs with no tools at all, so the roles that
could argue for a goal cannot run a command; and a run's change is compared
against the [protected paths](artifacts.md#protected-paths-in-a-developers-change) before any
check runs and before any reviewer sees it, so an approval a developer wrote is
refused with the rest of the diff and never reaches the repository the goals are
read from. [What reaches the queue](goals.md#what-reaches-the-queue) rests on those two
rather than on who an approval says gave it, which is what
`internal/chat/admission.go` says in its own words. If either is ever loosened,
this is what was resting on them.

Making an approval name the resolved human, and refusing one from anybody who
does not hold `own-intent`, is designed and not built.
[Operator identity, designed once](../team-mode-coordination.md#operator-identity-designed-once)
is where it is specified, and what it would and would not close.

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

**`yoyo init` writes it commented out**, together with the
[`operators`](#operators) example beside it, so the generated file shows the
shape and says the capability exists rather than leaving both to be found in
[`docs/slack/setup.md`](../slack/setup.md). Deleting the leading `# ` from each
line is the whole of turning it on; `yoyo setup` does the same edit for you, and
replaces the commented example rather than writing a second block under it.

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

An instruction from somebody on that list is recorded as a directive against the
item whose thread it was said in, and reaches the work exactly as one typed at a
terminal does; a question from them is answered by the product manager in the
same thread and recorded as nothing. A reply from a human this mapping names who is not on it is
answered in the thread saying it was not acted on, naming the grant they are
missing — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](../slack/setup.md#steering-the-work-from-a-thread).

**Somebody the mapping does not name at all gets a different answer**, and gets
it once: *I don't know you. Please reach out to … if you need something*, with
the humans this mapping names filling in the gap — by the names it files them
under, rather than as Slack mentions, so telling one stranger who to ask does not
notify everybody. It is said at most once per thread — in a thread this sink
opened, or under the message that @-mentioned the app — and everything the same
person says after it in that thread is written to the sink's log and answered
with nothing, so an unknown user cannot make the app talk by repeating
themselves. A project whose mapping names nobody has drawn no boundary and has
nobody to name as a contact, so it says this to nobody.

An earlier shape put this list under `slack` as `slack.operators`. It is gone,
and a file that still carries it is refused when the configuration loads, with a
message naming the entry to write instead.

**The credentials are not here and must never be.** The sink reads
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own process environment and
from nowhere else: never from this file, never from a work item, never from a
prompt. That is what keeps the boundary structural rather than behavioral — one
separate process posts, and the harness builds every run's environment from an
allowlist rather than handing down its own, so no run process, and therefore no
agent's subprocess tree, has a Slack token in its environment at all, even on a
machine where the pair is exported in a shell profile. The Git commands the
harness runs itself are held to the same rule, and for a reason of their own —
a Git hook is a program the repository supplies and the harness executes; see
[the environment the harness's own Git and forge commands run
in](runs.md#the-environment-the-harnesss-own-git-and-forge-commands-run-in). What such an export does
still cost is the harness's own process and the sink: they are read from a
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

## Services

Slack, the dashboard, the scheduler, and the maintenance pass are parts of one
product rather than independent small tools, and `services` is where a product
declares which of them it runs. It is the
[management-and-supervision design's](../designs/management-and-supervision.md)
supervision tree written down: one supervisor per product, and these are its
children, started together by
[`yoyo start`](../operations.md#starting-the-product-and-stopping-it) and stopped
together by `yoyo stop`.

```yaml
services:
  slack:
    enabled: false
  dashboard:
    enabled: false
    port: 8765
    bind: 127.0.0.1
    allowed_hosts: []
    token: generated
  scheduler:
    enabled: true
  maintenance:
    enabled: true
```

**Every service is present whether or not a project mentions it.** The section is
the product's shape rather than a list a project appends to: a service a file
leaves out is at its harness default, and `yoyo config show --origins` names
`harness-default` for each value the file did not write. `yoyo init` writes the
whole section, live rather than commented, so a generated file shows every part
there is and the state each starts in. The four names are the four the product
has; a fifth is refused when the file loads, and the refusal names the four.

| Service | What it is | Default |
| --- | --- | --- |
| `slack` | the reporting sink, the `yoyo slack` process that holds this product's two tokens | off |
| `dashboard` | the read-only projection of the read model, served to a browser | off |
| `scheduler` | the watch loop — `yoyo work --watch` — that reads the queue and starts what is ready | on |
| `maintenance` | the periodic pass that keeps the installation converged: reconciling interrupted runs, catching the checkout up, restarting what stopped | on |

The two that are on need nothing that is not already in the file: they are the
harness's own loop and its self-maintenance, and a product started with neither
starts nothing. The two that are off each need something arranged outside it
first — Slack a workspace, an app, and two stored tokens; the dashboard a port
somebody means to open — and each is switched on by the operator who arranged
it, as reporting itself is.

**The section declares and never widens.** There is no key here for a
capability, a tool, an account, or an authority. A part started from this
section holds exactly what it holds when started by hand: the sink still reads
its tokens from the store only its own launch looks at, the dashboard still
refuses every request without its bearer token, the scheduler still passes every
gate a `yoyo work --watch` you started yourself would pass.

**`services.slack` requires reporting to be on.** A sink started for a project
whose [`slack`](#reporting-to-slack) section is off reads a stream and then
discovers it has nowhere to post, so enabling the service over reporting that is
off is refused when the file loads, naming both ways out. Whether the two tokens
are actually stored cannot be read from the file; `yoyo doctor` asks, under
`service:slack`, and a service enabled with its tokens missing is a warning
carrying the command that stores them — the same command its `slack-secrets`
finding carries, because both are one question asked of one keychain.

### The dashboard's entry

The dashboard is the one service with more to say than a switch, because it
listens. Its entry is the
[observability-and-dashboard design's](../designs/observability-and-dashboard.md#web-security-the-repositorys-web-service-conventions-established-here)
web-security conventions made configuration:

- **`port`** is the TCP port it serves on, a fixed number rather than one the
  operating system chooses, because a supervised child that came up on a
  different port after every restart is one nobody can bookmark or reach from
  another device. It must be between 1 and 65535; `8765` is the default, the
  same port the operations guide has used as its example of one worth
  bookmarking.
- **`bind`** is the address it binds, as an IP literal. `127.0.0.1` is the
  default — the IPv4 loopback address by number rather than as `localhost`,
  which on some machines resolves to the IPv6 loopback first — and a project
  that says nothing gets exactly the loopback-only behaviour the standalone
  command had. Writing an interface address instead is the opt-in that lets
  another device on your network open the page. A specific interface address is
  preferred over a wildcard, which reaches every interface the machine has, and
  neither is refused.
- **`allowed_hosts`** are the names, beyond the bound address itself, a request
  may carry as its Host and Origin. Empty is the bound address alone — with
  `localhost` beside it under the loopback default, which the standalone
  command already accepted. Each entry is a host name or an IP address written
  without a scheme, a port, or a path, because a request's Host header is
  compared against it exactly and an entry with a port in it would match
  nothing a browser sends. The list is replaced wholesale rather than merged,
  as `checks` is.
- **`token`** is where the bearer token every request has to carry comes from.
  It is a reference and never the token: this file is committed, and a secret
  in it would be a secret in the repository. `generated` is the default and the
  loopback arrangement — a token made at each start and printed once where you
  can read it. `keychain` and `file` are the two stores the Slack tokens already
  use, under names that carry the product: the keychain item
  `yoyo-dashboard.<product id>` under the account `yoyo`, or the file
  `<state root>/products/<product id>/dashboard.token`. `yoyo dashboard`
  reads the one named and serves under it, printing where it was read from
  and never the value, so a stored token outlives a restart; a store that does
  not hold it refuses to start with the command that stores it.

**A bind outside loopback with a generated token is refused when the file
loads**, with the reason. The opt-in exists so a browser on another device can
reach the page, and a token printed to this terminal is exactly what that
device cannot read, so a non-loopback bind has to name where its token is
stored before it is accepted at all. What the opt-in keeps is every other rule:
the token is still required on every request, Host and Origin are still checked
against the configured address and hosts, and there is still no write path at
any address — a wider bind widens who can read observability data and never who
can direct work. Transport is plain HTTP in V1, so the opt-in is for a network
you trust.

`yoyo doctor` reports the dashboard under `service:dashboard`: off, on with a
generated token, or on with a supplied token that it looks for in the store the
entry names — the keychain item by name, the file by its existence and mode —
without ever reading the token. A supplied token that is not there is a warning
carrying the command that stores it.

It reports Node separately, under `node`, and asks about it whether or not the
service is enabled — what it goes by is the repository carrying
`internal/dashboard/testdata/render.js`, which is what says a product ships the
dashboard at all. The page is drawn by a script only Node can run, so a machine
without Node produces none of the page's evidence and its render check fails
there: an absence is a problem carrying the install command, an absence that
`YOYODYNE_NODE_UNAVAILABLE` declares is a warning quoting the declaration, and
every product that ships no dashboard is asked nothing. Nothing in the harness
sets that variable — it is set by whoever built an environment deliberately
without Node, so a declared absence is a decision somebody made rather than a
tool nobody installed.
[Working on yoyo itself](../developing-yoyo.md#what-a-checkout-needs-besides-go)
names Node as the development dependency this is about.

**[`yoyo start`](../operations.md#starting-the-product-and-stopping-it) is what
acts on this section.** It starts the product's supervisor, which reads the
section and starts every enabled part it knows how to: the Slack sink as
`yoyo slack ensure` starts it, and the scheduler as `yoyo work --watch` under
its own watch lease. Two parts are declared here ahead of the supervisor
knowing how to start them, and `yoyo start` says so for each: the dashboard's
adoption as a child is `yoyodyne-ifd.414`, and until it lands `yoyo dashboard`
is started by hand and still binds loopback on its `--port` rather than reading
this entry's `port`, `bind`, or `allowed_hosts` — `token` it does read, whether
or not the entry is enabled, so that
[a stored token outlives a restart](../operations.md#watching-from-a-browser-the-dashboard);
the maintenance pass is the resident item, `yoyodyne-ifd.413`, and
until it lands `yoyo reconcile` is scheduled by hand. Declaring the whole
section now is what lets that command and the resident that starts with the
machine read one statement rather than two.

## Recurring tasks

Everything else the harness starts is reactive: an item is admitted, a run stops,
somebody asks. A recurring task is the other shape — a role woken every so often
to look at its own domain and deal with what it finds — and it is configuration
because what runs and how often is a project's judgement rather than a release's:

```yaml
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Sweep for unresolved issues: stoppages nobody has decided, claims on work
      nothing is running, deliveries that have stopped moving. Fix what your
      authority allows, ask the architect where a ruling is needed, and file
      root-cause work with the product manager for every fix you make.
```

**Configuration decides which role is woken, when, and on which model, and
nothing else.** There is
no key here for a capability, a tool, an account, or an authority of any kind,
and the absence is deliberate rather than an omission to be filled in later: a
role woken on a cadence holds exactly what its role already holds, resolved from
the harness's own registry the same way it is resolved for a conversation you
open by hand. A scheduled turn also reads the role's own persona, so the
personality that answers is the one the project configured and not a second
version of it. The loader is strict about keys, so a `capabilities:` or `tools:`
written under a task fails the configuration rather than being ignored.

| Key | What it says |
| --- | --- |
| `role` | which role is woken. It must be a role this project configures an agent for; a task naming a role nobody fills is refused rather than discovered as silence. |
| `every` | the cadence, measured from the last firing rather than against a wall-clock grid. The shortest accepted is `5m`, which is what keeps `1m` written where `1h` was meant from becoming sixty times the spend. |
| `enabled` | the switch. It is explicit so a task can be turned off for a week without deleting its prompt and cadence. |
| `prompt` | what the role is told. It is the task, not a personality. |
| `max_turns` | how many turns one firing may take, defaulting to 3 and capped at 10. |
| `model` | the model this task's turns ask for. Optional: leave it out and the turns ask for the role's own configured `model`, which is what every task did before the key existed. See [a task's own model](#a-tasks-own-model). |

### A task's own model

**Model spend follows the work rather than the role.** A routine pass over a
domain and a decision about one stoppage are both the development manager's
turns, in the same conversation, and only the decision needs the role's model.
A task that names a `model` has its turns ask for that one, and every turn the
task does not cover — a message you send, a docket decision, a directive, a
summons delivered some other way — asks for the role's own model as before:

```yaml
recurring_tasks:
  development-manager-sweep:
    role: development-manager
    every: 1h
    enabled: true
    max_turns: 4
    model: sonnet
    prompt: |
      ...
  report-triage:
    role: product-manager
    every: 12h
    enabled: true
    model: sonnet
    prompt: |
      ...
```

It is the operator's direction of 2026-09-19, off the same seven-day reading as
[the developer mapping](runs.md#a-developer-model-chosen-by-the-items-label): the
management roles on their own model were $317 of $1,431, and most of it was
routine sweep passes. The lines are the operator's to paste into the project's
own configuration by hand, because `.yoyodyne/` is a
[protected path](artifacts.md#protected-paths-in-a-developers-change) no run may write; a
task whose block does not carry the key runs on the role's model.

**It selects a model for the task's turns and nothing more.** The turn is taken
in the role's own conversation, under the account that role's agent names,
holding the authority the role holds and reading the persona it reads — so
configuration still selects and never widens what a role may do. The
[account pool](#pooling-work-across-several-accounts) and the
[failover rules](recovery.md#serving-a-turn-from-a-permitted-alternate-model) apply
unchanged: a pass whose model has no capacity is served by the agent's
alternate where it has enabled one. Two things follow from the model being
another one. A [pinned version](recovery.md#pinning-an-agent-to-a-model-version) is a
version of the agent's own family, so a task naming another model carries no
pin — and a task naming the agent's own model keeps it. And an alternate that
*is* the task's model, on the agent's own provider, is dropped for that pass,
because failing over to the endpoint whose window just closed is a second
refusal rather than an alternate.

**The model is validated exactly as an agent's is**, and refused when the
configuration loads with the same reason: a selector longer than the bound, one
with whitespace in it, and one that begins with `-`. Leaving the key out, or
writing it empty, is not a refusal; it is the role's model.

**Every pass records the model it ran on**, beside its turns and its cost, as a
run's record names its developer's model. It is the model that served — the
task's own, the role's where the task names none, or the alternate where a
failover answered — and [`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found)
prints it on each pass's header. [`yoyo status --spend`](../operations.md#following-a-run-a-conversation-or-a-branch-review)
reads the same records and adds a table under its totals: each task's passes,
turns, and cost, by the model they ran on. It is a split of the conversations
figure above it rather than an addition to it, since a pass is conversation
turns. A pass recorded before passes named their model is shown as
`(not recorded)`.

**A firing costs what conversation turns cost.** The cadence is therefore the
spending decision: `every: 1h` is a turn an hour for as long as a `yoyo work
--watch` session is running. At most one task fires per pull, so a schedule with
three due tasks reaches them over three pulls rather than holding the queue
closed for all three at once.

**What bounds that spend is the session's own
[`--budget`](runs.md#watching-instead-of-draining)**, which counts a firing's turns
exactly as it counts a run it started or a stopped run it delivered — so a
session given a budget stops on it rather than sweeping past it, and a session
given none is bounded by the cadence and nothing else.

**`yoyo pause` is the switch that stops firings**, exactly as it stops runs and
conversation turns. The pause is read at the start of a pass, before anything is
claimed, so a task a held pause was in place for keeps its cadence and fires once
the pause lifts. One narrow case costs a cadence rather than keeping it: a pause
placed in the moment between a task's claim and its turn arrives after the claim
has already moved the clock, so that firing is recorded as one the role could not
be reached for and the task waits for its next cadence rather than firing when
the pause lifts. It costs one pass of one task, and only for a pause that lands
inside that window. **Holding intake does not stop them.**
The hold stops the harness choosing work, and a firing chooses none: it is read
before the hold on every pull, so a task fires under a held intake exactly as it
does under a clear one. That is deliberate — a held queue is often waiting on
exactly the kind of look a sweep takes — but it is the opposite of what an
operator reaching for the hold to stop spending expects, so it is worth saying
plainly: to stop paying for a cadence, pause rather than hold.

**A heavy pass iterates rather than truncating.** A role that has more to do than
one turn holds says so in its account, and the harness gives it another turn up
to `max_turns`. A pass that still had more to do when the bound ran out is
recorded as partial, so a truncated pass and a finished one are never the same
short report.

**A reply with more than one account keeps the last.** The contract is one
sweep block per reply, and a role that answers with two — a `more` and then a
`complete`, which is the shape the slip takes — has slipped rather than failed.
The last block is recorded as the pass's account, and the record notes beside
it that more than one was sent, rather than the pass being thrown away over
the shape of its reply after its decisions were taken. A block that cannot be
read is still refused wherever it sits, and a reply with one block is recorded
exactly as before.

**A firing that failed waits for its next cadence.** It is not retried at once:
the next pass looks at everything this one would have, and retrying immediately
would spend turns against whatever was already failing. What stopped it is
recorded against the task, so a schedule that is running and producing nothing is
something you can find.

**The intake brake summons a development manager's task out of its cadence.**
The first enabled task whose role is `development-manager` is the one the
[failure-storm brake](runs.md#watching-instead-of-draining) fires the moment it trips,
whether or not the task is due: the same conversation, the same turn bound, the
same durable report, with the runs that blocked and the reason each blocked in
the message that wakes her ahead of the task's own prompt. It is a firing like
any other — it counts, it is charged to the session's `--budget`, and the
cadence runs on from it, so the hourly pass does not follow a summons a minute
later over the same ground — and `yoyo sweeps` shows it as summoned, naming
what tripped the brake. A project that schedules no such task gets no summons;
its brake is decided by the cooldown's probe rather than by her, and the hold
says so. The provider answering nobody refuses a summons exactly as it refuses
a scheduled firing, and the pause covers both.

**A development manager's pass also reads the forge.** On every firing of a
task whose role is `development-manager`, and only that role's, the harness
itself lists the open pull requests of the repository the project publishes into
and adds a finding for each one the forge is holding open for nothing: a request
whose work item is closed, and a request whose head branch is already contained
in the branch it targets. Which work is closed is read from the tracker whole
rather than from the first page of its listing, so a request superseded long ago
is reported as such and not passed over as live because its item fell past a
page. The finding names the request, the work item, and which
of the two holds, and it is `left` rather than `fixed` — the harness closes
nothing, and neither does the role on its account; the request is there for
somebody to decide about. Each request is reported once, keyed on its number,
however many passes find it still open afterwards: the requests a pass reported
are recorded on its report, and the next pass reads them back before it looks.
The reading is taken beside the role's turns rather than by the role, so it
happens whether or not the role could be reached, and a forge that could not be
read is a problem on the record rather than a lost pass. The reading is taken
under exactly the setting the harness opens requests under:
[`approvals.publishing: automatic`](publishing.md#publishing-without-automatic-integration), which is
the only value that pushes a branch or opens a pull request. Under `human`, the
other value, the harness opens no requests and reads no forge, so a request
somebody opened by hand in such a project is not noticed here.

Every firing ends in a durable report, read with
[`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found). The reports
outlive the session that produced them and are written once and never revised.

### Working the report pile on a cadence

The other standing loop worth configuring is the one that drains the
[collected reports](../reporting.md#who-reads-them-and-what-became-of-each-one).
Every role files what it noticed into one pile, the product manager is the only
role that can record what became of a report, and until something wakes it for
that the pile is worked only when you happen to open a conversation. Reports
arrive at twenty to forty-five a day in this project, which is more than that
reaches.

`yoyo init` writes this entry into the generated configuration, commented out and
beside the development manager's sweep, so a new project has it to uncomment
rather than to compose. **A project that has not uncommented it has no cadence
over the pile**,
and no part of the harness supplies one on its behalf — the schedule is where a
project says which roles are woken and how often, and a task nobody wrote is a
task that does not fire:

```yaml
recurring_tasks:
  report-triage:
    role: product-manager
    every: 1h
    enabled: true
    max_turns: 4
    prompt: |
      Work the collected reports. The unhandled ones are carried into this turn
      already, oldest first with anything critical ahead of them; decide about
      every one you are shown and record each decision with the "handle"
      action, whether that decision is work to admit, a proposal to make, a
      question to raise, or that it needs nothing. Check anything you would
      admit against the work already admitted first. Say in your pass's summary
      how many you decided and how many are still behind them, and keep the
      findings for what was worth more than a handling; a pass that has more of
      the pile to work than one turn holds says so and takes another.
```

Every decision is on the record twice, which is why the prompt does not ask for
one finding per report: the `handle` action writes what became of each report
beside the pile, and the pass's own account in `yoyo sweeps` is the summary of
the pass — bounded at twenty findings a turn, which a pass working forty reports
would otherwise spend on bookkeeping.

Nothing about that turn is special, which is the point: the same persona, the
same authority, and the same bounded delivery a conversation you open yourself
gets. What makes the loop converge is the delivery being a walk with a durable
position rather than a listing — see
[the walk](../reporting.md#who-reads-them-and-what-became-of-each-one) — so each
firing takes the next slice of the pile instead of the same worst one. Whether
it is keeping up is answered by the count and the oldest undecided report's age
that every listing of the pile now leads with.

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
- `path` is relative to the directory the configuration file is in, and must name
  a Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected. For the ordinary project that is the
  `.yoyodyne` directory, and for a configuration
  [kept outside the repository](setup.md#keeping-the-configuration-outside-the-repository)
  it is the directory `init --external` wrote, which is what lets that directory
  be moved as one. A `.yoyodyne.yaml` uses the `.yoyodyne` directory beside it,
  which is where migrating it would put the personas; and a `config.yaml` placed
  by hand somewhere of its own falls back to a `.yoyodyne` directory beside it
  when the persona is not there, so an arrangement that predates this goes on
  loading.
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

**Zero is refused**, unlike the [triage caps](recovery.md#triage-thresholds). An exchange allowed no round
at all is a channel that is off, and turning the channel off is a matter of
nobody using it rather than of configuring a limit nothing can be spent against.
One is the floor.

One further bound is the harness's rather than yours: a single thing you say to a
conversation sets off at most as many rounds of asking as one exchange is
allowed, however many exchanges it spreads them over. That bounds a reply
opening thread after thread, which is a different question from how long one
thread may run.

## How far behind a conversation's picture may fall

The product manager, the architect, and the development manager are briefed
once, when a conversation opens, and every later turn resumes a session that
already holds that briefing. Before each reply the harness counts the landings
on the target branch since the picture was taken and records the count on the
conversation; past this many it re-reads the repository and the tracker before
the turn is answered, the way [`/refresh`](../conversation.md#how-fresh-the-conversations-picture-is-and-how-to-refresh-it)
does when you ask:

```yaml
conversation:
  refresh_after_landings: 20   # landings on the target branch before a turn re-reads
```

**It is measured in landings, not hours.** A branch that took fifty commits in a
morning has moved further under a conversation than one that took none in a
week, and the number a role would have to state about its picture — what the
repository holds that the picture does not — is the count, so the count is what
the threshold is in. The picture records the commit it was taken against, and
the comparison is `git rev-list --count` from that commit to `HEAD` in the
primary checkout, whose current branch is the integration target every run is
promoted into.

**It times the re-read and does not switch it off.** Zero is refused, since a
picture allowed no landings behind is re-read on every turn, and so is anything
above 200: the case that admitted this was a picture roughly five hundred
landings old advising the operator to add a section a file had opened with for
a month, and a threshold that let one be advised from unrefreshed would be this
file disabling the statement it is only meant to time. Where the re-read cannot
be made — the tracker locked, the repository not answering — the reply carries
its picture's age in its own text, and no value here reaches that either. The
number is a judgement about your project's pace: how many landings a
conversation may reason across before what it does not know it does not know
is worth the cost of re-briefing it, which is the whole briefing carried into
the turn again.

## Queueing a question, or holding it on a side thread

A conversation takes its turns one at a time. That is what stops two processes
interleaving one transcript, and the price of it is that a busy thread queues
everything behind whatever it is doing: a question worth a minute waits out a
turn worth twenty, and the roles every other role waits on are the ones whose
threads are busiest.

An agent may therefore hold **side threads** — bounded conversations beside its
main one, each with its own stream, its own lease, and its own transcript, so a
question put to it while the main thread is busy is answered rather than queued.
It is stated in the agent's own block, beside the persona and the model:

```yaml
agents:
  architect:
    role: architect
    model: opus
    conversations: side-threads
```

`conversations` is `queue` for every agent that does not write it, which is what
every agent did before this key existed. Nothing acquires side threads by
inheriting a bundle or by upgrading the executable — which roles are worth
answering two questions at once is a judgement about the work, exactly as
[failover](recovery.md#serving-a-turn-from-a-permitted-alternate-model) is. Stating it empty
in a later layer removes an inherited choice and puts the agent back to queueing.
A value that is neither word is refused at load, naming the two that are.
`yoyo agent list` says which agents hold side threads.

**Where the choice is made.** A single message — `yoyo chat --message` for the
product manager, `yoyo agent chat <name> --message` for any agent — that finds
the agent's conversation mid-turn is the moment the knob decides. An agent that
queues has the message wait for the turn, which is what every message did before
the key existed. An agent that holds side threads has it answered beside the busy
turn instead, on a side thread of its own, and the answer says so: which thread
it came from, that it is the agent's judgment and not an action, and what the
agent tentatively committed to. Three kinds of message always reach the main
conversation whatever the knob says, because each has to: a `/command`, which the
harness carries out against the conversation; a decision or an answer, which
settles something the main conversation is waiting on; and a message with
`--new`, which replaces the conversation rather than sitting beside it. An
interactive `yoyo chat` and a message from Slack queue as they always have — a
side thread is a bounded number of turns, not a prompt to sit at. A side thread
the agent left open for a further turn is continued with
`--side-thread <id> --message`, addressed to the agent that holds it — another
agent's command naming the stream is refused before a turn is spent, so a thread
is never served on one agent's account and merged into another's memory. It takes
its turns on its own record and its own lease, so it neither waits for the main
conversation nor holds it.

**The knob selects behaviour and never authority.** A side thread judges,
answers, and tentatively plans: it reads the tracker and the evidence its role
was given, and every intent it forms is a draft. It admits no work, mutates no
item, raises no proposal or concern, commissions no research, records no
evaluation, and issues no directive — whatever the role may do on its main
thread. That list is in the harness's own code rather than in any file, there is
no configuration key that names a capability, and no value of `conversations`
reaches it. Setting this key gives an agent a second thread; it gives that thread
nothing to act with.

**What a side thread promises is best effort until the main thread confirms it.**
A side thread concludes by finishing or by spending its turn budget, and what it
reached is written into the agent's own memory — budgeted, redacted, audited,
citing the side stream rather than copying its transcript. The main thread's next
turn reads that and ratifies or adjusts whatever the side thread drafted, through
its own single-threaded path, which is the only path there is. So an answer that
promised scheduling is tentative, and the surface carrying it says so.

**The bounds are the harness's, not yours.** A side thread runs to a turn cap and
an agent holds a limited number at once; both are the harness's defaults and
neither is configurable here yet. A side turn is otherwise a provider invocation
like any other: the spending pause and your holds gate it, it is priced from what
the provider reported and listed beside the conversations in the cost surfaces,
and it is served by the agent's [permitted alternate](recovery.md#serving-a-turn-from-a-permitted-alternate-model)
and [pinned version](recovery.md#pinning-an-agent-to-a-model-version) exactly as a main turn
is.

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

## Reading the repository from a conversation

The three management roles — product manager, architect, development manager —
can have the harness read one repository path for them, or list the names one
directory holds, at a recorded commit. It is here beside research because it is
the same shape and the opposite arrangement: research is evidence from outside
the repository, run by a command you wrote and off until you name one; this is
evidence from inside it, run by the harness's own Git, and **there is nothing to
configure**. No key switches it on, none switches it off, and none moves a
bound. [The conversation guide](../conversation.md#reading-the-repository-at-a-recorded-commit)
says how a role uses it; what belongs here is why the file you are reading has
no say in it.

**Which roles hold it is the role-capability registry's, in Go.** The three
management bundles hold `repository.read` and `repository.list`; the developer's
and the reviewer's hold `repository.read` alone, which is the harness reading a
change or a context bundle on their behalf rather than a path they name. `yoyo
config show` reports both under each agent's `capabilities`, and — as with every
capability — the set is read off the role and never written: a `capabilities`
key in this file is refused like any other key that does not exist. A persona
cannot widen it either, because the block is refused where the reply is read
whatever the persona said.

**The bounds are the protocol's rather than yours**, for the reason
`max_queries_per_turn` cannot be raised past four: what is bounded is the size
of a prompt. One reply names at most six paths; one read returns at most 48 KiB
of a file and one reply's reads together at most 96 KiB, a file beyond that
being cut with the cut declared and its whole size named rather than split
across reads; a listing returns at most 400 names; one message reads at most
twice. A `research`-style block for it would be a bound a project could
configure past, which is the thing this section exists to say there is not.

**Every read is against the tree of the commit `HEAD` names at that moment**, in
the repository this configuration's `product.repository` resolves to — the
primary checkout, never a worktree — and never the working tree, so an edit you
have not committed is not what a role is shown. That is also what makes the read
confined without a check: a committed tree has no link to follow and no path
that leaves it. The content is redacted with the same values every other
provider-facing path is redacted with, and each read is recorded on the
conversation as the commit, the path, and the time.

**What the product manager is handed is labelled as description, never intent**
— the same label its [shipped documentation](artifacts.md#what-the-product-manager-sees-besides-them-and-what-it-does-not)
carries, applied on every delivery, with the same rule: where a file contradicts
a specification, the conflict is reported rather than resolved. The
specifications remain the only statement of what the product is for.
