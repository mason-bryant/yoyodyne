# yoyodyne-ifd.289: the README's account of how work gets pulled is already true

The item was admitted on three developer reports naming a self-contradiction in
main's README: a paragraph landed 2026-08-16 saying nothing pulls from the
backlog on its own and that the development manager taking from the top of it
"is not built", against a scheduler that landed 2026-08-19. The item's own
instruction was to verify first whether a later sweep had already repaired it.

It had. Every sentence the reports named was gone before this run was dispatched,
and this is the record of how that was established — from the repository's own
history rather than from memory, so the next reader does not derive it again.

## The false text, and the commit that removed each copy

`0e1ef97` (yoyodyne-ifd.18, 2026-08-16) put the claim into `README.md` in two
places: a bullet in the bounds list, and a standalone paragraph under the
backlog-ordering paragraph.

> Nothing pulls from that order on its own yet, and the difference is worth
> stating plainly: the harness runs a work item when you ask it to with `/work`,
> and the development manager that would take from the top of the backlog
> without being told is not built.

| copy | where it lived | removed by | when |
| --- | --- | --- | --- |
| the bounds bullet | `README.md` | `26fd3d6` (yoyodyne-ifd.3, "Add scheduler and concurrent worktrees") | 2026-08-19 |
| the standalone paragraph, as extracted | `docs/conversation.md` | `8ee52a3` (yoyodyne-ifd.121.3) | 2026-08-20 |
| the standalone paragraph, in the README | `README.md` | `586deb8` (yoyodyne-ifd.160, "README.md is actually reduced against the extracted documents") | 2026-08-24 |

The scheduler's own commit rewrote the bullet as it landed, so the README's
summary line has been true since the day the mechanism shipped. The extraction
tranche copied the stale standalone paragraph into `docs/conversation.md` in
`244c9d8` and corrected it in `8ee52a3` later the same day, which is why a report
filed against the tranche found it there. What survived longest was the README's
own duplicate of that paragraph, which the reduction in `586deb8` deleted rather
than rewrote — the extracted document was already carrying the correct account.

All four commits are ancestors of this run's base, `384cef4`.

## What the two documents say now

`README.md:106-118` states the shipped behaviour: `yoyo work` is the harness
choosing for itself, pulling ready items from the top of the backlog and running
up to `execution.max_concurrent_developers` at once, with the product manager
still owning the order — and links to
[`docs/work.md#letting-the-harness-choose-the-work`](../work.md#letting-the-harness-choose-the-work).
`docs/conversation.md:35-42` states the same thing from the conversation's side:
`/work` is you naming an item, `yoyo work` reads the same order, `/hold` stops it
choosing. The README carries no sentence about pulling that
`docs/conversation.md` or `docs/work.md` contradicts, and the word "scheduler"
appears nowhere in it.

## The second satellite: the product manager called "she"

The same reports named `README.md:798`, which read *the product manager is never
asked, because she cannot carry out a command* where the document uses "it"
throughout. That line is one of the duplicates `586deb8` deleted from the README;
the surviving copy in `docs/conversation.md` was corrected to "it" by `c2ad654`
(yoyodyne-ifd.81, 2026-08-27) and reads that way at `docs/conversation.md:475`.
No gendered pronoun for any role remains in `README.md`.

Three of the same class did remain in documents outside the README, each
referring to a role the surrounding prose calls "it", and this run swept them:

| file | role | what it said |
| --- | --- | --- |
| `docs/work.md` | development manager | "for somebody to tell her" |
| `docs/terms.md` (×2) | architect | "until she decides otherwise", "what she decided on a date" |
| `docs/authority-inventory.md` | development manager | "in front of her", "what she may decide once she is there" |

One more is in a document a developer run may not edit:
`docs/decisions/invariants/developer-verifies-before-submitting.md` gives the
architect "her" and the operator "his" inside a recorded amendment reason in its
frontmatter. It is left alone deliberately: an amendment reason is an account of
what somebody decided on a date, and rewriting one is the architect's call.
`docs/reporting.md` gives the operator "his" and "him" twice, which is a
statement about a person rather than a role and is not this class.

## What this run verified, and how

- `make fmtcheck && make vet && make test` at `384cef4`, before any edit: exit 0.
- `make adoption` at `384cef4`: exit 0 — *the documented adoption path works as
  written*, with one claim named as unexercisable in this environment (the
  provider step, which needs `WALK_PROVIDER=1`). The walkthrough asserts the
  getting-started sections this item's paragraph sits above, so its claims hold.
- The history above, read with `git log -S` against `README.md` and
  `docs/conversation.md` for each phrase the reports quoted.
