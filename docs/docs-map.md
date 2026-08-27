# The documentation map

Drafted for yoyodyne-ifd.121.1. **Status: draft — the architect's voice on
anchor stability and cross-link structure, and the operator's approval, are
still outstanding.** It lives outside the artifact homes for the same reason
[team mode scope](team-mode-scope.md) does: every document inside one carries
identity frontmatter, and no governed kind exists yet for a structural
decision. When the artifact contract grows one, this document moves under it.

This is the single structure decision behind two splits — the README split
(yoyodyne-ifd.121.2) and the configuration guide split (yoyodyne-ifd.117). It is
one document rather than two so the link map lands once and the README never
links into an anchor its sibling split then moves. Each execution run cites the
section here that it implements, and does not decide structure of its own.

The problem it answers: at drafting the README was 152KB across 2,602 lines and
the configuration guide 132KB across 2,377. Both have grown since — re-measured
on 2026-08-24 for yoyodyne-ifd.166, the README is 3,792 lines and the
configuration guide 2,989. The adoption goal — *a newcomer can go from the
documented install to a working first run on their own repository using the
readme alone* — is failed by a document nobody reads to the end, and the
configuration guide costs more than a whole context budget to include.

## What each document is for

Audience is the split's only organizing principle. A section moves to the
document whose reader needs it, not to the one whose subsystem it names.

| Document | Audience | Roughly |
|---|---|---|
| [`README.md`](../README.md) | Someone deciding whether to use yoyo, and then reaching a first run | 500 lines |
| `docs/conversation.md` | An operator driving work from `yoyo chat` | 816 lines |
| `docs/work.md` | An operator who has approved work and wants to know what happens to it | 270 lines |
| `docs/artifacts.md` | An operator maintaining the brief, goals, designs, and invariants | 246 lines |
| `docs/reporting.md` | An operator asking what the work cost and what came back | 244 lines |
| `docs/operations.md` | An operator recovering from a stall, a crash, or a provider refusal | 454 lines |
| `docs/developing-yoyo.md` | Someone changing yoyo itself | 30 lines |
| [`docs/configuration.md`](configuration.md) | Anyone arriving at a configuration link — an index and the frozen anchors | 120 lines |
| `docs/configuration/setup.md` | Someone writing, inheriting, or validating a project configuration | 524 lines |
| `docs/configuration/artifacts.md` | Someone configuring artifact homes, approval, and ownership | 549 lines |
| `docs/configuration/goals.md` | Someone configuring admission, attribution, and staleness | 440 lines |
| `docs/configuration/runs.md` | Someone configuring checks, scheduling, and what a run costs | 343 lines |
| `docs/configuration/publishing.md` | Someone configuring pull requests, branches, and merges | 226 lines |
| `docs/configuration/recovery.md` | Someone configuring triage thresholds and provider-refusal waits | 513 lines |
| `docs/configuration/agents.md` | Someone configuring the agents and the humans: accounts, operators, personas, exchanges, research sources, and Slack | 370 lines |

Line counts are the current content's, carried across. They are a size budget
for review, not a target to write to: a run that lands a document materially
larger than its budget has found content the map misplaced, and should say so
rather than pad or trim to fit.

**The seven `docs/configuration/` budgets were re-measured on 2026-08-24** for
yoyodyne-ifd.166, and they are the sums of the reconciled disposition table
below rather than the drafting-time figures: every one of the seven moved,
because half the section counts they were summed from were undercounts and four
sections had no row at all. The `docs/configuration.md` row is the exception —
it is an estimate for prose the split writes rather than content carried across,
so it is left where drafting put it.

**The six README-split documents have landed**, and their budgets are spent
rather than pending: `conversation.md` at 1,189 lines against 816,
`operations.md` at 754 against 454, `work.md` at 445 against 270,
`reporting.md` at 444 against 244, `artifacts.md` at 306 against 246, and
`developing-yoyo.md` at 162 against 30. Every one overran, mostly because the
README itself grew by roughly 1,200 lines between drafting and execution. They
are recorded here as landed facts; nothing should be trimmed to reach the
figure in the table.

