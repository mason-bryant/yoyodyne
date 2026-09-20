package runstate

// How an append-only log is read past a line that will not decode, without
// losing the place of anything behind it.
//
// The sink reads these logs by position: its cursor for a stream is how many
// records it has read past, and that number is what it writes down after each
// message. A reader that failed on one bad line starved every record behind it
// for as long as the line stood — which is one torn write silencing every
// report, the outage the yoyodyne-ifd.118 run-state change taught. A reader
// that dropped the line instead would shift every record behind it by one, so
// a cursor written before the tear pointed at the wrong record afterwards: a
// message said twice, or one never said. So a line that will not decode is set
// aside with the position it holds, and the records around it keep theirs.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// SkippedLine is one line of an append-only log that would not decode, kept at
// the position it holds among the log's records rather than dropped. Named
// rather than skipped silently, for the reason the sweep log's unreadable lines
// are: a listing that quietly dropped a record would be a worse answer than the
// failure it replaced.
type SkippedLine struct {
	// Position is the line's place among the log's records, counting from zero
	// over every non-blank line whether or not it decoded. It is what a
	// positional reader keys on: the record after this line has the position it
	// would have had if this one had decoded, so a cursor standing past it stays
	// pointed at the same record whether or not the line is ever read.
	Position int `json:"position"`
	// Line is the 1-based line of the file, and Offset the byte it begins at, so
	// somebody can go and look at it.
	Line   int   `json:"line"`
	Offset int64 `json:"offset"`
	// Problem is why it would not decode, in the decoder's words.
	Problem string `json:"problem"`
}

// scanLog reads an append-only log one line at a time, handing each non-blank
// line to decode and setting aside the ones it refuses. It returns the lines set
// aside, and an error only where the log itself could not be read: a line that
// will not decode is not that, and nothing after it is lost to it.
//
// A line longer than maxLineBytes is set aside as well rather than failing the
// read. Every record the writers append is under that bound, so a line over it
// is a torn write joined to the record appended after it, which is the one
// shape of corruption an append-only log actually produces — and the record
// after a tear is exactly the one nobody has seen yet.
func scanLog(file io.Reader, maxLineBytes int, decode func(line []byte) error) ([]SkippedLine, error) {
	reader := bufio.NewReaderSize(file, 64<<10)
	var skipped []SkippedLine
	var offset int64
	line, position := 0, 0
	for {
		text, consumed, overlong, readErr := readLine(reader, maxLineBytes)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return skipped, readErr
		}
		if consumed == 0 {
			// Nothing left to read: the file ended on its last newline.
			return skipped, nil
		}
		start := offset
		offset += consumed
		line++
		trimmed := bytes.TrimSpace(text)
		if !overlong && len(trimmed) == 0 {
			if errors.Is(readErr, io.EOF) {
				return skipped, nil
			}
			continue
		}
		problem := ""
		if overlong {
			problem = fmt.Sprintf("line is longer than %d bytes", maxLineBytes)
		} else if err := decode(trimmed); err != nil {
			problem = err.Error()
		}
		if problem != "" {
			skipped = append(skipped, SkippedLine{Position: position, Line: line, Offset: start, Problem: problem})
		}
		position++
		if errors.Is(readErr, io.EOF) {
			return skipped, nil
		}
	}
}

// readLine reads one line and its newline, reporting how many bytes it took
// from the reader and whether the line ran past the bound. A line past the bound
// is consumed to its end and returned empty, so the read carries on from the
// line after it rather than stopping where the bound was met. It reports io.EOF
// on the last line of a file that does not end in a newline, with that line.
func readLine(reader *bufio.Reader, maxLineBytes int) (text []byte, consumed int64, overlong bool, err error) {
	for {
		chunk, readErr := reader.ReadSlice('\n')
		consumed += int64(len(chunk))
		if !overlong {
			if len(text)+len(chunk) > maxLineBytes {
				overlong, text = true, nil
			} else {
				text = append(text, chunk...)
			}
		}
		switch {
		case readErr == nil:
			return text, consumed, overlong, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			return text, consumed, overlong, io.EOF
		default:
			return nil, consumed, overlong, readErr
		}
	}
}

// firstSkipped is the failure a strict reader reports for a log with a line it
// could not decode. The strict readers fail on the first such line rather than
// reading past it, because a listing that quietly dropped what it could not
// parse is one nobody can trust to be complete; the sink reads past it by
// position instead, and says so.
func firstSkipped(log string, skipped []SkippedLine) error {
	if len(skipped) == 0 {
		return nil
	}
	return fmt.Errorf("decode %s: line %d: %s", log, skipped[0].Line, skipped[0].Problem)
}
