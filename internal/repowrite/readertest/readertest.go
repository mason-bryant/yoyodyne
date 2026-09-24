// Package readertest is the shared topology matrix every repository-confined
// reader in this harness is held to.
//
// It is the other half of writertest, and it exists because the two halves fail
// differently. A write that escaped puts bytes somewhere nobody promotes, and
// the document is simply missing afterwards. A read that escaped is worse: it
// delivers a document nobody committed and nobody reviewed under a
// repository-relative path, so an invariant planted outside the checkout arrives
// in every developer's context and every reviewer's evidence as a constraint the
// repository holds itself to. Nothing downstream can tell it from one that was.
//
// The topologies are the writers' own, for the reason writertest gives: the
// escape nobody covered is the one topology a private matrix was missing. Every
// case ends in the same question — was the planted document delivered — asked of
// whatever the reader returned, because a reader that reports an error after
// handing the document back has escaped just as completely as one that reported
// success.
package readertest

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Reader is one repository-confined reader, as the matrix drives it.
type Reader struct {
	// Name is what the reader is called in a failure, so a broken case names the
	// reader rather than only the topology.
	Name string
	// Directory is the repository-relative directory the reader has been
	// configured to read. The matrix builds each topology at this path, which is
	// what makes one set of cases fit readers that agree on nothing else.
	Directory string
	// File is the document the matrix plants, named so a delivery can be
	// recognized wherever the topology put the bytes.
	File string
	// Document is what the matrix plants in File. Each reader accepts a shape of
	// its own, and a document one of them would refuse as malformed would pass
	// every case for the wrong reason.
	Document string
	// Read loads Directory below root and returns the repository-relative paths
	// of the documents the reader delivered. Reporting a file as unreadable is
	// not delivering it and does not belong here; an error the reader returned
	// belongs in the error rather than in the paths.
	Read func(t *testing.T, root string) ([]string, error)
}

// Run holds one reader to every topology.
func Run(t *testing.T, reader Reader) {
	t.Helper()
	for _, shape := range cases() {
		t.Run(shape.name, func(t *testing.T) {
			if shape.needsParent && path.Dir(reader.Directory) == "." {
				t.Skipf("%s reads the repository root itself, so there is no parent directory to plant a symlink at", reader.Name)
			}
			root, outside := repository(t)
			shape.prepare(t, root, reader, outside)

			delivered, err := reader.Read(t, root)
			switch shape.expected {
			case finds:
				if err != nil {
					t.Fatalf("%s refused a topology that stays inside the repository (%s): %v", reader.Name, shape.name, err)
				}
				if !holds(delivered, reader.File) {
					t.Fatalf("%s read a repository holding %s and delivered %v (%s)", reader.Name, reader.File, delivered, shape.name)
				}
			case withholds:
				if holds(delivered, reader.File) {
					t.Fatalf("%s delivered %s from outside the repository as %v (%s)", reader.Name, reader.File, delivered, shape.name)
				}
			}
		})
	}
}

// What a confined reader is allowed to do about one topology. A reader that
// refuses a topology outright and one that reads it and finds nothing are both
// answers this matrix accepts, because the question it asks is only ever whether
// the planted document was handed back as the repository's own.
type expectation int

const (
	finds expectation = iota
	withholds
)

// shape is one arrangement the filesystem can be in under a reader's configured
// directory.
type shape struct {
	name string
	// prepare builds the arrangement below root and plants the reader's document
	// wherever this topology puts it. outside is a directory beside the
	// repository, which is where a case that escapes plants it.
	prepare  func(t *testing.T, root string, reader Reader, outside string)
	expected expectation
	// needsParent marks a case that plants its symlink above the reader's
	// directory, which a reader configured at the repository root has none of.
	needsParent bool
}

