package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Account is one provider account the harness runs agents under, named by the
// alias the operator gives it.
//
// What is deliberately absent from an entry is a credential, and it stays
// absent now that there is more than one: authentication is the provider's own
// and lives on this machine, so a key naming a secret here would be
// configuration nothing reads and a promise nothing keeps. What an entry states
// is the name every run record and every surface says the account by, which of
// the pool's two halves it is in, and what the operator is willing to spend on
// it in a week. Where the alias authenticates follows from the alias itself —
// see AccountConfigDirectory — so nothing machine-local is written into a file
// that is versioned with the repository.
type Account struct {
	// Description is whose account this is, in the operator's own words. It is
	// optional because an alias is usually the whole of what somebody needs, and
	// it is the one thing here a reader of the configuration cannot derive from
	// the alias itself.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Pool is which half of the pool this account is in: `active` accounts are
	// round-robined by every run the harness starts, and a `reserved` one is
	// served from only when no active account can be. It is optional and empty
	// means active, so a project that names two accounts and nothing else pools
	// both of them — which is what naming a second one is usually for.
	Pool AccountPool `yaml:"pool,omitempty" json:"pool,omitempty"`
	// WeeklyBudgetUSD is what the operator is willing to spend on this account
	// over the seven days behind now, read from what the runs that named it
	// actually cost. An account at or over its budget is passed over while the
	// pool has anywhere else to go.
	//
	// It is optional, and absent means unbudgeted: spend on this account until
	// the provider's own limit stops us. That is the operator's stated default
	// rather than an oversight — the subscription already has a limit, and a
	// second one the harness invents would stop work the provider was still
	// willing to serve.
	WeeklyBudgetUSD float64 `yaml:"weekly_budget_usd,omitempty" json:"weekly_budget_usd,omitempty"`
}

// AccountPool is which half of the pool an account is in.
type AccountPool string

const (
	// PoolActive is an account the round-robin serves from.
	PoolActive AccountPool = "active"
	// PoolReserved is an account held back for when no active one can serve:
	// exhausted budgets, or an active half that is empty. It is spent from
	// rather than merely held, which is why it is reserved rather than disabled.
	PoolReserved AccountPool = "reserved"
)

// Membership is which half of the pool this account is in, with the unstated
// case resolved. Empty is active deliberately: an operator who adds a second
// account and says nothing else has said they want both of them used.
func (a Account) Membership() AccountPool {
	if pool := AccountPool(strings.TrimSpace(string(a.Pool))); pool != "" {
		return pool
	}
	return PoolActive
}

// Valid reports a pool half the harness recognizes. Empty is valid and means
// active; anything else is a file naming a half that does not exist, which is
// refused rather than quietly read as one of the two.
func (p AccountPool) Valid() bool {
	switch AccountPool(strings.TrimSpace(string(p))) {
	case "", PoolActive, PoolReserved:
		return true
	default:
		return false
	}
}

// DefaultAccountAlias is the account a project that names none runs under. A
// project with one account has nothing to distinguish, so it writes nothing and
// its records still say which account they ran under — which is what makes the
// single-account case read naturally rather than as an abstraction being
// carried. It is also the alias that authenticates where the machine already
// signed in, which is what makes adding a second account additive: the account
// that was there keeps the login it had.
const DefaultAccountAlias = "default"

// MaxAccountDescriptionBytes bounds one account's description so it stays a
// phrase naming whose account it is rather than a document in a configuration
// every surface reads back.
const MaxAccountDescriptionBytes = 200

// AccountEndpoint is one account as an invocation is made under it: the alias a
// record names it by, and the provider configuration directory the invocation
// reads its authentication from. An empty directory is the machine's own
// provider home — what the provider would use if nothing said otherwise — which
// is what the default alias resolves to.
type AccountEndpoint struct {
	Alias     string `json:"alias"`
	Directory string `json:"directory,omitempty"`
}

