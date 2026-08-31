# The miscategorized questions in the live directive store

Work item: yoyodyne-ifd.215. **This document is the cleanup half of that item,
and that half is not done.** The withdrawal path the item asked for is built,
tested, and in this same change; the specific records it was built to end are
still recorded as in force in the operator's live store, because no developer
run can write there. The commands that end them are in
[What remains](#what-remains), and a person has to run them.

Anyone reading the item as done should stop here. The mechanism exists; the
fixture case has been measured, not cleaned up.

Read at 2026-08-31T13:49Z against the live store,
`~/Library/Application Support/Yoyodyne/state/products/yoyodyne/directives`,
six records.

## What is in the store

Six operational directives, all in force, because until this change nothing
could take an operational directive out of force. Two of them are directives:

| id | received | says |
| --- | --- | --- |
| `directive-e82486d6` | 2026-08-19T15:44:44Z | approvals are recorded with `yoyo artifact approve`, never by hand-editing frontmatter |
| `directive-da04b051` | 2026-08-19T20:14:49Z | work-item notes are append-only for every agent acting on the tracker |

Four are not. They are things the operator typed at the harness that the
one-category inbound machinery had no way to read as anything but a directive:

| id | received | says | scope |
| --- | --- | --- | --- |
| `directive-05d6bf25` | 2026-08-31T01:20:24Z | "Is this still running?" | `yoyodyne-ifd.194` |
| `directive-34b2f7aa` | 2026-08-31T01:21:02Z | "What does "in force from now" mean?" | `yoyodyne-ifd.194` |
| `directive-e0616231` | 2026-08-31T02:46:33Z | "Started?" | `yoyodyne-ifd.68.24` |
| `directive-f32c3573` | 2026-08-23T15:34:44Z | "— seems like the dev manager should prune worktrees from time to time per the comment above?" | `yoyodyne-ifd.82.2` |

The item names one question. There are four: the first three are one evening's
questions (2026-08-30 local, which is why the recorded UTC times read as the
31st), and the fourth is the same miscategorization a week earlier. The first
three are questions on their face and are listed below as the cleanup. The
fourth reads as a suggestion as much as a question, so whether it was ever a
directive is the operator's call rather than a developer's.

68.23's intent discrimination stops new ones being recorded. Nothing about it
reaches these.

## What could not be applied

The withdrawal was attempted against the live store from this run and refused by
the filesystem, twice over:

```text
$ yoyo directive withdraw --by … --reason … directive-05d6     # from inside the worktree
repository and worktree roots must not contain one another

$ yoyo directive list                                          # from outside it
6                                                              # the live records read fine
$ yoyo directive withdraw \
    --by "the yoyodyne-ifd.215 developer run (agent, not the operator)" \
    --reason "recorded in error: this was a question about a run, not an instruction" \
    directive-05d6
create temporary directive: open …/products/yoyodyne/directives/.directive-435362420.tmp: operation not permitted
```

Two boundaries, both working as designed:

- **A developer run's sandbox** permits writes inside its worktree and refuses
  them elsewhere. The store is under the state home, outside every worktree, so
  reads succeed and writes do not. The directory is mode 0700 owned by the same
  user running the command, so this is the sandbox rather than file permissions.
- **A worktree under the state home** cannot resolve a configuration whose
  repository root contains, or is contained by, the worktrees root — which is
  what the first refusal above is. It stops the command before the sandbox does.

Nothing was half-written. The store replaces a record by writing a temporary
file and renaming it, and the refusal lands on creating that file, so all six
records are byte-for-byte what they were and no temporary file was left behind.

## What remains

Three commands, run by a person from an ordinary checkout, with the live state
home. They were verified on a byte-for-byte copy of the six live records: the
in-force count went from six to three, and each withdrawn record still carries
the operator's words with the withdrawal under it.

```bash
yoyo directive withdraw --by "Mason, at a terminal" \
  --reason "recorded in error: this was a question about a run, not an instruction" \
  directive-05d6
yoyo directive withdraw --by "Mason, at a terminal" \
  --reason "recorded in error: this was a question, not an instruction" \
  directive-34b2
yoyo directive withdraw --by "Mason, at a terminal" \
  --reason "recorded in error: this was a question, not an instruction" \
  directive-e061
```

`--by` has no default, deliberately: agents run `yoyo` too, and a command line
does not say who typed at it. An agent doing this cleanup names itself and its
run rather than the operator, because that field is the whole of what the record
answers for who ended the directive.

`directive-f32c3573` is left alone. If the operator reads it as a question
rather than an instruction, it is a fourth command in the same shape.

Afterwards, `yoyo directive list` shows what is actually in force — the two
standing instructions — and `yoyo directive list --all` shows the withdrawn
questions reading as withdrawn, with what the operator said still on them.
