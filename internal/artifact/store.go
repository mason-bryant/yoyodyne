package artifact

// The on-disk half of an artifact: the metadata in YAML frontmatter at the top
// of the document it identifies, in the artifact homes the project configures,
// reviewed with the code like every other canonical document. The prose below
// the frontmatter is the artifact's content and no shape is imposed on it — a
// brief, a goals document, and a decision record have nothing in common
// structurally, and imposing one on all three would be a second contract nobody
// agreed to on top of the identity this adds. A mutation carries that prose
// through unchanged unless it was asked to replace it.
//
// Reading is open to anything that needs the set; writing goes through
// Authorize, so the ownership boundary is enforced by the only code that can
// change an artifact rather than by the callers that ask it to.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	// The relationships, and the authority each document records having been
	// changed under, are checked here rather than by each caller remembering to: a
	// rule validated only when somebody asked holds only where somebody asked.
	// Nothing loaded above is dropped over what either of them finds.
	set.ReferenceProblems = append(referenceProblems(set.Artifacts, set.Problems), UnauthorizedRevisions(set.Artifacts)...)
	return set, nil
}

// Draft is a new artifact as its owning role states it. The lifecycle fields
// are absent because the store owns them: the status, the revision log, and the
// file it lands in are the record of the mutation rather than something an
// author asserts.
type Draft struct {
	ID    string
	Kind  Kind
	Title string
	// Supports names the artifacts upstream of this one. It is empty for the
	// brief, which is the root, and for a decision record, which is a record of
	// how something was decided rather than a link in the intent chain.
	Supports []string
	// Directory is the repository-relative directory the document lands in: one
	// of the homes, or a directory beneath one, because the product files its
	// goals below its brief. An artifact written outside every home is a document
	// nothing reads, which is a worse outcome than a refused mutation.
	Directory string
	// Body is the document itself, everything below the frontmatter. It is
	// required: an artifact is an identity attached to a document, and there is
	// nothing to identify without one.
	Body string
	// Reason is why this artifact is being recorded, kept as the first revision.
	Reason string
}

// Amendment is a bounded change to an artifact that already exists. Every field
// is a pointer so an amendment says exactly what it changes, rather than
// replacing what nobody mentioned with the zero value of a struct.
//
// The kind is deliberately not amendable. It is what decides who owns the
// document, so changing it would be a mutation that reassigns its own
// authorization — which is a new artifact and a retirement, both recorded, and
// not an edit.
type Amendment struct {
	Title    *string
	Supports *[]string
	// Body replaces the document below the frontmatter. Leaving it unset amends
	// the metadata and carries the prose through untouched.
	Body *string
	// Reason is why the artifact is changing, recorded as a revision. It is
	// required for the same reason an ending's is: a change nobody explained is
	// one nobody can evaluate later.
	Reason string
}

// Create records a new artifact. Only the role that owns the kind may: see
// Authorize.
func (s Store) Create(role domain.AgentRole, draft Draft, now time.Time) (Artifact, error) {
	if err := Authorize(role, draft.Kind); err != nil {
		return Artifact{}, err
	}
	created := Artifact{
		ID:       strings.TrimSpace(draft.ID),
		Kind:     draft.Kind,
		Title:    strings.TrimSpace(draft.Title),
		Supports: trimmedList(draft.Supports),
		Status:   StatusActive,
		Revisions: []Revision{{
			Action: ActionCreated,
			By:     role,
			At:     now.UTC(),
			Reason: strings.TrimSpace(draft.Reason),
		}},
	}
	if err := created.Validate(); err != nil {
		return Artifact{}, err
	}
	body := strings.TrimSpace(draft.Body)
	if body == "" {
		return Artifact{}, fmt.Errorf("artifact %q has no document below its frontmatter; identity is attached to a document rather than standing on its own", created.ID)
	}
	path, relative, err := s.path(created.ID, draft.Directory)
	if err != nil {
		return Artifact{}, err
	}
	if err := s.unclaimed(created.ID, path, relative); err != nil {
		return Artifact{}, err
	}
	created.Path = relative
	if err := s.write(path, created, "\n"+body+"\n"); err != nil {
		return Artifact{}, err
	}
	return created, nil
}

// unclaimed reports whether anything already answers to an id. It looks across
// every home rather than only at the file being written, because one id names
// one artifact: a second document claiming it is refused whichever home it is
// filed in, and creating one anyway would leave both of them unreadable and
// whatever refers to the id holding nothing.
func (s Store) unclaimed(id, path, relative string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("artifact %q already exists at %s; amend it instead", id, relative)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect artifact %q: %w", id, err)
	}
	set, err := s.Load()
	if err != nil {
		return err
	}
	if found, exists := set.Find(id); exists {
		return fmt.Errorf("artifact %q already exists at %s; amend it instead", id, found.Path)
	}
	for _, problem := range set.Problems {
		if idForPath(problem.Path) == id {
			return fmt.Errorf("%s already claims the id %q, and is not readable as one: %s", problem.Path, id, problem.Reason)
		}
	}
	return nil
}

