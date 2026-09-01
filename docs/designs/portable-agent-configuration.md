---
id: portable-agent-configuration
kind: design
title: "Portable agent configuration: materialization, the baseline, and bundle boundaries"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-01T19:35:00Z
      reason: drafted by the developer under yoyodyne-ifd.36, carried whole to the architect, and ratified with amendments recorded in conversation - the authority paragraph rebound to authority-by-capability and configuration-never-grants-authority with role definitions excluded from materialization, draft scaffolding replaced by the ratification decisions, and conversion output placed under the working-tree publication rule. The four questions the draft asked the architect are answered in its Decided-at-ratification section
---

# Portable agent configuration

It serves the goal "Keep roles, policies, and provider selection configurable
without making safety invariants optional."

The work item asked four questions and told the developer not to start by
writing code. This answers the four, states what stays undecided, records the
architect's decisions at ratification, and names what stays open for the
operator.

## What already exists, so nothing below re-derives it

The resolution machinery is built and tested. Up to three layers produce the
effective configuration, later ones winning: harness defaults, a bundle named by
`extends`, then the project file. `checks` is replaced as a whole list rather
than concatenated, a `persona` override replaces rather than merges, an agent is
removed with `disabled: true` rather than by omission, and `version` is never
inheritable. The rules are stated in
[Precedence](../configuration.md#precedence) and
[Merge and removal semantics](../configuration.md#merge-and-removal-semantics);
[What fails closed](../configuration.md#what-fails-closed) lists what a
configuration is refused for.

`yoyo config show --origins` records, per key, the layer that supplied it. The
effective configuration has a revision — `cfg-` and a digest derived from the
values rather than declared — which a run record names when it says which
configuration set it up.

Two things are missing rather than broken. There is no way to move a project
from an explicit configuration to an inheriting one; only
[the other direction](../configuration.md#converting-an-inheriting-configuration-to-an-explicit-one)
is documented, and it is a manual re-application. And a project that has
materialized its defaults has no mechanism at all by which a later bundle
improvement reaches it — the standing advice is to re-run `init` into a scratch
directory and diff, which is the gap this design is mostly about.

## The decision underneath all four answers

**Ownership is materialization, and it is not a second vocabulary.**

The tempting design adds a way for a project to declare what it owns — an `owns`
list, a per-key marker, a partial `extends`. Every version of that is a second
way to say something the file already says by having a value in it, and this
repository has already paid for one of those: a proposal in the conversation and
a proposal in `yoyo amendment` are two vocabularies for one idea, and
consolidating them is recorded as not done. So a written value is an owned
value, an absent value is an inherited one, and nothing new is added to the
schema that resolution reads.

What is added is **a record of where a materialized value came from**, kept
beside the configuration rather than inside it. That record is what turns the
two-way diff the operator does today into the three-way comparison the problem
actually needs.

## 1. What a project owns versus what it inherits

A project owns every value it states and inherits every value it does not. That
is already true; what this design fixes is that today it is an all-or-nothing
choice made once, because `yoyo init` writes a complete file with no `extends`
and the only supported way back is to regenerate.

Selective inheritance needs no new merge semantics. A project that writes
`extends` and then states forty of the eighty values it could state already owns
forty and inherits forty, per rules that are built and tested. The three things
missing are a command that produces that file from an explicit one, a command
that produces an explicit one from it, and a way to see which is which. Those
are sections 3 and 2.

**Some values a project cannot inherit however it is shaped, and the set does
not grow quietly.** They divide into three reasons, which are worth keeping
apart because they fail in different places.

- **Refused from a bundle.** `version`, because a version taken from a bundle
  lets a file written against another schema load as whatever the bundle said;
  and `product`, because a bundle that supplied product identity would name
  every project after itself. `checkBundleDocument` refuses a bundle declaring
  either, so this fails when the bundle loads rather than when a project uses
  it.
- **Never supplied by one.** `checks` describe the managed project's toolchain,
  which a bundle has no view of, so `builtin:v1` deliberately states none. A
  project's checks are its own by there being nothing to inherit.
- **Reachable only by opting in.** `approvals.work_items` and
  `approvals.publishing` are stated by the bundle at the same value the harness
  default holds, so extending inherits neither and upgrading moves neither. That
  is existing behaviour and this design keeps it: an opt-in that arrived by
  inheritance would not be one, and it is exactly the class of value a
  `materialize`/`extract` round trip must not quietly relocate.

## 2. Seeing which is which without asking

The work item asks how an operator sees what is inherited without running a
command, given that `config show --origins` answers only when asked.

**The honest answer is that the file cannot carry it.** Anything the harness
writes into `.yoyodyne/config.yaml` to mark provenance is either schema — which
means resolution reads it, which means a project can lie about its own
provenance — or a comment, which the operator edits away or leaves to go stale.
A configuration whose comments disagree with its values is worse than one that
says nothing, because it is read as an answer.

So the design does not put provenance in the file. It makes provenance arrive
**on commands the operator already runs**, which is the same shape this
repository already uses for the ignore-rule warning and for artifact staleness:
reported beside the thing you asked for, never a separate errand.

- `yoyo config validate` and `yoyo doctor` report drift against the recorded
  baseline (section 4) on every run, on standard error, without changing their
  exit codes. A project with no drift says nothing.
- `yoyo config show` gains a fourth line in its header — the bundle a project
  materialized from and whether it is current — beside the layers and the
  revision it already prints.
- The conversation surfaces it where it changes what the operator would decide,
  and nowhere else.

**Every one of those is a projection of one derivation, computed server-side
once.** Drift is a domain derivation, so the CLI, the dashboard, and Slack read
it from the shared read model rather than each computing it; a second surface
computing "is this project current" differently is a disagreement only the
operator can settle, and the invariant on operator surfaces rules it out.

## 3. Moving between explicit and inherited, in both directions

Two commands, each the inverse of the other, and one criterion that makes both
checkable.

**`yoyo config materialize`** turns an inheriting configuration into an explicit
one. It resolves the effective configuration, writes it as a complete standalone
file with no `extends`, copies the personas into `.yoyodyne/personas/`, and
records the baseline of section 4. It writes what is already in force, so it
cannot lose a project value. This replaces
[the four manual steps](../configuration.md#converting-an-inheriting-configuration-to-an-explicit-one)
documented today, whose step 3 is "re-apply what was yours" and whose failure
mode is forgetting one.

**`yoyo config extract`** turns an explicit configuration into an inheriting
one. Against a named bundle, it removes every value byte-identical to what that
bundle supplies, writes `extends`, and keeps the rest as deviations. It reports
what it kept and why, in two classes the operator reads differently: values that
differ from the bundle, and values that are not inheritable at all (section 1).
A persona whose body differs from the bundle's is kept as a file and an
override, because a persona is replaced whole rather than merged and half of one
persona is guidance nobody wrote.

**The criterion for both: the effective configuration does not move.**
`yoyo config show --effective` before and after must be byte-identical, and the
configuration revision must be unchanged. That is a property a test asserts
rather than a claim a reviewer reads, and it is what makes the round trip
`materialize` → `extract` → `materialize` safe to offer. Where a conversion
cannot hold it, it refuses and names the value, rather than writing a file that
runs differently from the one it replaced.

Neither command is destructive without saying so: both refuse to overwrite
without `--force`, both fail before writing anything, and both go through the
shared safe-write primitive under the project root, so neither can be walked out
of the repository by a symlink in `.yoyodyne`. Both commands' output is subject
to the same working-tree rule as every other harness write to the primary
checkout — the operator commits it, and runs refuse over the uncommitted
change — so conversions land the way approved artifact writes already do rather
than inventing a third publication shape.

## 4. How a bundle improvement reaches a project that materialized

This is the specific thing an explicit configuration trades away, and the
mechanism that buys it back is **a baseline, recorded at materialization, that
is never read at load time**.

`materialize` and `init` write `.yoyodyne/config.lock`: the bundle's name, the
bundle's revision digest, and the digest of each value as that bundle supplied
it. It is generated, it is committed with the configuration, and **nothing in
the load path reads it**. What the file says is still exactly what runs, which
is ifd.35's guarantee and is not weakened here.

With a baseline, `yoyo config drift` is a three-way comparison rather than a
diff, and it sorts every value into four answers:

| Answer | What it means | What is offered |
| --- | --- | --- |
| unchanged | Neither you nor the bundle moved it. | Nothing. |
| yours | You changed it; the bundle did not. | Nothing. It is yours, and it is never touched. |
| available | The bundle improved it; you never edited it. | Adopting it. |
| conflicting | Both moved it, to different values. | Both values, named, for you to decide. |

The middle two are the entire point. Today's advice — regenerate into a scratch
directory and diff — is a two-way diff with no base, so it cannot tell a value
you deliberately changed from a value the bundle improved, and it reports both
as differences. The baseline is what supplies the missing third side.

`yoyo config adopt <key>` takes one available improvement and rewrites that
value and its baseline entry. Adoption is per value and never wholesale: a
command that adopted everything available would be `init --force` with better
manners, and would silently move values the operator had reasons for. A
conflicting value is never adopted; it is reported until the operator settles
it.

**A missing or stale lock is a report, not a refusal.** A project that predates
this, or that deleted the file, gets told once that its baseline is unknown and
that `yoyo config drift` cannot answer for it — the same treatment a document
with a broken relationship gets, and for the same reason: refusing would break a
project over a file that decides nothing about how it runs.

## 5. Where bundles come from, and where this meets the plugin contract

Today one bundle exists, `builtin:v1`, embedded in the executable and looked up
through a fixed map so an unknown name can never resolve to a path. The question
is whether a bundle may ever come from somewhere else — a plugin, a fleet
repository, an organization's house defaults — which is where this meets
yoyodyne-ifd.32.

**The position this design takes: yes, but only as a materialization source,
never as a load-time layer.**

A bundle from outside the executable is read once, by `yoyo config materialize
--from <bundle>`, at the operator's explicit instruction, and its values land in
the project's own file where the operator reads them, edits them, and commits
them. It never becomes a layer that resolution consults on a run. The
consequences are the reason for the rule:

- No third party's content is in the load path of a run. Upgrading a plugin
  cannot move a value the operator is running under, because the value is in
  their file.
- The operator sees what they adopted, as values, before anything runs on them.
  A supply chain that reaches the harness through a diff the operator reads is a
  different risk from one that reaches it at load.
- The baseline of section 4 works unchanged: it records which bundle and which
  revision supplied each value, whoever supplied it, so drift and adoption
  behave the same for a plugin bundle as for the built-in one.

**What a bundle may never do, whoever supplies it.** Authority *semantics* stay
in Go, and composition becomes protected operator-activated configuration only
after parity, per [authority-by-capability](../decisions/authority-by-capability.md)
and the invariant `configuration-never-grants-authority`. A persona specializes
how a role works and cannot widen what it is allowed to do, and the role
contract is sent ahead of it on every turn. A bundle is **ordinary**
configuration, so materialization never writes role definitions:
`materialize --from <bundle>` refuses bundle content addressed to
`.yoyodyne/roles/`, because protected role definitions have their own
operator-activation path and never arrive by materialization from anyone's
bundle. `checkBundleDocument`
already refuses a bundle that declares a `product` or extends another bundle,
and its checks apply to any bundle rather than to the embedded one. A
third-party bundle is content, and every write of it passes the shared safe-write
primitive under a declared root — an unpacked bundle that resolves outside that
root is refused, per
[repository-writes-are-physically-confined](../decisions/invariants/repository-writes-are-physically-confined.md).

That is the whole of what portability means here: **values travel; authority
does not.**

## What this design deliberately does not decide

- **A bundle registry, a resolver, or a fetch protocol.** Section 5 decides
  where a third-party bundle may be used and what it may not do. How one is
  named, discovered, verified, or pinned belongs to the plugin contract, and
  deciding it here would be this document legislating for a design it is not.
- **Whether `yoyo init` should change what it writes.** It should not, on this
  design's evidence — ifd.35's trade holds, and the lock is additive. Reopening
  it is the operator's call and is listed below.
- **Fleet configuration across several projects.** One repository at a time.
  Several projects sharing defaults is what a bundle is for; a fleet that also
  wants shared *state* is team mode's problem, not this one.
- **A migration for projects that predate the lock.** They report an unknown
  baseline and keep working. Whether `materialize` should offer to reconstruct
  a baseline by matching current values against a bundle is a real question with
  a real wrong answer — reconstructing one is guessing that unedited values were
  never edited — and it is left open.

## Decided at ratification

The architect ratified this design with four decisions, recorded in
conversation (chat-11558d325e9a214ebfd00bb4a0012750, turn 24):

1. **"Ownership is materialization" is the right refusal.** An `owns` list is a
   second vocabulary for what the file already says by having a value in it,
   and this repository has paid for exactly one such duplication already. A
   written value is owned; an absent value is inherited; nothing new enters the
   schema resolution reads. Ratified as the design's spine.
2. **The committed lockfile is the right shape.** A baseline in the state
   directory is per-machine, tells a collaborator nothing, and drifts per
   checkout. The property that makes it safe is kept verbatim: **nothing in the
   load path reads it.** What the file says is what runs; the lock only makes
   the three-way comparison possible. Committed, generated, decides nothing.
3. **"Materialization source, never a load-time layer" is the right boundary,
   and the plugin contract inherits it rather than re-deciding it.**
   Third-party content reaching the harness through a diff the operator reads
   is a categorically different risk from content consulted at load, and no
   plugin design gets to reopen that; yoyodyne-ifd.32's contract cites this
   design instead of arguing with it.
4. **Drift is a read-model derivation from the start.** "Is this project
   current" is a domain derivation that at least three surfaces will show;
   `surfaces-project-one-read-model` rules out a CLI-local computation.

The question routed to the operator below stays theirs and stays open; the
drift report's conversational surfacing is not implemented until they answer.

## Open: for the operator

Whether the four-answer drift report is the right amount of attention to spend.
It is silent when nothing changed and speaks on commands already being run, but
it is one more thing that can speak — and the standing direction on this
harness's surfaces is that visibility is the control, not nagging. If the
`available` class should be silent until asked, this design should say so before
anything implements it.
