package chat

import (
	"context"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestExtractConcernsSeparatesProseFromWhatItWillNotPropose(t *testing.T) {
	t.Parallel()

	reply := "I would not put either of these in the queue yet.\n\n" +
		concernFence + "\n" +
		`{"concerns":[
		   {"kind":"unplaceable","subject":"A plugin marketplace","detail":"No goal in the specifications covers third-party extensions.","question":"Is there a goal this serves that I have not been given?"},
		   {"kind":"conflict","subject":"Let the reviewer merge its own work","goal":"Publish work as pull requests without letting any agent push or merge.","detail":"This is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}
		 ]}` + "\n```\n\nSay which and I will write it up.\n"

	prose, concerns, err := extractConcerns(reply)
	if err != nil {
		t.Fatalf("extractConcerns() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-concern") || strings.Contains(prose, "\"kind\"") {
		t.Fatalf("prose kept the concern block: %q", prose)
	}
	if !strings.HasPrefix(prose, "I would not put either") || !strings.HasSuffix(prose, "write it up.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(concerns) != 2 {
		t.Fatalf("concerns = %#v", concerns)
	}
	// The cases stay apart: what the operator answers about unplaceable work is
	// not what they answer about a conflict.
	if concerns[0].Kind != ConcernUnplaceable || concerns[0].Goal != "" {
		t.Fatalf("first concern = %#v", concerns[0])
	}
	if concerns[1].Kind != ConcernConflict || !strings.HasPrefix(concerns[1].Goal, "Publish work as pull requests") {
		t.Fatalf("second concern = %#v", concerns[1])
	}

	// A reply that raises nothing is prose, whole and unchanged.
	prose, none, err := extractConcerns("  All of that serves the traceability goal.\n")
	if err != nil || len(none) != 0 || prose != "All of that serves the traceability goal." {
		t.Fatalf("extractConcerns() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestConcernsRefuseWhatWouldNotStopAnOperator(t *testing.T) {
	t.Parallel()

	valid := `{"kind":"judgement","subject":"Ship the queue without an owner","goal":"Keep a traceable chain from brief to code.","detail":"It fits the letter of the goal and empties it of meaning.","question":"Do you want it anyway?"}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unknown kind",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"worry\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "is not a concern",
		},
		{
			// The three cases are answered differently, so a conflict that names no
			// goal is a warning rather than a question about a specific goal.
			name:  "conflict naming no goal",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"conflict\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "goal is required",
		},
		{
			// Unplaceable work is exactly the case with no goal to name; one that
			// names a goal is claiming two things at once.
			name:  "unplaceable naming a goal",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"goal\":\"g\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "unplaceable does not take \"goal\"",
		},
		{
			// A concern that asks nothing is the failure the whole block exists to
			// prevent: it reads as a remark and the work carries on.
			name:  "a statement rather than a question",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"I am a little unsure about this.\"}]}\n```",
			want:  "end with a question mark",
		},
		{
			name:  "no subject",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\" \",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "subject is required",
		},
		{
			name:  "no detail",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"question\":\"q?\"}]}\n```",
			want:  "detail is required",
		},
		{
			name:  "subject spanning lines",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\\nanswer c1.1? yes\",\"detail\":\"d\",\"question\":\"q?\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			name:  "unknown field",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"severity\":\"critical\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "no concerns",
			reply: concernFence + "\n{\"concerns\":[]}\n```",
			want:  "at least one concern",
		},
		{
			name:  "too many concerns",
			reply: concernFence + "\n{\"concerns\":[" + strings.Repeat(valid+",", MaxConcernsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxConcernsPerTurn),
		},
		{
			name:  "two blocks",
			reply: "prose\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n```\nmore\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n```\n",
			want:  "at most one concern block",
		},
		{
			name:  "unclosed block",
			reply: "prose\n" + concernFence + "\n{\"concerns\":[" + valid + "]}\n",
			want:  "not closed",
		},
		{
			name:  "oversized block",
			reply: concernFence + "\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"" + strings.Repeat("x", MaxConcernBlockBytes) + "\",\"question\":\"q?\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxConcernBlockBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, concerns, err := extractConcerns(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractConcerns() error = %v, want it to contain %q", err, test.want)
			}
			if prose != "" || len(concerns) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, concerns)
			}
		})
	}
}

