package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// mustDecodeConfig is a configuration a test has already decided is valid, so
// what each test below reads is the effective configuration rather than the
// decoding of one.
func mustDecodeConfig(t *testing.T, source string) Config {
	t.Helper()
	cfg, err := Decode(strings.NewReader(source))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return cfg
}

// accountConfig is the smallest configuration that loads, with whatever a test
// wants to say about accounts appended to it.
func accountConfig(extra string) string {
	return `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
` + extra
}

func TestAProjectThatNamesNoAccountRunsUnderTheDefaultOne(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(accountConfig("")))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if alias := resolved.Config.AccountAlias(); alias != DefaultAccountAlias {
		t.Fatalf("AccountAlias() = %q, want %q", alias, DefaultAccountAlias)
	}
	// The single-account case is meant to read naturally, which means the project
	// writes nothing and the agent is still assigned somewhere a record can name.
	if account := resolved.Config.Agents["developer"].Account; account != DefaultAccountAlias {
		t.Fatalf("developer account = %q, want %q", account, DefaultAccountAlias)
	}
	if origin := resolved.Origins["agents.developer.account"]; origin != OriginDerivedAccount {
		t.Fatalf("developer account origin = %q, want %q", origin, OriginDerivedAccount)
	}
	if origin := resolved.Origins["accounts"]; origin != OriginDefault {
		t.Fatalf("accounts origin = %q, want %q", origin, OriginDefault)
	}
}

func TestAConfiguredAccountNamesEveryAgentThatDidNotChooseOne(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(accountConfig(`accounts:
  work:
    description: the subscription this team pays for
`)))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if alias := resolved.Config.AccountAlias(); alias != "work" {
		t.Fatalf("AccountAlias() = %q, want work", alias)
	}
	if account := resolved.Config.Agents["developer"].Account; account != "work" {
		t.Fatalf("developer account = %q, want work", account)
	}
	if description := resolved.Config.Accounts["work"].Description; description != "the subscription this team pays for" {
		t.Fatalf("description = %q", description)
	}
}

func TestAnAgentMayNameTheAccountItRunsUnder(t *testing.T) {
	t.Parallel()

	config, err := Decode(strings.NewReader(`version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
accounts:
  work: {}
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: work
`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if account := config.Agents["developer"].Account; account != "work" {
		t.Fatalf("developer account = %q, want work", account)
	}
}

func TestAnAgentNamingAnAccountNothingDeclaresIsRefused(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(`version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
accounts:
  work: {}
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: personal
`))
	if err == nil {
		t.Fatal("Decode() error = nil, want the undeclared account refused")
	}
	if !strings.Contains(err.Error(), `agent "developer" runs under account "personal"`) {
		t.Fatalf("Decode() error = %v, want the agent and the account it named", err)
	}
}

// A second account is a pool rather than a refusal, and an agent that names none
// is served by the pool rather than assigned somewhere while it is resolved.
func TestASecondAccountPoolsTheWork(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(accountConfig(`accounts:
  work: {}
  personal: {}
`)))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if !resolved.Config.Pooled() {
		t.Fatal("Pooled() = false, want a project with two accounts to pool them")
	}
	// Both halves are named, and an account that said nothing about its pool is
	// active: an operator who adds a second account and says nothing else has
	// said they want both of them used.
	if active := resolved.Config.ActiveAccountAliases(); len(active) != 2 || active[0] != "personal" || active[1] != "work" {
		t.Fatalf("ActiveAccountAliases() = %v, want both accounts in alias order", active)
	}
	if reserved := resolved.Config.ReservedAccountAliases(); len(reserved) != 0 {
		t.Fatalf("ReservedAccountAliases() = %v, want none", reserved)
	}
	// The configuration names no single account, because there is not one, and
	// the agent is left unassigned for the pool to answer for as a run starts.
	if alias := resolved.Config.AccountAlias(); alias != "" {
		t.Fatalf("AccountAlias() = %q, want a pooled configuration to name none", alias)
	}
	if account := resolved.Config.Agents["developer"].Account; account != "" {
		t.Fatalf("developer account = %q, want the pool to choose rather than resolution", account)
	}
}

// A conversation belongs to its agent and lasts for weeks, so it sits on one
// account rather than rotating with the runs.
func TestAPooledAgentThatNamesNoAccountSitsOnTheFirstActiveOne(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig(`accounts:
  personal:
    pool: reserved
  work: {}
`))
	if alias := cfg.AgentAccountAlias("developer"); alias != "work" {
		t.Fatalf("AgentAccountAlias() = %q, want the first active account", alias)
	}
	// An agent that names one runs under that one, whichever half it is in.
	developer := cfg.Agents["developer"]
	developer.Account = "personal"
	cfg.Agents["developer"] = developer
	if alias := cfg.AgentAccountAlias("developer"); alias != "personal" {
		t.Fatalf("AgentAccountAlias() = %q, want the account the agent named", alias)
	}
}

