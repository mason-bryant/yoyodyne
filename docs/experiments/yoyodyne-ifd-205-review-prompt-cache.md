# Why a review read none of its own prefix, and what was changed

Work item: yoyodyne-ifd.205, from the structural finding
[yoyodyne-ifd.84](yoyodyne-ifd-84-prompt-prefix-stability.md) left behind.

**Status: the cause is established from the provider's own prompt assembly, the
change is landed, and the before window is below. The after window is not: it
has to be taken from runs made with the change, and there are none yet.**

## What was wrong

Every review the harness has ever made paid the cache-write rate for text
identical across every review, and read none of it back. Over every run recorded
on the yoyodyne product to 2026-08-30:

| Phase | Priced invocations | Cached | Written to the cache | Fresh | Cache-read share |
|---|---|---|---|---|---|
| Development | 296 | 3,552,875,622 | 55,852,094 | 270,327 | **98.44%** |
| Review | 534 | 121,343 | 24,220,803 | 1,064 | **0.50%** |
| Repair | 252 | 1,377,110,834 | 61,110,440 | 9,638 | **95.75%** |

Nothing was unmeasured: all 1,082 priced invocations carried a usage object.

The aggregate over the three is 97.2%, which is why nobody saw this. A developer
session re-reads its own conversation on every turn and reads billions of tokens
from the cache; a review is one short invocation reading tens of thousands. The
review is four orders of magnitude below the developer in the sum, so it can read
nothing at all and leave the total at ninety-seven per cent.

## Why

Claude Code assembles its own system prompt above whatever `--append-system-prompt`
adds, and the cache breakpoint sits at the end of the whole block. One of the
sections it assembles states the working directory and whether that directory is
a git worktree. A review runs in the developer's worktree, whose path carries the
work item and a per-run suffix, so that section differs for every review — and
the review contract, the reviewer persona, and every other byte identical across
reviews sit behind it, off the shared prefix. The first byte that varies ends the
prefix, so a single unique line at the top costs the whole block.

That is the same class of defect ifd.84 fixed inside the invariant delivery, one
layer further out: there, a per-item count sat in front of the repository-wide
invariants; here, a per-run directory sits in front of everything.

The developer never had this problem despite running in the same worktree,
because it is not reading a cross-run prefix at all. It resumes its own session
and reads its own conversation, which is the same bytes it wrote a turn ago.

## What about the cache lifetime

The work item asked whether reviews are simply spaced beyond the cache's time to
live. They are not, and the recorded evidence settles it without a new run.

The provider reports its cache writes split by lifetime, and the harness records
the usage object verbatim, so the split is already in the event logs. Every cache
write the harness has made — developer, reviewer, conversation — is
`ephemeral_1h_input_tokens`, with `ephemeral_5m_input_tokens` at nought. Reviews
already write at the one-hour lifetime.

Against that, the gaps between consecutive review invocations, over the 33
reviews whose terminals name their own role: median 548s, quartiles 356s and
1,226s, longest 15,943s. Twenty-nine of the thirty-two gaps are under an hour.
At the five-minute lifetime the spacing would have been a second, independent
defeater — twenty-seven of thirty-two gaps exceed it — which is worth writing
down, because it is what would break this again if the lifetime ever changed.
At the lifetime actually in use, it is not in play.

Nothing here should be read as an argument for forcing the one-hour lifetime
somewhere it is not already on. It applies to the whole prompt rather than to the
prefix alone, and a review's prompt is mostly the patch it is judging, which is
unique and will never be read back: forcing it would double the write premium on
exactly the tokens that can never repay it.

## What changed

Read-only roles are invoked with `--exclude-dynamic-system-prompt-sections`,
which moves the working directory and the other per-machine sections out of the
cached system prompt and into the first user message. They still reach the role;
they no longer decide the cache key. The flag is in
`internal/backend/claudecode/backend.go` beside `--safe-mode`, which is the
branch every role that is not the developer takes.

It is deliberately not given to the developer. That role already reads 98% of its
input from the cache, so what the flag would buy is one turn — and what it would
cost is a change to what a tool-using agent is told about the directory it is
editing, which is a decision about that role rather than about what it is
charged.

### That the sections are moved and not dropped

The claim above decides the harm analysis below, so it is evidence rather than a
reading of the flag's name. Three things say it, and the third is the one that
settles it:

- `claude --help`: "Move per-machine sections (cwd, env info, memory paths, git
  status) from the system prompt into the first user message. Improves cross-user
  prompt-cache reuse. Only applies with the default system prompt (ignored with
  `--system-prompt`)."
- The same option in the CLI's own SDK schema: "omit per-user dynamic sections
  (working directory, auto-memory path) from the cached system prompt and
  re-inject them as the first user message."
- The shipped code of Claude Code 2.1.226. With the flag set, the system-prompt
  builder swaps its environment section for one carrying no working directory and
  omits the memory and scratchpad sections; the same content is rebuilt into a
  record keyed by each section's heading, and the function that calls the model
  prepends that record to the messages as a single user message wrapped in a
  `<system-reminder>` block. Nothing on that path discards it.

