package modelfailover

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An agent that has not enabled failover behaves exactly as it did before this
// existed: one invocation, under the model it named, and a refusal handed
// straight back. Anything else would make a default-off setting cost something.
func TestFailoverOffLeavesTheTurnExactlyAsItWas(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	windows := newTestWindows(t)
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the product manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.UsageLimit == nil {
		t.Fatal("result carried no usage limit, want the refusal handed back untouched")
	}
	if served.Model != "fable" || served.Substituted() {
		t.Fatalf("served = %#v, want the configured model and no substitution", served)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want exactly one", len(provider.requests))
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing written down for a turn nothing was done about", recorded, err)
	}
}

// The turn the whole thing exists for: the configured model has no capacity, the
// permitted alternate takes the turn, and the record says which model served it.
func TestARefusedTurnIsServedByThePermittedAlternate(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", resetsAt),
		{SessionID: "session-1", FinalText: "decided"},
	}}
	windows := newTestWindows(t)
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate: "opus",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.FinalText != "decided" {
		t.Fatalf("final text = %q, want the answer the alternate gave", result.FinalText)
	}
	if served.Model != "opus" || served.Refused != "fable" {
		t.Fatalf("served = %#v, want the alternate serving for the configured model", served)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused one and the one that served", len(provider.requests))
	}
	if provider.requests[0].Model != "fable" || provider.requests[1].Model != "opus" {
		t.Fatalf("models asked = %q then %q, want the configured model first and the alternate second",
			provider.requests[0].Model, provider.requests[1].Model)
	}

	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", recorded)
	}
	substitution := recorded[0]
	if substitution.Model != "fable" || substitution.ServedBy != "opus" {
		t.Fatalf("recorded = %#v, want the refused model and the one that served", substitution)
	}
	if !substitution.Substituted() {
		t.Fatal("the record does not read as a substitution, so nothing would say the work carried on")
	}
	if substitution.ResetsAt == nil || !substitution.ResetsAt.Equal(resetsAt) {
		t.Fatalf("resets at = %v, want the provider's own reset time", substitution.ResetsAt)
	}
	if substitution.Kind != "five_hour" {
		t.Fatalf("kind = %q, want the provider's own name for the limit", substitution.Kind)
	}
}

// The turn after a substitution goes straight to the alternate. A window the
// harness watched close is not worth one refused invocation per turn to
// rediscover, and the record is what carries that across processes.
func TestAKnownClosedWindowIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "fable",
		ServedBy: "opus",
		ResetsAt: pointerTo(fixedNow().Add(time.Hour)),
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate: "opus",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Refused != "fable" {
		t.Fatalf("served = %#v, want the alternate without asking the closed model", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "opus" {
		t.Fatalf("invocations = %#v, want one, straight to the alternate", provider.requests)
	}
	// Nothing new is written down: the window was already recorded, and saying it
	// again every turn is how a channel gets muted.
	if recorded, _ := windows.List(); len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one record that was already there", recorded)
	}
}