One document is reachable from the index below and is deliberately not a row
here: [`docs/releases/README.md`](releases/README.md), which explains the notes
home one file per tag lives in. It carries none of the content this map moves,
so it has no budget and no destination — it is registered in the further-reading
index so the split does not land a README that cannot reach it.

## The moved-anchor policy

Stated once, up front, so no execution run discovers it. Three tiers, and the
tier is a property of who cites the anchor rather than of what the section says.

**Tier 1 — frozen.** The anchor survives at its current path forever, as a
redirect stub: a heading with the same slug whose body is one sentence and a
link to the canonical home. Never renamed, never deleted, even when the content
under it has moved. An anchor is Tier 1 when it is cited by something this
repository cannot rewrite:

- **Shipped artifacts**, which are on disks we do not control or in text already
  published. Two today:
  - `docs/configuration.md#checks` — `internal/config/scaffold.go:360`, asserted
    by `internal/cli/init_test.go:214`. Every `.yoyodyne/config.yaml` that
    `yoyo init` has ever generated points at it, in a file its owner edits.
  - `README.md#getting-started` — `.github/release-notes-preamble.md:28`, as the
    repo-root URL `https://github.com/mason-bryant/yoyodyne#getting-started`.
    `scripts/release-body.sh` appends that preamble to the tag's own
    `docs/releases/<tag>.md`, and `.github/workflows/release.yml` passes the
    result to `gh release create --notes-file`. It is in the notes of every
    release already published, and published release notes cannot be corrected
    by a change to this repository.
- **Tracked work items.** The backlog is upstream: the product manager owns it,
  and no developer rewrites an item to chase a link. Today that is
  `docs/configuration.md#product-specifications`, cited by yoyodyne-ifd.20.2 and
  yoyodyne-ifd.21 — both closed, so the cost of dangling would be archaeological
  rather than live, but the rule holds on who may rewrite the citation rather
  than on how much it would hurt.
- **Protected artifact homes** — `docs/product/`, `docs/designs/`,
  `docs/decisions/`, and `.yoyodyne/`. A developer on this effort may not edit a
  file in one, so an anchor cited from inside one is Tier 1 by construction: the
  only alternative is a proposal to an owner who may decline it, which would
  leave the link dangling in the meantime. Today that is
  `docs/configuration.md#product-specifications` again, cited by
  `docs/designs/v1-harness-design.md:243` and `docs/product/goals/README.md:11`.

**Tier 2 — updated in the same change.** The anchor is cited only by files in
this repository, so the run that moves the section rewrites every citation in
the same commit. No stub; a stub for an anchor we can fix is debt with no
creditor. Every Tier 2 citation is enumerated below.

**Tier 3 — free.** Nothing cites the anchor. Move it without ceremony.

**The rule underneath all three: no change in this effort may leave a fragment
that resolves to nothing.** A moved anchor is either frozen or re-cited, and
"the link still opens the right file, just at the top of it" does not count —
GitHub ignores an unknown fragment silently, so a reader lands on an index with
no sign anything went wrong. That is the failure mode this policy exists to
prevent, and it is invisible to exactly the reviewer who would catch a 404.

**Uniqueness is not required across documents.** `## What a change upstream
leaves stale` and `## Architectural invariants` each exist today in both the
README and the configuration guide, and after the split each pair lands in two
different files. That is fine and intended: one is the narrative for an
operator, the other is the reference for someone editing configuration. Each
must link to the other, so a reader who arrives at the wrong one gets to the
right one in a click.

## Every anchor citation, and where it goes

Four sets, and all four are enumerated: citations of `docs/configuration.md`
anchors, citations of `README.md` anchors, and the links each of the two
documents makes into itself. Nothing is scoped out. The method that produced
them is stated with each set so the executing run can re-derive rather than
trust, because anything landed between drafting and execution adds rows.

### Citations of `docs/configuration.md` anchors

With the document each moves to and its tier. **Re-derived on 2026-08-24** for
yoyodyne-ifd.166 — every line number in the drafting-time version of this table
was stale, because the README grew and the six README-split documents landed
between drafting and now, and three cited anchors had no row. Re-derive it with
a search for `configuration.md#` across the tree; every row below except the two
Tier 1 ones is Tier 2, cited only by files this repository may rewrite.

