# yoyodyne-ifd.238: the probe verdict that came out of another run's log

A developer on run `run-b276d8e695715ea1c9c0a0cd96517e9f` — work item
yoyodyne-ifd.209.16, 2026-09-01 — reported that its minute-zero probe had failed
spuriously. `go vet ./...` had refused with

```
# github.com/mason-bryant/yoyodyne/internal/gitworktree
internal/gitworktree/manager.go:618:8: declared and not used: head
vet: internal/gitworktree/manager.go:618:8: declared and not used: head
make: *** [vet] Error 1
```

minutes after `go test ./...` and `go test -race ./...` had compiled the same
package cleanly, on a package the run never touched, and the same command passed
seconds later. It named two suspects and proved neither: that the worktree was
still being written when the probe started, and that `GOCACHE` points at
`.git/yoyodyne/go-build`, which every concurrent developer run shares. ifd.238
was admitted on that report.

**Both suspects are wrong, and the probe never failed.** The run's own
`make check` exited 0. What failed was the *log it read the result out of*: two
concurrent runs redirected their checks into one file, because the temporary
directory Claude Code gives a run is one directory shared by every run on the
machine. This is the record of how that was established, from the two runs'
durable records and provider transcripts.

## The verdict did not come from that worktree

The run's own commands settle it. Seven seconds after reading the failure, at
17:23:40Z, the developer printed the region the diagnostic named and vetted the
package again:

- `sed -n '605,630p' internal/gitworktree/manager.go` — no `head` declaration.
- `grep -n "head" internal/gitworktree/manager.go` — matches at lines 151, 195,
  197, 438 and 728, and none at 618.
- `go vet ./internal/gitworktree` — exit 0.
- `stat` — `manager.go` last written `2026-09-01 09:53:23`, which is the run's
  own `started_at` (`2026-09-01T16:53:23Z`) and therefore the moment
  `git worktree add` wrote it.
- `git status --porcelain internal/gitworktree/` — empty.

So the file the diagnostic names had no such variable, and had not been written
since the worktree was cut. The declaration it describes is real, but it belongs
to a different change: `path, head, err := m.verifyOwnedHead(...)` entered
`UnifiedChanges` with `de8172c` (yoyodyne-ifd.236) at 10:10:01 -0700 and reached
`main` in the merge `2846668` at 10:14:36 -0700 — after this worktree was cut
from `a8b21d1`, and after the probe ran.

A trap for anyone re-reading the run record: `run-b276…`'s `base_commit` field
now reads `2846668`, which is that later merge. The run replayed onto the moved
target once at integration (`integration_retries: 1`) and the field records where
it ended up, not where the developer worked. `git log --oneline -5` at 16:53:31Z
in the run's own transcript shows the worktree at `a8b21d1`.

## Where the verdict did come from

Both runs redirected their probe into `$TMPDIR/probe-check.log`, and `$TMPDIR` is
`/tmp/claude-501` for every Claude Code session this machine runs — one directory
per user, not per session or per run:

| time (UTC) | run | command |
| --- | --- | --- |
| 16:53:37.938 | ifd.236 (`run-…5e1071d8` worktree) | `make check > "$TMPDIR/probe-check.log" 2>&1` |
| 16:53:42.831 | ifd.209.16 (`run-b276…`) | `make check > "$TMPDIR/probe-check.log" 2>&1` |

Five seconds apart, into one file, from two worktrees. The ifd.236 run was
mid-change on exactly `internal/gitworktree/manager.go`, adding the `head` return
that its use at line 679 had not yet caught up with — a tree that vets with
precisely the diagnostic the other run read.

The retained task output of the ifd.209.16 probe
(`…/tasks/bdg9c00ud.output`) closes it. The command was
`make check > "$TMPDIR/probe-check.log" 2>&1; echo "exit=$?"; tail -30 "$TMPDIR/probe-check.log"`,
and the file records, in order:

```
exit=0
ok  	github.com/mason-bryant/yoyodyne/internal/notify	2.914s
...
go vet ./...

# github.com/mason-bryant/yoyodyne/internal/gitworktree
internal/gitworktree/manager.go:618:8: declared and not used: head
```

`make check` exited **0**. The probe passed. The thirty lines printed after it
are what the shared file held once both runs had written to it, and they carry
the other run's failure. A `make` that failed cannot exit 0, so the exit status
and the log disagree — and the log is the one that is wrong.

## What this leaves of the two suspects

