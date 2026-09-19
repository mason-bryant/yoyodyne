# yoyodyne-ifd.141.4: review of the read-model code PR 544 merged unreviewed

PR 544 merged as `8c10fa5` with about 880 lines of read-model code no reviewer
had seen, because the review bound cut them out of `yoyodyne-ifd.141.3`'s diff
on two rounds. This is the independent review of those lines as they stand on
`main`, read whole, against the three criteria the carve stated. The files:

| File | At `8c10fa5` | Since |
|---|---|---|
| `internal/readmodel/throughput.go` | new, 327 lines | unchanged to `a4d1314` |
| `internal/readmodel/throughput_test.go` | new, 334 lines | unchanged |
| `internal/readmodel/standing.go` | +105: `RunningRun` title, backend, model, account, `Stage`; `WorkingTurn` backend, model; `Refused.Kind`; `Standing.Startable`; `StageOf`, `modelOf` | `.423` and `.427` touched other parts of the file; none of these |
| `internal/readmodel/standing_test.go` | +112 | unchanged in the reviewed parts |
| `internal/backlog/backlog.go` | +63: `HoldKind`, `Entry.HoldKind`, `hold()` behind `Hold` | unchanged |
| `internal/backlog/backlog_test.go` | +28 | unchanged |

Nothing under `internal/dashboard` or `internal/cli/dashboard.go` is covered
here; `yoyodyne-ifd.141.3` covered those.

## Verdict

**Approved as it stands, with four findings, every one of them fixed in this change.**
Nothing here derives a figure a surface would re-derive, every source that
cannot be opened or read reaches the JSON as a named problem beside the figures
of the source that could, and the standing additions are held by the dashboard's
strict-decoded fixtures as well as by the package's own tests.

## Criterion 1: the derivation reads only from the shared read model's sources

`ReadThroughput` reads two things: `Runs.Recorded()`, the run store's own
listing, and `Ledger.Spend(SpendQuery{Days: 7, Now: now})`, the one call
`reportSpend` in `internal/cli/statusstream.go` makes to price
`yoyo status --spend`. The endings are `State.Outcome()`, the word
`yoyo status` prints for each run; a `succeeded` run is landed exactly where
`Integration != nil`, which is the same test `RunSummary.Integrated` is built
from in `internal/runstate/summary.go`. The window's first day is
`runstate.LocalDay` over the same calendar-day step `oldestLocalDay` takes, and
`TestThroughputPricesTheSameRecordsTheSpendReportPrices` pins `Since` to the
spend report's `Oldest` over a real state directory. The spend rows are kept or
dropped by the report's own rule (`SpendReport.take`, reached through
`SpendReport.Since` after finding 4): a row is inside from the window's first
day on, and an undated row is in every window; and the adding up is the
report's own `Totals`, which `yoyo status --spend` prints from too.

The page-side check held from the other direction: `internal/dashboard/page_test.go`
asserts at compile time that the ledger the page is priced from is
`*runstate.StreamStore`, that a refusal's kind is `backlog.HoldKind`, and that a
run's stage is `readmodel.Stage`.

`Stage` is a new governed vocabulary, owned in `readmodel` and folded by
`StageOf`; `TestStageOfFoldsEveryPhase` covers every `runstate.Phase` there is,
and the empty phase. `HoldKind` is owned by the queue, and `hold()` is the one
derivation behind `Hold` and `HoldKind`, so a sentence and its pile cannot
come apart.

## Criterion 2: a failing source open reaches the JSON as a named problem

Four cases, all in `TestThroughputSaysWhichSourceCouldNotBeRead`: a ledger
whose `Spend` fails, a run store whose `Recorded` fails, neither wired, and a
caller that could not open one and carried the reason (`RunsProblem`,
`LedgerProblem`). Each costs only its own figures — the runs are still counted
under an unreadable ledger and the spend still summed under unreadable runs —
and the reading carries both windows with `kinds` as `[]` rather than `null`.
The cancelled-context branch before the spend read is the one path the test
does not take.

The JSON shape was held only from the dashboard package before this review,
where `throughput-degraded.json` is strict-decoded into `readmodel.Throughput`
and served back with `spend_problem` present and `runs_problem` absent. This
change adds the same assertion beside the type, in the package that owns the
tags.

## Criterion 3: the standing additions are tested against the fixtures

