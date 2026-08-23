package console

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"
)

// escapes finds anything a terminal would interpret rather than print, which is
// exactly what must never reach a stream that is not a terminal.
var escapes = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func TestWhatTheStreamsAreDecidesWhetherTheRegionIsDrawn(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		term   string
		outTTY bool
		inTTY  bool
		want   bool
	}{
		{name: "a terminal on both ends", term: "xterm-256color", outTTY: true, inTTY: true, want: true},
		{name: "a dumb terminal is taken at its word", term: "dumb", outTTY: true, inTTY: true},
		{name: "a terminal that will not say what it is", outTTY: true, inTTY: true},
		{name: "redirected output", term: "xterm-256color", inTTY: true},
		{name: "redirected input", term: "xterm-256color", outTTY: true},
		{name: "a pipe on both ends", term: "xterm-256color"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := permitted(test.term, test.outTTY, test.inTTY); got != test.want {
				t.Fatalf("permitted() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestAnythingThatIsNotATerminalGetsPlainText is the degradation the recorded
// conversation depends on: a redirected transcript is the same content it has
// always been, and nothing in it has to be interpreted to be read.
func TestAnythingThatIsNotATerminalGetsPlainText(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	console := Open(Options{
		In:  strings.NewReader("the brief is thin\n"),
		Out: &out,
		// A terminal that would be addressable if the streams were files, so
		// what decides this is the streams themselves.
		Env: func(name string) string {
			if name == "TERM" {
				return "xterm-256color"
			}
			return ""
		},
	})
	line, err := console.Prompt(context.Background(), "you> ", nil)
	if err != nil || line != "the brief is thin" {
		t.Fatalf("Prompt() = %q, %v", line, err)
	}
	io.WriteString(console, "product-manager> Two goals, then.\n")
	// The input has ended, which a stream reports rather than waits through.
	if _, err := console.Prompt(context.Background(), "you> ", nil); err != io.EOF {
		t.Fatalf("Prompt() error = %v, want io.EOF", err)
	}
	if err := console.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	transcript := out.String()
	if escapes.MatchString(transcript) {
		t.Fatalf("cursor control reached a stream: %q", transcript)
	}
	// The prompt, the reply, and the newline that closes the prompt nobody
	// answered: exactly the lines a redirected conversation held before the
	// composing region existed.
	want := "you> product-manager> Two goals, then.\nyou> \n"
	if transcript != want {
		t.Fatalf("transcript = %q, want %q", transcript, want)
	}
}

// TestAStreamComposesAMultiLineMessageWithTheContinuationMark is the fallback
// where there are no keystrokes at all to report: a redirected conversation can
// still say something of more than one line, and the prompt is written once for
// the whole of it rather than once a line.
func TestAStreamComposesAMultiLineMessageWithTheContinuationMark(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	console := newPlain(strings.NewReader("two goals\\\nthen a brief\nand one line\n"), &out)
	line, err := console.Prompt(context.Background(), "you> ", nil)
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if line != "two goals\nthen a brief" {
		t.Fatalf("line = %q", line)
	}
	// A line that does not carry on is still one message, unchanged.
	if line, err := console.Prompt(context.Background(), "you> ", nil); err != nil || line != "and one line" {
		t.Fatalf("Prompt() = %q, %v", line, err)
	}
	if transcript := out.String(); transcript != "you> you> " {
		t.Fatalf("transcript = %q", transcript)
	}
	// What it says about composing names the mark and claims nothing about keys
	// a stream never sees.
	if help := console.Composing(); !strings.Contains(help, `\`) || strings.Contains(help, "shift-return") {
		t.Fatalf("Composing() = %q", help)
	}
}

// TestAStreamIsNeverInterrupted is the honest half of the degradation. A
// stream has no moment where the operator is waiting with the screen to
// themselves, so a run that finishes is reported at the next line rather than
// written into a transcript whose contents would then depend on timing.
func TestAStreamIsNeverInterrupted(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	console := newPlain(strings.NewReader("the brief is thin\n"), &out)
	finished := make(chan struct{})
	close(finished)
	line, err := console.Prompt(context.Background(), "you> ", finished)
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if line != "the brief is thin" {
		t.Fatalf("line = %q", line)
	}
}
