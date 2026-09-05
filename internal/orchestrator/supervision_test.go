package orchestrator

// The management loop over its real parts: records on disk in a state root,
// leases the operating system holds, and a pass that takes them. Nothing here
// is a fixture standing in for the store or the lease — what a restart does is
// only worth anything if it is what the actual path does.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/supervision"
)

// A process died carrying a request: it opened an attempt, took the lease, and
// stopped. The lease went with it, so the next pass finds the attempt held by
// nobody, delivers it again, and records the answer with what served it.
func TestASupervisionPassReclaimsARequestNobodyIsCarrying(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	interrupted := supervisionTestRequest(1)
	interrupted.Attempts = []supervision.Attempt{supervisionTestAttempt(1, "harness-a", interrupted.OpenedAt)}
	saveSupervisionRequest(t, store, interrupted)

	voice := &supervisionTestVoice{}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(voice.asked) != 1 || voice.asked[0].Attempt != 2 || !voice.asked[0].Reclaimed {
		t.Fatalf("the voice was asked %#v, want attempt 2 of the reclaimed request", voice.asked)
	}
	if len(pass.Results) != 1 || pass.Results[0].Outcome != SupervisionAnswered || !pass.Results[0].Reclaimed {
		t.Fatalf("Results = %#v, want the reclaimed request answered", pass.Results)
	}

	settled := loadSupervisionRequest(t, store, interrupted.ID)
	if settled.Outcome != supervision.OutcomeAnswered || settled.SettledAt == nil {
		t.Fatalf("the record = %+v, want it settled answered", settled)
	}
	if settled.Response == nil || settled.Response.Attempt != 2 {
		t.Fatalf("the answer = %+v, want it recorded against attempt 2", settled.Response)
	}
	if len(settled.Attempts) != 2 || settled.Attempts[0].Open() || settled.Attempts[1].Open() {
		t.Fatalf("attempts = %#v, want both closed", settled.Attempts)
	}
	// The invocation is attributable after the session that ran it is gone,
	// which is the only reason this record is the durable one.
	served := settled.Attempts[1]
	if served.Backend != "claudecode" || served.Model != "opus" ||
		served.AccountAlias != "work" || served.ConfigRevision != "cfg-0a1b2c3d" {
		t.Fatalf("the attempt = %+v, want what served it recorded", served)
	}
	if served.ResolvedModel != "claude-opus-5" || served.CostUSD != 0.12 {
		t.Fatalf("the attempt = %+v, want what the call itself reported recorded", served)
	}
}

// The invariant asks that every provider invocation be pinned to what served
// it, not every one that produced an answer. The failed attempt is the case
// that matters: no answer ever comes back to carry the attribution, and it is
// the invocation somebody is most likely to go looking for, because it was paid
// for and produced nothing.
func TestAFailedInvocationIsStillAttributableAndStillCosted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	pending := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, pending)

	voice := &supervisionTestVoice{
		fail:     errors.New("the provider refused the invocation"),
		failCost: 0.04,
	}
	if _, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	recorded := loadSupervisionRequest(t, store, pending.ID)
	if len(recorded.Attempts) != 1 {
		t.Fatalf("attempts = %#v, want the failed attempt recorded", recorded.Attempts)
	}
	spent := recorded.Attempts[0]
	if spent.Backend != "claudecode" || spent.Model != "opus" ||
		spent.AccountAlias != "work" || spent.ConfigRevision != "cfg-0a1b2c3d" {
		t.Fatalf("the failed attempt = %+v, want what served it recorded", spent)
	}
	if spent.SessionID != "session-that-failed" || spent.ResolvedModel != "claude-opus-5" {
		t.Fatalf("the failed attempt = %+v, want what the call reported recorded", spent)
	}
	if spent.CostUSD != 0.04 || recorded.CostUSD() != 0.04 {
		t.Fatalf("the failed attempt cost %v and the request %v, want the failure charged for",
			spent.CostUSD, recorded.CostUSD())
	}
}

