// Package terms is the register of coined words this project's governed
// documents are allowed to use, and the check that one already swept out has not
// come back with nothing defining it.
//
// The rule it serves is the legibility goal's: user-facing language chooses the
// ordinary, literal word over metaphor, coinage, or term of art — unless the
// term is defined in `docs/terms.md`. Registration is the whole of the
// exception. A word that names a real mechanism an operator meets in command
// output is worth keeping and cheap to define; a word that decorates a sentence
// nothing else in it needed is worth replacing. What is not acceptable is the
// third case, which is what this check exists for: a coinage in an
// operator-facing document with no plain-word definition anywhere, so a reader
// meeting it has nowhere to go.
//
// The vocabulary below is not a guess at what a coinage looks like. It is the
// inventory the yoyodyne-ifd.206 sweep measured, term by term, with the ordinary
// wording that sweep recorded for each. That is deliberately a closed list: no
// deterministic check can tell a word somebody coined this morning from an
// ordinary one, so this is the mechanical floor for the terms already known, and
// a new coinage is caught by a reviewer rather than here. What the floor buys is
// that the sweep does not have to be run twice — a term taken out of these
// documents cannot quietly come back, and a term kept has to say what it means.
//
// The list is closed but the spelling is not. A term is looked for however its
// parts are spaced — hyphenated, spaced, closed up, or broken by a line wrap —
// because those are one word to a reader and a check that knew only the spelling
// the sweep recorded would be a floor a hyphen walks over. `minute-zero` did
// exactly that to the sweep and to the first version of this check, which is why
// pattern is written the way it is.
//
// Where the register decides and the check only reports: adding an entry to
// `docs/terms.md` permits a term, and removing the entry forbids it again,
// neither of which is a change to this package. A term nothing here lists is
// registrable too — the register is the authority, and the vocabulary is only
// the set the check knows to look for.
package terms

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// RegisterPath is where the register lives, repository-relative. It sits outside
// the artifact homes on purpose: it is a reference every role reads and no role
// owns as an artifact, and putting it inside one would make each entry an
// amendment to somebody's document.
const RegisterPath = "docs/terms.md"

// RegisterHeading is the heading whose table is the register. The rows are read
// from under one heading rather than from every table in the file, because the
// document also lists the words that were replaced rather than registered, and a
// parser that took every table would register exactly the terms the sweep took
// out.
const RegisterHeading = "## The register"

// Homes are the document trees this check reads: the artifact homes the sweep
// covered. The guides under `docs/` are operator-facing too and are deliberately
// not read here — the sweep measured the homes, and holding documents to an
// inventory nobody has taken over them would fail on words nobody was asked
// about.
var Homes = []string{"docs/product", "docs/designs", "docs/decisions"}

// MaxFileBytes bounds one document read, for the reason internal/doclink bounds
// its own: a walk that meets something enormous reports it rather than reading
// it into memory.
const MaxFileBytes = 4 << 20

// Coinage is one word the sweep found and wrote an ordinary wording for.
type Coinage struct {
	// Term is the word as the register names it, which is what an entry has to
	// say for this term to be permitted.
	Term string
	// Match is what the scan looks for at a word start, which is the stem rather
	// than the term where a term inflects: `wedge` catches `wedged` and `starv`
	// catches `starving`, the same way the sweep counted them. Where the stem is
	// written in more than one part, every spacing of those parts is looked for
	// rather than only the one written here — see pattern.
	Match string
	// Whole holds the match to a whole word, for a term whose stem also begins an
	// ordinary word that is not it. `seamless` is not `seam`, and reporting it
	// with `seam`'s wording would tell an author to name a boundary in a sentence
	// that has none. The cost is that a plural is missed, which is the cheaper of
	// the two mistakes: a term this check misses a reviewer still catches, and a
	// term it reports wrongly is a check people learn to argue with.
	Whole bool
	// PlainWords is the ordinary wording the audit recorded, carried here so a
	// failure says what to write instead rather than only what is wrong.
	PlainWords string
}

