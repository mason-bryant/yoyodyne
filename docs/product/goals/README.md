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

The shape and identity a goals document must carry — the `Goals` heading, the
frontmatter, the *Supports* trailer resolved against the brief — is governed by
[the artifact contract](../../designs/artifact-contract.md), the normative home
for those rules. This index links and describes; it states no rules of its own,
and [index-and-stray-identity-governance](../../decisions/index-and-stray-identity-governance.md)
is the decision that keeps it that way.

Each goal states one outcome the product is trying to reach, and should be:

- **traceable upstream** — it names how it supports the brief. A goal that
  supports nothing in the brief is an orphan.
- **specific enough to design against** — an architect should be able to turn it
  into a design without first having to ask what was meant. Intent that is still
  ambiguous is a question for the operator, not a goal.
- **an outcome, not an implementation** — what should become true, and why. How
  it gets built belongs to designs and specifications.

(The three qualities above are the product manager's guidance and move into
their goals documentation when they rehouse them; they stay here until then so
nothing recorded is lost.)
