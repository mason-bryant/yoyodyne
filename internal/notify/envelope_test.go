package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

func TestTopicKeysRoundTrip(t *testing.T) {
	item, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	exchange, err := Exchange("exchange-7f3a")
	if err != nil {
		t.Fatalf("address an exchange: %v", err)
	}
	for _, topic := range []Topic{item, exchange, Product()} {
		parsed, err := ParseTopic(topic.Key())
		if err != nil {
			t.Fatalf("parse %q: %v", topic.Key(), err)
		}
		if parsed != topic {
			t.Fatalf("parse %q = %+v, want %+v", topic.Key(), parsed, topic)
		}
	}
	if got := item.Key(); got != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("work item key = %q", got)
	}
	if got := Product().Key(); got != "product" {
		t.Fatalf("product key = %q", got)
	}
}

func TestTopicRefusesAKeyThatCouldNotNameAThread(t *testing.T) {
	for name, topic := range map[string]Topic{
		"no kind":                  {ID: "yoyodyne-ifd.68"},
		"unknown kind":             {Kind: "channel", ID: "general"},
		"work item with no id":     {Kind: TopicWorkItem},
		"exchange with no id":      {Kind: TopicExchange, ID: "   "},
		"identifier with a colon":  {Kind: TopicWorkItem, ID: "work-item:nested"},
		"identifier with a space":  {Kind: TopicWorkItem, ID: "two words"},
		"product naming something": {Kind: TopicProduct, ID: "yoyodyne"},
		"oversized identifier":     {Kind: TopicWorkItem, ID: strings.Repeat("x", MaxTopicIDBytes+1)},
	} {
		if err := topic.Validate(); err == nil {
			t.Fatalf("%s: validate accepted %+v", name, topic)
		}
	}
	if _, err := ParseTopic("work-item"); err == nil {
		t.Fatal("parse accepted a key with no separator")
	}
}

func TestSpeakerNamesTheHarnessAndTheRoles(t *testing.T) {
	if !Harness().IsHarness() || Harness().Key() != HarnessSpeaker {
		t.Fatalf("the harness speaker is %+v", Harness())
	}
	for _, role := range domain.Roles() {
		speaker := Persona(role, "opus")
		if speaker.IsHarness() {
			t.Fatalf("%s reads as the harness", role)
		}
		if got := speaker.Key(); got != string(role) {
			t.Fatalf("%s key = %q", role, got)
		}
		if err := speaker.Validate(); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
	}
	if err := (Speaker{Role: "chief-architect"}).Validate(); err == nil {
		t.Fatal("validate accepted a role nothing has a voice for")
	}
	if err := (Speaker{Agent: "opus"}).Validate(); err == nil {
		t.Fatal("validate accepted the harness as a configured agent")
	}
}

func TestEveryReportableKindIsInTheSetAndNothingElseIs(t *testing.T) {
	seen := make(map[Kind]bool, len(Kinds()))
	for _, kind := range Kinds() {
		if seen[kind] {
			t.Fatalf("%s is listed twice", kind)
		}
		seen[kind] = true
		if !kind.Valid() {
			t.Fatalf("%s is listed but not valid", kind)
		}
	}
	for _, kind := range []Kind{"", "run.exploded", "checks"} {
		if kind.Valid() {
			t.Fatalf("%q reads as reportable", kind)
		}
	}
}

func TestEventRefusesWhatNothingCouldBeSaidAbout(t *testing.T) {
	moment := time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC)
	valid := Event{Kind: KindChecksPassed, At: moment, Severity: report.SeverityNote}
	if err := valid.Validate(); err != nil {
		t.Fatalf("validate a complete event: %v", err)
	}
	for name, event := range map[string]Event{
		"no kind":      {At: moment, Severity: report.SeverityNote},
		"unknown kind": {Kind: "run.exploded", At: moment, Severity: report.SeverityNote},
		"no severity":  {Kind: KindChecksPassed, At: moment},
		"no moment":    {Kind: KindChecksPassed, Severity: report.SeverityNote},
	} {
		if err := event.Validate(); err == nil {
			t.Fatalf("%s: validate accepted %+v", name, event)
		}
	}
}

func TestAnEventWithNoDetailIsStillAnEvent(t *testing.T) {
	// Absence is a fact about the record rather than an error in it: a run with
	// no recorded selection still has to be reported as started.
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	message, err := Render(topic, Harness(), Event{
		Kind:     KindRunStarted,
		At:       time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC),
		Severity: report.SeverityNote,
	})
	if err != nil {
		t.Fatalf("render an event with nothing recorded: %v", err)
	}
	for _, stated := range []string{"an unrecorded run", "nobody the record names", "no reason recorded"} {
		if !strings.Contains(message.Body, stated) {
			t.Fatalf("body %q does not state the absence %q", message.Body, stated)
		}
	}
	if strings.Contains(message.Body, "{") {
		t.Fatalf("body %q left a placeholder unfilled", message.Body)
	}
}

func TestMessageRefusesWhatCouldNotBePosted(t *testing.T) {
	moment := time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC)
	complete := Message{
		SchemaVersion: SchemaVersion,
		Kind:          KindChecksPassed,
		Topic:         "work-item:yoyodyne-ifd.68.2",
		Speaker:       HarnessSpeaker,
		Identity:      Harness().Identity(),
		Severity:      report.SeverityNote,
		Body:          "Checks passed.",
		At:            moment,
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("validate a complete message: %v", err)
	}
	broken := map[string]func(*Message){
		"wrong schema version": func(m *Message) { m.SchemaVersion = 2 },
		"unknown kind":         func(m *Message) { m.Kind = "run.exploded" },
		"unaddressed":          func(m *Message) { m.Topic = "" },
		"no speaker":           func(m *Message) { m.Speaker = "" },
		"no identity":          func(m *Message) { m.Identity = Identity{} },
		"no severity":          func(m *Message) { m.Severity = "" },
		"no body":              func(m *Message) { m.Body = "  " },
		"no moment":            func(m *Message) { m.At = time.Time{} },
	}
	for name, break_ := range broken {
		message := complete
		break_(&message)
		if err := message.Validate(); err == nil {
			t.Fatalf("%s: validate accepted %+v", name, message)
		}
	}
}

func TestRefsNameTheRecordThatHoldsTheWhole(t *testing.T) {
	for name, refs := range map[string]Refs{
		"run-abc":            {RunID: "run-abc", WorkItemID: "yoyodyne-ifd.68"},
		"chat-def":           {ConversationID: "chat-def"},
		"exchange-1":         {ExchangeID: "exchange-1"},
		"yoyodyne-ifd.68":    {WorkItemID: "yoyodyne-ifd.68"},
		"the durable record": {},
	} {
		if got := refs.Record(); got != name {
			t.Fatalf("record of %+v = %q, want %q", refs, got, name)
		}
	}
}