// Vocabulary is the sweep's inventory: the thirteen words it called decoration,
// `cadence` which it named in passing as one more, and the six mechanism names
// it measured for the architect. Every one is recorded in
// `docs/diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md` with the evidence
// behind it.
var Vocabulary = []Coinage{
	{Term: "brake", Match: "brake", PlainWords: "the automatic stop after a set number of blocked runs in a row"},
	{Term: "cadence", Match: "cadence", PlainWords: "how often it repeats"},
	{Term: "docket", Match: "docket", PlainWords: "the list of stopped runs waiting on the development manager"},
	{Term: "handback", Match: "handback", PlainWords: "handing the work back to the developer that made it"},
	{Term: "heartbeat", Match: "heartbeat", PlainWords: "how often to repeat"},
	{Term: "in force", Match: "in force", PlainWords: "active, or still applies"},
	{Term: "minute zero", Match: "minute zero", PlainWords: "before development begins"},
	{Term: "pane of glass", Match: "pane of glass", PlainWords: "one window"},
	{Term: "posture", Match: "posture", PlainWords: "which tools a role may use"},
	{Term: "re-arm", Match: "re-arm", PlainWords: "repeat the merge request"},
	{Term: "seam", Match: "seam", Whole: true, PlainWords: "name the boundary instead — what attaches to what"},
	{Term: "sidecar", Match: "sidecar", PlainWords: "a separate directory outside the repository"},
	{Term: "sink", Match: "sink", PlainWords: "the process that posts to Slack"},
	{Term: "soak", Match: "soak", PlainWords: "a trial run kept alongside the old path for comparison"},
	{Term: "starving", Match: "starv", PlainWords: "stopping"},
	{Term: "steer", Match: "steer", PlainWords: "direct, or change what is being worked on"},
	{Term: "supersession pile", Match: "supersession pile", PlainWords: "the list of superseded pull requests"},
	{Term: "tranche", Match: "tranche", PlainWords: "stage, or part 1 of 4"},
	{Term: "wedged", Match: "wedge", PlainWords: "stuck, or say the condition outright"},
	{Term: "whose-move", Match: "whose-move", PlainWords: "waiting on you"},
}

// Entry is one row of the register: a term, what it means in ordinary words, and
// where a reader meets it.
type Entry struct {
	Term       string `json:"term"`
	PlainWords string `json:"plainWords"`
	Used       string `json:"used"`
	// Line is where the row is written, so a malformed one can be opened rather
	// than searched for.
	Line int `json:"line"`
}

// Problem is one thing wrong: a coinage no entry defines, or an entry that does
// not define one.
type Problem struct {
	// Path is the repository-relative document and Line the physical line, because
	// what somebody has to open is the place the word is written.
	Path string `json:"path"`
	Line int    `json:"line"`
	// Term is the word this is about, so what is reported can be searched for.
	Term   string `json:"term"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Reason)
}

// Register reads the entries the register states, in the order it states them.
//
// A register that cannot be read is an error rather than an empty set: every
// term is undefined against an empty register, so a missing file would otherwise
// be reported as the documents being full of undefined coinage, which sends
// whoever reads it to the wrong place entirely.
func Register(root string) ([]Entry, error) {
	content, err := read(filepath.Join(root, filepath.FromSlash(RegisterPath)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegisterPath, err)
	}
	var entries []Entry
	within := false
	for index, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			within = line == RegisterHeading
			continue
		}
		if !within || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := tableCells(line)
		if len(cells) < 3 || !strings.HasPrefix(cells[0], "`") {
			// The header names the columns and the separator draws the rule under
			// them; neither is an entry, and neither writes its first cell as code.
			continue
		}
		entries = append(entries, Entry{
			Term:       strings.ToLower(strings.Trim(cells[0], "`")),
			PlainWords: cells[1],
			Used:       cells[2],
			Line:       index + 1,
		})
	}
	return entries, nil
}