// An invocation nobody could have attributed is one that should not be made. A
// voice that cannot say what will serve the call opens no attempt and spends
// nothing, and the request is left exactly as it was.
func TestNothingIsSpentWhenTheHarnessCannotSayWhatWouldServeIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	pending := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, pending)

	voice := &supervisionTestVoice{servingFails: errors.New("no account is configured for the architect")}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no account is configured") {
		t.Fatalf("Run() error = %v, want the routing failure reported", err)
	}
	if len(voice.asked) != 0 || len(pass.Results) != 0 {
		t.Fatalf("pass = %#v, voice = %#v, want nothing invoked", pass.Results, voice.asked)
	}
	if untouched := loadSupervisionRequest(t, store, pending.ID); untouched.Spent() != 0 {
		t.Fatalf("the record = %+v, want no attempt opened", untouched)
	}
}

// The window a crash actually lands in: the answer was written and the ending
// was not. The next pass settles it and never asks again, because it has
// already been paid for.
func TestASupervisionPassNeverAsksAgainForAnAnswerAlreadyRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	paidFor := supervisionTestRequest(1)
	paidFor.Attempts = []supervision.Attempt{supervisionTestAttempt(1, "harness-a", paidFor.OpenedAt)}
	paidFor.Response = &supervision.Response{
		Text:    "It costs a design revision.",
		At:      paidFor.OpenedAt.Add(time.Minute),
		Attempt: 1,
	}
	saveSupervisionRequest(t, store, paidFor)

	voice := &supervisionTestVoice{}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(voice.asked) != 0 {
		t.Fatalf("the voice was asked %#v, want an answered request never delivered again", voice.asked)
	}
	if len(pass.Results) != 1 || pass.Results[0].Outcome != SupervisionSettled {
		t.Fatalf("Results = %#v, want it settled", pass.Results)
	}
	settled := loadSupervisionRequest(t, store, paidFor.ID)
	if settled.Outcome != supervision.OutcomeAnswered || settled.SettledAt == nil {
		t.Fatalf("the record = %+v, want the recorded answer settled", settled)
	}
	if settled.Response.Text != paidFor.Response.Text {
		t.Fatalf("the answer changed: %+v", settled.Response)
	}
	// The attempt the dead process left open is closed by the ending, so a
	// settled request does not read as one still in flight.
	if settled.Attempts[0].Open() {
		t.Fatalf("attempts = %#v, want the abandoned attempt closed", settled.Attempts)
	}
}

// A request a live process holds is that process's, and the lease is what says
// so. This is the same lease the other process took, taken here and refused.
func TestASupervisionPassLeavesARequestAnotherProcessHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	carried := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, carried)

	// The other process, holding the lease for as long as this test runs.
	elsewhere := newSupervisionTestStore(t, root)
	lease, taken, err := elsewhere.HoldRequest(carried.ID)
	if err != nil || !taken {
		t.Fatalf("HoldRequest() = %v, %v", taken, err)
	}
	defer lease.Release()

	voice := &supervisionTestVoice{}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(voice.asked) != 0 || len(pass.Results) != 0 {
		t.Fatalf("pass = %#v, voice = %#v, want the held request left alone", pass, voice.asked)
	}
	if untouched := loadSupervisionRequest(t, store, carried.ID); len(untouched.Attempts) != 0 {
		t.Fatalf("the record = %+v, want it untouched", untouched)
	}
}