// AccountConfigDirectory is where one alias of a pool authenticates on this
// machine.
//
// The rule is one line and it is the same line for the harness, `yoyo doctor`,
// and `bin/yoyo-account`: the default alias uses the machine's own provider home,
// and every other alias has a provider home of its own under the state root.
// That is what keeps a second account additive — the account that was already
// signed in is untouched by the arrival of another — and it is why the mapping
// in the configuration names no paths: a versioned file must not carry one
// machine's directories.
//
// It answers for a pool, and a project with one account is not one. What a lone
// account authenticates as is Config.Endpoint's answer rather than this one,
// because it depends on how many accounts there are and not on the alias.
//
// A caller with no state root cannot be told where anything authenticates, so
// it is answered with the machine's own home rather than with a relative
// directory that would be created wherever the process happened to be running.
func AccountConfigDirectory(stateRoot, alias string) string {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" || trimmed == DefaultAccountAlias || strings.TrimSpace(stateRoot) == "" {
		return ""
	}
	return filepath.Join(stateRoot, "accounts", trimmed)
}

// AccountAliases lists the configured accounts in a stable order, so resolution
// and refusals read the mapping the same way round.
func (c Config) AccountAliases() []string {
	aliases := make([]string, 0, len(c.Accounts))
	for alias := range c.Accounts {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

// ActiveAccountAliases and ReservedAccountAliases are the two halves of the
// pool, each in the same stable order. A project that names no account at all
// has one active account — the default one — because that is the account it is
// running on whether or not it wrote it down.
func (c Config) ActiveAccountAliases() []string {
	return c.aliasesIn(PoolActive)
}

func (c Config) ReservedAccountAliases() []string {
	return c.aliasesIn(PoolReserved)
}

func (c Config) aliasesIn(pool AccountPool) []string {
	aliases := c.AccountAliases()
	if len(aliases) == 0 {
		if pool == PoolActive {
			return []string{DefaultAccountAlias}
		}
		return nil
	}
	selected := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if c.Accounts[alias].Membership() == pool {
			selected = append(selected, alias)
		}
	}
	return selected
}

// Pooled reports a project running work across more than one account. It is the
// switch everything additive hangs off: a project with one account behaves
// exactly as it did before pooling existed, down to which provider home its
// agents authenticate in and what `yoyo doctor` says about them.
func (c Config) Pooled() bool { return len(c.AccountAliases()) > 1 }

// AccountAlias is the account a run made under this configuration executes on
// when nothing chooses between several. A project that configures one account
// assigns every role to it, so a run has exactly one alias to record: the
// configured one, or the default where nothing named any. A pooled configuration
// names none here, because guessing which of them a run used is exactly the
// claim this record exists to make honestly — ChooseAccount is what picks, and
// what it picked is what the run records.
func (c Config) AccountAlias() string {
	switch aliases := c.AccountAliases(); len(aliases) {
	case 0:
		return DefaultAccountAlias
	case 1:
		return aliases[0]
	default:
		return ""
	}
}

// AgentAccountAlias is the account one configured agent runs under: the one its
// entry names, or — where it names none, which is every agent in a project with
// a single account — the configuration's own.
//
// A pooled project whose agent named no account is served by the first of the
// active half rather than by the rotation. That is deliberate and it is the one
// place the pool does not rotate: what this answers for is a conversation, which
// belongs to its agent and lasts for weeks, and an agent that moved between
// accounts each turn would have no provider session left to resume. Runs
// rotate; conversations sit still.
func (c Config) AgentAccountAlias(name string) string {
	if alias := strings.TrimSpace(c.Agents[name].Account); alias != "" {
		return alias
	}
	if alias := c.AccountAlias(); alias != "" {
		return alias
	}
	if active := c.ActiveAccountAliases(); len(active) > 0 {
		return active[0]
	}
	return DefaultAccountAlias
}

// HasAccountBudgets reports a project that has budgeted any of its accounts. It
// exists so that a project which budgeted none — which is every project until
// one says otherwise — never has to price a week of runs to find out that
// nothing was going to be excluded by the answer.
func (c Config) HasAccountBudgets() bool {
	for _, alias := range c.AccountAliases() {
		if c.Accounts[alias].WeeklyBudgetUSD > 0 {
			return true
		}
	}
	return false
}

// Endpoint is one configured alias as an invocation is made under it. An alias
// this configuration does not declare is refused rather than resolved: an
// invocation made under a name nothing configured would authenticate somewhere
// nobody chose.
//
// A project with one account authenticates where the machine already does,
// whatever that account is called. Only a pool gives an alias a provider home of
// its own, and that is the whole of what "additive" means here: until a second
// account is declared nothing about where the first one signs in changes, so a
// project that called its single account `work` rather than `default` is not
// quietly moved to a home nobody has signed in to. Declaring the second account
// is what moves the first, and it is a deliberate act with `yoyo doctor` on the
// other side of it naming every alias that now needs a login.
func (c Config) Endpoint(stateRoot, alias string) (AccountEndpoint, error) {
	trimmed := strings.TrimSpace(alias)
	if trimmed == "" {
		return AccountEndpoint{}, errors.New("an account alias is required to resolve where it authenticates")
	}
	_, declared := c.Accounts[trimmed]
	// A configuration that declares no account still has one, so the default
	// alias resolves there exactly as a declared alias does.
	if len(c.Accounts) == 0 && trimmed == DefaultAccountAlias {
		declared = true
	}
	if !declared {
		return AccountEndpoint{}, fmt.Errorf("account %q is not one this configuration declares; it names %s",
			trimmed, describeAliases(c.AccountAliases()))
	}
	if !c.Pooled() {
		return AccountEndpoint{Alias: trimmed}, nil
	}
	return AccountEndpoint{Alias: trimmed, Directory: AccountConfigDirectory(stateRoot, trimmed)}, nil
}

// ChooseAccount picks the account the next run is served by.
//
// The active half is round-robined: the aliases are in one stable order, and
// what is taken is the first one after the account the last run recorded. The
// cursor is that record rather than a counter of its own, which is what makes
// the rotation survive a crash, a second process, and a machine that was turned
// off for a week — every run already writes down the account it spent, so
// nothing further has to be kept for the pool to know where it was.
//
// An account at or over its weekly budget is passed over. When that leaves the
// active half with nothing, the reserved half is what a run falls back to, in
// the same order and under the same budgets — that is what reserving one is
// for. When it leaves the pool with nothing at all, no account is chosen and the
// refusal names the budgets, because a run served by an account the operator
// budgeted away is the one outcome a budget exists to prevent.
//
// spentUSD is what each alias has cost over the window the budgets are stated
// in. An alias absent from it has spent nothing that anything recorded.
func (c Config) ChooseAccount(stateRoot, lastServed string, spentUSD map[string]float64) (AccountEndpoint, error) {
	// The order is built once and then read twice — for the choice and for the
	// refusal that names why there was none — so what a refusal describes is the
	// order the choice was actually made over.
	order := append([]string(nil), c.rotate(c.ActiveAccountAliases(), lastServed)...)
	order = append(order, c.ReservedAccountAliases()...)
	for _, alias := range order {
		if c.withinBudget(alias, spentUSD) {
			return c.Endpoint(stateRoot, alias)
		}
	}
	if len(order) == 0 {
		return AccountEndpoint{}, errors.New("no provider account is configured to run work under")
	}
	return AccountEndpoint{}, fmt.Errorf("every configured account has spent its weekly budget: %s",
		c.describeBudgets(order, spentUSD))
}

// rotate turns a stable order into the order the round-robin reads it in: the
// aliases after the one last served, then the ones before it. An alias the pool
// no longer holds — an account since removed, or one moved to the reserved half
// — leaves the order as it stands, which starts the rotation from the top rather
// than from nowhere.
func (c Config) rotate(aliases []string, lastServed string) []string {
	last := strings.TrimSpace(lastServed)
	if last == "" || len(aliases) < 2 {
		return aliases
	}
	for index, alias := range aliases {
		if alias != last {
			continue
		}
		rotated := make([]string, 0, len(aliases))
		rotated = append(rotated, aliases[index+1:]...)
		rotated = append(rotated, aliases[:index+1]...)
		return rotated
	}
	return aliases
}

// withinBudget reports an account the pool may still spend. An account with no
// budget stated is always within it: what bounds that account is the provider's
// own limit, which is the operator's stated intent rather than an omission.
func (c Config) withinBudget(alias string, spentUSD map[string]float64) bool {
	budget := c.Accounts[alias].WeeklyBudgetUSD
	if budget <= 0 {
		return true
	}
	return spentUSD[alias] < budget
}

func describeAliases(aliases []string) string {
	if len(aliases) == 0 {
		return fmt.Sprintf("none, so it runs under %q", DefaultAccountAlias)
	}
	quoted := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		quoted = append(quoted, fmt.Sprintf("%q", alias))
	}
	return strings.Join(quoted, ", ")
}

