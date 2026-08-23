package console

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// terminalUnderTest is a console over a pipe, which is as close to a terminal
// as a test needs: the console never asks the descriptor anything once it is
// open, it only writes to it and reads keystrokes back.
func terminalUnderTest(t *testing.T, width int) (*terminal, *io.PipeWriter, *recorder) {
	t.Helper()

	reader, keys := io.Pipe()
	out := newRecorder(width)
	console := newTerminal(reader, out, func() int { return width }, nil)
	t.Cleanup(func() {
		console.Close()
		keys.Close()
	})
	return console, keys, out
}

// prompted is one Prompt running in a goroutine, so a test can watch the
// screen while it is blocked reading keystrokes.
type prompted chan struct {
	line string
	err  error
}

func prompting(ctx context.Context, console *terminal, prompt string, interrupt <-chan struct{}) prompted {
	answers := make(prompted, 1)
	go func() {
		line, err := console.Prompt(ctx, prompt, interrupt)
		answers <- struct {
			line string
			err  error
		}{line, err}
	}()
	return answers
}

func (p prompted) result(t *testing.T) (string, error) {
	t.Helper()

	select {
	case answer := <-p:
		return answer.line, answer.err
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt() did not return")
		return "", nil
	}
}

func (p prompted) line(t *testing.T) string {
	t.Helper()

	line, err := p.result(t)
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	return line
}

// TestOutputNeverLandsInsideTheLineBeingTyped is the reported problem: a reply
// that arrives while the operator is mid-sentence used to be printed into the
// middle of it, leaving neither legible.
func TestOutputNeverLandsInsideTheLineBeingTyped(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("the brief is thin on"))
	out.await(t, "the typed text", func(s *screen) bool {
		return s.lastLine() == "you> the brief is thin on"
	})

	// Everything that can arrive while they type: a reply, a proposal, and a run
	// that finished on its own.
	io.WriteString(console, "product-manager> Two goals, then.\n")
	io.WriteString(console, "It proposes 1 work item.\n")
	io.WriteString(console, "yoyodyne-1 was integrated into main.\n")
	rendered := out.screen()
	if rendered.lastLine() != "you> the brief is thin on" {
		t.Fatalf("the composing line was disturbed; screen was:\n%s", rendered.text())
	}
	// The output is above it, whole, and in the order it was written.
	want := strings.Join([]string{
		"product-manager> Two goals, then.",
		"It proposes 1 work item.",
		"yoyodyne-1 was integrated into main.",
		"you> the brief is thin on",
	}, "\n")
	if got := rendered.text(); got != want {
		t.Fatalf("screen =\n%s\nwant\n%s", got, want)
	}

	// Typing carries on from where it was, and the finished line is what the
	// operator typed, not what was written over it.
	keys.Write([]byte(" goals\r"))
	if line := lines.line(t); line != "the brief is thin on goals" {
		t.Fatalf("line = %q", line)
	}
	// The finished line joins the transcript under the operator's own name, so
	// the conversation still says who said what once it has scrolled.
	if final := out.screen(); final.lastLine() != "you> the brief is thin on goals" {
		t.Fatalf("screen =\n%s", final.text())
	}
}

