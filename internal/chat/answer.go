package chat

// An operator answers a concern from the command line the way they decide a
// proposal from it: by naming what they are answering. A concern is a question
// the product manager stopped on rather than an offer to create anything, so an
// answer to one approves nothing — and the whole reason this grammar exists is
// that, before it, an answer to a question and an approval of a proposal were
// the same word arriving at a conversation that could only read it one way.

import (
	"fmt"
	"strconv"
	"strings"
)

// answerVerb is the word that opens an answer to a concern by name. It is
// optional: the concern's own identifier is the thing nobody writes by
// accident, so "c3.1 the goal stands" and "answer c3.1 the goal stands" are the
// same answer.
const answerVerb = "answer"

// AnswerOutcome is what became of one concern the operator answered from a
// message. It is the concern's counterpart of DecisionOutcome: the same act the
// prompt records, reported once in a shape a script can read, where there is no
// prompt to print underneath.
type AnswerOutcome struct {
	ConcernID string `json:"concern_id"`
	// Subject is the work the question was about, so the report names what was
	// answered rather than only which identifier.
	Subject string `json:"subject"`
	// Answer is what the record kept: the operator's own words, or the answer on
	// offer they picked by its number.
	Answer string `json:"answer"`
}

// Render describes one answer for an operator reading what their message did.
func (a AnswerOutcome) Render() string {
	return fmt.Sprintf("[%s] answered: %s\n", a.ConcernID, strings.TrimSpace(a.Subject)) +
		indent("you said: "+a.Answer) +
		indent("it reaches the product manager when you next say something")
}

// namesAConcern reports a message that answers a concern by its identifier, and
// returns the identifier and the answer after it. The shape is the identifier
// itself, with or without "answer" in front of it, and then whatever the
// operator has to say: the identifier is what the harness prints and what
// nobody types into ordinary speech, so everything after it is the answer, and
// prose after it is exactly what a question is answered with.
//
// A message that merely mentions a concern's identifier somewhere inside a
// sentence is not an answer, for the reason a proposal mentioned mid-sentence is
// not a decision: the operator is talking, and what they are saying about the
// question is for the product manager to hear.
func namesAConcern(message string) (id, answer string, names bool) {
	first, rest := nextWord(strings.TrimSpace(message))
	if strings.EqualFold(first, answerVerb) {
		first, rest = nextWord(rest)
	}
	if !isConcernID(first) {
		return "", "", false
	}
	return first, strings.TrimSpace(rest), true
}

// isConcernID reports the c<turn>.<position> shape a raised concern is named
// by. The leading letter is what keeps it apart from a proposal's turn.position,
// so a message naming one can never be read as naming the other.
func isConcernID(value string) bool {
	rest, found := strings.CutPrefix(value, "c")
	return found && isProposalID(rest)
}

// answersAlone reports a message that is one bare answer word and nothing else.
// It is the one shape that answers a concern without naming it, and it is
// accepted only where the concern is the single thing the conversation is
// waiting on: there it does name it, exactly as a bare yes names the only
// proposal on the table. Anything longer is either speech, or an answer that
// should have said which question it answers.
func answersAlone(message string) bool {
	words := strings.Fields(message)
	return len(words) == 1 && (matches(words[0], approveWords) || matches(words[0], declineWords))
}

// chosenAnswer resolves an answer given as the number of one of the answers on
// offer to the answer itself, so what the record keeps is the option in the
// words it was offered in rather than the digit that picked it — which is what
// the prompt records when the operator chooses there. An answer that is not a
// number, or a number naming nothing on offer, is the operator's own words and
// is kept as they are.
func chosenAnswer(concern PendingConcern, answer string) string {
	number, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil || number < 1 || number > len(concern.Concern.Options) {
		return answer
	}
	return strings.TrimSpace(concern.Concern.Options[number-1])
}
