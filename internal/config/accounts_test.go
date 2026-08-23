package config

import (
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

// Pooling is what a second alias will be, and it does not exist yet: a project
// that declared two accounts would have every run recording one of them while
// both were being spent.
func TestASecondAccountIsRefusedUntilPoolingExists(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(accountConfig(`accounts:
  work: {}
  personal: {}
`)))
	if err == nil {
		t.Fatal("Decode() error = nil, want two accounts refused")
	}
	if !strings.Contains(err.Error(), "v1 runs one") {
		t.Fatalf("Decode() error = %v, want the refusal to name what is not implemented yet", err)
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
