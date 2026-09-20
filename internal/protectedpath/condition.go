package protectedpath

// A done-condition no developer run may satisfy.
//
// The gate in protectedpath.go refuses a developer's diff that touches an
// artifact home the item did not grant. What it cannot refuse is the item
// itself: a done-condition that names a design, a decision record, or a product
// artifact — "the design's query list marks the query as existing", "reconcile
// the design document's yoyo status entry", "her ruling is recorded on the
// design" — is a condition the run may not write the satisfaction of, and the
// run finds that out only by spending itself. Three items in one week did
// (yoyodyne-ifd.141.1, .63, and .68.25 before them): each ran, each parked or
// spent review rounds on the one clause no diff could meet, and each was fixed
// afterwards by the architect amending the document through the governed path.
// The development manager's 2026-09-03 checklist said where the fix belongs — a
// precondition or an edit the done-means implies is structure at admission, not
// a finding at review — and this is that structure.
//
// So an item's done-conditions are read where the item is written. A clause of
// the description's done-means or of the acceptance criteria that names a path
// under one of the artifact homes, or a document one of those homes owns, is
// refused unless the item grants that path — with the clause quoted and the fix
// named, which is one of two things: take the clause out and say the document's
// owner amends it, or carry the grant where one is permitted. The run's opening
// check asks the same question of the item text it is handed, so an item that
// acquired such a clause with the tracker's own command, or was admitted before
// this existed, is refused before it is claimed rather than parked after a run.
//
// # What is read, and what deliberately is not
//
// Only the done-conditions are read: the whole of the acceptance criteria, and
// in the description the sentences from a "Done means" (or "Done:", "done
// when") to the end of their paragraph. An item's prose cites these documents
// constantly — as the design the work builds against, the invariant it is held
// to, the decision that rules something out — and a citation is not a
// condition. Measured over the 596 items this repository's tracker held when
// this was written, reading every clause fires on 117 of them, a fifth of the
// backlog; reading the done-conditions as below fires on 10, one of them
// unfinished (yoyodyne-ifd.313, whose done-means records a rule "as a section
// of slack-reporting-design"), and each of the ten names a document as
// something the work leaves in a state. The one unfinished item that names a
// product path in its done-means and carries the grant for it
// (yoyodyne-ifd.262) passes.
//
// A document is named by its path, or by its id where the id is two words or
// more. A single-word id is left to the path: the brief's id is "brief", and a
// done-condition that says "the brief's acceptance criteria hold" is citing it,
// which the 17 items whose done-means say something of that kind bear out.
// Documents under the invariants directory are named by path only, for the
// reason the artifact store excludes them from its identity scheme: an invariant
// is delivered to every run by its id and cited by it in nearly every item, and
// a done-condition that edited one would name its path.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// Document is one document an artifact home owns, as an item may name it: by
// the path the file is at, or by the id the artifact store gives it, which is
// the file's own name.
type Document struct {
	ID   string
	Path string
}

// Homes are the artifact homes a done-condition may not reach into, and the
// documents they own. It is built from the configuration for the reason Set is:
// a project that keeps its designs somewhere else has not thereby made them a
// run's to satisfy a condition in.
type Homes struct {
	directories []string
	documents   []ownedDocument
}

// ownedDocument is a document with the shape its id is looked for in, compiled
// once when the homes are built rather than once per clause.
type ownedDocument struct {
	Document
	shape *regexp.Regexp
}

// ArtifactHomes builds the homes a configuration names — the product artifacts,
// the designs, the decision records, and the invariants — with whichever owned
// documents the caller read. The configuration directory is deliberately not
// among them: it is protected in a developer's diff, but a condition naming it
// is about the harness's own settings rather than about a document another role
// owns, and this gate is about the second.
func ArtifactHomes(cfg config.Config, documents ...Document) Homes {
	homes := Homes{}
	for _, directory := range []string{cfg.Product.Specifications, cfg.Product.Designs, cfg.Product.Decisions, cfg.Product.Invariants} {
		if clean, ok := normalize(directory); ok {
			homes.directories = appendUnique(homes.directories, clean)
		}
	}
	sort.Strings(homes.directories)
	for _, document := range documents {
		clean, ok := normalize(document.Path)
		if !ok {
			continue
		}
		id := strings.TrimSpace(document.ID)
		shape, named := idShape(id)
		if !named {
			continue
		}
		homes.documents = append(homes.documents, ownedDocument{Document: Document{ID: id, Path: clean}, shape: shape})
	}
	sort.Slice(homes.documents, func(i, j int) bool { return homes.documents[i].Path < homes.documents[j].Path })
	return homes
}