// The attempt is on disk before the role is invoked. That ordering is the whole
// of why a crash mid-invocation costs an attempt rather than going uncounted,
// so it is asserted from inside the invocation itself.
func TestTheAttemptIsRecordedBeforeTheRoleIsInvoked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	pending := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, pending)

	reader := newSupervisionTestStore(t, root)
	var duringInvocation supervision.Request
	voice := &supervisionTestVoice{during: func() {
		loaded, err := reader.LoadRequest(pending.ID)
		if err != nil {
			t.Errorf("LoadRequest() during the invocation: %v", err)
			return
		}
		duringInvocation = loaded
	}}

	if _, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(duringInvocation.Attempts) != 1 || !duringInvocation.Attempts[0].Open() {
		t.Fatalf("the record during the invocation = %+v, want the attempt already open on disk", duringInvocation)
	}
	if duringInvocation.Attempts[0].Holder != "harness-under-test" {
		t.Fatalf("holder = %q, want the process taking the pass named", duringInvocation.Attempts[0].Holder)
	}
	if duringInvocation.Response != nil {
		t.Fatalf("the answer was on disk before it came back: %+v", duringInvocation.Response)
	}
}

// A provider that failed spends the attempt and says why, so a later pass
// retries it against a budget that actually moved.
func TestAFailedInvocationSpendsTheAttemptAndSaysWhy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	pending := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, pending)

	voice := &supervisionTestVoice{fail: errors.New("the provider refused the invocation")}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(pass.Results) != 1 || pass.Results[0].Outcome != SupervisionUnanswered {
		t.Fatalf("Results = %#v, want the failed delivery reported", pass.Results)
	}
	recorded := loadSupervisionRequest(t, store, pending.ID)
	if !recorded.Open() {
		t.Fatalf("the record = %+v, want it still open with attempts left", recorded)
	}
	if recorded.Spent() != 1 || recorded.Attempts[0].Open() ||
		!strings.Contains(recorded.Attempts[0].Problem, "provider refused") {
		t.Fatalf("attempts = %#v, want one spent attempt saying why", recorded.Attempts)
	}
}

// One topic takes its requests one at a time, through the real path: two
// requests on one topic, one pass, one invocation.
func TestOneTopicTakesOneDeliveryPerPass(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	first, second := supervisionTestRequest(1), supervisionTestRequest(2)
	second.To = domain.RoleDevelopmentManager
	saveSupervisionRequest(t, store, first)
	saveSupervisionRequest(t, store, second)

	voice := &supervisionTestVoice{}
	pass, err := newSupervisionLoop(newSupervisionTestStore(t, root), voice).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(voice.asked) != 1 || voice.asked[0].RequestID != first.ID {
		t.Fatalf("the voice was asked %#v, want only the older request on the topic", voice.asked)
	}
	if len(pass.Plan.Queued) != 1 || pass.Plan.Queued[0].Behind != first.ID {
		t.Fatalf("Queued = %#v, want the newer request behind the older", pass.Plan.Queued)
	}
	if waiting := loadSupervisionRequest(t, store, second.ID); waiting.Spent() != 0 {
		t.Fatalf("the queued request = %+v, want nothing spent on it", waiting)
	}
}

// A request nothing answered ends rather than retrying for ever, and the
// operator is told — which is the only reason a bounded loop is safe to leave
// running.
func TestARequestOutOfAttemptsIsSettledAndEscalated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	spent := supervisionTestRequest(1)
	spent.CycleLimit = 1
	finished := spent.OpenedAt.Add(time.Minute)
	attempt := supervisionTestAttempt(1, "harness-a", spent.OpenedAt)
	attempt.FinishedAt = &finished
	attempt.Problem = "the provider refused the invocation"
	attempt.CostUSD = 0.03
	spent.Attempts = []supervision.Attempt{attempt}
	saveSupervisionRequest(t, store, spent)

	reports := &supervisionTestReports{}
	loop := newSupervisionLoop(newSupervisionTestStore(t, root), &supervisionTestVoice{})
	loop.Reports = reports
	pass, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(pass.Results) != 1 || pass.Results[0].Outcome != SupervisionSettled {
		t.Fatalf("Results = %#v, want it settled", pass.Results)
	}
	ended := loadSupervisionRequest(t, store, spent.ID)
	if ended.Outcome != supervision.OutcomeUnresolved || ended.SettledAt == nil {
		t.Fatalf("the record = %+v, want it ended unresolved", ended)
	}
	if len(reports.collected) != 1 || reports.collected[0].Severity != report.SeverityWarning ||
		!strings.Contains(reports.collected[0].Message, spent.ID) {
		t.Fatalf("reports = %#v, want the unresolved request escalated by name", reports.collected)
	}
}

