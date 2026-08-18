package artifact

// The on-disk half of an artifact: the metadata in YAML frontmatter at the top
// of the document it identifies, in the artifact homes the project configures,
// reviewed with the code like every other canonical document. The prose below
// the frontmatter is the artifact's content and is not touched here — a brief,
// a goals document, and a decision record have nothing in common structurally,
// and imposing one shape on all three would be a second contract nobody agreed
// to on top of the identity this adds.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxFileBytes bounds one artifact file. It is generous, because an artifact is
// a whole document rather than a tightly stated constraint, and it is bounded
// at all because the frontmatter is read by loading the file: a document larger
// than the entire product context budget could not be delivered anywhere
// afterwards.
const MaxFileBytes = 512 << 10

// frontmatterFence opens and closes the machine-readable half of an artifact
// file, which is where every other tool that reads Markdown expects to find it.
const frontmatterFence = "---"

// indexFileName is the one Markdown name in an artifact home that is not an
// artifact. A directory index describes what is filed beside it rather than
// stating any intent of its own, nothing downstream ever needs to refer to one,
// and `README` is not a usable id in the first place.
const indexFileName = "readme.md"

// Store reads one repository's canonical artifacts.
type Store struct {
	// RepositoryRoot is the repository the artifacts belong to.
	RepositoryRoot string
	// Homes are the artifact directories, relative to the repository root and
	// read in the order given. Each is walked to any depth, because the product
	// keeps its goals in a directory beneath its brief and a project may file
	// designs the same way.
	Homes []string
	// Excluded are directories inside those homes that are not read, because
	// they carry an identity scheme of their own. The invariants directory is
	// the one that exists today: it sits inside the decisions home, its files
	// are already identified by their own file names, and reading them twice
	// under two schemes is exactly the confusion one identity model is for.
	Excluded []string
}

// Load reads every artifact the repository records. A home that does not exist
// is not an error: a project that has not written its designs down yet has no
// design artifacts rather than a broken configuration. Anything else that stops
// a home being read is an error, because a set that silently came back empty
// would look exactly like a repository with nothing written down.
func (s Store) Load() (Set, error) {
	root, err := resolveRoot(s.RepositoryRoot)
	if err != nil {
		return Set{}, err
	}
	homes, err := resolveDirectories("artifact home", s.Homes)
	if err != nil {
		return Set{}, err
	}
	excluded, err := resolveDirectories("excluded directory", s.Excluded)
	if err != nil {
		return Set{}, err
	}
	set := Set{Homes: homes}

	// Homes may overlap — a project that pointed two of them at one directory
	// gets one artifact per file rather than a set where everything duplicates
	// itself.
	read := map[string]bool{}
	claims := map[string][]Artifact{}
	for _, home := range homes {
		paths, err := discover(root, home, excluded)
		if err != nil {
			return Set{}, err
		}
		for _, relative := range paths {
			if read[relative] {
				continue
			}
			read[relative] = true
			loaded, err := s.read(filepath.Join(root, filepath.FromSlash(relative)), relative)
			if err != nil {
				var unreadable unreadableError
				if errors.As(err, &unreadable) {
					set.Problems = append(set.Problems, Problem{Path: relative, Reason: unreadable.reason})
					continue
				}
				return Set{}, err
			}
			claims[loaded.ID] = append(claims[loaded.ID], loaded)
		}
	}

	for id, claimants := range claims {
		if len(claimants) == 1 {
			set.Artifacts = append(set.Artifacts, claimants[0])
			continue
		}
		// Neither file is admitted. One id names one artifact, and choosing
		// between two files that both claim it would hand whatever refers to that
		// id a document nobody decided on.
		sort.Slice(claimants, func(first, second int) bool { return claimants[first].Path < claimants[second].Path })
		for index, claimant := range claimants {
			others := make([]string, 0, len(claimants)-1)
			for other, competitor := range claimants {
				if other != index {
					others = append(others, competitor.Path)
				}
			}
			set.Problems = append(set.Problems, Problem{
				Path:   claimant.Path,
				Reason: fmt.Sprintf("its id %q is also claimed by %s; one id names one artifact, so none of them is read as %q", id, strings.Join(others, ", "), id),
			})
		}
	}

	sort.Slice(set.Artifacts, func(first, second int) bool { return set.Artifacts[first].ID < set.Artifacts[second].ID })
	sort.Slice(set.Problems, func(first, second int) bool { return set.Problems[first].Path < set.Problems[second].Path })
	// The relationships are checked here rather than by each caller remembering
	// to: a chain validated only when somebody asked is a chain that holds only
	// where somebody asked. Nothing loaded above is dropped over what this finds.
	set.ReferenceProblems = referenceProblems(set.Artifacts, set.Problems)
	return set, nil
}

// discover lists the Markdown one home holds, to any depth and without
// following symlinks out of the repository. Every path it returns is still
// validated when it is read.
func discover(root, home string, excluded []string) ([]string, error) {
	base := filepath.Join(root, filepath.FromSlash(home))
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect artifact home %q: %w", home, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("artifact home %q is not a directory", home)
	}

	var found []string
	err = filepath.WalkDir(base, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if entry.IsDir() {
			if isExcluded(slashed, excluded) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		if strings.EqualFold(entry.Name(), indexFileName) {
			return nil
		}
		found = append(found, slashed)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover artifacts under %q: %w", home, err)
	}
	sort.Strings(found)
	return found, nil
}

