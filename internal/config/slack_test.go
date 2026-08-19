package config

import (
	"strings"
	"testing"
)

// A project that enables reporting and names nowhere to report to is refused at
// load, before any work is claimed. The alternative is a sink that starts,
// reads a stream, and only then discovers it has nowhere to post — by which
// time the operator has stopped watching it.
func TestSlackReportingWithNoChannelIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`slack:
  enabled: true
`, nil)
	if err == nil || !strings.Contains(err.Error(), "slack.channel is required") {
		t.Fatalf("LoadResolved() error = %v, want the missing channel named", err)
	}
}

// A channel or a user id that names nothing the workspace has is a typo, and a
// typo found at load is one an operator fixes now rather than one that becomes a
// posting failure every few seconds for as long as the sink runs.
func TestSlackIdentifiersAreCheckedWhetherOrNotReportingIsOn(t *testing.T) {
	t.Parallel()

	for name, section := range map[string]string{
		"a channel that is not one": `slack:
  channel: "not a channel"
`,
		"an operator that is a display name": `slack:
  enabled: true
  channel: C0123456789
  operators:
    - "@mason"
`,
		"an empty operator": `slack:
  enabled: true
  channel: C0123456789
  operators:
    - ""
`,
	} {
		if _, err := loadProjectError(t, minimalProjectConfig+section, nil); err == nil {
			t.Errorf("LoadResolved() with %s = nil, want a refusal", name)
		}
	}
}

// The whole section is optional, and a project that never mentions it reports
// nothing rather than failing to load — which is every project until one opts
// in.
func TestAProjectThatSaysNothingAboutSlackReportsNothing(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if resolved.Config.Slack.Enabled || resolved.Config.Slack.Channel != "" {
		t.Fatalf("slack = %#v, want a project that never mentioned it to report nothing", resolved.Config.Slack)
	}
}

// The allow-list decides who may steer the harness from a chat workspace. A
// list silently concatenated from two layers is not the list either layer
// wrote, so an override replaces it outright — the same rule the checks follow,
// and for a stronger reason.
func TestTheOperatorAllowListIsReplacedRatherThanMerged(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`slack:
  enabled: true
  channel: "#development"
  operators:
    - U01234567
    - W76543210
`, nil)
	settings := resolved.Config.Slack
	if !settings.Enabled || settings.Channel != "#development" {
		t.Fatalf("slack = %#v, want the configured channel", settings)
	}
	if len(settings.Operators) != 2 {
		t.Fatalf("operators = %#v, want both named operators", settings.Operators)
	}
	if origin := resolved.Origins["slack.operators"]; origin == "" {
		t.Fatal("the allow-list must record where it came from, like every other configured value")
	}
}
