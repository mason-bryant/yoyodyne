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

// A channel that names nothing the workspace has is a typo, and a typo found at
// load is one an operator fixes now rather than one that becomes a posting
// failure every few seconds for as long as the sink runs.
func TestSlackIdentifiersAreCheckedWhetherOrNotReportingIsOn(t *testing.T) {
	t.Parallel()

	for name, section := range map[string]string{
		"a channel that is not one": `slack:
  channel: "not a channel"
`,
		"a channel longer than the limit": `slack:
  enabled: true
  channel: "` + strings.Repeat("C", MaxSlackChannelBytes+1) + `"
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

// The Slack section says where to report and nothing about who may steer the
// harness. That moved to the top-level operators mapping, so a file that still
// authors an allow-list here is refused rather than loading with an authority
// decision nothing reads. The refusal has to say where the decision went: the
// setup guide told operators to write this key, so the people who hit this are
// the ones who followed the documentation.
func TestAnAllowListLeftUnderSlackIsRefusedAndSaysWhereItWent(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
  operators:
    - U01234567
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() = nil, want a file still carrying slack.operators refused")
	}
	for _, wanted := range []string{
		"slack.operators has moved",
		"operators:",
		"slack_member_id",
		string(GrantDirectWork),
	} {
		if !strings.Contains(err.Error(), wanted) {
			t.Errorf("LoadResolved() error = %v, want it to name %q", err, wanted)
		}
	}
}

// The migration answer is for the retired key alone. Every other unknown key
// still gets the decoder's own report, which names the line it is on.
func TestAnOrdinaryUnknownKeyStillReportsItself(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
  operatrs:
    - U01234567
`, nil)
	if err == nil || strings.Contains(err.Error(), "has moved") {
		t.Fatalf("LoadResolved() error = %v, want a typo reported as an unknown field", err)
	}
}

// What the sink reads instead: the ordinary settings resolve, and the allow-list
// comes from the grants rather than from anything written here.
func TestTheSlackSectionCarriesAddressingAndNotAuthority(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`slack:
  enabled: true
  channel: "#development"
`, nil)
	settings := resolved.Config.Slack
	if !settings.Enabled || settings.Channel != "#development" {
		t.Fatalf("slack = %#v, want the configured channel", settings)
	}
	if allowed := resolved.Config.SlackOperators(); len(allowed) != 0 {
		t.Fatalf("SlackOperators() = %#v, want nobody until a human is granted direct-work", allowed)
	}
}
