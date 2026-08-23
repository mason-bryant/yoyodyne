package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Account is one provider account the harness runs agents under, named by the
// alias the operator gives it.
//
// V1 runs exactly one, and every role is assigned to it. What is deliberately
// absent from an entry is anything about how the account is reached: the
// harness invokes the provider with the credentials the process already has, so
// a key naming a credential here would be configuration nothing reads and a
// promise nothing keeps. What an entry is for is the name — every run record
// says which account it ran under and every surface says it back — so pooling
// work across accounts arrives as a second alias and a rule for choosing
// between them, rather than as a change to the shape of everything recorded
// between now and then.
type Account struct {
	// Description is whose account this is, in the operator's own words. It is
	// optional because an alias is usually the whole of what somebody needs, and
	// it is the one thing here a reader of the configuration cannot derive from
	// the alias itself.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// DefaultAccountAlias is the account a project that names none runs under. A
// project with one account has nothing to distinguish, so it writes nothing and
// its records still say which account they ran under — which is what makes the
// single-account case read naturally rather than as an abstraction being
// carried.
const DefaultAccountAlias = "default"

// MaxAccountDescriptionBytes bounds one account's description so it stays a
// phrase naming whose account it is rather than a document in a configuration
// every surface reads back.
const MaxAccountDescriptionBytes = 200

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

// AccountAlias is the account a run made under this configuration executes on.
// V1 configures one account and assigns every role to it, so a run has exactly
// one alias to record: the configured one, or the default where nothing named
// any. A configuration that somehow reached here with more than one names none,
// because guessing which of them a run used is exactly the claim this record
// exists to make honestly — and validation refuses that configuration before a
// run is ever made under it.
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

// accountProblems reports what makes the accounts mapping, and the assignments
// of roles to it, unusable.
func (c Config) accountProblems() []string {
	var problems []string
	aliases := c.AccountAliases()
	for _, alias := range aliases {
		if err := domain.ValidateIdentifier("account alias", alias); err != nil {
			problems = append(problems, err.Error())
		}
		if description := c.Accounts[alias].Description; len(description) > MaxAccountDescriptionBytes {
			problems = append(problems, fmt.Sprintf("accounts.%s.description is %d bytes, limit is %d", alias, len(description), MaxAccountDescriptionBytes))
		}
	}
	// More than one account is refused here rather than in the agents below,
	// because what is missing is not a key: v1 executes one configured account,
	// and a project that declared two would have every run recording an alias
	// while both accounts were being spent. Pooling is what lifts this, and it
	// lifts exactly this — the mapping, the per-agent assignment, and everything
	// recorded from them are already the shape it needs.
	if len(aliases) > 1 {
		problems = append(problems, fmt.Sprintf("accounts declares %d accounts (%s), and v1 runs one: every role is assigned to the single configured account, and pooling work across several is not implemented yet",
			len(aliases), strings.Join(aliases, ", ")))
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
			// one while it is resolved, and runs under it either way: what a run
			// records is the configuration's account rather than each agent's. An
			// empty value here is an agent nothing could assign, which is a
			// configuration declaring more than one account — already reported above.
			continue
		}
		if _, known := declared[account]; !known {
			problems = append(problems, fmt.Sprintf("agent %q runs under account %q, which the accounts mapping does not declare", name, account))
		}
	}
	return problems
}
