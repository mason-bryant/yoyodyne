package runstate

import "testing"

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