// Affinity is the configured model's. The moment the provider's own reset time
// passes, the next turn asks it again — so a substitution lasts a window rather
// than quietly becoming a permanent move to another model.
func TestAffinityReturnsWhenTheWindowReopens(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "fable",
		ServedBy: "opus",
		ResetsAt: pointerTo(fixedNow().Add(-time.Minute)),
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate: "opus",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "fable" || served.Substituted() {
		t.Fatalf("served = %#v, want the configured model back once its window reopened", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "fable" {
		t.Fatalf("invocations = %#v, want the configured model asked again", provider.requests)
	}
}

// A limit the provider reported with no reset time is a wait of unknown length
// rather than of no length. It stands for the configured probe interval and the
// alternate serves through it without saying so again — which is what makes the
// substitution said once per window rather than once per turn, for the refusal
// that carries no window of its own.
func TestARefusalWithNoResetTimeStandsForTheProbeInterval(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{Model: "fable", ServedBy: "opus"})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate:         "opus",
		Windows:           windows,
		Now:               fixedNow,
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Refused != "fable" {
		t.Fatalf("served = %#v, want the alternate for as long as the undated refusal stands", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "opus" {
		t.Fatalf("invocations = %#v, want one, straight to the alternate", provider.requests)
	}
	if recorded, _ := windows.List(); len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution said once rather than again on this turn", recorded)
	}
}

// And it stands for that interval and no longer: the harness was never told when
// the limit lifts, so it asks again rather than settling on the alternate.
func TestAnUndatedRefusalStopsStandingWhenTheProbeIntervalIsUp(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{Model: "fable", ServedBy: "opus"})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate:         "opus",
		Windows:           windows,
		Now:               func() time.Time { return fixedNow().Add(31 * time.Minute) },
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "fable" || served.Substituted() {
		t.Fatalf("served = %#v, want the configured model asked again once the interval was up", served)
	}
}

// The whole of what the once-per-window promise is worth, over an outage the
// provider never dated: several turns, one substitution record, and therefore
// one thing said to the operator.
func TestAnUndatedOutageIsSaidOncePerIntervalRatherThanOncePerTurn(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", time.Time{}),
		{FinalText: "first"},
		{FinalText: "second"},
		{FinalText: "third"},
	}}
	at := fixedNow()
	policy := Policy{
		Alternate:         "opus",
		Windows:           windows,
		Now:               func() time.Time { return at },
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the development manager conversation",
	}
	for turn := 0; turn < 3; turn++ {
		if _, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, policy); err != nil {
			t.Fatalf("turn %d: Serve() error = %v", turn+1, err)
		} else if served.Model != "opus" {
			t.Fatalf("turn %d: served = %#v, want the alternate", turn+1, served)
		}
		at = at.Add(5 * time.Minute)
	}
	// Four invocations: the one refusal that opened the window, and one served
	// turn each. Nothing re-asks the exhausted model inside its own interval.
	if len(provider.requests) != 4 {
		t.Fatalf("invocations = %d, want the refused one and one served turn each", len(provider.requests))
	}
	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want one substitution across the interval rather than one per turn", recorded)
	}
}

// A turn the alternate could not take either is the refusal the caller already
// handles, and it is recorded by whoever fails the turn rather than here: a
// substitution that did not happen must not read as one that did.
func TestAnAlternateThatIsAlsoRefusedRecordsNoSubstitution(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", time.Time{}),
		refused("five_hour", time.Time{}),
	}}
	windows := newTestWindows(t)
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate: "opus",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.UsageLimit == nil {
		t.Fatal("result carried no usage limit, want the alternate's refusal handed back")
	}
	if served.Model != "opus" || served.Refused != "fable" {
		t.Fatalf("served = %#v, want the attempt named honestly even though it failed", served)
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want no substitution recorded for a turn nothing served", recorded, err)
	}
}

// Everything that is not a refusal for want of capacity passes through
// untouched. Failing over on an answer the role gave badly would spend the
// operator's money asking a second model the same question.
func TestOnlyACapacityRefusalIsFailedOver(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		result backend.RunResult
		err    error
	}{
		{name: "a failure that is not a refusal", result: backend.RunResult{IsError: true, StopReason: "max_turns"}},
		{
			name:   "a limit reported beside an answer the provider still gave",
			result: backend.RunResult{FinalText: "here it is", UsageLimit: &backend.UsageLimit{Kind: "five_hour"}},
		},
		{name: "an invocation that died", result: backend.RunResult{}, err: errors.New("the provider went away")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeProvider{results: []backend.RunResult{test.result}, errs: []error{test.err}}
			windows := newTestWindows(t)
			_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
				Alternate: "opus",
				Windows:   windows,
				Now:       fixedNow,
				ProductID: "yoyodyne",
				Waiting:   "the development manager conversation",
			})
			if (err != nil) != (test.err != nil) {
				t.Fatalf("Serve() error = %v, want the invocation's own ending", err)
			}
			if served.Model != "fable" || served.Substituted() {
				t.Fatalf("served = %#v, want the configured model and no substitution", served)
			}
			if len(provider.requests) != 1 {
				t.Fatalf("invocations = %d, want exactly one", len(provider.requests))
			}
		})
	}
}

