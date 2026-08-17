package console

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// TestTheActivityLineNamesThePhaseAndSaysWhenNothingIsHappening is the promise
// that keeps the indicator honest. A display that animates through a stall
// looks like progress, and looking like progress is worse than silence: the
// frame stops moving once nothing has arrived for a while, and the line says so
// in words rather than leaving the operator to interpret an animation.
func TestTheActivityLineNamesThePhaseAndSaysWhenNothingIsHappening(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	state := working{phase: phaseUnderTest, started: opened, heard: opened}

	// A turn that is moving: the phase, and how long the operator has been
	// waiting for it.
	moving := state.line(opened.Add(3 * time.Second))
	if !strings.Contains(moving, phaseUnderTest) || !strings.Contains(moving, "(3s)") {
		t.Fatalf("line = %q", moving)
	}
	first := state.mark()
	state.tick(opened.Add(3 * time.Second))
	if state.mark() == first {
		t.Fatal("the animation did not advance while the turn was moving")
	}

	// A turn nothing has arrived for: the frame stays where it was, and the line
	// says how long it has been quiet.
	quiet := opened.Add(quietAfter + 14*time.Second)
	stalled := state.line(quiet)
	if !strings.Contains(stalled, "nothing from the provider for") || !strings.Contains(stalled, whole(quietAfter+14*time.Second)) {
		t.Fatalf("a stalled turn read as %q", stalled)
	}
	frozen := state.mark()
	state.tick(quiet)
	if state.mark() != frozen {
		t.Fatal("the animation kept moving while nothing was happening")
	}

	// Anything arriving is what makes it moving again, whether or not it has a
	// new phase to name.
	state.heard = quiet
	if again := state.line(quiet); strings.Contains(again, "nothing from the provider") {
		t.Fatalf("an event that arrived left the turn reading as stalled: %q", again)
	}
}

const phaseUnderTest = "the product manager is writing its reply"

// TestTheActivityLineSitsUnderTheConversationAndLeavesNothingBehind is where the
// indicator has to live: below everything written, above the line being
// composed, and gone entirely once there is an answer to read. An indicator
// written into the operator's own line, or left in the scrollback, would make
// the complaint it answers worse.
func TestTheActivityLineSitsUnderTheConversationAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	activity := console.Working("sending your message")
	out.await(t, "the activity line", func(s *screen) bool {
		return strings.Contains(s.lastLine(), "sending your message")
	})

	// The conversation keeps being written while the indicator is up, and lands
	// above it rather than through it.
	io.WriteString(console, "product-manager> Two goals, then.\n")
	rendered := out.await(t, "the reply above the activity line", func(s *screen) bool {
		return strings.Contains(s.lastLine(), "sending your message")
	})
	lines := rendered.lines()
	if len(lines) < 2 || lines[len(lines)-2] != "product-manager> Two goals, then." {
		t.Fatalf("the reply did not land above the activity line; screen was:\n%s", rendered.text())
	}

	// A phase the harness learns about replaces what the line says, in place.
	activity.Doing("the provider is refusing requests; retrying (attempt 3 of 10)")
	rendered = out.await(t, "the retry", func(s *screen) bool {
		return strings.Contains(s.lastLine(), "attempt 3 of 10")
	})
	if strings.Contains(rendered.text(), "sending your message") {
		t.Fatalf("the earlier phase was left in the scrollback:\n%s", rendered.text())
	}

	// Closing it takes it off the screen: what stays is the conversation, and
	// nothing that was only ever a display of work in progress.
	activity.Close()
	rendered = out.await(t, "the activity line to go", func(s *screen) bool {
		return !strings.Contains(s.text(), "attempt 3 of 10")
	})
	if rendered.text() != "product-manager> Two goals, then." {
		t.Fatalf("closing the activity left something behind:\n%s", rendered.text())
	}

	// The composing line goes back under it, which is where the operator's own
	// text belongs.
	lines2 := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("what next"))
	out.await(t, "the typed text", func(s *screen) bool {
		return s.lastLine() == "you> what next"
	})
	keys.Write([]byte("\r"))
	if line := lines2.line(t); line != "what next" {
		t.Fatalf("line = %q", line)
	}
}

// TestTheActivityLineIsDrawnAboveTheLineBeingComposed keeps both regions
// legible at once: the operator's own text stays at the bottom under the
// cursor, and the account of what is happening sits above it.
func TestTheActivityLineIsDrawnAboveTheLineBeingComposed(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("the brief is thin"))
	out.await(t, "the typed text", func(s *screen) bool {
		return s.lastLine() == "you> the brief is thin"
	})

	console.setStatus("⠋ waiting for the provider (12s)")
	rendered := out.await(t, "the activity line", func(s *screen) bool {
		return strings.Contains(s.text(), "waiting for the provider")
	})
	if rendered.lastLine() != "you> the brief is thin" {
		t.Fatalf("the composing line was disturbed:\n%s", rendered.text())
	}
	if got := rendered.lines(); got[len(got)-2] != "⠋ waiting for the provider (12s)" {
		t.Fatalf("the activity line is not above the composing line:\n%s", rendered.text())
	}

	// Erasing the region takes both away, at whatever width the window has now,
	// and the finished line is the only thing that stays.
	console.setStatus("")
	keys.Write([]byte("\r"))
	if line := lines.line(t); line != "the brief is thin" {
		t.Fatalf("line = %q", line)
	}
	rendered = out.screen()
	if rendered.text() != "you> the brief is thin" {
		t.Fatalf("the region left something behind:\n%s", rendered.text())
	}
}

