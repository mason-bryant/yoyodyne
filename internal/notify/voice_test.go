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

// testProduct is the product these speakers speak for, and testAppearance is the
// appearance of a project that configured nothing but is still running a named
// product — which is every project, because the id is required configuration.
const testProduct domain.ProductID = "yoyodyne"

var testAppearance = Appearance{Product: testProduct}

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
			Behind:          12,
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

// A thread's silence must never leave a reader guessing who holds the ball, and
// which message turns out to be a thread's last is not knowable when it is
// written. So the guarantee is on every message from every persona rather than
// on the ones somebody predicted would be final.
func TestEveryMessageSaysWhoseMoveFollowsIt(t *testing.T) {
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
			move, ok := nextMoves[kind]
			if !ok {
				t.Fatalf("%s says nothing about whose move follows it", kind)
			}
			if !strings.HasSuffix(message.Body, nextMoveLead+move) {
				t.Fatalf("the %s says %s as %q, which does not end on whose move follows", speaker.Key(), kind, message.Body)
			}
			// The clause is the harness's note about where the thread stands rather
			// than part of what the persona said, and most lines finish on words
			// somebody typed rather than on a full stop.
			account := strings.TrimSuffix(message.Body, nextMoveLead+move)
			if !strings.HasSuffix(account, ".") && !strings.HasSuffix(account, "!") && !strings.HasSuffix(account, "?") {
				t.Fatalf("the %s says %s as %q, which runs the clause into the account", speaker.Key(), kind, message.Body)
			}
		}
	}
}

// Work marked for a conversation is not queued for a run and never will be, so
// the queue's answer to what comes next would be telling the reader to expect
// something that cannot arrive — which is the same guessing, dressed up as an
// answer.
func TestWorkAConversationCarriesIsNeverSaidToBeWaitingForARun(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.138")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	for _, kind := range []Kind{KindItemAdmitted, KindItemDecomposed, KindItemAttributed, KindItemReprioritized} {
		queued := fullyRecorded(kind)
		message, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render ordinary %s: %v", kind, err)
		}
		if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[kind]) {
			t.Fatalf("ordinary %s reads as %q, want the queue's answer", kind, message.Body)
		}
		queued.Detail.Executor = string(domain.WorkItemExecutorConversation)
		handed, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render conversation-carried %s: %v", kind, err)
		}
		if !strings.HasSuffix(handed.Body, nextMoveLead+nextMoves[KindWorkHandedOff]) {
			t.Fatalf("conversation-carried %s reads as %q, want the handoff's answer", kind, handed.Body)
		}
		// An admission that says whose conversation carries the item answers with
		// that role: the item is in the queue and nothing will pull it, so the wait
		// starts here rather than at a later handoff.
		queued.Detail.Executor = string(domain.ConversationWith(domain.RoleArchitect))
		attributed, err := Render(topic, Persona(domain.RoleProductManager, ""), queued)
		if err != nil {
			t.Fatalf("render %s carried by a named role: %v", kind, err)
		}
		if !strings.HasSuffix(attributed.Body, nextMoveLead+"the architect's, in conversation — no run will ever be started for this.") {
			t.Fatalf("%s carried by the architect reads as %q, want the wait left with them", kind, attributed.Body)
		}
	}
}

// A receipt explains a term; it does not restate it. The acknowledgment of a
// directive that stopped nothing used to say it was in force from now and then
// say, in the clause after it, that it was in force from now — so an operator
// who asked what the phrase meant read it twice more and learned nothing. The
// two clauses are one receipt, and a phrase carried by both of them is an
// explanation that explains nothing.
func TestNoReceiptExplainsAPhraseByRestatingIt(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.23")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	// A directive that left nothing unsettled: the case the operator was reading.
	standing := fullyRecorded(KindDirectiveRecorded)
	standing.Detail.Unresolved = ""
	standing.Text = "the operator's own words"
	for _, speaker := range speakers() {
		message, err := Render(topic, speaker, standing)
		if err != nil {
			t.Fatalf("the %s says a standing directive: %v", speaker.Key(), err)
		}
		account, move, found := strings.Cut(message.Body, nextMoveLead)
		if !found {
			t.Fatalf("the %s says %q, which carries no clause to compare against", speaker.Key(), message.Body)
		}
		if shared, repeated := sharedPhrase(account, move); repeated {
			t.Fatalf("the %s says %q, where the clause restates %q rather than adding to it",
				speaker.Key(), message.Body, shared)
		}
	}
}