// A turn the alternate has already answered is not thrown away because the log
// would not take the line. What it costs is that the next turn asks the
// configured model again, which is one refused invocation rather than a silence.
func TestALogThatWillNotTakeTheRecordDoesNotCostTheTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", time.Time{}),
		{FinalText: "decided"},
	}}
	var reported []error
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate:     "opus",
		Windows:       refusingWindows{},
		Now:           fixedNow,
		ProductID:     "yoyodyne",
		Waiting:       "the development manager conversation",
		RecordFailure: func(err error) { reported = append(reported, err) },
	})
	if err != nil {
		t.Fatalf("Serve() error = %v, want the answer the alternate gave", err)
	}
	if result.FinalText != "decided" || served.Model != "opus" {
		t.Fatalf("result = %q served by %q, want the alternate's answer", result.FinalText, served.Model)
	}
	if len(reported) == 0 {
		t.Fatal("nothing was reported about the record that failed, so the loss would be silent")
	}
}

// A caller that wired no way to hear about a failed record is handed it joined
// onto the invocation's own error rather than losing it.
func TestAFailedRecordIsNeverSwallowed(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", time.Time{}),
		{FinalText: "decided"},
	}}
	_, _, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate: "opus",
		Windows:   refusingWindows{},
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	})
	if err == nil {
		t.Fatal("Serve() error = nil, want the failed record handed to a caller that offered nowhere else to put it")
	}
}

// Both attempts write to one event log, so the substituted one starts after the
// events the refused one already emitted. A sequence used twice is a log whose
// numbering no longer says what order anything happened in.
func TestTheSubstitutedAttemptDoesNotNumberOverTheRefusedOne(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backend.UsageLimit{Kind: "five_hour"},
			LastEvent:  7,
		},
		{FinalText: "decided", LastEvent: 9},
	}}
	if _, _, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable", LastSequence: 4}, Policy{
		Alternate: "opus",
		Windows:   newTestWindows(t),
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the development manager conversation",
	}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if provider.requests[1].LastSequence != 7 {
		t.Fatalf("the substituted attempt started at sequence %d, want it after the %d the refused one reached",
			provider.requests[1].LastSequence, provider.requests[0].LastSequence)
	}
}

type fakeProvider struct {
	results  []backend.RunResult
	errs     []error
	requests []backend.RunRequest
}

func (f *fakeProvider) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	index := len(f.requests)
	f.requests = append(f.requests, request)
	if index < len(f.errs) && f.errs[index] != nil {
		return backend.RunResult{}, f.errs[index]
	}
	if index >= len(f.results) {
		return backend.RunResult{}, errors.New("unexpected invocation")
	}
	return f.results[index], nil
}

// refusingWindows is a log that takes nothing and reads back nothing, which is
// how a state root that cannot be written looks from here.
type refusingWindows struct{}

func (refusingWindows) Record(runstate.UsageLimitExhaustion) error {
	return errors.New("the log would not take it")
}

func (refusingWindows) List() ([]runstate.UsageLimitExhaustion, error) {
	return nil, errors.New("the log could not be read")
}

func newTestWindows(t *testing.T) *runstate.UsageLimitStore {
	t.Helper()

	store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	return store
}