| Anchor | Cited by | New home |
|---|---|---|
| `#checks` | `README.md:370`, `internal/config/scaffold.go:378`, `internal/cli/init_test.go:301` | **Tier 1 stub stays**; canonical `configuration/runs.md#checks` |
| `#product-specifications` | `docs/designs/v1-harness-design.md:261`, `docs/product/goals/README.md:31`, and yoyodyne-ifd.20.2 and yoyodyne-ifd.21 in the tracker (`.beads/issues.jsonl:119`, `:121`) | **Tier 1 stub stays**; canonical `configuration/artifacts.md#product-specifications` |
| `#what-reaches-the-queue` | `README.md:755`, `:803`, `:845`, `:908`, `:1395`, `:2642`, `docs/conversation.md:155`, `:203`, `:245`, `:308`, `:685`, `docs/artifacts.md:44` | `configuration/goals.md` |
| `#where-the-tracker-syncs` | `README.md:328`, `:2446` | `configuration/setup.md` |
| `#what-one-work-item-has-been-given` | `README.md:1577`, `:3579`, `docs/conversation.md:863`, `docs/operations.md:665` | `configuration/recovery.md` |
| `#publishing-through-pull-requests` | `README.md:524`, `:2422`, `docs/work.md:445` | `configuration/publishing.md` |
| `#what-the-product-manager-sees-besides-them-and-what-it-does-not` | `README.md:688`, `docs/conversation.md:88` | `configuration/artifacts.md` |
| `#traceability-references-and-orphans` | `README.md:2683`, `docs/artifacts.md:85` | `configuration/goals.md` |
| `#scheduling-ready-work` | `README.md:2205`, `docs/work.md:228` | `configuration/runs.md` |
| `#protected-paths-in-a-developers-change` | `README.md:1653`, `:2062`, `docs/conversation.md:939`, `docs/work.md:46` | `configuration/artifacts.md` |
| `#personas` | `README.md:674`, `docs/conversation.md:74` | `configuration/agents.md` |
| `#operators` | `README.md:3771`, `docs/slack/setup.md:191` | `configuration/agents.md` |
| `#losing-a-race-for-the-target-branch` | `README.md:3564`, `docs/operations.md:650` | `configuration/publishing.md` |
| `#how-long-a-check-may-take` | `README.md:382` | `configuration/runs.md` |
| `#avatars` | `docs/slack/setup.md:170` | `configuration/agents.md` |
| `#approving-a-document` | `README.md:2649`, `docs/artifacts.md:51` | `configuration/artifacts.md` |
| `#research-sources` | `README.md:725`, `docs/conversation.md:125` | `configuration/agents.md` |
| `#how-long-one-role-may-ask-another` | `README.md:1487`, `docs/conversation.md:777` | `configuration/agents.md` |
| `#when-the-repository-ignores-the-configuration` | `README.md:406` | `configuration/setup.md` |

The last three rows are the ones the drafting-time sweep missed, and they are
the citations of three of the four sections that had no disposition row at all.
Two of the three are cited from `docs/conversation.md`, which did not exist when
this map was drafted — which is the standing argument for re-deriving this table
at the top of each execution run rather than trusting it.

### Citations of `README.md` anchors

Twenty-two README sections move out to seven new documents, so this set has to
be established rather than assumed. It was swept three ways: every
`README.md#…` reference in any case anywhere in the tree; every GitHub URL for
this repository carrying a fragment, which is how a link to the README's
rendered root is written; and every README slug whose section moves, searched
literally as `#slug` across the working tree and the tracker's current export.

The result is small, and worth stating flatly because the map's later claim that
the README keeps four things and needs no stub of its own depends on it. **The
sweep was re-run on 2026-08-24** for yoyodyne-ifd.166 and this table is its
result; every line number in the drafting-time version had moved, and it had two
rows where it now has five.

