package console

import (
	"strings"
	"testing"
)

const markdownReply = `# What is missing

The brief states **two** goals and the third is only implied.

- the first, which is written down
- the second, which is **not**

---

Should the third be written down, or dropped?

` + "```" + `text
# not a heading
- not a list item
` + "```" + `

Say which, and I will propose it.`

// Rendering is presentation and nothing else. Every escape is inserted between
// characters that were already there, so what the operator reads is the reply
// they would have read on a stream, with structure shown rather than spelled
// out — and a transcript stripped of the escapes is the recorded reply exactly.
func TestReplyOnlyDressesTheReplyAndNeverChangesIt(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), nil)
	dressed := theme.Reply(markdownReply)
	if dressed == markdownReply {
		t.Fatal("a terminal that permits colour was shown the reply undressed")
	}
	if stripped := escapes.ReplaceAllString(dressed, ""); stripped != markdownReply {
		t.Fatalf("stripping the dressing did not give the reply back:\n%q", stripped)
	}
}

// The structure that is shown is the structure the Markdown states: headings
// and thematic breaks weighted, list markers weighted, strong spans weighted,
// and questions still the loudest thing on the screen.
func TestReplyShowsHeadingsListsRulesAndStrongSpans(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), nil)
	lines := strings.Split(theme.Reply(markdownReply), "\n")
	for _, expected := range []struct {
		line   int
		starts string
		what   string
	}{
		{line: 0, starts: emphasisOn, what: "a heading"},
		{line: 4, starts: emphasisOn, what: "a list marker"},
		{line: 7, starts: emphasisOn, what: "a thematic break"},
		{line: 9, starts: questionDeep, what: "a question"},
	} {
		if !strings.HasPrefix(lines[expected.line], expected.starts) {
			t.Fatalf("%s was not dressed: %q", expected.what, lines[expected.line])
		}
	}
	// A strong span is weighted where it sits, inside the prose.
	if !strings.Contains(lines[2], emphasisOn+"**two**") {
		t.Fatalf("a strong span was not weighted: %q", lines[2])
	}
	// A fenced block is quoted rather than written, and a hash inside one is
	// not a heading. Nothing in it is dressed at all.
	for _, line := range lines[12:15] {
		if strings.Contains(line, "\x1b[") {
			t.Fatalf("fenced text was dressed: %q", line)
		}
	}
}

// Everything that is not a terminal, and every terminal that asked for no
// dressing, reads the reply as it was written.
func TestReplyIsThePlainReplyWhereNothingMayBeDressed(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]map[string]string{
		"no colour":   {"NO_COLOR": "1", "TERM": "xterm-256color"},
		"dumb":        {"TERM": "dumb"},
		"unstated":    {},
		"not a theme": nil,
	} {
		theme := Theme{}
		if env != nil {
			theme = NewTheme(environment(env), nil)
		}
		if dressed := theme.Reply(markdownReply); dressed != markdownReply {
			t.Fatalf("%s: reply was dressed: %q", name, dressed)
		}
	}
}

// A state is coloured once, here, so the same state cannot read one way in one
// report and another way in the next. The words say the state either way.
func TestStateColoursAreDistinctAndOptional(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), nil)
	seen := map[string]State{}
	for _, state := range []State{StateRunning, StateDone, StateFailed} {
		dressed := theme.State(state, "blocked (2):")
		if !strings.HasSuffix(dressed, resetColour) || !strings.Contains(dressed, "blocked (2):") {
			t.Fatalf("%s = %q", state, dressed)
		}
		colour := strings.TrimSuffix(dressed, resetColour+"blocked (2):"+resetColour)
		if previous, already := seen[colour]; already {
			t.Fatalf("%s and %s are the same colour", state, previous)
		}
		seen[colour] = state
	}
	if undressed := (Theme{}).State(StateBlocked, "blocked (2):"); undressed != "blocked (2):" {
		t.Fatalf("a theme that dresses nothing coloured a state: %q", undressed)
	}
}

// A survey the harness is answering with is dressed twice: once for the states
// in it and once as the harness speaking. The inner colour has to survive, and
// so does the outer one after it.
func TestDressingSurvivesInsideDressing(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), nil)
	var out strings.Builder
	harness := theme.Harness(&out)
	if _, err := harness.Write([]byte(theme.State(StateBlocked, "blocked (1):") + " and the rest\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	written := out.String()
	if !strings.Contains(written, blockedDeep+"blocked (1):"+resetColour+harnessDeep) {
		t.Fatalf("the harness colour did not resume after the state: %q", written)
	}
	if stripped := escapes.ReplaceAllString(written, ""); stripped != "blocked (1): and the rest\n" {
		t.Fatalf("dressing changed the text: %q", stripped)
	}
}
