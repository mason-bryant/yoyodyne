# Goals directory

This directory holds the goals derived from [the product brief](../brief.md).
It currently holds [the v1 goals](v1-goals.md) and
[the v1 non-goals](v1-non-goals.md) that bound them, moved here unchanged from
[the v1 harness design](../../designs/v1-harness-design.md) because goals are the
product manager's to own rather than the architect's.

A goal document opens with an introduction saying what it covers and why, and
states the goals that serve that introduction under a `Goals` heading. That is
the [shape the harness checks](../../configuration.md#product-specifications),
and it is checked of every document under `docs/product` — including the ones
that state no goals at all. [The v1 non-goals](v1-non-goals.md) states its
content under a `Non-goals` heading, so the harness reports it as stating none.
It is still read exactly as written rather than dropped, because refusing it
would lose intent somebody recorded. The contract having no shape for a
non-goals document is a gap in the contract, not a reason to file non-goals
under a `Goals` heading. That check reports this file too, because its `Goals` title is
taken for that heading with no introduction ahead of it; [the brief](../brief.md)
it does not report.

Each goal states one outcome the product is trying to reach, and should be:

- **traceable upstream** — it names how it supports the brief. A goal that
  supports nothing in the brief is an orphan.
- **specific enough to design against** — an architect should be able to turn it
  into a design without first having to ask what was meant. Intent that is still
  ambiguous is a question for the operator, not a goal.
- **an outcome, not an implementation** — what should become true, and why. How
  it gets built belongs to designs and specifications.

Goals are owned by the product manager and approved by the operator. Other roles
ask questions and propose amendments; they do not revise a goal directly. See
[artifact ownership](../../designs/v1-harness-design.md#artifact-ownership).

Artifact governance, delivered in milestone 2, made that machine-checkable:
each goals document carries identity frontmatter, every goal's *Supports*
trailer — the emphasized line directly under its entry, indented with it and
with no blank line between — is resolved against the brief mechanically, reference validation and
orphan reporting run over the loaded set, and a test enforces that every goal
in force names a claim the brief states. A reader can still trace the links by
hand; the harness no longer depends on them to. Two different mechanisms read
this directory, and they see different things: the shape check above reads
every document under `docs/product` by path, this index included, while
artifact governance reads only documents carrying identity frontmatter and
skips directory indexes by name. This index is therefore ungoverned prose —
the contract it teaches lives here informally, and normatively in the governed
documents.
