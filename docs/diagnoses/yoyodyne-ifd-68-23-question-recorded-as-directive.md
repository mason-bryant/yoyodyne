# yoyodyne-ifd.68.23: the question the record kept as an instruction

On 2026-08-30 the operator replied in a work item's thread, asking what a phrase
in one of the harness's own acknowledgments meant:

```text
What does 'in force from now' mean?
```

The inbound half had one category for a reply that stated no kind of its own, so
the question became an operational directive. The acknowledgment then answered
it with the phrase it was asking about, twice — once as the directive's effect
and once as the clause saying whose move followed:

```text
directive-… was recorded from this thread, received by the Product Manager:
What does 'in force from now' mean? — it is in force from now, and nothing
waits on it. Next: the harness's — the directive is in force from now, and no
work is waiting on it.
```

Two defects, and they are separate.

**The record took a question as an instruction.** The directive record is the
product's account of what somebody directed, and every run of the item it is
scoped to is held to it before it starts, before it resumes, and at its gate. An
operational directive is in force from the moment it is recorded and nothing
retires one, so a question filed there is a standing instruction nobody gave and
nothing can withdraw.

**The receipt explained a term by restating it.** Both clauses reached for "in
force from now", so an operator asking what it meant read it twice more.

## What changed

`internal/slack/intent.go` reads a reply that stated no kind of its own, and
decides one thing: whether anything reaches the directive record. A reply whose
sentences all end on a question mark is a question — written down in the sink's
own state, answered in the thread, and kept out of the record. A reply that
mixes a question with an instruction, or that opens on a word only a question
opens on and never reaches a mark, is asked back in one line and recorded as
nothing. Everything else is direction, recorded exactly as before.

The reading never decides that work stops. The pausing kinds are still stated by
the operator and inferred from nothing, and a reply that opened with one of them
is taken at its word without being read again.

`effectOf` and `directiveInForceMove` in `internal/notify/voice.go` now say what
"in force" amounts to rather than saying the phrase, and
`TestNoReceiptExplainsAPhraseByRestatingIt` holds the two clauses of one receipt
to sharing no phrase.

## What this change could not correct

The directive recorded from the fixture is in the operator's own state root and
is not reachable from a development worktree, so nothing here corrected it.
Nothing else can either: `Resolve` refuses a directive that pauses nothing,
`CarryOut` has no command-line surface, and `InForce` returns true for every
operational directive because retiring one is a lifecycle the record does not
have. Until it does, that question is listed as in force by `yoyo directive list`
and met by every run of the item whose thread it was asked in.

Retiring or withdrawing a directive is the work that closes this, and it is not
this item's.
