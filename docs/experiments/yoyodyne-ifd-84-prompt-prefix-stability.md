# Experiment: does a stable prompt prefix make provider caching pay?

Work item: yoyodyne-ifd.84, lever 5 of the token-efficiency brainstorm.
Audit performed 2026-08-23 against the tree at that date.

**Status: the change is landed; the instrument is landed; the measurement is
outstanding and is an operator's to run.** A developer run cannot reach a
provider, so no developer run produces the before/after window this is decided
on. What is below is the audit that decided the change, the change itself, the
command that now reports the criterion, and exactly what would tell the operator
to keep it or revert it.

The criterion had no tooling behind it until yoyodyne-ifd.171, which is why this
sat undecided: a null result was supposed to trigger a revert and nothing
computed the number, so the experiment concluded neither way by default.
`yoyo cost` now reports the cache-read share, so the decision below is a command
away rather than a script somebody has to write first.

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

**The decision is not yet taken.** Nothing here has been measured: this document
records that the instrument exists and what to run, and the keep-or-revert call
belongs to whoever first has a before/after window of real runs either side of
2026-08-23. Until then the change stays, on the standing terms below.

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
