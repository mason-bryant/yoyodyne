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
cadence costs two commands rather than a procedure once
[this tag's notes are on `main`](#every-cut-writes-its-notes):

```sh
make release VERSION=v0.3.0
git push origin v0.3.0
```

It gates on [this release's notes](releases/README.md), walks [the documented
adoption path](../scripts/walk-adoption.sh), runs `check`, builds and verifies
the archives for `<tag>`, then tags the commit they were built from — in that
order, so a red gate refuses the cut, names what was red, and leaves nothing to
undo. It also refuses a tag that is not `vMAJOR.MINOR.PATCH` or that already
exists, a dirty working tree, a checkout that is not on `main`, and a `HEAD`
that is not where `origin/main` is; where origin is unreachable it says that
last one went unchecked rather than passing over it.

It stops at the tag. Publishing is the `git push`, which is the irreversible
half and what the release workflow acts on, so it stays something you do
deliberately. [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh)
executes every one of those refusals against fabricated repositories.

## Every cut writes its notes

A release nobody can read is a release nobody adopts, so
[`docs/releases/<tag>.md`](releases/README.md) is a gate rather than a courtesy.
The cut checks for it before it spends the walkthrough, and a tag with no notes
is the one refusal that leaves something behind: it drafts them and stops.

```sh
make release VERSION=v0.3.1        # drafts docs/releases/v0.3.1.md and refuses
$EDITOR docs/releases/v0.3.1.md    # place each item; the judgement is yours
git add docs/releases/v0.3.1.md    # the draft is a new file, so -a will not do
git commit -m "v0.3.1 release notes"
git push origin main               # or a pull request, where main is protected
make release VERSION=v0.3.1        # green, and the tag carries its own notes
git push origin v0.3.1
```

The `git add` is not a flourish: the drafted notes are a file git has never seen,
so `git commit -a` stages nothing and stops with "no changes added to commit".

**The push is not optional, and it is not the tag push.** The cut
refuses a `HEAD` that is not where `origin/main` is, so a notes commit that
exists only in your checkout stops the *second* `make release` rather than the
first — and it stops it before the notes gate, so the message you get names the
remote rather than the notes. This repository protects `main` against direct
pushes, so its own notes commit reaches `main` the way every other change does,
through a branch and a merged pull request; the direct push above is the short
form for a repository that permits one. Either way the cut runs once
`origin/main` carries the notes. Only a checkout whose origin is unreachable
skips this, and the cut says that went unchecked rather than passing over it.
[`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh) executes this
loop against a scratch repository with a real remote, unpushed notes and all.

The draft comes from the tracker rather than the commit log:
[`scripts/release-notes.sh`](../scripts/release-notes.sh) reads the work items
closed between the previous tag and this one and carries their titles, their
types, and the goals they served. A commit message says what one change did; the
item behind it says what somebody wanted, which is the difference between notes
and a changelog. `make release-notes VERSION=<tag>` drafts one on its own, and
`bash scripts/release-notes.sh <tag> --print` shows one without writing it.

Only what the tracker calls **closed** reaches the notes. An id in a commit
message says work touched that item, not that the item is done — a parent epic
is named by every child's commit, and a multi-part item by each part as it lands
— so publishing either as shipped is the one lie this is careful about. Items
dropped for not being closed are counted in the output, alongside the tokens
that looked like ids and are not in the tracker at all, so neither exclusion is
silent.

Where the draft puts each item is placed from its type, and **that placement is
a starting point rather than an answer**: which work is key functionality, which
is an enhancement, and which fix is critical enough to go up to the top is the
product manager's judgement until the post-v1 release-manager role exists. The
three sections and their order are the operator's and are not the draft's to
change. [`scripts/release-notes-test.sh`](../scripts/release-notes-test.sh)
executes the placement rule, the section order, and the refusals against a
fabricated repository and a stub tracker.

The release workflow publishes that same file as the release page's body, with
the install preamble under it, so the release page and the repository tell one
story rather than two. [`scripts/release-body.sh`](../scripts/release-body.sh)
is the composition, kept as a script rather than inline in the workflow because
workflow YAML on a tag trigger first executes during a real publication; the
test above covers it, including what a tag with no notes file publishes.
