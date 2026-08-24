package notify

// What became of a reply, on the reply itself.
//
// A thread reply that steers the work is answered by a message saying what was
// recorded, and that message is the account. This is the other half, and it is
// the twin of the status a thread's opener carries: the message the operator
// typed wears where their directive stands, so somebody scrolling back through
// what they said can see which of their own replies is still open and which has
// been answered without reading a word under any of them.
//
// It is the directive's lifecycle rather than the harness's handling of the
// reply, which is the distinction the whole thing turns on. Recording a
// directive is not disposing of it: what settles one is somebody carrying it
// out, deciding the document it names, or saying what they meant, and that is
// hours or days after the acknowledgment. So the thinking face stands for
// exactly as long as the record says the directive is unresolved, and the check
// mark lands at the moment the outcome is said in the thread.
//
// It is three marks rather than prose because a reply's standing is a fact about
// one message and belongs on it: a second message saying "still open" every time
// somebody looked would bury the thread it was posted in.
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
	// ReceiptUnderConsideration is a reply whose directive is recorded and not
	// yet settled. It goes on as the reply arrives, so the gap between somebody
	// typing and the answer landing says "heard" rather than nothing, and it
	// stays on while the directive stands unresolved — which is the honest
	// answer to where it got to, however long that is.
	ReceiptUnderConsideration Receipt = "under-consideration"
	// ReceiptSettled is a reply whose directive has been settled: the one it
	// recorded, once somebody resolved it or carried it out, or the one it
	// resolved itself. It lands at the same moment the outcome is said in the
	// thread, never at the moment the directive was written down. What settled it
	// is in the thread; this says there is an answer to read.
	//
	// Carrying out is what most replies reach it by, because most replies record
	// an operational directive: it never paused anything, so the only thing that
	// ever moves this mark off the thinking face is somebody recording what came
	// of it.
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