// Recovering from a lost process is a question about recorded evidence, never a
// reason to start an invocation nobody asked for. A pass wired without a voice
// reclaims and settles and delivers nothing.
func TestAPassWithNoVoiceSettlesAndDeliversNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)

	interrupted := supervisionTestRequest(1)
	interrupted.Attempts = []supervision.Attempt{supervisionTestAttempt(1, "harness-a", interrupted.OpenedAt)}
	answered := supervisionTestRequest(2)
	answered.Topic = "chat-two"
	answered.Attempts = []supervision.Attempt{supervisionTestAttempt(1, "harness-a", answered.OpenedAt)}
	answered.Response = &supervision.Response{
		Text: "It costs a design revision.", At: answered.OpenedAt.Add(time.Minute), Attempt: 1,
	}
	saveSupervisionRequest(t, store, interrupted)
	saveSupervisionRequest(t, store, answered)

	loop := newSupervisionLoop(newSupervisionTestStore(t, root), nil)
	pass, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	outcomes := make(map[string]SupervisionOutcome, len(pass.Results))
	for _, result := range pass.Results {
		outcomes[result.RequestID] = result.Outcome
	}
	if outcomes[answered.ID] != SupervisionSettled {
		t.Fatalf("Results = %#v, want the recorded answer settled", pass.Results)
	}
	if outcomes[interrupted.ID] != SupervisionUndelivered {
		t.Fatalf("Results = %#v, want the reclaimable request named rather than invoked", pass.Results)
	}
	if reclaimable := loadSupervisionRequest(t, store, interrupted.ID); reclaimable.Spent() != 1 {
		t.Fatalf("the record = %+v, want no attempt opened without a voice", reclaimable)
	}
}

// A stale judgment comes back as a role to wake and holds nothing up: the work
// its item names is delivered in the same pass.
func TestAStaleJudgmentComesBackAsAWakeupAndStopsNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSupervisionTestStore(t, root)
	pending := supervisionTestRequest(1)
	saveSupervisionRequest(t, store, pending)

	judgment := supervision.Readiness{
		SchemaVersion: supervision.SchemaVersion,
		ID:            fmt.Sprintf("readiness-%032x", 1),
		ProductID:     "yoyodyne",
		Item:          "yoyodyne-ifd.142",
		Judgment:      supervision.JudgmentArchitecture,
		Disposition:   supervision.DispositionCrossCutting,
		Evidence:      "the slice reaches designs the item does not name",
		Against:       []supervision.Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}},
		JudgedBy:      domain.RoleArchitect,
		JudgedAt:      time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC),
	}
	if err := store.SaveReadiness(judgment); err != nil {
		t.Fatalf("SaveReadiness() error = %v", err)
	}

	voice := &supervisionTestVoice{}
	loop := newSupervisionLoop(newSupervisionTestStore(t, root), voice)
	loop.Revisions = map[string]string{"artifact/v1-goals": "r8"}
	pass, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(pass.Plan.Wake) != 1 || pass.Plan.Wake[0].Role != domain.RoleArchitect {
		t.Fatalf("Wake = %#v, want the architect woken for their own judgment", pass.Plan.Wake)
	}
	// Advisory, stated as the assertion that would fail if readiness ever
	// quietly became a gate.
	if len(voice.asked) != 1 || voice.asked[0].RequestID != pending.ID {
		t.Fatalf("the voice was asked %#v, want the work delivered despite the stale judgment", voice.asked)
	}
}

// Test parts.

func newSupervisionTestStore(t *testing.T, root string) *runstate.SupervisionStore {
	t.Helper()
	store, err := runstate.NewSupervisionStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSupervisionStore() error = %v", err)
	}
	return store
}