// sharedPhrase reports the first run of three or more words carried by both
// halves of one message, which is short enough to catch a repeated term and long
// enough that the ordinary connective words two sentences share do not trip it.
func sharedPhrase(account, move string) (string, bool) {
	const phrase = 3
	carried := map[string]bool{}
	for _, run := range runsOf(move, phrase) {
		carried[run] = true
	}
	for _, run := range runsOf(account, phrase) {
		if carried[run] {
			return run, true
		}
	}
	return "", false
}

func runsOf(said string, length int) []string {
	words := strings.Fields(strings.ToLower(said))
	var runs []string
	for index := 0; index+length <= len(words); index++ {
		runs = append(runs, strings.Join(words[index:index+length], " "))
	}
	return runs
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

// Which harness is talking. An operator develops more than one product, and a
// role name on its own says which chair spoke and not which product's — so every
// name carries the product, the harness's included, in the same place and the
// same shape whichever speaker it is.
func TestEverySpeakerIsNamedWithTheProductItSpeaksFor(t *testing.T) {
	for _, want := range []struct {
		speaker Speaker
		name    string
	}{
		{speaker: Harness(), name: "Yoyodyne (context-conductor)"},
		{speaker: Persona(domain.RoleDevelopmentManager, ""), name: "Development Manager (context-conductor)"},
		{speaker: Persona(domain.RoleProductManager, ""), name: "Product Manager (context-conductor)"},
		{speaker: Persona(domain.RoleArchitect, ""), name: "Architect (context-conductor)"},
		{speaker: Persona(domain.RoleDeveloper, ""), name: "Developer (context-conductor)"},
		{speaker: Persona(domain.RoleReviewer, ""), name: "Reviewer (context-conductor)"},
		// A project that configured more than one agent for a role still says the
		// product last, so the last thing on every name is the same fact.
		{speaker: Persona(domain.RoleDeveloper, "opus"), name: "Developer (opus) (context-conductor)"},
	} {
		appearance := Appearance{Product: "context-conductor"}
		if got := appearance.Identity(want.speaker); got.Name != want.name {
			t.Errorf("the %s appears as %q, want %q", want.speaker.Key(), got.Name, want.name)
		}
	}
	// A speaker the voice table has nothing for has no name to qualify, and stays
	// the empty identity a caller has to notice rather than becoming a product's
	// name for nobody.
	if got := (Appearance{Product: "context-conductor"}).Identity(Speaker{Role: "auditor"}); got != (Identity{}) {
		t.Errorf("an unvoiced speaker appears as %+v, want nothing", got)
	}
}

// The point of carrying it: two harnesses reporting into one channel are two
// sets of names rather than one, so nothing said by either is ambiguous about
// which product it is about.
func TestTwoProductsShareNoSpeakerName(t *testing.T) {
	here := Appearance{Product: testProduct}
	elsewhere := Appearance{Product: "context-conductor"}
	for _, speaker := range speakers() {
		mine, theirs := here.Identity(speaker), elsewhere.Identity(speaker)
		if mine.Name == theirs.Name {
			t.Errorf("the %s appears as %q on both products", speaker.Key(), mine.Name)
		}
		if err := mine.Validate(); err != nil {
			t.Errorf("the %s appears as %+v: %v", speaker.Key(), mine, err)
		}
	}
}

// The product qualifies the name and moves nothing else. Whose account a message
// is is a claim about who did the work, and it is the same claim whichever
// product the work was done on.
func TestNamingTheProductMovesNothingAboutAttribution(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.13")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	posted := &recorder{}
	speaker := Persona(domain.RoleDevelopmentManager, "")
	if err := New(posted, testAppearance).Notify(context.Background(), topic, speaker, fullyRecorded(KindItemAdmitted)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(posted.posted) != 1 {
		t.Fatalf("posted %d messages, want one", len(posted.posted))
	}
	message := posted.posted[0]
	if message.Speaker != speaker.Key() {
		t.Errorf("message attributed to %q, want the %s", message.Speaker, speaker.Key())
	}
	if want := "Development Manager (yoyodyne)"; message.Identity.Name != want {
		t.Errorf("message posted as %q, want %q", message.Identity.Name, want)
	}
	// The words are the persona's own, and naming the product beside the name
	// leaves them exactly as the voice table renders them.
	rendered, err := Render(topic, speaker, fullyRecorded(KindItemAdmitted))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if message.Body != rendered.Body {
		t.Errorf("message body = %q, want the rendered %q", message.Body, rendered.Body)
	}
}

// A configuration that names no product is the appearance there was before there
// were two products to tell apart, rather than a name with an empty parenthesis
// in it or a speaker called nothing at all.
func TestAnUnnamedProductLeavesTheShippedNames(t *testing.T) {
	for name, appearance := range map[string]Appearance{
		"no product":      {},
		"a blank product": {Product: "   "},
	} {
		for _, speaker := range speakers() {
			if got := appearance.Identity(speaker); got != speaker.Identity() {
				t.Errorf("with %s the %s appears as %+v, want the shipped %+v", name, speaker.Key(), got, speaker.Identity())
			}
		}
		// And a speaker the voice table has nothing for stays the empty identity a
		// caller has to notice, rather than becoming a name for nobody.
		if got := appearance.Identity(Speaker{Role: "auditor"}); got != (Identity{}) {
			t.Errorf("with %s an unvoiced speaker appears as %+v, want nothing", name, got)
		}
	}
}

// A project may choose the picture beside a name, in either shape a surface
// takes one. A speaker it configured nothing for keeps what the voice table
// ships, so naming one persona's avatar does not quietly un-decorate the rest.
func TestAConfiguredAvatarReplacesTheShippedOne(t *testing.T) {
	appearance := Appearance{Product: testProduct, Avatars: Avatars{
		HarnessSpeaker:               "https://example.invalid/faces/harness.png",
		string(domain.RoleDeveloper): ":ship-it:",
	}}
	for _, want := range []struct {
		speaker Speaker
		avatar  string
	}{
		{speaker: Harness(), avatar: "https://example.invalid/faces/harness.png"},
		{speaker: Persona(domain.RoleDeveloper, ""), avatar: ":ship-it:"},
		{speaker: Persona(domain.RoleReviewer, ""), avatar: Persona(domain.RoleReviewer, "").Identity().Avatar},
	} {
		if got := appearance.Identity(want.speaker); got.Avatar != want.avatar {
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
		shipped := testAppearance.Identity(speaker)
		if got := (Appearance{Product: testProduct, Avatars: configured}).Identity(speaker); got != shipped {
			t.Errorf("with %s the developer appears as %+v, want the shipped %+v", name, got, shipped)
		}
	}
}

// The boundary the override stops at. An avatar is decoration; the name a
// message appears under, and whose account it is, are the voice table's, so
// there is nothing a project can configure that moves either.
func TestConfiguringAnAvatarMovesNothingAboutWhoSpeaks(t *testing.T) {
	appearance := Appearance{Product: testProduct, Avatars: Avatars{string(domain.RoleDeveloper): ":ship-it:"}}
	for _, speaker := range speakers() {
		shipped := testAppearance.Identity(speaker)
		configured := appearance.Identity(speaker)
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
	if err := New(posted, appearance).Notify(context.Background(), topic, speaker, fullyRecorded(KindChecksPassed)); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(posted.posted) != 1 {
		t.Fatalf("posted %d messages, want one", len(posted.posted))
	}
	message := posted.posted[0]
	if message.Speaker != speaker.Key() || message.Identity.Name != testAppearance.Identity(speaker).Name {
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
	// A reader given a truncated account can go to the record for the rest; a
	// reader given no idea who holds the ball has nothing to go to, so the cut
	// takes the account rather than the clause.
	if !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindReportFiled]) {
		t.Fatalf("a cut body lost whose move follows it: %q", message.Body[len(message.Body)-80:])
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
