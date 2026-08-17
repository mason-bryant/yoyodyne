package console

// The product manager writes Markdown, and until now the operator read its
// source: a reply arrived with its hashes, asterisks, and dashes intact, and a
// heading looked exactly like the paragraph under it. Showing that structure is
// presentation and nothing else — it happens here, beside the theme, on the way
// to a terminal, and never touches the recorded reply, the event stream, or
// --json.
//
// Nothing is added to the text and nothing is taken out of it. Every escape is
// inserted between characters that were already there, so the rendered reply
// with its escapes stripped is the reply byte for byte, and a terminal that may
// not be dressed at all is shown the reply exactly as it was written. That is
// the same discipline the colours follow: what the dressing draws attention to
// is a distinction the text already makes.

import (
	"regexp"
	"strings"
)

// The Markdown a reply is dressed for: headings, thematic breaks, list items,
// strong spans, and the fences that mean none of the above applies.
var (
	headingLine = regexp.MustCompile(`^ {0,3}#{1,6}(\s|$)`)
	listLine    = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])(\s+)`)
	fenceLine   = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	strongSpan  = regexp.MustCompile(`\*\*[^*\n]+\*\*|__[^_\n]+__`)
)

// isRule reports a thematic break: three or more of one of -, * or _, with
// nothing on the line but those and blank space. It is written out rather than
// matched, because saying "the same character repeated" needs a back-reference
// the regular expressions here deliberately do not have.
func isRule(line string) bool {
	if indent := len(line) - len(strings.TrimLeft(line, " ")); indent > 3 {
		return false
	}
	body := strings.Map(func(character rune) rune {
		if character == ' ' || character == '\t' {
			return -1
		}
		return character
	}, line)
	if len(body) < 3 {
		return false
	}
	switch body[0] {
	case '-', '*', '_':
		return strings.Count(body, body[:1]) == len(body)
	default:
		return false
	}
}

// Reply dresses one reply from the product manager: the Markdown it is written
// in shown as structure, and the questions in it still the loudest thing on the
// screen. A question wins over structure where a line is both, because a
// question that scrolls past unanswered stalls the work and a heading does not.
func (t Theme) Reply(reply string) string {
	if t.emphasis == "" && t.question == "" {
		return reply
	}
	lines := strings.Split(reply, "\n")
	fenced := false
	for index, line := range lines {
		if fenceLine.MatchString(line) {
			fenced = !fenced
			continue
		}
		// Inside a fence the text is quoted rather than written: dressing it
		// would decorate something the product manager reproduced verbatim, and
		// a block of it is exactly where asterisks and hashes are not Markdown.
		if fenced {
			continue
		}
		lines[index] = t.markdown(line)
	}
	return strings.Join(lines, "\n")
}

// markdown dresses one line of Markdown for what it is.
func (t Theme) markdown(line string) string {
	var dressed string
	switch {
	case headingLine.MatchString(line), isRule(line):
		dressed = wrap(t.emphasis, t.strong(line))
	case listLine.MatchString(line):
		// Only the marker is weighted. Bolding the whole item would make a list
		// louder than the prose around it, when what the marker is for is
		// showing where one item ends and the next begins.
		bounds := listLine.FindStringSubmatchIndex(line)
		markerStart, markerEnd, prefixEnd := bounds[4], bounds[5], bounds[1]
		dressed = line[:markerStart] +
			wrap(t.emphasis, line[markerStart:markerEnd]) +
			line[markerEnd:prefixEnd] +
			t.strong(line[prefixEnd:])
	default:
		dressed = t.strong(line)
	}
	if asksSomething(line) {
		dressed = wrap(t.question, dressed)
	}
	return dressed
}

// strong weights the strong spans in one line, keeping the markers that made
// them strong: a transcript stripped of the escapes still says what was
// emphasized, which is the whole reason the markers are left where they are.
func (t Theme) strong(line string) string {
	if t.emphasis == "" {
		return line
	}
	return strongSpan.ReplaceAllStringFunc(line, func(span string) string {
		return wrap(t.emphasis, span)
	})
}
