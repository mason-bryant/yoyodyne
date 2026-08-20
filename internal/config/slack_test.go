package config

import (
	"reflect"
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

// The picture beside a name is the project's to choose, in either shape Slack
// takes it: a shortcode — including a custom one only this workspace has — or an
// image it fetches. A speaker the project said nothing about is absent rather
// than blank, so the sink keeps the avatar the harness ships for it.
func TestAvatarsAreConfigurableAsShortcodesOrImages(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
  avatars:
    harness: ":ship-it:"
    developer: ":wave::skin-tone-3:"
    reviewer: https://example.invalid/faces/reviewer.png
`, nil)
	avatars := resolved.Config.Slack.Avatars
	for speaker, want := range map[string]string{
		SlackHarnessAvatar: ":ship-it:",
		"developer":        ":wave::skin-tone-3:",
		"reviewer":         "https://example.invalid/faces/reviewer.png",
	} {
		if avatars[speaker] != want {
			t.Errorf("slack.avatars[%q] = %q, want %q", speaker, avatars[speaker], want)
		}
	}
	if _, named := avatars["architect"]; named {
		t.Errorf("slack.avatars = %#v, want a speaker nobody configured left to its shipped default", avatars)
	}
}

// An avatar Slack cannot render is refused at load. It has to be, because
// nothing downstream will say so: Slack accepts an unknown shortcode or an
// unreachable image without complaint and quietly shows the app's own icon, so
// the only symptom would be a picture nobody notices is the wrong one.
func TestAnAvatarThatIsNotOneIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	for name, section := range map[string]struct{ avatars, want string }{
		"a speaker that is not a role": {avatars: "  avatars:\n    dev-manager: \":clipboard:\"\n", want: "is not a role"},
		"a bare word":                  {avatars: "  avatars:\n    developer: hammer\n", want: "emoji shortcode"},
		"a shortcode missing a colon":  {avatars: "  avatars:\n    developer: \":hammer\"\n", want: "emoji shortcode"},
		"an image served in the clear": {avatars: "  avatars:\n    developer: http://example.invalid/dev.png\n", want: "emoji shortcode"},
		"an empty override":            {avatars: "  avatars:\n    developer: \"\"\n", want: "leave it out"},
		"an avatar past the limit": {
			avatars: "  avatars:\n    developer: \"https://example.invalid/" + strings.Repeat("a", MaxSlackAvatarBytes) + "\"\n",
			want:    "limit is",
		},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
`+section.avatars, nil)
		if err == nil {
			t.Errorf("LoadResolved() with %s = nil, want a refusal", name)
			continue
		}
		if !strings.Contains(err.Error(), section.want) {
			t.Errorf("LoadResolved() with %s error = %v, want it to say %q", name, err, section.want)
		}
	}
}

// Avatars are checked whether or not reporting is switched on, like the channel
// beside them and for the same reason: a project that configured the section and
// has not opted in yet learns about a typo now rather than on the day it does.
func TestAvatarsAreCheckedWhetherOrNotReportingIsOn(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`slack:
  avatars:
    developer: hammer
`, nil)
	if err == nil || !strings.Contains(err.Error(), "slack.avatars.developer") {
		t.Fatalf("LoadResolved() error = %v, want the avatar checked before reporting is on", err)
	}
}

// A layer that names one speaker's avatar says nothing about any other, so the
// entries merge rather than replacing each other wholesale — the way agents do,
// and unlike the check list. Each avatar is one speaker's decoration and
// independent of the rest, so restating an inherited one to keep it would be
// restating it for no reason.
func TestAnOverriddenAvatarLeavesTheOthersInherited(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: slackAvatarDocument(map[string]string{
			"harness":   ":gear:",
			"developer": ":hammer_and_wrench:",
		})},
		{origin: "project", document: slackAvatarDocument(map[string]string{
			"developer": ":ship-it:",
			"reviewer":  ":eyes:",
		})},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	want := map[string]string{"harness": ":gear:", "developer": ":ship-it:", "reviewer": ":eyes:"}
	if got := resolved.Config.Slack.Avatars; !reflect.DeepEqual(got, want) {
		t.Fatalf("slack.avatars = %#v, want %#v", got, want)
	}
	// Where each one came from is reported per speaker, so an operator asking
	// why a persona looks like that is answered about that persona.
	for speaker, origin := range map[string]string{"harness": "bundle", "developer": "project", "reviewer": "project"} {
		if got := resolved.Origins["slack.avatars."+speaker]; got != origin {
			t.Errorf("origin of slack.avatars.%s = %q, want %q", speaker, got, origin)
		}
	}
}

func slackAvatarDocument(avatars map[string]string) configDocument {
	version := CurrentVersion
	return configDocument{Version: &version, Slack: &slackDocument{Avatars: &avatars}}
}

// Names and attribution stay out of configuration. There is no key for either,
// so a file that tries to rename a speaker is refused by the decoder rather than
// loading with an attribution nothing governs: who speaks is a claim about who
// did the work.
func TestWhoSpeaksIsNotConfigurable(t *testing.T) {
	t.Parallel()

	for _, section := range []string{
		"  names:\n    developer: The Machine\n",
		"  speakers:\n    developer: architect\n",
	} {
		if _, err := loadProjectError(t, minimalProjectConfig+`slack:
  enabled: true
  channel: C0123456789
`+section, nil); err == nil {
			t.Errorf("LoadResolved() with %q = nil, want attribution to stay out of configuration", section)
		}
	}
}
