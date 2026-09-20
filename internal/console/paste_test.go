package console

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestAPasteIsOneMessageNewlinesAndAll is the reported problem: a paste of three
// lines with a blank line among them sent its first line as the message and
// spilled the rest into the prompts that followed. Bracketed, it reaches the
// conversation as one message with its line structure intact, and return after
// it is what sends.
func TestAPasteIsOneMessageNewlinesAndAll(t *testing.T) {
	t.Parallel()

	// The block as kitty, xterm, and Terminal.app each hand it over: the same
	// markers on every one of them, and the newlines spelled whichever way the
	// terminal spells a pasted newline.
	for _, test := range []struct {
		name   string
		pasted string
	}{
		{name: "newlines sent as carriage returns", pasted: "what is missing from the brief?\r\rand from the goals?"},
		{name: "newlines sent as line feeds", pasted: "what is missing from the brief?\n\nand from the goals?"},
		{name: "newlines sent as the pair", pasted: "what is missing from the brief?\r\n\r\nand from the goals?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			console, keys, out := terminalUnderTest(t, 40)
			lines := prompting(context.Background(), console, "you> ", nil)
			keys.Write([]byte(pasteBegin + test.pasted + pasteEnd))
			// The paste is composed in the region, over the rows it has lines,
			// and nothing in it has sent anything.
			out.await(t, "the pasted message", func(s *screen) bool {
				return s.lastLine() == "and from the goals?"
			})
			select {
			case answer := <-lines:
				t.Fatalf("the paste sent a message on its own: %q, %v", answer.line, answer.err)
			default:
			}

			// Return sends it, as typed input has always been sent.
			keys.Write([]byte("\r"))
			if line := lines.line(t); line != "what is missing from the brief?\n\nand from the goals?" {
				t.Fatalf("line = %q", line)
			}
			want := "you> what is missing from the brief?\n\nand from the goals?"
			if got := out.screen().text(); got != want {
				t.Fatalf("screen =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// TestAPasteArrivingInPiecesIsStillOneMessage covers the read boundary: a paste
// longer than one read from the terminal arrives with its closing marker in a
// later read, and is applied once it is whole rather than in halves.
func TestAPasteArrivingInPiecesIsStillOneMessage(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("before "))
	out.await(t, "the typed text", func(s *screen) bool { return s.lastLine() == "you> before" })
	keys.Write([]byte(pasteBegin + "one\rtw"))
	keys.Write([]byte("o\rthree" + "\x1b[20"))
	keys.Write([]byte("1~ after\r"))
	if line := lines.line(t); line != "before one\ntwo\nthree after" {
		t.Fatalf("line = %q", line)
	}
}

// TestAPasteLandsWhereTheCursorIs keeps a paste an edit like any other: it goes
// in at the cursor, and the cursor is after it.
func TestAPasteLandsWhereTheCursorIs(t *testing.T) {
	t.Parallel()

	console, keys, _ := terminalUnderTest(t, 40)
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("ab\x1b[D" + pasteBegin + "1\r2" + pasteEnd + "!\r"))
	if line := lines.line(t); line != "a1\n2!b" {
		t.Fatalf("line = %q", line)
	}
}

// TestAPasteIntoAListOfAnswersMovesNothing is the same rule a stray escape is
// held to while a question is being answered from a list: a block of text has
// no answer in it, so it moves the marker nowhere and answers nothing.
func TestAPasteIntoAListOfAnswersMovesNothing(t *testing.T) {
	t.Parallel()

	console, keys, out := terminalUnderTest(t, 40)
	answers := make(chan string, 1)
	go func() {
		answer, _ := console.Choose(context.Background(), "which? ", []string{"the first", "the second"}, nil)
		answers <- answer
	}()
	out.await(t, "the list", func(s *screen) bool { return strings.Contains(s.text(), "3. ") })
	keys.Write([]byte(pasteBegin + "2\r2\r" + pasteEnd))
	// Still on the first answer: the digits and returns in the paste were not
	// keystrokes.
	keys.Write([]byte("\r"))
	if answer := <-answers; answer != "the first" {
		t.Fatalf("answer = %q", answer)
	}
}

// TestBracketingIsAskedForAndHandedBack is the hand-over: the mode is turned off
// before the terminal is anybody else's, on suspend and on close, and asked for
// again once the conversation has it back.
func TestBracketingIsAskedForAndHandedBack(t *testing.T) {
	t.Parallel()

	reader, keys := io.Pipe()
	out := newRecorder(40)
	console := newTerminal(reader, out, func() int { return 40 }, func() int { return windowRows }, nil)
	t.Cleanup(func() {
		console.Close()
		keys.Close()
	})
	console.restoreKeyboard = kittyPop
	stopped := make(chan string, 1)
	console.raise = func(signalKey) { stopped <- out.raw() }
	lines := prompting(context.Background(), console, "you> ", nil)
	keys.Write([]byte("\x1b[122;5u"))
	handed := <-stopped
	// Off before the keyboard is handed back, so the shell finds neither.
	if !strings.HasSuffix(handed, pasteOff+kittyPop) {
		t.Fatalf("bracketing was still on when the process stopped: %q", handed)
	}
	// Resumed: asked for again.
	out.await(t, "the region drawn again", func(s *screen) bool { return s.lastLine() == "you>" })
	if resumed := out.raw()[len(handed):]; !strings.Contains(resumed, pasteOn) {
		t.Fatalf("bracketing was not asked for again on resuming: %q", resumed)
	}
	keys.Write([]byte("done\r"))
	lines.line(t)

	before := len(out.raw())
	console.Close()
	if closed := out.raw()[before:]; !strings.Contains(closed, pasteOff) {
		t.Fatalf("bracketing was left on at close: %q", closed)
	}
}

// TestWhatAPasteKeeps is the text a bracketed block becomes: its lines exactly,
// and nothing the region cannot draw.
func TestWhatAPasteKeeps(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		pasted string
		want   string
	}{
		{name: "a blank line is kept", pasted: "one\r\rthree", want: "one\n\nthree"},
		{name: "a trailing newline is kept", pasted: "one\r", want: "one\n"},
		{name: "a tab becomes a space rather than nothing", pasted: "a\tb", want: "a b"},
		{name: "other control characters are dropped", pasted: "a\x01b\x7fc", want: "abc"},
		{name: "an escape is dropped as it is when typed", pasted: "a\x1b[31mb", want: "a[31mb"},
		{name: "a byte that is not text is dropped", pasted: "a\xffb", want: "ab"},
		{name: "text outside ASCII is kept", pasted: "café — naïve", want: "café — naïve"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := pastedText([]byte(test.pasted)); got != test.want {
				t.Fatalf("pastedText(%q) = %q, want %q", test.pasted, got, test.want)
			}
		})
	}
}

// TestAPasteIsDecodedWhole covers the decoder's own contract: a paste is one
// keystroke that spans its markers, one still arriving is not a keystroke yet,
// and one nobody closes does not hold the keyboard for good.
func TestAPasteIsDecodedWhole(t *testing.T) {
	t.Parallel()

	whole := pasteBegin + "a\rb" + pasteEnd + "c"
	pressed, size, complete := decodeKey([]byte(whole))
	if !complete || pressed.code != keyPaste || pressed.text != "a\nb" || size != len(whole)-1 {
		t.Fatalf("decodeKey(%q) = %+v, %d, %v", whole, pressed, size, complete)
	}
	if _, _, complete := decodeKey([]byte(pasteBegin + "a\rb\x1b[20")); complete {
		t.Fatalf("a paste still arriving was read as a keystroke")
	}
	unclosed := pasteBegin + strings.Repeat("x", MaxLineBytes+1)
	pressed, size, complete = decodeKey([]byte(unclosed))
	if !complete || pressed.code != keyIgnored || size != len(pasteBegin) {
		t.Fatalf("an unclosed paste was not given up on: %+v, %d, %v", pressed, size, complete)
	}
	// Anything that is not a paste is left to the escape decoding as before.
	if pressed, _, _ := decodeKey([]byte("\x1b[13;2u")); pressed.code != keyNewline {
		t.Fatalf("shift-return was not decoded: %+v", pressed)
	}
}
