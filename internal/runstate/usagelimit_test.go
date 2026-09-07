package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestARefusalOutlivesTheProcessThatMetIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestUsageLimitStore(t, root)
	reset := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	refusals := []UsageLimitExhaustion{
		testUsageLimitExhaustion("the product manager conversation chat-91253e0e", &reset),
		testUsageLimitExhaustion("the independent review review-4d1f of main", nil),
	}
	for _, refusal := range refusals {
		if err := store.Record(refusal); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	// The reader is a different process, which is the whole point: a refusal that
	// only ever reached the terminal that met it is the silence this record
	// exists to break.
	reloaded := newTestUsageLimitStore(t, root)
	recorded, err := reloaded.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != len(refusals) {
		t.Fatalf("List() = %#v, want every refusal in the order it happened", recorded)
	}
	for index, refusal := range refusals {
		if recorded[index].Waiting != refusal.Waiting || recorded[index].Kind != refusal.Kind {
			t.Fatalf("refusal %d = %#v, want %#v", index, recorded[index], refusal)
		}
	}
	// A provider that named a reset is described with it, and one that named none
	// says the limit alone rather than a deadline nobody quoted.
	if described := recorded[0].Describe(); !strings.Contains(described, "an exhausted five-hour usage limit") ||
		!strings.Contains(described, reset.Format(time.RFC3339)) {
		t.Fatalf("Describe() = %q, want the limit and when it lifts", described)
	}
	if described := recorded[1].Describe(); strings.Contains(described, "until") {
		t.Fatalf("Describe() = %q, want no deadline where the provider named none", described)
	}
}

// A product no provider has refused has no refusals, which is not a failure to
// read: the absence of the log is the ordinary state of a healthy account.
func TestAProductNoProviderHasRefusedHasNoRefusals(t *testing.T) {
	t.Parallel()

	refusals, err := newTestUsageLimitStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(refusals) != 0 {
		t.Fatalf("List() = %#v, want nothing", refusals)
	}
}

func TestARefusalThatSaysNothingUsefulOrDoesNotBelongHereIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestUsageLimitStore(t, t.TempDir())
	elsewhere := testUsageLimitExhaustion("another product's conversation", nil)
	elsewhere.ProductID = "another-product"
	if err := store.Record(elsewhere); err == nil {
		t.Fatal("Record() error = nil, want another product's refusal refused")
	}
	// A refusal nobody can say what it stopped is not worth recording: what an
	// operator does about hours of silence depends entirely on whose work is
	// inside it.
	anonymous := testUsageLimitExhaustion("", nil)
	if err := store.Record(anonymous); err == nil {
		t.Fatal("Record() error = nil, want a refusal that names nothing waiting refused")
	}
	verbose := testUsageLimitExhaustion(strings.Repeat("x", MaxUsageLimitWaitingBytes+1), nil)
	if err := store.Record(verbose); err == nil {
		t.Fatal("Record() error = nil, want what is waiting past its bound refused")
	}
	undated := testUsageLimitExhaustion("the product manager conversation chat-91253e0e", nil)
	undated.At = time.Time{}
	if err := store.Record(undated); err == nil {
		t.Fatal("Record() error = nil, want a refusal with no moment refused")
	}
	// A present-but-empty reset is a provider that named none, recorded as though
	// it had: the two lead an operator to opposite conclusions.
	unset := testUsageLimitExhaustion("the product manager conversation chat-91253e0e", &time.Time{})
	if err := store.Record(unset); err == nil {
		t.Fatal("Record() error = nil, want a reset naming no moment refused")
	}
}

// A log that cannot be read is an error rather than an absence, for the reason
// every other record here is: refusals nobody can read must not be reported as a
// provider that refused nothing.
func TestAnUnreadableUsageLimitLogIsAnError(t *testing.T) {
	t.Parallel()

	store := newTestUsageLimitStore(t, t.TempDir())
	if err := store.Record(testUsageLimitExhaustion("the product manager conversation chat-91253e0e", nil)); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List() error = nil, want an unreadable log refused rather than read as empty")
	}
}

func newTestUsageLimitStore(t *testing.T, root string) *UsageLimitStore {
	t.Helper()
	store, err := NewUsageLimitStore(filepath.Clean(root), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	return store
}

func testUsageLimitExhaustion(waiting string, resetsAt *time.Time) UsageLimitExhaustion {
	return UsageLimitExhaustion{
		SchemaVersion: UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            time.Date(2026, 8, 19, 21, 0, 0, 0, time.UTC),
		Waiting:       waiting,
		Kind:          "five-hour",
		ResetsAt:      resetsAt,
	}
}

// A substitution says both halves or it says nothing worth reading: which model
// was refused, and which one served instead. A record naming only the alternate
// is a turn moved off nothing, and one naming the same model twice is not a
// substitution at all.
func TestASubstitutionNamesBothTheRefusedModelAndTheOneThatServed(t *testing.T) {
	t.Parallel()

	refusal := testUsageLimitExhaustion("the development manager conversation chat-91253e0e", nil)
	refusal.Model = "fable"
	refusal.ServedBy = "opus"
	if err := refusal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want a substitution that names both models accepted", err)
	}
	if !refusal.Substituted() {
		t.Fatal("the record does not read as a substitution, so nothing would say the work carried on")
	}

	orphaned := refusal
	orphaned.Model = ""
	if err := orphaned.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want an alternate named beside no refused model refused")
	}

	same := refusal
	same.ServedBy = same.Model
	if err := same.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want a substitution onto the model that was refused refused")
	}
}