func newSupervisionLoop(store *runstate.SupervisionStore, voice SupervisionVoice) SupervisionLoop {
	loop := SupervisionLoop{
		Store:        store,
		Holder:       "harness-under-test",
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}
	// A nil interface value is not the same as an interface holding a nil
	// pointer, and the pass asks whether it has a voice at all.
	if voice != nil {
		loop.Voice = voice
	}
	return loop
}

// supervisionTestAttempt is one attempt as the harness opens it: still running,
// and already naming what it is about to spend.
func supervisionTestAttempt(number int, holder string, at time.Time) supervision.Attempt {
	return supervision.Attempt{
		Number:         number,
		Holder:         holder,
		StartedAt:      at,
		Backend:        "claudecode",
		Model:          "opus",
		AccountAlias:   "work",
		ConfigRevision: "cfg-0a1b2c3d",
	}
}

func supervisionTestRequest(n int) supervision.Request {
	opened := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
	return supervision.Request{
		SchemaVersion: supervision.SchemaVersion,
		ID:            fmt.Sprintf("request-%032x", n),
		ProductID:     "yoyodyne",
		Topic:         "chat-one",
		Kind:          supervision.KindConsult,
		From:          domain.RoleProductManager,
		To:            domain.RoleArchitect,
		Subject:       "what would this cost, and what am I missing?",
		CycleLimit:    supervision.DefaultCycleLimit,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
}

func saveSupervisionRequest(t *testing.T, store *runstate.SupervisionStore, request supervision.Request) {
	t.Helper()
	if err := store.SaveRequest(request); err != nil {
		t.Fatalf("SaveRequest(%s) error = %v", request.ID, err)
	}
}

func loadSupervisionRequest(t *testing.T, store *runstate.SupervisionStore, id string) supervision.Request {
	t.Helper()
	loaded, err := store.LoadRequest(id)
	if err != nil {
		t.Fatalf("LoadRequest(%s) error = %v", id, err)
	}
	return loaded
}

// supervisionTestVoice stands in for the harness's provider invocation. It
// records what it was asked, and can look at the durable record while it is
// being asked — which is how the ordering the crash safety rests on is checked.
type supervisionTestVoice struct {
	mu     sync.Mutex
	asked  []supervision.Delivery
	during func()
	fail   error
	// failCost is what a failed invocation reports having cost, which a provider
	// that got far enough to charge for the call does.
	failCost float64
	// servingFails makes the voice unable to say what would serve the call, which
	// is the one case where nothing may be spent.
	servingFails error
}

func (v *supervisionTestVoice) Serving(supervision.Request) (SupervisionServing, error) {
	if v.servingFails != nil {
		return SupervisionServing{}, v.servingFails
	}
	return SupervisionServing{
		Backend:        "claudecode",
		Model:          "opus",
		AccountAlias:   "work",
		ConfigRevision: "cfg-0a1b2c3d",
		Build:          "abc1234",
	}, nil
}

func (v *supervisionTestVoice) Answer(_ context.Context, delivery supervision.Delivery, _ supervision.Request) (SupervisionSpoken, error) {
	v.mu.Lock()
	v.asked = append(v.asked, delivery)
	v.mu.Unlock()
	if v.during != nil {
		v.during()
	}
	if v.fail != nil {
		// A call that reached a provider and then failed still ran in a session
		// and was still charged for, and says so beside the error.
		return SupervisionSpoken{
			ResolvedModel: "claude-opus-5",
			SessionID:     "session-that-failed",
			CostUSD:       v.failCost,
		}, v.fail
	}
	return SupervisionSpoken{
		Answer:        "It costs a design revision, and you are missing the migration.",
		ResolvedModel: "claude-opus-5",
		SessionID:     "session-abc",
		CostUSD:       0.12,
	}, nil
}

type supervisionTestReports struct {
	collected []report.Report
}

func (r *supervisionTestReports) Append(reported report.Report) error {
	r.collected = append(r.collected, reported)
	return nil
}
