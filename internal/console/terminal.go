package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
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
	// theme is how much this terminal's environment permits it to be dressed.
	// It is fixed for the life of the console, because an operator who set
	// NO_COLOR set it before the conversation opened.
	theme Theme

	// pending is text written without a newline yet. It is held back rather than
	// drawn, because half a line above the composing region cannot be erased
	// again without taking the operator's own text with it.
	pending []byte

	// keys is input read but not yet applied: a rune split across two reads, or
	// a second line typed ahead while the first was being answered.
	keys []byte

	// keyboard is how much this terminal agreed to say about a keystroke, and
	// restoreKeyboard is what puts that agreement back as it was found.
	keyboard        keyboardMode
	restoreKeyboard string

	prompting  bool
	promptText string
	// line is the message being composed, which may be more than one line of it:
	// a newline in here is a newline the operator typed, and it reaches the
	// message they send exactly as it is drawn.
	line   []rune
	cursor int

	// status is the account of work in progress and resting is what is left on
	// that line between turns. They share one row of the region, and work in
	// progress covers what is merely true for as long as it lasts: what the
	// operator is waiting on is the more urgent of the two, and a second row
	// would take another line of their screen for good.
	status  string
	resting string

	// drawn says whether a region is on screen, drawnStatus is the status line it
	// was drawn with, drawnComposed is the prompt and the message as they were
	// drawn, and drawnCursor is where the cursor was left in that text, counted
	// in runes. Both the rows the status occupies and the row the cursor is on
	// are worked out from that text and the width at the time rather than
	// remembered, so a window the operator resized between the drawing and the
	// erasing is erased at the size the terminal has rewrapped it to.
	drawn         bool
	drawnStatus   string
	drawnComposed string
	drawnCursor   int
	closed        bool
}

// chunk is one read from the operator's terminal.
type chunk struct {
	data []byte
	err  error
}

func openTerminal(in, out *os.File, env func(string) string) (*terminal, error) {
	restore, err := enterCBreak(in.Fd())
	if err != nil {
		return nil, err
	}
	width := func() int { return terminalWidth(out.Fd()) }
	terminal := newTerminal(in, out, width, restore)
	terminal.theme = NewTheme(env, width)
	// What the terminal will say about a keystroke is settled before the first
	// prompt is drawn, because it decides both what shift-return does and what
	// /help is allowed to claim it does.
	terminal.negotiateKeyboard(keyboardReplyTimeout)
	return terminal, nil
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
	if rows := t.statusRows() + cursorRow(t.drawnComposed, t.drawnCursor, t.columns()); rows > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", rows)
	}
	out.WriteString("\r\x1b[J")
	t.drawn = false
	t.drawnStatus = ""
	t.drawnComposed = ""
	t.drawnCursor = 0
	return out.String()
}