The last of the three also names the precondition: the flag modifies the
provider's default system prompt and does nothing where a caller replaces that
prompt outright. This backend appends to it with `--append-system-prompt`, which
is why the flag applies here, and
`TestAOneShotInvocationSharesItsPrefixAndTheDevelopersDoesNot` asserts that it
still does — a later change from appending to replacing would otherwise leave the
flag in the arguments looking as though it worked.

### What holds it

`internal/backend/claudecode.TestAOneShotInvocationSharesItsPrefixAndTheDevelopersDoesNot`
holds the flag onto the read-only roles, off the developer, and beside an
appended rather than replaced system prompt. That test drives a fake process,
so on its own it cannot tell a correct flag from a misspelled one.

`TestTheInstalledCLIKnowsEveryFlagThisBackendPasses` is what can. It reads the
installed CLI's own `--help`, collects the options it lists, and asserts every
long flag this backend passes for every role it serves is one of them. An unknown
option makes Claude Code refuse the whole invocation before it reaches the
provider, so a flag misspelled here, or right but newer than the installed CLI,
would fail every read-only role at once — the reviewer, the product manager, the
architect, and the development manager — and the first evidence would be a day of
runs that cannot be reviewed. The check costs nothing: `--help` makes no provider
call and needs no account, so it is gated on the CLI being installed rather than
on opting in.

Taken against Claude Code 2.1.226 it passes for all five roles. Changing the
constant to the singular `--exclude-dynamic-system-prompt-section` fails it on
each of the four read-only roles, which is what says the check discriminates
rather than merely passing. The CLI agrees from the other side: invoked with the
singular spelling it exits with `error: unknown option
'--exclude-dynamic-system-prompt-section'` and makes no request, and invoked with
the real flag it reaches the provider exactly as it does without it.

The opt-in `TestLocalConformance` now makes a read-only invocation as well as a
developer one, so the same flag is exercised end to end wherever an account and a
network are available to run it.

## The instrument

The table above could not be produced before this work item. `yoyo cost` reported
one cache-read share per run and per item, deliberately not split by phase, on
the reasoning that every phase assembles its prompt the same way. That reasoning
is what this finding disproves, and it is the same failure ifd.84 concluded on: a
measure specified at a resolution its own effect cannot reach.

Token usage now lands in the phase its money landed in, and `yoyo cost` reports a
cache-read share per phase beside the aggregate:

```text
cached is the cache-read share of input tokens: 4930107799 of 5071572165 input token(s) over 1082 priced invocation(s)
cache-read share by phase: development 98.4% over 296 invocation(s), review 0.5% over 534 invocation(s), repair 95.8% over 252 invocation(s);
```

The same line is under each item and each run, so a before-and-after window is
cut on runs exactly as ifd.84's was.

## How to tell whether it worked

**Effect.** Take the review phase's cache-read share over a window of runs
started after this change, against the 0.50% above. The boundary is the
promotion that lands this work item; every review before it was assembled with
the working directory in its cached prefix and every review after it was not.

```sh
./bin/yoyo cost                  # the per-phase line under the ledger
./bin/yoyo cost --json           # per run, for a window cut by started_at
```

It must rise. What it will not reach is the developer's 98%, and it should not be
read against that: most of a review's prompt is the patch it is judging, which is
unique to that review and can never be read back. What is on the shared prefix is
the harness's contract, the persona, and Claude Code's own static sections, and
the share this can reach is that over a whole review prompt.

**Harm.** The per-machine sections now arrive in the first user message rather
than in the system prompt, inside a `<system-reminder>` block the provider writes
and ahead of the evidence the harness sends. A read-only role has no tools and
cannot act on a working directory either way, so what to watch for is not a
behaviour change but an evidence one: the reviewer's contract tells it the
supplied invariants, context, patch, and check results are the only evidence it
has, and a block arriving in front of them is now the first thing it reads. It is
the provider's own text rather than the developer's, so it is not untrusted
evidence — but a review that mistook it for part of the change would show up as
findings about the harness's own worktree paths. Compare first-pass approval rate
and findings per review across the same window boundary.

**Null result.** If the review phase's share does not rise, the flag is not
reaching the prefix and the change buys nothing; it is reverted, and what is
worth keeping from it is the instrument. Unlike ifd.84's, this criterion is
specified at a resolution the effect can reach: the measure is over the review
invocations alone, which are the only invocations the change touches.

## What was found and not acted on

Named here rather than queued; admitting them is the product manager's.

- **The developer contract still travels as a user message.** ifd.84 named this
  and it is still true: roughly 11KB identical in every run, sent on stdin rather
  than through `--append-system-prompt`, in the least cacheable position
  available to it. It is a change to the trust boundary rather than a
  reordering, which is why neither work item made it.
- **The developer's own first turn is off the shared prefix for the reason the
  review was.** The same flag would put it back on. The gain is one turn of a
  session that reads 98% of its input from the cache, and the cost is a change to
  what a tool-using role is told about its worktree, so it is a decision rather
  than an obvious extension of this one.
