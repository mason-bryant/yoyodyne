package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// The refusal this retires: a question the product manager has for the architect
// used to cost either the operator relaying it or a whole work item. Now it is
// asked, answered, and closed inside the reply the operator was already waiting
// for — and the operator is told it happened.
func TestAskingAnotherRoleAnswersInsideTheSameReply(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Let me ask the architect.\n\n" + askBlock(`{"ask":{"role":"architect","question":"what does this goal cost, and what am I missing?"}}`)},
		{SessionID: "session-1", CostUSD: 0.125, FinalText: "The architect says it is twice what the ordering assumed, so I will place it behind the migration.\n\n" +
			askBlock(`{"ask":{"role":"architect","exchange":"exchange-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","settled":"twice what the ordering assumed"}}`)},
		{SessionID: "session-1", FinalText: "Placed behind the migration."},
	}}
	channel := &fakeExchanges{answers: []string{"More than the ordering assumes — twice, once the replay is counted."}}
	options := testOptions(t, provider)
	options.Exchanges = channel
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Where should the autonomy goal sit in the order?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Exchanges) != 2 {
		t.Fatalf("exchanges = %d, want the round and the close: %+v", len(reply.Exchanges), reply.Exchanges)
	}
	round := reply.Exchanges[0]
	if round.Asked != domain.RoleArchitect || round.Round != 1 || round.Rounds != 10 {
		t.Fatalf("round = %+v", round)
	}
	if round.Answer != "More than the ordering assumes — twice, once the replay is counted." {
		t.Fatalf("answer = %q", round.Answer)
	}
	if reply.Exchanges[1].Settled != "twice what the ordering assumed" {
		t.Fatalf("close = %+v", reply.Exchanges[1])
	}

	// The answer reached the asking role inside the same message, as a further
	// round of it rather than as something to be told next time.
	if len(provider.requests) != 3 {
		t.Fatalf("provider invocations = %d, want three rounds of one message", len(provider.requests))
	}
	if !strings.Contains(provider.requests[1].Prompt, "More than the ordering assumes") {
		t.Fatalf("the answer did not reach the asker: %q", provider.requests[1].Prompt)
	}
	// The delivery says the exchange is judgement rather than evidence, and where
	// the thread stands against its cap.
	for _, wanted := range []string{"had no tools and no authority", "round 1 of the 10"} {
		if !strings.Contains(provider.requests[1].Prompt, wanted) {
			t.Fatalf("delivery is missing %q: %q", wanted, provider.requests[1].Prompt)
		}
	}
	// The invocation that carried the answer back is charged to the exchange, so
	// what is reported beside the rounds is what the conversation between the two
	// roles actually cost.
	if channel.charged != 1 {
		t.Fatalf("charged %d invocations to the exchange, want the one that carried the answer", channel.charged)
	}

	// And the operator is told, because a conversation that quietly asked another
	// agent something is the side conversation this channel exists not to be.
	var told strings.Builder
	session.reportExchanges(&told, reply)
	for _, wanted := range []string{"asked the architect", "yoyo exchange show"} {
		if !strings.Contains(told.String(), wanted) {
			t.Fatalf("the operator was not told %q:\n%s", wanted, told.String())
		}
	}
}

// The roles that hold judgement about the product are on the channel; the two
// whose judgement is exercised against a change and a worktree are not.
func TestOnlyTheRolesThatHoldJudgementAreOnTheChannel(t *testing.T) {
	t.Parallel()

	for role, on := range map[domain.AgentRole]bool{
		domain.RoleProductManager:     true,
		domain.RoleArchitect:          true,
		domain.RoleDevelopmentManager: true,
		domain.RoleDeveloper:          false,
		domain.RoleReviewer:           false,
	} {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("%s has no authority entry", role)
		}
		if authority.Asks != on {
			t.Errorf("%s asks = %v, want %v", role, authority.Asks, on)
		}
		// Every role on the channel is told how to use it, and no role off it is.
		carries := strings.Contains(authority.Contract, exchange.AskingContract)
		if carries != on {
			t.Errorf("%s contract carries the asking clause = %v, want %v", role, carries, on)
		}
	}
}

