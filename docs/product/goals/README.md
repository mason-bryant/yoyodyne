# docs/product/goals

**Purpose.** The goals derived from [the product brief](../brief.md), and the
non-goals that bound them. [The v1 goals](v1-goals.md) and
[the v1 non-goals](v1-non-goals.md) were moved here unchanged from
[the v1 harness design](../../designs/v1-harness-design.md), because goals are
the product manager's to own rather than the architect's. Every statement under
a goals document's `Goals` heading is a goal work can be attributed to, in the
words that document states it in; `yoyo goals list` is what they resolve to.

**Owner.** The product manager, and the operator approves. It is the only role
that changes a document filed here. Every other role proposes an amendment and
waits for the owner to decide rather than editing one, and `yoyo amendment list`
is what is waiting. See
[artifact ownership](../../designs/v1-harness-design.md#artifact-ownership).

**Editing by hand.** You may, and nothing here refuses it — you are the
operator, and these documents are yours. Two things follow rather than nothing.
Record the change in the document's own revision log under the product manager:
a revision recorded under any other role is reported as unauthorized every time
the artifacts load, which is something to look at rather than a refusal. And
whatever downstream traced to what you changed is reported by `yoyo stale` until
its own owner revises it. Then say what you changed in `yoyo chat`, because a
conversation that is already open is working from these documents as they read
when it opened.

## What a goal looks like

A goal document opens with an introduction saying what it covers and why, and
states the goals that serve that introduction under a `Goals` heading. That is
[the shape the harness checks](../../configuration.md#product-specifications),
and it is checked of every document filed here but this index, which is exempt
by name rather than for what it says. [The v1 non-goals](v1-non-goals.md)
states its content under a `Non-goals` heading, so the harness reports it as
stating none. It is still read exactly as written rather than dropped, because
refusing it would lose intent somebody recorded. The contract having no shape
for a non-goals document is a gap in the contract, not a reason to file
non-goals under a `Goals` heading.

Each goal states one outcome the product is trying to reach, and should be:

- **traceable upstream** — it names how it supports the brief. A goal that
  supports nothing in the brief is an orphan.
- **specific enough to design against** — an architect should be able to turn it
  into a design without first having to ask what was meant. Intent that is still
  ambiguous is a question for the operator, not a goal.
- **an outcome, not an implementation** — what should become true, and why. How
  it gets built belongs to designs and specifications.

Artifact governance, delivered in milestone 2, made that machine-checkable: each
goals document carries identity frontmatter, every goal's *Supports* trailer —
the emphasized line directly under its entry, indented with it and with no blank
line between — is resolved against the brief mechanically, reference validation
and orphan reporting run over the loaded set, and a test enforces that every
goal in force names a claim the brief states. A reader can still trace the links
by hand; the harness no longer depends on them to.

This file is a directory index rather than an artifact: it carries no identity
frontmatter, nothing refers to it by id, and artifact governance skips it by
name. The shape check above skips it by that same name and not by a rule of its
own, so there is no document the two read differently and nothing here is ever
reported for the shape of a document it was not trying to be. `yoyo init`
writes it and `yoyo doctor` reports it missing, so editing it is safe and
deleting it is noticed.
