# yoyodyne-ifd.430.1: the 88 per cent is mostly a misread field, and what is left of it is the cold cache rather than the threshold

The item was admitted on `/api/throughput` showing conversations at $26,781 of
$30,336 over seven days, on the premise that "at about 2.7 landings an hour the
20-landing threshold is crossed within eight hours, so most management turns
still re-read the whole bundle, and the three management conversations may each
be paying for it."

**The reading is right and both inferences from it are wrong.** The number on
the surface is what the record says; the record is overstating conversation
spend about forty-fold, for a reason
[`yoyodyne-ifd.424`](yoyodyne-ifd-424-one-shot-cache-reads.md) established two
days earlier. And of the real spend underneath it, the re-read the item suspects
is about seven per cent: management turns do not mostly re-read the bundle, and
what they are actually paying for is a prompt cache that has expired before the
turn begins.

Everything below is read on 2026-09-22 from the product's own state directory —
`spend.jsonl` and the three management conversations' event logs — with
`yoyo status --spend`, the instrument ifd.424 added, where it answers the
question, and a scratch script over the same terminals where it cannot. The
window is `yoyo status --spend 7`'s own: the seven local days 2026-09-16 to
2026-09-22, so every figure here can be checked against that command. A
developer run cannot write the tracker, so the figures the item asked to have
quoted on it are quoted here.

## Cache-read share per role, from `yoyo status --spend 7`

```text
role                   calls        cache_w  cache_w USD        cache_r  cache_r USD  cache_r%        USD
developer                231     36,968,804      $582.77  3,233,291,085     $2496.14     98.9%   $3367.40
reviewer                 275     22,148,809      $191.24      1,261,976        $0.63      5.4%    $243.63
product-manager          175      7,057,667      $790.38     62,837,816     $1678.33     89.9%   $2769.87
development-manager      282     20,969,822     $8720.02    116,071,879    $16311.47     84.7%  $26482.15
architect                  4      2,112,520       $42.25              0        $0.00      0.0%     $42.86
```

The token columns are sound and the USD columns are not, and the table says so
itself if read across. The developer read 3.23 billion cached tokens for
$3,367; the development manager read 116 million — twenty-eight times fewer —
for $26,482, eight times more. No rate multiple produces that. The USD columns
are apportioned from the provider's own reported figure, and since Claude Code
2.1.278 was installed here on 2026-09-19T18:22Z that figure is the **session's
running total** rather than the invocation's own cost. ifd.424 established this;
the mechanism is not re-derived here.

Every long-resumed conversation therefore re-records its whole history on every
turn, and the recorded sum is triangular. The development manager's last four
spend lines are `0, 355.79, 356.34, 356.74, 357.04` — one session id
(`fc892f4e`, unchanged since 2026-08-30), 885 turns, monotone non-decreasing for
three days. Differencing that chain gives each turn its own cost.

| | priced turns | recorded | real | over-count |
|---|---|---|---|---|
| development manager | 282 | $26,482.15 | $475.08 | 56× |
| product manager | 175 | $2,769.87 | $171.55 | 16× |
| architect | 4 | $42.86 | $25.26 | 2× |
| **conversations** | **461** | **$29,294.88** | **$671.89** | **44×** |

The recorded column is `yoyo status --spend 7` to the cent, over the same seven
local days; the real column differences each conversation's chain. **Real
conversation spend over the seven days is about $672, not $29,295.** Against the
$3,611.03 the same window records for the 510 run invocations — which a run
resuming a session two or three times barely overstates, and which is left
uncorrected here — conversations fall from 89 per cent of the product's recorded
spend to about a sixth of its real spend.

## Turns per day, and bundle bytes per turn

| | priced turns | per day | refreshes | refreshes per turn |
|---|---|---|---|---|
| development manager | 282 | 40.3 | 24 | 8.5% |
| product manager | 175 | 25.0 | 6 | 3.4% |
| architect | 4 | 0.6 | 2 | 50% |