// Amend changes an artifact that already exists and records why. Only the role
// that owns the kind may, which is why the artifact is read before the authority
// is judged: what a role is authorized over is decided by the document it is
// changing rather than by what the caller says it is.
func (s Store) Amend(role domain.AgentRole, id string, amendment Amendment, now time.Time) (Artifact, error) {
	existing, path, body, err := s.loadOne(id)
	if err != nil {
		return Artifact{}, err
	}
	if err := Authorize(role, existing.Kind); err != nil {
		return Artifact{}, err
	}
	// Amending an artifact that was superseded or retired would revive replaced
	// intent by editing it, which is not a decision anybody made. What replaces it
	// is a later artifact, and that is a creation rather than this.
	if ended, hasEnding := existing.Ended(); hasEnding {
		return Artifact{}, fmt.Errorf("artifact %q was %s on %s and is not amended back into force: %s",
			id, ended.Action, ended.At.Format(time.RFC3339), ended.Reason)
	}
	if amendment.changesNothing() {
		return Artifact{}, fmt.Errorf("amending artifact %q changes nothing; name what is being amended", id)
	}
	amended := existing
	if amendment.Title != nil {
		amended.Title = strings.TrimSpace(*amendment.Title)
	}
	if amendment.Supports != nil {
		amended.Supports = trimmedList(*amendment.Supports)
	}
	if amendment.Body != nil {
		replacement := strings.TrimSpace(*amendment.Body)
		if replacement == "" {
			return Artifact{}, fmt.Errorf("amending artifact %q to an empty document would leave an identity with nothing under it; retire it instead", id)
		}
		body = "\n" + replacement + "\n"
	}
	amended.Revisions = append(append([]Revision(nil), existing.Revisions...), Revision{
		Action: ActionAmended,
		By:     role,
		At:     now.UTC(),
		Reason: strings.TrimSpace(amendment.Reason),
	})
	if err := amended.Validate(); err != nil {
		return Artifact{}, err
	}
	if err := s.write(path, amended, body); err != nil {
		return Artifact{}, err
	}
	return amended, nil
}

// changesNothing reports an amendment that names no change at all, which is a
// recorded revision with nothing to record.
func (a Amendment) changesNothing() bool {
	return a.Title == nil && a.Supports == nil && a.Body == nil
}

// Supersede records that a later artifact replaced this one, and Retire that it
// stopped applying and was not replaced. Both leave the file: the record of what
// was intended is what makes the change that followed traceable, and a document
// that vanished leaves whoever read it last month with no way to find out which
// of the two happened.
func (s Store) Supersede(role domain.AgentRole, id, reason string, now time.Time) (Artifact, error) {
	return s.end(role, id, ActionSuperseded, StatusSuperseded, reason, now)
}

func (s Store) Retire(role domain.AgentRole, id, reason string, now time.Time) (Artifact, error) {
	return s.end(role, id, ActionRetired, StatusRetired, reason, now)
}

// end is the half Supersede and Retire share: the same authority, the same
// recorded ending, and a different status and action.
func (s Store) end(role domain.AgentRole, id string, action Action, status Status, reason string, now time.Time) (Artifact, error) {
	existing, path, body, err := s.loadOne(id)
	if err != nil {
		return Artifact{}, err
	}
	if err := Authorize(role, existing.Kind); err != nil {
		return Artifact{}, err
	}
	if ended, hasEnding := existing.Ended(); hasEnding {
		return Artifact{}, fmt.Errorf("artifact %q was already %s on %s: %s", id, ended.Action, ended.At.Format(time.RFC3339), ended.Reason)
	}
	existing.Status = status
	existing.Revisions = append(existing.Revisions, Revision{
		Action: action,
		By:     role,
		At:     now.UTC(),
		Reason: strings.TrimSpace(reason),
	})
	if err := existing.Validate(); err != nil {
		return Artifact{}, err
	}
	if err := s.write(path, existing, body); err != nil {
		return Artifact{}, err
	}
	return existing, nil
}

// loadOne reads the single artifact a mutation names, with the document below
// its frontmatter, and returns the file it came from so the mutation replaces
// exactly what it read. It goes through Load rather than guessing at a path,
// because an id is answered by whatever file claims it: an id two files claim is
// answered by neither, and a mutation that went straight to a path would edit
// one of them anyway.
func (s Store) loadOne(id string) (recorded Artifact, path, body string, err error) {
	set, err := s.Load()
	if err != nil {
		return Artifact{}, "", "", err
	}
	found, ok := set.Find(id)
	if !ok {
		for _, problem := range set.Problems {
			if idForPath(problem.Path) == id {
				return Artifact{}, "", "", fmt.Errorf("no artifact %q is recorded in %s; %s is not readable as one: %s",
					id, strings.Join(set.Homes, ", "), problem.Path, problem.Reason)
			}
		}
		return Artifact{}, "", "", fmt.Errorf("no artifact %q is recorded in %s", id, strings.Join(set.Homes, ", "))
	}
	root, err := resolveRoot(s.RepositoryRoot)
	if err != nil {
		return Artifact{}, "", "", err
	}
	path = filepath.Join(root, filepath.FromSlash(found.Path))
	content, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, "", "", fmt.Errorf("read artifact %q: %w", found.Path, err)
	}
	_, body, err = splitDocument(string(content))
	if err != nil {
		return Artifact{}, "", "", fmt.Errorf("read artifact %q: %w", found.Path, err)
	}
	return found, path, body, nil
}

