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

// A refusal that named a reset time is a window until that moment and no
// further. One that named none never reads as a closed window at all: the
// harness was not told when it lifts, so it asks again rather than assuming.
func TestOnlyARefusalWithAResetTimeDescribesAClosedWindow(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC)
	reset := at.Add(time.Hour)
	timed := testUsageLimitExhaustion("the development manager conversation", &reset)
	if !timed.WindowClosed(at) {
		t.Fatal("a refusal that lifts in an hour reads as open, so the next turn would be refused again")
	}
	if timed.WindowClosed(reset.Add(time.Minute)) {
		t.Fatal("a refusal reads as closed past its own reset time, so affinity would never return")
	}
	if testUsageLimitExhaustion("the development manager conversation", nil).WindowClosed(at) {
		t.Fatal("a refusal that named no reset reads as a closed window, which is a wait nobody was told about")
	}
}
