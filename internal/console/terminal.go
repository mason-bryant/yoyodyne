package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// terminal is the conversation on a terminal, with the line being composed kept
// in a region of its own at the bottom of the screen. Everything written goes
// above that region: the region is erased, the text is written into the
// scrollback where it stays, and the region is drawn again underneath. Nothing
// is written into the alternate screen and nothing is redrawn above the current
// line, so the terminal's own scrollback, selection, and copying keep working
// on the conversation exactly as they would on any other command's output.
//
// Everything below runs under mu. Output arrives from whichever goroutine is
// talking and keystrokes are applied by the one that is prompting, so the
// screen is only ever coherent if one of them holds it at a time.
type terminal struct {
	mu      sync.Mutex
	out     io.Writer
	input   <-chan chunk
	restore func() error
	// width is asked for at every draw rather than remembered, so a window the
	// operator resized is drawn at the size it is now.
	width func() int

	// pending is text written without a newline yet. It is held back rather than
	// drawn, because half a line above the composing region cannot be erased
	// again without taking the operator's own text with it.
	pending []byte

	// keys is input read but not yet applied: a rune split across two reads, or
	// a second line typed ahead while the first was being answered.
	keys []byte

	prompting  bool
	promptText string
	line       []rune
	cursor     int

	// drawn says whether a region is on screen, and drawnCursor is where the
	// cursor was left within it, counted in columns from the start of the
	// region. The row it is on is worked out from the width at the time rather
	// than remembered, so a window the operator resized between the drawing and
	// the erasing is erased at the size the terminal has rewrapped it to.
	drawn       bool
	drawnCursor int
	closed      bool
}

// chunk is one read from the operator's terminal.
type chunk struct {
	data []byte
	err  error
}

func openTerminal(in, out *os.File) (*terminal, error) {
	restore, err := enterCBreak(in.Fd())
	if err != nil {
		return nil, err
	}
	return newTerminal(in, out, func() int { return terminalWidth(out.Fd()) }, restore), nil
}

// newTerminal builds the console over whatever the caller supplies, so the
// region and the editing can be exercised without a real terminal to drive.
func newTerminal(in io.Reader, out io.Writer, width func() int, restore func() error) *terminal {
	return &terminal{
		out:     out,
		input:   readInput(in),
		restore: restore,
		width:   width,
	}
}

// readInput reads the operator's terminal for as long as it lasts. It is a
// goroutine of its own because a read that is blocked in the terminal driver
// cannot be waited on beside anything else, and Prompt has to be able to give
// up on one to report a run that has finished.
func readInput(in io.Reader) <-chan chunk {
	stream := make(chan chunk, 4)
	go func() {
		defer close(stream)
		if in == nil {
			return
		}
		buffer := make([]byte, 256)
		for {
			count, err := in.Read(buffer)
			if count > 0 {
				data := make([]byte, count)
				copy(data, buffer[:count])
				stream <- chunk{data: data}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					stream <- chunk{err: err}
				}
				return
			}
		}
	}()
	return stream
}

// Write puts text above the composing region. Whole lines are written and stay
// written; a trailing part-line waits for its newline.
func (t *terminal) Write(text []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, errors.New("the console is closed")
	}
	var ready []string
	t.pending = append(t.pending, text...)
	for {
		index := indexNewline(t.pending)
		if index < 0 {
			break
		}
		ready = append(ready, string(t.pending[:index]))
		t.pending = t.pending[index+1:]
	}
	if len(ready) == 0 {
		return len(text), nil
	}
	if err := t.render(ready); err != nil {
		return 0, err
	}
	return len(text), nil
}

// render erases the region, writes the finished lines into the scrollback, and
// draws the region again. It is the only path that puts anything on screen, so
// the region can never be left half erased.
func (t *terminal) render(lines []string) error {
	var out strings.Builder
	out.WriteString(t.eraseRegion())
	for _, line := range lines {
		out.WriteString(line)
		// Output processing is left as the terminal had it, so a newline is
		// still a newline and the harness writes lines the way it always did.
		out.WriteString("\n")
	}
	out.WriteString(t.drawRegion())
	_, err := io.WriteString(t.out, out.String())
	return err
}

// eraseRegion returns the sequence that takes the composing region off the
// screen, leaving the cursor where the region began. It only ever moves within
// the region it drew itself, so the conversation above stays exactly where the
// terminal put it.
func (t *terminal) eraseRegion() string {
	if !t.drawn {
		return ""
	}
	var out strings.Builder
	if rows := t.drawnCursor / t.columns(); rows > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", rows)
	}
	out.WriteString("\r\x1b[J")
	t.drawn = false
	t.drawnCursor = 0
	return out.String()
}

// columns is how wide the terminal is now. It divides the arithmetic that
// positions the cursor, so a terminal that answers with nothing usable is
// floored rather than allowed to take the conversation down with it.
func (t *terminal) columns() int {
	if width := t.width(); width > 0 {
		return width
	}
	return 1
}

// drawRegion returns the sequence that puts the region back: the line being
// composed, with the cursor where the operator left it.
func (t *terminal) drawRegion() string {
	if !t.prompting {
		return ""
	}
	width := t.columns()
	var out strings.Builder
	composed := t.promptText + string(t.line)
	out.WriteString(composed)
	end := visibleWidth(composed)
	// A line that exactly fills the width leaves the cursor in the terminal's
	// deferred-wrap state, where it is neither on this row nor the next. One
	// space commits the wrap and the carriage return puts it at the start of the
	// row it is really on, so the arithmetic below describes the screen.
	if end > 0 && end%width == 0 {
		out.WriteString(" \r")
	}
	target := visibleWidth(t.promptText) + t.cursor
	if up := end/width - target/width; up > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", up)
	}
	out.WriteString("\r")
	if column := target % width; column > 0 {
		fmt.Fprintf(&out, "\x1b[%dC", column)
	}
	t.drawn = true
	t.drawnCursor = target
	return out.String()
}

