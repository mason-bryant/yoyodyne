# Goals

This directory holds the goals derived from [the product brief](../brief.md).
It contains no goals yet — this file describes what one is so the directory
exists in version control and so the first goal written here has a shape to
follow.

A goal is one Markdown file stating an outcome the product is trying to reach.
It should be:

- **traceable upstream** — it names how it supports the brief. A goal that
  supports nothing in the brief is an orphan, and the harness reports it rather
  than assuming that related-sounding prose is a link.
- **specific enough to design against** — an architect should be able to turn it
  into a design without first having to ask what was meant. Intent that is still
  ambiguous is a question for the operator, not a goal.
- **an outcome, not an implementation** — what should become true, and why. How
  it gets built belongs to designs and specifications.

Goals are owned by the product manager and approved by the operator. Other roles
ask questions and propose amendments; they do not revise a goal directly. See
[artifact ownership](../../v1-harness-design.md#artifact-ownership).

Stable artifact IDs, lifecycle status, and the rest of the machine-readable
metadata each goal will carry are not settled yet; they arrive with artifact
governance in milestone 2. Until then a goal is ordinary prose in this
directory.