// idShape is the shape an id is looked for in, and whether the id is one that
// is looked for at all. An id is matched by its words in order, joined by a
// hyphen, an underscore, or a space, because prose names "the slack-reporting
// design" as readily as `slack-reporting-design` and both name the same file.
// A one-word id is never looked for: it is as likely to be the word as the
// document, and the path names the document unambiguously.
func idShape(id string) (*regexp.Regexp, bool) {
	words := strings.Split(id, "-")
	if len(words) < 2 {
		return nil, false
	}
	for i, word := range words {
		if word == "" {
			return nil, false
		}
		words[i] = regexp.QuoteMeta(word)
	}
	shape, err := regexp.Compile(`(?i)` + strings.Join(words, `[-_ ]`))
	if err != nil {
		return nil, false
	}
	return shape, true
}

// Empty reports homes with nothing to check against, which is what a caller
// that was wired none gets: such a caller refuses nothing, exactly as every
// admission did before this existed.
func (h Homes) Empty() bool {
	return len(h.directories) == 0
}

// OwnedDocuments reads the documents a repository's artifact homes own, as the
// artifact store identifies them, so what a done-condition is checked against is
// the same set every other reader of those homes sees. A document the store
// could not read as an artifact is still a document the home owns — a
// done-condition naming it is no more satisfiable for its frontmatter being
// wrong — so the store's problems are read for their paths as well.
func OwnedDocuments(repositoryRoot string, product config.Product) ([]Document, error) {
	set, err := artifact.StoreFor(repositoryRoot, product).Load()
	if err != nil {
		return nil, err
	}
	var documents []Document
	for _, recorded := range set.Artifacts {
		documents = append(documents, Document{ID: recorded.ID, Path: recorded.Path})
	}
	for _, problem := range set.Problems {
		documents = append(documents, Document{ID: idForPath(problem.Path), Path: problem.Path})
	}
	return documents, nil
}

// idForPath is the id a file in an artifact home answers to: its own name. It
// is the artifact store's rule, restated for the files the store refused.
func idForPath(relative string) string {
	base := relative
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return base
}

// Condition is one done-condition clause naming a document no grant admits.
type Condition struct {
	// Path is the repository-relative path the clause reaches into: the path it
	// wrote, or the path of the document it named by id.
	Path string
	// Named is what the clause wrote, as it wrote it.
	Named string
	// Clause is the clause itself, folded to one line and bounded.
	Clause string
}

// ConditionInstruction is what a refused role is told to do about it. It names
// both fixes because both are real: a condition that belongs to the document's
// owner comes out of the item, and a change somebody already decided is granted.
// It says who owns what only as far as the artifact ownership table does, and
// says the run's summary is how the owner learns what to record, because that
// is the path the three incidents above were each fixed along.
const ConditionInstruction = "No developer run may write there, so a run handed this condition spends itself and parks on it. " +
	"Either take the clause out of the done-condition and say that the document's owner — the architect for a design or a decision record, the product manager for a product artifact — amends the document through the governed path, with the run's summary naming what there is to record; " +
	"or, where a grant is permitted for that path because the change behind it is already decided, carry one on a line beginning \"" + GrantMarker + "\" that names it."

// Refusal is what admission says about one condition it will not admit: what the
// clause names and where that is, the clause itself, and what to do instead.
func (c Condition) Refusal() string {
	where := c.Path
	if c.Named != c.Path {
		where = fmt.Sprintf("%s (%s)", c.Named, c.Path)
	}
	return fmt.Sprintf("a done-condition names %s, which is under a protected artifact home and no %q line in the item admits: %q. %s",
		where, GrantMarker, c.Clause, ConditionInstruction)
}

// maxConditions bounds how many conditions one reading reports. Whoever is told
// has to rewrite the first of them, and the item is where the rest are.
const maxConditions = 5

// maxClauseBytes keeps one quoted clause to its part of one line. What is quoted
// came out of a role's reply, so it is folded rather than trusted to be short.
const maxClauseBytes = 200

// Ungranted reports the done-conditions in an item's description and acceptance
// criteria that name a path under these homes, or a document they own, without
// a grant covering it. An empty result is the ordinary answer: nearly every item
// names nothing of the kind in its done-conditions, and one whose every named
// document is granted is as clear as one that named none.
//
// The grants are passed rather than read here, because which of an item's
// fields they are read from is the caller's decision — the same four fields
// Grants is given, which the harness never writes into.
func (h Homes) Ungranted(description, acceptanceCriteria string, granted []string) []Condition {
	if h.Empty() {
		return nil
	}
	grants := normalizeAll(granted)
	var (
		conditions []Condition
		seen       = map[string]bool{}
	)
	note := func(path, named, clause string) {
		if within(path, grants) {
			return
		}
		key := path + "\x00" + clause
		if seen[key] {
			return
		}
		seen[key] = true
		conditions = append(conditions, Condition{Path: path, Named: named, Clause: fold(clause, maxClauseBytes)})
	}
	for _, span := range doneConditions(description, acceptanceCriteria) {
		for _, clause := range clauses(span) {
			for _, cited := range standingAlone(clause, writtenPath) {
				path, ok := normalize(strings.TrimRight(cited, trailingDecoration))
				if !ok || !within(path, h.directories) {
					continue
				}
				note(path, path, clause)
			}
			for _, document := range h.documents {
				for _, named := range standingAlone(clause, document.shape) {
					note(document.Path, named, clause)
					break
				}
			}
		}
	}
	if len(conditions) > maxConditions {
		conditions = conditions[:maxConditions]
	}
	return conditions
}