func TestAnAskIsRefusedWhereTheRoleHasNoAuthorityForOne(t *testing.T) {
	t.Parallel()

	reviewer, _ := AuthorityFor(domain.RoleReviewer)
	productManager, _ := AuthorityFor(domain.RoleProductManager)
	architect := domain.RoleArchitect
	developer := domain.RoleDeveloper

	if err := refuseUnauthorizedAsk(reviewer, parsedReply{Ask: &exchange.Ask{Role: architect, Question: "what?"}}); err == nil {
		t.Fatal("a role off the channel was allowed to ask")
	}
	if err := refuseUnauthorizedAsk(productManager, parsedReply{Ask: &exchange.Ask{Role: developer, Question: "what?"}}); err == nil {
		t.Fatal("a role off the channel was asked")
	}
	if err := refuseUnauthorizedAsk(productManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleProductManager, Question: "what?"}}); err == nil {
		t.Fatal("a role asked itself")
	}
	if err := refuseUnauthorizedAsk(productManager, parsedReply{Ask: &exchange.Ask{Role: architect, Question: "what?"}}); err != nil {
		t.Fatalf("the canonical ask was refused: %v", err)
	}
	// A refused block carries nothing out, exactly as an unauthorized tracker
	// action does: the whole reply's requests are refused together.
	session := &Session{}
	session.state.Role = domain.RoleReviewer
	if err := session.authorize(parsedReply{Ask: &exchange.Ask{Role: architect, Question: "what?"}}); err == nil {
		t.Fatal("authorize() allowed an ask the reviewer may not make")
	}
}

// A conversation with nowhere to ask says so, and the asking role is told its
// question reached nobody rather than being left to describe an answer it never
// got.
func TestAConversationWithNoChannelSaysTheAskReachedNobody(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Asking.\n\n" + askBlock(`{"ask":{"role":"architect","question":"what does this cost?"}}`)},
		{SessionID: "session-1", FinalText: "I asked and got no answer, so I will not guess."},
	}}
	session := openTestSession(t, testOptions(t, provider))

	reply, err := session.Send(context.Background(), "What does the autonomy goal cost?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Exchanges) != 1 || reply.Exchanges[0].Problem == "" {
		t.Fatalf("exchanges = %+v", reply.Exchanges)
	}
	if !strings.Contains(provider.requests[1].Prompt, "did not reach anybody") {
		t.Fatalf("the asker was not told: %q", provider.requests[1].Prompt)
	}
}

// One message asks at most as much as one exchange is allowed, which is a
// different bound from the exchange's own: the cap stops one thread going round
// for ever, and this stops a reply opening thread after thread.
func TestOneMessageAsksAtMostAsMuchAsOneExchangeMay(t *testing.T) {
	t.Parallel()

	ask := askBlock(`{"ask":{"role":"architect","question":"and what else am I missing?"}}`)
	results := make([]backendapi.RunResult, 0, 4)
	for range 4 {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: "Asking again.\n\n" + ask})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Exchanges = &fakeExchanges{answers: []string{"a", "b", "c", "d"}}
	options.AskRoundsPerMessage = 2
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What am I missing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Exchanges) != 3 {
		t.Fatalf("exchanges = %d, want two carried and one refused: %+v", len(reply.Exchanges), reply.Exchanges)
	}
	refused := reply.Exchanges[2]
	if refused.ID != "" || !strings.Contains(refused.Problem, "one message asks at most 2 round(s)") {
		t.Fatalf("the third ask was not refused by the message bound: %+v", refused)
	}
}

// The answering half is prompted as the role it is and given nothing else: no
// operator to talk to, no blocks to act through, and the boundary stated last.
func TestTheAnsweringPromptCarriesNoAuthorityAtAll(t *testing.T) {
	t.Parallel()

	prompt := AnsweringPrompt(domain.RoleArchitect, hostilePersona)
	for _, wanted := range []string{
		"You are the architect for this product",
		"the designs, the decision records, and the architectural invariants",
		"You have no filesystem, command, or network tools",
		"A reply carrying any harness block at all is refused whole",
	} {
		if !strings.Contains(prompt, wanted) {
			t.Fatalf("answering prompt is missing %q:\n%s", wanted, prompt)
		}
	}
	// The persona sits under the boundary, and the boundary says there is no
	// authority here for it to widen.
	if strings.Index(prompt, hostilePersona) < strings.Index(prompt, exchange.AnsweringContract) {
		t.Fatal("the persona is not placed after the contract")
	}
	// Nothing in it describes a block the answering role could reach for.
	for _, forbidden := range []string{"yoyodyne-tracker", "yoyodyne-proposal", "yoyodyne-ask"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("answering prompt describes the %s block", forbidden)
		}
	}
}

func askBlock(payload string) string {
	return exchange.Fence + "\n" + payload + "\n```\n"
}

