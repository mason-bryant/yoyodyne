package supervision

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestAWellFormedRequestValidates(t *testing.T) {
	t.Parallel()

	request := testRequest(1)
	request.Refers = []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// A request moves a question between two roles. One addressed to the role that
// wrote it is a role talking to itself through durable storage, which costs an
// invocation and answers nothing.
func TestARoleDoesNotAskItself(t *testing.T) {
	t.Parallel()

	request := testRequest(1)
	request.From, request.To = domain.RoleArchitect, domain.RoleArchitect
	assertRefused(t, request.Validate(), "a role does not ask itself")
}

// An escalation exists to reach the operator, and the product manager is the
// only role that carries anything to them. One addressed anywhere else would go
// round the person it was raised for.
func TestAnEscalationReachesTheOperatorThroughTheProductManager(t *testing.T) {
	t.Parallel()

	request := testRequest(1)
	request.Kind = KindEscalate
	request.From, request.To = domain.RoleDevelopmentManager, domain.RoleArchitect
	assertRefused(t, request.Validate(), "an escalation reaches the operator through the product manager")

	request.To = domain.RoleProductManager
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want an escalation to the product manager accepted", err)
	}
}

// The answer and the ending are one fact recorded in two places, so a record
// that disagrees with itself does not load. Either direction of the
// disagreement is how an answer somebody paid for gets lost.
func TestAnAnswerAndTheEndingAgree(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)

	claimsAnAnswerItDoesNotHave := testRequest(1)
	claimsAnAnswerItDoesNotHave.Outcome = OutcomeAnswered
	claimsAnAnswerItDoesNotHave.SettledAt = &moment
	assertRefused(t, claimsAnAnswerItDoesNotHave.Validate(), "records the answer")

	endedSomeOtherWay := testRequest(2)
	endedSomeOtherWay.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", moment, "")}
	endedSomeOtherWay.Response = testAnswer(1, moment)
	endedSomeOtherWay.Outcome = OutcomeWithdrawn
	endedSomeOtherWay.SettledAt = &moment
	assertRefused(t, endedSomeOtherWay.Validate(), "rather than answered")

	unresolvedWithAnAnswer := testRequest(3)
	unresolvedWithAnAnswer.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", moment, "")}
	unresolvedWithAnAnswer.Response = testAnswer(1, moment)
	unresolvedWithAnAnswer.Outcome = OutcomeUnresolved
	unresolvedWithAnAnswer.SettledAt = &moment
	assertRefused(t, unresolvedWithAnAnswer.Validate(), "no answer recorded")
}

// Only the last attempt can be the one still running. An earlier one left open
// is two processes having written over each other, and delivering against it is
// exactly the duplicate the whole scheme is here to prevent.
func TestAnAttemptCannotBeLeftOpenBehindALaterOne(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	request := testRequest(1)
	request.Attempts = []Attempt{
		testOpenAttempt(1, "harness-a", moment),
		testFinishedAttempt(2, "harness-b", moment.Add(time.Minute), "the provider refused"),
	}
	assertRefused(t, request.Validate(), "still open behind a later attempt")
}

// The cycle limit is copied onto the request when it opens, so nothing a later
// process does can let it spend more than it was allowed.
func TestAttemptsCannotOutrunTheCycleLimit(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	request := testRequest(1)
	request.CycleLimit = 2
	request.Attempts = []Attempt{
		testFinishedAttempt(1, "harness-a", moment, "the provider refused"),
		testFinishedAttempt(2, "harness-a", moment.Add(time.Minute), "the provider refused"),
		testFinishedAttempt(3, "harness-a", moment.Add(2*time.Minute), "the provider refused"),
	}
	assertRefused(t, request.Validate(), "3 attempts are recorded against a limit of 2")
}

// An attempt still open was started, so it was spent. Counting it only once it
// finished would let a crash loop retry for ever on a budget that never moves.
func TestAnOpenAttemptIsAlreadySpent(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	request := testRequest(1)
	request.CycleLimit = 2
	request.Attempts = []Attempt{
		testFinishedAttempt(1, "harness-a", moment, "the provider refused"),
		testOpenAttempt(2, "harness-b", moment.Add(time.Minute)),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if request.Spent() != 2 {
		t.Fatalf("Spent() = %d, want the open attempt counted", request.Spent())
	}
	if request.CyclesRemaining() != 0 {
		t.Fatalf("CyclesRemaining() = %d, want none", request.CyclesRemaining())
	}
	carried, running := request.InFlight()
	if !running || carried.Holder != "harness-b" {
		t.Fatalf("InFlight() = %+v, %v, want the open attempt and who is carrying it", carried, running)
	}
}

// A request at its limit has nothing remaining rather than a debt, whatever a
// record written by an older build happens to hold.
func TestCyclesRemainingIsNeverNegative(t *testing.T) {
	t.Parallel()

	request := testRequest(1)
	request.CycleLimit = 1
	request.Attempts = []Attempt{
		testFinishedAttempt(1, "harness-a", request.OpenedAt, ""),
		testFinishedAttempt(2, "harness-a", request.OpenedAt, ""),
	}
	if request.CyclesRemaining() != 0 {
		t.Fatalf("CyclesRemaining() = %d, want none", request.CyclesRemaining())
	}
}

// The answer names the attempt it came back on, so the answer and what getting
// it cost are one record.
func TestAnAnswerNamesAnAttemptThatWasMade(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)
	request := testRequest(1)
	request.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", moment, "")}
	request.Response = testAnswer(4, moment)
	request.Outcome = OutcomeAnswered
	request.SettledAt = &moment
	assertRefused(t, request.Validate(), "response names attempt 4")
}

