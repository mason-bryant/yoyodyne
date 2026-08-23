package exchange

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A whole exchange: the product manager asks the architect what a goal costs,
// reads the answer, asks once more, and closes it. Every round is durable before
// it is taken, and what it cost is on the record beside the rounds.
func TestAnExchangeIsDurableRoundByRoundAndClosesResolved(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	voice := &scriptedVoice{answers: []string{"More than the ordering assumes.", "Roughly twice."}}
	conductor := newTestConductor(store, voice, nil, 10)
	asker := Party{Role: domain.RoleProductManager, Agent: "product-manager", Conversation: "chat-" + strings.Repeat("c", 32)}

	first, err := conductor.Put(context.Background(), Ask{
		Role:     domain.RoleArchitect,
		Question: "what does this goal cost, and what am I missing?",
		Context:  "I am about to order the backlog with it.",
	}, asker)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if first.Spent() != 1 || first.Rounds[0].Answer != "More than the ordering assumes." {
		t.Fatalf("first round = %+v", first.Rounds)
	}
	// The round was written before the provider was asked anything, so a process
	// that died in between would have spent a round rather than taken an uncounted
	// one. That is the direction a cap has to fail in to be a cap at all.
	if voice.roundsWhenAsked[0] != 1 {
		t.Fatalf("the round was recorded after the voice was invoked: %v", voice.roundsWhenAsked)
	}
	if !first.Open() {
		t.Fatalf("the exchange closed itself: %s", first.Outcome)
	}

	second, err := conductor.Put(context.Background(), Ask{
		Role:     domain.RoleArchitect,
		Exchange: first.ID,
		Question: "how much more?",
	}, asker)
	if err != nil {
		t.Fatalf("Put() second error = %v", err)
	}
	if second.Spent() != 2 {
		t.Fatalf("rounds = %d, want 2", second.Spent())
	}
	// The answering side is one session across the thread, so round two is
	// answered by something that remembers round one.
	if voice.sessions[1] != "session-1" {
		t.Fatalf("second round session = %q, want the first round's", voice.sessions[1])
	}

	// What the asking side spends carrying an answer back is the exchange's too.
	if _, err := conductor.Charge(first.ID, 0.125); err != nil {
		t.Fatalf("Charge() error = %v", err)
	}

	closed, err := conductor.Put(context.Background(), Ask{
		Role:     domain.RoleArchitect,
		Exchange: first.ID,
		Settled:  "twice the ordering assumed; I will put it behind the migration.",
	}, asker)
	if err != nil {
		t.Fatalf("Put() closing error = %v", err)
	}
	if closed.Outcome != OutcomeResolved || closed.ClosedAt == nil {
		t.Fatalf("closed = %+v", closed)
	}
	if got := closed.CostUSD(); got != 0.625 {
		t.Fatalf("cost = %v, want 0.625 (two answers at 0.25 plus the charged round)", got)
	}
	// A closed exchange is closed. There is no continuing one afterwards, which
	// is what stops a thread quietly outliving the cap it was opened with.
	if _, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleArchitect, Exchange: first.ID, Question: "one more?",
	}, asker); err == nil {
		t.Fatal("a closed exchange was continued")
	}
}

// The cap is not a silent cutoff. Reaching it closes the exchange as unresolved
// and puts it to the operator as a report, which is the whole reason the number
// is worth having: the loop it bounds is two judgement models deferring to each
// other, and nobody would ever see it otherwise.
func TestReachingTheCapClosesTheExchangeAndEscalatesIt(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	voice := &scriptedVoice{answers: []string{"It depends what you mean.", "It depends what you mean."}}
	reports := &memoryReports{}
	conductor := newTestConductor(store, voice, reports, 2)
	asker := Party{Role: domain.RoleProductManager, Agent: "product-manager"}

	recorded, err := conductor.Put(context.Background(), Ask{Role: domain.RoleArchitect, Question: "what does this cost?"}, asker)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleArchitect, Exchange: recorded.ID, Question: "and if we halved it?",
	}, asker); err != nil {
		t.Fatalf("Put() second error = %v", err)
	}

	spent, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleArchitect, Exchange: recorded.ID, Question: "and if we quartered it?",
	}, asker)
	if !errors.Is(err, ErrRoundsSpent) {
		t.Fatalf("Put() past the cap error = %v, want a rounds-spent refusal", err)
	}
	if spent.Outcome != OutcomeUnresolved || spent.Settled != "" {
		t.Fatalf("exhausted exchange = %+v", spent)
	}
	if spent.Spent() != 2 {
		t.Fatalf("rounds = %d, want the 2 it was allowed and no more", spent.Spent())
	}
	if len(voice.answered) != 2 {
		t.Fatalf("the provider was asked %d times, want the 2 rounds the cap allowed", len(voice.answered))
	}
	if len(reports.filed) != 1 {
		t.Fatalf("reports filed = %d, want one escalation", len(reports.filed))
	}
	escalation := reports.filed[0]
	if escalation.Severity != report.SeverityWarning {
		t.Fatalf("escalation severity = %q, want warning", escalation.Severity)
	}
	for _, wanted := range []string{recorded.ID, "closed unresolved after 2 round(s)", "product manager", "architect"} {
		if !strings.Contains(escalation.Message, wanted) {
			t.Fatalf("escalation is missing %q: %s", wanted, escalation.Message)
		}
	}
	if escalation.RunID != recorded.ID {
		t.Fatalf("escalation points at %q, want the exchange", escalation.RunID)
	}
}