func cases() []shape {
	return []shape{
		{
			name: "a directory that does not exist",
			prepare: func(t *testing.T, _ string, reader Reader, outside string) {
				plant(t, filepath.Join(outside, filepath.FromSlash(reader.Directory)), reader)
			},
			expected: withholds,
		},
		{
			name: "a directory that is already there",
			prepare: func(t *testing.T, root string, reader Reader, _ string) {
				plant(t, filepath.Join(root, filepath.FromSlash(reader.Directory)), reader)
			},
			expected: finds,
		},
		{
			// A symlink that stays inside the repository has not left it, and the
			// read follows it for the reason a write does: a project that keeps its
			// documents behind one has not thereby put them out of reach.
			name: "a directory that is a symlink to somewhere else in the repository",
			prepare: func(t *testing.T, root string, reader Reader, _ string) {
				elsewhere := filepath.Join(root, "elsewhere-inside")
				makeDirectory(t, elsewhere)
				plant(t, elsewhere, reader)
				link(t, elsewhere, filepath.Join(root, filepath.FromSlash(reader.Directory)))
			},
			expected: finds,
		},
		{
			name: "a directory that is a symlink out of the repository",
			prepare: func(t *testing.T, root string, reader Reader, outside string) {
				plant(t, outside, reader)
				link(t, outside, filepath.Join(root, filepath.FromSlash(reader.Directory)))
			},
			expected: withholds,
		},
		{
			name: "a directory that is a symlink climbing out of the repository",
			prepare: func(t *testing.T, root string, reader Reader, outside string) {
				// Written as `../../outside` rather than as an absolute path, because
				// that is how somebody writes one by hand and a check that only
				// understood absolute targets would miss it.
				plant(t, outside, reader)
				target := filepath.Join(root, filepath.FromSlash(reader.Directory))
				makeDirectory(t, filepath.Dir(target))
				climbing, err := filepath.Rel(filepath.Dir(target), outside)
				if err != nil {
					t.Fatalf("Rel() error = %v", err)
				}
				link(t, climbing, target)
			},
			expected: withholds,
		},
		{
			// The reproduction this matrix was written for: the invariants directory
			// reached through a symlinked `docs`, holding a constraint nobody
			// committed, delivered as `docs/decisions/invariants/planted.md`.
			name: "a parent of the directory that is a symlink out of the repository",
			prepare: func(t *testing.T, root string, reader Reader, outside string) {
				parent := strings.Split(reader.Directory, "/")[0]
				// The rest of the configured directory exists below the symlink, so the
				// reader finds a real directory at exactly the path it was configured
				// with, and the only thing wrong is which repository it is in.
				plant(t, filepath.Join(outside, filepath.FromSlash(strings.TrimPrefix(reader.Directory, parent+"/"))), reader)
				link(t, outside, filepath.Join(root, parent))
			},
			expected:    withholds,
			needsParent: true,
		},
		{
			name: "the document itself a symlink out of the repository",
			prepare: func(t *testing.T, root string, reader Reader, outside string) {
				plant(t, outside, reader)
				directory := filepath.Join(root, filepath.FromSlash(reader.Directory))
				makeDirectory(t, directory)
				link(t, filepath.Join(outside, reader.File), filepath.Join(directory, reader.File))
			},
			expected: withholds,
		},
	}
}

// repository is a fresh repository to read and a directory beside it holding
// what the repository must never deliver. They are siblings rather than nested
// so that "outside the repository" is somewhere a relative symlink can plausibly
// reach, which is how the escapes this matrix covers actually get written.
func repository(t *testing.T) (root, outside string) {
	t.Helper()
	// A temporary directory is itself a symlink on macOS, and these readers
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

// holds reports whether the reader delivered the planted document. It is matched
// by name rather than by path, because the whole of the defect is that an escaped
// document arrives under a path the repository appears to hold.
func holds(delivered []string, file string) bool {
	for _, candidate := range delivered {
		if path.Base(filepath.ToSlash(candidate)) == file {
			return true
		}
	}
	return false
}

func plant(t *testing.T, directory string, reader Reader) {
	t.Helper()
	makeDirectory(t, directory)
	if err := os.WriteFile(filepath.Join(directory, reader.File), []byte(reader.Document), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", reader.File, err)
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
