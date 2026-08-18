package chat

// A reply that arrives all at once leaves the operator watching a spinner for
// as long as the product manager takes to write it, with nothing to read until
// it is finished. The text exists before then: the provider reports the
// assistant's message before the terminal result the turn is built from, so
// what changes here is only when the operator is shown it.
//
// Nothing about the record moves. The fragments are the same text the backend
// puts in the event it records and in the result the turn is built from,
// redacted before they arrive, so what is shown is a presentation of the reply
// and never a second source of it. A conversation held where nothing may be
// dressed — a pipe, a file, NO_COLOR, a terminal that says it is dumb — streams
// nothing and writes the whole reply when there is one, exactly as it always
// did.
//
// The one thing this must never do is leave half an answer looking like a whole
// one. A turn whose provider invocation did not finish says so on the line
// after the prose it managed to show, because prose that simply stops reads as
// a product manager that had nothing more to say.

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// replyOpening introduces the answer. It is the same opening the finished reply
// is written with, so a conversation reads the same whether or not it was shown
// as it was written.
const replyOpening = "\nproduct-manager> "

// replyCutOff is what is said when the turn that was being shown did not
// finish. The prose above it is real and is left where it is; what it must not
// do is read as a finished answer.
const replyCutOff = "…the reply above stops there: the turn did not finish, so it is not the whole answer."

// harnessFences open the blocks a reply carries for the harness rather than for
// the operator. Each is reported in its own way once the turn is over — as
// actions taken, proposals to decide, questions to answer, reports collected —
// so showing the source of one as it arrived would be showing the operator the
// protocol instead of the answer.
var harnessFences = []string{trackerFence, proposalFence, concernFence, report.Fence}

// replyStream shows one answer as it is written. It is fed whole messages
// rather than characters, because that is the granularity the provider reports
// at, and it writes them a line at a time, because a line is the unit both the
// Markdown dressing and the console's own composing region work in.
type replyStream struct {
	// mu is held for every fragment. The fragments arrive from whichever
	// goroutine the backend is reading the provider on, and the console they are
	// written to is shared with everything else the conversation says.
	mu    sync.Mutex
	out   io.Writer
	theme console.Theme
	// pending is text received without the newline that ends it. A part line is
	// held rather than shown, so the dressing sees whole lines and the console
	// never has to keep half of one above the operator's own.
	pending string
	// fenced says the lines arriving now belong to a block written for the
	// harness, and quoted says they belong to a code block the product manager
	// wrote for the operator. Neither is dressed: one is not shown at all, and
	// the other is text the product manager reproduced verbatim, which is
	// exactly where a hash and an asterisk are not Markdown.
	fenced bool
	quoted bool
	// held counts blank lines received and not written. They are written only
	// once more prose follows, which drops the leading and trailing blank lines
	// the finished reply is trimmed of and keeps the ones between paragraphs.
	held int
	// shown says prose reached the screen, which is what tells the conversation
	// the answer has already been read and must not be written again.
	shown bool
	// ended says the stream has already said how it finished, so an answer is
	// closed once however many ways the turn reaches its end.
	ended bool
}

// newReplyStream returns the stream this console can show a reply on, or
// nothing where it may not be dressed at all. A nil stream is the ordinary case
// rather than an error: every method below tolerates one, and the conversation
// then writes the reply when it is finished, which is what it has always done.
func newReplyStream(out io.Writer, theme console.Theme) *replyStream {
	if !theme.Permitted() {
		return nil
	}
	return &replyStream{out: out, theme: theme}
}

// write shows one message from the product manager as it arrives.
func (r *replyStream) write(fragment string) {
	if r == nil || fragment == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	r.pending += fragment
	for {
		index := strings.IndexByte(r.pending, '\n')
		if index < 0 {
			return
		}
		line := r.pending[:index]
		r.pending = r.pending[index+1:]
		r.show(line)
	}
}

// endMessage closes the message a provider invocation produced. Whatever is
// left without its newline is the last line of that message rather than the
// start of the next one, and the paragraph break that separates it from what a
// further round says is exactly the break the finished reply joins rounds with.
func (r *replyStream) endMessage() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	r.flush()
	if r.shown {
		r.held = 1
	}
}

// end closes the answer. It reports whether anything was shown, which is how
// the conversation knows the reply has been read and does not write it a second
// time.
func (r *replyStream) end() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return r.shown
	}
	r.flush()
	r.ended = true
	if r.shown {
		// The blank line after the answer is the one the finished reply is
		// written with, so what separates an answer from the next prompt is the
		// same either way.
		fmt.Fprintln(r.out)
	}
	return r.shown
}

// cutOff ends the answer the way a provider invocation that did not finish
// leaves it. It is called from the turn itself rather than inferred from the
// error the conversation reports, because only the turn knows which failures
// stopped the provider mid-reply and which are the harness failing to read a
// block of an answer that arrived whole.
func (r *replyStream) cutOff() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	r.flush()
	r.ended = true
	if !r.shown {
		// Nothing was shown, so there is no half answer on screen to warn about
		// and the failure is reported by the conversation like any other.
		return
	}
	fmt.Fprintln(r.out, replyCutOff)
	fmt.Fprintln(r.out)
}

// flush writes whatever is held without its newline. What the provider sent is
// the end of a message rather than the start of a line it will finish later.
func (r *replyStream) flush() {
	if r.pending == "" {
		return
	}
	line := r.pending
	r.pending = ""
	r.show(line)
}

// show writes one finished line of the reply, or nothing where the line belongs
// to a block the harness reads rather than the operator.
func (r *replyStream) show(line string) {
	if r.fenced {
		if closesFence(line) {
			r.fenced = false
		}
		return
	}
	if opensHarnessFence(line) {
		r.fenced = true
		return
	}
	if strings.TrimSpace(line) == "" && !r.quoted {
		if r.shown {
			r.held++
		}
		return
	}
	if !r.shown {
		fmt.Fprint(r.out, replyOpening)
		r.shown = true
	}
	for ; r.held > 0; r.held-- {
		fmt.Fprintln(r.out)
	}
	// The line is dressed exactly as the finished reply's line would be: the
	// Markdown is shown as structure and a question is still the loudest thing
	// on the screen, with every escape inserted between characters that were
	// already there.
	fmt.Fprintln(r.out, r.dress(line))
}

// dress is one line of the reply as it is shown. A code fence and everything
// inside it is left alone, which is what the finished reply does with it too.
func (r *replyStream) dress(line string) string {
	if quotesFence(line) {
		r.quoted = !r.quoted
		return line
	}
	if r.quoted {
		return line
	}
	return r.theme.Reply(line)
}

// opensHarnessFence reports a line that begins one of the blocks a reply
// carries for the harness. The fences are matched at the start of the line, the
// same place the blocks are extracted from, so a fence the product manager
// quoted inside a paragraph is prose here as it is there.
func opensHarnessFence(line string) bool {
	for _, fence := range harnessFences {
		if strings.HasPrefix(line, fence) {
			return true
		}
	}
	return false
}

// closesFence reports the line that ends a block written for the harness. It is
// the rule the blocks are extracted by: whatever shares the closing fence's
// line belongs to the fence, and prose resumes on the line after it.
func closesFence(line string) bool { return strings.HasPrefix(line, "```") }

// quotesFence reports a Markdown code fence in the prose itself, which is where
// the dressing stops and starts again.
func quotesFence(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}
