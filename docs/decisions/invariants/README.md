# docs/decisions/invariants

**Purpose.** The architectural invariants: one Markdown file per durable
constraint, named by its id, carrying what must hold, why, what established it,
and its revision history. The harness selects the ones relevant to a work item
and delivers them into the developer's context and the reviewer's evidence, so
a constraint holds even where the work item never mentions it.

**Owner.** The architect, and no other role at all. A developer or a reviewer
that believes an invariant is wrong leaves it active and proposes the
amendment in what it reports, for the architect to decide.

**Editing by hand.** You may, and nothing refuses it. What `yoyo invariant`
does that an editor does not is record who changed the constraint and why, so
an edit made by hand leaves a history that has stopped accounting for itself.
Retiring one is `yoyo invariant retire` rather than deleting the file: the file
stays, the constraint stops being delivered, and the reason it was lifted stays
readable. Then say what you changed in `yoyo agent chat architect`, because a
conversation that is already open is working from these constraints as they
read when it opened — and every run started since is being handed the ones you
edited.

These carry a scheme of their own rather than artifact identity frontmatter —
the file name is the id — which is why this directory is skipped when the
artifacts are loaded even though it usually sits inside the decisions home.

This file is a directory index rather than an artifact: it carries no identity
frontmatter, nothing refers to it by id, and artifact governance skips it.
`yoyo init` writes it and `yoyo doctor` reports it missing, so editing it is
safe and deleting it is noticed.
