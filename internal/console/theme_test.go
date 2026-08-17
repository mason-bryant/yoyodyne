package console

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// environment is the few variables a theme is decided by.
func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// TestTheEnvironmentDecidesWhetherAnythingIsDressed covers what an operator can
// say about their terminal. Each of these suppresses the colour and the rules
// together: both are decoration, and somebody who asked for undecorated output
// asked for all of it.
func TestTheEnvironmentDecidesWhetherAnythingIsDressed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		env     map[string]string
		dressed bool
		orange  string
	}{
		{name: "a colour terminal", env: map[string]string{"TERM": "xterm-256color"}, dressed: true, orange: questionDeep},
		{name: "a terminal that says truecolor", env: map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"}, dressed: true, orange: questionDeep},
		// Sixteen colours have no orange in them, so the nearest thing that
		// terminal can say is what it is asked for.
		{name: "a sixteen-colour terminal", env: map[string]string{"TERM": "xterm"}, dressed: true, orange: questionBasic},
		{name: "NO_COLOR", env: map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}},
		{name: "a dumb terminal", env: map[string]string{"TERM": "dumb"}},
		{name: "a terminal that will not say what it is", env: map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			theme := NewTheme(environment(test.env), func() int { return 40 })
			question := theme.Questions("What is missing from the brief?")
			rule := theme.Rule()
			if theme.Permitted() != test.dressed {
				t.Fatalf("Permitted() = %v, want %v", theme.Permitted(), test.dressed)
			}
			if !test.dressed {
				if question != "What is missing from the brief?" {
					t.Fatalf("a question was coloured anyway: %q", question)
				}
				if rule != "" {
					t.Fatalf("a rule was drawn anyway: %q", rule)
				}
				if dressed := theme.Proposal("proposal"); dressed != "proposal" {
					t.Fatalf("a proposal was coloured anyway: %q", dressed)
				}
				// The bell and the window title are escapes exactly as the colours
				// are, and they are suppressed with them rather than separately.
				if alert := theme.Alert("a run finished"); alert != "" {
					t.Fatalf("a bell rang anyway: %q", alert)
				}
				if title := theme.Title("a run finished"); title != "" {
					t.Fatalf("the window was renamed anyway: %q", title)
				}
				return
			}
			if alert := theme.Alert("a run finished"); alert != "\a\x1b]2;a run finished\a" {
				t.Fatalf("alert = %q", alert)
			}
			// A title outlives the process that set it, so a conversation has to be
			// able to put the window's name back.
			if title := theme.Title(""); title != "\x1b]2;\a" {
				t.Fatalf("emptying the title = %q", title)
			}
			if !strings.HasPrefix(question, test.orange) || !strings.HasSuffix(question, resetColour) {
				t.Fatalf("question = %q, want it wrapped in %q", question, test.orange)
			}
			if rule != strings.Repeat("─", 40)+"\n" {
				t.Fatalf("rule = %q", rule)
			}
			// The three kinds are told apart from each other, not only from
			// undressed text.
			if theme.Proposal("x") == theme.Questions("x?") || theme.Proposal("x") == dress(theme.harness, "x") {
				t.Fatal("two kinds of thing are dressed the same way")
			}
		})
	}
}

// TestARuleIsNeverWiderThanTheScreen keeps the separator one line. A rule that
// wraps reads as two rules, and one drawn at a width nobody reported would be a
// guess at how wide the screen is.
func TestARuleIsNeverWiderThanTheScreen(t *testing.T) {
	t.Parallel()

	colourful := environment(map[string]string{"TERM": "xterm-256color"})
	for _, test := range []struct {
		name  string
		width int
		want  int
	}{
		{name: "a narrow window", width: 24, want: 24},
		{name: "a very wide window", width: 400, want: ruleWidth},
		{name: "a terminal that will not say", width: 0, want: unknownWidth},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			rule := NewTheme(colourful, func() int { return test.width }).Rule()
			if rule != strings.Repeat("─", test.want)+"\n" {
				t.Fatalf("rule was %d wide, want %d", len([]rune(strings.TrimSuffix(rule, "\n"))), test.want)
			}
		})
	}
}