// recordWindow writes one entry with the fields every refusal needs already filled in,
// so a test states only the part it is about.
func recordWindow(t *testing.T, windows *runstate.UsageLimitStore, exhaustion runstate.UsageLimitExhaustion) {
	t.Helper()

	exhaustion.SchemaVersion = runstate.UsageLimitSchemaVersion
	exhaustion.ProductID = "yoyodyne"
	exhaustion.At = fixedNow()
	if exhaustion.Waiting == "" {
		exhaustion.Waiting = "the development manager conversation"
	}
	if err := windows.Record(exhaustion); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func refused(kind string, resetsAt time.Time) backend.RunResult {
	return backend.RunResult{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backend.UsageLimit{Kind: kind, ResetsAt: resetsAt},
	}
}

func fixedNow() time.Time { return time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC) }

func pointerTo(at time.Time) *time.Time { return &at }

// A substitution can never move a role onto an endpoint whose sandbox cannot
// hold that role's tool posture. The check is the point at which an endpoint
// changes without anybody having configured the change, so this is where the
// posture is asked about again — and a refusal here leaves the turn exactly
// where it was rather than moving it.
func TestASubstitutionOntoAnIneligibleEndpointIsRefusedWithTheReasonNamed(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	windows := newTestWindows(t)
	var reported []error
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate:     "opus",
		Windows:       windows,
		Now:           fixedNow,
		ProductID:     "yoyodyne",
		Waiting:       "the reviewer",
		Endpoint:      backend.Endpoint{Provider: "writes-only", AdapterVersion: "claude-code/1", AccountAlias: "default", Model: "fable"},
		Role:          domain.RoleReviewer,
		Eligibility:   writesOnlyRegistry(t),
		RecordFailure: func(err error) { reported = append(reported, err) },
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	// One invocation, under the model the turn named: the refusal is handed back
	// exactly as it would have been with failover off.
	if len(provider.requests) != 1 || provider.requests[0].Model != "fable" {
		t.Fatalf("invocations = %#v, want the one attempt on the configured model", provider.requests)
	}
	if served.Substituted() {
		t.Fatalf("served = %#v, want the turn left on the endpoint it was already on", served)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), `cannot hold the "read-only" tool posture`) {
		t.Fatalf("reported = %v, want the posture that could not be held named", reported)
	}
	// Nothing is written down: no substitution happened, so a record of one would
	// be the log saying a turn moved that never did.
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing recorded for a substitution that was refused", recorded, err)
	}
}

// The same policy, for the role that endpoint can hold, substitutes exactly as
// it did before the check existed. What the check refuses is the posture and
// never the substitution itself.
func TestTheEligibleSubstitutionIsMadeAsItAlwaysWas(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", time.Time{}),
		{SessionID: "session-1", FinalText: "written"},
	}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "fable"}, Policy{
		Alternate:   "opus",
		Windows:     newTestWindows(t),
		Now:         fixedNow,
		ProductID:   "yoyodyne",
		Waiting:     "the developer",
		Endpoint:    backend.Endpoint{Provider: "writes-only", AdapterVersion: "claude-code/1", AccountAlias: "default", Model: "fable"},
		Role:        domain.RoleDeveloper,
		Eligibility: writesOnlyRegistry(t),
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Refused != "fable" {
		t.Fatalf("served = %#v, want the alternate serving for the model with no capacity", served)
	}
}

// writesOnlyRegistry is a project naming one provider that scopes writes to a
// worktree and cannot express no tools at all — which is the shape of a real
// provider rather than an invented one, and the shape the reviewer's posture
// exists to refuse.
func writesOnlyRegistry(t *testing.T) *backend.Registry {
	t.Helper()

	registry, err := backend.NewRegistry(map[domain.Backend]backend.ProviderPlugin{
		"writes-only": {
			Adapter:  domain.BackendClaudeCode,
			Roles:    []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer},
			Postures: []backend.Posture{backend.PostureWorktreeWrite},
			Dialect: backend.DialectSpec{Rules: []backend.DialectRule{
				{Answer: backend.AnswerRefused, Terminal: truth(true), Failed: truth(true)},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func truth(value bool) *bool { return &value }
