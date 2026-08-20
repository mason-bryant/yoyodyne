// Package surface is the part of the repository one work item is going to
// change, as far as the item itself says.
//
// It exists for the scheduler, and for one arithmetic. Development is parallel
// and integration is serial, so two runs started at once over the same files do
// not cost what one costs twice: one of them promotes, and the other finds its
// target moved, replays its change onto where it went, re-runs every check, and
// is reviewed again from nothing — and a replay that will not apply stops the
// run outright and leaves the item for a person. Sequencing those two costs a
// wait. Racing them costs all of that, on the run that loses.
//
// Nothing here prevents any of it. The promotion lease still serializes
// integration, the replay still handles a target that moved, and both would
// still be right if this package did not exist. What this adds is the scheduler
// being able to tell work that would race from work that would not, early enough
// to sequence it rather than pay for it.
//
// # Declared beats inferred
//
// An item declares its surfaces by naming them after a marker, the way it grants
// a protected path, and a declaration is taken as the whole answer: an author who
// said which files this touches has answered the question, and reading their
// prose for more would only add what they did not mean.
//
// Inference is what an item that declared nothing gets, and it is deliberately
// narrow. The cost of the two mistakes is not symmetric — a surface missed lets
// two runs race, which is what the harness already handles at some expense, and a
// surface invented holds unrelated work back, which is the concurrency this is
// supposed to be protecting. So inference takes only what is unmistakably a file:
// a path with a separator in it and an extension on the end. A directory, a
// package name, an area of the product described in words — none of those are
// inferred, and an item that wants one of them named says so.
//
// The fields read are the ones somebody authored. The notes are not among them,
// for the reason a protected-path grant is not read from them either: the harness
// appends each run's own record there, and a summary that happened to name the
// files it touched would otherwise declare surfaces for the item after the fact,
// from the run rather than from the person who wrote the work down.
package surface

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// DeclareMarker is how a work item says which part of the repository it will
// change. It is an unlovely token on purpose, and for the same reason the
// protected-path grant marker is one: item text discusses surfaces in prose —
// the item that introduced this scheduling rule uses the word repeatedly — and a
// marker ordinary prose can produce is a marker that declares by accident.
const DeclareMarker = "conflict-surface:"

// filePath is what inference accepts: at least two segments, with an extension
// of at most six characters on the last of them. Anything less is prose that
// happens to contain a slash — "and/or" is the one that would otherwise be read
// as a path on nearly every item, and a whole directory named in passing is the
// one that would hold the most unrelated work back.
var filePath = regexp.MustCompile(`^[^/]+(?:/[^/]+)+\.[A-Za-z0-9]{1,6}$`)

// Of is the surfaces one work item touches: what it declares, or what its
// authored prose unmistakably names where it declares nothing.
func Of(item beads.WorkItem) []string {
	authored := []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria}
	if declared := Declared(authored...); len(declared) > 0 {
		return declared
	}
	return Inferred(authored...)
}

// Declared reads the surfaces a work item's text names after the marker, from
// whichever of the item's fields the caller passes. Which fields those are is
// the caller's decision, because it is the caller that knows which of them the
// harness itself writes into.
func Declared(texts ...string) []string {
	var declared []string
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			remainder, found := cutMarker(line)
			if !found {
				continue
			}
			for _, entry := range strings.FieldsFunc(remainder, isSeparator) {
				if clean, ok := normalize(undecorate(entry)); ok {
					declared = appendUnique(declared, clean)
				}
			}
		}
	}
	sort.Strings(declared)
	return declared
}

// Inferred reads the files a work item's text names in passing. It is the
// fallback for an item nobody declared anything on, and it is narrow by design:
// see the package comment for why the two mistakes here do not cost the same.
func Inferred(texts ...string) []string {
	var inferred []string
	for _, text := range texts {
		for _, field := range strings.Fields(text) {
			candidate := undecorate(field)
			// A URL is not a surface of this repository, and one that named a
			// file would otherwise read as the file it names.
			if strings.Contains(candidate, "://") || !filePath.MatchString(candidate) {
				continue
			}
			if clean, ok := normalize(candidate); ok {
				inferred = appendUnique(inferred, clean)
			}
		}
	}
	sort.Strings(inferred)
	return inferred
}

// Shared names the first surface two sets have in common, or reports that they
// have none. What it names is the narrower of the two where one contains the
// other, because a file inside a declared directory is the specific thing the
// two pieces of work would collide over and the directory is only where it sits.
//
// Both sides arrive sorted from the readers above, so which shared surface is
// reported is stable for a given pair rather than an artifact of map order.
func Shared(left, right []string) (string, bool) {
	for _, l := range left {
		for _, r := range right {
			switch {
			case l == r, within(l, r):
				return l, true
			case within(r, l):
				return r, true
			}
		}
	}
	return "", false
}

// cutMarker finds the marker at the start of one line and returns what follows
// it. The marker has to start the line — after whatever a Markdown bullet,
// quote, or emphasis put in front of it — so a sentence discussing the marker
// declares nothing.
func cutMarker(line string) (string, bool) {
	trimmed := strings.TrimLeft(strings.TrimSpace(line), "-*>#` \t")
	if len(trimmed) < len(DeclareMarker) || !strings.EqualFold(trimmed[:len(DeclareMarker)], DeclareMarker) {
		return "", false
	}
	return trimmed[len(DeclareMarker):], true
}

func isSeparator(r rune) bool {
	return r == ',' || r == ';' || r == ' ' || r == '\t'
}

// What an item's prose puts around a path that is not part of the path:
// Markdown emphasis and code spans, quotes, brackets, and — at the end only —
// the punctuation that closes the sentence it was written in. The two sets
// differ by the full stop deliberately: a leading one is part of the path rather
// than decoration around it, and trimming it would lose `.beads/issues.jsonl`.
const (
	leadingDecoration  = "`'\"*_(["
	trailingDecoration = "`'\"*_)].,;:!?"
)

func undecorate(value string) string {
	return strings.TrimRight(strings.TrimLeft(value, leadingDecoration), trailingDecoration)
}

// normalize turns a written path into the repository-relative slash form both
// sides are compared in. It reports false for anything that cannot name a file
// inside the repository, and for the repository root itself: a surface of "."
// is every surface, which is not something an author meant to say.
func normalize(value string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return "", false
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// within reports whether a normalized path sits inside a prefix. The separator
// is required, so "docs/products" is not inside "docs/product".
func within(candidate, prefix string) bool {
	return strings.HasPrefix(candidate, prefix+"/")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