| Anchor | Cited by | Tier | New home |
|---|---|---|---|
| `#talking-to-the-other-agents` | `docs/configuration.md:225` | 2 | `conversation.md#talking-to-the-other-agents` |
| `#keeping-the-configuration-out-of-the-repository` | `docs/configuration.md:145` | 2 | merges into the `docs/configuration.md` index with the rest of `## Configuring a project` |
| `#getting-started` | `.github/release-notes-preamble.md:28` | **1** | stays in the README — see below |
| `#further-reading` | `skills/yoyo-setup/SKILL.md:21`, and the back-link each of `docs/conversation.md:4`, `work.md:4`, `artifacts.md:4`, `reporting.md:4`, `operations.md:4`, `developing-yoyo.md:4`, and `releases/README.md:4` opens with | — | section stays; no move to service |
| `#3-yoyo-chat--establish-the-brief-and-the-goals` | `skills/yoyo-setup/SKILL.md:217` | — | section stays; no move to service |

**Two of those five name a section that moves, and both are cited from
`docs/configuration.md`** — so both are Tier 2, and both are rewritten by the
configuration split rather than by the README one, which is the sequencing worth
noticing. The other three name sections that stay. No work item, design,
`docs/product/`, `docs/slack/`, or Go source file cites a README anchor at all;
`skills/yoyo-setup/SKILL.md` does, twice, and both of its targets stay.
`#getting-started` is still the sole Tier 1 README anchor, and the section it
names stays — so the README acquires no stub.

Two near-misses the sweep turned up, recorded so a re-derivation does not
re-litigate them. Five intra-file links in `docs/configuration.md` match README
slugs as prefixes — `#artifact-identity-and-metadata` (:379, :385, :413),
`#architectural-invariants` (:490), `#what-a-change-upstream-leaves-stale`
(:1551) — but each resolves against `configuration.md`'s own headings at :469,
:1270 and :1215, so they belong to the set below rather than to this one. And
yoyodyne-ifd.54 and yoyodyne-ifd.1.2 mention README anchors in the recorded
prose of closed review findings, not as links anything resolves.

That ifd.54 note is worth reading, because it is this policy's precedent: it
records that a link "to `#releasing-a-usage-limit-wait-early` or
`#waiting-out-a-provider-usage-limit` is now dead" after a README rename, and
tells a later run to grep for both old anchors. README anchors have been broken
by a rename here before, and were found by hand afterwards rather than by a
check.

### Links each document makes into itself

An intra-file `](#slug)` link survives a split only if its target lands in the
same new document. Both sources have many, and they are the largest category by
count in this effort — 91 links, re-counted on 2026-08-24 and up from the 67 at
drafting — so they are the likeliest thing for an execution run to miss.

**The README links into itself 54 times across 32 anchors.** Six anchors stay
put — `#install`, `#getting-started`, the three numbered step headings, and
`#optional-publishing-and-auto-merge` — because their sections stay. The other
26 become relative links into the new documents, resolved from the disposition
table below; two of those 26, `#configuring-a-project` and
`#keeping-the-configuration-out-of-the-repository`, point at the section that
merges into the configuration index rather than at one that moves whole.

**`docs/configuration.md` links into itself 37 times across 24 anchors**, and
after the split most cross a document boundary. Resolve each against the
configuration disposition table: a link whose target lands in the same new
document stays a bare `#slug`; one whose target lands elsewhere becomes a
relative link. The anchors are `#artifact-identity-and-metadata` (×3),
`#who-may-change-an-artifact` (×3),
`#proposing-a-change-to-a-document-you-do-not-own` (×3),
`#what-one-work-item-has-been-given` (×3), `#what-reaches-the-queue` (×2),
`#approving-a-document` (×2), `#goals-and-the-work-attributed-to-them` (×2),
`#traceability-references-and-orphans` (×2), `#operators` (×2), and one each of
`#extending-a-built-in-bundle`, `#what-init-proposes-for-checks`,
`#what-fails-closed`, `#protected-paths-in-a-developers-change`, `#personas`,
`#architectural-invariants`, `#product-specifications`,
`#watching-instead-of-draining`, `#what-a-change-upstream-leaves-stale`,
`#how-long-a-check-may-take`, `#relaunching-a-run-the-provider-killed`,
`#losing-a-race-for-the-target-branch`, `#provider-accounts`,
`#reporting-to-slack`, and `#layout`.

Three of those anchors — `#provider-accounts`, `#reporting-to-slack`, and
`#layout` — are new to this list, and `#provider-accounts` is the one that would
have broken silently: it is linked from `## Layout`, which lands in
`configuration/setup.md`, while its target lands in `configuration/agents.md`,
so it has to become a relative link and the drafting-time list did not name it
at all.

