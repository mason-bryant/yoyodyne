# yoyodyne-ifd.100.1: the gate answered, and the answer was no

yoyodyne-ifd.100.1 asks for the last clause of yoyodyne-ifd.100: *after the
operator's `y` on a typed artifact write, the harness commits the document under
its own identity and opens or updates a pull request per the architect's settled
shape; the primary checkout is left clean so the next run is not blocked; the
interim documentation claim that committing is the operator's is retracted; tests
cover approve-then-commit-then-publish and the refusal paths.* It was written
blocked, on a design answer that did not exist yet, and yoyodyne-ifd.100.2 was
carved to be that gate.

**The gate answered, and the answer negates every one of those conditions.** The
architect ruled that a typed artifact write stops at the operator's working tree:
no harness commit, no pull request, and committing stays the operator's — not as
an interim state to be retracted later, but as the settled boundary. There is no
publish step to build. That is the whole finding, and this document is the
evidence for it, because two runs before this one reached the same conclusion,
delivered empty trees, and were refused twice for delivering an absence with no
account attached to it.

## The ruling, and where it lives

yoyodyne-ifd.100.2 — *The architect settles the publish shape for approved
artifact writes* — was closed 2026-08-31T15:46:30Z, close reason *"The architect
settled the shape: identical to her conversation-documents rulings, stated whole
on this item; 100.1 builds against it."* The ruling is recorded in that item's
notes and reads, whole:

> Architect ruling, 2026-08-31, stated whole: the typed artifact-write's output
> stops at the operator's working tree. Authorize records approval in frontmatter
> against the revision produced; publication is the operator's own commit under
> their own identity; until they commit, the document is an uncommitted change and
> runs refuse to start over it — and the surface that performed the write says so
> at the moment of writing. Added reason: no path into the target branch exists
> except a reviewed promotion or the operator's hand — a harness-made commit would
> be a promotion by another name sweeping in unapproved tree state — and the
> working-tree stop keeps the artifact read model two-valued: committed state, or
> operator-pending edit visibly blocking runs. No committed-but-never-operator-touched
> third state.

Set the item's four done conditions against it:

| The item asks for | The ruling says |
| --- | --- |
| the harness commits the document under its own identity | publication is the operator's own commit under their own identity; a harness-made commit would be a promotion by another name |
| the harness opens or updates a pull request | the write's output stops at the working tree; there is no publication for the harness to open |
| the primary checkout is left clean so the next run is not blocked | until the operator commits, the document is an uncommitted change and runs refuse to start over it — the block is the design, not a defect |
| the interim claim that committing is the operator's is retracted | committing *is* the operator's; the claim was never interim |

"Per the architect's settled shape" resolves, on every clause, to *do not*. An
implementation satisfying the item as written would have to overrule the gate the
item cites, which is the one thing a developer run may not do — the ruling belongs
to the architect, and a run that disagrees with it proposes a change rather than
building past it.

The two governing reasons are worth keeping in view, because they are not
preferences. A harness commit onto the target branch is a publication reached by
neither of the two routes the repository admits, so it lands unapproved tree state
with no check evidence and no review behind it: that is what
`integration-requires-revision-bound-evidence` refuses, and it is
`one-promotion-per-target-branch`'s boundary — the roles that authorize a
promotion must not be able to perform one — read onto documents. The second reason
is the read model: committed, or operator-pending and visibly blocking runs, with
no third state that is on the branch but no human ever touched.

## The second reason it is not buildable here

Independently of the ruling, the mechanism this item extends is not in this base.
ifd.100's typed artifact-write action was delivered on
`yoyodyne/yoyodyne-ifd-100/43d88b2c`, whose tip `91357b7` is **not** an ancestor of
`main` at `15f6b1a`. In this tree:

- `internal/chat/document.go` and `internal/artifact/write.go` — the action, its
  rendering, and its Authorize path — do not exist; they exist only on that branch.
- `yoyo artifact` dispatches `list`, `show`, and `approve` and nothing else
  (`internal/cli/artifact.go`).
- `docs/artifacts.md` still says of the ownership refusals that *"no command
  reaches that path yet, so what it constrains today is nothing that is
  happening."*

So there is no operator `y` on a typed artifact write in this tree to hang a
commit off, and no interim documentation claim in this tree to retract — the
paragraph the item calls interim is on the unmerged branch, not on `main`. Even
had the ruling gone the other way, this item could not have been built at this
base. ifd.100 is still `open`.

## Why the empty trees were refused

Runs `run-fdb0cadd85c8d6e05de322f68cce662f` and
`run-cee7ab608130eef9ff3724ff618f0c58` each submitted a clean worktree and an
empty patch, offering `fmtcheck`, `test`, `race` and `vet` passing on an
unmodified tree as their evidence. Both reviewers refused, and both were right to:
those checks demonstrate that the repository was already green, and an absent
change carries no account of itself. The reviewer of the second run named the fork
exactly — *either the shape is recorded, in which case implement it, or it is not,
in which case do not invent one and return the item as blocked* — but could not
resolve which, because the ruling is in the tracker's notes and a reviewer is
shown the diff.

The conclusion those runs reached was correct. What was missing was a landing: a
reader of this item, at this base, needs to be able to find out in one file why
there is no code, and the tracker note that holds the answer is not a thing the
review path reads. That is what this document is.

## What has to happen before ifd.100.1 can be discharged

Nothing a developer run can do. Two decisions, in this order:

1. **The item needs to be retired or rewritten**, by the product manager, against
   the ruling it was gated on. Its done conditions describe a shape the architect
   declined; there is no version of "commit and publish an approved artifact
   write" that both satisfies this item and respects `100.2`. If anything survives
   the rewrite it is the half the ruling does require — *the surface that performed
   the write says so at the moment of writing* — and that half is already
   implemented on ifd.100's branch, so it belongs to the parent rather than here.
2. **ifd.100's own delivery needs to land or be retired.** Its branch has been
   unmerged since 2026-08-23 and carries the whole mechanism plus documentation
   that is now stale: `docs/artifacts.md` on that branch tells the reader that
   whether the harness should commit or open a pull request *"is an open question
   rather than a settled boundary, and it is the architect's to answer"*, and
   points at a filed amendment. The question is answered. That paragraph should
   state the settled boundary instead — but it is on a branch this base does not
   contain, so it is named here rather than edited.

One gap is worth recording separately. ifd.100.2's own done condition was that the
shape be *"recorded in an architect-owned design or decision record"*; it was
closed with the ruling stated in the tracker item's notes. The governed designs do
not carry it — `docs/designs/artifact-contract.md` says nothing about how an
artifact write reaches disk. The tracker is not readable from a developer run, and
it is not what a reviewer is shown, which is precisely how two runs came to spend
six review rounds on a question that had been settled for a week.
