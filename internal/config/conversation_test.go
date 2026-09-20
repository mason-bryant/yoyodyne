package config

import (
	"fmt"
	"strings"
	"testing"
)

// A project that says nothing about its conversations still has their picture
// measured against a threshold, attributed to the harness rather than to a
// layer that never mentioned it. It matters most where nobody thought about
// it: the case that admitted this was a product manager advising from a picture
// five hundred landings old in a project that had configured nothing.
func TestRefreshAfterLandingsDefaultsWhenAbsent(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	if got := resolved.Config.Conversation.RefreshAfterLandings; got != DefaultRefreshAfterLandings {
		t.Errorf("refresh_after_landings = %d, want %d", got, DefaultRefreshAfterLandings)
	}
	if got := resolved.Origins["conversation.refresh_after_landings"]; got != OriginDefault {
		t.Errorf("origin = %q, want %q", got, OriginDefault)
	}
}

func TestRefreshAfterLandingsResolvesFromEveryLayer(t *testing.T) {
	t.Parallel()

	if inherited := loadProject(t, minimalProjectConfig, nil).Config.Conversation.RefreshAfterLandings; inherited != DefaultRefreshAfterLandings {
		t.Fatalf("inherited refresh_after_landings = %d, want the harness default", inherited)
	}
	resolved := loadProject(t, minimalProjectConfig+`conversation:
  refresh_after_landings: 50
`, nil)
	if got := resolved.Config.Conversation.RefreshAfterLandings; got != 50 {
		t.Fatalf("overridden refresh_after_landings = %d, want 50", got)
	}
	if origin := resolved.Origins["conversation.refresh_after_landings"]; origin != resolved.Path {
		t.Fatalf("origin = %q, want %q", origin, resolved.Path)
	}

	// A generated project writes the threshold down rather than inheriting it,
	// so an operator reading their own configuration finds it and what bounds
	// it.
	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	if got := generated.Config.Conversation.RefreshAfterLandings; got != DefaultRefreshAfterLandings {
		t.Errorf("generated refresh_after_landings = %d, want %d", got, DefaultRefreshAfterLandings)
	}
	if origin := generated.Origins["conversation.refresh_after_landings"]; origin == OriginDefault {
		t.Error("the generated configuration inherited the threshold rather than stating it")
	}
}

// The threshold times the re-read and cannot turn it off. Zero would re-read on
// every turn, a negative number describes no age anything reaches, and a
// number past the harness's bound is the configuration disabling the statement
// it is only meant to time — so all three are refused rather than read as
// choices.
func TestRefreshAfterLandingsIsBoundedBothWays(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		value string
		want  string
	}{
		{value: "0", want: "conversation.refresh_after_landings must be at least 1"},
		{value: "-1", want: "conversation.refresh_after_landings must be at least 1"},
		{value: fmt.Sprint(MaxRefreshAfterLandings + 1), want: fmt.Sprintf("conversation.refresh_after_landings must be at most %d", MaxRefreshAfterLandings)},
	} {
		_, err := DecodeResolved(strings.NewReader(validBootstrapConfig + `
conversation:
  refresh_after_landings: ` + testCase.value + `
`))
		if err == nil {
			t.Fatalf("refresh_after_landings %s was accepted", testCase.value)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("refresh_after_landings %s error = %v, want %q", testCase.value, err, testCase.want)
		}
	}
	// The bound itself is a value, not a refusal.
	if _, err := DecodeResolved(strings.NewReader(validBootstrapConfig + `
conversation:
  refresh_after_landings: ` + fmt.Sprint(MaxRefreshAfterLandings) + `
`)); err != nil {
		t.Fatalf("refresh_after_landings at the bound was refused: %v", err)
	}
}