// A pool the round-robin cannot serve from is one every run falls out of, so it
// is refused where it is written rather than met as a run that could not choose.
func TestAPoolWithNoActiveAccountIsRefused(t *testing.T) {
	t.Parallel()

	for name, layer := range map[string]string{
		"every account reserved": "accounts:\n  work:\n    pool: reserved\n  personal:\n    pool: reserved\n",
		"a half that is neither": "accounts:\n  work:\n    pool: standby\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(strings.NewReader(accountConfig(layer))); err == nil {
				t.Fatal("Decode() error = nil, want the accounts mapping refused")
			}
		})
	}
}

// Where an alias authenticates follows from the alias, and only under a pool: a
// project with one account keeps the login the machine already had, whatever
// that account is called. That is the whole of what makes a second account
// additive.
func TestAnAliasResolvesToItsOwnProviderHomeAndTheDefaultToTheMachines(t *testing.T) {
	t.Parallel()

	if directory := AccountConfigDirectory("/state", DefaultAccountAlias); directory != "" {
		t.Fatalf("the default alias resolved to %q, want the machine's own provider home", directory)
	}
	want := filepath.Join("/state", "accounts", "second")
	if directory := AccountConfigDirectory("/state", "second"); directory != want {
		t.Fatalf("AccountConfigDirectory() = %q, want %q", directory, want)
	}

	// A lone account authenticates where the machine does, whatever it is called.
	lone := mustDecodeConfig(t, accountConfig("accounts:\n  work: {}\n"))
	endpoint, err := lone.Endpoint("/state", "work")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if endpoint.Directory != "" {
		t.Fatalf("a lone account authenticates in %q, want the machine's own home", endpoint.Directory)
	}

	// Declaring the second account is what moves the first into a home of its own.
	pooled := mustDecodeConfig(t, accountConfig("accounts:\n  default: {}\n  second: {}\n"))
	endpoint, err = pooled.Endpoint("/state", "second")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if endpoint.Alias != "second" || endpoint.Directory != want {
		t.Fatalf("Endpoint() = %#v, want %q at %q", endpoint, "second", want)
	}
	// The default alias stays the machine's own home even under a pool, which is
	// what keeps the account that was already signed in untouched.
	if endpoint, err := pooled.Endpoint("/state", DefaultAccountAlias); err != nil || endpoint.Directory != "" {
		t.Fatalf("Endpoint(default) = %#v, %v, want the machine's own home", endpoint, err)
	}
	// An alias nothing declares is refused rather than resolved somewhere nobody
	// chose.
	if _, err := pooled.Endpoint("/state", "invented"); err == nil {
		t.Fatal("Endpoint() accepted an alias the configuration does not declare")
	}
}

// The rotation, the budgets, and the reserved half, over the configuration
// alone: what each account has spent is the caller's evidence to supply.
func TestTheActiveHalfIsRoundRobinedAndBudgetsStandAnAccountDown(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig(`accounts:
  one: {}
  two: {}
  spare:
    pool: reserved
`))

	// Nothing served yet starts the rotation at the top of the active half.
	chosen, err := cfg.ChooseAccount("/state", "", nil)
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "one" {
		t.Fatalf("ChooseAccount() = %q, want the first active account", chosen.Alias)
	}
	// The cursor is the last account served, so the next run takes the one after.
	if chosen, err = cfg.ChooseAccount("/state", "one", nil); err != nil || chosen.Alias != "two" {
		t.Fatalf("ChooseAccount(after one) = %q, %v, want two", chosen.Alias, err)
	}
	// And it wraps rather than running off the end.
	if chosen, err = cfg.ChooseAccount("/state", "two", nil); err != nil || chosen.Alias != "one" {
		t.Fatalf("ChooseAccount(after two) = %q, %v, want one", chosen.Alias, err)
	}
	// An alias the pool no longer holds leaves the order as it stands.
	if chosen, err = cfg.ChooseAccount("/state", "departed", nil); err != nil || chosen.Alias != "one" {
		t.Fatalf("ChooseAccount(after a removed alias) = %q, %v, want the top of the order", chosen.Alias, err)
	}

	// The reserved half is what a run falls back to once the active one has
	// nothing left to spend, and only then.
	budgeted := mustDecodeConfig(t, accountConfig(`accounts:
  one:
    weekly_budget_usd: 10
  two:
    weekly_budget_usd: 10
  spare:
    pool: reserved
`))
	spent := map[string]float64{"one": 10, "two": 4}
	if chosen, err = budgeted.ChooseAccount("/state", "", spent); err != nil || chosen.Alias != "two" {
		t.Fatalf("ChooseAccount() = %q, %v, want the active account still inside its budget", chosen.Alias, err)
	}
	spent["two"] = 12
	if chosen, err = budgeted.ChooseAccount("/state", "", spent); err != nil || chosen.Alias != "spare" {
		t.Fatalf("ChooseAccount() = %q, %v, want the reserved account", chosen.Alias, err)
	}
}

