package console

import (
	"io"
	"strings"
)

// A theme dresses the conversation so that one kind of thing can be told from
// another at a glance: a question the product manager put to the operator, a
// proposal waiting for their decision, and what the harness itself said when
// they asked it for something. All three are things the operator has to act on
// rather than read past, which is what makes them worth separating.
//
// Colour is an addition to the text and never the carrier of meaning. Every
// distinction it draws is one the words already make — a question ends in a
// question mark, a proposal says what it is proposing, an answer from the
// harness follows the command that asked for it — so a conversation read with
// the colour stripped, or read where colour was never permitted, loses the
// decoration and nothing else.
const (
	// The sixteen-colour palette has no orange in it, so a terminal that can
	// only say that much says bright yellow, which is the nearest thing it has.
	questionDeep  = "\x1b[38;5;208m"
	questionBasic = "\x1b[93m"
	proposalDeep  = "\x1b[38;5;141m"
	proposalBasic = "\x1b[95m"
	harnessDeep   = "\x1b[38;5;44m"
	harnessBasic  = "\x1b[36m"
	resetColour   = "\x1b[0m"
)

// ruleWidth bounds the horizontal rule, and unknownWidth is how wide it is
// drawn where the terminal will not say. A rule wider than the screen wraps
// onto a second row, which reads as two rules rather than one.
const (
	ruleWidth    = 80
	unknownWidth = 60
)

// Theme is how much a conversation may be dressed. Its zero value dresses
// nothing at all, which is what a stream that is not a terminal gets, and what
// a terminal gets where NO_COLOR or TERM says colour is unwelcome.
type Theme struct {
	// question, proposal, and harness are the escapes that begin each kind, and
	// are empty together when nothing may be coloured.
	question string
	proposal string
	harness  string
	// width is how wide the rule may be drawn, and is nil where there is no
	// rule to draw.
	width func() int
}

// NewTheme returns the theme this environment permits. NO_COLOR and a terminal
// that says it is dumb or will not say what it is are all taken at their word,
// and each suppresses the rules as well as the colour: both are decoration, and
// an operator who asked for undecorated output asked for all of it. A nil width
// is a caller with nothing to measure a rule against, which is every stream
// that is not a terminal.
func NewTheme(env func(string) string, width func() int) Theme {
	if env("NO_COLOR") != "" {
		return Theme{}
	}
	term := env("TERM")
	if term == "" || term == "dumb" {
		return Theme{}
	}
	theme := Theme{width: width, question: questionBasic, proposal: proposalBasic, harness: harnessBasic}
	if deepColour(term, env("COLORTERM")) {
		theme.question, theme.proposal, theme.harness = questionDeep, proposalDeep, harnessDeep
	}
	return theme
}

// deepColour reports a terminal that can say more than sixteen colours, which
// is what it takes to say orange.
func deepColour(term, colorterm string) bool {
	return strings.Contains(term, "256") ||
		strings.Contains(colorterm, "256") ||
		strings.Contains(colorterm, "truecolor") ||
		strings.Contains(colorterm, "24bit")
}

// Rule returns the horizontal rule that separates what the operator said from
// the answer to it, with the newline that ends it. It is empty where rules are
// suppressed, so a caller writes it unconditionally and a stream is unchanged.
func (t Theme) Rule() string {
	if t.width == nil {
		return ""
	}
	width := unknownWidth
	if measured := t.width(); measured > 0 {
		width = measured
	}
	if width > ruleWidth {
		width = ruleWidth
	}
	return strings.Repeat("─", width) + "\n"
}

// Questions dresses a reply so the questions in it stand out. Only the lines
// that ask something are coloured, and the question mark that makes them
// questions is still there once the colour is gone: a question that scrolls
// past unanswered stalls the work, which is why it is the one thing given the
// loudest colour there is.
func (t Theme) Questions(reply string) string {
	if t.question == "" {
		return reply
	}
	lines := strings.Split(reply, "\n")
	for index, line := range lines {
		if asksSomething(line) {
			lines[index] = t.question + line + resetColour
		}
	}
	return strings.Join(lines, "\n")
}

// asksSomething reports a line that puts a question to the operator.
func asksSomething(line string) bool {
	return strings.HasSuffix(strings.TrimRight(line, " \t"), "?")
}

// Proposal dresses a proposal the operator has not decided on yet.
func (t Theme) Proposal(text string) string { return dress(t.proposal, text) }

// Harness returns a writer that dresses what the harness itself says in answer
// to an operator command. It is a writer rather than a string because a command
// reports itself as it goes, in as many writes as it needs.
func (t Theme) Harness(out io.Writer) io.Writer {
	if t.harness == "" {
		return out
	}
	return &dressed{out: out, colour: t.harness}
}

// dressed colours each line it is given. A colour never outlives the line it
// began on, so text held back for its newline, or written above a composing
// region that is drawn again underneath, cannot leave a terminal wearing a
// colour nobody asked for.
type dressed struct {
	out    io.Writer
	colour string
}

func (d *dressed) Write(text []byte) (int, error) {
	if _, err := io.WriteString(d.out, dress(d.colour, string(text))); err != nil {
		return 0, err
	}
	// The caller wrote what it wrote. What reached the terminal is longer by the
	// escapes, and reporting that would be reporting on text nobody handed us.
	return len(text), nil
}

// dress colours every line of text that has anything on it, leaving the line
// structure exactly as it was.
func dress(colour, text string) string {
	if colour == "" || text == "" {
		return text
	}
	var out strings.Builder
	for _, segment := range strings.SplitAfter(text, "\n") {
		body := strings.TrimSuffix(segment, "\n")
		if strings.TrimSpace(body) != "" {
			out.WriteString(colour + body + resetColour)
		} else {
			out.WriteString(body)
		}
		if strings.HasSuffix(segment, "\n") {
			out.WriteString("\n")
		}
	}
	return out.String()
}
