package console

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// screen is enough of a terminal to see what the console drew. The console
// writes cursor control and expects a terminal to interpret it, so a test that
// only reads the bytes back proves nothing about what the operator sees; this
// interprets them the way a terminal would and leaves the rows to assert on.
//
// A screen with a height is a window: rows written past the bottom of it scroll
// the ones at the top off, into a scrollback the console can no longer address.
// A height of nothing is a screen that goes on for as far as anything is
// written, which is all a test needs where nothing is tall enough to scroll.
type screen struct {
	width      int
	height     int
	rows       []string
	scrollback []string
	row        int
	column     int
}

func newScreen(width int) *screen {
	return &screen{width: width, rows: []string{""}}
}

// newWindow is a screen of a fixed height, which is what a terminal actually
// is: what is drawn past the bottom of it costs the top of it.
func newWindow(width, height int) *screen {
	return &screen{width: width, height: height, rows: []string{""}}
}

func (s *screen) write(text string) {
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			index += s.control(text[index:])
			continue
		}
		character := rune(text[index])
		size := 1
		if character >= 0x80 {
			character, size = decodeRuneAt(text, index)
		}
		index += size
		switch character {
		case '\n':
			// The terminal is left with output processing on, so a newline is a
			// carriage return and a line feed.
			s.row++
			s.column = 0
			s.fit()
		case '\r':
			s.column = 0
		default:
			if s.column >= s.width {
				s.row++
				s.column = 0
				s.fit()
			}
			s.put(character)
		}
	}
}

// control applies one escape sequence and returns how many bytes it took.
func (s *screen) control(text string) int {
	length := escapeLength(text)
	if length < 3 {
		return length
	}
	body := text[2 : length-1]
	final := text[length-1]
	count, err := strconv.Atoi(body)
	if err != nil {
		count = 1
	}
	switch final {
	case 'A':
		s.row -= count
		if s.row < 0 {
			s.row = 0
		}
	case 'C':
		s.column += count
	case 'J':
		// Clear from the cursor to the end of the screen.
		s.fit()
		row := []rune(s.rows[s.row])
		if s.column < len(row) {
			s.rows[s.row] = string(row[:s.column])
		}
		s.rows = s.rows[:s.row+1]
	}
	return length
}

func (s *screen) put(character rune) {
	s.fit()
	row := []rune(s.rows[s.row])
	for len(row) < s.column {
		row = append(row, ' ')
	}
	if s.column < len(row) {
		row[s.column] = character
	} else {
		row = append(row, character)
	}
	s.rows[s.row] = string(row)
	s.column++
}

func (s *screen) fit() {
	for len(s.rows) <= s.row {
		s.rows = append(s.rows, "")
	}
	if s.height <= 0 {
		return
	}
	for len(s.rows) > s.height {
		s.scrollback = append(s.scrollback, s.rows[0])
		s.rows = append([]string(nil), s.rows[1:]...)
		s.row--
	}
}

// lines is the screen as text, with the trailing blank rows dropped.
func (s *screen) lines() []string {
	lines := append([]string(nil), s.rows...)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}
	return lines
}

func (s *screen) text() string { return strings.Join(s.lines(), "\n") }

// scrolled is everything the window has held, in the order it held it: what has
// scrolled off the top followed by what is still on it. The conversation the
// operator can scroll back to is this, so it is what says whether an erase took
// a line of it.
func (s *screen) scrolled() []string {
	lines := make([]string, 0, len(s.scrollback))
	for _, line := range s.scrollback {
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return append(lines, s.lines()...)
}

// lastLine is the bottom of the screen, which is where the composing region
// lives.
func (s *screen) lastLine() string {
	lines := s.lines()
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func decodeRuneAt(text string, index int) (rune, int) {
	for _, character := range text[index:] {
		return character, len(string(character))
	}
	return 0, 1
}

// escapeLength is how many bytes the escape sequence at the start of text
// occupies. An unterminated sequence consumes the rest of the text, because
// there is nothing after it that could be printable.
func escapeLength(text string) int {
	if len(text) < 2 || text[1] != '[' {
		return 1
	}
	for index := 2; index < len(text); index++ {
		if text[index] >= 0x40 && text[index] <= 0x7e {
			return index + 1
		}
	}
	return len(text)
}

// recorder is a terminal's output as a test can read it while the console is
// still writing to it from another goroutine.
type recorder struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	width  int
}

func newRecorder(width int) *recorder { return &recorder{width: width} }

func (r *recorder) Write(text []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buffer.Write(text)
}

// raw is everything written so far, for a test that replays it at a width the
// recorder was not opened with.
func (r *recorder) raw() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buffer.String()
}

// screen replays everything written so far.
func (r *recorder) screen() *screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	rendered := newScreen(r.width)
	rendered.write(r.buffer.String())
	return rendered
}

// window replays everything written so far onto a screen this tall, so a test
// can see what a region and a conversation cost each other for room.
func (r *recorder) window(height int) *screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	rendered := newWindow(r.width, height)
	rendered.write(r.buffer.String())
	return rendered
}

// await waits for the screen to say something, which is how a test watches a
// console that is being driven by the goroutine reading the operator's
// keystrokes rather than by the test itself.
func (r *recorder) await(t *testing.T, what string, satisfied func(*screen) bool) *screen {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		rendered := r.screen()
		if satisfied(rendered) {
			return rendered
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; screen was:\n%s", what, rendered.text())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestScreenInterpretsWhatTheConsoleWrites(t *testing.T) {
	t.Parallel()

	rendered := newScreen(10)
	rendered.write("first\nyou> ab")
	// Erasing a region moves back up to where it started and clears from there.
	rendered.write("\r\x1b[J")
	rendered.write("second\nyou> ab")
	if rendered.text() != "first\nsecond\nyou> ab" {
		t.Fatalf("screen = %q", rendered.text())
	}

	// A line longer than the width wraps, and moving up lands on the row the
	// text actually occupies rather than the one it was written from.
	wrapped := newScreen(4)
	wrapped.write("abcdefg")
	if got := wrapped.lines(); len(got) != 2 || got[0] != "abcd" || got[1] != "efg" {
		t.Fatalf("wrapped = %#v", got)
	}

	// A window is a screen with a bottom: rows written past it scroll the ones at
	// the top off, where they are still the operator's scrollback.
	window := newWindow(6, 2)
	window.write("first\nsecond\nthird")
	if got := window.scrolled(); strings.Join(got, "|") != "first|second|third" {
		t.Fatalf("window = %#v", got)
	}
	if got := window.lines(); strings.Join(got, "|") != "second|third" {
		t.Fatalf("window rows = %#v", got)
	}
	// Climbing further than the window is tall stops at the top of it rather than
	// following what scrolled away, so clearing from there clears rows that were
	// never meant to be climbed over. This is what a region drawn taller than the
	// window does to the conversation above it.
	window.write("\x1b[9A\r\x1b[J")
	if got := window.scrolled(); strings.Join(got, "|") != "first" {
		t.Fatalf("a clamped climb left %#v", got)
	}
}
