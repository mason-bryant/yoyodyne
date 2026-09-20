# yoyodyne-ifd.68.23: a question in a thread was recorded as a directive because the inbound half had one category

On 2026-08-30 the operator asked, in a work item's Slack thread, *What does 'in
force from now' mean?* The sink recorded the question as an operational
directive against the item and answered:

> Recorded, for the Product Manager: What does 'in force from now' mean? — it is
> in force from now, and nothing waits on it.

Nothing answered him, and the directive record held his question as a standing
instruction every run of that item then read. On 2026-09-05 *Did you restart?*
went the same way, and was withdrawn by hand.

## The mechanism

`internal/slack/inbound.go` read every reply through one grammar: a stated
`resolve`, `ambiguous:`, or `artifact:` opening, and otherwise an operational
directive in the operator's own words. That default was chosen so that nothing a
classifier guessed could stop work, which is right, and it had no second
category for a sentence that directs nothing. A question fell through to the
default and was written into `directives/` exactly as an instruction would be.

The receipt then read the record back. `KindDirectiveRecorded`'s voice line
quotes what was recorded and adds what it did (`effectOf`), which for an
operational directive was *it is in force from now*. Asked what the phrase
meant, the channel repeated the phrase — the second defect in the screenshot,
and the general rule the operator drew from it: a receipt must not explain a
term by restating it.

## The fix

What a reply is for is read before anything is recorded, by one stated rule in
the directive package (`internal/directive/intent.go`): a reply ending in a
question mark is a question; one that asks and goes on, or opens with an
interrogative and ends with no mark, is uncertain; everything else is an
instruction. The stated openings are not read this way — `ambiguous: which did
you mean?` is the operator's own question, recorded as one on purpose.

- A question is carried to the product manager's durable conversation — the
  same door an @-mention at the top of the channel uses, framed with the item
  whose thread it was asked in — and her answer is posted in the thread, tagged
  to whoever asked, in her name. The receipt ahead of it (`question.heard`) says
  the reply was heard as a question and that nothing was recorded, and does not
  quote the question back. The turn is taken before the receipt is posted, so a
  question that cannot be answered (no conversation behind the sink, or the
  product manager mid-turn) is refused outright rather than promised an answer.
- An uncertain reply is asked back in one line and recorded as nothing.
- An instruction is recorded exactly as before, and the receipt's wording had
  already moved from *in force* to *applies from now on* (yoyodyne-ifd.68.15).

## The record

A developer run cannot reach the durable directive store, so the entries
recorded this way are not corrected here. What is corrected is that they can be
found: `directive.Render` marks any operational directive that still applies
and whose text reads as a question — *reads as a question rather than an
instruction: it directs nothing, and withdrawing it is what ends it* — and both `yoyo
directive list` and `/directives` carry the mark. Withdrawing them is the
operator's, with `yoyo directive withdraw --by <who> --reason <why> <id>`; the
fixture's own entry is the one the notes record as withdrawn by hand.

## What the replay shows

`TestAQuestionInAThreadIsAnsweredByTheProductManagerRatherThanRecorded` in
`internal/slack/intent_test.go` replays the screenshot against a sink with both
the directive record and the product manager's conversation wired: the record
stays empty, the receipt neither says *Recorded* nor contains *in force*, and
the product manager's answer follows it in the thread under her name.
`TestWhatASentenceIsFor` in `internal/directive/intent_test.go` pins the rule
on both fixture sentences and on the instructions that must keep reaching the
record.
