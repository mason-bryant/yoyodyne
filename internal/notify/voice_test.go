package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

var moment = time.Date(2026, 8, 19, 15, 4, 5, 0, time.UTC)

// speakers is every speaker there is, which is what "events from every persona
// type" has to mean for a test to be able to check it.
func speakers() []Speaker {
	all := []Speaker{Harness()}
	for _, role := range domain.Roles() {
		all = append(all, Persona(role, ""))
	}
	return all
}

// fullyRecorded is an event with every field a voice line could reach for, so a
// rendering failure is a missing line rather than a missing fact.
func fullyRecorded(kind Kind) Event {
	return Event{
		Kind:     kind,
		At:       moment,
		Severity: report.SeverityNote,
		Refs: Refs{
			RunID:       "run-4d1f",
			WorkItemID:  "yoyodyne-ifd.68.2",
			ExchangeID:  "exchange-7f3a",
			PullRequest: "https://example.test/pull/84",
		},
		Detail: Detail{
			SelectedBy:      "development manager",
			SelectionReason: "highest-priority item nothing is holding back",
			Command:         "go test ./...",
			ExitCode:        1,
			Findings:        3,
			TargetBranch:    "main",
			Commit:          "0123456789abcdef0123456789abcdef01234567",
			PullRequest:     "#84 (https://example.test/pull/84)",
			Cause:           "an exhausted provider usage limit",
			Round:           2,
			Rounds:          5,
			Unresolved:      "which branch the change belongs on",
			Artifact:        "slack-reporting-design",
			Title:           "Conversation milestones reach Slack",
			Goal:            "Work the harness runs on its own is visible while it runs",
			Parent:          "yoyodyne-ifd.68",
			Priority:        1,
			Reason:          "reordering the backlog first",
			Since:           moment.Add(-3 * time.Hour),
			Ready:           4,
			Accumulated:     37,
		},
		Text: "the developer's own words, carried through",
	}
}

func TestEveryPersonaSaysEveryReportableKind(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, speaker := range speakers() {
		for _, kind := range Kinds() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if strings.ContainsAny(message.Body, "{}") {
				t.Fatalf("the %s says %s as %q, which left a placeholder", speaker.Key(), kind, message.Body)
			}
			if message.Speaker != speaker.Key() || message.Kind != kind {
				t.Fatalf("message is %+v, want speaker %q and kind %q", message, speaker.Key(), kind)
			}
			if message.Topic != topic.Key() {
				t.Fatalf("the %s says %s to %q, want %q", speaker.Key(), kind, message.Topic, topic.Key())
			}
		}
	}
}

func TestNoTwoPersonasSayTheSameEventTheSameWay(t *testing.T) {
	// This is the whole point of a voice: a reader who has scrolled past the
	// display name still knows who is talking.
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, kind := range Kinds() {
		said := make(map[string]string, len(speakers()))
		for _, speaker := range speakers() {
			message, err := Render(topic, speaker, fullyRecorded(kind))
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if other, seen := said[message.Body]; seen {
				t.Fatalf("the %s and the %s both say %s as %q", other, speaker.Key(), kind, message.Body)
			}
			said[message.Body] = speaker.Key()
		}
	}
}

func TestEveryPersonaAppearsAsItself(t *testing.T) {
	identities := make(map[string]string, len(speakers()))
	avatars := make(map[string]string, len(speakers()))
	for _, speaker := range speakers() {
		identity := speaker.Identity()
		if err := identity.Validate(); err != nil {
			t.Fatalf("the %s appears as %+v: %v", speaker.Key(), identity, err)
		}
		if other, seen := identities[identity.Name]; seen {
			t.Fatalf("the %s and the %s both appear as %q", other, speaker.Key(), identity.Name)
		}
		identities[identity.Name] = speaker.Key()
		if other, seen := avatars[identity.Avatar]; seen {
			t.Fatalf("the %s and the %s share the avatar %q", other, speaker.Key(), identity.Avatar)
		}
		avatars[identity.Avatar] = speaker.Key()
	}
}

func TestTheConfiguredAgentIsNamedOnlyWhenItSaysSomethingTheRoleDoesNot(t *testing.T) {
	plain := Persona(domain.RoleDeveloper, "").Identity()
	if plain.Name != "Developer" {
		t.Fatalf("an unnamed agent appears as %q", plain.Name)
	}
	same := Persona(domain.RoleDeveloper, "developer").Identity()
	if same.Name != "Developer" {
		t.Fatalf("an agent named for its role appears as %q", same.Name)
	}
	named := Persona(domain.RoleDeveloper, "opus").Identity()
	if named.Name != "Developer (opus)" {
		t.Fatalf("a configured agent appears as %q", named.Name)
	}
}

