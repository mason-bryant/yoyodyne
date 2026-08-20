package contextbundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

const defaultMaxBytes = 256 << 10

// maxExcerptSourceBytes bounds the file an excerpt is chosen from. Choosing
// sections of a document means reading the whole of it, and past this size that
// is no longer the cheap answer the excerpt exists to be, so the reference is
// stated as left out instead.
const maxExcerptSourceBytes = 8 << 20

// minExcerptBytes is how little room leaves an excerpt not worth making. Below
// it what survives is a heading and half a sentence, which reads as the document
// rather than as a fragment of one.
const minExcerptBytes = 512

// maxElisionBytes bounds one marker saying what an excerpt left out, so what the
// markers can cost is charged before the sections are chosen rather than
// discovered once they are rendered.
const maxElisionBytes = 96

// minTermBytes is how short a word is dropped from what a work item is about.
// There is no stopword list behind it: what makes a common word harmless is that
// it appears in every section, and the weighting below already discounts a term
// for exactly that.
const minTermBytes = 4

type Request struct {
	RepositoryRoot string
	WorkItem       beads.WorkItem
	References     []string
	MaxBytes       int
}

type Reference struct {
	Path    string
	Content string
	// Excerpted says Content is part of the file rather than the whole of it,
	// because the whole of it did not fit the context budget. A reference listed
	// here as if it were the file would be a smaller version of the silence the
	// excerpt exists to avoid.
	Excerpted bool
}

type Bundle struct {
	Text       string
	References []Reference
	Bytes      int
	// SpecificationProblems is set by AssembleProduct alone: it names the
	// specifications that were included despite not following the required
	// structure, so a caller can report them to the operator as well as to the
	// product manager.
	SpecificationProblems []SpecificationProblem
	// SpecificationsIncluded counts the specifications among the references,
	// which the references alone no longer answer: a role that reads its own
	// documents as well has references whether or not the product records any
	// intent at all, and "this repository has no brief or goals" is exactly the
	// thing a caller must still be able to say.
	SpecificationsIncluded int
}

var markdownReferencePattern = regexp.MustCompile(`[A-Za-z0-9._/-]+\.md`)

func Assemble(request Request) (Bundle, error) {
	if request.WorkItem.ID == "" {
		return Bundle{}, errors.New("work item is required")
	}
	root, err := filepath.Abs(request.RepositoryRoot)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	maxBytes := request.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBytes < 1 {
		return Bundle{}, errors.New("max context bytes must be greater than zero")
	}

	referencePaths := append([]string(nil), request.References...)
	implicitReferences, err := existingImplicitReferences(root, ExtractMarkdownReferences(request.WorkItem))
	if err != nil {
		return Bundle{}, err
	}
	referencePaths = append(referencePaths, implicitReferences...)
	referencePaths = uniqueSorted(referencePaths)

	base := renderWorkItem(request.WorkItem)
	if len(base) > maxBytes {
		return Bundle{}, fmt.Errorf("work item context is %d bytes, exceeding limit %d", len(base), maxBytes)
	}
	bundle := Bundle{Bytes: len(base)}
	var output bytes.Buffer
	output.WriteString(base)

	// What the statements of omission can cost is reserved before any reference is
	// read and released as each one is decided. Without it the reference that
	// filled the budget would be the one whose omission there was no room left to
	// state, which is the one thing this context must never be silent about.
	pendingNotes := 0
	for _, referencePath := range referencePaths {
		pendingNotes += len(renderOmittedReference(referencePath))
	}

	for _, referencePath := range referencePaths {
		pendingNotes -= len(renderOmittedReference(referencePath))
		section, reference, err := referenceSection(root, referencePath, request.WorkItem, maxBytes-bundle.Bytes-pendingNotes)
		if err != nil {
			return Bundle{}, err
		}
		output.WriteString(section)
		bundle.Bytes += len(section)
		if reference.Path != "" {
			bundle.References = append(bundle.References, reference)
		}
	}
	bundle.Text = output.String()
	bundle.Bytes = len(bundle.Text)
	return bundle, nil
}

