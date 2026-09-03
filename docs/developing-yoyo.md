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
runs. Those same four are what this project declares as its
[checks](configuration.md), and a run applies them one at a time before it will
let a change reach review or integration — so anything a run has to exercise has
to reach one of the four, and for content that is not Go that means `make test`.

## Before you change how a run works

[What the delivery pipeline actually guarantees](delivery-pipeline-baseline.md)
enumerates the paths a run can take and the boundaries it holds — the phases,
the pause cases, the shared repair budget and the counters beside it, the
reconciliation actions, and the terminal outcomes. It is the specification the
code never had, and it is kept true by golden traces that record what each path
actually produced, so a change in behavior shows up as a diff rather than as a
document nobody re-read.

## Where the build cache goes

The Go toolchain writes what it has compiled to `$GOCACHE`, which defaults to a
directory under your home. Every command above needs it before it compiles
anything, so an environment that does not grant writes there fails all four
checks at setup — `operation not permitted` on a path, and no mention of a cache
anywhere in the message — which reads as a broken toolchain rather than as a
directory nobody granted. An agent sandbox is exactly such an environment: it
grants writes to the worktree, to `.git`, and to `TMPDIR`, and to nothing else.

The harness sets `GOCACHE` for every run it makes, at `.git/yoyodyne/go-build`
in the repository the run works in. That directory is granted, it is outside the
working tree so it is not untracked content in anybody's checkout, and every
worktree of one repository shares it — so a run's own execution probe and the
checks the harness then applies to its change compile against one cache rather
than two. `internal/execution/gocache.go` is the whole of it, and it creates
nothing: it names a path, and the Go command creates its own cache.

Nothing sets it for an environment the harness did not make, so redirect it
yourself in one:

```sh
export GOCACHE="${TMPDIR:-/tmp}/go-build"
```

`make build`, `make test`, `make race`, and `make vet` refuse before they spend
anything when the cache cannot be written, and the refusal names that redirect,
so it costs a message rather than a diagnosis. `GOTMPDIR` is deliberately left
alone: it defaults into `TMPDIR`, which every environment that runs these grants
already, and the Go command refuses a `GOTMPDIR` that does not exist — naming
one would add a way to fail rather than remove one.

## What a surface may do with emphasis

This is the contract for anything that writes output an operator reads — a
command, a listing, the conversation, a run's closing lines, a message posted to
Slack, and whatever surface comes after them. It is recorded once, here, because
it was argued separately for the conversation, for the goals listing, and for the
reports listing, and three surfaces that agree by coincidence are not a
discipline. A new surface cites this section rather than deriving the rules
again.

