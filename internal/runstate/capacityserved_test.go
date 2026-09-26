package runstate

import (
	"os"
	"testing"
	"time"
)

// The record keeps the latest served moment per account and model, and an
// earlier moment than the one it holds writes nothing: two processes served at
// nearly the same moment agree about the window.
func TestTheCapacityServedRecordKeepsTheLatestMomentPerAccountAndModel(t *testing.T) {
	t.Parallel()

	store, err := NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	if listed, err := store.List(); err != nil || len(listed) != 0 {
		t.Fatalf("List() on nothing recorded = %+v, %v; want an empty list and no error", listed, err)
	}
	first := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	for _, served := range []CapacityServed{
		{AccountAlias: "default", Model: "opus", At: first, What: "a turn"},
		{AccountAlias: "default", Model: "opus", At: first.Add(time.Hour), What: "a later turn"},
		{AccountAlias: "default", Model: "opus", At: first.Add(time.Minute), What: "an earlier turn"},
		{AccountAlias: "default", Model: "sonnet", At: first, What: "another model"},
	} {
		if err := store.Record(served); err != nil {
			t.Fatalf("Record(%+v) error = %v", served, err)
		}
	}
	listed, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() = %+v, want one entry per account and model", listed)
	}
	if listed[0].Model != "opus" || !listed[0].At.Equal(first.Add(time.Hour)) || listed[0].What != "a later turn" {
		t.Fatalf("opus entry = %+v, want the latest moment kept", listed[0])
	}
	if err := store.Record(CapacityServed{AccountAlias: "default", At: first}); err == nil {
		t.Fatal("Record() of a served invocation naming no model succeeded, want it refused")
	}
}

// A field a newer build wrote is stepped over by the reading every surface
// makes, and refused by the write that would drop it.
func TestTheCapacityServedRecordIsReadTolerantlyAndRewrittenStrictly(t *testing.T) {
	t.Parallel()

	store, err := NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	newer := `{"schema_version":1,"product_id":"yoyodyne","served":[{"model":"opus","at":"2026-09-24T09:00:00Z","region":"us"}]}`
	if err := os.WriteFile(store.Path(), []byte(newer), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if listed, err := store.List(); err != nil || len(listed) != 1 {
		t.Fatalf("List() = %+v, %v; want the entry read past the unknown field", listed, err)
	}
	if err := store.Record(CapacityServed{Model: "opus", At: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Fatal("Record() over a record carrying an unknown field succeeded, want it refused rather than the field dropped")
	}
}

// Lifts is the whole rule: later, on the same model, and on the same account
// where both name one.
func TestAServedInvocationLiftsOnlyEarlierRefusalsOfItsAccountAndModel(t *testing.T) {
	t.Parallel()

	refusedAt := time.Date(2026, 9, 23, 6, 53, 0, 0, time.UTC)
	served := CapacityServed{AccountAlias: "default", Model: "opus", At: refusedAt.Add(26 * time.Hour)}
	for _, test := range []struct {
		name    string
		refusal UsageLimitExhaustion
		lifted  bool
	}{
		{"same account and model, earlier", UsageLimitExhaustion{At: refusedAt, Model: "opus", AccountAlias: "default"}, true},
		{"after the served turn", UsageLimitExhaustion{At: served.At.Add(time.Minute), Model: "opus", AccountAlias: "default"}, false},
		{"another model", UsageLimitExhaustion{At: refusedAt, Model: "sonnet", AccountAlias: "default"}, false},
		{"another account", UsageLimitExhaustion{At: refusedAt, Model: "opus", AccountAlias: "overflow"}, false},
		{"no account recorded", UsageLimitExhaustion{At: refusedAt, Model: "opus"}, true},
		{"no model recorded", UsageLimitExhaustion{At: refusedAt}, true},
	} {
		if got := served.Lifts(test.refusal); got != test.lifted {
			t.Errorf("%s: Lifts() = %v, want %v", test.name, got, test.lifted)
		}
	}
}
