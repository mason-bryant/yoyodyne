# Working on yoyo itself

*For someone changing yoyo itself. Part of
[yoyo's documentation](../README.md#further-reading).*

`yoyo` is configured against its own repository, so a checkout of it is a
project like any other. From one, verify the tools, run every check, and open
the conversation:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyo config validate
./bin/yoyo chat
```

`make check` is `fmtcheck`, `test`, `race`, and `vet`, and it is the gate CI
runs.

## What `test` checks besides the code

Some of what `make test` runs is not about the Go code at all: it reads this
repository's own documents, so a mechanical defect in them fails a check instead
of costing a reviewer a paragraph. Each one exists because a reviewer wrote that
paragraph, more than once, and because the thing being checked is one nobody can
verify by eye — a relative path resolves only against the directory layout, and a
`#fragment` names a heading through a slug nothing writes down.

| What fails | Where it lives | What it means |
| --- | --- | --- |
| A link in any Markdown file here resolving to nothing — a path that is not in the repository, or a fragment naming a heading the target does not carry | `internal/doclink` | Fix the link, or the heading it points at. Absolute URLs are not resolved: they are somebody else's to keep working, and reaching for one would put the network in a deterministic check. |
| A goal in an in-force goals document written across more than one physical line | `internal/cli` (`goals_repository_test.go`) | Rejoin the statement onto one line. The goal is recorded whole either way; what the check holds is that the words an attribution must match are what the file says outright. |
| A governed document whose place in the chain is wrong — a `supports` entry naming nothing, an artifact reaching no brief, or a revision recorded by a role that does not own the document | `internal/cli` (`artifact_repository_test.go`) | The harness reports these and never refuses a document over one; here they fail, because a warning nobody is made to read is how one of them breaks unnoticed. |

Fixtures written to be malformed on purpose are not walked: anything under a
`testdata` directory is skipped, along with `.git`, `.dolt`, and `dist`.

`make dist VERSION=<tag>` builds the release archives and their checksums into
`dist/`, and `make dist-verify VERSION=<tag>` does that and then unpacks the
archive for the platform it is running on and asserts the binary reports
`<tag>`. That target is the whole of what a release consists of: the release
workflow runs it for a pushed tag and publishes what it produced, and CI runs
the same target on every change with a placeholder version, so a tag push
reruns a path that is already exercised rather than executing it for the first
time when a failure would mean a botched or missing release.

## Cutting a release

`make release VERSION=<tag>` is that build with its gate in front, so a daily
cadence costs two commands rather than a procedure:

```sh
make release VERSION=v0.3.0
git push origin v0.3.0
```

It walks [the documented adoption path](../scripts/walk-adoption.sh), runs
`check`, builds and verifies the archives for `<tag>`, then tags the commit
they were built from — in that order, so a red gate refuses the cut, names what
was red, and leaves nothing to undo. It also refuses a tag that is not
`vMAJOR.MINOR.PATCH` or that already exists, a dirty working tree, a checkout
that is not on `main`, and a `HEAD` that is not where `origin/main` is; where
origin is unreachable it says that last one went unchecked rather than passing
over it.

The tracker's own exports — `.beads/interactions.jsonl` and
`.beads/issues.jsonl` — do not count as a dirty tree. They are derived from a
store that is authoritative elsewhere, nothing a release ships is built from
them, and the walkthrough this gate runs rewrites them itself, so refusing on
them would stall most days of a daily cadence. The cut commits them instead,
as their own housekeeping commit placed after the last gate is green and before
the tag, which keeps the tag naming a tree with nothing uncommitted in it
rather than a clean tree with a footnote. On a day it had to make that commit
it prints `git push --atomic origin main <tag>` as the publishing command,
because origin does not have that commit and the branch has to carry it.

It stops at the tag. Publishing is the `git push`, which is the irreversible
half and what the release workflow acts on, so it stays something you do
deliberately. [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh)
executes every one of those refusals against fabricated repositories.