func (c Config) describeBudgets(aliases []string, spentUSD map[string]float64) string {
	described := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		described = append(described, fmt.Sprintf("%s spent %.2f of %.2f",
			alias, spentUSD[alias], c.Accounts[alias].WeeklyBudgetUSD))
	}
	return strings.Join(described, "; ")
}

// accountProblems reports what makes the accounts mapping, and the assignments
// of roles to it, unusable.
func (c Config) accountProblems() []string {
	var problems []string
	aliases := c.AccountAliases()
	for _, alias := range aliases {
		if err := domain.ValidateIdentifier("account alias", alias); err != nil {
			problems = append(problems, err.Error())
		}
		account := c.Accounts[alias]
		if description := account.Description; len(description) > MaxAccountDescriptionBytes {
			problems = append(problems, fmt.Sprintf("accounts.%s.description is %d bytes, limit is %d", alias, len(description), MaxAccountDescriptionBytes))
		}
		if !account.Pool.Valid() {
			problems = append(problems, fmt.Sprintf("accounts.%s.pool is %q, and an account is %q or %q", alias, account.Pool, PoolActive, PoolReserved))
		}
		// A negative budget is not a stricter budget: it is a number no spend can
		// be under, which takes the account out of the pool while reading as a
		// limit on it. Nothing is said about zero, which is the same thing said
		// deliberately — an account budgeted at nothing is one the operator has
		// stood down without removing.
		if account.WeeklyBudgetUSD < 0 {
			problems = append(problems, fmt.Sprintf("accounts.%s.weekly_budget_usd cannot be negative", alias))
		}
	}
	// A pool with nothing in its active half is a pool nothing rotates: every run
	// would fall through to the reserved half, which is the opposite of reserving
	// it. It is refused here rather than met as a run that could not choose.
	if len(aliases) > 0 && len(c.ActiveAccountAliases()) == 0 {
		problems = append(problems, fmt.Sprintf("accounts declares %d account(s) and every one of them is %q; a pool the round-robin cannot serve from is one every run falls out of",
			len(aliases), PoolReserved))
	}

	// What an agent may name is what the mapping declares, or the default alias
	// where it declares nothing: a configuration that names no account still has
	// one, and an agent that says so out loud is saying what is true.
	declared := make(map[string]struct{}, len(aliases)+1)
	for _, alias := range aliases {
		declared[alias] = struct{}{}
	}
	if len(aliases) == 0 {
		declared[DefaultAccountAlias] = struct{}{}
	}
	for _, name := range sortedNames(c.Agents) {
		account := strings.TrimSpace(c.Agents[name].Account)
		if account == "" {
			// An agent that names no account is assigned the configuration's single
			// one while it is resolved, and runs under it either way. Under a pooled
			// configuration there is no single one to assign, and the pool is what
			// chooses — so an empty value here is the ordinary shape of a pooled
			// project rather than something missing.
			continue
		}
		if _, known := declared[account]; !known {
			problems = append(problems, fmt.Sprintf("agent %q runs under account %q, which the accounts mapping does not declare", name, account))
		}
	}
	return problems
}
