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