// A run served by an account the operator budgeted away is the one outcome a
// budget exists to prevent, so the pool refuses and names what each has spent.
func TestAPoolWithEveryBudgetSpentChoosesNothingAndSaysWhy(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig(`accounts:
  one:
    weekly_budget_usd: 10
  spare:
    pool: reserved
    weekly_budget_usd: 5
`))
	_, err := cfg.ChooseAccount("/state", "", map[string]float64{"one": 10, "spare": 5})
	if err == nil {
		t.Fatal("ChooseAccount() error = nil, want the spent budgets refused")
	}
	for _, want := range []string{"one spent 10.00 of 10.00", "spare spent 5.00 of 5.00"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ChooseAccount() error = %v, want it to name %q", err, want)
		}
	}
}

// An unbudgeted account is spent until the provider's own limit stops it, which
// is the operator's stated default rather than an oversight.
func TestAnUnbudgetedAccountIsNeverStoodDownForMoney(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig("accounts:\n  one: {}\n  two: {}\n"))
	if cfg.HasAccountBudgets() {
		t.Fatal("HasAccountBudgets() = true, want a project that budgeted nothing to say so")
	}
	chosen, err := cfg.ChooseAccount("/state", "", map[string]float64{"one": 10_000})
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "one" {
		t.Fatalf("ChooseAccount() = %q, want the unbudgeted account regardless of spend", chosen.Alias)
	}
}

// A budget of nothing and no budget at all are opposite instructions, and the
// number an operator types to stop spending is `0`. Reading it as "no limit" is
// the one mistake here that would put an account back in full rotation at the
// moment they meant to take it out, so the two are held apart deliberately.
func TestABudgetOfNothingStandsAnAccountDownAndAnAbsentOneDoesNot(t *testing.T) {
	t.Parallel()

	stoodDown := mustDecodeConfig(t, accountConfig("accounts:\n  one:\n    weekly_budget_usd: 0\n  two: {}\n"))
	if _, budgeted := stoodDown.Accounts["one"].Budgeted(); !budgeted {
		t.Fatal("a stated budget of 0 read as no budget at all")
	}
	if _, budgeted := stoodDown.Accounts["two"].Budgeted(); budgeted {
		t.Fatal("an account that stated no budget read as budgeted")
	}
	// Stating one anywhere is what makes the pool price the week at all, and a
	// zero has to count: it is the only budget that excludes without any spend.
	if !stoodDown.HasAccountBudgets() {
		t.Fatal("HasAccountBudgets() = false, want a stated budget of 0 to count as one")
	}
	// Nothing has been spent on either account, and the one budgeted at nothing is
	// still passed over — which is the whole of what standing it down means.
	chosen, err := stoodDown.ChooseAccount("/state", "", nil)
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "two" {
		t.Fatalf("ChooseAccount() = %q, want the account that was not stood down", chosen.Alias)
	}

	// With every account stood down there is nowhere to send a run, and the
	// refusal says so rather than serving one anyway.
	everyone := mustDecodeConfig(t, accountConfig("accounts:\n  one:\n    weekly_budget_usd: 0\n  two:\n    weekly_budget_usd: 0\n"))
	if _, err := everyone.ChooseAccount("/state", "", nil); err == nil {
		t.Fatal("ChooseAccount() error = nil, want a pool with every account stood down to refuse")
	}
}

// A negative budget says nothing a zero does not say more plainly, and reads as
// a limit while behaving as a removal, so it is refused where it is written.
func TestANegativeBudgetIsRefusedAndNamesTheZeroThatMeansIt(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(accountConfig("accounts:\n  work:\n    weekly_budget_usd: -1\n")))
	if err == nil {
		t.Fatal("Decode() error = nil, want a negative budget refused")
	}
	if !strings.Contains(err.Error(), "0 is how an account is stood down") {
		t.Fatalf("Decode() error = %v, want it to name what to write instead", err)
	}
}

func TestAnAccountAliasAndDescriptionAreHeldToAShape(t *testing.T) {
	t.Parallel()

	for name, layer := range map[string]string{
		"an alias nothing could address": "accounts:\n  Work Account: {}\n",
		"a description that is a document": "accounts:\n  work:\n    description: " +
			strings.Repeat("x", MaxAccountDescriptionBytes+1) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(strings.NewReader(accountConfig(layer))); err == nil {
				t.Fatal("Decode() error = nil, want the account refused")
			}
		})
	}
}

// An overriding layer states the accounts it has rather than adding to what it
// inherited, for the reason the operators mapping does: a set half from one
// layer and half from another is not the set either of them wrote.
func TestASuppliedAccountsMappingReplacesTheInheritedOne(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
accounts:
  inherited: {}
`)},
		{origin: "project", document: mustDecodeDocument(t, accountConfig(`accounts:
  work: {}
`))},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	if aliases := resolved.Config.AccountAliases(); len(aliases) != 1 || aliases[0] != "work" {
		t.Fatalf("AccountAliases() = %v, want only the project's own", aliases)
	}
	if origin := resolved.Origins["accounts"]; origin != "project" {
		t.Fatalf("accounts origin = %q, want project", origin)
	}
}

func mustDecodeDocument(t *testing.T, source string) configDocument {
	t.Helper()
	document, err := decodeDocument(strings.NewReader(source))
	if err != nil {
		t.Fatalf("decodeDocument() error = %v", err)
	}
	return document
}