`TestTheFixturesAreTheReadModelsShape` decodes every `standing-*.json` fixture
with unknown fields refused, so `title`, `backend`, `model`, `account`,
`stage`, `kind`, and `startable` are the model's own names or the test fails.
In the package: `TestTheOperatorsExampleRendersFromState` reads title, backend,
resolved-over-selector model, and account off the running run, backend and
model off the working turn, and the stall and held-for-a-person kinds off the
refusals; `TestStartableIsCountedFromTheSameEntriesAsTheRefusals` holds the
startable count against the same entries the refusals come from, in-flight and
parked work left out, and zero under the operator's hold; the directive-pause
and parked-work tests each assert their kind; and
`TestHoldKindNamesThePileTheHoldSentenceDescribes` walks the precedence.

## Findings

1. **Fixed — a comment on `Window.Landed` misdescribed evidence landings.** It
   said a run that "succeeded without promoting anything" is "what an
   escalation or an evidence landing looks like in the record". An evidence
   landing integrates its change exactly as a discharge does
   (`renderOutcomeNotes` in `internal/orchestrator/pipeline.go` writes the
   integrated headline for it), so it carries an `Integration` and is counted
   under `Landed`. The derivation was right; the comment now says so, and names
   the bootstrap run as the other succeeded-without-promoting case.
2. **Fixed — `Entry.Awaits` restated the precedence `hold()` owns.** The
   `HoldKind` comment says the sentence and the pile come from one reading, but
   the head-of-line counts (`AwaitingDecision`, `AwaitingCarryOut`) were taken
   from a third copy of the executor-then-parking-then-hold rule in `Awaits`.
   A reorder in one would not have followed in the other. `Awaits` now reads
   the kind off `hold()`: held for a person exactly when the kind is
   `HeldForAPerson`.

   Which kind wins where an entry is awaiting a person and under another hold
   at once is what `hold()` already decided, and it is the same answer `Awaits`
   gave before: the executor and the parking rank ahead of the hold, so an
   entry a conversation carries or that is parked is not held for a person
   however its `Awaiting` reads; the hold ranks ahead of the wait and ahead of
   blocked work whose holds could not be read, so an entry that is both held
   and waiting on other work is held. The two kinds the queue does not make —
   a directive pause and the pass-level stall — are set by the standing status
   on top of the queue's reading and never come out of `hold()`, so no entry
   can carry one there. The counts therefore did not change; what changed is
   that they now cannot drift. `TestAwaitsIsReadOffTheHoldKind` pins each of
   those pairings, and was confirmed to fail with `Awaits` reading `Awaiting`
   alone.
3. **Fixed — the `endedAt` fallback was untested.** A terminal run with no
   completion recorded ends when its record last moved; nothing exercised that
   branch. One such run is added to
   `TestThroughputCountsEachEndingIntoTheWindowItEndedIn`, and the case was
   confirmed to fail with the fallback removed.
4. **Fixed — the spend total and its split by kind were derived twice.**
   `sumSpend` here and `printSpendTotals` with `renderKindSplit` in
   `internal/cli/statusstream.go` each summed `SpendReport.Rows` by kind, in
   the same order. The figures agreed because both read the same rows, and the
   CLI predates this code; but it is the disagreement
   `surfaces-project-one-read-model` names. The summation now lives on the
   report: `SpendReport.Totals()` adds the rows up in all and by kind in the
   order the kinds are priced, and `SpendReport.Since(day)` narrows a report to
   a later first day by the report's own `take` rule. `sumSpend` reads
   `report.Since(window.Since).Totals()`, and the CLI's spend printing reads
   `report.Totals()`, keeping only its wording. `kindOrder` in `throughput.go`
   is gone with it. `TestSpendReportTotalsAndNarrowsByItsOwnRule` holds both
   methods, and the existing throughput and `yoyo status --spend` tests hold
   the two callers.

## Noted, not findings

- `backlog.HeldForAPerson` is a `HoldKind` constant and
  `readmodel.HeldForAPerson` is the function that reads the harness's holds;
  both are used in `standing.go`. They compile and mean related things, but a
  reader meeting the bare name has to check the package.
- `Unpriced` and `Floor` are the report's over every exchange record, dated or
  not, and so are the same on both windows. That is also what
  `yoyo status --spend` says for a one-day window, since an unreadable record
  has no day to be outside of.
- A `succeeded` run with a promotion recorded and its pull request still
  awaiting the forge is counted as landed: the local target branch moved, which
  is what `Integration` records. The standing's needs-human line is where the
  forge wait is said.