// The cap travels with the exchange rather than being read afresh, so neither a
// configuration edit nor a second process can lengthen a thread already running
// long.
func TestTheCapAnExchangeOpenedWithSurvivesTheConfigurationChanging(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	voice := &scriptedVoice{answers: []string{"a", "b", "c"}}
	asker := Party{Role: domain.RoleProductManager, Agent: "product-manager"}

	opened, err := newTestConductor(store, voice, &memoryReports{}, 1).
		Put(context.Background(), Ask{Role: domain.RoleArchitect, Question: "what does this cost?"}, asker)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if opened.MaxRounds != 1 {
		t.Fatalf("max rounds = %d, want the 1 it was opened with", opened.MaxRounds)
	}

	// A different process, a raised limit, the same thread.
	generous := newTestConductor(store, voice, &memoryReports{}, 100)
	spent, err := generous.Put(context.Background(), Ask{
		Role: domain.RoleArchitect, Exchange: opened.ID, Question: "and again?",
	}, asker)
	if !errors.Is(err, ErrRoundsSpent) {
		t.Fatalf("the raised limit let the thread go on: err = %v", err)
	}
	if spent.MaxRounds != 1 {
		t.Fatalf("max rounds = %d, want the durable 1", spent.MaxRounds)
	}
}

// A round the answering provider failed is still a round: it happened, it was
// charged for, and the exchange says so rather than reading as a question nobody
// asked.
func TestAnUnansweredRoundIsStillSpentAndSaysWhy(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	voice := &scriptedVoice{errs: []error{errors.New("the provider went away")}}
	conductor := newTestConductor(store, voice, &memoryReports{}, 10)

	recorded, err := conductor.Put(context.Background(), Ask{Role: domain.RoleArchitect, Question: "what does this cost?"},
		Party{Role: domain.RoleProductManager, Agent: "product-manager"})
	if err == nil {
		t.Fatal("Put() reported no problem for a round nobody answered")
	}
	if recorded.Spent() != 1 {
		t.Fatalf("rounds = %d, want the one that was spent", recorded.Spent())
	}
	if !strings.Contains(recorded.Rounds[0].Problem, "the provider went away") {
		t.Fatalf("round problem = %q", recorded.Rounds[0].Problem)
	}
	if recorded.Rounds[0].Answer != "" {
		t.Fatalf("a failed round recorded an answer: %q", recorded.Rounds[0].Answer)
	}
	// The asker is told, in words that do not invite it to ask the same thing
	// again in the same breath.
	if delivery := recorded.Delivery(); !strings.Contains(delivery, "went unanswered") {
		t.Fatalf("delivery = %q", delivery)
	}
}

