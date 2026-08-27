package runstate

import (
	"testing"
	"time"
)

// Which account a run spent and which configuration set it up outlive the run,
// the process, and the file the configuration was read from, so they are held
// to surviving a round trip like every other piece of a run's evidence.
func TestStoreRoundTripsTheAccountAndConfigurationARunRanUnder(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.AccountAlias = "default"
	state.ConfigRevision = "cfg-0123456789ab"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AccountAlias != state.AccountAlias || loaded.ConfigRevision != state.ConfigRevision {
		t.Fatalf("loaded account %q at %q, want %q at %q",
			loaded.AccountAlias, loaded.ConfigRevision, state.AccountAlias, state.ConfigRevision)
	}
	// The summary carries both, because reading the record back is what every
	// surface that reports a run actually does.
	summary := store.summarize(loaded)
	if summary.AccountAlias != state.AccountAlias || summary.ConfigRevision != state.ConfigRevision {
		t.Fatalf("summary account %q at %q, want %q at %q",
			summary.AccountAlias, summary.ConfigRevision, state.AccountAlias, state.ConfigRevision)
	}
}

// A record written before either was carried says nothing about them, and that
// is a record rather than an error: it decodes and keeps meaning what it meant.
func TestARunThatRecordedNoAccountIsStillAValidRecord(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusSucceeded)
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// What is refused is a record naming an account or a configuration nothing
// could have produced. It would read as evidence, and evidence is exactly what
// these two are for.
func TestARecordedAccountAndConfigurationAreHeldToTheirShape(t *testing.T) {
	t.Parallel()

	for name, corrupt := range map[string]func(*State){
		"an alias nothing configured": func(s *State) { s.AccountAlias = "Work Account" },
		"a revision of no digest":     func(s *State) { s.ConfigRevision = "yesterday's" },
		"a revision missing its mark": func(s *State) { s.ConfigRevision = "0123456789ab" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := testState(t, StatusRunning)
			corrupt(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() accepted a record that could not have been written")
			}
		})
	}
}

// The rotation's cursor is the run records themselves, so what the pool reads to
// decide where the next run goes is the account the last one actually spent —
// which survives a crash and a second process for the reason the run does.
func TestTheAccountTheLastRunSpentIsWhatTheRecordsSay(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if alias, err := store.LastAccountAlias(); err != nil || alias != "" {
		t.Fatalf("LastAccountAlias() = %q, %v; want nothing before any run named an account", alias, err)
	}
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for offset, alias := range []string{"one", "two", "three"} {
		state := testState(t, StatusSucceeded)
		state.AccountAlias = alias
		state.StartedAt = base.Add(time.Duration(offset) * time.Hour)
		state.UpdatedAt = state.StartedAt
		completed := state.StartedAt
		state.CompletedAt = &completed
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	alias, err := store.LastAccountAlias()
	if err != nil {
		t.Fatalf("LastAccountAlias() error = %v", err)
	}
	// The latest by start time rather than the last written or the highest run
	// id: the cursor is where the rotation had got to, which is a question about
	// time.
	if alias != "three" {
		t.Fatalf("LastAccountAlias() = %q, want the account the most recently started run spent", alias)
	}
}

// What an account has cost is read from the runs that named it, which is the
// same evidence a work item's price comes from. A second ledger counting the
// same money is the one thing an operator cannot adjudicate.
func TestWhatAnAccountHasSpentIsReadFromTheRunsThatNamedIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for offset, alias := range []string{"one", "one", "two"} {
		state := testState(t, StatusSucceeded)
		state.AccountAlias = alias
		state.StartedAt = base.Add(time.Duration(offset) * time.Hour)
		state.UpdatedAt = state.StartedAt
		completed := state.StartedAt
		state.CompletedAt = &completed
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	// A run recording no account is left out rather than pooled under a name
	// nobody chose: records written before the alias was carried say nothing
	// about which account they spent.
	unattributed := testState(t, StatusSucceeded)
	unattributed.StartedAt = base
	unattributed.UpdatedAt = base
	unattributed.CompletedAt = &base
	if err := store.Create(unattributed); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	spends, err := store.SpendByAccountSince(base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SpendByAccountSince() error = %v", err)
	}
	if len(spends) != 2 || spends[0].Alias != "one" || spends[1].Alias != "two" {
		t.Fatalf("SpendByAccountSince() = %+v, want one and two in alias order", spends)
	}
	if spends[0].Runs != 2 || spends[1].Runs != 1 {
		t.Fatalf("SpendByAccountSince() counted %d and %d runs, want 2 and 1", spends[0].Runs, spends[1].Runs)
	}
	// The window is a window: a run started before it is not this week's spend,
	// which is what lets a budget be spent and then come back.
	later, err := store.SpendByAccountSince(base.Add(90 * time.Minute))
	if err != nil {
		t.Fatalf("SpendByAccountSince() error = %v", err)
	}
	if len(later) != 1 || later[0].Alias != "two" || later[0].Runs != 1 {
		t.Fatalf("SpendByAccountSince() over a shorter window = %+v, want only the run inside it", later)
	}
	spent, err := store.SpentByAccountSince(base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SpentByAccountSince() error = %v", err)
	}
	if len(spent) != 2 {
		t.Fatalf("SpentByAccountSince() = %v, want one entry per account", spent)
	}
}