// path returns where an artifact lands, absolute and repository-relative. The id
// is validated first because it becomes a file name, and the directory is held
// to the homes because an artifact filed outside them is one nothing reads.
func (s Store) path(id, directory string) (absolute, relative string, err error) {
	trimmed := strings.TrimSpace(id)
	if err := domain.ValidateIdentifier("artifact id", trimmed); err != nil {
		return "", "", err
	}
	name := trimmed + ".md"
	if strings.EqualFold(name, indexFileName) {
		return "", "", fmt.Errorf("%s is a directory index rather than an artifact, and is not read as one", indexFileName)
	}
	root, err := resolveRoot(s.RepositoryRoot)
	if err != nil {
		return "", "", err
	}
	homes, err := resolveDirectories("artifact home", s.Homes)
	if err != nil {
		return "", "", err
	}
	excluded, err := resolveDirectories("excluded directory", s.Excluded)
	if err != nil {
		return "", "", err
	}
	targets, err := resolveDirectories("artifact directory", []string{directory})
	if err != nil {
		return "", "", err
	}
	target := targets[0]
	if !within(target, homes) {
		return "", "", fmt.Errorf("artifact directory %q is not inside an artifact home (%s); an artifact filed outside one is a document nothing reads", directory, strings.Join(homes, ", "))
	}
	if within(target, excluded) {
		return "", "", fmt.Errorf("artifact directory %q carries an identity scheme of its own and is not read as an artifact home", directory)
	}
	relative = target + "/" + name
	return filepath.Join(root, filepath.FromSlash(relative)), relative, nil
}

// within reports a directory that is one of the named directories or below one.
func within(directory string, directories []string) bool {
	for _, candidate := range directories {
		if directory == candidate || strings.HasPrefix(directory, candidate+"/") {
			return true
		}
	}
	return false
}

// write replaces one artifact file. These are repository documents reviewed with
// the code rather than runtime state, so they are written with ordinary file
// permissions, and through a temporary file and a rename so an interrupted write
// cannot leave half a document where a whole one was.
func (s Store) write(path string, recorded Artifact, body string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	rendered, err := render(recorded, body)
	if err != nil {
		return err
	}
	if len(rendered) > MaxFileBytes {
		return fmt.Errorf("artifact %q renders to %d bytes, limit is %d", recorded.ID, len(rendered), MaxFileBytes)
	}
	temporary, err := os.CreateTemp(directory, ".artifact-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set artifact permissions: %w", err)
	}
	if _, err := temporary.WriteString(rendered); err != nil {
		temporary.Close()
		return fmt.Errorf("write artifact %q: %w", recorded.ID, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace artifact %q: %w", recorded.ID, err)
	}
	return nil
}

// render writes an artifact back out in the shape parse reads, so a mutation
// produces a file the next load accepts and a hand-written document and a
// generated one are the same thing. The body is written exactly as it was read,
// because it is the document and this package identifies documents rather than
// rewriting them.
func render(recorded Artifact, body string) (string, error) {
	metadata, err := yaml.Marshal(frontmatter{
		ID:        recorded.ID,
		Kind:      recorded.Kind,
		Title:     recorded.Title,
		Supports:  recorded.Supports,
		Status:    recorded.Status,
		Revisions: recorded.Revisions,
	})
	if err != nil {
		return "", fmt.Errorf("render artifact %q frontmatter: %w", recorded.ID, err)
	}
	var rendered strings.Builder
	rendered.WriteString(frontmatterFence + "\n")
	rendered.Write(metadata)
	rendered.WriteString(frontmatterFence + "\n")
	rendered.WriteString(body)
	return rendered.String(), nil
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
	metadata, _, err := splitDocument(content)
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

// splitDocument separates the fenced metadata at the top of a file from the
// document below it. Only the metadata is parsed — this package identifies
// documents rather than reading what they say — and the body is returned
// verbatim so a mutation can write back exactly the prose it read.
func splitDocument(content string) (metadata, body string, err error) {
	// A byte-order mark ahead of the fence is still frontmatter; an editor that
	// wrote one must not turn an artifact into an unidentified document.
	lines := strings.Split(strings.TrimPrefix(content, "\ufeff"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return "", "", errors.New("it does not open with `---` frontmatter naming its id, kind, title, status, and revisions")
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == frontmatterFence {
			return strings.Join(lines[1:index], "\n"), strings.Join(lines[index+1:], "\n"), nil
		}
	}
	return "", "", errors.New("its frontmatter is not closed by a `---` line")
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
