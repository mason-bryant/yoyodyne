package runstate

// What each provider account has been spent, and which one was spent last.
//
// Both answers come out of the run records rather than out of a ledger kept
// beside them. Every run already writes down the account it ran under and every
// run's event log already carries what its invocations cost, so a second store
// counting the same money would be a number that could disagree with the runs it
// was counting — and the one thing an operator cannot adjudicate is two of their
// own surfaces disagreeing about what they spent.
//
// It is also what makes the pool's rotation durable without a cursor of its own.
// The account a run recorded is where the rotation had got to, so it survives a
// crash, a second process, and a machine that was off for a week, for the same
// reason the run itself does.

import (
	"sort"
	"time"
)

// AccountSpend is what one account has cost over a window, and how much of that
// window's evidence survives. An account with unknown runs has been spent more
// than this says: the total is a floor, exactly as an item's price is.
type AccountSpend struct {
	Alias   string  `json:"alias"`
	CostUSD float64 `json:"cost_usd"`
	// Runs counts the runs the figure covers, unpriceable ones included, because
	// how much work an account served is as much of the answer as what it cost.
	Runs int `json:"runs,omitempty"`
	// UnknownRuns counts the runs whose evidence is gone. While it is non-zero the
	// total is a lower bound, and a budget read against it is generous rather than
	// wrong — which is the direction to be wrong in, since the alternative is
	// standing an account down over money nobody can show was spent.
	UnknownRuns int `json:"unknown_runs,omitempty"`
}

// Known reports a figure every run behind it could be priced from.
func (a AccountSpend) Known() bool { return a.UnknownRuns == 0 }

// SpendByAccountSince reports what each account has cost across the runs started
// at or after a moment, one entry per account in alias order. A run that
// recorded no account is left out rather than pooled under a name nobody chose:
// records written before the alias was carried say nothing about which account
// they spent, and inventing one would put somebody else's money against a
// budget.
func (s *Store) SpendByAccountSince(since time.Time) ([]AccountSpend, error) {
	states, err := s.scan("recorded", func(state State) bool {
		return state.AccountAlias != "" && !state.StartedAt.Before(since)
	})
	if err != nil {
		return nil, err
	}
	byAlias := make(map[string]*AccountSpend)
	for _, state := range states {
		spend, seen := byAlias[state.AccountAlias]
		if !seen {
			spend = &AccountSpend{Alias: state.AccountAlias}
			byAlias[state.AccountAlias] = spend
		}
		spend.Runs++
		price := s.priceRun(state)
		if !price.Known() {
			spend.UnknownRuns++
			continue
		}
		spend.CostUSD += price.CostUSD
	}
	aliases := make([]string, 0, len(byAlias))
	for alias := range byAlias {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	spends := make([]AccountSpend, 0, len(aliases))
	for _, alias := range aliases {
		spends = append(spends, *byAlias[alias])
	}
	return spends, nil
}

// SpentByAccountSince is the same evidence as the mapping the pool reads its
// budgets against. It exists beside the listing because a caller deciding
// where to send the next run wants a lookup rather than a report.
func (s *Store) SpentByAccountSince(since time.Time) (map[string]float64, error) {
	spends, err := s.SpendByAccountSince(since)
	if err != nil {
		return nil, err
	}
	spent := make(map[string]float64, len(spends))
	for _, spend := range spends {
		spent[spend.Alias] = spend.CostUSD
	}
	return spent, nil
}

// LastAccountAlias is the account the most recently started run recorded, which
// is where the pool's rotation had got to. A product with no run that named one
// answers with nothing, and the rotation starts from the top — which is what a
// first run under a new pool should do.
//
// Ties are broken on the run id so two runs started in the same instant leave
// the rotation somewhere rather than somewhere different on each reading.
func (s *Store) LastAccountAlias() (string, error) {
	states, err := s.scan("recorded", func(state State) bool { return state.AccountAlias != "" })
	if err != nil {
		return "", err
	}
	var latest State
	for _, state := range states {
		newer := state.StartedAt.After(latest.StartedAt) ||
			(state.StartedAt.Equal(latest.StartedAt) && state.RunID > latest.RunID)
		if latest.RunID == "" || newer {
			latest = state
		}
	}
	return latest.AccountAlias, nil
}
