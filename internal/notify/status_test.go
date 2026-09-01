package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The whole value of a status board is that a reader already knows every mark
// they can see, so the set is four words with four symbols and nothing else. A
// status that grew a fifth member without anybody deciding it would be a second
// severity system nobody ratified — and a symbol shared with a severity would
// make a reader work out whether a mark is about one message or the whole item.
func TestTheStatusVocabularyIsFourWordsAndFourDistinctSymbols(t *testing.T) {
	t.Parallel()

	statuses := Statuses()
	if len(statuses) != 4 {
		t.Fatalf("Statuses() = %v, want the four the surfaces are held to", statuses)
	}
	seen := map[string]Status{}
	for _, status := range statuses {
		if !status.Valid() {
			t.Fatalf("Statuses() offered %q, which nothing recognizes", status)
		}
		symbol := status.Symbol()
		if symbol == "" {
			t.Fatalf("%s has no symbol, so nothing could mark a thread with it", status)
		}
		if other, found := seen[symbol]; found {
			t.Fatalf("%s and %s are both marked %q, so a reader cannot tell them apart", other, status, symbol)
		}
		seen[symbol] = status
		for _, mark := range []string{criticalMark, warningMark} {
			if strings.Contains(mark, symbol) {
				t.Fatalf("%s is marked %q, which is also how a severity is said", status, symbol)
			}
		}
	}
	if Status("landed").Valid() || Status("landed").Symbol() != "" {
		t.Fatal("a word nobody decided on is a status, so the set is not fixed")
	}
}

// The mark tracks the record rather than any message: what the run's state says
// is what the thread says, through the whole of a run's life.
func TestAnItemsStatusFollowsWhatItsRunRecordSays(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name  string
		state runstate.State
		want  Status
	}{
		{
			name:  "a run being developed is working",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping},
			want:  StatusWorking,
		},
		{
			name:  "a run at the checks is still working",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseChecking},
			want:  StatusWorking,
		},
		{
			name:  "a change with the reviewer is in review",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing},
			want:  StatusInReview,
		},
		{
			name:  "a run being integrated is working again",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseIntegrating},
			want:  StatusWorking,
		},
		{
			name:  "a run parked on a provider is blocked",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, UsageLimitResetsAt: &deadline},
			want:  StatusBlocked,
		},
		{
			name: "a run parked on an unresolved directive is blocked",
			state: runstate.State{
				Status:         runstate.StatusRunning,
				Phase:          runstate.PhaseReviewing,
				DirectivePause: &runstate.DirectivePause{DirectiveID: "dir-1", Kind: "ambiguous"},
			},
			want: StatusBlocked,
		},
		{
			name:  "a run the operator held is blocked",
			state: runstate.State{Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, OperatorHeldSince: &deadline},
			want:  StatusBlocked,
		},
		{
			name:  "a run that succeeded is completed",
			state: runstate.State{Status: runstate.StatusSucceeded, Phase: runstate.PhaseComplete},
			want:  StatusCompleted,
		},
		{
			// The status says the run succeeded and the blocker says a person was
			// handed the work: a sweep that found the promotion missing from the
			// target leaves both on the record, and what is true of the item is the
			// second one.
			name: "a run whose promotion was contradicted is blocked",
			state: runstate.State{
				Status:  runstate.StatusSucceeded,
				Phase:   runstate.PhaseCleaningUp,
				Blocker: "the run recorded a promotion main does not carry",
			},
			want: StatusBlocked,
		},
		{
			name:  "a run that failed is blocked",
			state: runstate.State{Status: runstate.StatusFailed, Phase: runstate.PhaseDeveloping},
			want:  StatusBlocked,
		},
		{
			name:  "a run that was cancelled is blocked",
			state: runstate.State{Status: runstate.StatusCancelled, Phase: runstate.PhaseDeveloping},
			want:  StatusBlocked,
		},
		{
			name:  "a run the harness timed out is blocked",
			state: runstate.State{Status: runstate.StatusTimedOut, Phase: runstate.PhaseChecking},
			want:  StatusBlocked,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := StatusOfRun(testCase.state); got != testCase.want {
				t.Fatalf("StatusOfRun() = %q, want %q", got, testCase.want)
			}
		})
	}
}