// ConditionProblems is Ungranted as the errors an admission joins, one per
// condition, each carrying its refusal. It is one predicate every door into the
// queue asks rather than each deciding for itself, for the reason GrantProblems
// is: a door that asked a weaker question is the door such an item would arrive
// through.
func (h Homes) ConditionProblems(description, acceptanceCriteria string, granted []string) []error {
	var problems []error
	for _, condition := range h.Ungranted(description, acceptanceCriteria, granted) {
		problems = append(problems, errors.New(condition.Refusal()))
	}
	return problems
}

// doneMarker is where a description's done-conditions begin. The phrasings are
// this repository's own: "Done means" opens the done-conditions of 418 of the
// 596 items the tracker held when this was written, and the other two are the
// forms the remainder used. A phrasing not here is a done-condition this does
// not read, which is the safe way round — the acceptance criteria field is read
// whole, and an item written with neither is one nothing here refuses.
var doneMarker = regexp.MustCompile(`(?i)\bdone means\b|\bdone:|\bdone when\b`)

// paragraphEnd is where a done-means paragraph stops: a blank line.
var paragraphEnd = regexp.MustCompile(`\n[ \t]*\n`)

// doneConditions is the spans of an item's text that state what done means:
// each done-means paragraph of the description, and the acceptance criteria
// whole, because every clause of that field is a condition by construction.
func doneConditions(description, acceptanceCriteria string) []string {
	var spans []string
	for _, at := range doneMarker.FindAllStringIndex(description, -1) {
		rest := description[at[0]:]
		if end := paragraphEnd.FindStringIndex(rest); end != nil {
			rest = rest[:end[0]]
		}
		spans = append(spans, rest)
	}
	if strings.TrimSpace(acceptanceCriteria) != "" {
		spans = append(spans, acceptanceCriteria)
	}
	return spans
}

// clauses splits a span at the boundaries a clause is written with: a
// semicolon, a line break, or a full stop that ends a sentence. A full stop
// inside a path or an id — "v1-harness-design.md", "yoyodyne-ifd.63" — is not a
// boundary, which is why the stop has to be followed by space or the end.
func clauses(span string) []string {
	var (
		found []string
		start int
	)
	for i := 0; i < len(span); i++ {
		switch span[i] {
		case ';', '\n':
		case '.':
			if i+1 < len(span) && span[i+1] != ' ' && span[i+1] != '\t' {
				continue
			}
		default:
			continue
		}
		if clause := strings.TrimSpace(span[start : i+1]); clause != "" {
			found = append(found, clause)
		}
		start = i + 1
	}
	if clause := strings.TrimSpace(span[start:]); clause != "" {
		found = append(found, clause)
	}
	return found
}

// writtenPath is a path as prose writes one: at least one directory and a
// slash, and whatever name follows, which may be empty for a home written with
// its trailing slash. The character class is what a repository path is made of,
// so a Markdown link's brackets and a sentence's closing punctuation fall
// outside it — except the full stop, which is inside paths and is trimmed from
// the end of a match by the caller.
var writtenPath = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]*`)

// standingAlone is the matches of one shape in the text that are not part of
// something longer: a path inside a longer path is the longer path's, and an id
// inside a longer identifier is not the id.
func standingAlone(text string, shape *regexp.Regexp) []string {
	var found []string
	for _, at := range shape.FindAllStringIndex(text, -1) {
		if continues(text, at[0]-1) || continues(text, at[1]) {
			continue
		}
		found = append(found, text[at[0]:at[1]])
	}
	return found
}

// continues reports the byte at this position being one a path or an id could
// carry, so a match with one beside it is part of something longer.
func continues(text string, at int) bool {
	if at < 0 || at >= len(text) {
		return false
	}
	character := text[at]
	return character == '_' || character == '/' || character == '-' ||
		(character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z')
}

// fold turns a clause into one bounded line, cut on a rune boundary, so what is
// quoted back is text whatever the clause carried.
func fold(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}