// A refusal that named a reset time stands until that moment and no further.
// One that named none stands for the caller's own probe interval instead — a
// limit reported without a deadline is a wait of unknown length rather than of
// no length, and a caller with no interval to offer is told nothing stands.
func TestARefusalStandsUntilItsResetOrThePauseThatStandsInForOne(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)
	recordedAt := at.Add(-time.Minute)
	reset := at.Add(time.Hour)
	timed := testUsageLimitExhaustion("the development manager conversation", &reset)
	timed.At = recordedAt
	if !timed.WindowClosed(at, 0) {
		t.Fatal("a refusal that lifts in an hour reads as lifted, so the next turn would be refused again")
	}
	if timed.WindowClosed(reset.Add(time.Minute), 0) {
		t.Fatal("a refusal still stands past its own reset time, so affinity would never return")
	}

	// A reset already past when the provider refused describes no wait at all, so
	// it is read as though the provider named none rather than as a window that
	// closed before it opened.
	past := reset.Add(-2 * time.Hour)
	malformed := testUsageLimitExhaustion("the development manager conversation", &past)
	malformed.At = recordedAt
	if malformed.WindowClosed(at, 0) {
		t.Fatal("a reset already past when the limit refused reads as a standing window")
	}
	if !malformed.WindowClosed(at, time.Hour) {
		t.Fatal("a reset that describes no wait does not fall back to the probe interval")
	}

	// And the undated refusal itself: nothing stands where the caller offers no
	// interval, and the interval is what stands where it does.
	undated := testUsageLimitExhaustion("the development manager conversation", nil)
	undated.At = recordedAt
	if undated.WindowClosed(at, 0) {
		t.Fatal("a refusal that named no reset stands forever for a caller with no interval to apply")
	}
	if !undated.WindowClosed(at, time.Hour) {
		t.Fatal("a refusal that named no reset does not stand for the interval offered for it")
	}
	if undated.WindowClosed(recordedAt.Add(time.Hour), time.Hour) {
		t.Fatal("a refusal stands past the interval offered for it, so the model would never be asked again")
	}
}

// A substitution says why the turn moved, and an entry from before there was
// more than one reason reads as the reason there was. Whoever reads this log
// back to decide what to ask for next turn tells the two apart by nothing else.
func TestASubstitutionSaysWhyTheTurnMoved(t *testing.T) {
	t.Parallel()

	capacity := testUsageLimitExhaustion("the development manager conversation", nil)
	capacity.Model = "fable"
	capacity.ServedBy = "opus"
	if reason := capacity.Reason(); reason != SubstitutedForCapacity {
		t.Fatalf("Reason() = %q, want an entry naming none to read as the only reason there was", reason)
	}

	availability := capacity
	availability.Substitution = SubstitutedForAvailability
	if err := availability.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want an availability substitution accepted", err)
	}
	// And it is described as what it is. Calling it an exhausted limit would tell
	// an operator to expect a window that is never going to lift.
	if described := availability.Describe(); strings.Contains(described, "usage limit") {
		t.Fatalf("Describe() = %q, want a model the provider has not got said as itself", described)
	}

	orphaned := testUsageLimitExhaustion("the development manager conversation", nil)
	orphaned.Substitution = SubstitutedForAvailability
	if err := orphaned.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want a reason with nothing that took the turn refused")
	}

	unknown := availability
	unknown.Substitution = "whatever"
	if err := unknown.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want a reason outside the closed set refused")
	}

	// A model the provider has not got is not waiting for a window, so a reset
	// time on one describes a wait that is not happening.
	reset := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	dated := availability
	dated.ResetsAt = &reset
	if err := dated.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want an availability substitution with a reset time refused")
	}
}

// An availability substitution stands for the caller's probe interval and no
// longer, so a version that arrives — or one withdrawn in error that comes back
// — is found by asking again rather than never asked for at all.
func TestAnAvailabilitySubstitutionStandsForTheProbeIntervalOnly(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)
	missing := testUsageLimitExhaustion("the architect conversation", nil)
	missing.Model = "claude-opus-5-20260401"
	missing.ServedBy = "opus"
	missing.Substitution = SubstitutedForAvailability
	missing.At = at.Add(-time.Minute)
	if !missing.WindowClosed(at, time.Hour) {
		t.Fatal("a version found missing a minute ago is asked for again immediately, which costs an invocation per turn")
	}
	if missing.WindowClosed(at.Add(2*time.Hour), time.Hour) {
		t.Fatal("a version found missing stays missing forever, so a pin would become an alias the operator thinks is a pin")
	}
}
