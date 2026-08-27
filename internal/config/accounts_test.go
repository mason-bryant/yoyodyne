package config

import (
	"path/filepath"
	"strings"
	"testing"
)

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

// A second alias is what pools the work, and it loads. The two halves are read
// back as the operator wrote them, and an account that said nothing about which
// half it is in is active — because an operator who adds an account and says
// nothing else has said they want it used.
func TestASecondAccountPoolsTheWork(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(accountConfig(`accounts:
  work: {}
  personal:
    pool: active
  spare:
    pool: reserved
    weekly_budget_usd: 40
`)))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	cfg := resolved.Config
	if !cfg.Pooled() {
		t.Fatal("Pooled() = false for a configuration naming three accounts")
	}
	if active := cfg.ActiveAccountAliases(); len(active) != 2 || active[0] != "personal" || active[1] != "work" {
		t.Fatalf("ActiveAccountAliases() = %v, want personal and work in that order", active)
	}
	if reserved := cfg.ReservedAccountAliases(); len(reserved) != 1 || reserved[0] != "spare" {
		t.Fatalf("ReservedAccountAliases() = %v, want spare alone", reserved)
	}
	// A pooled configuration names no single account, because which one a run
	// spent is what the pool decided rather than what the file says.
	if alias := cfg.AccountAlias(); alias != "" {
		t.Fatalf("AccountAlias() = %q, want no single account under a pool", alias)
	}
	if budget := cfg.Accounts["spare"].WeeklyBudgetUSD; budget != 40 {
		t.Fatalf("spare weekly_budget_usd = %v, want 40", budget)
	}
}

// The rotation takes the account after the one the last run recorded, which is
// what spreads runs across the pool rather than pinning them to whichever alias
// sorts first. The run records are the cursor, so nothing else has to be kept.
func TestTheActivePoolIsRotatedFromTheAccountTheLastRunSpent(t *testing.T) {
	t.Parallel()

	// Alphabetical order is one, three, two.
	cfg := mustDecodeConfig(t, accountConfig("accounts:\n  one: {}\n  two: {}\n  three: {}\n"))
	for _, rotation := range []struct {
		lastServed string
		want       string
	}{
		{lastServed: "", want: "one"},
		{lastServed: "one", want: "three"},
		{lastServed: "three", want: "two"},
		{lastServed: "two", want: "one"},
		// An alias the pool no longer holds leaves the order as it stands, which
		// starts the rotation from the top rather than from nowhere.
		{lastServed: "retired", want: "one"},
	} {
		chosen, err := cfg.ChooseAccount("", rotation.lastServed, nil)
		if err != nil {
			t.Fatalf("ChooseAccount(%q) error = %v", rotation.lastServed, err)
		}
		if chosen.Alias != rotation.want {
			t.Errorf("after %q the pool chose %q, want %q", rotation.lastServed, chosen.Alias, rotation.want)
		}
	}
}

// A budget is what stands an account down, the reserved half is what a run falls
// back to when the active half has nothing left to spend, and a pool with
// nothing left anywhere refuses rather than spending past what was budgeted.
func TestAnAccountThatHasSpentItsWeeklyBudgetIsPassedOver(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig(`accounts:
  one:
    weekly_budget_usd: 10
  two:
    weekly_budget_usd: 10
  spare:
    pool: reserved
`))
	spent := map[string]float64{"one": 10.5}
	chosen, err := cfg.ChooseAccount("", "", spent)
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "two" {
		t.Fatalf("the pool chose %q, want the active account still under its budget", chosen.Alias)
	}
	spent["two"] = 99
	chosen, err = cfg.ChooseAccount("", "", spent)
	if err != nil {
		t.Fatalf("ChooseAccount() error = %v", err)
	}
	if chosen.Alias != "spare" {
		t.Fatalf("the pool chose %q, want the reserved account", chosen.Alias)
	}
	budgeted := mustDecodeConfig(t, accountConfig(`accounts:
  one:
    weekly_budget_usd: 10
  two:
    weekly_budget_usd: 10
`))
	if _, err := budgeted.ChooseAccount("", "", spent); err == nil {
		t.Fatal("ChooseAccount() error = nil, want a pool with nothing left to spend refused")
	}
}

