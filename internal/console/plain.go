package console

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
)

// plain is the conversation as an ordinary stream of text. It is what a pipe, a
// file, a redirected terminal, and a terminal that says it is dumb all get:
// lines in, lines out, nothing written that a reader would have to interpret.
//
// It deliberately does not interrupt a prompt when a background run finishes.
// A stream has no moment where the operator is waiting with the screen to
// themselves, and reporting between two lines that are already buffered would
// make a redirected transcript depend on timing. The run is reported at the
// next line instead, which is where a stream can say it truthfully, and which
// is what a redirected conversation has always done.
type plain struct {
	// mu keeps one write whole against another. A conversation writes from the
	// goroutine that is talking, but a console is reachable from anywhere.
	mu      sync.Mutex
	out     io.Writer
	scanner *bufio.Scanner
}

func newPlain(in io.Reader, out io.Writer) *plain {
	console := &plain{out: out}
	if in != nil {
		console.scanner = bufio.NewScanner(in)
		console.scanner.Buffer(make([]byte, 4096), MaxLineBytes)
	}
	return console
}

func (p *plain) Write(text []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(text)
}

func (p *plain) Prompt(ctx context.Context, prompt string, _ <-chan struct{}) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.Lock()
	_, err := io.WriteString(p.out, prompt)
	p.mu.Unlock()
	if err != nil {
		return "", err
	}
	if p.scanner == nil || !p.scanner.Scan() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.scanner != nil {
			if err := p.scanner.Err(); err != nil {
				return "", err
			}
		}
		// The newline closes the prompt line nobody answered, so a transcript
		// does not end mid-line.
		fmt.Fprintln(p.out)
		return "", io.EOF
	}
	// Nothing is echoed here. Where the input is a terminal it echoed the line
	// itself, and where it is not there was never an echo to reproduce; writing
	// one would either double the line or change what a redirected transcript
	// has always contained.
	return p.scanner.Text(), nil
}

// Close has nothing to restore: a stream was never put into a state anybody
// has to be got out of.
func (p *plain) Close() error { return nil }
