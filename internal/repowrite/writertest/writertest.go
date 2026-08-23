// Package writertest is the shared topology matrix every repository-confined
// writer in this harness is held to.
//
// The confinement these writers depend on is a property of the filesystem they
// write into rather than of the strings they were configured with, and each of
// them reaches that filesystem through a directory of its own: an artifact home,
// the invariants directory, the `.yoyodyne` directory an initialization creates.
// A matrix each writer keeps its own copy of is a matrix that drifts, and the
// topology one copy is missing is exactly the escape nobody covered — which is
// how the escapes this exists for got in. So the topologies live here once, and
// a writer is confined when it survives all of them rather than when its own
// test says so.
//
// Every case ends in the same question, asked whichever way the write went: is
// there still nothing new outside the repository. A refusal is checked for it as
// carefully as an acceptance, because a writer that reports a failure after
// writing the bytes anyway has escaped just as completely as one that reported
// success.
package writertest

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Writer is one repository-confined writer, as the matrix drives it.
type Writer struct {
	// Name is what the writer is called in a failure, so a broken case names the
	// writer rather than only the topology.
	Name string
	// Directory is the repository-relative directory the writer has been
	// configured to write into. The matrix builds each topology at this path,
	// which is what makes one set of cases fit writers that agree on nothing else.
	Directory string
	// File is the document Write produces, so the matrix can find the bytes
	// wherever the topology sent them.
	File string
	// Write performs one write into Directory below root, on a repository the
	// matrix has just prepared, and returns what the writer said about it.
	Write func(t *testing.T, root string) error
}

// Run holds one writer to every topology.
func Run(t *testing.T, writer Writer) {
	t.Helper()
	for _, shape := range cases() {
		t.Run(shape.name, func(t *testing.T) {
			if shape.needsParent && path.Dir(writer.Directory) == "." {
				t.Skipf("%s writes directly into the repository root, so there is no parent directory to plant a symlink at", writer.Name)
			}
			root, outside := repository(t)
			shape.prepare(t, root, writer, outside)
			planted := snapshot(t, outside)

			err := writer.Write(t, root)
			assertNothingNew(t, outside, planted, writer, shape.name)

			switch shape.expected {
			case writesInside:
				if err != nil {
					t.Fatalf("%s refused a topology that stays inside the repository (%s): %v", writer.Name, shape.name, err)
				}
				if !contains(t, root, writer.File) {
					t.Fatalf("%s reported writing %s, and no such file is anywhere inside the repository (%s)", writer.Name, writer.File, shape.name)
				}
			case refuses:
				if err == nil {
					t.Fatalf("%s wrote %s into a topology that leaves the repository (%s) and reported no error", writer.Name, writer.File, shape.name)
				}
			}
		})
	}
}

// What a confined writer is allowed to do about one topology. There are three
// answers rather than two because replacing a symlink is not the same as
// following it: a writer that renames its document over a link pointing out of
// the repository has kept every byte inside, and one that refuses to touch it at
// all has too. Only writing through it is an escape, and that is what the
// matrix checks of this case whichever of the two the writer chose.
type expectation int

const (
	writesInside expectation = iota
	refuses
	eitherWay
)

// shape is one arrangement the filesystem can be in under a writer's configured
// directory.
type shape struct {
	name string
	// prepare builds the arrangement below root. outside is a directory beside
	// the repository that nothing may write anything new into.
	prepare  func(t *testing.T, root string, writer Writer, outside string)
	expected expectation
	// needsParent marks a case that plants its symlink above the writer's
	// directory, which a writer configured at the repository root has none of.
	needsParent bool
}

