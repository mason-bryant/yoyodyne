package chat

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/console"
)

// TestCrossingsSayWhatChangedAndSayItOnce covers the rule the whole display
// rests on: a milestone is a transition rather than a state, so re-reading a
// record that has not moved says nothing at all. Without that, watching a run
// would be an event log scrolling past the conversation, which is exactly what
// the activity display was built not to be.
func TestCrossingsSayWhatChangedAndSayItOnce(t *testing.T) {
	t.Parallel()

	developing := RunProgress{RunID: "run-a", Status: "running", Phase: "developing"}
	checked := RunProgress{RunID: "run-a", Status: "running", Phase: "reviewing", ChecksPassed: true}
	approved := checked
	approved.ReviewDecision = "approve"
	promoted := approved
	promoted.Phase, promoted.Integrated, promoted.TargetBranch = "integrating", true, "main"
	queued := promoted
	queued.MergeQueued = true
	merged := queued
	merged.Merged = true

	for _, held := range []struct {
		name   string
		before RunProgress
		after  RunProgress
		want   []string
	}{
		{"nothing has moved", checked, checked, nil},
		{"the checks are behind it", developing, checked, []string{"its checks passed"}},
		{"the reviewer approved", checked, approved, []string{"the reviewer approved it"}},
		{"the reviewer asked for repairs", checked, repaired(checked), []string{"the reviewer asked for repairs"}},
		{"it was promoted", approved, promoted, []string{"it was integrated into main"}},
		{"the merge is waiting on the forge", promoted, queued, []string{"its pull request is queued to merge"}},
		{"the forge merged it", queued, merged, []string{"its pull request was merged"}},
		// A record read for the first time long after the run started says
		// everything it crossed at once rather than losing the lot.
		{"several at once", developing, merged, []string{
			"its checks passed",
			"the reviewer approved it",
			"it was integrated into main",
			"its pull request is queued to merge",
			"its pull request was merged",
		}},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			if got := crossings(held.before, held.after); !reflect.DeepEqual(got, held.want) {
				t.Fatalf("crossings() = %#v, want %#v", got, held.want)
			}
		})
	}
}

func repaired(progress RunProgress) RunProgress {
	progress.ReviewDecision = "repair"
	return progress
}

// TestARunSaysWhatItCrossesWhileTheOperatorIsAtThePrompt is what a background
// run used to withhold: everything it did was in the record as it happened, and
// none of it was said until the operator asked. The crossings are written above
// whatever they are composing, exactly as a finished run is, and they reach the
// product manager as the harness activity they are.
func TestARunSaysWhatItCrossesWhileTheOperatorIsAtThePrompt(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	work := &fakeWork{
		gate:   gate,
		report: RunReport{RunID: "run-a", Status: "succeeded", Integrated: true, TargetBranch: "main", Commit: "0f1e2d3c"},
		progress: []RunProgress{
			{RunID: "run-a", Status: "running", Phase: "developing"},
			{RunID: "run-a", Status: "running", Phase: "integrating", ChecksPassed: true,
				ReviewDecision: "approve", Integrated: true, TargetBranch: "main"},
		},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)
	session.progressInterval = 5 * time.Millisecond

	var out strings.Builder
	screen := &scriptedConsole{out: &out, theme: dressedTheme(), steps: []scriptedStep{
		{line: "/work yoyodyne-1"},
		// The operator waits at the prompt. What takes it back the first time is
		// the run crossing something, and the second time the run ending.
		{await: func(interrupt <-chan struct{}) { <-interrupt }},
		{await: func(interrupt <-chan struct{}) { close(gate); <-interrupt }},
		{line: "/exit"},
	}}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := escapes.ReplaceAllString(out.String(), "")
	for _, crossed := range []string{
		"yoyodyne-1: its checks passed",
		"yoyodyne-1: the reviewer approved it",
		"yoyodyne-1: it was integrated into main",
	} {
		if !strings.Contains(transcript, crossed) {
			t.Fatalf("the run crossed %q without saying so: %q", crossed, transcript)
		}
		// Every crossing is said before the prompt that follows it, which is what
		// "above whatever they are typing" means on a screen a test can read.
		if strings.Index(transcript, crossed) > strings.LastIndex(transcript, "you> ") {
			t.Fatalf("%q was said after the last prompt: %q", crossed, transcript)
		}
	}
	// A crossing is said once however often the record is read.
	if count := strings.Count(transcript, "yoyodyne-1: its checks passed"); count != 1 {
		t.Fatalf("the same crossing was said %d times: %q", count, transcript)
	}
	// The product manager is told the same thing, as evidence about the work
	// rather than as an instruction, so the next turn is not answering about a
	// run it believes is still developing.
	notices := session.renderNotices()
	if !strings.Contains(notices, "reached a milestone: the reviewer approved it") {
		t.Fatalf("the product manager was not told what the run crossed: %q", notices)
	}
}

