# docs/decisions

**Purpose.** The decision records: how something was decided, what was decided
against, and what that cost. A record is an account of a decision rather than a
statement of intent, which is why nothing asks you to approve one.

**Owner.** The architect. It is the only role that changes a document filed
here. Every other role proposes an amendment and waits for the owner to decide
rather than editing one, and `yoyo amendment list` is what is waiting.

**Editing by hand.** You may, and nothing here refuses it — you are the
operator, and these documents are yours. Two things follow rather than nothing.
Record the change in the document's own revision log under the architect: a
revision recorded under any other role is reported as unauthorized every time
the artifacts load, which is something to look at rather than a refusal. And
whatever downstream traced to what you changed is reported by `yoyo stale`
until its own owner revises it. Then say what you changed in `yoyo agent chat
architect`, because a conversation that is already open is working from these
documents as they read when it opened.

This file is a directory index rather than an artifact: it carries no identity
frontmatter, nothing refers to it by id, and artifact governance skips it.
`yoyo init` writes it and `yoyo doctor` reports it missing, so editing it is
safe and deleting it is noticed.
