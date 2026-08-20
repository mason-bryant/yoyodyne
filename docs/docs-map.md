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

**Execution state.** The README split has landed, under yoyodyne-ifd.121: the
README is 483 lines, the six documents beside it exist, and every citation of a
README anchor was rewritten with it. The configuration guide split has not, so
`docs/configuration.md` is still the whole reference at its current path and
every `configuration.md#…` citation below still resolves as written. The tables
here have been re-derived against the tree as it stands after the README split;
a run implementing the configuration split re-derives again rather than trusting
them, for the reason [the citation section](#every-anchor-citation-and-where-it-goes)
already gives.

The problem it answers: the README was 152KB across 2,602 lines and the
configuration guide is 132KB across 2,377 lines. The adoption goal — *a newcomer
can go from the documented install to a working first run on their own
repository using the readme alone* — is failed by a document nobody reads to the
end, and the configuration guide costs more than a whole context budget to
include.

## What each document is for

Audience is the split's only organizing principle. A section moves to the
document whose reader needs it, not to the one whose subsystem it names.

| Document | Audience | Roughly |
|---|---|---|
| [`README.md`](../README.md) | Someone deciding whether to use yoyo, and then reaching a first run | 500 lines |
| `docs/conversation.md` | An operator driving work from `yoyo chat` | 883 lines |
| `docs/work.md` | An operator who has approved work and wants to know what happens to it | 270 lines |
| `docs/artifacts.md` | An operator maintaining the brief, goals, designs, and invariants | 246 lines |
| `docs/reporting.md` | An operator asking what the work cost and what came back | 228 lines |
| `docs/operations.md` | An operator recovering from a stall, a crash, or a provider refusal | 454 lines |
| `docs/developing-yoyo.md` | Someone changing yoyo itself | 30 lines |
| [`docs/configuration.md`](configuration.md) | Anyone arriving at a configuration link — an index and the frozen anchors | 120 lines |
| `docs/configuration/setup.md` | Someone writing or inheriting a project configuration | 359 lines |
| `docs/configuration/artifacts.md` | Someone configuring artifact homes, approval, and ownership | 486 lines |
| `docs/configuration/goals.md` | Someone configuring admission, attribution, and staleness | 346 lines |
| `docs/configuration/runs.md` | Someone configuring checks, scheduling, and what a run costs | 325 lines |
| `docs/configuration/publishing.md` | Someone configuring pull requests, branches, and merges | 305 lines |
| `docs/configuration/recovery.md` | Someone configuring triage thresholds and provider-refusal waits | 332 lines |
| `docs/configuration/agents.md` | Someone configuring operators, personas, and Slack | 200 lines |

Line counts are the current content's, carried across. They are a size budget
for review, not a target to write to: a run that lands a document materially
larger than its budget has found content the map misplaced, and should say so
rather than pad or trim to fit.

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
    repo-root URL `https://github.com/mason-bryant/yoyodyne#getting-started`,
    which `.github/workflows/release.yml:55` passes to `gh release create
    --notes-file`. It is in the notes of every release already published, and
    published release notes cannot be corrected by a change to this repository.
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

With the document each moves to and its tier. Re-derived against the tree after
the README split; the sixteen anchors are the same sixteen the draft found, and
what changed is which file cites each one, because most of the citing prose left
the README for the documents beside it.

| Anchor | Cited by | New home |
|---|---|---|
| `#checks` | `README.md:316`, `internal/config/scaffold.go:361`, `internal/cli/init_test.go:214` | **Tier 1 stub stays**; canonical `configuration/runs.md#checks` |
| `#product-specifications` | `docs/designs/v1-harness-design.md:243`, `docs/product/goals/README.md:11` | **Tier 1 stub stays**; canonical `configuration/artifacts.md#product-specifications` |
| `#what-reaches-the-queue` | `docs/conversation.md:141`, `:212`, `:542`, `docs/artifacts.md:43` | `configuration/goals.md` |
| `#where-the-tracker-syncs` | `README.md:274` | `configuration/setup.md` |
| `#what-one-work-item-has-been-given` | `docs/conversation.md:634`, `docs/operations.md:383` | `configuration/recovery.md` |
| `#publishing-through-pull-requests` | `README.md:442`, `docs/work.md:271` | `configuration/publishing.md` |
| `#what-the-product-manager-sees-besides-them-and-what-it-does-not` | `docs/conversation.md:84` | `configuration/artifacts.md` |
| `#traceability-references-and-orphans` | `docs/artifacts.md:84` | `configuration/goals.md` |
| `#scheduling-ready-work` | `docs/work.md:129` | `configuration/runs.md` |
| `#protected-paths-in-a-developers-change` | `docs/work.md:29` | `configuration/artifacts.md` |
| `#personas` | `docs/conversation.md:70` | `configuration/agents.md` |
| `#operators` | `docs/slack/setup.md:161` | `configuration/agents.md` |
| `#losing-a-race-for-the-target-branch` | `docs/operations.md:368` | `configuration/publishing.md` |
| `#how-long-a-check-may-take` | `README.md:328` | `configuration/runs.md` |
| `#avatars` | `docs/slack/setup.md:139` | `configuration/agents.md` |
| `#approving-a-document` | `docs/artifacts.md:50` | `configuration/artifacts.md` |

One citation the draft listed is gone rather than moved: `README.md:1782`, the
second citation of `#where-the-tracker-syncs`, sat in `## Configuring a project`,
which the README split merged away.

### Citations of `README.md` anchors

Twenty-two README sections move out to seven new documents, so this set has to
be established rather than assumed. It was swept three ways: every
`README.md#…` reference in any case anywhere in the tree; every GitHub URL for
this repository carrying a fragment, which is how a link to the README's
rendered root is written; and every README slug whose section moves, searched
literally as `#slug` across the working tree and the tracker's current export.

The result is small, and worth stating flatly because the map's later claim that
the README keeps four things and needs no stub of its own depends on it:

| Anchor | Cited by | Tier | New home |
|---|---|---|---|
| `#talking-to-the-other-agents` | `docs/configuration.md:178` | 2 | `conversation.md#talking-to-the-other-agents` |
| `#getting-started` | `.github/release-notes-preamble.md:28` | **1** | stays in the README — see below |

**Both rows are settled.** The Tier 2 citation was rewritten in the README
split's own change, and `#getting-started` is where it was. The README also
acquired one new anchor that other documents now cite — `#further-reading`, from
the orientation line each of the six new documents opens with — which is Tier 2
by the same rule and moves only with its citations.

**Those two rows are the whole set.** No work item, design, `docs/product/`,
`docs/slack/`, or Go source file cites a README anchor at all, and the only
external citation of a README anchor whose section *moves* is
`docs/configuration.md:178`, which is Tier 2 and rewritten in the same change.
`#getting-started` is the sole Tier 1 README anchor, and the section it names
stays — so the README acquires no stub.

Two near-misses the sweep turned up, recorded so a re-derivation does not
re-litigate them. Four intra-file links in `docs/configuration.md` match README
slugs as prefixes — `#artifact-identity-and-metadata` (:315, :340),
`#architectural-invariants` (:417), `#what-a-change-upstream-leaves-stale`
(:1312) — but each resolves against `configuration.md`'s own headings at :396,
:1053 and :998, so they belong to the set below rather than to this one. And
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
count in this effort — 67 links — so they are the likeliest thing for an
execution run to miss.

**The README linked into itself 38 times across 26 anchors**, and that half is
done. Six anchors stayed put — `#install`, `#getting-started`, the three
numbered step headings, and `#optional-publishing-and-auto-merge` — because
their sections stayed. The other 20 became relative links, resolved from the
disposition table below: a link whose target landed in the same new document
stayed a bare `#slug`, and the rest name the document they went to.

**`docs/configuration.md` links into itself 29 times across 21 anchors**, and
after the split most cross a document boundary. Resolve each against the
configuration disposition table: a link whose target lands in the same new
document stays a bare `#slug`; one whose target lands elsewhere becomes a
relative link. The anchors are `#who-may-change-an-artifact` (×3),
`#proposing-a-change-to-a-document-you-do-not-own` (×3), `#what-reaches-the-queue`
(×2), `#artifact-identity-and-metadata` (×2), `#approving-a-document` (×2),
`#what-one-work-item-has-been-given` (×2), and one each of
`#extending-a-built-in-bundle`, `#what-init-proposes-for-checks`,
`#what-fails-closed`, `#protected-paths-in-a-developers-change`, `#personas`,
`#architectural-invariants`, `#product-specifications`,
`#traceability-references-and-orphans`, `#goals-and-the-work-attributed-to-them`,
`#watching-instead-of-draining`, `#what-a-change-upstream-leaves-stale`,
`#how-long-a-check-may-take`, `#relaunching-a-run-the-provider-killed`,
`#losing-a-race-for-the-target-branch`, and `#operators`.

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
- [`docs/product/`](docs/product) — the brief and goals the product manager reads.
- [Working on yoyo itself](docs/developing-yoyo.md) — the checks, the build, and
  what a release is.
```

### Disposition of every current README section

Every section, with its destination. Nothing is dropped; a run that finds
content with no row here stops and reports rather than choosing a home.

| Current section | Lines | Destination |
|---|---|---|
| `# yoyo` (opening, testimonials, gates, quick start, bounds) | 121 | **stays** |
| `## Install` | 71 | **stays** |
| `## Getting started` + steps 1–3 + `Optional: publishing and auto-merge` | 250 | **stays** |
| `### Working on yoyo itself` | 27 | `developing-yoyo.md` |
| `## The conversation` | 134 | `conversation.md` |
| `### Proposals, and deciding them in batches` | 116 | `conversation.md` |
| `### Steering the work from the conversation` | 166 | `conversation.md` |
| `### What the work cost` | 92 | `reporting.md`, except the `/diff` and `/redirect` paragraphs → `conversation.md` |
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

| Current section | Lines | Destination |
|---|---|---|
| `# Yoyodyne configuration` (opening, what owning defaults costs) | 24 | **stays** as the index opening, merged with the README's `## Configuring a project` |
| `## Creating a project configuration` | 30 | `configuration/setup.md` |
| `### Where the tracker syncs` | 31 | `configuration/setup.md` |
| `## Layout` | 95 | `configuration/setup.md` |
| `## Discovery` | 23 | `configuration/setup.md` |
| `## Precedence` | 79 | `configuration/setup.md` |
| `## Extending a built-in bundle` (+ `### Converting an inheriting configuration…`) | 51 | `configuration/setup.md` |
| `## Migrating from .yoyodyne.yaml` | 16 | `configuration/setup.md` |
| `## Inspection` | 34 | `configuration/setup.md` |
| `## Product specifications` (+ `### What the product manager sees…`) | 113 | `configuration/artifacts.md` |
| `## Artifact identity and metadata` | 96 | `configuration/artifacts.md` |
| `### Approving a document` | 63 | `configuration/artifacts.md` |
| `### Who may change an artifact` | 47 | `configuration/artifacts.md` |
| `### Protected paths in a developer's change` | 76 | `configuration/artifacts.md` |
| `### Proposing a change to a document you do not own` | 91 | `configuration/artifacts.md` |
| `### What reaches the queue` | 69 | `configuration/goals.md` |
| `### Traceability: references and orphans` | 44 | `configuration/goals.md` |
| `### Goals, and the work attributed to them` | 116 | `configuration/goals.md` |
| `### What a change upstream leaves stale` | 55 | `configuration/goals.md` |
| `## Architectural invariants` | 62 | `configuration/goals.md` |
| `## Checks` (+ `### What init proposes`, `### How long a check may take`) | 155 | `configuration/runs.md` |
| `## Scheduling ready work` (+ `### Watching instead of draining`, `### When a configuration change takes effect`, `### Why each run says why it was there`) | 170 | `configuration/runs.md` |
| `## Publishing through pull requests` (+ its three children) | 180 | `configuration/publishing.md` |
| `## Losing a race for the target branch` | 41 | `configuration/publishing.md` |
| `## Merge and removal semantics` (+ `### What fails closed`) | 84 | `configuration/publishing.md` |
| `## Waiting out a provider that refuses` | 116 | `configuration/recovery.md` |
| `## Relaunching a run the provider killed` | 46 | `configuration/recovery.md` |
| `## Triage thresholds` (+ `### What one work item has been given`) | 170 | `configuration/recovery.md` |
| `## Operators` | 72 | `configuration/agents.md` |
| `## Personas` | 43 | `configuration/agents.md` |
| `## Reporting to Slack` (+ `### Avatars`) | 85 | `configuration/agents.md` |

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
`internal/contextbundle/product.go:79` carries the shipped documentation as a
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
lands the last split document lands the list. The set stays explicit — this map
is the enumeration it needs, which is the argument for the map existing as a
checked-in document rather than as a decision recorded in a conversation.

**What the README split did instead, recorded rather than decided here.** That
run grew the list to the eight documents that exist today — `README.md`,
`docs/configuration.md`, and the six beside them — rather than leaving it to the
configuration split as the rule above says, because naming the seven documents
under `docs/configuration/` before anybody has written them would assert
coverage that is not there. That is a departure from the rule, and the rule
stands as written until somebody with the authority to change it says otherwise:
whether it should become one-half-lands-its-own-entries is yoyodyne-ifd.121.1's
to settle or the operator's, not an execution run's.

**And this document is the only place that contradiction can be recorded**,
which is worth knowing before somebody reaches for the amendment path. This map
lives outside the artifact homes, so `yoyo artifact list` does not record it,
and a `yoyodyne-amendment` naming `docs-map` resolves to no owner and is refused
— the refusal reaching the operator rather than the agent that wrote it. A run
that believes this rule is wrong therefore says so here and in its summary, for
yoyodyne-ifd.121.1 or the operator to settle, and does not wait on a decision
nothing will produce. Either way the configuration split still adds the seven
under `docs/configuration/`, so what is open is which run was supposed to do
what, not what the list ends up holding.

**A fragment that resolves is checked by a script, and by no gate.**
yoyodyne-ifd.121.2 makes "every link resolves" its definition of done, and what
used to stand behind that was a reviewer reading a diff — exactly the check that
does not catch a silently-ignored fragment. This has already happened once at a
much smaller scale: yoyodyne-ifd.54 records two README anchors left dead by a
rename, found by hand afterwards and repaired by asking a later run to grep for
the old names. This effort moves 22 README sections and 30 configuration
sections at once, and 67 intra-document links with them.

So the README split landed
[`scripts/check-doc-links.py`](../scripts/check-doc-links.py), which resolves
every intra-repository Markdown link — path and fragment both, under GitHub's
own slug rules — across the whole tree including `.github/`, and resolves the
`docs/…#fragment` anchors cited from Go source with it, which is what holds
`docs/configuration.md#checks` to the heading every generated
`.yoyodyne/config.yaml` names. **The configuration split runs it rather than
re-deriving these tables by hand**, and a run that moves a section runs it
before it says it is done.

What is still missing is the gate: nothing runs that script unless somebody
does. Wiring it into `make check` is the durable form of the whole policy above,
and it is named here and in the summary as work to admit, not queued — an
execution run does not add a gate the whole repository must then pass.

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
