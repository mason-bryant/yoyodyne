package config

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/research"
)

// The capability is off until an operator names a source. That is the important
// default: a conversational role reaching the network is something they turn on
// deliberately, naming what it may reach, rather than something they acquire by
// extending a bundle or upgrading the executable.
func TestAProjectThatNamesNoSourcePermitsNoResearch(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Research.Policy().Enabled() {
		t.Fatalf("a file naming no source permits research: %#v", cfg.Research)
	}
}

func TestConfiguredSourcesReachTheCapabilityWithTheirBounds(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + `research:
  max_queries_per_turn: 2
  timeout: 30s
  sources:
    - name: web
      command: search-the-web
      describes: public web search
`
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	policy := cfg.Research.Policy()
	if !policy.Enabled() {
		t.Fatal("a configured source permits nothing")
	}
	source, found := policy.Find("web")
	if !found || source.Command != "search-the-web" || source.Describes != "public web search" {
		t.Fatalf("Find(web) = %#v, %v", source, found)
	}
	if policy.QueryBudget() != 2 {
		t.Fatalf("QueryBudget() = %d, want the configured 2", policy.QueryBudget())
	}
	if policy.SourceTimeout() != 30*time.Second {
		t.Fatalf("SourceTimeout() = %s, want the configured 30s", policy.SourceTimeout())
	}
	// A project that named a source and nothing else gets the capability rather
	// than one it has to configure twice.
	bare := Research{Sources: []research.Source{{Name: "web", Command: "search"}}}
	if bare.Policy().QueryBudget() != research.DefaultMaxQueriesPerTurn || bare.Policy().SourceTimeout() != research.DefaultTimeout {
		t.Fatalf("an unstated bound did not take the harness default: %#v", bare.Policy())
	}
}

func TestResearchSettingsNobodyCouldAskThroughAreRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "a source with no command",
			input: `research:
  sources:
    - name: web
`,
			want: "command is required",
		},
		{
			name: "a source with no name",
			input: `research:
  sources:
    - command: search
`,
			want: "name is required",
		},
		{
			// Two sources of one name is a question whose destination depends on
			// which entry the harness found first, which is not a destination the
			// operator chose.
			name: "two sources of one name",
			input: `research:
  sources:
    - name: web
      command: search
    - name: web
      command: search-elsewhere
`,
			want: `research source "web" is named twice`,
		},
		{
			name: "a negative budget",
			input: `research:
  max_queries_per_turn: -1
  sources:
    - name: web
      command: search
`,
			want: "max_queries_per_turn cannot be negative",
		},
		{
			name: "a negative timeout",
			input: `research:
  timeout: -5s
  sources:
    - name: web
      command: search
`,
			want: "timeout cannot be negative",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decode(strings.NewReader(validBootstrapConfig + test.input))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}
