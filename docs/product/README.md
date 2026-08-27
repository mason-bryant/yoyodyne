# docs/product

**Purpose.** The product brief and the goals that serve it: what this product
is, who it is for, and what finished looks like. This is the only directory the
harness reads product intent from, and work reaches the backlog only with a
goal stated here named against it.

**Owner.** The product manager. It is the only role that changes a document
filed here. Every other role proposes an amendment and waits for the owner to
decide rather than editing one, and `yoyo amendment list` is what is waiting.

**Editing by hand.** You may, and nothing here refuses it — you are the
operator, and these documents are yours. Two things follow rather than nothing.
Record the change in the document's own revision log under the product manager:
a revision recorded under any other role is reported as unauthorized every time
the artifacts load, which is something to look at rather than a refusal. And
whatever downstream traced to what you changed is reported by `yoyo stale`
until its own owner revises it. Then say what you changed in `yoyo chat`,
because a conversation that is already open is working from these documents as
they read when it opened.

This file is a directory index rather than an artifact: it carries no identity
frontmatter, nothing refers to it by id, and artifact governance skips it.
`yoyo init` writes it and `yoyo doctor` reports it missing, so editing it is
safe and deleting it is noticed.