## What the README becomes

The README keeps four things and links out for everything else: the value
proposition, the testimonials, everything a newcomer needs to reach a working
first run, and the index. It keeps **nothing extra to service a link** — no
Tier 1 stub of its own — and that is a result of the sweep above rather than an
assumption: nothing outside the README cites a README anchor whose section moves.

Install and Getting started **stay in the README**, for two independent reasons.
The first is the goal: a newcomer reaches a working first run *using the readme
alone*, and a README that sends someone to another document between installing
and running fails that goal in its own words, however brief it reads. The second
is that `#getting-started` is Tier 1 — every published release's notes link to
it — so that section is pinned where it is whatever the first argument decides.
A later restructure that finds the goal argument unpersuasive still may not move
it without leaving a stub.

This is the one place the map spends its size budget deliberately: the README
lands at roughly 500 lines rather than the 150 a pure landing page would be, and
the sections below the quick start are what carry the weight of the reduction.

Target order:

1. `# yoyo` — the value proposition, unchanged (README.md:1–31).
2. **User testimonials**, unchanged (:32–49).
3. **Three gates**, unchanged (:50–64) — the shortest honest statement of what
   makes the thing trustworthy, and it belongs before the quick start.
4. **You drive it from one conversation** (:65–83), trimmed to the paragraph and
   its command names, with the links out repointed at the new documents.
5. **Quick start**, unchanged (:84–98).
6. **What exists today is bounded** (:99–121), unchanged — the bounds are worth
   knowing before you start rather than after, and that reasoning is unaffected.
7. `## Install` — unchanged (:122–192).
8. `## Getting started` — the three steps, `Optional: publishing and auto-merge`,
   unchanged except for repointed links (:193–442).
9. `## Further reading` — replaced by the index below.

`### Working on yoyo itself` leaves the README for `docs/developing-yoyo.md`. It
is contributor material, and the README's reader is not yet a contributor.

The new index, which is the link map made concrete and the only genuinely new
prose the README split writes:

```markdown
## Further reading

**Driving the work**
- [The conversation](docs/conversation.md) — proposals and batches, steering,
  directives, the other agents, and what the conversation looks like on a terminal.
- [How work flows](docs/work.md) — what happens after you approve an item, letting
  the harness choose, reviewing a branch, and publishing.
- [What comes back to you](docs/reporting.md) — what the work cost, what agents
  report and propose, and reporting into Slack.

**Your written intent**
- [Artifacts, goals, and invariants](docs/artifacts.md) — artifact identity, the
  goal a work item names, what a change upstream leaves stale, and architectural
  invariants.

**When something goes wrong**
- [Operations and recovery](docs/operations.md) — pausing and resuming, provider
  limits and stalls, recovering interrupted runs, and following a run.

**Reference**
- [The configuration guide](docs/configuration.md) — the full reference, split by
  audience, with an index.
- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture,
  the artifact and agent models, and the Git model.
- [Reporting into Slack](docs/slack/setup.md) — an empty workspace to live
  reporting in threads.
- [Release notes](docs/releases/README.md) — one file per tag, what each section
  is for, and how a cut drafts one from the work that landed.
- [`docs/product/`](docs/product) — the brief and goals the product manager reads.
- [Working on yoyo itself](docs/developing-yoyo.md) — the checks, the build, and
  what a release is.
```

### Disposition of every current README section

Every section, with its destination. Nothing is dropped; a run that finds
content with no row here stops and reports rather than choosing a home.

**This table has not been reconciled since drafting, and its counts are the
README as it stood at 2,602 lines rather than the 3,792 it stands at now.**
yoyodyne-ifd.166 reconciled the configuration table below; the README one was
outside that item and is left as drafted. Six sections the README has acquired
since have no row — `### Bringing it an idea rather than a work item`,
`### Roles asking each other things`, `#### Where the money went`,
`#### Measuring the reviewer against itself`,
`### Who reads them, and what became of each one`, and
`### Keeping the configuration out of the repository`. The first five are
already extracted and landed in `conversation.md`, `reporting.md`, and
`work.md`, so what is missing here is the record rather than the content; only
`### Keeping the configuration out of the repository` has no landed home, and it
is a child of `## Configuring a project`, which merges into the configuration
index. A run trimming the README works from the landed documents and this note,
not from the counts below alone.