func cases() []shape {
	return []shape{
		{
			name:     "a directory that does not exist yet",
			prepare:  func(*testing.T, string, Writer, string) {},
			expected: writesInside,
		},
		{
			name: "a directory that is already there",
			prepare: func(t *testing.T, root string, writer Writer, _ string) {
				makeDirectory(t, filepath.Join(root, filepath.FromSlash(writer.Directory)))
			},
			expected: writesInside,
		},
		{
			// A writer may follow this one or refuse it. Following it lands the
			// document inside the repository, and refusing it fails closed on a
			// layout nobody asked for; what neither may do is put anything outside,
			// which is what this case is here to check of both answers.
			name: "a directory that is a symlink to somewhere else in the repository",
			prepare: func(t *testing.T, root string, writer Writer, _ string) {
				elsewhere := filepath.Join(root, "elsewhere-inside")
				makeDirectory(t, elsewhere)
				link(t, elsewhere, filepath.Join(root, filepath.FromSlash(writer.Directory)))
			},
			expected: eitherWay,
		},
		{
			name: "a directory that is a symlink out of the repository",
			prepare: func(t *testing.T, root string, writer Writer, outside string) {
				link(t, outside, filepath.Join(root, filepath.FromSlash(writer.Directory)))
			},
			expected: refuses,
		},
		{
			name: "a directory that is a symlink climbing out of the repository",
			prepare: func(t *testing.T, root string, writer Writer, outside string) {
				// Written as `../../outside` rather than as an absolute path, because
				// that is how somebody writes one by hand and a check that only
				// understood absolute targets would miss it.
				target := filepath.Join(root, filepath.FromSlash(writer.Directory))
				makeDirectory(t, filepath.Dir(target))
				climbing, err := filepath.Rel(filepath.Dir(target), outside)
				if err != nil {
					t.Fatalf("Rel() error = %v", err)
				}
				link(t, climbing, target)
			},
			expected: refuses,
		},
		{
			name: "a parent of the directory that is a symlink out of the repository",
			prepare: func(t *testing.T, root string, writer Writer, outside string) {
				parent := strings.Split(writer.Directory, "/")[0]
				// The rest of the configured directory exists below the symlink, so the
				// writer finds a real directory at exactly the path it was configured
				// with, and the only thing wrong is which repository it is in.
				makeDirectory(t, filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(writer.Directory, parent+"/"))))
				link(t, outside, filepath.Join(root, parent))
			},
			expected:    refuses,
			needsParent: true,
		},
		{
			name: "the document already there as a symlink out of the repository",
			prepare: func(t *testing.T, root string, writer Writer, outside string) {
				directory := filepath.Join(root, filepath.FromSlash(writer.Directory))
				makeDirectory(t, directory)
				link(t, filepath.Join(outside, writer.File), filepath.Join(directory, writer.File))
			},
			expected: eitherWay,
		},
	}
}

// repository is a fresh repository to write into and a directory beside it that
// nothing may write anything new into. They are siblings rather than nested so
// that "outside the repository" is somewhere a relative symlink can plausibly
// reach, which is how the escapes this matrix covers actually get written.
func repository(t *testing.T) (root, outside string) {
	t.Helper()
	// A temporary directory is itself a symlink on macOS, and these writers
	// resolve their root through symlinks, so the matrix hands them what they will
	// resolve to rather than something they will report as a different path.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	root = filepath.Join(base, "repository")
	outside = filepath.Join(base, "outside")
	makeDirectory(t, root)
	makeDirectory(t, outside)
	return root, outside
}

// contains reports whether a file by that name is anywhere inside the
// repository. Where exactly is the topology's business rather than the writer's:
// a document written through a symlink that stayed inside is still inside.
func contains(t *testing.T, root, name string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == name {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return found
}

// snapshot is everything a case planted outside the repository before the write,
// so what the write itself put there can be told from it.
func snapshot(t *testing.T, outside string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(outside, func(candidate string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(outside, candidate)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk outside the repository: %v", err)
	}
	sort.Strings(found)
	return found
}

// assertNothingNew is the assertion the whole matrix is for: whatever the writer
// did or said, the world outside the repository is as the case left it.
func assertNothingNew(t *testing.T, outside string, planted []string, writer Writer, name string) {
	t.Helper()
	before := map[string]bool{}
	for _, entry := range planted {
		before[entry] = true
	}
	var appeared []string
	for _, entry := range snapshot(t, outside) {
		if !before[entry] {
			appeared = append(appeared, entry)
		}
	}
	if len(appeared) != 0 {
		t.Fatalf("%s wrote %s outside the repository (%s)", writer.Name, strings.Join(appeared, ", "), name)
	}
}

func makeDirectory(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", directory, err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(name), err)
	}
	if err := os.Symlink(target, name); err != nil {
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, errors.ErrUnsupported) {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		t.Fatalf("Symlink(%s, %s) error = %v", target, name, err)
	}
}
