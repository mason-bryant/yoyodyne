// Package doclink is the links a repository's documentation makes to itself,
// resolved rather than believed.
//
// The artifact layer already reports a reference that names nothing: a
// `supports` entry pointing at an id no document answers to is a broken
// relationship, and it is reported beside the set rather than left to be read
// out of prose. That covers the links documents make in their frontmatter and
// nothing else. The links a reader actually follows are written in the prose —
// `[the product brief](../brief.md)`, `[design invariant
// 1](../../designs/v1-harness-design.md#design-invariants)` — and until now
// nothing resolved one.
//
// Nobody can. A relative path is checkable by eye only by holding the directory
// layout in your head, and a `#fragment` is worse: it names a heading in another
// file, through a slug nobody writes down, so a reviewer looking at a patch can
// see the link and cannot see the target. That is what the reviews of this
// repository kept saying — "the anchor slug could not be verified from the
// evidence available", "a grep for those two anchors across the repo would
// confirm it before merge" — and a check that a reviewer has to end by asking
// somebody else to run is attention spent on nothing.
//
// So both ends are resolved here. A relative target has to be a file that is
// there, and a fragment has to be a heading the target document carries. What is
// deliberately not resolved is anything outside the repository: an absolute URL
// is somebody else's to keep working, and reaching for one would make the check
// depend on the network.
package doclink

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MaxFileBytes bounds one document read. It is generous — this repository's
// longest document is well over a hundred kilobytes — and it is here so that a
// walk that meets something enormous reports it rather than reading it into
// memory.
const MaxFileBytes = 4 << 20

// Problem is one link that resolves to nothing.
type Problem struct {
	// Path is the repository-relative document the link is written in and Line
	// the physical line it is written on, because what somebody has to open is the
	// document making the link rather than the one that is missing.
	Path string `json:"path"`
	Line int    `json:"line"`
	// Target is the link as the document writes it, so what is reported can be
	// found by searching for it.
	Target string `json:"target"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Reason)
}

// Check reports every link the repository's Markdown documents make to
// themselves that resolves to nothing. Problems are reported in the order the
// documents are walked and the order the links are written, so two runs over one
// checkout report the same thing in the same order.
//
// A document that cannot be read fails the whole check rather than being
// reported as a document with no links: a walk that quietly skipped what it
// could not open would report a repository with fewer links instead of a named
// problem, which is the failure this exists to prevent arrived at from the other
// side.
func Check(root string) ([]Problem, error) {
	documents, err := Documents(root)
	if err != nil {
		return nil, err
	}
	contents := make(map[string]string, len(documents))
	headings := make(map[string]map[string]bool, len(documents))
	for _, document := range documents {
		content, err := read(filepath.Join(root, filepath.FromSlash(document)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", document, err)
		}
		contents[document] = content
		headings[document] = anchorsIn(content)
	}
	var problems []Problem
	for _, document := range documents {
		problems = append(problems, problemsIn(root, document, contents[document], headings)...)
	}
	return problems, nil
}

// skippedDirectories are the trees a documentation check has no business
// walking: the two databases a checkout carries, the fixtures tests write to be
// malformed on purpose, and the release output. Everything else under the root
// is documentation somebody will read, including the agent instructions and the
// personas, which link into the same documents the guides do.
//
// The stores are named because they are not in the checkout — they are written
// beside it and ignored — and a check that reads whatever a tool happened to
// leave in one is a check that fails on one machine and passes on the next.
var skippedDirectories = map[string]bool{".git": true, ".dolt": true, "testdata": true, "dist": true}

// Documents is every Markdown file under the root, repository-relative and in
// sorted order. It is exported because what was checked is part of what a check
// reports: a walk that found nothing and a repository with no documentation are
// the same result otherwise.
func Documents(root string) ([]string, error) {
	var documents []string
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if current != root && skippedDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
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
		return nil, fmt.Errorf("walk %s for documentation: %w", root, err)
	}
	sort.Strings(documents)
	return documents, nil
}

// linkPattern matches a Markdown inline link and captures its target: the text
// in brackets, which may itself contain one level of brackets, then the target
// in parentheses, optionally angle-bracketed and optionally followed by a
// quoted title. An image is matched too — the `!` is simply the character
// before the bracket — because a picture that is not there is as broken as a
// document that is not.
var linkPattern = regexp.MustCompile(`\[(?:[^\[\]]|\[[^\[\]]*\])*\]\(\s*(<[^>]*>|[^()\s]*)(?:\s+"[^"]*")?\s*\)`)

// codeSpanPattern matches a Markdown inline code span. A link inside one is an
// example of a link rather than a link, so the spans are removed before the
// pattern above is run and a document teaching the syntax is not reported for
// what it teaches. Only a span that opens and closes on one line is removed:
// what is left of an unbalanced backtick is prose, and prose is what the rest of
// the line already is.
var codeSpanPattern = regexp.MustCompile("`[^`]*`")

// headingPattern matches an ATX Markdown heading and captures its text, which is
// what a fragment names through its slug.
var headingPattern = regexp.MustCompile(`^#{1,6}\s+(.*)$`)

// fencePattern matches the line that opens or closes a fenced code block.
var fencePattern = regexp.MustCompile("^(?:```|~~~)")

// schemePattern matches a target that names something outside the repository:
// an absolute URL, a `mailto:`, anything with a scheme. None of it is this
// check's to resolve.
var schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// problemsIn reports the links one document makes that resolve to nothing.
func problemsIn(root, document, content string, headings map[string]map[string]bool) []Problem {
	var problems []Problem
	directory := path.Dir(document)
	fenced := false
	for index, raw := range strings.Split(content, "\n") {
		if fencePattern.MatchString(strings.TrimSpace(raw)) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, match := range linkPattern.FindAllStringSubmatch(codeSpanPattern.ReplaceAllString(raw, ""), -1) {
			target := strings.TrimSpace(match[1])
			target = strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
			if target == "" || schemePattern.MatchString(target) || strings.HasPrefix(target, "//") {
				continue
			}
			if reason := resolve(root, document, directory, target, headings); reason != "" {
				problems = append(problems, Problem{Path: document, Line: index + 1, Target: target, Reason: reason})
			}
		}
	}
	return problems
}

// resolve says why one target names nothing, and returns nothing for one that
// resolves. The two halves are separate questions: whether the file is there,
// and — for a Markdown file, which is the only kind whose headings are known —
// whether it carries the heading the fragment names.
func resolve(root, document, directory, target string, headings map[string]map[string]bool) string {
	reference, fragment, _ := strings.Cut(target, "#")
	// A path is written as a URL, so what it says and what the filesystem holds
	// differ wherever a character had to be escaped. A target that is not valid
	// escaping is left as written rather than refused, because the filesystem is
	// what decides and a name containing a stray percent is a name.
	if unescaped, err := url.PathUnescape(reference); err == nil {
		reference = unescaped
	}
	if unescaped, err := url.PathUnescape(fragment); err == nil {
		fragment = unescaped
	}

	// An empty path is a fragment naming a heading in the document making the
	// link, which is how a long guide points at its own sections.
	linked := document
	if reference != "" {
		// A target that opens with a separator is written from the repository root,
		// which is how the forge reads one; anything else is written from the
		// document making the link.
		if strings.HasPrefix(reference, "/") {
			linked = path.Clean(strings.TrimPrefix(reference, "/"))
		} else {
			linked = path.Clean(path.Join(directory, reference))
		}
		if linked == ".." || strings.HasPrefix(linked, "../") {
			return fmt.Sprintf("it links to %q, which leaves the repository, so nothing here can say whether it is there", target)
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(linked)))
		if err != nil {
			return fmt.Sprintf("it links to %q, and %s is not in the repository", target, linked)
		}
		if info.IsDir() {
			// A link to a directory is a link to a place rather than to a heading, so
			// a fragment on one is not something to resolve.
			return ""
		}
	}
	if fragment == "" {
		return ""
	}
	anchors, known := headings[linked]
	if !known {
		// Only a Markdown document's headings were collected. A fragment on
		// anything else is an anchor in something this does not read, and guessing
		// would be worse than saying nothing.
		return ""
	}
	if !anchors[slug(fragment)] {
		return fmt.Sprintf("it links to %q, and %s carries no heading with that anchor", target, linked)
	}
	return ""
}

// anchorsIn is every fragment one document answers to: the slug of each of its
// headings, with the suffix a repeated heading gets so that the second `## Why`
// in a document is reachable as `why-1`. That numbering is the forge's, and it
// is followed rather than invented because the links being checked are the ones
// a reader clicks on the forge.
func anchorsIn(content string) map[string]bool {
	anchors := map[string]bool{}
	seen := map[string]int{}
	fenced := false
	for _, raw := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(raw)
		if fencePattern.MatchString(trimmed) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		heading := headingPattern.FindStringSubmatch(trimmed)
		if heading == nil {
			continue
		}
		anchor := slug(heading[1])
		if anchor == "" {
			continue
		}
		if repeated := seen[anchor]; repeated > 0 {
			anchors[fmt.Sprintf("%s-%d", anchor, repeated)] = true
		} else {
			anchors[anchor] = true
		}
		seen[anchor]++
	}
	return anchors
}