| Current section | Lines | Destination |
|---|---|---|
| `# yoyo` (opening, testimonials, gates, quick start, bounds) | 121 | **stays** |
| `## Install` | 71 | **stays** |
| `## Getting started` + steps 1–3 + `Optional: publishing and auto-merge` | 250 | **stays** |
| `### Working on yoyo itself` | 27 | `developing-yoyo.md` |
| `## The conversation` | 134 | `conversation.md` |
| `### Proposals, and deciding them in batches` | 116 | `conversation.md` |
| `### Steering the work from the conversation` | 166 | `conversation.md` |
| `### What the work cost` | 92 | `reporting.md` |
| `### Directives, and the work they pause` | 85 | `conversation.md` |
| `### Talking to the other agents` (+ `#### Deciding what becomes of stopped work`) | 160 | `conversation.md` |
| `### What the conversation looks like on a terminal` | 96 | `conversation.md` |
| `### What agents report, and where it reaches you` | 62 | `reporting.md` |
| `### What agents propose changing, and who decides` | 50 | `reporting.md` |
| `### How fresh the conversation's picture is, and how to refresh it` | 59 | `conversation.md` |
| `## How work flows once you approve it` | 69 | `work.md` |
| `### Letting the harness choose the work` | 100 | `work.md` |
| `### Reviewing what a branch adds up to` | 43 | `work.md` |
| `### Publishing, and the merge that follows it` | 58 | `work.md` |
| `## Configuring a project` | 90 | **merged** into `docs/configuration.md`'s index — see below |
| `## Artifact identity` | 82 | `artifacts.md` |
| `## Goals, and what work serves them` | 64 | `artifacts.md` |
| `## What a change upstream leaves stale` | 40 | `artifacts.md` |
| `## Architectural invariants` | 60 | `artifacts.md` |
| `## Operations and recovery` + all children except Slack | 454 | `operations.md` |
| `### Reporting into Slack` | 40 | `reporting.md` |
| `## Further reading` | 13 | **stays**, replaced by the index above |

`## Configuring a project` is the one section that merges rather than moves. It
is a narrative retelling of what `docs/configuration.md`'s own opening already
says, and keeping both is an invitation to drift that has no reader on its side.
The run that moves it reconciles the two into the configuration index and leaves
the README pointing at it.

## What the configuration guide becomes

`docs/configuration.md` **stays at that path**. It becomes a short index — what
a project configuration is, what owning it costs, and the map of the seven
documents — plus the Tier 1 redirect stubs. Keeping the path is not a
convenience: it is what makes the frozen anchors above possible at all.

**This table was reconciled against `docs/configuration.md` on 2026-08-24** for
yoyodyne-ifd.166, and it was not the exhaustive enumeration it presents itself
as before that: four sections had no row, one was misassigned, and sixteen of
its thirty-one rows undercounted — `## Triage thresholds` by 181 lines. It now
has a row for each of the 48 headings the file carries outside its fenced
examples — grouped as 35 rows, since a parent and its children move together —
and the counts sum to the file's 2,989 lines exactly, which is the arithmetic
that makes "nothing is dropped" checkable rather than asserted. The bracketed
span beside each count is that section's line range in `configuration.md` as it
stood at reconciliation. Re-derive the whole table by listing the file's `##`
and `###` headings, ignoring the ones inside fenced examples, and partitioning
the file between them; a heading with no row here means the file has moved on
and the run that found it should say so rather than choose a home.