// statusRows is how many rows the status line took at the width the terminal
// has now, counting the newline that ends it. A line that exactly fills the
// width still occupies one row: the terminal defers the wrap, and the newline
// is what commits it.
func (t *terminal) statusRows() int {
	if t.drawnStatus == "" {
		return 0
	}
	return (visibleWidth(t.drawnStatus)-1)/t.columns() + 1
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

// drawRegion returns the sequence that puts the region back: the account of
// work in progress if there is one, the line being composed under it, and the
// cursor where the operator left it.
func (t *terminal) drawRegion() string {
	status := t.statusLine()
	if !t.prompting && status == "" {
		return ""
	}
	width := t.columns()
	var out strings.Builder
	if status != "" {
		// The status is written and left behind: the region is redrawn as a
		// whole, so it is put back on every draw rather than moved.
		out.WriteString(status)
		out.WriteString("\n")
	}
	t.drawnStatus = status
	if !t.prompting {
		// Nothing is being composed, so the cursor rests at the start of the row
		// below the status, which is where the next thing written will go.
		t.drawn = true
		t.drawnComposed = ""
		t.drawnCursor = 0
		return out.String()
	}
	composed := t.promptText + string(t.line)
	// A newline the operator typed is written as one: the message occupies as
	// many rows as it has lines, and the rest of the region is measured from the
	// same text, so what is erased is what was drawn.
	out.WriteString(composed)
	endRow, endColumn := place(composed, visibleWidth(composed), width)
	// A row that exactly fills the width leaves the cursor in the terminal's
	// deferred-wrap state, where it is neither on this row nor the next. One
	// space commits the wrap and the carriage return puts it at the start of the
	// row it is really on, so the arithmetic below describes the screen.
	if endColumn >= width {
		out.WriteString(" \r")
		endRow++
		endColumn = 0
	}
	cursor := visibleWidth(t.promptText) + t.cursor
	targetRow, targetColumn := place(composed, cursor, width)
	if targetColumn >= width {
		targetRow++
		targetColumn = 0
	}
	if up := endRow - targetRow; up > 0 {
		fmt.Fprintf(&out, "\x1b[%dA", up)
	}
	out.WriteString("\r")
	if targetColumn > 0 {
		fmt.Fprintf(&out, "\x1b[%dC", targetColumn)
	}
	t.drawn = true
	t.drawnComposed = composed
	t.drawnCursor = cursor
	return out.String()
}

// place is where the cursor sits once the first index runes of text have been
// written: the row, counted from the row the text began on, and the column
// across it. A row that is exactly full is reported as the column past its end,
// which is the terminal's deferred-wrap state — neither on that row nor the
// next until something else is written — and is what its callers resolve.
func place(text string, index, width int) (int, int) {
	row, column, position := 0, 0, 0
	for _, character := range text {
		if position >= index {
			break
		}
		position++
		if character == '\n' {
			row++
			column = 0
			continue
		}
		if column >= width {
			row++
			column = 0
		}
		column++
	}
	return row, column
}

// cursorRow is how many rows below the start of the text the cursor is, which
// is how far the region has to climb to erase itself.
func cursorRow(text string, index, width int) int {
	row, column := place(text, index, width)
	if column >= width {
		row++
	}
	return row
}

// redraw puts the region back where it belongs after the state behind it
// changed. It writes nothing into the scrollback.
func (t *terminal) redraw() error {
	return t.render(nil)
}

// Working draws the account of work in progress on a line of its own below the
// conversation, where it is erased and drawn again exactly as the composing
// line is. It is a region rather than ordinary output for the same reason the
// composing line is: an indicator that scrolled away with the transcript would
// be a log of itself, and one written into the operator's own line would be
// worse than none at all.
func (t *terminal) Working(phase string) Activity {
	return newSpinner(phase, t.setStatus, time.Now, spinnerInterval)
}

// Theme reports how much this terminal may be dressed.
func (t *terminal) Theme() Theme { return t.theme }

// Composing says how a message of more than one line is typed on this terminal,
// which is what it turned out to report rather than what terminals usually do.
func (t *terminal) Composing() string { return composingHelp(t.keyboard) }

// setStatus replaces what the activity line says. A console that has been
// closed keeps the screen the operator's shell left it with, and an unchanged
// line is not redrawn, so a display that has nothing new to say costs nothing.
func (t *terminal) setStatus(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.status == text {
		return
	}
	t.status = text
	t.redraw()
}

// Status replaces what rests on that line between turns. It takes effect at
// once when nothing is being waited for, and otherwise when the account of work
// in progress ends: the two would fight over one row, and the work is what the
// operator is waiting to hear about.
func (t *terminal) Status(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.resting == text {
		return
	}
	t.resting = text
	if t.status != "" {
		return
	}
	t.redraw()
}

// statusLine is what the region's top row says now.
func (t *terminal) statusLine() string {
	if t.status != "" {
		return t.status
	}
	return t.resting
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

// feed applies input to the message being composed and returns it once the
// operator sends one. Anything typed after that stays buffered: two messages
// that arrive in one read are two messages, in order.
func (t *terminal) feed(data []byte) (string, bool, error) {
	line, submitted, raised, err := t.consume(data)
	// The signals a negotiated keyboard stopped the terminal raising are raised
	// here, with the screen let go of: one of them stops this process, and doing
	// that while holding the console would stop it mid-draw.
	for _, pressed := range raised {
		raiseSignal(pressed)
	}
	return line, submitted, err
}

// consume is feed under the lock: everything that changes what is on screen,
// and the signal keys it met on the way, which are raised once it is let go of.
func (t *terminal) consume(data []byte) (string, bool, []signalKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var raised []signalKey
	t.keys = append(t.keys, data...)
	if len(t.keys) > MaxLineBytes {
		t.keys = nil
		t.line = nil
		t.cursor = 0
		t.redraw()
		return "", false, raised, fmt.Errorf("the operator sent more than %d bytes without a newline", MaxLineBytes)
	}
	for len(t.keys) > 0 {
		pressed, size, complete := decodeKey(t.keys)
		if !complete {
			break
		}
		t.keys = t.keys[size:]
		if pressed.code == keySignal {
			raised = append(raised, pressed.signal)
			continue
		}
		if pressed.code != keyEnter {
			t.apply(pressed)
			continue
		}
		line := string(t.line)
		// A message whose last character is a backslash is one the operator is
		// carrying on: the newline that asks the terminal for nothing at all, and
		// so the one that works where shift-return cannot be reported and
		// alt-return is taken by something else. It is composed in the region like
		// any other newline rather than sent and joined up afterwards.
		if carried, ok := carriedOn(line); ok {
			t.line = []rune(carried)
			t.cursor = len(t.line)
			continue
		}
		t.line = nil
		t.cursor = 0
		t.prompting = false
		// The finished message joins the transcript above, so who said what is
		// still legible after it scrolls: the terminal echoed nothing, because
		// the region is drawn rather than typed into.
		if err := t.render([]string{t.promptText + line}); err != nil {
			return "", false, raised, err
		}
		return line, true, raised, nil
	}
	if err := t.redraw(); err != nil {
		return "", false, raised, err
	}
	return "", false, raised, nil
}

// apply is one keystroke's effect on the message being composed.
func (t *terminal) apply(pressed key) {
	switch pressed.code {
	case keyRune:
		t.insert(pressed.value)
	case keyNewline:
		t.insert('\n')
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
		for start > 0 && separates(t.line[start-1]) {
			start--
		}
		for start > 0 && !separates(t.line[start-1]) {
			start--
		}
		t.line = append(t.line[:start], t.line[t.cursor:]...)
		t.cursor = start
	}
}

// insert puts one rune where the cursor is. A newline is a rune like any other
// here: what it changes is how the region is drawn, not how it is edited.
func (t *terminal) insert(value rune) {
	t.line = append(t.line, 0)
	copy(t.line[t.cursor+1:], t.line[t.cursor:])
	t.line[t.cursor] = value
	t.cursor++
}

// separates reports what a word ends at. A newline ends one as a space does, so
// deleting the last word of a line stops at the line rather than running back
// into the one above it.
func separates(character rune) bool { return character == ' ' || character == '\n' }

func (t *terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.prompting = false
	t.status = ""
	t.resting = ""
	var out strings.Builder
	out.WriteString(t.eraseRegion())
	// Whatever was negotiated about the keyboard is handed back before the modes
	// are, so a shell that gets the terminal back is not left with a protocol
	// this conversation turned on for itself.
	out.WriteString(t.restoreKeyboard)
	t.restoreKeyboard = ""
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
