# yoyodyne-ifd.341: the Gemini/Antigravity probe has no subject on this machine

yoyodyne-ifd.341 asks for a capability report on Gemini/Antigravity answering six
questions — non-interactive mode, event stream, session semantics, sandbox model,
refusal vocabulary, and role postures — **from observation rather than
documentation where possible**, because what a third adapter costs is not
knowable until somebody has run the thing.

**Neither provider is on this machine, and nothing here can install one.** There
is no `gemini` and no `antigravity` on `PATH`, no application bundle, no
credential home, no global npm package, no editor extension, and no
`GEMINI_*`/`GOOGLE_*`/`VERTEX_*` credential in the environment. Network egress is
refused, so the install that would create a subject is not available either. Six
questions that must be answered by running a program cannot be answered where the
program does not exist, and answering them from recall is the one thing the item
rules out.

So this is not the capability report. It is the record of why there is not one
yet, and — the part that is worth keeping — the specification the report has to
satisfy, written from this repository's own adapter code, so that whoever runs
the probe next executes it instead of deriving it again.

## What is actually here

Every line below was run in this worktree on 2026-09-07.

| Looked for | Found |
|---|---|
| `which gemini`, `which antigravity` | `not found` |
| `/Applications`, `~/Applications` | no Gemini or Antigravity bundle among 60 apps |
| `/opt/homebrew/bin`, `/usr/local/bin` | nothing matching `gemini`, `anti`, `google` |
| `npm ls -g`, `/usr/local/lib/node_modules` | `corepack` and `npm`, nothing else |
| `~/.gemini`, `~/.antigravity`, `~/Library/Application Support/{Gemini,Antigravity}*` | none exist |
| `~/.vscode/extensions`, `~/.cursor/extensions` | nothing matching |
| environment | no `GEMINI_*`, `GOOGLE_*`, `ANTIGRAV*`, or `VERTEX_*` key set |
| `curl https://registry.npmjs.org/@google/gemini-cli` | `curl: (56) CONNECT tunnel failed, response 403` |

The last row is the one that closes the door. The absence of a binary is a thing
a run could ordinarily fix; a refused egress means it cannot fetch the package,
cannot reach Google to authenticate, and could not have reached a model endpoint
even with the binary in hand. All three are needed, and none is present.

## Why a report written from memory would be worse than this one

The temptation is to write the six answers from what is generally known about
Gemini CLI and Antigravity and mark the document done. That would defeat the item
rather than discharge it.

The item exists because the product manager admitted it ahead of the adapter, on
the reasoning that admitting the adapter first would be guessing at a shape. A
report assembled from recall *is* that guess, wearing the clothes of evidence: it
would be read as the observation it claims to be, the adapter would be shaped by
it, and nothing downstream would find out until an adapter written to it met the
real stream.

The two questions most likely to be wrong are the two that decide the most. A
sandbox model is documented by its intent and observed by its holes — the
governing precedent here is Codex, whose read-only sandbox is documented as
read-only and still lets the agent read the whole machine, which is why
`docs/provider-plugins.md` refuses it the `read-only` posture. And a refusal
vocabulary is barely documented by anyone: the exact spelling of an exhausted
limit, the unit its reset time arrives in, and whether an overload is
distinguishable from a self-retry are things this project has only ever learned
by watching a provider fail.

An honest gap is cheap. A confident wrong answer about either of those is an
adapter rewritten.

## What the six questions have to establish

This half does not depend on having the binary. It is read off the contract the
harness already holds — `internal/backend/contract.go`,
`internal/backend/backend.go`, `internal/backend/registry.go`, and the one
existing adapter in `internal/backend/claudecode/` — and it is what makes each of
the six a pass-or-fail question rather than a topic.

Throughout, "Claude Code's answer" is given as the worked example, because a
second adapter's job is to produce the same effects by whatever means its own
provider offers.

### 1. Non-interactive mode

**What it decides.** Whether an adapter can exist at all. `Backend.Run`
(`internal/backend/claudecode/backend.go:278`) assembles one argv, starts one
process with no terminal attached, streams it, and gets an exit status. A
provider that only drives an interactive UI has nothing for that function to call.

**Claude Code's answer.** `-p` with the prompt, plus `--append-system-prompt`
for the role contract, a working directory, and `--model`
(`internal/backend/claudecode/backend.go:323`–`352`).

**Pass condition.** One invocation, no TTY, that accepts: a prompt, a working
directory, a system-prompt addendum distinct from the prompt, and a model
selector — and terminates on its own with a status. Each of those four is load-
bearing separately; a mode that takes a prompt but has nowhere to put a role
contract fails, because the contract is what the posture rests on.

**What a "no" costs.** The whole item. There is no adapter without this, and the
answer to the operator's cost question is "unbounded".

