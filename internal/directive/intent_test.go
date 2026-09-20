package directive

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The two sentences the record actually took as instructions are the fixture:
// the operator's question about a receipt's own phrase, and "Did you restart?" a
// week later. Both are questions, outright, and so is anything ending on a mark
// however it opens; what the operator actually directs from a thread is an
// instruction; and a sentence the rule cannot settle is neither.
func TestWhatASentenceIsFor(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		said string
		want Intent
	}{
		"the screenshot":                 {"What does 'in force from now' mean?", IntentQuestion},
		"the second misrecord":           {"Did you restart?", IntentQuestion},
		"a mark through a closing quote": {`what does "in force" mean?"`, IntentQuestion},
		"a mark through emphasis":        {"is this done?*", IntentQuestion},
		"a question that opens plainly":  {"the smaller change is what landed?", IntentQuestion},
		"an instruction":                 {"prefer the smaller change here — don't refactor the store as well", IntentInstruction},
		"an instruction opening with do": {"do the smaller change", IntentInstruction},
		"a negative instruction":         {"do not refactor the store", IntentInstruction},
		"a contraction instruction":      {"don't refactor the store", IntentInstruction},
		"a question that goes on":        {"what does this mean? use the smaller change", IntentUncertain},
		"an interrogative with no mark":  {"what does this mean", IntentUncertain},
		"a contraction with no mark":     {"what's the smaller change", IntentUncertain},
		"an auxiliary with no mark":      {"is this done", IntentUncertain},
		"a polite instruction":           {"can you prefer the smaller change", IntentUncertain},
		"a statement opening like one":   {"what I want is the smaller change", IntentUncertain},
		"nothing":                        {"   ", IntentUncertain},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := ReadIntent(tc.said); got != tc.want {
				t.Fatalf("ReadIntent(%q) = %q, want %q", tc.said, got, tc.want)
			}
		})
	}
}

// A directive already recorded from a question is found by the same rule, and
// the listing says so on the entry — while it still applies, and only for the
// operational kind. An ambiguous directive is the operator's own question,
// stated as one, and is holding work until it is answered.
func TestAQuestionInTheRecordIsMarkedWhileItStands(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	question := Directive{
		SchemaVersion: SchemaVersion,
		ID:            "directive-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		Kind:          KindOperational,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    at,
		Text:          "What does 'in force from now' mean?",
		Scope:         []string{"yoyodyne-ifd.68.4"},
	}
	if !question.ReadsAsQuestion() {
		t.Fatalf("%q does not read as a question", question.Text)
	}
	const mark = "reads as a question rather than an instruction"
	if rendered := question.Render(); !strings.Contains(rendered, mark) {
		t.Fatalf("rendered = %q, want the entry marked as a question that directs nothing", rendered)
	}

	instruction := question
	instruction.Text = "prefer the smaller change here"
	if instruction.ReadsAsQuestion() {
		t.Fatalf("%q reads as a question", instruction.Text)
	}
	if rendered := instruction.Render(); strings.Contains(rendered, mark) {
		t.Fatalf("rendered = %q, want an instruction left unmarked", rendered)
	}

	stated := question
	stated.Kind = KindAmbiguous
	stated.Unresolved = "which of the two did you mean?"
	if stated.ReadsAsQuestion() {
		t.Fatalf("an ambiguous directive reads as a misrecorded question; it is the operator's own question, stated as one")
	}

	withdrawn, err := question.Withdraw("the operator", "", "recorded in error: it was a question", at.Add(time.Hour))
	if err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if rendered := withdrawn.Render(); strings.Contains(rendered, mark) {
		t.Fatalf("rendered = %q, want a withdrawn question left unmarked: it is over", rendered)
	}
}
