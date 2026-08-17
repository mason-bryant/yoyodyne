package console

import (
	"io"
	"strings"
	"unicode/utf8"
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
	// emphasis is what Markdown structure is shown with. It is not a colour: a
	// heading and a bold run are the author's own emphasis rather than a kind of
	// thing the harness is telling apart, so they are weighted rather than
	// recoloured, and they leave the colours above to mean what they mean.
	emphasisOn = "\x1b[1m"
)

// The states work is reported in, coloured the same way wherever they are
// reported. They are a fixed set rather than free text so that one report
// cannot colour a blocked item differently from another, and they are an
// addition exactly like the rest: every group and every item says its state in
// words as well.
const (
	runningDeep  = "\x1b[38;5;39m"
	runningBasic = "\x1b[94m"
	// Blocked shares orange with a question, and for the same reason: it is
	// work that is waiting on somebody.
	blockedDeep  = questionDeep
	blockedBasic = questionBasic
	doneDeep     = "\x1b[38;5;42m"
	doneBasic    = "\x1b[92m"
	failedDeep   = "\x1b[38;5;203m"
	failedBasic  = "\x1b[91m"
)

// State is one of the states work is reported in. A caller names the state and
// the theme decides what it looks like, so the colour of "blocked" is decided
// once rather than at every place that prints it.
type State string

const (
	StateRunning State = "running"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// ruleWidth bounds the horizontal rule, and unknownWidth is how wide it is
// drawn where the terminal will not say. A rule wider than the screen wraps
// onto a second row, which reads as two rules rather than one.
const (
	ruleWidth    = 80
	unknownWidth = 60
)

// bell is the terminal's own way of asking for attention, and titleOpen and
// titleClose rename the window. Both are escapes exactly as the colours are:
// written into a file they are corruption rather than presentation, so they are
// permitted and suppressed with the rest of the dressing.
const (
	bell       = "\a"
	titleOpen  = "\x1b]2;"
	titleClose = "\a"
)

// maxTitleBytes bounds what may be put in a window title. A title is a few
// words in a tab, and a terminal handed a paragraph either truncates it itself
// or scrolls the rest of the window's furniture out of reach.
const maxTitleBytes = 96

// Theme is how much a conversation may be dressed. Its zero value dresses
// nothing at all, which is what a stream that is not a terminal gets, and what
// a terminal gets where NO_COLOR or TERM says colour is unwelcome.
type Theme struct {
	// question, proposal, and harness are the escapes that begin each kind, and
	// are empty together when nothing may be coloured.
	question string
	proposal string
	harness  string
	// emphasis is what Markdown structure is shown with, and is empty with the
	// rest when nothing may be dressed.
	emphasis string
	// states is what each state of work looks like. It is nil when nothing may
	// be coloured, so a state is named in words and dressed in nothing.
	states map[State]string
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
	theme := Theme{
		width:    width,
		question: questionBasic,
		proposal: proposalBasic,
		harness:  harnessBasic,
		emphasis: emphasisOn,
		states: map[State]string{
			StateRunning: runningBasic,
			StateBlocked: blockedBasic,
			StateDone:    doneBasic,
			StateFailed:  failedBasic,
		},
	}
	if deepColour(term, env("COLORTERM")) {
		theme.question, theme.proposal, theme.harness = questionDeep, proposalDeep, harnessDeep
		theme.states = map[State]string{
			StateRunning: runningDeep,
			StateBlocked: blockedDeep,
			StateDone:    doneDeep,
			StateFailed:  failedDeep,
		}
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

// Permitted reports whether this console may be dressed at all. It is one
// question rather than several because the answer is always the same: an
// operator who set NO_COLOR, a terminal that says it is dumb, and a stream that
// is not a terminal each rule out every escape there is, not only the colours.
// Everything that writes one — the rules, the cards, the bell, the window
// title, and a reply shown as it forms — asks this before it writes anything.
func (t Theme) Permitted() bool { return t.width != nil }

// Alert is what a terminal is sent when something the operator was not watching
// has finished: the bell, and the window title renamed to say what it was. A
// conversation is often left in a background tab while a run works, and those
// are the two places a terminal can say so where the operator will see it.
//
// It is written at the start of the line that says the same thing in words,
// rather than on a line of its own. Neither escape occupies a column, so a
// transcript with them stripped out is the transcript without them, which is
// the same discipline the colours follow.
func (t Theme) Alert(title string) string {
	if !t.Permitted() {
		return ""
	}
	return bell + t.Title(title)
}

// Title renames the terminal window, and empties the name when given nothing:
// a title outlives the process that set it, so a conversation that renamed the
// operator's window has to be able to put it back. It is empty where dressing
// is suppressed, so a caller writes it unconditionally.
func (t Theme) Title(title string) string {
	if !t.Permitted() {
		return ""
	}
	return titleOpen + titleText(title) + titleClose
}

// titleText is what may be put in a window title: one bounded line of printable
// text. The terminal interprets this rather than printing it, so anything that
// could end the sequence early or begin another one is removed rather than
// escaped — a run headline carries a work item's own title, and a title is not
// a place to find out what somebody put in one.
func titleText(title string) string {
	// A control character becomes a space rather than vanishing, so a headline
	// folded onto one line still reads as words rather than running two of them
	// together.
	printable := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, title)
	folded := strings.Join(strings.Fields(printable), " ")
	if len(folded) <= maxTitleBytes {
		return folded
	}
	cut := maxTitleBytes
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut])
}