// Documents is every Markdown file under the homes, repository-relative and in
// sorted order. It is exported for the reason internal/doclink exports its own:
// a walk that found nothing and a set of documents with nothing wrong in them
// are the same result otherwise.
func Documents(root string) ([]string, error) {
	var documents []string
	for _, home := range Homes {
		directory := filepath.Join(root, filepath.FromSlash(home))
		err := filepath.WalkDir(directory, func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			relative, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			documents = append(documents, filepath.ToSlash(relative))
			return nil
		})
		if err != nil {
			if os.IsNotExist(err) {
				// A home a project has not created is intent not yet written, not a
				// defect. The same judgement the goals check makes about an absent
				// artifact home.
				continue
			}
			return nil, fmt.Errorf("walk %s for documents: %w", home, err)
		}
	}
	sort.Strings(documents)
	return documents, nil
}

// Check reports every coinage in the governed homes that the register does not
// define, and every register entry that defines nothing. Problems come out in
// the order the register is written and then the order the documents are walked,
// so two runs over one checkout report the same thing in the same order.
func Check(root string) ([]Problem, error) {
	entries, err := Register(root)
	if err != nil {
		return nil, err
	}
	problems, registered := checkRegister(entries)
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	patterns := make([]*regexp.Regexp, len(Vocabulary))
	for index, coinage := range Vocabulary {
		patterns[index] = pattern(coinage)
	}
	for _, document := range documents {
		content, err := read(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", document, err)
		}
		problems = append(problems, problemsIn(document, content, patterns, registered)...)
	}
	return problems, nil
}

// checkRegister holds the register to its own shape and reports which terms it
// permits. An entry missing either half of what an entry is for — the plain
// words, or where the word is met — is reported rather than silently permitting
// the term: an entry that defines nothing is the coinage with the appearance of
// having been registered, which is worse than no entry at all.
func checkRegister(entries []Entry) ([]Problem, map[string]bool) {
	var problems []Problem
	registered := make(map[string]bool, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Term == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Reason: "a register entry names no term"})
			continue
		case registered[entry.Term]:
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered twice; the register holds one entry per term", entry.Term)})
			continue
		case entry.PlainWords == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered with no plain-word definition, which is the whole of what an entry is for", entry.Term)})
		case entry.Used == "":
			problems = append(problems, Problem{Path: RegisterPath, Line: entry.Line, Term: entry.Term,
				Reason: fmt.Sprintf("%q is registered without saying where it is used, so a reader cannot tell whether it still is", entry.Term)})
		}
		registered[entry.Term] = true
	}
	return problems, registered
}

// fencePattern matches the line that opens or closes a fenced code block. A term
// inside one is a command, an identifier, or a script the sweep's own document
// quotes, rather than prose somebody reads for its meaning.
var fencePattern = regexp.MustCompile("^(?:```|~~~)")

// termParts splits a match into the parts a writer can space differently.
var termParts = regexp.MustCompile(`[-\s]+`)

// pattern is what one term is looked for as: its parts, at a word start, spaced
// however whoever wrote the sentence spaced them.
//
// A term written in more than one part is one word to a reader however it was
// typed. `minute zero`, `minute-zero`, and a `minute` that a line wrap left at
// the end of one line with its `zero` at the start of the next are the same
// coinage, and a check that found only the spelling the register happens to use
// is a floor with a hole in it — `minute-zero` is written in an active
// invariant and escaped the sweep and the first version of this check alike.
// Whichever spelling is found, the failure names the term as the register
// spells it, because what the reader needs is the wording to write rather than
// the respelling they used.
//
// The tolerance is only where the term is already divided. A term the register
// writes as one word is looked for as one word: `hand back` is ordinary English
// in sentences that have nothing to do with `handback`, and reporting those is
// the mistake Whole already exists to avoid — a term this check misses a
// reviewer still catches, and a term it reports wrongly is a check people learn
// to argue with.
func pattern(coinage Coinage) *regexp.Regexp {
	parts := termParts.Split(coinage.Match, -1)
	for index, part := range parts {
		parts[index] = regexp.QuoteMeta(part)
	}
	// The separator is optional rather than required, so the parts closed up into
	// one word — `rearm` for `re-arm` — is found too.
	expression := `(?i)\b` + strings.Join(parts, `[-\s]*`)
	if coinage.Whole {
		expression += `\b`
	}
	return regexp.MustCompile(expression)
}