### 2. The event stream it emits

**What it decides.** `Capabilities.StructuredEvents`
(`internal/backend/backend.go:19`), and everything the dialect keys on. A dialect
matches on `backend.ProviderEvent` — `Type`, `Subtype`, `Text`, `Terminal`,
`Failed`, and the provider's verbatim `Payload`.

**Claude Code's answer.** `--output-format stream-json --verbose`.

**Pass condition**, and it is four things, not one:

- Line-delimited structured events on stdout while the invocation is running.
- A terminal event that is distinguishable from every non-terminal one, and
  which says whether the invocation ended badly. `Terminal` and `Failed` are
  separate fields on purpose: a successful terminal is what supersedes a usage
  limit reported mid-stream.
- Enough on the terminal to fill `RunResult`: a session id, the model actually
  served (`ResolvedModel` — a floating family alias makes the requested selector
  useless as durable evidence), a usage blob, and a cost the provider itself
  named. `CostReported` exists because "priced at nothing" and "never priced" are
  the same zero and opposite facts.
- Events emitted *continuously*, not accumulated and flushed at the end. The
  adapter's idle timeout is five minutes between events
  (`internal/backend/claudecode/backend.go:32`) and is what detects a stuck run;
  a provider that says nothing until it finishes has no liveness signal, and the
  only bound left is the four-hour total.

**What a partial "no" costs.** A provider with a terminal-only structured report
and prose in between is adaptable, at the price of losing idle detection. Say so
explicitly if that is what is observed — it changes a timeout constant, not the
design.

### 3. Session semantics

**What it decides.** `Capabilities.SessionResumption`, and the round trip
`RunResult.SessionID` → `RunRequest.SessionID` → the provider's resume flag
(`--resume` for Claude Code).

**Pass condition.** The provider names a stable session identifier in what it
emits, and accepts that identifier later to continue the same session.

**What a "no" costs, and it is less than it looks.** The invariant
`durable-state-is-provider-independent` already forbids a provider session from
being the only copy of anything, and the provider-adapters design already prices
cross-provider failover as rebuilding from the durable record rather than
resuming. So a
provider with no resumption at all is admissible: it declares
`session_resumption: false` and pays context reconstruction on every invocation.
What is *not* admissible is a session id that appears to resume and silently
starts fresh — that is worse than none, because the harness would stop rebuilding
the context the provider is no longer carrying. Probe the round trip by asserting
recall of something stated only in the first turn.

### 4. The sandbox model

**What it decides.** The `worktree-write` posture, and therefore whether the
provider can run a developer at all.

**Claude Code's answer.** Two mechanisms, and both are required. Built-in write
tools are scoped by rule — `Edit(/**)`, `Write(/**)`, checked by
`developerWriteToolIsScoped` — and shell is separately confined by an OS-level
sandbox passed as `--settings`
(`internal/backend/claudecode/backend.go:46`): `"enabled":true`,
`"failIfUnavailable":true`, `"allowUnsandboxedCommands":false`.

**Pass condition.** Writes confinable to one named directory, *and* a way to make
an unavailable sandbox fail the invocation rather than quietly proceed
unsandboxed. The second clause is the whole of `failIfUnavailable`: a
confinement that degrades to no confinement when it cannot start is not a
confinement, it is a default.

**One more thing to observe here.** Whether the provider can be pointed at a
per-account credential home, which is what `RunRequest.AccountConfigDir` carries.
An adapter with no such handle ignores it honestly, and the cost is that account
pooling for this provider collapses to whatever the machine is signed in as —
worth knowing before the endpoint-model slice prices pools that include it.

### 5. The refusal vocabulary

**What it decides.** Admission. The architect's provider-adapters design — in
flight as PR 481 and not yet in this tree — is unambiguous: *"An adapter that
cannot classify refusals cannot be admitted"* —
the wait machinery, the capacity-blocked state, and the retry taxonomy all key on
the classification.

The seven answers (`internal/backend/contract.go`) and what must be separable:

| Answer | What must be observably distinct |
|---|---|
| `served` | capacity reported, including a limit that has stopped refusing |
| `retrying` | the provider handling something itself; the attempt has *not* ended |
| `limit-reached` | an exhausted usage limit, with its reset time and the unit that time is in |
| `unavailable` | the provider's servers transiently failing; no reset time, lifts in seconds |
| `interrupted` | the attempt dying of something that judged nothing about the work |
| `model-unavailable` | this provider has not got the requested model |
| `refused` | a refusal that stands; the same request earns the same answer |

Three of these need deliberate provocation and will not appear in a happy-path
probe. `model-unavailable` is cheap — ask for a model that does not exist, and
check the answer is distinguishable from an ordinary error, because that
distinction is what makes an optional pinned model version safe.
`limit-reached` is the expensive one and the one that matters most; if it cannot
be provoked, say so and record it as the largest remaining unknown rather than
inferring it.

