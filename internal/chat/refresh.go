package chat

// What a refresh carries into a conversation that already holds a picture.
//
// A later turn resumes a provider session, and the session keeps everything it
// was ever sent. So a refresh that carries the whole picture again adds the
// whole picture to the session again, and the session is sent whole on every
// turn after it. The development manager's conversation showed where that ends:
// about twenty re-reads of roughly a megabyte each from 2026-09-20 on, then a
// turn that failed and was retried with the whole picture some twenty-five
// times, until the session passed the 32 MB the provider accepts in one request
// and no turn could be taken at all.
//
// The session already holds the picture the agent was last given, so what a
// refresh owes it is what moved between that picture and the new one. The text
// of the last delivered picture is kept beside the conversation's record for
// exactly this comparison, and a refresh is delivered as the sections that
// differ: the lines that changed in a section both pictures carry, a section
// that is new in full, and a section that is gone by name. A turn after a
// refresh is then as large as what moved rather than as large as the product.
//
// Where there is nothing to compare against — a conversation recorded before
// the text was kept, or a record that cannot be read — the refresh carries the
// whole picture, as it always did, and the text it leaves behind is what the
// next refresh is compared against.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
)

// changesLead tells the role how to read a refresh carried as changes. It is
// part of the framing rather than of the changes, so a role is told once what
// the markers mean and never has to guess from the first hunk.
const changesLead = `What follows is what changed between the picture you were given last and the product as it stands now, rather than the whole of it: every section not named below reads exactly as it did in that picture. In a changed section a line beginning "-" is gone, a line beginning "+" is new, and a line beginning with a space is unchanged and shows where the change sits; a section that is new is given whole, and a section that is gone is named.`

// maxSectionEdits bounds the line comparison of one section. A section that
// differs by more lines than this is given whole: past that many edits the
// changes are no longer much smaller than the section, and the comparison's
// cost grows with the square of them.
const maxSectionEdits = 600

// changeContextLines is how many unchanged lines are shown either side of a
// change, so the role can see where in a section it sits.
const changeContextLines = 2

// refreshPrompt is the refresh this turn delivers: as the changes from the
// picture the role was last given, where that picture's text was kept and the
// changes are the smaller of the two, and whole otherwise.
func (s *Session) refreshPrompt() string {
	delivered, err := s.options.Store.DeliveredPictureText(s.options.identity())
	if err != nil || strings.TrimSpace(delivered) == "" {
		return s.refresh.prompt()
	}
	changes := pictureChanges(delivered, s.refresh.briefing.Text)
	if len(changes) >= len(s.refresh.briefing.Text) {
		return s.refresh.prompt()
	}
	s.carriedChanges = true
	return s.refresh.changesPrompt(changes)
}

// pictureSection is one section of an assembled product context: the heading
// that opens it, and the lines under that heading up to the next section's.
type pictureSection struct {
	heading string
	key     string
	lines   []string
}

// pictureSections splits an assembled product context at the headings of the
// sections it was assembled from. The text before the first of them is a
// section with no heading. A heading that appears twice is told apart by
// where it appears, so each section has a key of its own.
func pictureSections(text string) []pictureSection {
	lines := strings.Split(text, "\n")
	sections := []pictureSection{{}}
	seen := map[string]int{}
	for _, line := range lines {
		if contextbundle.SectionHeading(line) {
			seen[line]++
			key := line
			if seen[line] > 1 {
				key = fmt.Sprintf("%s (%d)", line, seen[line])
			}
			sections = append(sections, pictureSection{heading: line, key: key})
			continue
		}
		last := &sections[len(sections)-1]
		last.lines = append(last.lines, line)
	}
	return sections
}

// pictureChanges says what differs between two assembled product contexts, as
// the role is given it: the changed lines of each section both carry, each new
// section whole, and each section that is gone by name, in the order the newer
// picture carries them. Two pictures that do not differ yield a sentence saying
// so rather than nothing, because an empty refresh reads as one that was lost.
func pictureChanges(was, now string) string {
	before := map[string]pictureSection{}
	for _, section := range pictureSections(was) {
		before[section.key] = section
	}
	var changes strings.Builder
	kept := map[string]bool{}
	for _, section := range pictureSections(now) {
		kept[section.key] = true
		previous, existed := before[section.key]
		switch {
		case !existed:
			fmt.Fprintf(&changes, "## New: %s\n\n%s\n", sectionName(section.heading), strings.Join(section.lines, "\n"))
		case equalLines(previous.lines, section.lines):
		default:
			hunks, compared := lineChanges(previous.lines, section.lines)
			if !compared {
				fmt.Fprintf(&changes, "## Changed, given whole: %s\n\n%s\n", sectionName(section.heading), strings.Join(section.lines, "\n"))
				continue
			}
			fmt.Fprintf(&changes, "## Changed: %s\n\n%s", sectionName(section.heading), hunks)
		}
	}
	for _, section := range pictureSections(was) {
		if !kept[section.key] {
			fmt.Fprintf(&changes, "## Gone: %s\n\n", sectionName(section.heading))
		}
	}
	if changes.Len() == 0 {
		return "Nothing in the picture differs from the one you were given last.\n"
	}
	return changes.String()
}