func ExtractMarkdownReferences(item beads.WorkItem) []string {
	text := strings.Join([]string{item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n")
	matches := markdownReferencePattern.FindAllString(text, -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		// URL matches begin at the authority separator (for example,
		// //example.com/design.md). Absolute paths are not valid implicit
		// repository references either, so exclude both before resolution.
		if strings.HasPrefix(match, "/") {
			continue
		}
		references = append(references, match)
	}
	return uniqueSorted(references)
}

func existingImplicitReferences(root string, referencePaths []string) ([]string, error) {
	existing := make([]string, 0, len(referencePaths))
	for _, referencePath := range referencePaths {
		clean, err := validateReferencePath(referencePath)
		if err != nil {
			return nil, err
		}
		_, err = os.Lstat(filepath.Join(root, clean))
		if errors.Is(err, os.ErrNotExist) {
			// Work-item prose can name a Markdown deliverable that does not
			// exist yet. Only explicit Request.References are required inputs.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect implicit reference %q: %w", referencePath, err)
		}
		existing = append(existing, referencePath)
	}
	return existing, nil
}

func readReference(root, referencePath string, remainingBytes int) (Reference, error) {
	resolved, err := resolveReference(root, referencePath)
	if err != nil {
		return Reference{}, err
	}
	if resolved.size > int64(remainingBytes) {
		return Reference{}, tooLargeError{path: referencePath, remainingBytes: remainingBytes}
	}
	data, err := readBounded(resolved, referencePath, remainingBytes)
	if err != nil {
		return Reference{}, err
	}
	if len(data) > remainingBytes {
		return Reference{}, tooLargeError{path: referencePath, remainingBytes: remainingBytes}
	}
	return Reference{Path: resolved.path, Content: string(data)}, nil
}

// resolvedReference is a reference that has been proven to be a regular file
// inside the repository, and how big it is. It is separated from reading one
// because a reference that does not fit is still read — as an excerpt — and
// deciding that twice from two resolutions is deciding it about two files.
type resolvedReference struct {
	// path is the repository-relative path, which is what the reader is shown.
	path string
	// location is the resolved path on disk, which is what is opened.
	location string
	size     int64
}

func resolveReference(root, referencePath string) (resolvedReference, error) {
	clean, err := validateReferencePath(referencePath)
	if err != nil {
		return resolvedReference{}, err
	}
	path := filepath.Join(root, clean)
	location, err := filepath.EvalSymlinks(path)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("resolve reference %q: %w", referencePath, err)
	}
	relative, err := filepath.Rel(root, location)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("verify reference %q: %w", referencePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return resolvedReference{}, fmt.Errorf("reference %q resolves outside the repository", referencePath)
	}
	info, err := os.Stat(location)
	if err != nil {
		return resolvedReference{}, fmt.Errorf("stat reference %q: %w", referencePath, err)
	}
	if !info.Mode().IsRegular() {
		return resolvedReference{}, fmt.Errorf("reference %q is not a regular file", referencePath)
	}
	return resolvedReference{path: filepath.ToSlash(relative), location: location, size: info.Size()}, nil
}

// readBounded reads at most one byte past the budget, so a file that grew
// between being sized and being read is caught by the caller rather than
// spending whatever it has become.
func readBounded(reference resolvedReference, referencePath string, limitBytes int) ([]byte, error) {
	file, err := os.Open(reference.location)
	if err != nil {
		return nil, fmt.Errorf("open reference %q: %w", referencePath, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limitBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read reference %q: %w", referencePath, err)
	}
	return data, nil
}

// referenceSection renders one reference into a work-item context and reports
// what of it was carried. A reference that fits is carried whole; one that does
// not is excerpted to the sections that look most relevant to the work item, or,
// where not even an excerpt fits, stated as left out.
//
// Nothing here fails the bundle for size. The file is in the worktree whatever
// this context carries, so a reference that does not fit is unread rather than
// unreachable — and failing would fail every work item whose prose or
// append-only notes happen to name a document that has since grown past the
// budget, which is not something the item can be edited out of.
//
// The returned Reference has no path when nothing of the file was carried.
func referenceSection(root, referencePath string, item beads.WorkItem, available int) (string, Reference, error) {
	resolved, err := resolveReference(root, referencePath)
	if err != nil {
		return "", Reference{}, err
	}
	header := fmt.Sprintf("\n## Referenced file: %s\n\n", resolved.path)
	// The trailing newline the section is terminated with is part of what it
	// costs, so it is charged here rather than discovered afterwards.
	budget := available - len(header) - 1
	if resolved.size <= int64(budget) {
		data, err := readBounded(resolved, referencePath, budget)
		if err != nil {
			return "", Reference{}, err
		}
		if len(data) <= budget {
			reference := Reference{Path: resolved.path, Content: string(data)}
			return header + terminated(reference.Content), reference, nil
		}
	}
	return excerptSection(resolved, referencePath, item, available)
}

// excerptSection renders what a reference too large for the budget can still
// contribute: an excerpt of the sections most relevant to the work item, or the
// statement that it was left out. The omission names the path the work item
// wrote rather than the resolved one, because that is what a developer will look
// for in the worktree.
func excerptSection(resolved resolvedReference, referencePath string, item beads.WorkItem, available int) (string, Reference, error) {
	omission := renderOmittedReference(referencePath)
	if resolved.size > maxExcerptSourceBytes {
		return omission, Reference{}, nil
	}
	header := fmt.Sprintf("\n## Referenced file: %s (excerpt)\n\n", resolved.path)
	notice := renderExcerptNotice(resolved.path, resolved.size)
	budget := available - len(header) - len(notice) - 1
	if budget < minExcerptBytes {
		return omission, Reference{}, nil
	}
	data, err := readBounded(resolved, referencePath, maxExcerptSourceBytes)
	if err != nil {
		return "", Reference{}, err
	}
	body := excerpt(string(data), item, budget)
	if body == "" {
		return omission, Reference{}, nil
	}
	reference := Reference{Path: resolved.path, Content: body, Excerpted: true}
	return header + notice + terminated(body), reference, nil
}

func terminated(content string) string {
	if strings.HasSuffix(content, "\n") {
		return content
	}
	return content + "\n"
}

func renderExcerptNotice(path string, size int64) string {
	return fmt.Sprintf(`%s is %d bytes and does not fit in this context. What follows is an excerpt:
the sections of it that look most relevant to this work item, in the file's own
order, with a marker wherever something was left out. The whole file is in the
worktree — read it there rather than reading this as all of it.

`, path, size)
}

func renderOmittedReference(referencePath string) string {
	return fmt.Sprintf(`
## Referenced file: %s (omitted)

%s was referenced but exceeds the context budget; consult it in the worktree.
`, referencePath, referencePath)
}

// tooLargeError reports a reference that did not fit in the remaining budget.
// It is a distinct type because a product document that does not fit is
// reported to the reader as omitted rather than as an error. A work-item
// reference that does not fit is neither: it is excerpted or stated as left
// out, and never reaches an error at all.
type tooLargeError struct {
	path           string
	remainingBytes int
}

func (e tooLargeError) Error() string {
	return fmt.Sprintf("reference %q exceeds remaining context limit of %d bytes", e.path, e.remainingBytes)
}

// excerpt chooses as much of an oversized document as the budget holds: whole
// sections, ranked by how much of what the work item is about they carry, and
// rendered back in the document's own order so what survives still reads as part
// of the file. It returns "" when there is nothing worth carrying, and the
// reference is stated as left out instead.
//
// The excerpt is bounded by construction rather than by measuring afterwards:
// every kept section is charged one elision marker, and one more is reserved for
// a document that ends in a gap, so the markers can never push the rendering
// past what was budgeted for it.
func excerpt(content string, item beads.WorkItem, budget int) string {
	sections := splitSections(content)
	if len(sections) < 2 {
		// A document with no headings is one section, and one section that did not
		// fit is nothing to choose between. Cutting it at an arbitrary byte would
		// hand the reader a document that stops mid-sentence and says so nowhere,
		// so it is stated as left out instead.
		return ""
	}
	budget -= maxElisionBytes
	scores := sectionScores(sections, workItemTerms(item))
	ranked := make([]int, len(sections))
	for index := range ranked {
		ranked[index] = index
	}
	// Stable, so sections nothing distinguishes stay in the document's order
	// rather than in whichever one the sort happened to leave them.
	sort.SliceStable(ranked, func(i, j int) bool { return scores[ranked[i]] > scores[ranked[j]] })

	kept := make([]bool, len(sections))
	spent, carried := 0, 0
	// The opening of the document is offered first whatever it scores: it says
	// what the file is, and an excerpt that starts mid-document reads as a
	// different document rather than as part of one.
	for _, index := range append([]int{0}, ranked...) {
		if kept[index] {
			continue
		}
		cost := len(sections[index]) + maxElisionBytes
		if spent+cost > budget {
			continue
		}
		kept[index] = true
		spent += cost
		carried += len(sections[index])
	}
	if carried < minExcerptBytes {
		// What fit is the document's title and little else, which tells the reader
		// nothing while looking like something. Saying the file was left out is the
		// more useful of the two.
		return ""
	}
	return renderExcerpt(sections, kept)
}

// splitSections cuts a Markdown document at its headings. Each section carries
// its own heading, and whatever precedes the first one is a section of its own.
// Headings inside a fenced block are not headings: a configuration example
// showing a commented line would otherwise cut the section describing it in two.
func splitSections(content string) []string {
	var sections []string
	var current strings.Builder
	inFence := false
	for _, raw := range strings.SplitAfter(content, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "```"), strings.HasPrefix(line, "~~~"):
			inFence = !inFence
		case !inFence && headingPattern.MatchString(line) && current.Len() > 0:
			sections = append(sections, current.String())
			current.Reset()
		}
		current.WriteString(raw)
	}
	if current.Len() > 0 {
		sections = append(sections, current.String())
	}
	return sections
}

