# yoyodyne-ifd.283.2: the forge-hygiene pass reads the whole closed set, and gh resolves the URL it is handed

yoyodyne-ifd.283.1 landed the harness's own reading of the forge on the
development manager's pass (`internal/forgehygiene`), and its run reported two
concerns after landing. Both are about a reading the pass takes from a tool it
does not control, and both were only ever exercised against a scripted runner.
This is what each turned out to be, checked against the real binaries.

## 1. `bd list` pages, and the pass now says not to

The pass decides "this item is closed" from `beads.Client.List(ctx, "closed")`,
which runs `bd list --json --status=closed`. `bd list` caps its output: fifty
rows by default, twenty in what it takes for an agent session, and zero is its
word for no cap (`cmd/bd/list.go` and `cmd/bd/list_input.go` in
`steveyegge/beads`, whose v1.2.2 is the v1.1.2 code this machine runs). It also
lifts the cap by itself when its stdout is not a terminal — which is why the
first real pass, below, was not truncated. That is bd's reading of its own
stdout rather than anything the client asked for, so the client now asks:
`Client.List` passes `--limit=0` on every listing.

Against a throwaway store holding seventy closed items (`bd init` in the run's
scratch directory, seventy closed rows imported; stdout piped throughout, the
sandbox refusing a pty):

```
bd version 1.1.2 (20e493e56)
bd list --json --limit=5 --status=closed : 5 rows
bd list --json --limit=0 --status=closed : 70 rows
bd list --json --status=closed           : 70 rows   (bd lifts its default itself when piped)
bd list --json --limit=0                 : 0 rows    (an unfiltered listing hides closed work)
bd list --json --limit=0 --all           : 70 rows
```

So `--limit` is honoured, `0` is the whole set, and a listing of more than fifty
closed items comes back whole. The fourth line is worth knowing on its own:
`List(ctx, "")` reads open work only, which is what its callers want, and a
reader that needs closed work asks for the status by name.

The test that pins it is
`TestTheWholeClosedSetIsReadSoAnOldSupersededRequestIsNotTakenForLive` in
`internal/forgehygiene/forgehygiene_test.go`. It puts the real `beads.Client`
over a fake `bd` that pages the way bd does on a terminal — fifty rows unless
`--limit=` says otherwise, `0` for all — holding 150 closed items newest first,
and asserts that a request superseded by the oldest of them is reported as held
open for a closed item, that the live request beside it is not, and that a
later pass given that report says nothing. With `--limit=0` removed from the
client the test fails with `notices = [], want the superseded #12 reported for
its closed item yoyodyne-ifd.1, which is past bd's default page`.

## 2. `GH_REPO` holds the remote's URL, and gh resolves it

`publish.GitHub.Contains` asks the forge whether a base already carries a head
through `gh api repos/{owner}/{repo}/compare/<base>...<head>`. The `api` verb
takes no `--repo` flag, so the repository is named in `GH_REPO`, and what is put
there is the configured remote's URL exactly as `git remote get-url` reports it
— `git@github.com:mason-bryant/yoyodyne.git` for this project — rather than the
`OWNER/REPO` form gh's documentation names for that variable. The concern was
that gh would not read a URL there.

It does. gh resolves `GH_REPO` through the same parser as `--repo`, which takes
a URL in either form. With the request logged before it is sent
(`GH_DEBUG=api`) and the network refused by the sandbox, the request gh built
from the URL is visible even though it could not be made:

```
gh version 2.94.0 (2026-06-10)
GH_REPO=git@github.com:mason-bryant/yoyodyne.git gh api --method GET -F per_page=1 repos/{owner}/{repo}/compare/main...69939baf…
* Request to https://api.github.com/repos/mason-bryant/yoyodyne/compare/main...69939baf…?per_page=1
> GET /repos/mason-bryant/yoyodyne/compare/main...69939baf…?per_page=1 HTTP/1.1
> Host: api.github.com

GH_REPO=https://github.com/mason-bryant/yoyodyne.git gh api ... repos/{owner}/{repo}/compare/main...HEAD
* Request to https://api.github.com/repos/mason-bryant/yoyodyne/compare/main...HEAD

GH_REPO="not a repository" gh api ... repos/{owner}/{repo}/compare/main...HEAD
unable to expand placeholder in path: expected the "[HOST/]OWNER/REPO" format, got "not a repository"
```

The placeholders are filled from the URL in both of its forms, and a value gh
cannot resolve fails in the placeholder step before any request, which is the
failure the concern predicted and which the real form does not produce.

### The first real pass's record

The record 283.1 asked to be quoted is the development manager's pass at
2026-09-19T05:49:21Z, in `<state root>/products/yoyodyne/sweeps/sweeps.jsonl`.
Its fields other than the account and the notices, verbatim:

```json
{"schema_version":1,"product_id":"yoyodyne","task":"development-manager-sweep","role":"development-manager","conversation_id":"chat-419cedb4a013b063f477e322a2a60466","started_at":"2026-09-19T05:49:21.593517Z","ended_at":"2026-09-19T05:49:39.747762Z","turns":1,"cost_usd":8.418222}
```

It carries fifty `pull_requests`, #98 through #500, every one with
`"base_branch":"main"` and `"item_closed":true`; the first and last:

```json
{"number":98,"url":"https://github.com/mason-bryant/yoyodyne/pull/98","head_branch":"yoyodyne/yoyodyne-ifd-102-4/e005eb9a","base_branch":"main","work_item_id":"yoyodyne-ifd.102.4","item_closed":true}
{"number":500,"url":"https://github.com/mason-bryant/yoyodyne/pull/500","head_branch":"yoyodyne/yoyodyne-ifd-355/81537b70","base_branch":"main","work_item_id":"yoyodyne-ifd.355","item_closed":true}
```

It carries no `problem` field. A comparison the forge refused is recorded there
as `the forge could not be fully read on this pass: pull request #N: compare
main with <commit> ...` (`Trigger.noticeForge` in
`internal/orchestrator/recurring.go`), and the field is omitted only when there
was nothing to say; fifty requests, each with a base and each listed with its
head commit, were compared and none refused. The pass after it, at
2026-09-19T10:23:29Z, carries no `problem` either and no notices, the fifty
having been reported once.

That record is quoted here rather than on the work item because a developer run
cannot write to the tracker; whoever closes 283.2 can carry it across.

## What this leaves

- The fifty requests are still open on the forge. The pass notices and closes
  nothing, by design; closing them is somebody's decision.
- `ListOpen` is bounded at two hundred open requests, deliberately and
  documented on the method. It is not the same concern — the bound is chosen
  rather than inherited — and a forge holding more than that is reported two
  hundred at a time.