**The known trap.** `docs/provider-plugins.md` records that a reset time quoted
in human local time — `resets 8:30pm (America/Los_Angeles)` — is not expressible
by any of the three declarative formats. Google's surfaces are a plausible place
for exactly that spelling. If it is what Gemini does, that is the clearest case
yet for a fourth format, and it belongs in the report as a named finding.

### 6. Which role postures it can hold

**What it decides.** Everything the item was asked to state explicitly. The two
postures are in `internal/backend/registry.go`, and `PostureFor` assigns them:
the developer needs `worktree-write`; the reviewer, product manager, architect,
and development manager each need `read-only`.

**`read-only` is the hard one, and it is not "a read-only sandbox."** It requires
a provider that can refuse *every* tool, including nominally read-only ones,
because what the posture prevents is injected evidence reading unrelated local
files and sending them to a provider. Claude Code holds it with `--tools ""`
(an empty allow-list, `internal/backend/claudecode/backend.go:92`) plus the
`manual` permission mode standing behind it. Codex is the counter-example already
settled in `docs/provider-plugins.md`: its read-only sandbox stops writes and
network and still lets the agent read the machine, so `codex` is refused for a
reviewer, and the refusal names the posture rather than the role.

**The trap that is specific to this provider, and the reason to probe it
carefully.** An adapter chooses the session mode from the posture, and must never
choose a mode that *instructs*. Claude Code's plan mode injects its own workflow
into the system prompt — do not execute yet, write a plan, hand it back — and a
harness-invoked role receives that on top of a role contract saying the opposite.
A reviewer read exactly that on 2026-08-30 in
`run-fe0ad8461100ca399c4d2dee371afd53`, followed its contract, and reported the
injection; nothing but its own judgement made that the outcome
(`internal/backend/backend.go:36`–`48`).

Antigravity is an IDE-first agentic product. If its non-interactive mode carries
an agent-manager or plan-first workflow that cannot be turned off, that fails the
`read-only` posture on prompt grounds even if every tool is refused — and it is
the finding least likely to show up in anything but a transcript. Read the
system prompt the provider assembles, not the flag list.

**What the report must say.** Not "it probably can" for either posture. For each
of `read-only` and `worktree-write`: held, or not held, and the observation that
settles it. A provider that holds only `worktree-write` is the Codex shape — a
developer-only provider, which is still worth having and is a much smaller
adapter than one serving every role.

## The probe protocol

For whoever runs this next, on a machine with egress. Steps 1 and 2 are the
preconditions the item is actually blocked on; everything after is the report.

1. Install both subjects and record the exact versions. They are two different
   products and the item names both; a report covering one is half an answer.
2. Authenticate, and record the auth method and whether credentials can be held
   per-directory rather than per-machine (question 4).
3. **Non-interactive invocation.** Run a one-shot prompt with no TTY, a working
   directory, a system-prompt addendum, and a model selector. Capture argv,
   stdout, stderr, and exit status.
4. **Event stream.** Re-run step 3 with whatever structured output mode exists,
   piping stdout to a file. Keep the raw capture — it is the evidence for the
   dialect and it is what a later adapter is written against. Note the wall-clock
   gap between consecutive events (question 2, fourth clause).
5. **Session round trip.** State a fact in turn one, resume by the identifier the
   terminal named, and ask for the fact back. A resume that answers wrongly is
   the failure that matters.
6. **Sandbox.** In a scratch directory, ask the agent to write outside its
   working directory, and separately run it with the sandbox made unavailable.
   Record whether each is refused or proceeds.
7. **Refusals.** Provoke what can be provoked without spending an account:
   a nonexistent model selector, a revoked or absent credential, a killed
   connection mid-stream. Record the exact event shape for each, and record
   plainly which of the seven answers could not be provoked.
8. **Postures.** With every tool refused, confirm the agent has no tool available
   *and* read the full system prompt the provider assembled, looking for an
   injected workflow (question 6).
9. Write the report against the six headings above, ending with the explicit
   posture verdict, and land it in `docs/diagnoses`.

## What has to hold before this item can be discharged

Three conditions, and this run could satisfy none of them:

- **A subject.** Gemini CLI and Antigravity installed on the machine the probe
  runs on.
- **Credentials.** Without them the probe reaches questions 1 and 2 partially and
  question 5 not at all.
- **Egress.** The probe is a program talking to a remote model; a sandbox with no
  allowed hosts cannot run it, whatever else it is granted.

None of these is a change to this repository, which is why there is no code
change to make and why the item is not simply unfinished. The probe is an
operator-run errand on an equipped machine, or a run made under a posture that
grants installation and network — and the person who decides which is not this
run.