// Who is in an exchange is fixed when it opens. A thread a third role picked up
// would be two conversations wearing one identifier.
func TestAnExchangeCannotChangeWhoIsInIt(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	conductor := newTestConductor(store, &scriptedVoice{answers: []string{"a"}}, &memoryReports{}, 10)
	opened, err := conductor.Put(context.Background(), Ask{Role: domain.RoleArchitect, Question: "what does this cost?"},
		Party{Role: domain.RoleProductManager, Agent: "product-manager"})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleArchitect, Exchange: opened.ID, Question: "mine now?",
	}, Party{Role: domain.RoleDevelopmentManager, Agent: "development-manager"}); err == nil {
		t.Fatal("a third role continued somebody else's exchange")
	}
	if _, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleDevelopmentManager, Exchange: opened.ID, Question: "you instead?",
	}, Party{Role: domain.RoleProductManager, Agent: "product-manager"}); err == nil {
		t.Fatal("an exchange was redirected to a third role")
	}
	if _, err := conductor.Put(context.Background(), Ask{
		Role: domain.RoleProductManager, Question: "what do I think?",
	}, Party{Role: domain.RoleProductManager, Agent: "product-manager"}); err == nil {
		t.Fatal("a role asked itself")
	}
}

// An answer that reaches for authority is refused by the conductor whatever the
// voice hands back, which is what makes decisionless a property of the channel
// rather than of any provider's good behaviour.
func TestAnAnswerReachingForAuthorityIsRefusedByTheConductor(t *testing.T) {
	t.Parallel()

	store := &memoryExchanges{}
	voice := &scriptedVoice{answers: []string{"Admitting it.\n\n```yoyodyne-tracker\n{\"actions\":[{\"action\":\"survey\"}]}\n```"}}
	conductor := newTestConductor(store, voice, &memoryReports{}, 10)

	recorded, err := conductor.Put(context.Background(), Ask{Role: domain.RoleArchitect, Question: "what does this cost?"},
		Party{Role: domain.RoleProductManager, Agent: "product-manager"})
	if err == nil {
		t.Fatal("an answer carrying a tracker block was accepted")
	}
	if recorded.Rounds[0].Answer != "" {
		t.Fatalf("half the refused answer was kept: %q", recorded.Rounds[0].Answer)
	}
	if !strings.Contains(recorded.Rounds[0].Problem, "carries no authority") {
		t.Fatalf("round problem = %q", recorded.Rounds[0].Problem)
	}
}

func newTestConductor(store Store, voice Voice, reports Reports, maxRounds int) Conductor {
	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	minted := 0
	return Conductor{
		Store:        store,
		Voice:        voice,
		Reports:      reports,
		MaxRounds:    maxRounds,
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Now:          func() time.Time { at = at.Add(time.Second); return at },
		NewID: func() (string, error) {
			minted++
			return fmt.Sprintf("exchange-%032x", minted), nil
		},
	}
}

// memoryExchanges is the durable store without the disk. What it holds is a
// copy, so a test that mutates what it was handed back does not quietly rewrite
// the record.
type memoryExchanges struct {
	saved map[string]Exchange
}

func (m *memoryExchanges) Save(recorded Exchange) error {
	if err := recorded.Validate(); err != nil {
		return err
	}
	if m.saved == nil {
		m.saved = map[string]Exchange{}
	}
	clone := recorded
	clone.Rounds = append([]Round(nil), recorded.Rounds...)
	m.saved[recorded.ID] = clone
	return nil
}

func (m *memoryExchanges) Load(id string) (Exchange, error) {
	recorded, found := m.saved[id]
	if !found {
		return Exchange{}, fmt.Errorf("%w: %s", ErrNoExchange, id)
	}
	clone := recorded
	clone.Rounds = append([]Round(nil), recorded.Rounds...)
	return clone, nil
}

// scriptedVoice answers in order and records what it was asked, including how
// many rounds the record already held when it was invoked — which is what makes
// "written before it was taken" an assertion rather than a claim.
type scriptedVoice struct {
	answers         []string
	errs            []error
	answered        []Question
	sessions        []string
	roundsWhenAsked []int
}

func (s *scriptedVoice) Answer(_ context.Context, question Question) (Spoken, error) {
	index := len(s.answered)
	s.answered = append(s.answered, question)
	s.sessions = append(s.sessions, question.SessionID)
	s.roundsWhenAsked = append(s.roundsWhenAsked, question.Round)
	if index < len(s.errs) && s.errs[index] != nil {
		return Spoken{Agent: "architect"}, s.errs[index]
	}
	if index >= len(s.answers) {
		return Spoken{}, errors.New("unexpected exchange round")
	}
	return Spoken{
		Answer:    s.answers[index],
		Agent:     "architect",
		SessionID: "session-1",
		CostUSD:   0.25,
	}, nil
}

type memoryReports struct {
	filed []report.Report
}

func (m *memoryReports) Append(reported report.Report) error {
	m.filed = append(m.filed, reported)
	return nil
}