// idForPath is the id a file in an artifact home has to answer to: its own
// name. It is one function rather than the same expression in two places
// because a reference that names a refused file is reported by matching the two
// against each other, and two rules that drifted apart would report a document
// that is on disk as one nobody wrote.
func idForPath(relative string) string {
	base := filepath.Base(relative)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func isExcluded(directory string, excluded []string) bool {
	for _, entry := range excluded {
		if directory == entry {
			return true
		}
	}
	return false
}

// unreadableError is a file that is not a usable artifact, as opposed to a
// filesystem failure. The two are told apart because the first is a problem
// reported alongside a set that still loads, and the second means the set
// itself is not known.
type unreadableError struct {
	reason string
}

func (e unreadableError) Error() string { return e.reason }

// read parses one artifact file. Everything wrong with the file is reported as
// unreadable rather than fatal, so one malformed artifact never hides the rest.
func (s Store) read(path, relative string) (Artifact, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Artifact{}, err
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, unreadableError{reason: "it is not a regular file"}
	}
	if info.Size() > MaxFileBytes {
		return Artifact{}, unreadableError{reason: fmt.Sprintf("it is %d bytes, limit is %d", info.Size(), MaxFileBytes)}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("read artifact %q: %w", relative, err)
	}
	parsed, err := parse(string(content))
	if err != nil {
		return Artifact{}, unreadableError{reason: err.Error()}
	}
	// The file's name is the artifact's identity, so a file whose frontmatter
	// claims another id is refused rather than read as either: two names for one
	// artifact is how something downstream ends up referring to a document that
	// is not the one anybody edited.
	expected := idForPath(relative)
	if parsed.ID != expected {
		return Artifact{}, unreadableError{reason: fmt.Sprintf("its id %q does not match its file name; an artifact with that id lives in %s.md", parsed.ID, parsed.ID)}
	}
	parsed.Path = relative
	if err := parsed.Validate(); err != nil {
		return Artifact{}, unreadableError{reason: err.Error()}
	}
	return parsed, nil
}

// frontmatter is the machine-readable half of an artifact file. It is a
// separate type from Artifact so the fields the store derives — the file the
// artifact was read from — cannot be asserted by the document itself.
type frontmatter struct {
	ID        string     `yaml:"id"`
	Kind      Kind       `yaml:"kind"`
	Title     string     `yaml:"title"`
	Supports  []string   `yaml:"supports,omitempty"`
	Status    Status     `yaml:"status"`
	Revisions []Revision `yaml:"revisions"`
}

// parse reads one artifact's frontmatter. Unknown keys are refused rather than
// ignored, so a mistyped field fails visibly instead of leaving an artifact
// quietly missing half of what its author wrote.
func parse(content string) (Artifact, error) {
	metadata, err := frontmatterOf(content)
	if err != nil {
		return Artifact{}, err
	}
	decoder := yaml.NewDecoder(strings.NewReader(metadata))
	decoder.KnownFields(true)
	var decoded frontmatter
	if err := decoder.Decode(&decoded); err != nil {
		return Artifact{}, fmt.Errorf("its frontmatter could not be read: %w", err)
	}
	return Artifact{
		ID:        strings.TrimSpace(decoded.ID),
		Kind:      decoded.Kind,
		Title:     strings.TrimSpace(decoded.Title),
		Supports:  trimmedList(decoded.Supports),
		Status:    decoded.Status,
		Revisions: decoded.Revisions,
	}, nil
}

// frontmatterOf returns the fenced metadata at the top of a document. The prose
// below it is the artifact's content, and it is deliberately not returned: this
// package identifies documents rather than reading what they say.
func frontmatterOf(content string) (string, error) {
	// A byte-order mark ahead of the fence is still frontmatter; an editor that
	// wrote one must not turn an artifact into an unidentified document.
	lines := strings.Split(strings.TrimPrefix(content, "\ufeff"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return "", errors.New("it does not open with `---` frontmatter naming its id, kind, title, status, and revisions")
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == frontmatterFence {
			return strings.Join(lines[1:index], "\n"), nil
		}
	}
	return "", errors.New("its frontmatter is not closed by a `---` line")
}

// resolveRoot resolves the repository the artifacts belong to.
func resolveRoot(repositoryRoot string) (string, error) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	return root, nil
}

// resolveDirectories keeps every configured directory inside the repository.
// Configuration refuses the same settings when it loads, and this repeats the
// rule rather than relying on it, because this package is what actually reads
// the filesystem: a confinement that holds only when a caller remembered to
// check is not one. A path that escaped the repository would put documents
// nobody reviewed with the code into the set that says what the product
// intends.
func resolveDirectories(what string, directories []string) ([]string, error) {
	resolved := make([]string, 0, len(directories))
	for _, directory := range directories {
		trimmed := strings.TrimSpace(directory)
		if trimmed == "" {
			return nil, fmt.Errorf("%s is required", what)
		}
		clean := filepath.Clean(trimmed)
		if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s %q resolves outside the repository", what, directory)
		}
		resolved = append(resolved, filepath.ToSlash(clean))
	}
	return resolved, nil
}

func trimmedList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			trimmed = append(trimmed, cleaned)
		}
	}
	if len(trimmed) == 0 {
		return nil
	}
	return trimmed
}
