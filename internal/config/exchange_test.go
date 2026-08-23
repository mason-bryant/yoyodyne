package config

import (
	"strings"
	"testing"
)

// A project that says nothing about the ask channel still gets a limit on it,
// attributed to the harness rather than to a layer that never mentioned it. The
// limit matters most where nobody thought about it: two roles deferring to each
// other for ever is not something an operator configures against in advance.
func TestExchangeRoundsDefaultWhenAbsent(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if got := resolved.Config.Exchange.MaxRounds; got != 10 {
		t.Errorf("max_rounds = %d, want 10", got)
	}
	if got := resolved.Origins["exchange.max_rounds"]; got != OriginDefault {
		t.Errorf("origin = %q, want %q", got, OriginDefault)
	}
}

func TestExchangeRoundsResolveFromEveryLayer(t *testing.T) {
	t.Parallel()

	if inherited := loadProject(t, minimalProjectConfig, nil).Config.Exchange.MaxRounds; inherited != 10 {
		t.Fatalf("inherited max_rounds = %d, want the harness default", inherited)
	}
	resolved := loadProject(t, minimalProjectConfig+`exchange:
  max_rounds: 3
`, nil)
	if got := resolved.Config.Exchange.MaxRounds; got != 3 {
		t.Fatalf("overridden max_rounds = %d, want 3", got)
	}
	if origin := resolved.Origins["exchange.max_rounds"]; origin != resolved.Path {
		t.Fatalf("origin = %q, want %q", origin, resolved.Path)
	}

	// A generated project writes the limit down rather than inheriting it, so an
	// operator reading their own configuration finds it.
	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	if got := generated.Config.Exchange.MaxRounds; got != 10 {
		t.Errorf("generated max_rounds = %d, want 10", got)
	}
	if origin := generated.Origins["exchange.max_rounds"]; origin == OriginDefault {
		t.Error("the generated configuration inherited the limit rather than stating it")
	}
}

// Zero is refused rather than read as a choice. An exchange allowed no round is
// a channel that is off, and turning the channel off is leaving it unused rather
// than configuring a limit nothing can be spent against.
func TestAnExchangeMustBeAllowedAtLeastOneRound(t *testing.T) {
	t.Parallel()

	for _, rounds := range []string{"0", "-1"} {
		_, err := DecodeResolved(strings.NewReader(validBootstrapConfig + `
exchange:
  max_rounds: ` + rounds + `
`))
		if err == nil {
			t.Fatalf("max_rounds %s was accepted", rounds)
		}
		if !strings.Contains(err.Error(), "exchange.max_rounds must be at least 1") {
			t.Fatalf("max_rounds %s error = %v", rounds, err)
		}
	}
}
