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
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// accountLoginCommand is what an operator runs to sign one configured account
// in. The default account authenticates where the machine already does, so its
// command is the provider's own; every other alias has a provider home of its
// own, and the command names it. It is a command rather than a description for
// the reason every remedy in `yoyo doctor` is one: what is printed under a
// problem should be what to run.
func accountLoginCommand(account config.AccountEndpoint) string {
	if strings.TrimSpace(account.Directory) == "" {
		return "claude auth login"
	}
	return "CLAUDE_CONFIG_DIR=" + shellQuote(account.Directory) + " claude auth login"
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
	now       func() time.Time
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

func (p accountPool) clock() time.Time {
	if p.now == nil {
		return time.Now().UTC()
	}
	return p.now()
}
