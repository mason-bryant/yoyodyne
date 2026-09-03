// Package repowrite is how this harness writes a document into the repository it
// is working on, and the one place the confinement that keeps such a write
// inside that repository is decided.
//
// The paths these writes land at come from a project's own configuration — the
// artifact homes, the invariants directory, the `.yoyodyne` directory an
// initialization creates — and a configuration is checked for traversal when it
// loads. That check is lexical, and the filesystem is not. A directory that
// reads as `docs/decisions` in the configuration is whatever the filesystem says
// it is by the time anything writes there, and one symlink along it puts the
// bytes outside the repository without a single `..` appearing anywhere. What a
// run promotes is the repository's own diff, so a write that escaped is a change
// nobody reviews, nobody promotes, and nobody finds afterwards: the document
// simply is not where the harness says it wrote it.
//
// So confinement is decided here, against the filesystem, immediately before the
// bytes are written rather than wherever the path was configured — a confinement
// that holds only when a caller remembered to check is not one. Every component
// below the root is resolved, a symlink is followed only while it stays inside
// the repository, and anything that leaves is refused rather than written.
//
// What this does not defend against is a symlink planted between the resolution
// and the rename. That is a race against something already running inside the
// repository, and the escapes this exists for are configuration and repository
// layout rather than a local attacker: a checkout that hostile has already lost
// more than a write.
package repowrite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The permissions a repository document and the directories above it are given.
// These are files reviewed with the code and committed beside it rather than
// runtime state, so they are readable exactly as the rest of a checkout is, and
// they are fixed here rather than per caller because "what a repository document
// is written as" is one answer rather than each writer's own.
const (
	filePermissions      fs.FileMode = 0o644
	directoryPermissions fs.FileMode = 0o755
)

// temporaryPattern names the file a write goes through before it is renamed into
// place. It is hidden and it is not Markdown, so a crash between the two leaves
// something the directories these writes land in already skip rather than a
// half-written document the next load believes in.
const temporaryPattern = ".yoyo-write-*.tmp"

// Root is one repository, its own symlinks already resolved, that every write
// through it lands inside.
type Root struct {
	path string
}

// NewRoot resolves the repository writes are confined to. The root is resolved
// through its own symlinks once, here, because every later answer is a
// comparison against it: a root still carrying a symlink would call the resolved
// form of a path inside it an escape.
func NewRoot(repositoryRoot string) (Root, error) {
	trimmed := strings.TrimSpace(repositoryRoot)
	if trimmed == "" {
		return Root{}, errors.New("repository root is required")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return Root{}, fmt.Errorf("resolve repository root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Root{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Root{}, fmt.Errorf("inspect repository root %q: %w", repositoryRoot, err)
	}
	if !info.IsDir() {
		return Root{}, fmt.Errorf("repository root %q is not a directory", repositoryRoot)
	}
	return Root{path: resolved}, nil
}

// Path is the repository every write is held inside, absolute and with its own
// symlinks resolved.
func (r Root) Path() string {
	return r.path
}

// EscapeError is the refusal this package exists for: a repository-relative path
// that resolved somewhere the repository does not contain. It is a type rather
// than a message because a caller that wants to say something better about it —
// which directory the operator configured, which document was being written —
// needs the part that left and where it went.
type EscapeError struct {
	// Path is the repository-relative path that was asked for.
	Path string
	// Component is the prefix of it that leaves, so a refusal names the symlink
	// rather than only the document that tripped over it.
	Component string
	// Resolved is where that prefix actually points.
	Resolved string
	// Root is the repository it had to stay inside.
	Root string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s resolves outside the repository: %s points at %s, which is not inside %s",
		e.Path, e.Component, e.Resolved, e.Root)
}

// Relative is the repository-relative slash form a confined path is named in. It
// refuses what cannot name a file inside a repository at all — nothing, an
// absolute path, one that climbs out lexically, and the repository root itself —
// before the filesystem is touched, so a plainly impossible path fails the same
// way whether or not anything exists yet.
func Relative(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("a repository path is required")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("%q is absolute and resolves outside the repository", value)
	}
	clean := path.Clean(filepath.ToSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q resolves outside the repository", value)
	}
	return clean, nil
}

// Resolve is where a repository-relative path actually writes to: every
// component below the root followed through whatever the filesystem has put
// there, and an EscapeError rather than a path for anything that leaves.
//
// The path does not have to exist. What does not exist cannot be a symlink, so
// once a component is missing the rest of the path is taken as written — which
// is what lets a writer create a directory and the document in it in one go.
func (r Root) Resolve(relative string) (string, error) {
	_, resolved, err := r.resolve(relative)
	return resolved, err
}

func (r Root) resolve(relative string) (clean, resolved string, err error) {
	clean, err = Relative(relative)
	if err != nil {
		return "", "", err
	}
	elements := strings.Split(clean, "/")
	current := r.path
	for index, element := range elements {
		candidate := filepath.Join(current, element)
		info, err := os.Lstat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return clean, filepath.Join(append([]string{candidate}, elements[index+1:]...)...), nil
		}
		if err != nil {
			return "", "", fmt.Errorf("inspect %s inside the repository: %w", strings.Join(elements[:index+1], "/"), err)
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			current = candidate
			continue
		}
		// A symlink is followed rather than refused outright: one that points
		// somewhere else in the same repository still lands the write inside it,
		// and a project that keeps its designs behind one has not thereby put them
		// out of reach. What is refused is where it points, not that it is one.
		linked, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", "", fmt.Errorf("resolve the symlink at %s inside the repository: %w", strings.Join(elements[:index+1], "/"), err)
		}
		if !r.contains(linked) {
			return "", "", &EscapeError{
				Path:      clean,
				Component: strings.Join(elements[:index+1], "/"),
				Resolved:  linked,
				Root:      r.path,
			}
		}
		current = linked
	}
	return clean, current, nil
}

