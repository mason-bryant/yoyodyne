package console

// A question whose answers can be counted is a different question from one that
// cannot: the operator is picking rather than composing, and a console that can
// address the screen can let them pick. What is shared between the consoles is
// here — what is on offer and how a typed answer is read as one of them —
// because the answers have to be the same wherever the question is put, and only
// the way they are put may differ.

import (
	"strconv"
	"strings"
)

// FreeEntryChoice is the last thing offered wherever a question enumerates its
// answers, in every console there is. A list somebody else wrote is a narrower
// question than the one that was asked: the operator may have an answer nobody
// listed, or a question of their own, and a prompt that cannot carry one is a
// prompt that makes them abandon it. So it is appended here rather than by
// whoever puts the question, and no caller can leave it out.
const FreeEntryChoice = "something else — answer in your own words"

// offered is what is actually put to the operator: the answers on offer with
// their own words at the end of them.
func offered(options []string) []string {
	return append(append(make([]string, 0, len(options)+1), options...), FreeEntryChoice)
}

// chosenNumber reads a line as the number of a choice, counted from one as the
// list is written. Anything else is not a number the list offers, and is taken
// as what the operator wanted to say rather than as a selection nobody can be
// sure of.
func chosenNumber(line string, count int) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || number < 1 || number > count {
		return 0, false
	}
	return number - 1, true
}

// sameQuestion reports whether these are the answers already on screen. It is
// what lets a choice survive being interrupted by something the harness had to
// write: the operator is put back where they had got to, and a different
// question starts at the top.
func sameQuestion(drawn, offered []string) bool {
	if len(drawn) != len(offered) {
		return false
	}
	for index, choice := range drawn {
		if choice != offered[index] {
			return false
		}
	}
	return true
}
