package cli

// Choosing which configured provider account serves the next run.
//
// The choosing lives here rather than in the configuration or in the pipeline
// because it needs both halves and neither package holds them: the configuration
// says which accounts exist, which half of the pool each is in, and what the
// operator is willing to spend on it, and the run records say which account was
// spent last and what each has cost since. This is the join, and it is the only
// place the two meet.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// accountLoginCommand is what an operator runs to sign one configured account
// in. It is the diagnosis's own command rather than a second spelling of it:
// `yoyo doctor` reports an account that is not authenticated and a conversation
// refuses to open on one, and an operator who met that condition from either
// direction has to be handed the same thing to run.
func accountLoginCommand(account config.AccountEndpoint) string {
	return doctor.AccountLoginCommand(account.Directory)
}

// weeklyBudgetWindow is the seven days a weekly budget is measured over. It is a
// rolling window rather than a calendar week deliberately: a calendar week has a
// boundary an operator would have to know about to understand why a run was
// refused on Sunday evening and served on Monday morning, and the provider's own
// limits roll rather than reset on a day.
const weeklyBudgetWindow = 7 * 24 * time.Hour

// accountPool picks the account the next run is served by, from the
// configuration's pool and the evidence the run records already hold.
type accountPool struct {
	config    config.Config
	stateRoot string
	runs      *runstate.Store
	// now is when the budget window is measured back from. It is a field because
	// a budget that can only be exercised by waiting a week is one nothing tests.
	now func() time.Time
}

// ChooseAccount rotates the active half of the pool and honours the weekly
// budgets, falling back to the reserved half when the active one has nothing
// left to spend.
//
// What each account has spent is only read when a budget was actually stated. A
// project that budgeted nothing — which is every project until one says
// otherwise — would otherwise price a week of runs on every start to arrive at
// an answer that could exclude nobody, and would acquire a new way for a run to
// fail: an event log that cannot be read.
//
// The cursor is read here and written when the caller reserves the run's record,
// and nothing holds the two together, so two runs starting in the same moment
// can read the same cursor and be served by the same account. That is a known
// bound on the rotation rather than an oversight, and it is bounded in turn: it
// costs an uneven split across a burst and nothing else, because each run is
// still attributed to the account it actually spent and an exhausted budget
// still excludes at the same moment it is read. Serializing it would mean a
// lease held across the choice and the reservation, which is a heavier mechanism
// than the unevenness it would remove — and the choice deliberately happens
// before anything is claimed, so that a pool with nothing left refuses without
// taking a work item.
func (p accountPool) ChooseAccount() (config.AccountEndpoint, error) {
	lastServed, err := p.runs.LastAccountAlias()
	if err != nil {
		return config.AccountEndpoint{}, fmt.Errorf("read which account the last run was served by: %w", err)
	}
	var spent map[string]float64
	if p.config.HasAccountBudgets() {
		spent, err = p.runs.SpentByAccountSince(p.clock().Add(-weeklyBudgetWindow))
		if err != nil {
			return config.AccountEndpoint{}, fmt.Errorf("read what each account has spent this week: %w", err)
		}
	}
	return p.config.ChooseAccount(p.stateRoot, lastServed, spent)
}

// clock reads the field, and falls back rather than panicking on a pool nothing
// gave one to. Every construction supplies it; the fallback is here because the
// alternative to a wrong window is a nil dereference in the middle of starting a
// run, and of those two a correct window is plainly better.
func (p accountPool) clock() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now()
}