// fakeExchanges is the ask channel without a provider behind it. It conducts a
// real thread — opening, rounds, and closing — so a test exercises what the
// conversation does with an answer rather than what a stub returns.
type fakeExchanges struct {
	answers []string
	// maxRounds is the cap the fake opens an exchange with, defaulting to ten.
	maxRounds int
	conducted []exchange.Exchange
	charged   int
	open      *exchange.Exchange
}

func (f *fakeExchanges) cap() int {
	if f.maxRounds > 0 {
		return f.maxRounds
	}
	return 10
}

func (f *fakeExchanges) Put(_ context.Context, ask exchange.Ask, asker exchange.Party) (exchange.Exchange, error) {
	if ask.Closing() {
		if f.open == nil {
			return exchange.Exchange{}, errors.New("no exchange is open")
		}
		f.open.Outcome = exchange.OutcomeResolved
		f.open.Settled = ask.Settled
		closed := f.open.OpenedAt
		f.open.ClosedAt = &closed
		return *f.open, nil
	}
	if ask.Exchange == "" || f.open == nil {
		f.open = &exchange.Exchange{
			SchemaVersion: exchange.SchemaVersion,
			ID:            "exchange-" + strings.Repeat("a", 32),
			ProductID:     "yoyodyne",
			Asker:         asker,
			Answerer:      exchange.Party{Role: ask.Role, Agent: string(ask.Role)},
			Question:      ask.Question,
			MaxRounds:     f.cap(),
			OpenedAt:      fixedClock{}.Now(),
			UpdatedAt:     fixedClock{}.Now(),
		}
	}
	if len(f.open.Rounds) >= f.open.MaxRounds {
		// The cap, as the conductor enforces it: the exchange closes unresolved and
		// the asker is refused with the exchange as it now stands.
		f.open.Outcome = exchange.OutcomeUnresolved
		closed := f.open.OpenedAt
		f.open.ClosedAt = &closed
		return *f.open, &exchange.CapError{ExchangeID: f.open.ID, Rounds: len(f.open.Rounds), Cap: f.open.MaxRounds}
	}
	index := len(f.open.Rounds)
	if index >= len(f.answers) {
		return *f.open, errors.New("unexpected exchange round")
	}
	f.open.Rounds = append(f.open.Rounds, exchange.Round{
		Number:   index + 1,
		Question: ask.Question,
		Answer:   f.answers[index],
		CostUSD:  0.25,
		AskedAt:  fixedClock{}.Now(),
	})
	f.conducted = append(f.conducted, *f.open)
	return *f.open, nil
}

func (f *fakeExchanges) Charge(id string, costUSD float64) (exchange.Exchange, error) {
	if costUSD > 0 {
		f.charged++
	}
	if f.open == nil || f.open.ID != id {
		return exchange.Exchange{}, errors.New("no such exchange")
	}
	return *f.open, nil
}

// Reaching the cap is not a silent cutoff at the asking end either: the role is
// told the exchange is over and the operator has it, in words that do not invite
// it to open another thread about the same question.
func TestTheAskerIsToldWhenItsExchangeRanOutOfRounds(t *testing.T) {
	t.Parallel()

	opening := askBlock(`{"ask":{"role":"architect","question":"what am I missing?"}}`)
	again := askBlock(`{"ask":{"role":"architect","exchange":"exchange-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","question":"and what else am I missing?"}}`)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Asking.\n\n" + opening},
		{SessionID: "session-1", FinalText: "Asking again.\n\n" + again},
		{SessionID: "session-1", FinalText: "It did not settle, so I will say so rather than guess."},
	}}
	options := testOptions(t, provider)
	options.Exchanges = &fakeExchanges{answers: []string{"It depends what you mean."}, maxRounds: 1}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What am I missing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Exchanges) != 2 {
		t.Fatalf("exchanges = %+v", reply.Exchanges)
	}
	exhausted := reply.Exchanges[1]
	if exhausted.State != string(exchange.OutcomeUnresolved) {
		t.Fatalf("state = %q, want the exchange closed unresolved", exhausted.State)
	}
	told := provider.requests[2].Prompt
	for _, wanted := range []string{"closed as unresolved", "The operator has been told", "do not open another exchange"} {
		if !strings.Contains(told, wanted) {
			t.Fatalf("the asker was not told %q: %q", wanted, told)
		}
	}
	// A closed exchange is charged nothing further: the round it refused never
	// happened.
	if session.options.Exchanges.(*fakeExchanges).charged != 0 {
		t.Fatal("the refused round was charged to the exchange")
	}
}