// closingHashesPattern matches the optional trailing hashes of a closed ATX
// heading, which are punctuation rather than part of what the heading says.
var closingHashesPattern = regexp.MustCompile(`\s+#+$`)

// inlineLinkTextPattern matches a link inside a heading, capturing the text: a
// heading that links somewhere is slugged from what it reads as, not from where
// it points.
var inlineLinkTextPattern = regexp.MustCompile(`\[([^\[\]]*)\]\([^()]*\)`)

// emphasisPattern matches the markers a heading's words may be wrapped in.
// Underscores are deliberately not among them: the forge keeps an underscore in
// a slug, so stripping one would make `check_timeout` unreachable as itself.
var emphasisPattern = regexp.MustCompile(`\*+`)

// droppedPattern matches what a slug does not keep. Letters, digits, marks,
// underscores, hyphens, and spaces survive; everything else is dropped rather
// than replaced, which is why `what fails, closed` and `what fails closed` are
// the same anchor.
var droppedPattern = regexp.MustCompile(`[^\p{L}\p{N}\p{M}_\- ]`)

// slug is the anchor a heading answers to, derived the way the forge derives it:
// the heading's text with its markup taken off, lowercased, punctuation dropped,
// and spaces turned into hyphens.
func slug(heading string) string {
	text := closingHashesPattern.ReplaceAllString(strings.TrimSpace(heading), "")
	text = inlineLinkTextPattern.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, "`", "")
	text = emphasisPattern.ReplaceAllString(text, "")
	text = droppedPattern.ReplaceAllString(strings.ToLower(text), "")
	return strings.ReplaceAll(strings.TrimSpace(text), " ", "-")
}

// read reads one document, bounded, so that a file too large to be
// documentation is reported rather than loaded.
func read(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("it is not a regular file")
	}
	if info.Size() > MaxFileBytes {
		return "", fmt.Errorf("it is %d bytes, limit is %d", info.Size(), MaxFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