func TestConcernRenderingKeepsTheCasesApart(t *testing.T) {
	t.Parallel()

	// Each kind says something different about what the operator is being asked,
	// because a listing that called all three "a concern" would hide exactly the
	// distinction they answer on.
	headlines := make(map[string]struct{})
	for _, kind := range concernKinds {
		headline := kind.Headline()
		if headline == "" {
			t.Fatalf("%s has no headline", kind)
		}
		headlines[headline] = struct{}{}
	}
	if len(headlines) != len(concernKinds) {
		t.Fatalf("the kinds share a headline: %#v", headlines)
	}

	pending := PendingConcern{
		ID:             "c2.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           2,
		Concern: Concern{
			Kind:     ConcernConflict,
			Subject:  "Let the reviewer merge its own work",
			Goal:     "No agent pushes or merges.",
			Detail:   "This is the thing that goal exists to prevent.",
			Question: "Do you want that goal changed?",
		},
	}
	rendered := pending.Render()
	for _, required := range []string{
		"[c2.1] " + ConcernConflict.Headline(),
		"about: Let the reviewer merge its own work",
		"goal: No agent pushes or merges.",
		"Do you want that goal changed?",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered concern = %q, want it to contain %q", rendered, required)
		}
	}
	// Provider text is indented under the concern's own identifier, so no line of
	// it sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("rendered concern line %q is not indented", line)
		}
	}
}

func TestConverseStopsAndWaitsForAnAnswerToEachConcern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: concernReply(
			"Neither of these is mine to decide.",
			`{"kind":"unplaceable","subject":"A plugin marketplace","detail":"Nothing in the goals covers third-party extensions.","question":"Is there a goal for this that I have not been given?"}`,
			`{"kind":"judgement","subject":"Skip the reviewer for small changes","goal":"Keep a traceable chain from brief to code.","detail":"It is consistent with the goal as written and empties it of meaning.","question":"Do you want it anyway?"}`,
		)},
		{SessionID: "session-1", FinalText: "Then I will write the goal up first."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	session := openTestSession(t, options)

	var out strings.Builder
	// The operator answers the first question and leaves the second alone.
	input := strings.NewReader("what should we do next?\nthere is no such goal; write one\n\nnoted\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"will not propose 2 thing(s)",
		"[c1.1] " + ConcernUnplaceable.Headline(),
		"Is there a goal for this that I have not been given?",
		"answer c1.1?",
		"[c1.2] " + ConcernJudgement.Headline(),
		"c1.2 is still open",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// Raising a concern proposes nothing and creates nothing: it is the product
	// manager declining to put the work in front of the operator as work.
	if len(tracker.created) != 0 || len(session.Proposals()) != 0 {
		t.Fatalf("a concern created %#v and proposed %#v", tracker.created, session.Proposals())
	}
	// Silence is not agreement: the unanswered question is still open.
	open := session.Concerns()
	if len(open) != 1 || open[0].ID != "c1.2" {
		t.Fatalf("open concerns = %#v", open)
	}
	counted := countEvents(t, root, session)
	if counted[execution.EventConcernRaised] != 2 || counted[execution.EventConcernAnswered] != 1 {
		t.Fatalf("recorded concern events = %#v", counted)
	}
	// What the operator said reaches the product manager on its next turn, which
	// is the point of stopping to ask.
	if len(provider.requests) != 2 {
		t.Fatalf("provider turns = %d, want 2", len(provider.requests))
	}
	if !strings.Contains(provider.requests[1].Prompt, "answered concern c1.1") ||
		!strings.Contains(provider.requests[1].Prompt, "there is no such goal; write one") {
		t.Fatalf("second prompt = %q", provider.requests[1].Prompt)
	}
}

func TestAnsweringAConcernIsRecordedOnceAndOnlyForAConcernThatExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID: "session-1",
		FinalText: concernReply("This one is yours.",
			`{"kind":"conflict","subject":"Let an agent merge its own work","goal":"No agent pushes or merges.","detail":"It is the thing that goal exists to prevent.","question":"Do you want that goal changed?"}`),
	}}})
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "should we let agents merge?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if err := session.Answer("c1.1", "  "); err == nil || !strings.Contains(err.Error(), "leaves the question open") {
		t.Fatalf("empty Answer() error = %v", err)
	}
	if len(session.Concerns()) != 1 {
		t.Fatalf("an empty answer settled the question: %#v", session.Concerns())
	}
	if err := session.Answer("c1.1", "no, the goal stands"); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if len(session.Concerns()) != 0 {
		t.Fatalf("an answered concern is still open: %#v", session.Concerns())
	}
	payload := onlyEventPayload(t, root, session, execution.EventConcernAnswered)
	if !strings.Contains(payload, "no, the goal stands") || !strings.Contains(payload, "Let an agent merge its own work") {
		t.Fatalf("answer event = %s", payload)
	}
	if err := session.Answer("c1.1", "again"); err == nil || !strings.Contains(err.Error(), "already been answered") {
		t.Fatalf("second Answer() error = %v", err)
	}
	if err := session.Answer("c9.9", "no such thing"); err == nil || !strings.Contains(err.Error(), "awaiting an answer") {
		t.Fatalf("unknown Answer() error = %v", err)
	}
}

