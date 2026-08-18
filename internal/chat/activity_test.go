package chat

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// escapes finds anything a terminal would interpret rather than print, which is
// what must never reach the recorded reply or the event stream.
var escapes = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// orange is the colour a question the product manager asks the operator is
// shown in. It is pinned here rather than read from the theme, because which
// colour it is, is the part the operator asked for.
const orange = "\x1b[38;5;208m"

// TestATurnSaysWhatItIsDoingWhileTheOperatorWaits is the reported problem: a
// conversation recorded seven consecutive provider rejections over about
// thirty-six seconds and told the operator none of it, so a turn that was
// working looked exactly like a hung command. Every phase below is read off the
// same events the turn was already recording.
func TestATurnSaysWhatItIsDoingWhileTheOperatorWaits(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &streamingBackend{reply: "Two goals, then.", stream: []streamedEvent{
		{eventType: execution.EventRunStarted, payload: map[string]any{"session_id": "session-1", "model": "claude-opus-5"}},
		// What the operator hit: the provider refusing the request, over and
		// over, while the harness recorded every rejection and said nothing.
		{eventType: execution.EventProcessOutput, payload: map[string]any{"provider_type": "system", "provider_subtype": "api_retry", "attempt": 1, "max_retries": 10, "error": "unknown"}},
		{eventType: execution.EventProcessOutput, payload: map[string]any{"provider_type": "system", "provider_subtype": "api_retry", "attempt": 7, "max_retries": 10, "error": "unknown"}},
		{eventType: execution.EventAgentMessage, payload: map[string]any{"text": "Two goals, then."}},
		{eventType: execution.EventRunCompleted, payload: map[string]any{"session_id": "session-1"}},
	}})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what is missing from the brief?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"… " + phaseSending,
		"… " + phaseThinking,
		// A turn that is slow because the provider is refusing requests is
		// saying something about the operator's account or the service, so it is
		// named for what it is rather than folded into generic waiting.
		"… the provider is refusing requests; retrying (attempt 7 of 10)",
		"… " + phaseWriting,
		"Two goals, then.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// The phases are said in the order they happened, so a transcript reads as
	// an account of the turn rather than a jumble of states.
	if strings.Index(transcript, phaseThinking) > strings.Index(transcript, "attempt 7 of 10") {
		t.Fatalf("the phases are out of order: %q", transcript)
	}

	// None of it is anything but display. The reply is what the product manager
	// said, and the event stream is what it was already recording.
	events := loadTestEvents(t, root, session)
	if len(events) != 5 {
		t.Fatalf("recorded events = %d, want the five the provider emitted", len(events))
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), "refusing requests") {
			t.Fatalf("the display reached the event stream: %s", event.Payload)
		}
	}
	recorded, err := newTestStore(t, root).Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Turns != 1 || recorded.ProviderSessionID != "session-1" {
		t.Fatalf("the recorded conversation is not the turn that happened: %#v", recorded)
	}
}

// TestATurnWithNoEventsIsStillAccountedFor covers the turn that says nothing at
// all: the display still opens, so an operator who sees the phase and then
// nothing knows the message was sent and that the provider has not answered.
func TestATurnWithNoEventsIsStillAccountedFor(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &streamingBackend{reply: "Two goals, then."})
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !strings.Contains(out.String(), "… "+phaseSending) {
		t.Fatalf("a silent turn said nothing at all: %q", out.String())
	}
}

// TestOneShotMessagesAreNotDisplayedAtAll keeps the display where it belongs.
// A `--message` invocation has nobody watching a screen, and its output is
// read by whatever ran it.
func TestOneShotMessagesAreNotDisplayedAtAll(t *testing.T) {
	t.Parallel()

	session := openTestSession(t, testOptions(t, &streamingBackend{reply: "Two goals, then.", stream: []streamedEvent{
		{eventType: execution.EventProcessOutput, payload: map[string]any{"provider_type": "system", "provider_subtype": "api_retry", "attempt": 1, "max_retries": 10}},
	}}))
	reply, err := session.Send(context.Background(), "what next?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.Text != "Two goals, then." {
		t.Fatalf("reply = %q", reply.Text)
	}
}

