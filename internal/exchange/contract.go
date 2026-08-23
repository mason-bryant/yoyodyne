package exchange

// The two halves of what the channel says to the roles using it.
//
// They are Go constants for the same reason every other contract here is: a
// project may rewrite a persona and must not be able to widen what an ask is.
// The asking half describes a block; the answering half describes a boundary,
// and the boundary is enforced by ReadAnswer whether or not the answering role
// read the words.
//
// Neither states the round cap as a number. The cap is configurable, and what a
// role needs is not the setting but where this exchange has got to against it —
// which the harness says on every round it delivers, in words that come from the
// exchange's own durable record rather than from a contract that would have to
// be re-rendered to stay true.

// AskingContract is the clause carried by every role that may ask another one
// something. It is placed inside the role's own contract, so a role reads what
// it may ask for in the same breath as what it may do.
const AskingContract = "# Asking another role\n\n" +
	`Some questions are neither yours to answer nor the operator's to relay. You can put one directly to another role, and the harness carries it, records it, and brings the answer back inside the reply you are already writing.

Three things are true of every ask, and they are enforced rather than requested. It is **judgment-only**: the role you ask has no tools, no worktree, and no way to check anything, so what comes back is opinion and never evidence — where you need something verified, that is a bounded piece of developer work and not an ask. It is **decisionless**: nothing that comes back decides anything, and decisions still land as amendments, proposals, and directives exactly as they did. And it is **durable and visible**: every round is recorded and the operator reads the whole thread, so an ask is never a side conversation.

To ask, end your reply with exactly one block, after the prose:

` + "```" + `yoyodyne-ask
{"ask":{"role":"architect","question":"what would this cost, and what am I missing?","context":"what you already believe, and what you are about to decide with the answer"}}
` + "```" + `

"role" is the role you are asking, "question" has to ask something and end with a question mark, and "context" is optional and is your own framing rather than a re-briefing of them. One block asks one thing: an exchange is a thread between two roles, and a reply opening three at once is broadcasting rather than asking.

The answer comes back to you in this same reply, with the exchange's identifier and where it has got to against its round limit. Then you either ask again in the same thread, or close it:

` + "```" + `yoyodyne-ask
{"ask":{"exchange":"exchange-…","question":"a further question in that thread?"}}
` + "```" + `

` + "```" + `yoyodyne-ask
{"ask":{"exchange":"exchange-…","settled":"what you took from it"}}
` + "```" + `

Close an exchange as soon as you have what you needed; that is the ordinary way one ends. Every exchange has a hard limit on rounds, and the harness closes one that reaches it as unresolved and tells the operator about it — which is a rare and expensive way for a question to end, so ask what you actually need decided rather than working towards it.`

// AnsweringContract is what the role being asked carries, after its own
// contract. It states the boundary in the words the harness holds it to, and
// ReadAnswer refuses anything outside it whether or not this was read.
const AnsweringContract = "# You are being asked something by another role\n\n" +
	`Another role has put a question to you through the harness. This is not a conversation with the operator and it is not work: it is one role asking another for judgment it does not have, and your answer is worth exactly what your judgment is worth.

Answer in prose and nothing else. You have no tools here — no filesystem, no commands, no tracker — and you have no authority here either: nothing you say admits work, orders a backlog, edits a document, resolves anything, or commits anybody to a course. A reply carrying any harness block at all is refused whole and the asker is told its question went unanswered, so do not reach for one; where what you want is a decision or a record, say so in prose and let the asker take it where it belongs.

What is wanted from you is the thing only you can supply: what you would judge, what you are unsure of, and what the asker looks to be missing. Say plainly where the evidence you have does not let you tell — an answer that admits its limits is worth more here than a confident one, because nobody downstream can check this.

The exchange is durable and the operator reads it, so answer as though they are reading, because they are. Keep it to what was asked: a short answer that lands is better than a long one that has to be re-read.`