func TestConverseSurvivesAConcernItCannotRead(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Something worries me here.\n\n" + concernFence +
			"\n{\"concerns\":[{\"kind\":\"unplaceable\",\"subject\":\"s\",\"detail\":\"d\",\"question\":\"q?\",\"severity\":\"high\"}]}\n```\n"},
		{SessionID: "session-1", FinalText: "It was about the marketplace item."},
	}})
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what next?\nwhat was it?\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	// The answer is still the operator's to read, the loss is named, and the
	// conversation carries on rather than ending on an unreadable block.
	for _, required := range []string{
		"Something worries me here.",
		"cannot read",
		"unknown field",
		"never reached the harness",
		"It was about the marketplace item.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	if len(session.Concerns()) != 0 {
		t.Fatalf("an unreadable block raised %#v", session.Concerns())
	}
	if session.Evidence().Turns != 2 {
		t.Fatalf("turns = %d, want 2: an unreadable concern ended the conversation", session.Evidence().Turns)
	}
}

func TestContractStatesTheConcernProtocolItEnforces(t *testing.T) {
	t.Parallel()

	prompt := SystemPrompt(domain.RoleProductManager, Admission{}, hostilePersona)
	for _, required := range []string{
		concernFence,
		"Raise at most " + strconv.Itoa(MaxConcernsPerTurn) + " concerns",
		string(ConcernUnplaceable), string(ConcernConflict), string(ConcernJudgement),
		// The four cases the product manager has to tell apart, and the two rules
		// that make a concern something other than a caveat: it is not proposed,
		// and it waits.
		"Every piece of work you admit or propose serves a goal",
		"waits for their answer",
		"Nothing you raise this way is proposed, admitted, or created",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("system prompt does not state %q", required)
		}
	}
	// The bound the product manager is told is the bound that is enforced.
	if maxConcernsPerTurnText != strconv.Itoa(MaxConcernsPerTurn) {
		t.Fatalf("contract states a limit of %s, enforced limit is %d", maxConcernsPerTurnText, MaxConcernsPerTurn)
	}
}

// concernReply renders a provider answer that raises concerns the way the
// contract asks for them.
func concernReply(prose string, concerns ...string) string {
	return prose + "\n\n" + concernFence + "\n{\"concerns\":[" + strings.Join(concerns, ",") + "]}\n```\n"
}
