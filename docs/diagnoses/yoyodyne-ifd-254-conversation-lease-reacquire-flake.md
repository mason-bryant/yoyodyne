# yoyodyne-ifd.254: the conversation lease that answered "already held" after it was released

A developer on `run-0193cafd` — work item yoyodyne-ifd.120, 2026-08-20 —
reported that `internal/runstate` `TestConversationStoreHoldsOneConversationAtATime`
had failed once under `go test -race ./...`. The failing assertion was the third
of the test's three holds, the one taken immediately after the second was
released:

```go
if err := held.Release(); err != nil { ... }
// A released conversation is immediately available again.
regained, err := newConversationStore(t, root).Hold(...)
if err != nil {
    t.Fatalf("Hold() after release error = %v", err)
}
```

It did not reproduce in three further race suites, a `-count=5` targeted run, or
six targeted runs on the untouched base commit. `race` is one of the four checks
`make check` runs, so a flake there fails verification and hands a correct change
back for a repair round it did not earn. ifd.254 was admitted to find out
whether it can fire in the gate.

**It cannot fire on the path it was seen on, and the reason is a property of
that path that nothing was pinning.** The mechanism that produces that exact
message does exist in this package, and it was live on four other locks. Those
are fixed, and both halves are now under test.

## Only one thing produces that message

`ConversationStore.Hold` refuses with `ErrConversationHeld` — "already held by
another process" — in exactly one case: `tryLockStateFile` returned `EWOULDBLOCK`
(`internal/runstate/conversation.go`, `internal/runstate/lock_unix.go`). Every
other failure inside `Hold` reports something else — a directory it could not
create, a lease it could not open, a stamp it could not write. So at the instant
the third hold ran, some open file description held an exclusive `flock` on that
lease file.

The test's own descriptions cannot be it. The first hold's was unlocked and
closed by `Release`; the second hold's never took the lock at all. That leaves
one candidate, and it is the one this package already knows about, in the comment
on `leaseGrace`:

> The lock belongs to the open file description, not to the descriptor, so a
> child process forked while the lease was open shares it until that child execs
> and its close-on-exec descriptor goes away. The harness forks Git and check
> processes constantly, so a released lease can keep answering "held" for a few
> milliseconds afterwards.

The `internal/runstate` test binary forks itself twice over —
`exitedProcess` and `TestPromotionLeaseDiesWithItsHolder` both run
`exec.Command(os.Args[0], ...)` — and its tests are parallel, so a fork landing
inside another test's held lease is ordinary rather than exotic.

## The conversation path is immune, and it is immune for a reason

A fork sharing the description only outlives the holder if the holder let the
*close* drop the lock. `Lease.Release` does not: it calls `unlockStateFile`
first, and `LOCK_UN` clears the lock on the description itself, for every
descriptor still referring to it.

Measured on the machine the flake was seen on (macOS 26.6, arm64, Go 1.26.6),
with four goroutines forking continuously and eight running the test's exact
open / lock / contend / release / reacquire sequence:

| release | rounds | reacquire refused |
| --- | --- | --- |
| `LOCK_UN` then close | 160,000 | 0 |
| close alone | — | first round |

The close-only variant does not need pressure to fail; it fails on the round
after the first fork. The same sequence driven through the real store —
`Hold`, contend, `Release`, `Hold` — under `-race`, with the binary forking
itself as its own tests do and with descriptor churn alongside, ran 25,380 rounds
with no refusal, and twelve `-race` runs of the whole package under concurrent
suite load found nothing.

So the reported failure cannot have come from the fork window on that path, and
nothing else in the process can lock that file. What remains for the original
sighting is outside this code: the run that saw it predates both the shared-log
diagnosis (`yoyodyne-ifd-238-probe-verdict-crosstalk.md`, 2026-09-01) and the
per-run scratch directory that replaced the shared temporary one
(yoyodyne-ifd.247, 2026-09-02), so it was reading its check output out of a file
every concurrent run on the machine could write. That is not proven here and is
not offered as the answer; it is where to look if the sighting matters more than
the mechanism.

## Four locks did have the window

Looking for the mechanism found it. Five places in `internal/runstate` take an
advisory lock and let it go again. One — `Lease.Release` — unlocked first. Four
released by closing:

- `Store.lockReservations` (`store.go`), which serializes reservation and
  adoption across every Yoyodyne process
- `TriageStore.lock` (`triage.go`)
- `RerunStore.lock` (`rerun.go`)
- `EscalationStore.lock` (`escalation.go`)

All four are taken with `lockStateFile`, which waits and retries, so a phantom
lock usually costs milliseconds rather than an answer. The exception is a caller
whose context is already done, and `escalation_lock_unix_test.go` names it
exactly: an uncontended lock is taken without the context being consulted at
all, so a give-back under a cancelled context succeeds or fails depending on
what else wanted the record at that moment. A fork-shared lock left behind by
the previous holder is one of the things that can want it — and the give-back
that loses that race spends a triage attempt that decided nothing.

All five now release through one `releaseStateFile`, which unlocks and then
closes.

## What is under test now

Two tests in `lock_unix_test.go`, each of which fails if the unlock is taken
back out. A duplicate descriptor stands in for the forked child's: it is the
same sharing of one open file description, without the fork.

- `TestReleasingAStateFileDropsTheLockADuplicateStillShares` covers
  `releaseStateFile`, which is what the four closers now call.
- `TestReleasingAConversationDropsTheLockADuplicateStillShares` covers
  `Hold` / `Release` / `Hold` through the real store, and with the unlock removed
  it reproduces the reported message verbatim: `the product-manager conversation
  is already held by another process`.

`TestUnlockingALeaseFileFreesItWithoutClosingIt` did not catch any of this
because it exercises `unlockStateFile` directly rather than any path that
releases a lock.
