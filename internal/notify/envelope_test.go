package notify

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// An envelope that cannot be posted must be refused where it is made rather
// than discovered by a sink halfway through a pass, because a sink that fails
// on one message is a sink that stops reporting the rest.
func TestAnUnpostableEnvelopeIsRefused(t *testing.T) {
	t.Parallel()

	valid := New(KindRunStarted, WorkItemTopic("yoyodyne-ifd.68.3"), Harness, report.SeverityNote, "it started", Refs{})
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a complete envelope to be accepted", err)
	}

	for name, envelope := range map[string]Envelope{
		"no version":  {Kind: KindRunStarted, Topic: ProductTopic, Severity: report.SeverityNote, Body: "said"},
		"no kind":     New("", ProductTopic, Harness, report.SeverityNote, "said", Refs{}),
		"no topic":    New(KindRunStarted, "", Harness, report.SeverityNote, "said", Refs{}),
		"no body":     New(KindRunStarted, ProductTopic, Harness, report.SeverityNote, "   ", Refs{}),
		"no severity": New(KindRunStarted, ProductTopic, Harness, "", "said", Refs{}),
		// The bound exists so nothing can push a document into a channel. The
		// sink truncates what it posts; an envelope this size is a producer
		// bug rather than a long message.
		"oversized body": New(KindReport, ProductTopic, Harness, report.SeverityNote, strings.Repeat("a", MaxBodyBytes+1), Refs{}),
	} {
		if err := envelope.Validate(); err == nil {
			t.Errorf("Validate() with %s = nil, want a refusal", name)
		}
	}
}

// One thread per topic is the addressing rule, so what is threaded and what is
// not has to be a property of the topic rather than a decision each producer
// makes for itself.
func TestOnlyTheProductTopicIsUnthreaded(t *testing.T) {
	t.Parallel()

	if !WorkItemTopic("yoyodyne-ifd.68").Threaded() || !ExchangeTopic("ask-1").Threaded() {
		t.Fatal("a work item and an exchange each get a thread of their own")
	}
	if ProductTopic.Threaded() {
		t.Fatal("what is about the whole line must not be buried in one item's thread")
	}
	if label := WorkItemTopic("yoyodyne-ifd.68").Label(); label != "yoyodyne-ifd.68" {
		t.Fatalf("Label() = %q, want the item as a person reads it", label)
	}
	if label := ExchangeTopic("ask-1").Label(); label != "exchange ask-1" {
		t.Fatalf("Label() = %q, want the exchange named as one", label)
	}
}

// A project may configure more than one agent for a role, so "which developer
// said this" has to be answerable from the message itself.
func TestASpeakerIsNamedByItsAgentAndFallsBackToItsRole(t *testing.T) {
	t.Parallel()

	if name := (Speaker{Role: domain.RoleDeveloper, Agent: "developer-2"}).Name(); name != "developer-2" {
		t.Fatalf("Name() = %q, want the configured agent", name)
	}
	if name := (Speaker{Role: domain.RoleReviewer}).Name(); name != string(domain.RoleReviewer) {
		t.Fatalf("Name() = %q, want the role where no agent is named", name)
	}
	if name := Harness.Name(); name != "harness" {
		t.Fatalf("Name() = %q, want what no persona did to be attributed to the harness", name)
	}
	if !Harness.IsHarness() || (Speaker{Role: domain.RoleDeveloper}).IsHarness() {
		t.Fatal("the harness and a role must not be mistaken for one another")
	}
}