// A project may choose the picture beside a name, in either shape a surface
// takes one. A speaker it configured nothing for keeps what the voice table
// ships, so naming one persona's avatar does not quietly un-decorate the rest.
func TestAConfiguredAvatarReplacesTheShippedOne(t *testing.T) {
	avatars := Avatars{
		HarnessSpeaker:               "https://example.invalid/faces/harness.png",
		string(domain.RoleDeveloper): ":ship-it:",
	}
	for _, want := range []struct {
		speaker Speaker
		avatar  string
	}{
		{speaker: Harness(), avatar: "https://example.invalid/faces/harness.png"},
		{speaker: Persona(domain.RoleDeveloper, ""), avatar: ":ship-it:"},
		{speaker: Persona(domain.RoleReviewer, ""), avatar: Persona(domain.RoleReviewer, "").Identity().Avatar},
	} {
		if got := avatars.Identity(want.speaker); got.Avatar != want.avatar {
			t.Errorf("the %s appears with %q, want %q", want.speaker.Key(), got.Avatar, want.avatar)
		}
	}

	// A project with nothing configured is the shipped table exactly, and a blank
	// entry is the same as no entry rather than a speaker with no picture at all.
	for name, configured := range map[string]Avatars{
		"nothing configured": nil,
		"a blank entry":      {string(domain.RoleDeveloper): "   "},
	} {
		speaker := Persona(domain.RoleDeveloper, "")
		if got := configured.Identity(speaker); got != speaker.Identity() {
			t.Errorf("with %s the developer appears as %+v, want the shipped %+v", name, got, speaker.Identity())
		}
	}
}

