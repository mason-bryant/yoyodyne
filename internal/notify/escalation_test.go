package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// An escalation is the governed shape rather than a second one invented for a
// chat client: the blocked outcome, why it is the operator's, the options, and a
// recommendation — with whose move follows and where the record is, which every
// message here owes its reader.
func TestAnEscalationCarriesTheBlockedOutcomeAndTheDecision(t *testing.T) {
	raised := Escalation{
		Stopped:        "intake is held, so nothing new is being chosen",
		Why:            "intake is released by a person and by nobody else",
		Since:          moment.Add(-10 * time.Hour),
		Record:         "`yoyo status`",
		Options:        []string{"release intake", "keep it held", "something else"},
		Recommendation: "(b) until you have read what it was held for",
		Topic:          Product(),
	}

	alarm := FromEscalation(raised, moment)
	if alarm.Topic.Kind != TopicProduct || !alarm.Speaker.IsHarness() {
		t.Fatalf("alarm addressed to %q by %q, want the whole line and the harness", alarm.Topic.Key(), alarm.Speaker.Key())
	}
	// A stopped system is not a routine fact and is not the thing critical is kept
	// for either, which is work already lost rather than work not happening yet.
	if alarm.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a warning", alarm.Event.Severity)
	}
	said, err := Render(alarm.Topic, alarm.Speaker, alarm.Event)
	if err != nil {
		t.Fatalf("render the alarm: %v", err)
	}
	for _, owed := range []string{raised.Stopped, raised.Why, raised.Record, nextMoveLead + nextMoves[KindEscalationRaised]} {
		if !strings.Contains(said.Body, owed) {
			t.Fatalf("alarm %q does not carry %q", said.Body, owed)
		}
	}

	asked := EscalationOptions(raised, moment)
	ask, err := Render(asked.Topic, asked.Speaker, asked.Event)
	if err != nil {
		t.Fatalf("render the ask: %v", err)
	}
	for _, owed := range []string{"(a) release intake", "(b) keep it held", "(c) something else", raised.Recommendation, "10 hours"} {
		if !strings.Contains(ask.Body, owed) {
			t.Fatalf("ask %q does not carry %q", ask.Body, owed)
		}
	}
}

// The letters are how a decision is named, so the two halves of the lettering
// have to agree: what a message offered under a letter is what a reply naming
// that letter chose.
func TestALetterNamesExactlyTheOptionItWasOfferedUnder(t *testing.T) {
	options := []string{"release intake", "keep it held", "something else"}
	for index, wanted := range options {
		letter := OptionLetter(index)
		if letter == "" {
			t.Fatalf("option %d is offered under no letter", index)
		}
		if got, named := OptionAt(options, letter); !named || got != wanted {
			t.Fatalf("(%s) names %q (found %t), want %q", letter, got, named, wanted)
		}
	}
	// A letter nothing was offered under names nothing, rather than the nearest
	// thing: a decision recorded against an option that was never on the table is
	// worse than no decision at all.
	if _, named := OptionAt(options, "d"); named {
		t.Fatal("(d) named an option, want a letter past the offer to name nothing")
	}
	if letter := OptionLetter(MaxOptions); letter != "" {
		t.Fatalf("option past the bound is offered as %q, want nothing", letter)
	}
}