// redraw puts the region back where it belongs after the state behind it
// changed. It writes nothing into the scrollback.
func (t *terminal) redraw() error {
	return t.render(nil)
}

func (t *terminal) Prompt(ctx context.Context, prompt string, interrupt <-chan struct{}) (string, error) {
	line, submitted, err := t.beginPrompt(prompt)
	for {
		switch {
		case err != nil:
			return "", err
		case submitted:
			return line, nil
		}
		select {
		case piece, open := <-t.input:
			if !open {
				return "", t.endPrompt(io.EOF)
			}
			if piece.err != nil {
				return "", t.endPrompt(fmt.Errorf("read what the operator typed: %w", piece.err))
			}
			line, submitted, err = t.feed(piece.data)
		case <-interrupt:
			// Something else needs the screen. What has been typed stays exactly
			// as it is, and the next prompt picks it up mid-word.
			return "", ErrInterrupted
		case <-ctx.Done():
			return "", t.endPrompt(ctx.Err())
		}
	}
}

// beginPrompt shows the prompt and applies anything the operator typed ahead
// while the last turn was being answered, so type-ahead is honoured in the
// order it was typed rather than discarded.
func (t *terminal) beginPrompt(prompt string) (string, bool, error) {
	t.mu.Lock()
	t.prompting = true
	t.promptText = prompt
	t.mu.Unlock()
	return t.feed(nil)
}

// endPrompt takes the composing region down because there will be no line: the
// input ended, or the conversation was cancelled.
func (t *terminal) endPrompt(cause error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// The prompt and anything composed under it are written into the scrollback
	// rather than erased with the region: the operator typed it, and the screen
	// is the only place it exists.
	composed := t.promptText + string(t.line)
	t.prompting = false
	t.line = nil
	t.cursor = 0
	t.render([]string{composed})
	return cause
}

// feed applies input to the line being composed and returns a line once the
// operator finishes one. Anything typed after that line stays buffered: two
// lines that arrive in one read are two lines, in order.
func (t *terminal) feed(data []byte) (string, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = append(t.keys, data...)
	if len(t.keys) > MaxLineBytes {
		t.keys = nil
		t.line = nil
		t.cursor = 0
		t.redraw()
		return "", false, fmt.Errorf("the operator sent more than %d bytes without a newline", MaxLineBytes)
	}
	for len(t.keys) > 0 {
		pressed, size, complete := decodeKey(t.keys)
		if !complete {
			break
		}
		t.keys = t.keys[size:]
		if pressed.code != keyEnter {
			t.apply(pressed)
			continue
		}
		line := string(t.line)
		t.line = nil
		t.cursor = 0
		t.prompting = false
		// The finished line joins the transcript above, so who said what is
		// still legible after it scrolls: the terminal echoed nothing, because
		// the region is drawn rather than typed into.
		if err := t.render([]string{t.promptText + line}); err != nil {
			return "", false, err
		}
		return line, true, nil
	}
	if err := t.redraw(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// apply is one keystroke's effect on the line being composed.
func (t *terminal) apply(pressed key) {
	switch pressed.code {
	case keyRune:
		t.line = append(t.line, 0)
		copy(t.line[t.cursor+1:], t.line[t.cursor:])
		t.line[t.cursor] = pressed.value
		t.cursor++
	case keyBackspace:
		if t.cursor > 0 {
			t.line = append(t.line[:t.cursor-1], t.line[t.cursor:]...)
			t.cursor--
		}
	case keyDelete:
		if t.cursor < len(t.line) {
			t.line = append(t.line[:t.cursor], t.line[t.cursor+1:]...)
		}
	case keyLeft:
		if t.cursor > 0 {
			t.cursor--
		}
	case keyRight:
		if t.cursor < len(t.line) {
			t.cursor++
		}
	case keyHome:
		t.cursor = 0
	case keyEnd:
		t.cursor = len(t.line)
	case keyKillLine:
		t.line = t.line[t.cursor:]
		t.cursor = 0
	case keyKillWord:
		start := t.cursor
		for start > 0 && t.line[start-1] == ' ' {
			start--
		}
		for start > 0 && t.line[start-1] != ' ' {
			start--
		}
		t.line = append(t.line[:start], t.line[t.cursor:]...)
		t.cursor = start
	}
}

func (t *terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.prompting = false
	var out strings.Builder
	out.WriteString(t.eraseRegion())
	// A part-line held back is written rather than dropped. It is something the
	// harness said, and the screen is the only place it exists.
	if len(t.pending) > 0 {
		out.Write(t.pending)
		out.WriteString("\n")
		t.pending = nil
	}
	_, err := io.WriteString(t.out, out.String())
	t.mu.Unlock()
	if t.restore != nil {
		if restoreErr := t.restore(); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore the terminal: %w", restoreErr))
		}
	}
	return err
}

func indexNewline(text []byte) int {
	for index, character := range text {
		if character == '\n' {
			return index
		}
	}
	return -1
}

// visibleWidth counts the columns text occupies. Every rune is counted as one
// column: this is what the composing region positions the cursor by, and a
// double-width rune in a typed line will therefore be one column out until
// somebody needs it to be otherwise.
func visibleWidth(text string) int { return utf8.RuneCountInString(text) }
