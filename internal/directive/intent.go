package directive

// What a sentence is for, read before it is recorded.
//
// The record used to have one category for everything a person said in a work
// item's thread: a directive. On 2026-08-30 the operator asked in a thread what a
// phrase in a receipt meant, and the inbound machinery — having nothing else to
// do with a sentence — recorded the question as a standing instruction and then
// acknowledged it with the same phrase he had asked about. Nothing answered him,
// and the record held a question as if somebody had directed it. "Did you
// restart?" went the same way a week later.
//
// Enforceability presumes the record holds only things somebody actually
// directed, so a question has to be told from an instruction before either is
// written down. This is that reading, and it is deliberately a stated rule
// rather than a classifier: a question mark at the end is a question, a sentence
// that opens with an interrogative and ends with no mark is one nobody should
// guess about, and everything else is what the person said to do. The reading
// cannot silently stop work — the pausing kinds are still stated by the person,
// never inferred — and the one thing it can get wrong on its own is to treat a
// question as an instruction, which is the defect it exists to end and the case
// the question mark decides outright.
//
// It lives here rather than in the channel that reads replies because the record
// wants the same reading of its own contents: a directive already recorded from a
// question is found by the same rule, so the listing can say which of its entries
// direct nothing.

import (
	"strings"
	"unicode"
)

// Intent is what a sentence somebody typed at the harness is for.
type Intent string

const (
	// IntentInstruction is something to record: the person said what to do.
	IntentInstruction Intent = "instruction"
	// IntentQuestion is something to answer, and nothing to record.
	IntentQuestion Intent = "question"
	// IntentUncertain is a sentence the reading will not decide: it opens like a
	// question and ends like an instruction, or asks something and then goes on.
	// What that gets is one line asking which was meant, rather than a guess
	// written into the record.
	IntentUncertain Intent = "uncertain"
)

// interrogativeOpenings are the words a question opens with when it does not end
// with a mark. A sentence starting with one and ending with no question mark is
// as likely "what I want is the smaller change" as "what does this mean", so it
// is uncertain rather than either. Apostrophes are folded out before the word is
// looked up, so "what's" and "isn't" are found under their folded forms.
//
// "do" is deliberately absent: it opens an imperative — "do the smaller change",
// "do not refactor the store" — far more often than a question with no mark on
// it, and asking back on every one of those would teach an operator to stop
// typing in this channel.
var interrogativeOpenings = wordSet(
	"what", "whats", "why", "whys", "how", "hows", "when", "whens", "where", "wheres",
	"who", "whos", "whom", "whose", "which",
	"is", "isnt", "are", "arent", "am", "was", "wasnt", "were", "werent",
	"does", "doesnt", "did", "didnt",
	"can", "cant", "could", "couldnt", "should", "shouldnt", "would", "wouldnt",
	"will", "wont", "shall",
	"has", "hasnt", "have", "havent", "had", "hadnt",
	"may", "might",
)

func wordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}

// ReadIntent reads what one sentence is for.
//
// A sentence ending in a question mark is a question, whatever it opens with. A
// question mark anywhere else — a question followed by more — is uncertain,
// because the part after it may be the instruction the question was leading up
// to. A sentence with no mark that opens with an interrogative is uncertain for
// the reason given on the list. Everything else is an instruction, which is the
// reading the channel always had and the one that cannot stop work.
func ReadIntent(text string) Intent {
	said := strings.TrimSpace(text)
	if said == "" {
		return IntentUncertain
	}
	// A question mark is read through whatever closes the sentence after it: a
	// quote, a bracket, or Slack's own emphasis marks.
	closing := strings.TrimRightFunc(said, func(character rune) bool {
		return unicode.IsSpace(character) || strings.ContainsRune("\"'”’)]*_~`", character)
	})
	if strings.HasSuffix(closing, "?") {
		return IntentQuestion
	}
	if strings.Contains(said, "?") {
		return IntentUncertain
	}
	if interrogativeOpenings[openingWord(said)] {
		return IntentUncertain
	}
	return IntentInstruction
}

// openingWord is the first word of a sentence as the list above files it: lower
// case, letters only, apostrophes folded out so a contraction is one word.
func openingWord(said string) string {
	var word strings.Builder
	for _, character := range strings.ToLower(said) {
		switch {
		case unicode.IsLetter(character):
			word.WriteRune(character)
		case character == '\'' || character == '’':
			continue
		case word.Len() > 0:
			return word.String()
		}
	}
	return word.String()
}

// ReadsAsQuestion reports an operational directive whose text is a question.
// Such a record directs nothing — nobody said what to do — and it is the shape
// every directive mis-recorded before questions were told apart has, so this is
// what a listing marks them by. The pausing kinds are never read this way: an
// ambiguous directive is the operator's own question, stated as one on purpose,
// and it is holding work until it is answered.
func (d Directive) ReadsAsQuestion() bool {
	return d.Kind == KindOperational && ReadIntent(d.Text) == IntentQuestion
}
