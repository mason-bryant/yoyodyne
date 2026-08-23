package artifact

// Identity the store does not read, as opposed to identity it read and refused.
//
// A file inside an artifact home that is not a usable artifact is a Problem:
// the store walked it, judged it, and said so. Two documents are neither
// artifacts nor problems, because nothing ever looked at them, and both of them
// look governed to whoever is reading the file rather than the loader:
//
//   - A document carrying artifact frontmatter outside every configured home.
//     The store never walks it, so the load stays clean while nothing downstream
//     can refer to it — no reference resolves to it, no staleness reaches it,
//     and no amendment channel carries a proposal against it. That is exactly
//     how the v1 harness design sat ungoverned with every check green.
//   - Frontmatter on a directory index. An index is skipped by name, deliberately
//     and permanently: it describes what is filed beside it rather than stating
//     intent, and `README` is not a usable id. Frontmatter written on one is
//     therefore inert, which is worse than absent because it reads as identity.
//
// Both are reported and neither is refused. Nothing here is a gate: an index is
// still an index, a document outside the homes is still whatever it was, and
// what the report buys is that somebody is told the identity on it is not being
// read by anything.
//
// The scan is bounded to the parent directories of the configured homes —
// `docs/` in the recommended layout — because that is where a document filed
// beside the homes lands, and because a repository-wide walk would report every
// test fixture and vendored document that happens to carry frontmatter. A
// project that configures a home at the repository root therefore gets the whole
// repository scanned, which is what that bound means there.

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// UngovernedKind is why a document carrying identity is not governed by it.
// They are told apart rather than left to be read out of prose because they are
// answered differently: one is a document to move into a home or to leave alone
// deliberately, one is frontmatter to delete from an index or prose to move into
// a governed document, and one is a gap in the report itself.
type UngovernedKind string

const (
	// UngovernedOutsideHomes is a document carrying artifact frontmatter that no
	// configured home contains, so nothing reads its identity.
	UngovernedOutsideHomes UngovernedKind = "outside-every-home"
	// UngovernedIndex is artifact frontmatter on a directory index, which is
	// skipped by name wherever it sits.
	UngovernedIndex UngovernedKind = "directory-index"
	// UngovernedUnread is a path the scan could not read. It is reported for the
	// same reason the other two are: identity nothing looked at is what this
	// exists to stop being silent, and a subtree that was skipped is exactly that.
	UngovernedUnread UngovernedKind = "unread"
)

