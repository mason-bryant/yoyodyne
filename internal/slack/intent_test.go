package slack

import "testing"

// What the reading is and is not allowed to decide, case by case. The two
// directions do not cost the same: reading direction as a question drops an
// instruction, and reading a question as direction puts something nobody
// directed into the record every later run of the item is held to. Only the
// second one has already happened.
func TestWhatOneReplyTurnsOutToBe(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		said string
		want intent
		why  string
	}{
		{
			said: "What does 'in force from now' mean?",
			want: intentQuestion,
			why:  "the fixture: one sentence, and the operator's own question mark on it",
		},
		{
			said: "why is the store read twice? does anything depend on the second read?",
			want: intentQuestion,
			why:  "every sentence asks, so the whole reply asks",
		},
		{
			said: "prefer the smaller change here",
			want: intentDirection,
			why:  "nothing asks, and this is the ordinary reply the record exists for",
		},
		{
			said: "do it the other way",
			want: intentDirection,
			why:  "an instruction opening on an auxiliary is not a question, which is why the openings list holds none",
		},
		{
			said: "stop refactoring the store. why is it read twice?",
			want: intentUnclear,
			why:  "one sentence tells and one asks, and either guess loses half of it",
		},
		{
			said: "what we need here is the smaller change",
			want: intentUnclear,
			why:  "it opens on a word only a question opens on and never reaches a mark",
		},
		{
			said: "don't refactor the store as well — e.g. leave the cursor alone",
			want: intentDirection,
			why:  "an abbreviation cut in half leaves two halves that still end on no question mark",
		},
		{
			said: "ship the smaller change\nleave the store alone",
			want: intentDirection,
			why:  "a reply typed as a list is several instructions rather than one long one",
		},
	} {
		if got := intentOf(testCase.said); got != testCase.want {
			t.Errorf("intentOf(%q) = %v, want %v — %s", testCase.said, got, testCase.want, testCase.why)
		}
	}
}
