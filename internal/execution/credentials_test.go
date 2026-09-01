package execution

// What an environment built here may not carry.
//
// The sink-only credential model is worth exactly what it can be shown to be
// worth, and what is asserted here is the half an operator cannot undo: whatever
// the process the harness was started from holds, the environment handed on does
// not hold the two Slack tokens.

import (
	"slices"
	"testing"
)

func TestTheSinkCredentialsAreRemovedFromAGivenEnvironment(t *testing.T) {
	t.Parallel()

	environment := WithoutSinkCredentials([]string{
		"PATH=/usr/bin",
		SlackBotTokenVariable + "=xoxb-secret",
		"HOME=/home/somebody",
		SlackAppTokenVariable + "=xapp-secret",
	})

	want := []string{"PATH=/usr/bin", "HOME=/home/somebody"}
	if !slices.Equal(environment, want) {
		t.Fatalf("WithoutSinkCredentials() = %v, want %v", environment, want)
	}
}

// The case this exists for: a command given no environment inherits this
// process's, so an exported pair reaches it without anybody having passed it.
func TestAnInheritedEnvironmentIsStrippedRatherThanPassedOnWhole(t *testing.T) {
	t.Setenv(SlackBotTokenVariable, "xoxb-secret")
	t.Setenv(SlackAppTokenVariable, "xapp-secret")
	t.Setenv("YOYO_CREDENTIALS_TEST_MARKER", "kept")

	environment := WithoutSinkCredentials(nil)

	for _, entry := range environment {
		if entry == SlackBotTokenVariable+"=xoxb-secret" || entry == SlackAppTokenVariable+"=xapp-secret" {
			t.Fatalf("an inherited environment carried %q", entry)
		}
	}
	// The rest of the environment has to survive, or what is handed on is not an
	// environment at all.
	if !slices.Contains(environment, "YOYO_CREDENTIALS_TEST_MARKER=kept") {
		t.Fatalf("the inherited environment lost what was not a credential: %v", environment)
	}
}

// A variable whose name merely begins with one of the two is somebody else's,
// and dropping it would be this quietly deciding what other programs may read.
func TestOnlyTheTwoNamedVariablesAreRemoved(t *testing.T) {
	t.Parallel()

	environment := WithoutSinkCredentials([]string{
		SlackBotTokenVariable + "_FILE=/run/secrets/bot",
		"MY_" + SlackAppTokenVariable + "=elsewhere",
	})

	if len(environment) != 2 {
		t.Fatalf("WithoutSinkCredentials() = %v, want both entries kept", environment)
	}
}