// The boundary the override stops at. An avatar is decoration; the name a
// message appears under, and whose account it is, are the voice table's, so
// there is nothing a project can configure that moves either.
func TestConfiguringAnAvatarMovesNothingAboutWhoSpeaks(t *testing.T) {
	avatars := Avatars{string(domain.RoleDeveloper): ":ship-it:"}
	for _, speaker := range speakers() {
		shipped := speaker.Identity()
		configured := avatars.Identity(speaker)
		if configured.Name != shipped.Name {
			t.Errorf("the %s appears as %q with an avatar configured, want %q", speaker.Key(), configured.Name, shipped.Name)
		}
	}
	// And the message still says whose account it is, whatever the picture is.
	topic, err := WorkItem("yoyodyne-ifd.68.6")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	posted := &recorder{}
	speaker := Persona(domain.RoleDeveloper, "")
	if err := New(posted, avatars).Notify(context.Background(), topic, speaker, fullyRecorded(KindChecksPassed)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(posted.posted) != 1 {
		t.Fatalf("posted %d messages, want one", len(posted.posted))
	}
	message := posted.posted[0]
	if message.Speaker != speaker.Key() || message.Identity.Name != speaker.Identity().Name {
		t.Fatalf("message = %+v, want it still attributed to the developer", message)
	}
	if message.Identity.Avatar != ":ship-it:" {
		t.Fatalf("message avatar = %q, want the configured one", message.Identity.Avatar)
	}
}

func TestWhatAnAgentWroteIsCarriedThroughUnchanged(t *testing.T) {
	// The deterministic half must never paraphrase the genuine half: this is the
	// text an agent already wrote, and a message that summarized it would be the
	// harness speaking in the agent's name.
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	written := "The staleness check reads the tracker twice; the second read can disagree with the first."
	for _, speaker := range speakers() {
		for _, kind := range []Kind{KindReportFiled, KindProposalRaised, KindExchangeTurn, KindBlockerRecorded} {
			event := fullyRecorded(kind)
			event.Text = written
			message, err := Render(topic, speaker, event)
			if err != nil {
				t.Fatalf("the %s says %s: %v", speaker.Key(), kind, err)
			}
			if !strings.Contains(message.Body, written) {
				t.Fatalf("the %s says %s as %q, which does not carry what was written", speaker.Key(), kind, message.Body)
			}
		}
	}
}

func TestSeverityIsSaidInWordsBeforeItIsSaidInDecoration(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	bodies := make(map[report.Severity]string, 3)
	for _, severity := range []report.Severity{report.SeverityCritical, report.SeverityWarning, report.SeverityNote} {
		event := fullyRecorded(KindReportFiled)
		event.Severity = severity
		message, err := Render(topic, Persona(domain.RoleDeveloper, ""), event)
		if err != nil {
			t.Fatalf("render a %s report: %v", severity, err)
		}
		bodies[severity] = message.Body
	}
	// Strip every emoji shortcode and the distinctions must all survive, because
	// the words carry the meaning and the decoration only adds to it.
	stripped := func(body string) string {
		for _, shortcode := range []string{":rotating_light: ", ":warning: "} {
			body = strings.ReplaceAll(body, shortcode, "")
		}
		return body
	}
	if !strings.HasPrefix(stripped(bodies[report.SeverityCritical]), "Critical — ") {
		t.Fatalf("a critical report reads as %q with its decoration stripped", stripped(bodies[report.SeverityCritical]))
	}
	if !strings.HasPrefix(stripped(bodies[report.SeverityWarning]), "Warning — ") {
		t.Fatalf("a warning reads as %q with its decoration stripped", stripped(bodies[report.SeverityWarning]))
	}
	if strings.Contains(bodies[report.SeverityNote], "Critical") || strings.Contains(bodies[report.SeverityNote], "Warning") {
		t.Fatalf("a note reads as %q", bodies[report.SeverityNote])
	}
	if stripped(bodies[report.SeverityCritical]) == stripped(bodies[report.SeverityNote]) {
		t.Fatal("a critical report and a note are indistinguishable without decoration")
	}
}

func TestACountIsSaidInWordsRatherThanAsABareNumber(t *testing.T) {
	for count, want := range map[int]string{-1: "findings the record does not count", 0: "no findings", 1: "one finding", 4: "4 findings"} {
		if got := countOf(count, "finding", "findings", "findings the record does not count"); got != want {
			t.Fatalf("countOf(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestAnExchangeSaysWhereItIsAndHowItEnded(t *testing.T) {
	if got := roundsOf(Detail{Round: 2, Rounds: 5}); got != "round 2 of 5" {
		t.Fatalf("rounds = %q", got)
	}
	if got := roundsOf(Detail{Round: 2}); got != "round 2" {
		t.Fatalf("rounds with no cap = %q", got)
	}
	if got := roundsOf(Detail{}); got != "an unrecorded round" {
		t.Fatalf("unrecorded rounds = %q", got)
	}
	if got := outcomeOf(Detail{}); got != "resolved" {
		t.Fatalf("a settled exchange closed %q", got)
	}
	unresolved := outcomeOf(Detail{Unresolved: "which branch the change belongs on"})
	if !strings.HasPrefix(unresolved, "unresolved at its round cap") || !strings.HasSuffix(unresolved, "which branch the change belongs on") {
		t.Fatalf("an exchange out of rounds closed %q", unresolved)
	}
}

func TestABodyTooLongIsCutWithTheRecordThatHoldsTheWhole(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	event := fullyRecorded(KindReportFiled)
	event.Text = strings.Repeat("a very long observation. ", 400)
	message, err := Render(topic, Persona(domain.RoleDeveloper, ""), event)
	if err != nil {
		t.Fatalf("render an oversized report: %v", err)
	}
	if len(message.Body) > MaxBodyBytes {
		t.Fatalf("body is %d bytes, limit is %d", len(message.Body), MaxBodyBytes)
	}
	if !strings.Contains(message.Body, "run-4d1f") || !strings.Contains(message.Body, "cut") {
		t.Fatalf("a cut body does not name the record that holds the whole: %q", message.Body[len(message.Body)-80:])
	}
}

// The envelope carries what the topic is called beside the key it is addressed
// by, so whatever posts the message can head a thread in words a reader knows
// without ever asking the tracker what an identifier means.
func TestTheEnvelopeCarriesWhatTheTopicIsCalled(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.5")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	message, err := Render(topic.WithTitle("Slack run-started messages speak as the selector"),
		Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render a titled topic: %v", err)
	}
	if message.Topic != topic.Key() {
		t.Fatalf("addressed to %q, want the key alone: %q", message.Topic, topic.Key())
	}
	if message.TopicTitle != "Slack run-started messages speak as the selector" {
		t.Fatalf("topic title = %q, want what the record calls the item", message.TopicTitle)
	}
	// A topic built by hand rather than through WithTitle is bounded here, so
	// what the envelope carries is a header line whichever way it was assembled.
	overlong, err := Render(Topic{Kind: TopicWorkItem, ID: "yoyodyne-ifd.118", Title: strings.Repeat("long ", 200)},
		Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render an oversized title: %v", err)
	}
	if len(overlong.TopicTitle) > MaxTopicTitleBytes {
		t.Fatalf("topic title is %d bytes, limit is %d", len(overlong.TopicTitle), MaxTopicTitleBytes)
	}
	// A record with no title says nothing about one rather than heading a thread
	// with a blank.
	plain, err := Render(topic, Harness(), fullyRecorded(KindRunStarted))
	if err != nil {
		t.Fatalf("render an untitled topic: %v", err)
	}
	if plain.TopicTitle != "" {
		t.Fatalf("topic title = %q, want nothing where the record carried nothing", plain.TopicTitle)
	}
}

func TestRenderRefusesWhatItHasNoWordsFor(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.2")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	if _, err := Render(topic, Speaker{Role: "chief-architect"}, fullyRecorded(KindChecksPassed)); err == nil {
		t.Fatal("rendered a speaker nothing has a voice for")
	}
	if _, err := Render(topic, Harness(), Event{Kind: "run.exploded", At: moment, Severity: report.SeverityNote}); err == nil {
		t.Fatal("rendered a kind nobody wrote words for")
	}
	if _, err := Render(Topic{Kind: TopicWorkItem}, Harness(), fullyRecorded(KindChecksPassed)); err == nil {
		t.Fatal("rendered a message addressed to nothing")
	}
}

func TestSubstitutionRefusesALineNamingSomethingNoRecordHolds(t *testing.T) {
	fields := map[string]string{"item": "yoyodyne-ifd.68.2"}
	if got, err := substitute("on {item}", fields); err != nil || got != "on yoyodyne-ifd.68.2" {
		t.Fatalf("substitute = %q, %v", got, err)
	}
	if _, err := substitute("on {nothing}", fields); err == nil {
		t.Fatal("substituted a field no record holds")
	}
	if _, err := substitute("on {item", fields); err == nil {
		t.Fatal("substituted an unclosed placeholder")
	}
}
