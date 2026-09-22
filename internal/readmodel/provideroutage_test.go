package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeOutages struct {
	outage runstate.ProviderOutage
	away   bool
	fail   error
}

func (f fakeOutages) Standing() (runstate.ProviderOutage, bool, error) {
	return f.outage, f.away, f.fail
}

var loginExpired = time.Date(2026, 9, 17, 15, 17, 0, 0, time.UTC)

func expiredLogin() runstate.ProviderOutage {
	return runstate.ProviderOutage{
		SchemaVersion: runstate.ProviderOutageSchemaVersion,
		ProductID:     "yoyodyne",
		Cause:         domain.ProviderUnauthenticated,
		Provider:      domain.BackendClaudeCode,
		AccountAlias:  "default",
		Since:         loginExpired,
		LastSeen:      loginExpired.Add(time.Hour),
		Refusals:      3,
		Waiting:       "the dispatch of yoyodyne-ifd.377",
	}
}

// The provider answering nobody is the banner above the four lines and an
// entry on the attention line naming the login, from the same reading `yoyo
// status` prints — and it says so over an empty queue too, because a login the
// operator has to renew is waiting on him whatever is in the queue.
func TestAnExpiredLoginIsTheBannerAndAnAttentionEntry(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.ProviderOutages = fakeOutages{outage: expiredLogin(), away: true}

	standing := ReadStanding(context.Background(), sources)
	if standing.ProviderOutage == nil || standing.ProviderOutage.Cause != domain.ProviderUnauthenticated {
		t.Fatalf("standing = %+v, want the outage carried", standing.ProviderOutage)
	}
	if !strings.HasPrefix(standing.Paused, "The provider is not authenticated; the operator must log in") {
		t.Fatalf("paused = %q, want the login as the banner", standing.Paused)
	}
	if !strings.HasPrefix(standing.Render(), standing.Paused) {
		t.Fatalf("render = %q, want the banner first", standing.Render())
	}
	found := 0
	for _, attention := range standing.NeedsHuman {
		if strings.Contains(attention.What(), "the operator must log in") && strings.Contains(attention.Whose(), "log in to the provider") {
			found++
		}
		if strings.Contains(attention.Whose(), "yoyo release") && strings.Contains(attention.What(), "provider") {
			t.Fatalf("needs a human = %+v, want no release prescribed for a wait it does not lift", attention)
		}
	}
	if found != 1 {
		t.Fatalf("needs a human = %+v, want the login exactly once as something waiting on the operator", standing.NeedsHuman)
	}
}

// Where the queue holds ready work, the outage is what stops it: every ready
// item is refused for it, said in the same words the attention line uses, and
// the stall taxonomy names it ahead of a full machine because the runs holding
// the slots are waiting on the same provider.
func TestAnExpiredLoginIsWhyNothingStarts(t *testing.T) {
	t.Parallel()

	stall := WhyNothingStarts(Conditions{
		ProviderOutage: expiredLogin(), ProviderAway: true,
		Running: 2, Capacity: 2,
	})
	if stall.Reason != ReasonProviderAway || !strings.HasPrefix(stall.Says, "The provider is not authenticated") {
		t.Fatalf("stall = %+v, want the outage named ahead of the full machine", stall)
	}
	if !stall.Since.Equal(loginExpired) {
		t.Fatalf("since = %s, want when the login expired", stall.Since)
	}
	waiting, attention := stall.Waiting()
	if !attention || !strings.Contains(waiting.Whose(), "log in to the provider") {
		t.Fatalf("waiting = %+v, %t; want the outage as something waiting on the operator", waiting, attention)
	}
	// The switches somebody placed still answer first: an operator who paused
	// everything is told that, not that the provider is away.
	held := WhyNothingStarts(Conditions{OperatorHeld: true, ProviderOutage: expiredLogin(), ProviderAway: true})
	if held.Reason != ReasonOperatorHold {
		t.Fatalf("stall = %+v, want the operator's own hold answered first", held)
	}
}

// A record that could not be read is said rather than read as the provider
// answering.
func TestAnUnreadableOutageRecordIsSaidRatherThanReadAsAnswering(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.ProviderOutages = fakeOutages{fail: errors.New("permission denied")}

	standing := ReadStanding(context.Background(), sources)
	if standing.ProviderOutage != nil || standing.Paused != "" {
		t.Fatalf("standing = %+v, want no outage invented over a record nobody could read", standing)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "whether the provider is answering could not be read") {
		t.Fatalf("problem = %q, want the unreadable record named", standing.NeedsHumanProblem)
	}
}

// The watchdog's reading: runs each waiting on the provider leave their records
// unmoved, and that is not a machine that died.
func TestAProviderAnsweringNobodyAccountsForTheQuiet(t *testing.T) {
	t.Parallel()

	activity := Activity{
		Since:          loginExpired.Add(-time.Hour),
		ProviderOutage: expiredLogin(),
		ProviderAway:   true,
		Watched:        true,
		Now:            loginExpired.Add(3 * 24 * time.Hour),
	}
	if activity.Unexplained() {
		t.Fatal("three days of a provider answering nobody read as a silence nothing accounts for")
	}
	silence := ReadSilence(activity)
	if silence.Stalled || !strings.Contains(silence.Explains, "the operator must log in") {
		t.Fatalf("silence = %+v, want the login named as what accounts for the quiet", silence)
	}
}