The bundle is 960,017 bytes of shipped documentation across eight documents plus
25,399 bytes of specifications, and the work items and the triage docket on top;
each picture records the shipped part on itself, which read 950,697 bytes on the
development manager's last refresh and 959,202 on the product manager's. It is
sent on a conversation's first turn and on a refresh, and never otherwise — every
other turn resumes a session that already holds it.

So **bundle bytes per turn is about 84 KB for the development manager and 34 KB
for the product manager**, against a resident bundle of roughly a megabyte that
every turn reads back. What the bundle costs to deliver is measurable directly:
a cold turn that delivers a refresh writes a mean of 684,985 tokens against
559,916 for a cold turn that delivers none, so the bundle's marginal write is
about **125,000 tokens, or $7.80 at the fable rate**.

## What the 88 per cent is made of

Differencing the running total and splitting by what each turn did. The chain the
difference is taken over begins when the cumulative reporting did, on
2026-09-19T18:42Z, so these are the last three days of the window rather than all
seven — $371.00 of the development manager's $475.08 and $83.58 of the product
manager's $171.55, the remainder being the turns before that date, which priced
themselves and need no correction.

| development manager, 171 turns, $371.00 real | turns | cost | share | mean | mean cache_w |
|---|---|---|---|---|---|
| cold prefix, delivered a refresh | 3 | $57.04 | 15.4% | $19.01 | 684,985 |
| cold prefix, no refresh | 24 | $269.70 | 72.7% | $11.24 | 559,916 |
| warm | 143 | $44.26 | 11.9% | $0.31 | 6,257 |

| product manager, 60 turns, $83.58 real | turns | cost | share | mean | mean cache_w |
|---|---|---|---|---|---|
| cold prefix, delivered a refresh | 3 | $42.04 | 50.3% | $14.01 | 690,789 |
| cold prefix, no refresh | 6 | $26.99 | 32.3% | $4.50 | 212,866 |
| warm | 50 | $14.55 | 17.4% | $0.29 | 3,664 |

**88 per cent of the development manager's real spend is its 27 cold-prefix
turns, and 73 per cent is cold turns that re-read nothing at all.** A cold turn
costs thirty-six times a warm one because the whole resumed transcript is
rewritten at the one-hour cache-write rate, and the transcript is cold because
the gap since the previous turn was just over an hour: the hourly sweep
(`recurring_tasks.development-manager-sweep.every: 1h`) measured from the end of
the previous turn always lands past a one-hour cache measured from its start.
The gaps are 61 minutes over and over — 06:16, 08:17, 10:19, 12:20, 14:22,
16:23, 18:25, 20:26 on 2026-09-21, each preceded by 61 idle minutes.

## Which lever

**Not the threshold.** The item's premise is that most management turns re-read
the whole bundle. Since the turn-size backstop was fixed, three of 127
development-manager turns and three of 42 product-manager turns delivered a
refresh — 2.4 per cent and 7.1 per cent, not "most". Six delivered refreshes
across the week at $7.80 of marginal write each is about $47, seven per cent of
the real conversation total, and that is the whole of what raising the threshold
could save — bought by making the advice older, which is the one thing the
threshold exists to prevent.

**The cache, by the sweep interval.** A sweep firing inside the hour rather than
just past it pays a read where it now pays a write: $0.31 instead of $11.24, on
the turns that are 88 per cent of the spend. That is `every: 1h` in
`.yoyodyne/config.yaml`, one line, which a developer run may not touch. ifd.424
named it for the product manager and it has not moved; this measurement is the
second independent arrival at it.

**Bundle size, as the multiplier under the first.** The ~1 MB bundle is the
largest single part of what a cold turn rewrites. Halving it would halve every
cold turn, but the product manager has twice decided the guides are not trimmed
(yoyodyne-ifd.240, yoyodyne-ifd.403) and ifd.430.1 does not reopen that.