The goal it serves is stated in [the v1 goals](product/goals/v1-goals.md#goals):
*the harness's surfaces read clearly: boundaries between topics and speakers are
visible, important findings stand out, and every distinction survives a terminal
that cannot render emphasis.* Every rule below is that sentence made checkable.

**A surface asks for a theme; it does not write an escape.**
`console.ThemeFor(out, os.Getenv)` is the whole of the question for a command,
and a conversation asks the console it opened. Both answer with a
`console.Theme` whose zero value dresses nothing, so a surface calls
`theme.Entry`, `theme.Detail`, `theme.State`, `theme.Severity`, `theme.Card`, or
`theme.Rule` unconditionally and the theme decides whether that is anything at
all. That is where a surface inherits the rest of this: no package outside
`internal/console` writes an escape into its output, and a surface reaching for
one has found something the theme should be taught rather than a licence to dress
itself.

**The words carry the meaning; the dressing only makes it findable.** Every
distinction the dressing draws is one the text already makes — a question ends in
a question mark, a group says `blocked (2)` in words, a goal that may no longer
be named is marked `[no longer in force]`, a proposal says what it is proposing.
The test is mechanical rather than a matter of taste: strip every escape from the
output and it must say everything it said dressed. If stripping loses a
distinction, the distinction was never in the text and the layout is what has to
change.

**Where the words will not carry it, put a mark in them.** A report's severity is
`!!` for critical and `!` for warning, in the column before the identifier, so a
pile can be scanned down its margin and a critical report does not read like a
note on a terminal that may not be dressed at all. Reaching for a louder colour
instead is how a distinction ends up living only in the decoration.

**Emphasis is spent, not spread.** A note is dressed as nothing on purpose: a
listing where every line is coloured has no emphasis left for the line that
matters. The same reasoning is why structure is weighted rather than recoloured —
a heading, a bold run, and a listing's entries are the text's own emphasis, not a
kind of thing the harness is telling apart, so they leave the colours to mean
what they mean.

**The vocabulary is named, and the theme decides what it looks like.** A surface
names `console.StateBlocked` or `console.SeverityCritical`; it does not choose an
orange. That is what keeps blocked work the same colour in `/status`, in a run's
closing lines, and in a listing, and it is why the set is fixed rather than free
text.

**Permission is all or nothing, and it is asked rather than assumed.**
`NO_COLOR`, a `TERM` that says `dumb` or says nothing, and a stream that is not a
terminal each suppress every escape there is — the colour, the rules, the cards,
the bell, the window title — and with them anything that depends on there being a
moment at which something unprompted can be written. Somebody who asked for an
undecorated conversation asked for all of it, which is why `Theme.Permitted` is
one question rather than several.

**Machine-readable output carries none of it.** `--json` states a severity as a
field, and dressing it would be corrupting the field. The same goes for anything
else written for a program to read.

**A surface that is not a terminal holds the same contract in its own
materials.** Slack has no ANSI, so severity is said in words there — a `critical`
says "Critical" — and an ordinary fact carries no marker at all, which is the
words carrying the meaning and emphasis being spent rather than spread, in a
medium that renders emoji instead of escapes. A future dashboard inherits the
reasoning, not the escape codes.

What an operator is told they will see is written where they read it — [the
conversation on a terminal](conversation.md#what-the-conversation-looks-like-on-a-terminal),
[the goals listing](artifacts.md#goals-and-what-work-serves-them), [what a
report looks like](reporting.md#what-agents-report-and-where-it-reaches-you), and
[severity in Slack](slack/setup.md#what-it-posts). Those describe what
one surface does; this section is the rule they are each an instance of, and it
is the one a new surface has to satisfy.

## What `test` checks besides the code

Some of what `make test` runs is not about the Go code at all: it reads this
repository's own documents, it executes the part of the build that is shell, and
it holds every other kind of file the repository carries to something, so a
mechanical defect in any of them fails a check instead of costing a reviewer a
paragraph or waiting for the day it matters. Each one exists because a reviewer
wrote that paragraph, more than once, and because the thing being checked is one
nobody can verify by eye — a relative path resolves only against the directory
layout, a `#fragment` names a heading through a slug nothing writes down, and no
Go check has ever run a line of bash.

| What fails | Where it lives | What it means |
| --- | --- | --- |
| A link in any Markdown file here resolving to nothing — a path that is not in the repository, or a fragment naming a heading the target does not carry | `internal/doclink` | Fix the link, or the heading it points at. Absolute URLs are not resolved: they are somebody else's to keep working, and reaching for one would put the network in a deterministic check. |
| A goal in an in-force goals document written across more than one physical line | `internal/cli` (`goals_repository_test.go`) | Rejoin the statement onto one line. The goal is recorded whole either way; what the check holds is that the words an attribution must match are what the file says outright. |
| A goal in an in-force goals document naming a brief goal the brief does not state | `internal/goal` (`goal_test.go`) | Correct the `*Supports: ...*` trailer, or the brief claim it names. What fails here is a document asserting something false about another one, which is what silently orphans the work attributed under it. The states beside it deliberately do not fail: a goal that has not said yet what it supports, a goals document that states no goals, a brief that states none, and a configured artifact home nobody has created are intent still being written rather than intent contradicted, and `yoyo goals list` reports each of them on stderr. Editing what the product intends must not be the thing that reddens a build. |
| A coined term in a document under `docs/product`, `docs/designs`, or `docs/decisions` that [the register](terms.md) does not define — or a register entry with no definition, no place of use, or a second row for a term already listed | `internal/terms` | Write the ordinary word, or add the entry. The register decides and the check only reports: adding a row permits a term and removing it refuses the term again, neither of which is a change to any code. Frontmatter and fenced blocks are not read — a revision's recorded reason is what somebody decided in their own words, and a fenced block is code. A term of more than one word is looked for however its parts are spaced, so a hyphen, a doubled space, or a line wrap between them does not get one past the check, and a row permits every spelling of its term rather than the one the row happens to write. What no check can recognize is a word coined this morning, which is why the reviewer persona carries the same rule as a finding class. |
| A place the harness enforces role authority that [the authority inventory](authority-inventory.md) does not list, or a listed check whose declaration has moved or been renamed | `internal/authority` | Add the row, or correct the one that moved. The inventory is the statement of what the harness authorizes today and the ground truth the capability registry re-expresses, so an authorization site nothing lists is authority nobody wrote down. The document decides and the check only reports: adding a row lists a check and moving one to the second table excuses it, neither of which is a change to any code. What the sweep can recognize is a floor — a function that names a role and refuses, a name carrying `authoriz` or `authorit`, and the `protect`, `independen`, and `lease` boundaries — so a check outside all of those is still a reviewer's to catch. |
| A row of [the authority inventory](authority-inventory.md) that the role-capability registry neither expresses as a capability question nor names as a gap | `internal/rolecapability` | Answer the row: write the question a call site would ask instead of the role's name, or the reason there is not one yet. The registry is the inventory said once more in capabilities, so a check nobody re-expressed is one the conversion would silently drop — and a gap written down is the honest half of the claim, which is why an unanswered row fails and a gap does not. |
| A place in the Go sources that reads what became of a directive — `Resolved()`, or the `ResolvedAt` behind it — that the audit in `internal/directive` (`disposition_audit_test.go`) does not list, or a listed reader that has moved, gone, or changed how many times it reads | `internal/directive` | Add the row, or correct the one that moved, saying what the read means where it sits. `Resolved` is has-a-disposition and `InForce` is still-applies: a standing instruction carries an outcome the moment somebody records what came of it and goes on applying until the operator withdraws it, so a filter that asked `Resolved` to find out what still constrains work would retire that instruction as soon as its first item was admitted, silently. The audit is the list of every reader and what each one meant by reading it; the sweep is a floor rather than a fence, because it recognizes the call and the field by name, and a reader that reaches the same question another way is still a reviewer's to catch. |
| A durable field that [the delivery-pipeline baseline](delivery-pipeline-baseline.md) states, that no recorded trace carries, and that its own not-covered section does not name | `internal/orchestrator` (`baseline_test.go`) | Record a trace that carries the field, or name it in the gap list. The document promises a parity harness that what it does not measure is written down, and that promise was broken three times running while every check was green, because nothing but a reader was holding it. The sweep is a floor: it recognizes field names, so an ordering or a refusal the document states in prose alone is still a reviewer's to catch. |
| A recorded delivery trace that no parity scenario walks, or a scenario whose transcript the trace does not evidence | `internal/orchestrator` (`parity_test.go`) | Write the transcript the trace evidences, name why no built-in definition can express that path, or fix the definition. The built-in workflow definitions are the delivery loop as data, and the only thing that makes them the pipeline rather than a plausible state machine is that every frozen path walks through them. A trace nothing walks is measured by nothing and looks exactly like one that passed. |
| A governed document whose place in the chain is wrong — a `supports` entry naming nothing, an artifact reaching no brief, or a revision recorded by a role that does not own the document | `internal/cli` (`artifact_repository_test.go`) | The harness reports these and never refuses a document over one; here they fail, because a warning nobody is made to read is how one of them breaks unnoticed. |
| A claim in the release verb's own suite, [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh), that no longer holds | `internal/cli` (`release_repository_test.go`) | Read the claim it named and fix `scripts/cut-release.sh`. The verb is shell, so no other check here executes it, and its value is entirely in cuts it refuses — a refusal first exercised on the day it was needed is one nobody had. |
| A claim in the notes writer's own suite, [`scripts/release-notes-test.sh`](../scripts/release-notes-test.sh), that no longer holds | `internal/cli` (`release_repository_test.go`) | Read the claim it named and fix `scripts/release-notes.sh` or `scripts/release-body.sh`. The same argument as the row above, for the other half of the release path: what a release page publishes would otherwise first execute during a publication. |
| The release verb committing a derived export that a run does not declare as churn the primary checkout may acquire | `internal/cli` (`release_repository_test.go`) | Either declare the path in `AllowedPrimaryChanges` as well, or take it back out of `derived_exports`. The containment is one-way on purpose: a run may come to tolerate a path the cut has no business committing on the operator's behalf, so widening the run's list alone is fine and widening the cut's alone is not. |
| A shell file a shell will not parse — every `.sh` here, the tools in `bin`, and the hooks the tracker installs | `internal/composition` | Fix the syntax. Parsing is `bash -n`, which reads a script and runs none of it, so it is safe to point at the release verb and the adoption walkthrough. It is the floor rather than the gate: shell with a suite gets executed as well. |
| A claim in the status tool's own suite, [`scripts/yoyo-status-test.sh`](../scripts/yoyo-status-test.sh), that no longer holds | `internal/composition` | Read the claim it named and fix [`bin/yoyo-status`](../bin/yoyo-status). The tool is shell and its claims are what the README and [the operations guide](operations.md) tell an operator they can rely on; the same argument as the two release rows above. |
| A YAML or JSON file that does not decode | `internal/composition` | Fix the file. What each one means belongs to whatever reads it — Claude Code, Codex, the tracker, the harness — but one that nothing can parse is this repository's defect whoever owns the schema, and it is not a defect a reviewer reading a diff reliably sees. |
| A workflow that is not shaped like one — no trigger, no jobs, or a job with no runner or no steps | `internal/composition` | Fix the workflow. Decoding is not enough for these: the release workflow is triggered by a tag push, so what is wrong with it would otherwise first misbehave during a real publication. |
| A file no content class recognizes, a class that recognizes nothing, or a class crediting its coverage to a check the project no longer declares | `internal/composition` | Write the class, retire it, or say what covers it now. This is the audit rather than a gate: it holds what this repository is made of against what its declared checks actually exercise, so a new kind of content cannot arrive covered by nothing and unnoticed — which is how shell got here. |

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

## What the checks cover, and what they own up to not covering

The four declared checks are Go commands, so on their own they read none of the
shell, Markdown, workflow YAML, or Makefile this repository is also made of. A
change made entirely of those passed every gate a run applied with nothing having
exercised it, which is not a gate that was weak — it is a gate that never ran.

`internal/composition` is where that is written down. Every file the repository
carries belongs to a content class there, and every class records either the
declared checks that exercise it and what those checks do with it, or why
nothing does. "Nothing exercises this, and here is why" is a legitimate answer —
a `.png` avatar's defect is how it looks, and the tracker's `.jsonl` exports are
rewritten wholesale from a store that is authoritative elsewhere. What is not
legitimate is the class going unwritten, and that is what the audit holds: a
file no class recognizes fails, so the question gets asked rather than skipped.
So does a class crediting its coverage to a check the configuration no longer
declares, which is the same loss arriving from the other direction.

The census is git's own list — what the repository tracks, plus what it carries
untracked and is not ignoring. Both halves matter. A directory walk would find
build output and scratch files, and a gate failing on those is one people learn
to run with a flag; tracked files alone would miss a run's own new files, which
are untracked at the moment its checks run, and a run introducing a new kind of
content is the case the audit is for.

## Cutting a release

`make release VERSION=<tag>` is that build with its gate in front, so a daily
cadence costs two commands rather than a procedure once
[this tag's notes are on `main`](#every-cut-writes-its-notes):

```sh
make release VERSION=v0.3.0
git push origin v0.3.0
```

It gates on [this release's notes](releases/README.md), on [release
readiness](#release-readiness), walks [the documented adoption
path](../scripts/walk-adoption.sh), runs
`check`, builds and verifies the archives for `<tag>`, then tags the commit
they were built from — in that order, so a red gate refuses the cut, names what
was red, and leaves nothing to undo. It also refuses a tag that is not
`vMAJOR.MINOR.PATCH` or that already exists, a dirty working tree, a checkout
that is not on `main`, and a `HEAD` that is not where `origin/main` is; where
origin is unreachable it says that last one went unchecked rather than passing
over it.

Two things are written before the tag, in one housekeeping commit placed after
the last gate is green. The tracker's own exports —
`.beads/interactions.jsonl` and `.beads/issues.jsonl` — do not count as a dirty
tree: they are derived from a store that is authoritative elsewhere, nothing a
release ships is built from them, and the walkthrough this gate runs rewrites
them itself, so refusing on them would stall most days of a daily cadence. The
readiness result is stamped into `docs/releases/<tag>.md` and goes into the same
commit, so the notes the tag names carry the conformance result of the tree it
names rather than one taken on whichever day the notes were drafted; it is
written only where it differs from the section the notes already carry, so a cut
that changes neither it nor the exports has nothing to commit. Committing both
rather than excepting them keeps the tag naming a tree with nothing uncommitted
in it. On a day it had to make that commit it prints
`git push --atomic origin main <tag>`, because origin does not have it and the
branch has to carry it; on a day it had nothing to commit, `git push origin
<tag>`.

That commit is made with hooks turned off, since a tracker installs a hook that
exports after every commit and it would rewrite the very files the commit
exists to clean. Turning them off is `core.hooksPath`, which git honours from
2.9, so 2.9 is the verb's floor and its header says so: older git ignores the
option rather than refusing it, and the hook would run.

It stops at the tag. Publishing is the `git push`, which is the irreversible
half and what the release workflow acts on, so it stays something you do
deliberately. [`scripts/cut-release-test.sh`](../scripts/cut-release-test.sh)
executes every one of those refusals against fabricated repositories, and
`make test` runs it, so changing the verb is checked by the same command as
changing anything else.

## Release readiness

`make check` says the code does what its tests say. A tag says more than that:
that the system still matches what it records about itself. So the cut runs
[`yoyo conformance`](artifacts.md#asking-all-of-this-at-once-before-a-tag)
before it spends the walkthrough — the artifacts and their references, the links
the documentation makes to itself, the invariants, and every admitted work
item's attribution to a goal, with staleness surveyed alongside and refusing
nothing. A divergence refuses the tag and names every mismatch the check that
found it collected; nothing is written.

The sequence those checks run in is not in the code. It is a
[workflow definition](configuration.md#the-release-readiness-workflow) — data in
the project-owned format, selecting actions the harness registered in Go —
validated and compiled before the first check runs, and walked by the workflow
runtime one durable transition at a time. It is the first sequence this harness
runs that never existed as Go control flow, which is the point of it: the same
checks, ordered by a file a project can read and replace, and no way for that
file to make the gate perform anything but reads.

The cut asks for it as the Markdown section a release's notes carry, and there
is one invocation rather than two — a gate read one way and a notes section
written from a second run would be two results, and only one of them would be
the one that refused or did not. On a green cut that section is stamped into
`docs/releases/<tag>.md` between two HTML-comment markers and committed with the
tag's housekeeping, replacing an earlier one rather than accumulating beside it,
and leaving everything the product manager wrote around it alone.

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
fabricated repository and a stub tracker, and `make test` runs it.

The release workflow publishes that same file as the release page's body, with
the install preamble under it, so the release page and the repository tell one
story rather than two. [`scripts/release-body.sh`](../scripts/release-body.sh)
is the composition, kept as a script rather than inline in the workflow because
workflow YAML on a tag trigger first executes during a real publication; the
test above covers it, including what a tag with no notes file publishes.