// Rule returns the horizontal rule that separates what the operator said from
// the answer to it, with the newline that ends it. It is empty where rules are
// suppressed, so a caller writes it unconditionally and a stream is unchanged.
func (t Theme) Rule() string {
	if t.width == nil {
		return ""
	}
	return strings.Repeat("─", t.columns()) + "\n"
}

// columns is how wide the theme may draw something that spans the screen. It is
// asked at every draw rather than remembered, so a window the operator resized
// is measured as it now is.
func (t Theme) columns() int {
	width := unknownWidth
	if measured := t.width(); measured > 0 {
		width = measured
	}
	if width > ruleWidth {
		width = ruleWidth
	}
	return width
}

// Card frames one thing the operator has to act on, so several of them in a row
// are told apart at a glance instead of read as one wall of text. The heading
// names it — which is what an operator answers with — and the body is what they
// are deciding about.
//
// The frame is decoration exactly as the rule is, and it is suppressed with the
// rest wherever decoration is unwelcome. What is left then is the heading with
// its body indented underneath, which is what one of these has always looked
// like on a stream: the frame makes the boundary findable and never carries any
// part of the meaning.
func (t Theme) Card(heading, body string) string {
	lines := cardLines(body)
	var card strings.Builder
	if t.width == nil {
		card.WriteString(strings.TrimRight(heading, " ") + "\n")
		for _, line := range lines {
			card.WriteString(strings.TrimRight("    "+line, " ") + "\n")
		}
		return card.String()
	}
	width := t.columns()
	top := cardCornerTop + cardEdge + " " + strings.TrimRight(heading, " ") + " "
	if fill := width - utf8.RuneCountInString(top); fill > 0 {
		top += strings.Repeat(cardEdge, fill)
	}
	card.WriteString(top + "\n")
	for _, line := range lines {
		card.WriteString(strings.TrimRight(cardSide+" "+line, " ") + "\n")
	}
	card.WriteString(cardCornerBottom + strings.Repeat(cardEdge, width-1) + "\n")
	return card.String()
}

// The pieces one card is drawn from. They are box-drawing characters rather
// than ASCII for the same reason the rule is: a terminal that may be dressed at
// all is a terminal that renders them.
const (
	cardCornerTop    = "╭"
	cardCornerBottom = "╰"
	cardEdge         = "─"
	cardSide         = "│"
)

// cardLines is the body of a card as the lines it is drawn from, with the
// trailing blank ones dropped so a card never ends in an empty row it was
// handed by accident.
func cardLines(body string) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}
	return lines
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
			lines[index] = wrap(t.question, line)
		}
	}
	return strings.Join(lines, "\n")
}

// State dresses a piece of text as the state of work it describes. What it is
// given already says the state in words — "blocked (2):" is an answer with the
// colour gone — so this only makes it findable at a glance. Like everything
// else here, the colour never outlives the line it began on.
func (t Theme) State(state State, text string) string {
	return dress(t.states[state], text)
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
			out.WriteString(wrap(colour, body))
		} else {
			out.WriteString(body)
		}
		if strings.HasSuffix(segment, "\n") {
			out.WriteString("\n")
		}
	}
	return out.String()
}

// wrap puts one piece of text in a colour, and puts that colour back after
// anything inside it that was already dressed. Without that, text that says
// something of its own — a state inside a survey the harness is answering
// with — would end its own colour and leave the rest of the line wearing the
// terminal's default, which reads as the dressing having stopped rather than
// resumed.
func wrap(colour, text string) string {
	if colour == "" || text == "" {
		return text
	}
	return colour + strings.ReplaceAll(text, resetColour, resetColour+colour) + resetColour
}