| Current section | Lines | Destination |
|---|---|---|
| `# Yoyodyne configuration` (opening, what owning defaults costs) | 24 (1–24) | **stays** as the index opening, merged with the README's `## Configuring a project` |
| `## Creating a project configuration` | 36 (25–60) | `configuration/setup.md` |
| `### When the repository ignores the configuration` | 30 (61–90) | `configuration/setup.md` |
| `### Where the tracker syncs` | 31 (91–121) | `configuration/setup.md` |
| `## Layout` | 106 (122–227) | `configuration/setup.md` |
| `## Discovery` | 40 (228–267) | `configuration/setup.md` |
| `## Precedence` | 79 (268–346) | `configuration/setup.md` |
| `## Merge and removal semantics` (+ `### What fails closed`) | 92 (2518–2609) | `configuration/setup.md` |
| `## Extending a built-in bundle` (+ `### Converting an inheriting configuration…`) | 51 (2880–2930) | `configuration/setup.md` |
| `## Migrating from .yoyodyne.yaml` | 16 (2931–2946) | `configuration/setup.md` |
| `## Inspection` | 43 (2947–2989) | `configuration/setup.md` |
| `## Product specifications` (+ `### What the product manager sees…`) | 122 (347–468) | `configuration/artifacts.md` |
| `## Artifact identity and metadata` | 113 (469–581) | `configuration/artifacts.md` |
| `### Approving a document` | 63 (582–644) | `configuration/artifacts.md` |
| `### Who may change an artifact` | 47 (765–811) | `configuration/artifacts.md` |
| `### Protected paths in a developer's change` | 113 (812–924) | `configuration/artifacts.md` |
| `### Proposing a change to a document you do not own` | 91 (925–1015) | `configuration/artifacts.md` |
| `### What reaches the queue` | 120 (645–764) | `configuration/goals.md` |
| `### Traceability: references and orphans` | 44 (1016–1059) | `configuration/goals.md` |
| `### Goals, and the work attributed to them` | 155 (1060–1214) | `configuration/goals.md` |
| `### What a change upstream leaves stale` | 55 (1215–1269) | `configuration/goals.md` |
| `## Architectural invariants` | 66 (1270–1335) | `configuration/goals.md` |
| `## Checks` (+ `### What init proposes`, `### How long a check may take`) | 155 (1336–1490) | `configuration/runs.md` |
| `## Scheduling ready work` (+ `### Watching instead of draining`, `### When a configuration change takes effect`, `### Why each run says why it was there`) | 188 (1491–1678) | `configuration/runs.md` |
| `## Publishing through pull requests` (+ its three children) | 185 (1679–1863) | `configuration/publishing.md` |
| `## Losing a race for the target branch` | 41 (2026–2066) | `configuration/publishing.md` |
| `## Waiting out a provider that refuses` | 116 (1864–1979) | `configuration/recovery.md` |
| `## Relaunching a run the provider killed` | 46 (1980–2025) | `configuration/recovery.md` |
| `## Triage thresholds` (+ `### What one work item has been given`) | 351 (2167–2517) | `configuration/recovery.md` |
| `## How long one role may ask another` | 40 (2067–2106) | `configuration/agents.md` |
| `## Research sources` | 60 (2107–2166) | `configuration/agents.md` |
| `## Provider accounts` | 38 (2610–2647) | `configuration/agents.md` |
| `## Operators` | 74 (2648–2721) | `configuration/agents.md` |
| `## Personas` | 43 (2837–2879) | `configuration/agents.md` |
| `## Reporting to Slack` (+ `### Avatars`) | 115 (2722–2836) | `configuration/agents.md` |

Five rows changed destination or arrived in this reconciliation, and each is a
judgement rather than arithmetic, so each is stated:

- **`### When the repository ignores the configuration`** is a child of
  `## Creating a project configuration` describing what `init` and
  `config validate` warn about. It goes where its parent goes.
- **`## Provider accounts`** and **`## Research sources`** go to
  `configuration/agents.md`: one names the account each agent runs under, the
  other names the commands a conversational role's questions are answered by.
  Both are configuration of the agents rather than of a run.
- **`## How long one role may ask another`** is the section that belonged to no
  document at all, and it goes to `configuration/agents.md` for the same reason:
  `exchange.max_rounds` bounds one agent asking another, which is a property of
  the agents rather than of the work. It is cited from `README.md:1487` and
  `docs/conversation.md:777`, so a tranche that dropped it would have left two
  live fragments resolving to nothing.
