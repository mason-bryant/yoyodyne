# Experiment: does a stable prompt prefix make provider caching pay?

Work item: yoyodyne-ifd.84, lever 5 of the token-efficiency brainstorm.
Audit performed 2026-08-23 against the tree at that date.

**Status: the change is landed, the instrument is landed, the measurement has
been taken, and it is a null result. By this document's own clause the change is
to be reverted.** The numbers, the windows they were taken over, and what they
do and do not establish are in [The measurement](#the-measurement) below.

The criterion had no tooling behind it until yoyodyne-ifd.171, which is why this
sat undecided for four days: a null result was supposed to trigger a revert and
nothing computed the number, so the experiment concluded neither way by default.
`yoyo cost` now reports the cache-read share, and the measurement below was taken
against the runs already recorded — which is the point worth keeping. Nothing new
had to be run. The evidence had been accumulating in the event logs since the day
the change landed, and what was missing was only something that would read it.

## What the lever is

A provider charges the cheaper cached rate for a request prefix it has already
seen byte for byte. Everything a run sends before the first byte that varies is
eligible; everything after it is not. So the question the audit asks of each
assembled prompt is only this: how many leading bytes are identical between two
runs of different work items, and is anything volatile sitting in front of
something stable that could have been shared?

## The audit

Four places in the harness invoke a provider, and each assembles its own prompt.

| Site | System prompt | User prompt, in order |
|---|---|---|
| `internal/orchestrator/pipeline.go` — developer | none | harness contract, configured persona, architectural invariants, work-item context bundle |
| `internal/review/reviewer.go` — reviewer | review contract, reviewer persona | architectural invariants, untrusted evidence: context, patch, check results |
| `internal/chat/chat.go` — conversation turn | role contract, admission clause, role persona | product briefing (first turn only), harness activity, proposals, reports, tracker results, operator message |
| `internal/cli/exchange.go` — exchange answer | answering contract, role persona | the question and its earlier rounds |

Three findings, and only one of them was worth changing.

**The contracts and personas are already first everywhere.** That is not luck —
it is the same ordering the trust model already demanded, because a persona must
never be able to weaken the contract above it. Prefix stability wanted the same
order for a different reason, so nothing needed moving.

**The volatile evidence is already last everywhere.** The product briefing puts
its stable framing and the specifications ahead of tracker state and the triage
docket, which are the parts carrying run identifiers and timestamps. The work
item bundle, the patch, and the check results are the last thing in every prompt
that has them. Nothing here needed moving either.

**The invariant section put a per-item count in front of the constraints.**
`invariant.Delivery.Text()` rendered `"2 of the 3 active invariant(s) recorded
here were selected as relevant"` between its framing paragraph and the
invariants themselves. That sentence changes with the work item, so every byte
behind it was off the shared prefix — including the repository-wide invariants,
which are identical in every prompt this repository assembles, are already
sorted first by `Select`, and are the largest block of genuinely shared text
outside the contracts.

## What changed

The count moved below the set it describes, beside the omissions and the
unreadable files that were already rendered there. Nothing was removed: every
delivery still states how much of the repository's set it is, and still says the
set is not the whole set. The framing paragraph plus every repository-wide
invariant is now byte-identical across work items.

Three tests hold it, and each of them also holds the guard — that a prefix made
stable has not quietly become a prompt that stopped carrying the work:

- `internal/invariant`: `TestDeliveredTextKeepsWhatAppliesEverywhereOnAByteStablePrefix`
- `internal/orchestrator`: `TestDeveloperPromptsShareAStablePrefixAndStillCarryWhatIsDynamic`
- `internal/review`: `TestReviewEvidenceKeepsTheSharedConstraintsAheadOfWhatVaries`

## How to tell whether it worked

**Effect.** Every provider invocation ends in a `run_completed` or `run_failed`
event whose payload carries the provider's own `usage` object, and that object
carries `cache_read_input_tokens` and `cache_creation_input_tokens`. The event
logs are at `state/products/<product>/runs/<run-id>.events.jsonl`. The measure is
the cache-read share of input tokens — `cache_read / (input + cache_read +
cache_creation)` — summed over a window of runs. It must rise after this change.

`yoyo cost` reports it, in the `cached` column of the ledger and per run under
`yoyo cost <item>`, from those same event logs:

```sh
./bin/yoyo cost                  # the cached column per item, and the share over everything
./bin/yoyo cost yoyodyne-ifd.84  # per run, with the cached, fresh, and cache-write split
./bin/yoyo cost --json           # tokens on each run and each item, for a window taken by hand
```

The runs are in identifier order rather than in date order, so a before/after
window is cut on each run's `started_at` from the JSON rather than by reading
two ends of the table. Invocations whose terminal carried no usage object are
counted apart and named under the table; where a window is mostly those, it
cannot answer, and the report says so rather than reporting a share of nought.

**Harm.** Compare a window of roughly 20 runs before and after on first-pass
approval rate, repair attempts per landed item, and cost per landed item, all of
which are already in the run records. The specific harm to watch for is
staleness: the completeness statement now arrives after the invariants rather
than before them, so a role could read a selected set as the whole set. That
would surface as review findings citing repository-state mismatches that were
not real.

**Null result.** If the cache-read share does not rise, the change failed at its
purpose and is reverted. It buys nothing else — the prose reads no better either
way — so there is no second reason to keep it.

## The measurement

Taken 2026-08-27 against the recorded run event logs, on the yoyodyne product.
The boundary is exact rather than a date: `run-bd535e5ee0027b61fc5b190053699e0b`
is the run that made this change, and it promoted `ee9d8841` onto `main` at
`2026-08-23T10:41:35Z`. Every run started before that assembled its prompts
without the reordering; every run started after it assembled them with it.

| Window | Runs | Priced invocations | cache_read | + cache_creation | + fresh input | **Cache-read share** |
|---|---|---|---|---|---|---|
| Before — started 2026-08-23 00:00–09:59Z | 26 | 119 | 464,562,608 | 12,424,485 | 23,924 | **97.39%** |
| After — started 2026-08-24 00:00–23:59Z | 31 | 137 | 613,259,701 | 19,998,749 | 16,407 | **96.84%** |

Both windows clear the twenty runs the harm comparison asks for. There is no
gap to fill between them: no run was started on 2026-08-21 or 2026-08-22, and
the runs of 2026-08-23 after the promotion are left out rather than split,
because a run that starts in the same hour a promotion lands is a run nobody can
say which prompt it was given.

**The share did not rise. It fell, by 0.55 points.** By the clause above that is
a null result, the change failed at its purpose, and it is to be reverted. The
revert is a separate change and is named as work rather than made here.

### Why the number is what it is, and what it does not establish

The fall is not evidence that the change did harm, and the reader should not take
it as such. It is what a measure looks like when it cannot resolve what it was
pointed at.

Almost all of the cache-read in both windows is a developer session re-reading
its own conversation on every turn. A single developer invocation in these
windows reads between three and forty-seven million cached tokens; the entire
block of text this change moved onto the shared prefix is a few thousand. The
lever is four orders of magnitude below the noise, and the difference between the
two windows is the mix of session lengths in each — the after window wrote
proportionally more cache (3.26% of input against 2.67%), which is what a window
with more, shorter sessions looks like.

Beside that there is a structural finding the aggregate hides, and it is the one
worth keeping. The short one-shot invocations — the reviewer's, which is the case
the shared cross-run prefix was supposed to pay for — report
`cache_read_input_tokens` of exactly **0** almost without exception, in both
windows, while writing 10,000 to 160,000 tokens of fresh cache each time. Those
invocations are not reading a shared prefix at all; each one pays to write its
own. Whatever this change did to prefix stability, nothing downstream is
converting it into a cache read, so the lever cannot pay until that is
understood.

So the honest reading is two things at once: the criterion as written is met and
says revert, and the criterion as written could never have said anything else,
because it was specified at a resolution the effect could not reach. Both belong
in the record. The second is the precedent — a measurement-based revert clause
has to be specified so that the measure can resolve the effect, and stating the
metric is not the same as establishing that it discriminates.

## What the audit found and did not act on

Named here rather than queued; admitting them is the product manager's.

- **The developer contract travels as a user message, not a system prompt.** The
  developer is the one role whose contract is sent on stdin rather than through
  `--append-system-prompt`, so roughly 11KB of text that is identical in every
  run sits in the least cacheable position available to it. Moving it is a real
  lever and a real change to the trust boundary — a system prompt and a user
  message are not the same thing to a provider — so it is a decision rather than
  a reordering, and it was left alone.
- **The three repair prompts drop the configured persona.** A repair attempt
  resumes the developer's session, so the persona is already in the conversation
  and the omission costs nothing in the ordinary case. It is a real divergence
  where the provider fails to restore the session, which the harness explicitly
  allows for.
- **An exchange answer leads with its exchange identifier.** `renderQuestion`
  writes the exchange id and round number into the second line of the prompt.
  There is no stable block behind it to protect, so reordering would buy
  nothing.