// passage is one stretch of a document that is read for coinage: consecutive
// body lines, trimmed and joined, with the line number the stretch starts at.
//
// The document is read in stretches rather than a line at a time because a term
// of more than one word is as easily broken by a line wrap as by a hyphen, and
// neither makes it a different word. What ends a stretch is what a term cannot
// wrap across: a blank line, a fence, the fenced lines themselves, and the
// frontmatter. So `minute` at the end of one line and `zero` at the start of the
// next is found, and the last word of one paragraph followed by the first word
// of the next is not.
type passage struct {
	first int
	text  string
}

// lineOf is the physical line a match at this offset falls on.
func (p passage) lineOf(offset int) int {
	return p.first + strings.Count(p.text[:offset], "\n")
}

// passages is the stretches of one document's body, in the order they are
// written.
func passages(lines []string) []passage {
	var stretches []passage
	var current []string
	first := 0
	fenced := false
	end := func() {
		if len(current) > 0 {
			stretches = append(stretches, passage{first: first, text: strings.Join(current, "\n")})
			current = nil
		}
	}
	for index := skipFrontmatter(lines); index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if fencePattern.MatchString(line) {
			fenced = !fenced
			end()
			continue
		}
		if fenced || line == "" {
			end()
			continue
		}
		if len(current) == 0 {
			first = index + 1
		}
		current = append(current, line)
	}
	end()
	return stretches
}

// problemsIn reports the unregistered coinage one document uses.
//
// Two parts of a document are deliberately not read. The frontmatter carries the
// identity and the revision history, and a revision's recorded reason is what
// somebody decided in their own words on a date — rewriting one to change a word
// falsifies a record rather than clarifying a sentence. Fenced blocks are code.
func problemsIn(document, content string, patterns []*regexp.Regexp, registered map[string]bool) []Problem {
	var problems []Problem
	for _, stretch := range passages(strings.Split(content, "\n")) {
		// Collected by line first and reported afterwards, so problems come out in
		// the order the document is written whichever term was searched for first,
		// and a term written twice on one line is one problem rather than two.
		found := make(map[int]map[int]bool)
		for position, coinage := range Vocabulary {
			if registered[coinage.Term] {
				continue
			}
			for _, match := range patterns[position].FindAllStringIndex(stretch.text, -1) {
				line := stretch.lineOf(match[0])
				if found[line] == nil {
					found[line] = make(map[int]bool)
				}
				found[line][position] = true
			}
		}
		for line := stretch.first; line <= stretch.first+strings.Count(stretch.text, "\n"); line++ {
			for position, coinage := range Vocabulary {
				if !found[line][position] {
					continue
				}
				problems = append(problems, Problem{
					Path: document, Line: line, Term: coinage.Term,
					Reason: fmt.Sprintf("%q is a coined term with no entry in %s; write %s, or register the term with a plain-word definition",
						coinage.Term, RegisterPath, coinage.PlainWords),
				})
			}
		}
	}
	return problems
}

// skipFrontmatter is the first line of the document proper: the line after the
// closing `---` where a document opens with frontmatter, and the first line
// otherwise. A document that opens a frontmatter block and never closes it is
// read whole, because the alternative is a check that reads nothing at all in a
// document whose first line is a horizontal rule.
func skipFrontmatter(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return index + 1
		}
	}
	return 0
}

// tableCells is one Markdown table row's cells, trimmed, without the empty
// strings the leading and trailing pipes produce.
func tableCells(line string) []string {
	cells := strings.Split(strings.Trim(line, "|"), "|")
	for index, cell := range cells {
		cells[index] = strings.TrimSpace(cell)
	}
	return cells
}

// read is one document, refused rather than loaded if it is larger than a
// document has any business being.
func read(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxFileBytes {
		return "", fmt.Errorf("%d bytes is larger than the %d this check reads", info.Size(), MaxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
