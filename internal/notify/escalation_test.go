package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// stoppedLine is a state that has stopped the whole system, in the shape the
// governed escalation format asks for: the blocked outcome, why it is the
// operator's, the options in nontechnical terms, and a recommendation.
func stoppedLine() Escalation {
	return Escalation{
		Stopped:        "intake is held, so nothing new is being chosen however much is ready",
		Why:            "intake is released by a person, and no role may release it.",
		Since:          moment.Add(-3 * time.Hour),
		Record:         "`yoyo status`, which says what is ready behind the hold",
		Recommendation: "(a), on the evidence there is",
		Options: []Option{
			{Text: "release intake and let the line choose from the backlog again"},
			{Text: "keep it held until what stopped it is dealt with"},
			{Text: "something else, or you want more of the record in front of you first"},
		},
		Topic: Product(),
	}
}

// The alarm is what interrupts somebody, so it carries the three things they can
// act on without opening anything: what stopped, why it is theirs, and where the
// whole of it is. It is a warning rather than a critical — a run that stopped and
// stayed stopped is news about work already lost, and this is news about work not
// happening yet.
func TestTheAlarmSaysWhatStoppedWhyItIsYoursAndWhereTheRecordIs(t *testing.T) {
	raised := FromEscalation(stoppedLine(), moment)
	if raised.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a stopped system to weigh more than a note and less than work already lost", raised.Event.Severity)
	}
	message, err := Render(raised.Topic, raised.Speaker, raised.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, fact := range []string{"intake is held", "released by a person", "yoyo status"} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("the alarm reads %q, which does not carry %q", message.Body, fact)
		}
	}
	// The harness says it: the state was derived from the record rather than
	// judged by anybody, and a persona made to say it would be claiming a
	// judgment it never made.
	if message.Speaker != HarnessSpeaker {
		t.Fatalf("speaker = %q, want the harness rather than a persona", message.Speaker)
	}
}

// The ask is what they read when they have a moment, and every option is
// lettered because what gets recorded is which letter they named.
func TestTheAskLettersEveryOptionAndCarriesARecommendation(t *testing.T) {
	message, err := renderAsk(t, stoppedLine())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, want := range []string{"(a)", "(b)", "(c)", "release intake", "3 hours", "(a), on the evidence there is"} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("the ask reads %q, which does not carry %q", message.Body, want)
		}
	}
}

// The letters are what a decision is recorded against, so the half that lays
// them out and the half that reads one back have to agree. A surface that
// lettered them for itself could record a decision the operator did not make.
func TestALetterNamesTheOptionItWasOfferedUnder(t *testing.T) {
	options := stoppedLine().Options
	for index, want := range options {
		letter := OptionLetter(index)
		if letter == "" {
			t.Fatalf("option %d was offered under no letter", index)
		}
		chosen, named := OptionAt(options, letter)
		if !named || chosen.Text != want.Text {
			t.Fatalf("(%s) names %#v, want %q", letter, chosen, want.Text)
		}
		// A reply is typed by a person, so the case they typed it in is not a
		// different answer.
		if upper, named := OptionAt(options, strings.ToUpper(letter)); !named || upper.Text != want.Text {
			t.Fatalf("(%s) upper-cased names %#v, want the same option", letter, upper)
		}
	}
	if _, named := OptionAt(options, "z"); named {
		t.Fatal("a letter nothing was offered under named an option")
	}
}

// An option the record left empty is said as an absence in its own place rather
// than dropped, because dropping it would shift every letter after it — and a
// shifted letter is an answer recorded against the wrong decision.
func TestAnEmptyOptionKeepsItsLetterRatherThanShiftingTheRest(t *testing.T) {
	raised := stoppedLine()
	raised.Options[1] = Option{}
	message, err := renderAsk(t, raised)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(message.Body, "(c) something else") {
		t.Fatalf("the ask reads %q, want the third option still offered under (c)", message.Body)
	}
	chosen, named := OptionAt(raised.Options, "c")
	if !named || !strings.HasPrefix(chosen.Text, "something else") {
		t.Fatalf("(c) names %#v, want the option that was printed under it", chosen)
	}
}

// An ask with nothing to choose between says so rather than rendering an empty
// list, for the reason every other absence here is stated: a sentence with a
// hole in it reads as a bug.
func TestAnAskWithNoOptionsSaysSo(t *testing.T) {
	raised := stoppedLine()
	raised.Options = nil
	message, err := renderAsk(t, raised)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.ContainsAny(message.Body, "{}") {
		t.Fatalf("the ask reads %q, which left a placeholder", message.Body)
	}
	if !strings.Contains(message.Body, "nothing the record offers to choose between") {
		t.Fatalf("the ask reads %q, want the absence stated", message.Body)
	}
}

func renderAsk(t *testing.T, raised Escalation) (Message, error) {
	t.Helper()
	ask := EscalationOptions(raised, moment)
	return Render(ask.Topic, ask.Speaker, ask.Event)
}