- **`## Merge and removal semantics`** (+ `### What fails closed`) moves from
  `configuration/publishing.md` to `configuration/setup.md`. It was misassigned
  by its title: its content is how `extends` layers combine and what refuses a
  configuration at load, not anything about a git merge. It belongs beside
  `## Precedence` and `## Extending a built-in bundle`, and moving it makes
  `configuration.md:224`'s link to `#what-fails-closed` an intra-document link
  rather than a cross-document one.

The index at `docs/configuration.md` carries, verbatim as headings so the slugs
survive:

```markdown
## Checks

Moved to [`configuration/runs.md`](configuration/runs.md#checks). This heading
stays so the link in every generated `.yoyodyne/config.yaml` keeps resolving.

## Product specifications

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#product-specifications).
```

## What the split breaks that neither item mentions

**The product manager stops being given the content that moves.**
`internal/contextbundle/product.go:80` carries the shipped documentation as a
hardcoded set — `[]string{"README.md", "docs/configuration.md"}` — deliberately
a named set rather than a walk, so that a walk does not sweep the design
document in. After this split those two files are an index and a landing page,
and everything the product manager reads them *for* lives in thirteen documents
that set does not name.

This is the same failure the comment above that variable already records: ifd.20
narrowed the product manager's view, and the cost came due when it drafted a
work item that mis-assumed which surfaces existed. Doing it again by accident,
through a documentation restructure, would be worse than doing it on purpose.

So: **`shippedDocumentation` grows to name every document in this map's table**,
and that is part of the execution work rather than a follow-up. The run that
lands the last split document lands the list. **Six of those documents have
landed and the list has not moved** — `conversation.md`, `work.md`,
`artifacts.md`, `reporting.md`, `operations.md`, and `developing-yoyo.md` all
exist and none of them is named. Nothing is lost yet only because the README
still carries the same content; the tranche that trims it is the one that turns
this from a pending edit into the ifd.20 failure repeated. The set stays explicit — this map
is the enumeration it needs, which is the argument for the map existing as a
checked-in document rather than as a decision recorded in a conversation.

**Nothing mechanically enforces that a fragment resolves.** yoyodyne-ifd.121.2
makes "every link resolves" its definition of done, and the only thing standing
behind that today is a reviewer reading a diff — which is exactly the check that
does not catch a silently-ignored fragment. This has already happened once at a
much smaller scale: yoyodyne-ifd.54 records two README anchors left dead by a
rename, found by hand afterwards and repaired by asking a later run to grep for
the old names. This effort moves 22 README sections and 34 configuration
sections at once, and 91 intra-document links with them. yoyodyne-ifd.166 is
the second demonstration and a nearer miss: four configuration sections had no
row in the table a tranche executes against, three of them cited from documents
that would have been left pointing at nothing. A link checker over the repository's Markdown — resolving both
relative paths and fragments, and covering `.github/` — wired into `make check`
is the durable form of the whole policy above, and it is what would let a
reviewer verify the tables here instead of re-deriving them. It is named here
and in the summary as work to admit, not queued.

## What the architect is being asked

1. **Is the Tier 1 / Tier 2 / Tier 3 rule the right shape** — freeze by who
   cites, rewrite everything internal, and refuse to leave a dangling fragment?
   The alternative considered and rejected was freezing every currently-cited
   anchor, which buys a stub graveyard in exchange for never re-deriving a
   citation list.
2. **Should the frozen anchors live in `docs/configuration.md` as stubs, or
   should `docs/configuration.md` keep the real `## Checks` content** and the
   split take everything else? The stub is proposed because a document that
   keeps one arbitrary section for URL reasons is harder to explain than a
   document that keeps none.
3. **Is `docs/configuration/` as a directory beside a surviving
   `docs/configuration.md` acceptable**, or should the split targets be flat
   `docs/configuration-runs.md` and so on? The directory is proposed; the
   coexistence is admittedly odd to look at.
4. **Does `shippedDocumentation` growing to fifteen entries change what the
   product manager should be given at all**, or is the enumeration the right
   answer to keep?

## What the operator is being asked

Whether the README landing at roughly 500 lines rather than a short landing page
is the right trade — it is the direct consequence of keeping install and the
three getting-started steps in the README, which the adoption goal's own wording
demands. Everything else in this map follows from decisions already made in
yoyodyne-ifd.121 and yoyodyne-ifd.117.