// TestTypingSurvivesAnInterruption covers the run that finishes while the
// operator is mid-sentence: the prompt gives up the wait so the outcome can be
// reported at once, and what was typed is still there to carry on from.
func TestTypingSurvivesAnInterruption(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	ctx := context.Background()
	finished := make(chan struct{})
	interrupted := prompting(ctx, console, "you> ", finished)

	keys.Write([]byte("what about"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> what about" })

	close(finished)
	if _, err := interrupted.result(t); err != ErrInterrupted {
		t.Fatalf("Prompt() error = %v, want ErrInterrupted", err)
	}
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")

	// The next prompt resumes the same half-typed line rather than starting a
	// new one: an interruption costs the operator nothing.
	lines := prompting(ctx, console, "you> ", nil)
	out.await(t, "the resumed line", func(s *screen) bool { return s.lastLine() == "you> what about" })

	keys.Write([]byte(" the goals?\n"))
	if line := lines.line(t); line != "what about the goals?" {
		t.Fatalf("line = %q", line)
	}
	if transcript := out.screen().text(); !strings.Contains(transcript, "harness> yoyodyne-1 finished.") {
		t.Fatalf("the report was lost; screen was:\n%s", transcript)
	}
}

// TestEditingKeepsWhatWasTypedAcrossWrappingAndCorrection checks that the
// region is still the operator's own text after the edits a line of prose
// actually gets, including one long enough to wrap.
func TestEditingKeepsWhatWasTypedAcrossWrappingAndCorrection(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 20)
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("pause on a usage limitx"))
	out.await(t, "the wrapped line", func(s *screen) bool {
		return strings.Contains(strings.Join(s.lines(), ""), "pause on a usage limitx")
	})
	// Backspace removes the mistyped character, the arrow keys move within the
	// line, and a rune typed in the middle lands in the middle.
	keys.Write([]byte("\x7f"))
	keys.Write([]byte("\x1b[D\x1b[D\x1b[D\x1b[D\x1b[D"))
	keys.Write([]byte("hard "))
	keys.Write([]byte("\n"))
	if line := lines.line(t); line != "pause on a usage hard limit" {
		t.Fatalf("line = %q", line)
	}
	if last := out.screen().lastLine(); !strings.HasSuffix(last, "hard limit") {
		t.Fatalf("screen =\n%s", out.screen().text())
	}
}

// TestOutputArrivingUnderAWrappedLineKeepsBothWhole is the same guarantee at
// the width where the arithmetic is hardest: the region is more than one row,
// so erasing it has to climb back to where it started.
func TestOutputArrivingUnderAWrappedLineKeepsBothWhole(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 20)
	lines := prompting(context.Background(), console, "you> ", nil)

	keys.Write([]byte("a much longer sentence than the width"))
	out.await(t, "the wrapped line", func(s *screen) bool {
		return strings.Contains(strings.Join(s.lines(), ""), "a much longer sentence than the width")
	})
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")

	rendered := out.screen()
	if first := rendered.lines()[0]; first != "harness> yoyodyne-1" {
		t.Fatalf("output did not start at the top of the screen:\n%s", rendered.text())
	}
	if composed := strings.Join(rendered.lines()[2:], ""); composed != "you> a much longer sentence than the width" {
		t.Fatalf("the composing line was disturbed:\n%s", rendered.text())
	}
	keys.Write([]byte("\n"))
	if line := lines.line(t); line != "a much longer sentence than the width" {
		t.Fatalf("line = %q", line)
	}
}

// TestAResizedWindowIsErasedAtTheSizeItIsNow covers the window the operator
// widens while a wrapped line is being composed. The terminal rewraps the
// region under them, so where the region starts is worked out from the width
// now rather than the one it was drawn at: erasing by the remembered row count
// would climb past the region and take a line of the conversation with it.
func TestAResizedWindowIsErasedAtTheSizeItIsNow(t *testing.T) {
	t.Parallel()

	width := make(chan int, 1)
	width <- 20
	current := func() int {
		size := <-width
		width <- size
		return size
	}
	reader, keys := io.Pipe()
	out := newRecorder(20)
	console := newTerminal(reader, out, current, nil)
	t.Cleanup(func() {
		console.Close()
		keys.Close()
	})

	// Something is already in the transcript, so an erase that climbs one row
	// too far takes a line of the conversation with it.
	io.WriteString(console, "product-manager> Two goals, then.\n")
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("a line that wraps at twenty"))
	out.await(t, "the wrapped line", func(s *screen) bool {
		return strings.Contains(strings.Join(s.lines(), ""), "a line that wraps at twenty")
	})

	// The operator widens the window, and then something arrives to be written
	// above what they are typing.
	<-width
	width <- 60
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")

	// At sixty columns the composed line is one row, so the region begins where
	// the cursor is and nothing is climbed over to erase it.
	rendered := newScreen(60)
	rendered.write(out.raw())
	want := "product-manager> Two goals, then.\nharness> yoyodyne-1 finished.\nyou> a line that wraps at twenty"
	if got := rendered.text(); got != want {
		t.Fatalf("screen at the new width =\n%s\nwant\n%s", got, want)
	}
	keys.Write([]byte("\n"))
	if line := lines.line(t); line != "a line that wraps at twenty" {
		t.Fatalf("line = %q", line)
	}
}

