package notify

// What became of a reply, on the reply itself.
//
// A thread reply that steers the work is answered by a message saying what was
// recorded, and that message is the account. This is the other half, and it is
// the twin of the status a thread's opener carries: the message the operator
// typed wears what became of it, so somebody scrolling back through what they
// said can see which of their own replies landed without reading a word under
// any of them.
//
// It is three marks rather than prose for the reason a status is: the lifecycle
// of one reply moves twice in a few seconds — heard, then settled — and two
// messages saying so would bury the acknowledgment they are about.
//
// The set is small and fixed for the same reason the statuses are, and it is
// three words rather than a vocabulary that grows: a reader already knows every
// mark they can see. Nothing here shares a message with a status — a status is
// only ever on the message a thread hangs from, and a receipt is only ever on a
// reply inside one — so the check mark the two have in common is never two
// meanings on one message, and it is the mark somebody who has just been
// answered actually expects.

// Receipt is what became of one reply the harness read. The three are the whole
// set.
type Receipt string

const (
	// ReceiptUnderConsideration is a reply the harness has read and not yet
	// disposed of. It goes on as the reply arrives, so the gap between somebody
	// typing and the answer landing says "heard" rather than nothing.
	ReceiptUnderConsideration Receipt = "under-consideration"
	// ReceiptSettled is a reply whose disposition has landed: a directive
	// recorded, or one somebody settled. What it was is in the thread; this says
	// there is an answer to read.
	ReceiptSettled Receipt = "settled"
	// ReceiptRefused is a reply that recorded nothing — from somebody the project
	// granted nothing, in a thread that is not a work item's, or saying something
	// the grammar refuses. It is its own mark rather than the absence of one,
	// because a reply that did nothing and a reply nobody read look identical
	// otherwise.
	ReceiptRefused Receipt = "refused"
)

// Receipts is the whole set, in the order one reply reaches them. A caller that
// has to cover every receipt reads it from here rather than repeating the list.
func Receipts() []Receipt {
	return []Receipt{ReceiptUnderConsideration, ReceiptSettled, ReceiptRefused}
}

// Valid reports whether a name is one of the three. Anything else has no
// symbol, so a surface refuses it rather than marking somebody's message with
// something no reader has been told the meaning of.
func (r Receipt) Valid() bool {
	switch r {
	case ReceiptUnderConsideration, ReceiptSettled, ReceiptRefused:
		return true
	default:
		return false
	}
}

// Symbol is the emoji shortcode a surface marks the reply with. They are stated
// here beside the words for the reason the statuses' are: the mark is part of
// the vocabulary, and a second surface must not be able to say the same thing a
// second way.
//
// Every one of them is an emoji Slack ships in every workspace, because a custom
// name would render in the workspace it was chosen in and be refused in every
// other. None of them repeats a severity's mark.
func (r Receipt) Symbol() string {
	switch r {
	case ReceiptUnderConsideration:
		return "thinking_face"
	case ReceiptSettled:
		return "white_check_mark"
	case ReceiptRefused:
		return "no_entry_sign"
	default:
		return ""
	}
}