// TestAPreviousRunIsNotAnnouncedAsThisOnesProgress is the mistake the baseline
// reading exists to prevent. Run state is kept per work item, so an item that
// has been run before already has a record saying it was reviewed and merged;
// reading that once and calling it news would announce the last attempt as
// though this one had just done it.
func TestAPreviousRunIsNotAnnouncedAsThisOnesProgress(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	finished := RunProgress{RunID: "run-before", Status: "succeeded", Phase: "complete",
		ChecksPassed: true, ReviewDecision: "approve", Integrated: true, TargetBranch: "main", Merged: true}
	work := &fakeWork{
		gate:   gate,
		report: RunReport{RunID: "run-after", Status: "succeeded"},
		// The record still describes the previous attempt when the new run starts,
		// and then becomes the new run's own, still early in it.
		progress: []RunProgress{finished, finished, {RunID: "run-after", Status: "running", Phase: "developing"}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)
	session.progressInterval = 5 * time.Millisecond

	var out strings.Builder
	screen := &scriptedConsole{out: &out, theme: dressedTheme(), steps: []scriptedStep{
		{line: "/work yoyodyne-1"},
		{await: func(interrupt <-chan struct{}) { close(gate); <-interrupt }},
		{line: "/exit"},
	}}
	if err := session.Converse(context.Background(), screen); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := escapes.ReplaceAllString(out.String(), "")
	for _, mistaken := range []string{"its checks passed", "the reviewer approved it", "its pull request was merged"} {
		if strings.Contains(transcript, "yoyodyne-1: "+mistaken) {
			t.Fatalf("the previous attempt was announced as this run's progress: %q", transcript)
		}
	}
}

// TestARunIsNotWatchedWhereTheConsoleMayNotBeDressed keeps a redirected
// transcript free of anything a clock decided. A stream has no moment at which
// something unprompted can be written, so reporting a crossing between two
// lines that are already buffered would make what a transcript holds depend on
// how long a run took.
func TestARunIsNotWatchedWhereTheConsoleMayNotBeDressed(t *testing.T) {
	t.Parallel()

	work := &fakeWork{progress: []RunProgress{{RunID: "run-a", Status: "running"}}}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)
	session.progressInterval = time.Millisecond

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/work yoyodyne-1\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if asked := work.progressReadings(); asked != 0 {
		t.Fatalf("a redirected conversation read the run's record %d time(s)", asked)
	}
}

// TestAFinishedRunRingsTheBellAndRenamesTheWindow covers the two things a
// terminal can say to somebody who is not looking at it. A conversation is
// often left in a background tab while a run works, and a run that finished
// there is exactly what an operator wants to be told about.
func TestAFinishedRunRingsTheBellAndRenamesTheWindow(t *testing.T) {
	t.Parallel()

	for _, held := range []struct {
		name    string
		theme   console.Theme
		alerted bool
	}{
		{"a terminal that may be dressed", dressedTheme(), true},
		{"anything that may not be", console.Theme{}, false},
	} {
		t.Run(held.name, func(t *testing.T) {
			t.Parallel()

			gate := make(chan struct{})
			work := &fakeWork{gate: gate, report: RunReport{
				RunID: "run-a", Status: "succeeded", Integrated: true, TargetBranch: "main", Commit: "0f1e2d3c",
			}}
			options := testOptions(t, &fakeBackend{})
			options.Work = work
			session := openTestSession(t, options)

			var out strings.Builder
			screen := &scriptedConsole{out: &out, theme: held.theme, steps: []scriptedStep{
				{line: "/work yoyodyne-1"},
				{await: func(interrupt <-chan struct{}) { close(gate); <-interrupt }},
				{line: "/exit"},
			}}
			if err := session.Converse(context.Background(), screen); err != nil {
				t.Fatalf("Converse() error = %v", err)
			}

			transcript := out.String()
			rang := strings.Contains(transcript, "\a")
			renamed := strings.Contains(transcript, "\x1b]2;yoyodyne-1 was integrated into main at 0f1e2d3c\a")
			// The window is left as the operator's shell had it: a title outlives
			// the process, and one naming work that finished long ago is worse
			// than none.
			restored := strings.Contains(transcript, "\x1b]2;\a")
			if rang != held.alerted || renamed != held.alerted || restored != held.alerted {
				t.Fatalf("rang = %v, renamed = %v, restored = %v, want all %v: %q",
					rang, renamed, restored, held.alerted, transcript)
			}
			// Whatever the terminal was told, what the operator reads is the same
			// report in the same words.
			if !strings.Contains(escapes.ReplaceAllString(transcript, ""), "yoyodyne-1 was integrated into main at 0f1e2d3c") {
				t.Fatalf("the run was not reported in words: %q", transcript)
			}
		})
	}
}

// dressedTheme is what a colour terminal permits, built here so the tests that
// turn on the dressing say so in one place.
func dressedTheme() console.Theme {
	return console.NewTheme(func(name string) string {
		if name == "TERM" {
			return "xterm-256color"
		}
		return ""
	}, func() int { return 40 })
}