// contains reports a resolved absolute path the repository holds. The root
// itself counts as inside it, because a symlink pointing at the repository root
// has not left the repository.
func (r Root) contains(candidate string) bool {
	relative, err := filepath.Rel(r.path, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// OpenAppend opens the file a root-relative path names for appending, creating
// it and any missing directories above it, and hands back the open descriptor.
//
// It is here for the output nobody can hold in a byte slice: a long-lived child
// process writes its standard output and standard error into a descriptor for
// as long as it runs, so the write-and-rename below cannot express it, and a
// writer that reached for `os.OpenFile` itself would be a write outside this
// package deciding its own containment. Confinement is the same and is decided
// the same way — every existing component below the root is resolved before
// anything is created or opened, and one that leaves the root is refused rather
// than followed.
//
// The modes are the caller's, unlike the fixed ones a repository document is
// written with, because what this opens is not a repository document: a file
// holding what the harness is doing, under a state directory, wants the
// permissions its own root does rather than a checkout's.
func (r Root) OpenAppend(relative string, file, directory fs.FileMode) (*os.File, error) {
	clean, target, err := r.resolve(relative)
	if err != nil {
		return nil, err
	}
	// Every existing component of the target was checked above and everything
	// below the first missing one does not exist yet, so there is nothing left
	// here for MkdirAll to follow out of the root.
	if err := os.MkdirAll(filepath.Dir(target), directory); err != nil {
		return nil, fmt.Errorf("create %s: %w", path.Dir(clean), err)
	}
	// Opened refusing to follow a link, which is this path's half of what the
	// rename below does for a written document. The resolve above already refuses
	// a final component that points out of the root, so what is left is the
	// moment after that answer: a link planted at the target between the check
	// and the open. A rename replaces such a link rather than writing through it,
	// and there is no rename here — what is handed back is a descriptor a
	// long-lived process writes to for as long as it runs, so following one would
	// send everything it ever says somewhere nobody is looking.
	opened, err := os.OpenFile(target, appendFlags, file)
	if err != nil {
		return nil, fmt.Errorf("open %s for appending: %w", clean, err)
	}
	return opened, nil
}

// WriteFile replaces the document a repository-relative path names and returns
// where it landed, which is the resolved path rather than the one asked for.
//
// The bytes go into a temporary file beside the target and are renamed over it,
// so an interrupted write leaves the previous document whole rather than half of
// the new one, and a reader never sees a partial file. Renaming is also what
// keeps a target that is itself a symlink honest: the link is replaced rather
// than written through. It never gets that far here — a link out of the
// repository is refused above — but the two together are why nothing this
// package writes can appear outside the repository.
func (r Root) WriteFile(relative string, content []byte) (string, error) {
	clean, target, err := r.resolve(relative)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(target)
	// Every existing component of the target was checked above and everything
	// below the first missing one does not exist yet, so there is nothing left
	// here for MkdirAll to follow out of the repository.
	if err := os.MkdirAll(directory, directoryPermissions); err != nil {
		return "", fmt.Errorf("create %s: %w", path.Dir(clean), err)
	}
	temporary, err := os.CreateTemp(directory, temporaryPattern)
	if err != nil {
		return "", fmt.Errorf("create a temporary file beside %s: %w", clean, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(filePermissions); err != nil {
		temporary.Close()
		return "", fmt.Errorf("set the permissions of %s: %w", clean, err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write %s: %w", clean, err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", clean, err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", fmt.Errorf("replace %s: %w", clean, err)
	}
	return target, nil
}

// MakeDirectory creates the directory a root-relative path names, and any
// missing directory above it, and returns where it landed.
//
// It is here for the caller that needs the directory rather than anything in it:
// a per-run scratch directory is created by the harness and written into by an
// agent, so there is no document for the write-and-rename above to carry. A
// caller reaching for `os.MkdirAll` itself would be a write outside this package
// deciding its own containment, which is the one thing that has no exceptions —
// so the entry point is here rather than the containment being restated there.
//
// Confinement is decided exactly as it is above: every existing component below
// the root is resolved before anything is created, and one that points out of
// the root is refused rather than followed. Creating is idempotent, so a caller
// that asks twice is given the same directory rather than a failure — which is
// what a run resumed by a second process needs.
//
// The mode is the caller's for the reason OpenAppend's are: what this creates is
// not a repository document, and a directory holding what one run is doing wants
// the permissions of the root it sits under rather than a checkout's.
func (r Root) MakeDirectory(relative string, mode fs.FileMode) (string, error) {
	clean, target, err := r.resolve(relative)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, mode); err != nil {
		return "", fmt.Errorf("create %s: %w", clean, err)
	}
	return target, nil
}