**Worktree materialization does not race the probe.** `Manager.Create` is
synchronous and does the whole of it before it returns: `git worktree add`, then
the export refresh, then an inspection that refuses a worktree which is not
registered on the expected branch or which already carries uncommitted changes.
A clean `git status --porcelain --untracked-files=all` over a fresh checkout is a
statement that every tracked file is present with the content the base commit
carries. The pipeline calls `Create` and only then invokes the developer, so a
probe cannot start before that has all happened.
`TestCreateReturnsOnlyAWorktreeThatIsWhole` pins the postcondition. What the
2026-09-01 report read as a checkout still in flight was the uniform
creation-time timestamp `git worktree add` leaves on every file it writes.

**The shared build cache is not implicated and stays shared.** The diagnostic the
probe read was produced by a `go vet` that really did run over a tree containing
that variable, in the worktree that contained it, and reached the reader as text
in a file rather than as anything the toolchain returned. Build cache entries are
keyed by the content compiled, so two worktrees at different content are two sets
of entries. Isolating the cache per worktree was measured before it was dropped:
a cold `make check` in this repository is 203s and leaves 688 MB, paid once per
run instead of once per repository, for a hazard nothing has shown.

Deliberate contention against one cache did not reproduce a crossed verdict
either. This is the experiment, so that the suspicion can be put to the same test
rather than argued again — `gocache.go` sends a reader here, and prose alone is
not something a reader can re-run. Two modules of the same module path and
different content, one of them carrying exactly the defect the incident named,
vetted concurrently against one `GOCACHE`, with the uniform timestamps
`git worktree add` leaves reproduced by hand:

```sh
R="$TMPDIR/gocache-contention"; rm -rf "$R"; mkdir -p "$R/a/p" "$R/b/p" "$R/cache"
for d in a b; do printf 'module example.com/m\n\ngo 1.26\n' > "$R/$d/go.mod"; done
printf 'package p\n\nfunc F() int {\n\thead := 1\n\treturn head\n}\n' > "$R/a/p/p.go"
printf 'package p\n\nfunc F() int {\n\thead := 1\n\treturn 2\n}\n' > "$R/b/p/p.go"
touch -t 202609010953.23 "$R"/*/p/p.go "$R"/*/go.mod
export GOCACHE="$R/cache"
bad=0
for i in $(seq 1 60); do
  ( cd "$R/a" && go vet ./... >/dev/null 2>&1; echo $? > "$R/a.rc" ) &
  ( cd "$R/b" && go vet ./... >/dev/null 2>&1; echo $? > "$R/b.rc" ) &
  wait
  [ "$(cat "$R/a.rc")" = 0 ] && [ "$(cat "$R/b.rc")" = 1 ] || { bad=$((bad+1)); echo "round $i crossed"; }
done
echo "crossed rounds: $bad / 60"
```

Run on 2026-09-01 against go1.26.6 darwin/arm64, it reported `crossed rounds: 0 /
60`: the clean module vetted clean and the defective one vetted defective, every
round. That is a floor rather than a proof — a fault seen once in months of runs
is not one sixty rounds are entitled to find — which is why the attribution
above, and not this, is what settles the question.

## What changed

The developer contract now tells every run that the machine's temporary
directory is shared with the runs beside it, that a scratch path must carry the
id of the work item that wrote it, and that the alternative is not the worktree —
a scratch file there is untracked content in the change. `CLAUDE.md` and
`AGENTS.md` say the same for a session the harness did not make, which on this
machine is the operator working in the checkout while two runs execute.

`docs/developing-yoyo.md` is the better home for that second half and could not
take it. It is one of the eight documents the product manager's context bundle
carries, whose combined 512 KiB budget had 32 bytes free when this ran, so a
paragraph added there fails `make test` for a reason that has nothing to do with
the paragraph. Raising `defaultMaxProductBytes` in
`internal/contextbundle/product.go`, trimming the guides, or narrowing the set is
a decision somebody owns and this run does not; two earlier runs reported the
same wall as a prediction and this is the first documentation it has actually
turned away. Whoever settles it should move the section there.

That decision has since been taken, in yoyodyne-ifd.240: the budget is 640 KiB
and the guides were not trimmed, so the wall this section describes is no longer
in the way of moving it.

That is guidance rather than a wall, deliberately: Claude Code sets `TMPDIR`
itself in sandbox mode, to a directory it derives per user, so a `TMPDIR` the
harness passed in would be replaced before the run's first command ran. The
directory a run may write and the directory a run is given are the provider's to
reconcile; what the harness can do is make sure no run picks a name another run
would pick.