// TestPromptReportsTheEndOfInputAndKeepsWhatWasTyped covers the operator who
// closes the input mid-sentence. There is no line to return, but what they
// typed is theirs and is left in the transcript rather than erased with the
// region.
func TestPromptReportsTheEndOfInputAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	go func() {
		keys.Write([]byte("half a th"))
		time.Sleep(20 * time.Millisecond)
		keys.Close()
	}()
	if _, err := console.Prompt(context.Background(), "you> ", nil); err != io.EOF {
		t.Fatalf("Prompt() error = %v, want io.EOF", err)
	}
	if rendered := out.screen(); rendered.text() != "you> half a th" {
		t.Fatalf("screen = %q", rendered.text())
	}
}

// TestANewlineIsComposedInTheRegionAndSurvivesIntoTheMessage is the reported
// want: a message of more than one line, composed where every other message is
// composed and sent with its lines where the operator put them.
func TestANewlineIsComposedInTheRegionAndSurvivesIntoTheMessage(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		keys string
	}{
		// Shift-return, as the two protocols report it, then the keys that work
		// where nothing was negotiated at all.
		{name: "shift-return over the kitty protocol", keys: "two goals\x1b[13;2uthen a brief\r"},
		{name: "shift-return over modifyOtherKeys", keys: "two goals\x1b[27;2;13~then a brief\r"},
		{name: "alt-return", keys: "two goals\x1b\rthen a brief\r"},
		{name: "a line carried on with a backslash", keys: "two goals\\\rthen a brief\r"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			console, keys, out := terminalUnderTest(t, 40)
			lines := prompting(context.Background(), console, "you> ", nil)
			keys.Write([]byte(test.keys))

			// The message reaches the conversation with its line structure intact.
			if line := lines.line(t); line != "two goals\nthen a brief" {
				t.Fatalf("line = %q", line)
			}
			// Both of its lines are in the transcript, under the operator's own
			// prompt, and neither has been written over the other.
			rendered := out.screen()
			want := "you> two goals\nthen a brief"
			if got := rendered.text(); got != want {
				t.Fatalf("screen =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestOutputArrivingUnderAMultiLineRegionKeepsBothWhole is the region's own
// guarantee at the shape this work introduced: erasing has to climb back over
// the rows the operator's newlines put there, not just the ones the width did.
func TestOutputArrivingUnderAMultiLineRegionKeepsBothWhole(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 20)
	io.WriteString(console, "product-manager> Two goals, then.\n")
	lines := prompting(context.Background(), console, "you> ", nil)

	// Three lines, one of them long enough to wrap on its own, and the cursor
	// then sent back to the start: the region is several rows and the cursor is
	// nowhere near the last of them, which is the arithmetic that has to be
	// right for the erase to stop where the region began.
	keys.Write([]byte("first\x1b[13;2ua second line that wraps\x1b[13;2uthird"))
	out.await(t, "the composed message", func(s *screen) bool {
		return s.lastLine() == "third"
	})
	keys.Write([]byte("\x01"))

	io.WriteString(console, "harness> yoyodyne-1 finished.\n")
	rendered := out.screen()
	// The rows are what a terminal twenty columns wide makes of it: the region is
	// drawn as it was typed and left to wrap, which is what keeps what the
	// operator sees and what they will send the same text.
	want := strings.Join([]string{
		"product-manager> Two",
		" goals, then.",
		"harness> yoyodyne-1",
		"finished.",
		"you> first",
		"a second line that w",
		"raps",
		"third",
	}, "\n")
	if got := rendered.text(); got != want {
		t.Fatalf("screen =\n%s\nwant\n%s", got, want)
	}

	// What was typed is still whole, and the cursor is still where the operator
	// left it: the rune typed now lands there rather than at the end.
	keys.Write([]byte("!"))
	keys.Write([]byte("\r"))
	if line := lines.line(t); line != "!first\na second line that wraps\nthird" {
		t.Fatalf("line = %q", line)
	}
}

// TestADressedPromptIsMeasuredByWhatItShows is the region's arithmetic on a
// prompt a theme has coloured. The escape sequences are runes that occupy no
// columns at all, so counting them as columns would put the cursor past the end
// of the prompt and, worse, leave the erase climbing the wrong number of rows
// and a row of the region behind in the transcript.
func TestADressedPromptIsMeasuredByWhatItShows(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 20)
	io.WriteString(console, "product-manager> Two goals.\n")
	// The prompt a dressed terminal draws: colour on, five columns of text,
	// colour off.
	lines := prompting(context.Background(), console, "\x1b[33myou> \x1b[0m", nil)
	// Long enough that counting the dressing as columns would have the region
	// believe the first line wrapped when on screen it did not.
	keys.Write([]byte("first line\x1b[13;2usecond"))
	out.await(t, "the composed message", func(s *screen) bool { return s.lastLine() == "second" })

	// Something arrives above a region whose rows come from the operator's own
	// newline rather than from the width.
	io.WriteString(console, "harness> yoyodyne-1 finished.\n")
	rendered := out.screen()
	want := strings.Join([]string{
		"product-manager> Two",
		" goals.",
		"harness> yoyodyne-1",
		"finished.",
		"you> first line",
		"second",
	}, "\n")
	if got := rendered.text(); got != want {
		t.Fatalf("screen =\n%s\nwant\n%s", got, want)
	}
	// The cursor is where the prompt shows it to be, so what is typed next lands
	// at the end of what was typed before rather than a few columns off it.
	keys.Write([]byte("!\r"))
	if line := lines.line(t); line != "first line\nsecond!" {
		t.Fatalf("line = %q", line)
	}
}

// TestEditingAMultiLineMessage keeps the small editing the region has working
// once there is more than one line to do it on.
func TestEditingAMultiLineMessage(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	// Backspace at the start of a line takes the newline out, which is how a
	// newline typed by mistake is undone. Ctrl-W deletes the word before the
	// cursor and stops at the line rather than running back into the one above.
	keys.Write([]byte("two goals\x1b\rthen\x7f\x7f\x7f\x7f\x7f a brief\x1b\rand a plan\x17\x17\x17\r"))
	if line := lines.line(t); line != "two goals a brief\n" {
		t.Fatalf("line = %q", line)
	}
}

func TestTypeAheadIsAppliedInOrder(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	// Two lines arrive in one read, as they do when the operator carries on
	// typing while the last turn is being answered.
	go keys.Write([]byte("first thing\nsecond thing\n"))
	for _, want := range []string{"first thing", "second thing"} {
		line, err := console.Prompt(context.Background(), "you> ", nil)
		if err != nil {
			t.Fatalf("Prompt() error = %v", err)
		}
		if line != want {
			t.Fatalf("line = %q, want %q", line, want)
		}
	}
}

// TestAPartLineIsHeldBackUntilItIsWhole keeps half a line from being drawn
// where the region would later erase it.
func TestAPartLineIsHeldBackUntilItIsWhole(t *testing.T) {
	t.Parallel()

	console, _, out := terminalUnderTest(t, 40)
	io.WriteString(console, "the item was ")
	if rendered := out.screen(); strings.Contains(rendered.text(), "the item was") {
		t.Fatalf("a part line was drawn:\n%s", rendered.text())
	}
	io.WriteString(console, "integrated\n")
	if rendered := out.screen(); rendered.text() != "the item was integrated" {
		t.Fatalf("screen = %q", rendered.text())
	}
	// Closing writes out whatever is still held, because the screen is the only
	// place it exists.
	io.WriteString(console, "and nothing else")
	console.Close()
	if rendered := out.screen(); !strings.Contains(rendered.text(), "and nothing else") {
		t.Fatalf("a held line was dropped on close:\n%s", rendered.text())
	}
}

// TestSuspendingHandsTheTerminalOverAndTakesItBack covers Ctrl-Z on a terminal
// that reports the key instead of stopping the process for itself. The shell
// that takes the foreground must find none of this conversation's settings on
// the terminal, and the conversation that comes back must not be left composing
// on a keyboard that stopped reporting what it negotiated.
func TestSuspendingHandsTheTerminalOverAndTakesItBack(t *testing.T) {
	t.Parallel()

	reader, keys := io.Pipe()
	out := newRecorder(40)
	console := newTerminal(reader, out, func() int { return 40 }, nil)
	t.Cleanup(func() {
		console.Close()
		keys.Close()
	})
	// A conversation that negotiated a keyboard and can take its modes back.
	console.keyboard = keyboardKitty
	console.restoreKeyboard = kittyPop
	var handedBack, taken atomic.Int64
	console.restore = func() error { handedBack.Add(1); return nil }
	console.modes = func() (func() error, error) {
		taken.Add(1)
		return func() error { handedBack.Add(1); return nil }, nil
	}
	stopped := make(chan string, 1)
	console.raise = func(pressed signalKey) {
		if pressed != signalSuspend {
			t.Errorf("raised %v, want the suspend", pressed)
		}
		// What the screen holds at the moment the process stops: this is what the
		// shell is about to be handed.
		stopped <- out.raw()
	}
	// The terminal answers the negotiation the resumed conversation makes.
	go func() {
		for !strings.Contains(out.raw(), kittyQuery) {
			time.Sleep(time.Millisecond)
		}
		keys.Write([]byte("\x1b[?0u\x1b[?62;c"))
	}()

	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("half a thought"))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> half a thought" })
	// Ctrl-Z, as a negotiated keyboard reports it rather than as a signal the
	// terminal raised.
	keys.Write([]byte("\x1b[122;5u"))

	select {
	case screen := <-stopped:
		if !strings.HasSuffix(screen, kittyPop) {
			t.Fatalf("the keyboard was still this conversation's when the process stopped: %q", screen)
		}
		if handedBack.Load() != 1 {
			t.Fatalf("the terminal's own modes were not restored before the stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl-Z did not stop the conversation")
	}

	// Resumed: the modes are taken again, the keyboard is negotiated again
	// rather than assumed, and what was being composed is drawn back.
	out.await(t, "the region drawn again", func(s *screen) bool {
		return s.lastLine() == "you> half a thought"
	})
	keys.Write([]byte(" of it\r"))
	if line := lines.line(t); line != "half a thought of it" {
		t.Fatalf("line = %q", line)
	}
	if taken.Load() != 1 {
		t.Fatalf("the terminal's modes were taken back %d time(s), want 1", taken.Load())
	}
	if console.keyboard != keyboardKitty || console.restoreKeyboard != kittyPop {
		t.Fatalf("the keyboard was not negotiated again: %v/%q", console.keyboard, console.restoreKeyboard)
	}
}

func TestTheTerminalIsHandedBackOnClose(t *testing.T) {
	t.Parallel()

	restored := 0
	console := newTerminal(strings.NewReader(""), newRecorder(40), func() int { return 40 }, func() error {
		restored++
		return nil
	})
	if err := console.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := console.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if restored != 1 {
		t.Fatalf("terminal restored %d time(s), want 1", restored)
	}
}