## Why the picture used to be stuck, and what this change locks in

The item records that before the turn-limit fix it dates to 2026-09-21 the banner
"showed landings-behind counts above 1000 on every message, which did not
decrease". That fix is `a35ddda`, committed 2026-09-20T21:25Z, and the
conversation log shows the mechanism exactly. On 2026-09-20 the development
manager took 21 refreshes and delivered none of them:

```text
06:42:04 context.refreshed  since={'commits': 970, ...} replaces=2026-08-19T19:01:28 commit=1b4207f8
07:20:31 context.refreshed  since={'commits': 970, ...} replaces=2026-08-19T19:01:28 commit=1b4207f8
07:35:35 context.refreshed  since={'commits': 970, ...} replaces=2026-08-19T19:01:28 commit=1b4207f8
...
16:00:56 context.refreshed  since={'commits': 994, ...} replaces=2026-08-19T19:01:28 commit=d9b5658d
```

`replaces` is the conversation's opening briefing every time. Every management
turn was being refused that day because `chat.MaxTurnInputBytes` had drifted
below the bundle it is meant to backstop, when yoyodyne-ifd.403 raised the
shipped-documentation ceiling that morning, so the
turn that would have carried the new picture never completed, the picture was
adopted only on a turn that succeeds, and the next process re-read the whole
repository and tracker from the same month-old commit. Twenty-one full re-reads,
all discarded. They cost no provider money — the backstop is the harness's own
bound and refuses before the provider is invoked, which is why no terminal sits
between them — so what they cost was the repository and tracker reads, the
sweeps that produced nothing, and a record that said "refreshed" twenty-one
times about a picture that never moved.

After the backstop fix both conversations deliver every refresh they take —
three of three each — and `replaces` advances: the development manager's last
refresh replaces a picture from 2026-09-22T00:32 rather than 2026-08-19, and the
product manager's one from 2026-09-22T04:20, which is the "27 landings behind
from a picture 10 hours old" the item records.

The change this document lands with puts the commit on the freshness line and on
both refresh renderings, so a reader can see the picture move rather than take
it on trust, and
`TestARefreshRecordsTheCommitItReadAgainstSoTheNextProcessMeasuresFromIt` holds
the durable half: the commit reaches `ContextCommit`, and a fresh session over
the same record measures from it. That is the regression test for the 21
discarded re-reads above, from the side that would have shown them.

## Named for the product manager

Three pieces of work this measurement identifies and this item does not do:

1. **Record the invocation's own cost, not the session's running total.** ifd.424
   named this and it is still open. It needs a backend signal for providers that
   report cumulatively, durable per-session baseline state, the six
   `spend.Metered` call sites and `internal/runstate/price.go`, and a correction
   of the records since 2026-09-19. Every spend decision taken since then was
   taken on numbers up to fifty times too high.
2. **Fire the recurring sweeps inside the cache lifetime.** One line of project
   configuration, worth about $300 a week on the development manager alone. The
   general form — the harness refusing or warning on a recurring interval at or
   above the provider's cache lifetime — is a second, larger item.
3. **Keep a taken refresh across a failed turn.** `pendingRefresh` lives only in
   the `Session` that took it, so a re-read whose turn then fails is discarded
   and the next process reads the repository and the tracker again. That is what
   turned one stuck picture into 21 full re-reads on 2026-09-20. The backstop fix
   removed the cause of those failures rather than this amplifier, and making the
   pending picture durable means putting roughly a megabyte of briefing text in
   the conversation record, which is a design decision rather than a repair.

   *Admitted as `yoyodyne-ifd.430.2` and landed on 2026-09-22.* The megabyte did
   not go in the record — the record may be a megabyte altogether, so carrying
   the text inside it would have been a conversation that could no longer save
   itself. It waits in a file beside the record, which names which picture is
   waiting and the commit it was read against; the delivery clears both.