// Where an alias authenticates follows from the alias, so the harness, the
// diagnosis, and the setup script cannot disagree about it. The default alias is
// wherever the machine is already signed in, which is what keeps a second
// account additive: the account that was there keeps the login it had.
func TestAnAliasResolvesToItsOwnProviderHomeAndTheDefaultToTheMachines(t *testing.T) {
	t.Parallel()

	if directory := AccountConfigDirectory("/state", DefaultAccountAlias); directory != "" {
		t.Fatalf("the default alias resolved to %q, want the machine's own provider home", directory)
	}
	want := filepath.Join("/state", "accounts", "second")
	if directory := AccountConfigDirectory("/state", "second"); directory != want {
		t.Fatalf("AccountConfigDirectory() = %q, want %q", directory, want)
	}
	cfg := mustDecodeConfig(t, accountConfig("accounts:\n  default: {}\n  second: {}\n"))
	endpoint, err := cfg.Endpoint("/state", "second")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if endpoint.Alias != "second" || endpoint.Directory != want {
		t.Fatalf("Endpoint() = %+v, want second at %q", endpoint, want)
	}
	// An alias nothing declares resolves to nowhere rather than to a home nobody
	// chose: an invocation made there would authenticate as whoever happened to
	// have signed in.
	if _, err := cfg.Endpoint("/state", "third"); err == nil {
		t.Fatal("Endpoint() error = nil, want an undeclared alias refused")
	}
}

// A project with one account authenticates where the machine already does,
// whatever that account is called. Only a pool gives an alias a home of its own,
// which is the whole of what makes a second account additive: a project that
// called its single account "work" is not quietly moved to a directory nobody
// has ever signed in to.
func TestALoneAccountAuthenticatesWhereTheMachineDoesWhateverItIsCalled(t *testing.T) {
	t.Parallel()

	lone := mustDecodeConfig(t, accountConfig("accounts:\n  work: {}\n"))
	endpoint, err := lone.Endpoint("/state", "work")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if endpoint.Directory != "" {
		t.Fatalf("a lone account resolved to %q, want the machine's own provider home", endpoint.Directory)
	}
	// Declaring the second account is what moves it, and that is a deliberate act
	// with `yoyo doctor` on the other side of it naming every alias that now
	// needs a login.
	pooled := mustDecodeConfig(t, accountConfig("accounts:\n  work: {}\n  spare: {}\n"))
	endpoint, err = pooled.Endpoint("/state", "work")
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if want := filepath.Join("/state", "accounts", "work"); endpoint.Directory != want {
		t.Fatalf("under a pool %q authenticates in %q, want %q", endpoint.Alias, endpoint.Directory, want)
	}
}

func TestAnAccountAliasDescriptionPoolAndBudgetAreHeldToAShape(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		name  string
		layer string
	}{
		{name: "an alias nothing could address", layer: "accounts:\n  Work Account: {}\n"},
		{name: "a description that is a document", layer: "accounts:\n  work:\n    description: " + strings.Repeat("x", MaxAccountDescriptionBytes+1) + "\n"},
		{name: "a pool half that does not exist", layer: "accounts:\n  work:\n    pool: standby\n"},
		{name: "a budget no spend could be under", layer: "accounts:\n  work:\n    weekly_budget_usd: -1\n"},
		// A pool the round-robin cannot serve from is one every run falls out of.
		{name: "nothing in the active half", layer: "accounts:\n  work:\n    pool: reserved\n"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Decode(strings.NewReader(accountConfig(refused.layer))); err == nil {
				t.Fatal("Decode() error = nil, want the account refused")
			}
		})
	}
}

func mustDecodeConfig(t *testing.T, source string) Config {
	t.Helper()
	decoded, err := Decode(strings.NewReader(source))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return decoded
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
