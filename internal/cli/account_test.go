package cli

// The join between the configured pool and the run records.
//
// Each half is tested where it lives — the rotation and the budget arithmetic in
// internal/config, the spend over a window and the last-served alias in
// internal/runstate — and neither of those says the two are actually wired to
// each other. What is under test here is that the pool reads its cursor and its
// spend out of the runs this machine has really made, and that the window those
// budgets are measured over is the one the clock says.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// poolClock is when these pools are asked to choose. It is fixed so a budget
// window is a thing a test can stand on either side of.
var poolClock = time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

func TestThePoolTakesItsCursorFromTheRunRecords(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	pool := accountPool{
		config:    twoAccounts(config.Account{}, config.Account{}),
		stateRoot: stateRoot,
		runs:      store,
		now:       func() time.Time { return poolClock },
	}

	// A machine that has run nothing has no cursor, so the rotation starts at the
	// top of the active half rather than nowhere.
	chosen, err := pool.ChooseAccount()
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "one" {
		t.Fatalf("with nothing recorded the pool chose %q, want the first active account", chosen.Alias)
	}

	served := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-first", poolClock.Add(-time.Hour))
	served.AccountAlias = "one"
	saveRun(t, store, served)

	chosen, err = pool.ChooseAccount()
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "two" {
		t.Fatalf("after a run on %q the pool chose %q, want the next account", served.AccountAlias, chosen.Alias)
	}
	// A pooled alias other than the default authenticates in a home of its own,
	// and the pool is what hands that home to the invocation.
	if want := filepath.Join(stateRoot, "accounts", "two"); chosen.Directory != want {
		t.Fatalf("the chosen account authenticates in %q, want %q", chosen.Directory, want)
	}
}

func TestThePoolStandsDownAnAccountThatHasSpentItsWeeklyBudget(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The most recent run named "two", so the rotation offers "one" first in both
	// readings below. What changes between them is only whether "one" is still
	// inside its budget, which is what makes the budget the thing being measured.
	latest := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-latest", poolClock.Add(-24*time.Hour))
	latest.AccountAlias = "two"
	saveRun(t, store, latest)
	overspent := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-overspent", poolClock.Add(-48*time.Hour))
	overspent.AccountAlias = "one"
	saveRun(t, store, overspent)
	appendRunCost(t, store, overspent.RunID, 12)

	budgeted := twoAccounts(config.Account{WeeklyBudgetUSD: 10}, config.Account{})
	pool := accountPool{
		config:    budgeted,
		stateRoot: stateRoot,
		runs:      store,
		now:       func() time.Time { return poolClock },
	}
	chosen, err := pool.ChooseAccount()
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "two" {
		t.Fatalf("the pool chose %q, want the account that has not spent its budget", chosen.Alias)
	}

	// The window rolls, which is the other half of what a weekly budget is: read
	// a fortnight later, the same spend is outside it and the account it stood
	// down is available again. Nothing about the records changed.
	rolled := pool
	rolled.now = func() time.Time { return poolClock.Add(14 * 24 * time.Hour) }
	chosen, err = rolled.ChooseAccount()
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "one" {
		t.Fatalf("once the spend aged out the pool chose %q, want the account back in the rotation", chosen.Alias)
	}
}

// A project with one account has nothing to choose between, and what the join
// answers for it is the same answer the configuration alone would have given —
// the machine's own provider home, under the alias the record already names.
func TestAProjectWithOneAccountIsAnsweredWithoutPricingAnything(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	pool := accountPool{
		config:    config.Config{Accounts: map[string]config.Account{"work": {}}},
		stateRoot: stateRoot,
		runs:      store,
		now:       func() time.Time { return poolClock },
	}
	chosen, err := pool.ChooseAccount()
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "work" || chosen.Directory != "" {
		t.Fatalf("ChooseAccount() = %#v, want the lone account in the machine's own home", chosen)
	}
}

// An operator meets an unauthenticated account from two directions — `yoyo
// doctor` reporting it, and a conversation refusing to open on it — and has to
// be handed the same command by both. Two spellings of one remedy is a question
// about which of them is real, and the one that omitted the `mkdir` would fail
// for exactly the alias this is a remedy for: one that has never been signed in,
// whose provider home therefore does not exist yet.
func TestTheLoginRemedyIsTheOneTheDiagnosisPrints(t *testing.T) {
	t.Parallel()

	pooled := config.AccountEndpoint{Alias: "second", Directory: "/state/accounts/second"}
	remedy := accountLoginCommand(pooled)
	if want := doctor.AccountLoginCommand(pooled.Directory); remedy != want {
		t.Fatalf("the conversation's remedy = %q, want the diagnosis's %q", remedy, want)
	}
	if !strings.Contains(remedy, "mkdir -p") || !strings.Contains(remedy, pooled.Directory) {
		t.Fatalf("remedy = %q, want it to create and name the home the alias signs in to", remedy)
	}

	// The default alias signs in where the machine already does, so its remedy is
	// the provider's own and has no home to make.
	lone := accountLoginCommand(config.AccountEndpoint{Alias: config.DefaultAccountAlias})
	if lone != "claude auth login" {
		t.Fatalf("the default alias remedy = %q, want the provider's own login", lone)
	}
}

// twoAccounts is the smallest pooled configuration: two accounts named so their
// stable order is one then two, which is the order the rotation reads them in.
func twoAccounts(one, two config.Account) config.Config {
	return config.Config{Accounts: map[string]config.Account{"one": one, "two": two}}
}