// Ungoverned names one document whose identity nothing reads, and says why. It
// is kept apart from Problem because a Problem is a file in an artifact home
// that the store judged, and this is a file the store never governs at all.
type Ungoverned struct {
	Kind UngovernedKind `json:"kind"`
	Path string         `json:"path"`
	// ID is the id the document claims. Nothing answers to it — that is the whole
	// finding — so it is carried to say what a reference would have been written
	// against.
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

func (u Ungoverned) String() string {
	return u.Path + ": " + u.Reason
}

// ungoverned reports the identity the homes do not reach. It never fails the
// load: a directory it could not walk is reported as a gap in what it looked at
// rather than becoming an error that costs every command the artifacts it could
// read perfectly well.
func (s Store) ungoverned(root string, homes, excluded []string) []Ungoverned {
	var found []Ungoverned
	seen := map[string]bool{}
	for _, scanned := range scanRoots(homes) {
		base := root
		if scanned != "." {
			base = filepath.Join(root, filepath.FromSlash(scanned))
		}
		if _, err := os.Lstat(base); err != nil {
			// A directory that is not there holds nothing, which is the answer
			// rather than a failure — the homes themselves are allowed to be
			// missing, and so is what sits beside them.
			continue
		}
		_ = filepath.WalkDir(base, func(candidate string, entry fs.DirEntry, walkErr error) error {
			relative, err := filepath.Rel(root, candidate)
			if err != nil {
				return nil
			}
			slashed := filepath.ToSlash(relative)
			if walkErr != nil {
				found = append(found, Ungoverned{
					Kind:   UngovernedUnread,
					Path:   slashed,
					Reason: fmt.Sprintf("it could not be read, so identity under it is not accounted for: %v", walkErr),
				})
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				// A directory carrying an identity scheme of its own is read under
				// that scheme, and a dot directory is a repository's own state rather
				// than its documentation. Walking either would report constraints and
				// tool state as ungoverned artifacts.
				if slashed != "." && (within(slashed, excluded) || strings.HasPrefix(entry.Name(), ".")) {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
				return nil
			}
			if seen[slashed] {
				return nil
			}
			seen[slashed] = true
			index := strings.EqualFold(entry.Name(), indexFileName)
			inHome := within(path.Dir(slashed), homes)
			if inHome && !index {
				// Governed, or already refused and named as a Problem. Either way it
				// was looked at, which is the whole of what this reports on.
				return nil
			}
			claimed, kind, carries := identityClaim(candidate)
			if !carries {
				return nil
			}
			found = append(found, ungovernedEntry(slashed, claimed, kind, index, inHome, homes))
			return nil
		})
	}
	sort.Slice(found, func(first, second int) bool { return found[first].Path < found[second].Path })
	return found
}

// ungovernedEntry says which of the two a document is and what somebody would
// have to do about it. An index is reported as an index wherever it sits,
// because the name is what stops it being read and moving it would not change
// that.
func ungovernedEntry(relative, claimed, kind string, index, inHome bool, homes []string) Ungoverned {
	if index {
		reason := fmt.Sprintf("it claims the identity %q (kind %q), and a directory index is not read as an artifact: %s is skipped by name wherever it sits, so the frontmatter on it is inert",
			claimed, kind, path.Base(relative))
		if !inHome {
			reason += fmt.Sprintf(", and it is inside none of the artifact homes (%s) besides", strings.Join(homes, ", "))
		}
		return Ungoverned{Kind: UngovernedIndex, Path: relative, ID: claimed, Reason: reason}
	}
	return Ungoverned{
		Kind: UngovernedOutsideHomes,
		Path: relative,
		ID:   claimed,
		Reason: fmt.Sprintf("it claims the identity %q (kind %q) and is inside none of the artifact homes (%s), so nothing reads it: no reference resolves to that id and nothing downstream can be traced through it",
			claimed, kind, strings.Join(homes, ", ")),
	}
}

// scanRoots is where the scan looks: the parent of each configured home, with
// any root another one already contains dropped, so a repository is walked once
// whatever its homes are nested like.
func scanRoots(homes []string) []string {
	var roots []string
	for _, home := range homes {
		parent := path.Dir(home)
		if parent == "." {
			// The repository contains every other root there could be, so a home at
			// the top of it settles the whole scan.
			return []string{"."}
		}
		if within(parent, roots) {
			continue
		}
		kept := roots[:0]
		for _, existing := range roots {
			if !within(existing, []string{parent}) {
				kept = append(kept, existing)
			}
		}
		roots = append(kept, parent)
	}
	sort.Strings(roots)
	return roots
}

// identity is the half of an artifact's frontmatter that says it is one. It is
// decoded leniently, unlike the frontmatter the store reads: a document filed
// outside every home is invisible whether or not its metadata would have passed,
// so a mistyped field must not turn a governed-looking document into one this
// says nothing about.
type identity struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
}

// identityClaim reports the identity a document claims, if it claims one.
// Claiming one is an id and a kind together. Requiring both is what keeps every
// other frontmatter convention out of this report — a publishing tool's title
// and layout, and the invariants' own scheme, which records an id and no kind —
// so what is reported is a document that was written to be governed.
func identityClaim(path string) (id, kind string, carries bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Size() > MaxFileBytes {
		// A file too large to be an artifact could not be governed by moving it
		// either, so there is nothing here for anybody to act on.
		return "", "", false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	metadata, _, err := splitDocument(string(content))
	if err != nil {
		return "", "", false
	}
	var claimed identity
	if err := yaml.Unmarshal([]byte(metadata), &claimed); err != nil {
		return "", "", false
	}
	claimed.ID = strings.TrimSpace(claimed.ID)
	claimed.Kind = strings.TrimSpace(claimed.Kind)
	if claimed.ID == "" || claimed.Kind == "" {
		return "", "", false
	}
	return claimed.ID, claimed.Kind, true
}