// This record is the only durable copy of the invocation that produced the
// answer, so what served it is required rather than optional. An answer pinned
// to nothing is one nobody can attribute or reconstruct once the provider
// session behind it is gone.
func TestAnAnswerNamesWhatServedIt(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)
	answered := testRequest(1)
	answered.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", moment, "")}
	answered.Outcome = OutcomeAnswered
	answered.SettledAt = &moment

	bare := answered
	bare.Response = &Response{Text: "It costs a design revision.", At: moment, Attempt: 1}
	for _, want := range []string{
		"records no backend",
		"response model is required",
		"records no account alias",
		"records no configuration revision",
	} {
		assertRefused(t, bare.Validate(), want)
	}

	// The two that are recorded where they are known: a provider that does not
	// say which model it served, and a binary built without the stamping, each
	// leave a comparison nobody can make rather than an answer served by nothing.
	served := *testAnswer(1, moment)
	served.ResolvedModel, served.Build = "", ""
	unstamped := answered
	unstamped.Response = &served
	if err := unstamped.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the resolved model and build optional", err)
	}
}

// Shape is checked as well as presence: a record naming an account or a
// configuration nothing could have issued is worse than one naming none,
// because it reads as evidence.
func TestWhatServedTheAnswerIsCheckedForShape(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)
	request := testRequest(1)
	request.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", moment, "")}
	request.Outcome = OutcomeAnswered
	request.SettledAt = &moment

	for _, invented := range []struct {
		what string
		with func(*Response)
		want string
	}{
		{"backend", func(r *Response) { r.Backend = "Claude Code" }, "is not a backend identifier"},
		{"account", func(r *Response) { r.AccountAlias = "Work Account" }, "is not an account alias"},
		{"configuration", func(r *Response) { r.ConfigRevision = "revision-two" }, "is not a configuration revision"},
		{"build", func(r *Response) { r.Build = "not-a-revision" }, "is not a revision"},
	} {
		response := *testAnswer(1, moment)
		invented.with(&response)
		claimed := request
		claimed.Response = &response
		assertRefused(t, claimed.Validate(), invented.want)
	}
}

// A judgment or a request that names fifty documents is one that will be stale
// the moment it is written, because every reference is a revision somebody has
// to keep current.
func TestAReferenceCarriesTheRevisionItWasRead(t *testing.T) {
	t.Parallel()

	request := testRequest(1)
	request.Refers = []Reference{{What: "artifact", ID: "v1-goals"}}
	assertRefused(t, request.Validate(), "refers[0] revision is required")

	if key := (Reference{What: "artifact", ID: "v1-goals", Revision: "r7"}).Key(); key != "artifact/v1-goals" {
		t.Fatalf("Key() = %q", key)
	}
}

func TestARequestIdentifierIsCheckedBeforeItNamesAPath(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "request", "request-", "request-zz", "../escape", testRequestID(1) + "x"} {
		if ValidRequestID(id) {
			t.Fatalf("ValidRequestID(%q) = true", id)
		}
	}
	issued, err := NewRequestID()
	if err != nil {
		t.Fatalf("NewRequestID() error = %v", err)
	}
	if !ValidRequestID(issued) {
		t.Fatalf("NewRequestID() issued %q, which it will not accept back", issued)
	}
}

// Test helpers, shared with the readiness and survey tests in this package.

func testRequestID(n int) string { return fmt.Sprintf("request-%032x", n) }

func testReadinessID(n int) string { return fmt.Sprintf("readiness-%032x", n) }

// testOpenedAt spaces requests a minute apart, so the order they are taken in is
// the order they were opened and a test can say which is which.
func testOpenedAt(n int) time.Time {
	return time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
}

func testRequest(n int) Request {
	opened := testOpenedAt(n)
	return Request{
		SchemaVersion: SchemaVersion,
		ID:            testRequestID(n),
		ProductID:     "yoyodyne",
		Topic:         "chat-one",
		Kind:          KindConsult,
		From:          domain.RoleProductManager,
		To:            domain.RoleArchitect,
		Subject:       "what would this cost, and what am I missing?",
		CycleLimit:    DefaultCycleLimit,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
}

func testOpenAttempt(number int, holder string, at time.Time) Attempt {
	return Attempt{Number: number, Holder: holder, StartedAt: at}
}

func testFinishedAttempt(number int, holder string, at time.Time, problem string) Attempt {
	finished := at.Add(time.Minute)
	return Attempt{Number: number, Holder: holder, StartedAt: at, FinishedAt: &finished, Problem: problem}
}

func testAnswer(attempt int, at time.Time) *Response {
	return &Response{
		Text:           "It costs a design revision, and you are missing the migration.",
		At:             at,
		Attempt:        attempt,
		Backend:        "claudecode",
		Model:          "opus",
		ResolvedModel:  "claude-opus-5",
		AccountAlias:   "work",
		ConfigRevision: "cfg-0a1b2c3d",
		Build:          "abc1234",
	}
}

func assertRefused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Validate() error = nil, want %q refused", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Validate() error = %v, want it to name %q", err, want)
	}
}