// workItemTerms is the vocabulary the work item is about, which is what decides
// which sections of an oversized document are worth carrying.
func workItemTerms(item beads.WorkItem) map[string]struct{} {
	text := strings.ToLower(strings.Join([]string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}, "\n"))
	terms := make(map[string]struct{})
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(word) < minTermBytes {
			continue
		}
		terms[word] = struct{}{}
	}
	return terms
}

// sectionScores weighs each section by the work item's terms it carries, and
// weighs each term by how few sections hold it. That weighting is what stands in
// for a list of words to ignore: a term in every section says nothing about
// which section to keep, and is discounted to almost nothing without anybody
// having to have written it down as common.
func sectionScores(sections []string, terms map[string]struct{}) []float64 {
	lowered := make([]string, len(sections))
	for index, section := range sections {
		lowered[index] = strings.ToLower(section)
	}
	scores := make([]float64, len(sections))
	for term := range terms {
		var carrying []int
		for index, section := range lowered {
			if strings.Contains(section, term) {
				carrying = append(carrying, index)
			}
		}
		if len(carrying) == 0 {
			continue
		}
		weight := math.Log(float64(len(sections)+1) / float64(len(carrying)))
		for _, index := range carrying {
			scores[index] += weight
		}
	}
	return scores
}

// renderExcerpt writes the kept sections in the document's order, saying at each
// gap how much of the file is not there. A gap left unmarked is what would let
// an excerpt be read as the whole document.
func renderExcerpt(sections []string, kept []bool) string {
	var rendered strings.Builder
	skipped := 0
	for index, section := range sections {
		if !kept[index] {
			skipped++
			continue
		}
		if skipped > 0 {
			rendered.WriteString(renderElision(skipped))
			skipped = 0
		}
		rendered.WriteString(section)
	}
	if skipped > 0 && rendered.Len() > 0 {
		rendered.WriteString(renderElision(skipped))
	}
	return rendered.String()
}

func renderElision(sections int) string {
	return fmt.Sprintf("\n[%d section(s) of this file are not included in this excerpt]\n\n", sections)
}

func validateReferencePath(referencePath string) (string, error) {
	clean := filepath.Clean(referencePath)
	if filepath.IsAbs(referencePath) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference %q must be a repository-relative path", referencePath)
	}
	if strings.ToLower(filepath.Ext(clean)) != ".md" {
		return "", fmt.Errorf("reference %q must be a Markdown file", referencePath)
	}
	return clean, nil
}

func renderWorkItem(item beads.WorkItem) string {
	return fmt.Sprintf(`# Assigned work item

ID: %s
Title: %s
Status: %s

## Description

%s

## Design guidance

%s

## Acceptance criteria

%s

## Notes

%s
`, item.ID, item.Title, item.Status, emptyFallback(item.Description), emptyFallback(item.Design), emptyFallback(item.AcceptanceCriteria), emptyFallback(item.Notes))
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not provided."
	}
	return value
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