// sectionName is a section's heading as it is named in the changes, without the
// markup that made it one.
func sectionName(heading string) string {
	if heading == "" {
		return "the opening of the context"
	}
	return strings.TrimSpace(strings.TrimLeft(heading, "#"))
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// lineOp is one step of a line comparison: a line both sides keep, one only the
// older side has, or one only the newer side has.
type lineOp struct {
	kind byte // ' ', '-', or '+'
	line string
}

// lineChanges renders the changes between two versions of one section as hunks:
// each run of changed lines with the unchanged lines around it, headed by the
// nearest heading above it where the section has one. It reports false where
// the two differ by more than maxSectionEdits lines, which the caller answers by
// giving the section whole.
func lineChanges(was, now []string) (string, bool) {
	ops, compared := diffLines(was, now, maxSectionEdits)
	if !compared {
		return "", false
	}
	var rendered strings.Builder
	heading := ""
	for start := 0; start < len(ops); {
		// Find the next change, keeping track of the nearest heading above it.
		next := start
		for next < len(ops) && ops[next].kind == ' ' {
			if strings.HasPrefix(ops[next].line, "#") {
				heading = ops[next].line
			}
			next++
		}
		if next == len(ops) {
			break
		}
		from := next - changeContextLines
		if from < start {
			from = start
		}
		// The hunk runs until a stretch of unchanged lines long enough to separate
		// it from the next one, or the end of the section.
		end := next
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*changeContextLines {
				end += min(changeContextLines, run-end)
				break
			}
			end = run
		}
		hunkHeading := heading
		for _, op := range ops[from:next] {
			if strings.HasPrefix(op.line, "#") {
				hunkHeading = op.line
			}
		}
		if hunkHeading != "" {
			fmt.Fprintf(&rendered, "@@ %s\n", hunkHeading)
		} else {
			rendered.WriteString("@@\n")
		}
		for _, op := range ops[from:end] {
			rendered.WriteByte(op.kind)
			rendered.WriteString(op.line)
			rendered.WriteByte('\n')
			if op.kind != '-' && strings.HasPrefix(op.line, "#") {
				heading = op.line
			}
		}
		rendered.WriteByte('\n')
		start = end
	}
	return rendered.String(), true
}

// diffLines is the shortest sequence of line deletions and insertions that turns
// was into now, found by Myers' algorithm after the lines the two share at
// either end are set aside. It gives up, reporting false, once more than
// maxEdits edits would be needed, which bounds both its time and the memory
// its trace takes.
func diffLines(was, now []string, maxEdits int) ([]lineOp, bool) {
	prefix := 0
	for prefix < len(was) && prefix < len(now) && was[prefix] == now[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(was)-prefix && suffix < len(now)-prefix && was[len(was)-1-suffix] == now[len(now)-1-suffix] {
		suffix++
	}
	a, b := was[prefix:len(was)-suffix], now[prefix:len(now)-suffix]
	middle, ok := myers(a, b, maxEdits)
	if !ok {
		return nil, false
	}
	ops := make([]lineOp, 0, prefix+len(middle)+suffix)
	for _, line := range was[:prefix] {
		ops = append(ops, lineOp{kind: ' ', line: line})
	}
	ops = append(ops, middle...)
	for _, line := range was[len(was)-suffix:] {
		ops = append(ops, lineOp{kind: ' ', line: line})
	}
	return ops, true
}

// myers is the greedy forward search of "An O(ND) Difference Algorithm and Its
// Variations", keeping the furthest-reaching point of every diagonal at every
// edit count so the path can be walked back.
func myers(a, b []string, maxEdits int) ([]lineOp, bool) {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil, true
	}
	if n+m < maxEdits {
		maxEdits = n + m
	}
	offset := maxEdits + 1
	v := make([]int, 2*offset+1)
	var trace [][]int
	found := -1
	for d := 0; d <= maxEdits && found < 0; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
		// Only the diagonals this edit count reached are kept, so the trace grows
		// with the square of the edits rather than with the length of the section.
		snapshot := make([]int, 2*d+1)
		copy(snapshot, v[offset-d:offset+d+1])
		trace = append(trace, snapshot)
	}
	if found < 0 {
		return nil, false
	}
	// Walk the trace back from the end, collecting the path in reverse.
	var reversed []lineOp
	x, y := n, m
	for d := found; d > 0; d-- {
		previous := trace[d-1]
		at := func(k int) int { return previous[k+(d-1)] }
		k := x - y
		var prevK int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := at(prevK)
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			reversed = append(reversed, lineOp{kind: ' ', line: a[x]})
		}
		if x == prevX {
			y--
			reversed = append(reversed, lineOp{kind: '+', line: b[y]})
		} else {
			x--
			reversed = append(reversed, lineOp{kind: '-', line: a[x]})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		reversed = append(reversed, lineOp{kind: ' ', line: a[x]})
	}
	ops := make([]lineOp, len(reversed))
	for index, op := range reversed {
		ops[len(reversed)-1-index] = op
	}
	return ops, true
}
