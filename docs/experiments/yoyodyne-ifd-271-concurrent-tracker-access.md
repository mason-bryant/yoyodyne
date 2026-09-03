# What concurrent runs do to the tracker, exercised against a real store

Work item: yoyodyne-ifd.271, from the developer report on run-3e6cc3ad.

**Status: a null result, and a check that keeps it true.** Concurrent `bd`
invocations against one embedded Dolt store were exercised live at and beyond the
capacity that provoked the question. Nothing failed and nothing was lost, so
`internal/beads/client.go` was left as it is — no lock, no retry — and the
exercise was made repeatable rather than reported once.

## The question

Raising `execution.max_concurrent_developers` above one makes several runs invoke
`bd` at the same time — `Show`, `Claim`, `RecordOutcome`, `RecordCost`,
`Complete` — beside the scheduler's `List` and `Ready`. The tracker is an
embedded Dolt database behind a file lock, and the adapter neither serializes its
invocations nor retries a contended one: a contended invocation has nowhere to go
but back to its caller as a failed run, on a boundary where the work is already
done. Two failure modes were open, and one of them is silent:

- **Loud.** A second opener is refused, and a run that finished its work is
  recorded as failed at the write that would have recorded it.
- **Silent.** Two overlapping writes to one item are read-modify-write inside bd
  — a note appended to the notes already there, a metadata key set beside the
  keys already there — so one of them is lost with no error anywhere. A lost
  `--append-notes` takes a goal attribution with it, which is the loss
  [the goal witness](../work.md) exists to make recoverable.

Nothing settled either way, because every concurrency check in the package drives
an in-process fake: all of them pass identically against a bd that refuses a
second opener.

## What was exercised

Against `bd version 1.1.2`, on macOS, over a scratch store in a temporary
directory. One `bd` invocation against an idle store takes about 0.95s wall,
which is the engine starting rather than the work.

| Exercise | Concurrency | Result |
|---|---|---|
| `bd create` alongside `bd list` | 8 writers, 6 readers | 14/14 succeeded; 8 distinct items, none lost |
| `bd update --set-metadata=kN=vN` on **one** item | 6 writers | 6/6 keys present afterwards |
| `bd update --append-notes` on **one** item | 6 writers | 6/6 lines present afterwards |
| The capacity-2 run shape through `beads.Client` | 2 runs + scheduler reads | every invocation succeeded; every item closed with its notes and its price |
| 6 creates sequential, then 6 concurrent | 1 then 6 | 3.05s then 3.23s |

The last row is the whole of the mechanism. Six concurrent writes cost what six
sequential ones cost, so bd is serializing them internally and a caller waits its
turn rather than being refused. That is why the adapter needs neither a lock —
there is one already, one layer down — nor a retry, since there is no failure to
retry.

## What was not exercised

`yoyo work` was not run at capacity two. It would have spent provider
invocations, cut worktrees, and written to this project's own tracker, none of
which is the question: what capacity two does to the tracker is the invocation
pattern it produces, and that is what ran. The substitution is worth knowing
about, because it means nothing here says anything about two concurrent runs
contending for anything other than the tracker — the check suite and the machine
are covered by [how long a check may take](../configuration.md#how-long-a-check-may-take)
instead, which is a measured cost rather than an open question.

Neither does anything here say bd will go on serializing. It is somebody else's
software, and the observation is about the version above.

## What keeps it true

`TestConcurrentRunConformance` and `TestConcurrentWriteConformance` in
`internal/beads/conformance_test.go`, beside the other checks that pin real-bd
behavior. The first runs the capacity-2 invocation pattern against a real store —
two runs making the whole sequence a run makes, while the scheduler reads beside
them — and fails naming what a contended invocation would cost. The second makes
four overlapping appends to one item and fails if any of them is missing, which
is the silent case caught loudly.

They run in `make check` like everything else, and skip where bd is not
installed. A bd version that stops serializing concurrent openers fails them
rather than failing a run at its last write.
