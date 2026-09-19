# yoyodyne-ifd.283.3: the pass names the repository as gh documents it, and a listing survives a bd without `--limit`

yoyodyne-ifd.283.2 checked two readings the forge-hygiene pass takes from tools
it does not control, and its reviewer reported both as still resting on a
single binary's behaviour: gh 2.94.0 accepting a URL in `GH_REPO`, and bd 1.1.2
accepting `--limit=0`. This is what 283.3 changed so that neither reading rests
on that, and the evidence for each.

## 1. `GH_REPO` is derived as `[HOST/]OWNER/REPO`

`publish.GitHub.Contains` used to put the configured remote's URL into
`GH_REPO` exactly as `git remote get-url` reports it, and gh resolved it. gh's
documentation names `[HOST/]OWNER/REPO` for that variable; the URL working was
its parser's leniency rather than its contract.

`Contains` now derives the repository from the URL through
`remoteRepository` in `internal/publish/github.go`, which reads host, owner,
and name out of every form git reports — `git@github.com:owner/name.git`,
`ssh://git@github.com/owner/name.git`, `https://github.com/owner/name`, with or
without the `.git` suffix, a trailing slash, a user, or a port — and names the
repository `OWNER/REPO` for github.com and `HOST/OWNER/REPO` for any other
host, so an enterprise remote is not resolved against the public forge. A URL
naming no repository is refused before gh is asked.
`TestRemoteRepositoryIsOwnerAndNameInTheFormGHRepoTakes` pins every form and
every refusal, and `TestGitHubContainsAsksTheForgeHowFarAheadTheCommitIs` pins
that the derived name, not the URL, is what the API verb is given.

Against gh itself, with the request logged before it is sent and the network
refused by the sandbox, both forms fill the placeholders the same way the URL
did in 283.2:

```
gh version 2.94.0 (2026-06-10)
GH_REPO=mason-bryant/yoyodyne gh api --method GET -F per_page=1 repos/{owner}/{repo}/compare/main...2f6ac76
* Request to https://api.github.com/repos/mason-bryant/yoyodyne/compare/main...2f6ac76?per_page=1
> GET /repos/mason-bryant/yoyodyne/compare/main...2f6ac76?per_page=1 HTTP/1.1
> Host: api.github.com

GH_REPO=github.com/mason-bryant/yoyodyne gh api ... repos/{owner}/{repo}/compare/main...2f6ac76
* Request to https://api.github.com/repos/mason-bryant/yoyodyne/compare/main...2f6ac76?per_page=1
```

## 2. A listing bd refuses for `--limit` is asked again without it

`beads.Client.List` passes `--limit=0` on every listing so that a decision over
the whole set is not taken over bd's first page. A bd release without the flag
refuses every such listing before it opens the store — cobra's `Error: unknown
flag: --limit`, exit 1 — and among those listings is the scheduler's selection,
so every run the harness would make fails with it.

The client now asks again without the flag when, and only when, bd's refusal
names the flag as unknown (`refusedFlag` in `internal/beads/client.go`). What
the second reading loses is the lift: it is bd's own page, whole where bd lifts
its cap for a pipe — which bd 1.1.2 does, per 283.2 — and its first fifty rows
where it does not. A bd failing for any other reason is not asked twice, so a
locked store is still the error it was.

The refusal wording was read off bd itself:

```
bd version 1.1.2 (20e493e56)
bd list --json --bogus-flag=0 --status=open
Error: unknown flag: --bogus-flag
exit=1
```

`TestClientListsWithoutTheLimitFlagWhereBDRefusesIt` puts the real client, over
the real process runner, on a shell script standing in for bd that refuses
`--limit=` with that wording and refuses `--status=blocked` for the store. It
asserts that the rows come back, that the flag was tried first and dropped on
the retry, and that the store's refusal was not retried:

```
list --json --limit=0 --status=open
list --json --status=open
list --json --limit=0 --status=blocked
```

## The first real hourly pass's record

The item asks for the first real hourly pass's record to be quoted on it,
showing the compare endpoint read. A developer run cannot write to the tracker,
so it is quoted here for whoever closes the item to carry across.

The pass is the development manager's at 2026-09-19T05:49:21Z, in
`<state root>/products/yoyodyne/sweeps/sweeps.jsonl` — the first pass after
the forge reading landed that found open requests. Its fields other than the
role's result and the fifty `pull_requests`, verbatim:

```json
{"schema_version":1,"product_id":"yoyodyne","task":"development-manager-sweep","role":"development-manager","conversation_id":"chat-419cedb4a013b063f477e322a2a60466","started_at":"2026-09-19T05:49:21.593517Z","ended_at":"2026-09-19T05:49:39.747762Z","turns":1,"cost_usd":8.418222}
```

The fifty `pull_requests` run #98 through #500, every one with
`"base_branch":"main"` and `"item_closed":true`; the first and last:

```json
{"number":98,"url":"https://github.com/mason-bryant/yoyodyne/pull/98","head_branch":"yoyodyne/yoyodyne-ifd-102-4/e005eb9a","base_branch":"main","work_item_id":"yoyodyne-ifd.102.4","item_closed":true}
{"number":500,"url":"https://github.com/mason-bryant/yoyodyne/pull/500","head_branch":"yoyodyne/yoyodyne-ifd-355/81537b70","base_branch":"main","work_item_id":"yoyodyne-ifd.355","item_closed":true}
```

The record carries no `problem` field. The pass compares every listed request
that has a base and a head commit (`Sweeper.Notice` in
`internal/forgehygiene/forgehygiene.go`), and a comparison the forge refused is
recorded as `the forge could not be fully read on this pass: pull request #N:
compare main with <commit> ...`; the field is omitted only when every reading
succeeded. Fifty requests with a base, listed with `headRefOid`, were compared
and none refused: that is the compare endpoint read, fifty times, on the
`GH_REPO` form 283.2 shipped. The passes since — 07:35, 10:23, 11:30, 12:59,
and 14:53 UTC on the same day — carry no `problem` and no requests either, the
fifty having been reported once.

The same record made by the first pass after this change merges is what shows
the endpoint read on the derived form; it is made by the running harness, after
integration, and is not something this run can produce.
