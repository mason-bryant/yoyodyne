package slack

// What one reply is: something asked, something told, or something that could be
// either.
//
// The inbound half had one category. Every reply that stated no kind of its own
// was direction, so a question typed into a thread became a directive: the
// durable record — the product's account of what somebody actually instructed —
// held a question, and every later run of that item met it. On 2026-08-30 the
// operator asked in a thread what a phrase in one of these acknowledgments
// meant, and was answered that their question was recorded and in force from
// that moment, in the phrase they were asking about.
//
// So a reply that states nothing about its own shape is read here first. The
// reading decides one thing and one thing only: whether anything is written to
// the directive record at all. It never decides that work stops — the pausing
// kinds are still stated and never inferred, which is the rule this file is
// careful to leave exactly where it was — and both of its non-direction answers
// record nothing, so the most a misreading can cost is a sentence somebody has
// to type again.
//
// Ambiguity is asked back rather than guessed. A reply that mixes a question
// with an instruction is the case where guessing is expensive in both
// directions: filed as direction it puts something nobody directed into the
// record, and filed as a question it drops an instruction silently. One line
// asking which it was costs a message, and the person who can settle it is
// already reading the thread.

import "strings"

// intent is what a reply that stated no kind of its own turns out to be.
type intent int

const (
	// intentDirection is the ordinary case and the one that existed before: words
	// the operator meant the work to be done differently after. It is the default
	// in the same sense operational is the default kind — anything the reading
	// below does not positively find to be a question is direction, said in the
	// operator's own words and recorded as it always was.
	intentDirection intent = iota
	// intentQuestion is a reply that asks and instructs nothing.
	intentQuestion
	// intentUnclear is a reply that could be either.
	intentUnclear
)

// askBack is what an unclear reply is answered with. One line, saying what the
// two readings would each cost, because somebody told only that they were not
// understood has no idea which half of what they wrote caused it.
const askBack = "that reads as both a question and an instruction, and either guess is wrong in a way you would have to undo — one puts something you never directed into the record, the other drops what you asked for; say it again as one or the other"

// interrogativeOpenings are the words that open a question and open nothing
// else. It is a stated list for the reason the pausing kinds are stated: the
// alternative is something guessing, and the guess is being made about whether
// the operator's own words go into a durable record.
//
// The auxiliaries are deliberately absent. "Do it the other way" and "will you
// stop refactoring the store" open with words a question also opens with, and a
// list holding those would read plain instructions as questions — which is the
// mistake this whole file exists to stop, made in the other direction.
//
// A reply opening with one of these and never reaching a question mark is
// unclear rather than a question: "what we need is the smaller change" is an
// instruction, and it is not this file's to decide which one somebody meant.
var interrogativeOpenings = []string{
	"what", "whats", "why", "how", "hows", "when", "where", "which", "who", "whom", "whose",
}

// intentOf reads one reply.
//
// The question mark is the whole of the positive test, because it is the one
// thing in a chat message the operator themselves put there to say they were
// asking. Every sentence ending in one is a reply that only asks; some of them
// and not others is a reply that does both; none of them, under a word that
// opens nothing but a question, is a reply whose mark was probably dropped and
// which is worth one line to settle.
func intentOf(said string) intent {
	sentences := sentencesOf(said)
	asked := 0
	for _, sentence := range sentences {
		if strings.HasSuffix(sentence, "?") {
			asked++
		}
	}
	switch {
	case len(sentences) == 0:
		return intentDirection
	case asked == len(sentences):
		return intentQuestion
	case asked > 0:
		return intentUnclear
	case opensInterrogatively(said):
		return intentUnclear
	default:
		return intentDirection
	}
}

// sentencesOf cuts a reply into the sentences it is made of, keeping the mark
// each one ended on, and dropping what is left between two marks with nothing in
// it. A line break ends a sentence as well: a reply typed as a list has no full
// stops in it and is still several things said rather than one.
//
// It is a reading of punctuation rather than of language. Only the final
// character of each sentence is ever looked at, so an abbreviation cut in half
// costs nothing: both halves end without a question mark, exactly as the whole
// would have.
func sentencesOf(said string) []string {
	var sentences []string
	var current strings.Builder
	end := func() {
		if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
			sentences = append(sentences, trimmed)
		}
		current.Reset()
	}
	for _, character := range said {
		switch character {
		case '.', '!', '?':
			current.WriteRune(character)
			end()
		case '\n':
			end()
		default:
			current.WriteRune(character)
		}
	}
	end()
	return sentences
}

// opensInterrogatively reports a reply beginning with a word that opens nothing
// but a question. It folds first, so the punctuation and the capital somebody
// typed decide nothing.
func opensInterrogatively(said string) bool {
	opening, _ := firstWord(fold(said))
	for _, interrogative := range interrogativeOpenings {
		if opening == interrogative {
			return true
		}
	}
	return false
}