// TestColourIsAnAdditionAndNeverTheMeaning is the promise the whole theme rests
// on: strip every escape and what is left is exactly the text that was written.
// An operator reading a copied transcript, a `script` capture, or a terminal
// that lost its colours has lost decoration and nothing else.
func TestColourIsAnAdditionAndNeverTheMeaning(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), func() int { return 40 })
	reply := "Two goals, then.\n\nWhich of them matters first?\nSay so and I will file it."
	if stripped := escapes.ReplaceAllString(theme.Questions(reply), ""); stripped != reply {
		t.Fatalf("stripping colour changed the reply: %q", stripped)
	}
	// The question is the only line dressed, and the question mark that makes it
	// one is what says so without the colour.
	coloured := strings.Split(theme.Questions(reply), "\n")
	if strings.Contains(coloured[0], theme.question) || !strings.Contains(coloured[2], theme.question) {
		t.Fatalf("the wrong lines were coloured: %#v", coloured)
	}

	proposal := "  [1.1] Separate the operator's turn from the reply\n      why: it is unreadable otherwise\n"
	if stripped := escapes.ReplaceAllString(theme.Proposal(proposal), ""); stripped != proposal {
		t.Fatalf("stripping colour changed a proposal: %q", stripped)
	}

	var out strings.Builder
	harness := theme.Harness(&out)
	// Command output arrives in as many writes as the command needs, including
	// ones that do not end a line.
	report := []string{"in flight:\n", "  yoyodyne-1 ", "developing\n", "\n"}
	for _, piece := range report {
		count, err := harness.Write([]byte(piece))
		if err != nil || count != len(piece) {
			t.Fatalf("Write(%q) = %d, %v", piece, count, err)
		}
	}
	if stripped := escapes.ReplaceAllString(out.String(), ""); stripped != strings.Join(report, "") {
		t.Fatalf("stripping colour changed command output: %q", stripped)
	}
	// A colour never outlives the line it began on: the region below is redrawn
	// after every write, and a colour left open would dress it too.
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.Contains(line, theme.harness) && !strings.HasSuffix(line, resetColour) {
			t.Fatalf("a line was left wearing a colour: %q", line)
		}
	}
}

// A card is a frame around something the operator has to act on. Like the rule
// it is decoration: it makes the boundary between two of them findable, and
// everything that carries meaning is in the heading and the body, which is what
// is left when the frame is suppressed.
func TestACardFramesWithoutCarryingMeaning(t *testing.T) {
	t.Parallel()

	const heading = "1 · proposal 2.1 · Pause on a usage limit"
	body := "Wait and resume.\nwhy: Capacity is not failure."

	framed := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), func() int { return 30 }).Card(heading, body)
	want := strings.Join([]string{
		"╭─ " + heading + " ",
		"│ Wait and resume.",
		"│ why: Capacity is not failure.",
		"╰" + strings.Repeat("─", 29),
		"",
	}, "\n")
	if framed != want {
		t.Fatalf("framed card =\n%q\nwant\n%q", framed, want)
	}

	// Where nothing may be dressed, the same card is the heading with its body
	// indented under it, which is what one of these has always been on a stream.
	plain := Theme{}.Card(heading, body)
	if plain != heading+"\n    Wait and resume.\n    why: Capacity is not failure.\n" {
		t.Fatalf("undressed card = %q", plain)
	}
	// A heading too long for the screen still opens its card rather than being
	// cut: what it names matters more than the frame lining up.
	long := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), func() int { return 10 }).Card(heading, "body")
	if !strings.Contains(long, heading) {
		t.Fatalf("a long heading was lost: %q", long)
	}
}

// TestAWindowTitleCarriesNothingTheTerminalWouldInterpret is the one place here
// where the text is interpreted rather than printed. A run headline carries a
// work item's own title, so what goes into a title is whatever somebody typed
// into the tracker, and a sequence that ended the title early or began another
// one would be that text deciding what the terminal does.
func TestAWindowTitleCarriesNothingTheTerminalWouldInterpret(t *testing.T) {
	t.Parallel()

	theme := NewTheme(environment(map[string]string{"TERM": "xterm-256color"}), func() int { return 40 })
	for _, test := range []struct {
		name  string
		title string
		want  string
	}{
		{"an ordinary headline", "yoyodyne-1 was integrated into main", "yoyodyne-1 was integrated into main"},
		// The escape that opens a sequence and the bell that closes one are both
		// removed rather than escaped: a title is a few words, not a place to
		// preserve control characters faithfully.
		{"an escape in the middle", "yoyodyne-1\x1b]0;whoami\a done", "yoyodyne-1 ]0;whoami done"},
		{"a newline", "yoyodyne-1\nwas integrated", "yoyodyne-1 was integrated"},
		{"leading and trailing space", "  yoyodyne-1  done  ", "yoyodyne-1 done"},
		{"nothing at all", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := theme.Title(test.title); got != titleOpen+test.want+titleClose {
				t.Fatalf("Title(%q) = %q, want %q", test.title, got, titleOpen+test.want+titleClose)
			}
		})
	}

	// A title longer than a window has room for is cut on a rune boundary, so
	// what reaches the terminal is still text.
	long := theme.Title(strings.Repeat("é", 200))
	body := strings.TrimSuffix(strings.TrimPrefix(long, titleOpen), titleClose)
	if len(body) > maxTitleBytes || !utf8.ValidString(body) {
		t.Fatalf("a long title = %d bytes, valid = %v", len(body), utf8.ValidString(body))
	}
}