// TestAnActivityLineWiderThanTheWindowIsStillErasedWhole is the arithmetic the
// region depends on. The line wraps onto rows the terminal chose, and erasing
// it has to move back over every one of them or the conversation above is
// overwritten by what comes next.
func TestAnActivityLineWiderThanTheWindowIsStillErasedWhole(t *testing.T) {
	t.Parallel()

	// Four rows of activity line above two rows of composing line, at a width
	// that wraps both.
	const region = "⠋ the provider is re\n" +
		"fusing requests; ret\n" +
		"rying (attempt 7 of\n" +
		"10)\n" +
		"you> the brief is th\n" +
		"in on goals"

	console, keys, out := terminalUnderTest(t, 20)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("the brief is thin on goals"))
	out.await(t, "the typed text", func(s *screen) bool {
		return strings.HasSuffix(s.text(), "in on goals")
	})

	console.setStatus("⠋ the provider is refusing requests; retrying (attempt 7 of 10)")
	rendered := out.await(t, "the activity line", func(s *screen) bool {
		return len(s.lines()) == 6
	})
	if rendered.text() != region {
		t.Fatalf("the region was not drawn whole:\n%s", rendered.text())
	}

	// Something written now has to land above the whole region, leaving it
	// intact underneath rather than doubling it or erasing part of the reply.
	io.WriteString(console, "product-manager> Two goals, then.\n")
	if rendered = out.screen(); rendered.text() != "product-manager> Two\n goals, then.\n"+region {
		t.Fatalf("the reply did not land above the region:\n%s", rendered.text())
	}

	console.setStatus("")
	keys.Write([]byte("\r"))
	if line := lines.line(t); line != "the brief is thin on goals" {
		t.Fatalf("line = %q", line)
	}
	// What stays is the conversation and the line the operator finished: the
	// account of work in progress was only ever a display.
	if rendered = out.screen(); rendered.text() != "product-manager> Two\n goals, then.\nyou> the brief is th\nin on goals" {
		t.Fatalf("the activity line outlived the work:\n%s", rendered.text())
	}
}

// TestAStreamIsToldEachPhaseOnce is the honest degradation. There is nothing to
// animate and nothing to erase on a stream, so the phases are said as lines,
// each one once, with nothing in them that a clock decided: a redirected
// transcript whose contents depend on how long the provider took is not one
// anybody can compare against another.
func TestAStreamIsToldEachPhaseOnce(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	console := newPlain(nil, &out)
	activity := console.Working("sending your message to the product manager")
	activity.Doing("sending your message to the product manager")
	activity.Doing("")
	activity.Doing("the provider is refusing requests; retrying (attempt 3 of 10)")
	activity.Close()

	want := "… sending your message to the product manager\n" +
		"… the provider is refusing requests; retrying (attempt 3 of 10)\n"
	if out.String() != want {
		t.Fatalf("transcript = %q, want %q", out.String(), want)
	}
	if escapes.MatchString(out.String()) {
		t.Fatalf("cursor control reached a stream: %q", out.String())
	}
	if console.Theme().Rule() != "" {
		t.Fatal("a stream was given a rule to draw")
	}
}

// TestAStatusLineRestsUnderTheConversationAndYieldsToWorkInProgress covers the
// row the region keeps for what is true between turns. Both things want that
// row, and only one of them can have it: what the operator is waiting on is the
// more urgent, so it covers the other for as long as it lasts and gives the row
// back when it ends.
func TestAStatusLineRestsUnderTheConversationAndYieldsToWorkInProgress(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("what next"))
	out.await(t, "the typed text", func(s *screen) bool {
		return s.lastLine() == "you> what next"
	})

	console.Status("this turn $0.0125 · this session $0.0125")
	rendered := out.await(t, "the resting line", func(s *screen) bool {
		return strings.Contains(s.text(), "this session $0.0125")
	})
	if rendered.lastLine() != "you> what next" {
		t.Fatalf("the composing line was disturbed:\n%s", rendered.text())
	}

	// Work in progress takes the row while it lasts.
	activity := console.Working("the product manager is thinking")
	rendered = out.await(t, "the activity line", func(s *screen) bool {
		return strings.Contains(s.text(), "the product manager is thinking")
	})
	if strings.Contains(rendered.text(), "this session") {
		t.Fatalf("the resting line and the activity line shared a screen:\n%s", rendered.text())
	}

	// A new resting line set while work is in progress waits for it rather than
	// fighting it for the row, and is there when it ends.
	console.Status("this turn $0.0300 · this session $0.0425")
	activity.Close()
	rendered = out.await(t, "the resting line to come back", func(s *screen) bool {
		return strings.Contains(s.text(), "this session $0.0425")
	})
	if strings.Contains(rendered.text(), "the product manager is thinking") {
		t.Fatalf("the activity line was left in the scrollback:\n%s", rendered.text())
	}
	if rendered.lastLine() != "you> what next" {
		t.Fatalf("the composing line was disturbed:\n%s", rendered.text())
	}

	// Nothing that was only ever a display of what is true now survives the
	// conversation: closing takes the whole region away.
	keys.Write([]byte("\r"))
	if line := lines.line(t); line != "what next" {
		t.Fatalf("line = %q", line)
	}
	console.Close()
	rendered = out.screen()
	if strings.Contains(rendered.text(), "this session") {
		t.Fatalf("closing left the resting line behind:\n%s", rendered.text())
	}
}