// TestTheOperatorsTurnIsSeparatedFromTheReplyAndQuestionsStandOut covers the
// dressing on a terminal that permits it: a rule between what the operator said
// and the answer to it, questions in orange, and nothing coloured that the text
// does not already distinguish.
func TestTheOperatorsTurnIsSeparatedFromTheReplyAndQuestionsStandOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reply := "Two goals, then.\n\nWhich of them matters first?"
	options := testOptions(t, &streamingBackend{reply: reply})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)

	var out strings.Builder
	screen := dressedConsole{
		Console: testConsole(strings.NewReader("what is missing from the brief?\n/status\n/exit\n"), &out),
		theme: console.NewTheme(func(name string) string {
			if name == "TERM" {
				return "xterm-256color"
			}
			return ""
		}, func() int { return 40 }),
	}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	// One rule for each thing the operator said, drawn between their turn and
	// the answer to it: the reply to the first, and what the harness itself said
	// about the other two.
	turns := strings.Split(transcript, strings.Repeat("─", 40)+"\n")
	if len(turns) != 4 {
		t.Fatalf("a rule was not drawn for each of the three turns: %q", transcript)
	}
	if !strings.Contains(turns[1], orange+"Which of them matters first?"+"\x1b[0m") {
		t.Fatalf("the question was not shown in orange: %q", turns[1])
	}
	if strings.Contains(turns[1], orange+"Two goals, then.") {
		t.Fatalf("a line that asks nothing was coloured as a question: %q", turns[1])
	}
	// The harness's own answer to a command is dressed as its own kind of thing,
	// distinct from the conversation and from a question.
	if !strings.Contains(turns[2], "\x1b[38;5;44m") || strings.Contains(turns[2], orange) {
		t.Fatalf("command output was not distinguished from the conversation: %q", turns[2])
	}

	// Strip the colour and what is left is the conversation itself, still saying
	// which part of it is which: the reply names who said it, and the question
	// still ends in a question mark.
	stripped := escapes.ReplaceAllString(transcript, "")
	if !strings.Contains(stripped, "product-manager> "+reply) {
		t.Fatalf("stripping colour changed the reply: %q", stripped)
	}

	// None of the dressing is in the record. What was said is what was said.
	for _, event := range loadTestEvents(t, root, session) {
		if escapes.Match(event.Payload) {
			t.Fatalf("colour reached the event stream: %s", event.Payload)
		}
	}
}

// dressedConsole is a conversation held over a stream that is dressed as a
// terminal would be, which is how the colour and the rules are testable without
// a terminal to drive.
type dressedConsole struct {
	console.Console
	theme console.Theme
}

func (d dressedConsole) Theme() console.Theme { return d.theme }

// streamedEvent is one event a provider emits during a turn.
type streamedEvent struct {
	eventType execution.EventType
	payload   map[string]any
}

// streamingBackend replays a turn's event stream before it answers, which is
// what makes what the operator is told during a turn testable without a
// provider that is slow on purpose.
type streamingBackend struct {
	stream   []streamedEvent
	reply    string
	requests []backendapi.RunRequest
}

func (b *streamingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	b.requests = append(b.requests, request)
	sequence := request.LastSequence
	for _, streamed := range b.stream {
		sequence++
		event, err := execution.NewEvent(request.RunID, sequence, fixedClock{}.Now(), streamed.eventType, "provider.claude-code", streamed.payload)
		if err != nil {
			return backendapi.RunResult{}, err
		}
		if request.EventSink != nil {
			if err := request.EventSink(event); err != nil {
				return backendapi.RunResult{}, err
			}
		}
	}
	return backendapi.RunResult{SessionID: "session-1", FinalText: b.reply, LastEvent: sequence}, nil
}

// TestAnEventThePayloadCannotBeReadIsStillActivity keeps the display from
// becoming a second reader of the event stream: what it cannot describe, it
// still counts as something having arrived.
func TestAnEventThePayloadCannotBeReadIsStillActivity(t *testing.T) {
	t.Parallel()

	recorded := &recordingActivity{}
	activity := &turnActivity{display: recorded, phase: phaseSending}
	activity.observe(execution.Event{Type: execution.EventProcessOutput, Payload: json.RawMessage(`{"attempt":"seven"}`)})
	activity.observe(execution.Event{Type: execution.EventProcessOutput, Payload: json.RawMessage(`{"provider_subtype":"api_retry","attempt":3,"max_retries":10}`)})
	// A retry the provider did not number is still a retry, and is named as one.
	activity.observe(execution.Event{Type: execution.EventProcessOutput, Payload: json.RawMessage(`{"provider_subtype":"api_retry"}`)})

	want := []string{
		phaseSending,
		"the provider is refusing requests; retrying (attempt 3 of 10)",
		"the provider is refusing requests; it is being retried",
	}
	if len(recorded.phases) != len(want) {
		t.Fatalf("phases = %#v, want %#v", recorded.phases, want)
	}
	for index, phase := range want {
		if recorded.phases[index] != phase {
			t.Fatalf("phase %d = %q, want %q", index, recorded.phases[index], phase)
		}
	}

	// A conversation with no display is the one-shot case, and must not be a
	// crash.
	var absent *turnActivity
	absent.doing(phaseTracker)
	absent.observe(execution.Event{Type: execution.EventRunStarted})
}

// recordingActivity is a display that keeps what it was told, so what the
// events are turned into is assertable without a screen.
type recordingActivity struct{ phases []string }

func (r *recordingActivity) Doing(phase string) { r.phases = append(r.phases, phase) }

func (r *recordingActivity) Close() {}
