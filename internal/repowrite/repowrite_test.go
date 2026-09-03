package repowrite

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestARepositoryPathIsRefusedBeforeTheFilesystemIsTouched(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   ", "/etc/passwd", "..", "../elsewhere", "docs/../..", ".", "./"} {
		if _, err := Relative(value); err == nil {
			t.Errorf("Relative(%q) accepted a path that names nothing inside a repository", value)
		}
	}
	for _, value := range []string{"docs/product/brief.md", "./docs/product/brief.md", "docs/./product/brief.md", "brief.md"} {
		if _, err := Relative(value); err != nil {
			t.Errorf("Relative(%q) error = %v", value, err)
		}
	}
}

func TestAPathThatDoesNotExistYetResolvesToWhereItWouldBe(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	resolved, err := root.Resolve("docs/decisions/invariants/one.md")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := filepath.Join(root.Path(), "docs", "decisions", "invariants", "one.md"); resolved != want {
		t.Fatalf("Resolve() = %q, want %q", resolved, want)
	}
}

func TestASymlinkThatStaysInsideTheRepositoryIsFollowed(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	elsewhere := filepath.Join(root.Path(), "elsewhere")
	makeDirectory(t, elsewhere)
	link(t, elsewhere, filepath.Join(root.Path(), "docs"))

	written, err := root.WriteFile("docs/brief.md", []byte("what this says"))
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if want := filepath.Join(elsewhere, "brief.md"); written != want {
		t.Fatalf("WriteFile() = %q, want the resolved path %q", written, want)
	}
	if content := readFile(t, written); content != "what this says" {
		t.Fatalf("content = %q", content)
	}
}

func TestASymlinkOutOfTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	// The four ways a write leaves: the directory itself linked away by an
	// absolute path and by a relative one, a parent of it linked away, and the
	// document already there as a link to somewhere outside.
	for _, escape := range []struct {
		name    string
		prepare func(t *testing.T, root Root, outside string)
		path    string
	}{
		{
			name: "the directory is an absolute symlink out",
			prepare: func(t *testing.T, root Root, outside string) {
				link(t, outside, filepath.Join(root.Path(), "docs"))
			},
			path: "docs/brief.md",
		},
		{
			name: "the directory is a symlink climbing out",
			prepare: func(t *testing.T, root Root, outside string) {
				climbing, err := filepath.Rel(root.Path(), outside)
				if err != nil {
					t.Fatalf("Rel() error = %v", err)
				}
				link(t, climbing, filepath.Join(root.Path(), "docs"))
			},
			path: "docs/brief.md",
		},
		{
			name: "a parent of the directory is a symlink out",
			prepare: func(t *testing.T, root Root, outside string) {
				makeDirectory(t, filepath.Join(outside, "product"))
				link(t, outside, filepath.Join(root.Path(), "docs"))
			},
			path: "docs/product/brief.md",
		},
		{
			name: "the document is a symlink out",
			prepare: func(t *testing.T, root Root, outside string) {
				makeDirectory(t, filepath.Join(root.Path(), "docs"))
				writeFile(t, filepath.Join(outside, "brief.md"), "somebody else's document")
				link(t, filepath.Join(outside, "brief.md"), filepath.Join(root.Path(), "docs", "brief.md"))
			},
			path: "docs/brief.md",
		},
	} {
		t.Run(escape.name, func(t *testing.T) {
			t.Parallel()

			root, outside := repository(t)
			escape.prepare(t, root, outside)

			_, err := root.WriteFile(escape.path, []byte("what this says"))
			var refused *EscapeError
			if !errors.As(err, &refused) {
				t.Fatalf("WriteFile() error = %v, want an EscapeError", err)
			}
			if !strings.Contains(refused.Error(), "resolves outside the repository") {
				t.Errorf("the refusal reads %q, and does not say what is wrong with the path", refused)
			}
			assertUnchanged(t, outside)
		})
	}
}

func TestAWriteCreatesTheDirectoriesAboveIt(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	written, err := root.WriteFile("docs/decisions/invariants/one.md", []byte("must hold"))
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	info, err := os.Stat(written)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != filePermissions {
		t.Errorf("permissions = %v, want %v", info.Mode().Perm(), filePermissions)
	}
	if content := readFile(t, written); content != "must hold" {
		t.Fatalf("content = %q", content)
	}
}

func TestAWriteLeavesNoTemporaryFileBehind(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	if _, err := root.WriteFile("docs/brief.md", []byte("first")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := root.WriteFile("docs/brief.md", []byte("second")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root.Path(), "docs"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "brief.md" {
		t.Fatalf("the directory holds %v, and a replaced document should leave only itself", entries)
	}
	if content := readFile(t, filepath.Join(root.Path(), "docs", "brief.md")); content != "second" {
		t.Fatalf("content = %q, want the second write", content)
	}
}

// What OpenAppend is for is output that keeps arriving: a descriptor a process
// writes to for as long as it runs. So it adds to what is there rather than
// replacing it, and it makes the directories it needs, the same way a document
// written into a directory nobody created yet does.
func TestAppendingAddsToWhatIsAlreadyThere(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	for _, line := range []string{"first\n", "second\n"} {
		opened, err := root.OpenAppend("state/sink.log", 0o600, 0o700)
		if err != nil {
			t.Fatalf("OpenAppend() error = %v", err)
		}
		if _, err := opened.WriteString(line); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	written := filepath.Join(root.Path(), "state", "sink.log")
	if content := readFile(t, written); content != "first\nsecond\n" {
		t.Fatalf("content = %q, want the second open to have added to the first", content)
	}
	info, err := os.Stat(written)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the appended file is mode %v (%v), want the mode it was opened with", info.Mode().Perm(), err)
	}
}

// The reason this is in this package at all: a descriptor handed to a
// long-lived process is a write nobody watches afterwards, so where it points
// is decided here and refused here.
func TestAppendingThroughASymlinkOutOfTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	root, outside := repository(t)
	writeFile(t, filepath.Join(outside, "sink.log"), "somebody else's log")
	link(t, filepath.Join(outside, "sink.log"), filepath.Join(root.Path(), "sink.log"))

	opened, err := root.OpenAppend("sink.log", 0o600, 0o700)
	if opened != nil {
		opened.Close()
	}
	var refused *EscapeError
	if !errors.As(err, &refused) {
		t.Fatalf("OpenAppend() error = %v, want an EscapeError", err)
	}
	if content := readFile(t, filepath.Join(outside, "sink.log")); content != "somebody else's log" {
		t.Fatalf("the file outside the repository reads %q, and a refused append changed it", content)
	}
}

// A link inside the root is followed by the resolution, so what the append
// opens is the file that link points at rather than the link — which is what
// keeps refusing to follow one from refusing a layout that never left.
func TestAppendingThroughASymlinkThatStaysInsideTheRepositoryLandsOnItsTarget(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	elsewhere := filepath.Join(root.Path(), "elsewhere")
	writeFile(t, filepath.Join(elsewhere, "real.log"), "before\n")
	link(t, filepath.Join(elsewhere, "real.log"), filepath.Join(root.Path(), "sink.log"))

	opened, err := root.OpenAppend("sink.log", 0o600, 0o700)
	if err != nil {
		t.Fatalf("OpenAppend() error = %v", err)
	}
	if _, err := opened.WriteString("after\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if content := readFile(t, filepath.Join(elsewhere, "real.log")); content != "before\nafter\n" {
		t.Fatalf("content = %q, want the append to have landed on what the link points at", content)
	}
}

// The case the resolution cannot answer, because it is about the moment after
// it answered: a link standing at the target when the descriptor is opened. A
// descriptor handed to a process that runs for hours is not one write anybody
// will look at again, so the open refuses rather than follows. This plants the
// link where the check has already been made, which is what a race would do at a
// moment nothing can arrange on purpose.
func TestAppendingRefusesALinkStandingAtTheTargetItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("refusing to follow the final component is the Unix hosts' O_NOFOLLOW")
	}
	t.Parallel()

	root, outside := repository(t)
	writeFile(t, filepath.Join(outside, "sink.log"), "somebody else's log")
	// Straight at the resolved target, past the resolution rather than through
	// it: what this proves is that the open itself refuses a link.
	target := filepath.Join(root.Path(), "sink.log")
	link(t, filepath.Join(outside, "sink.log"), target)

	opened, err := os.OpenFile(target, appendFlags, 0o600)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Fatal("the append flags followed a symlink at the target")
	}
	if content := readFile(t, filepath.Join(outside, "sink.log")); content != "somebody else's log" {
		t.Fatalf("the file outside the repository reads %q, and a refused open changed it", content)
	}
}

// A directory is created with the same confinement a document is written with,
// and creating one that is already there is the same answer rather than a
// failure: the caller this exists for is a run resumed by a second process,
// which has to be handed the directory the first process made.
func TestMakingADirectoryCreatesItAndIsIdempotent(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	made, err := root.MakeDirectory("yoyodyne/scratch/run-one", 0o700)
	if err != nil {
		t.Fatalf("MakeDirectory() error = %v", err)
	}
	if want := filepath.Join(root.Path(), "yoyodyne", "scratch", "run-one"); made != want {
		t.Fatalf("MakeDirectory() = %q, want %q", made, want)
	}
	writeFile(t, filepath.Join(made, "check.log"), "what the run wrote")

	again, err := root.MakeDirectory("yoyodyne/scratch/run-one", 0o700)
	if err != nil {
		t.Fatalf("MakeDirectory() again error = %v", err)
	}
	if again != made {
		t.Fatalf("MakeDirectory() = %q, want the same %q", again, made)
	}
	if content := readFile(t, filepath.Join(made, "check.log")); content != "what the run wrote" {
		t.Fatalf("the directory read %q after being made again", content)
	}
}

// The refusal the entry point exists for: a link along the way pointing out of
// the root is refused rather than followed, so nothing is created outside it.
func TestMakingADirectoryThroughASymlinkOutOfTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	root, outside := repository(t)
	link(t, outside, filepath.Join(root.Path(), "yoyodyne"))

	made, err := root.MakeDirectory("yoyodyne/scratch/run-one", 0o700)
	var refused *EscapeError
	if !errors.As(err, &refused) {
		t.Fatalf("MakeDirectory() = %q, error = %v, want an EscapeError", made, err)
	}
	assertUnchanged(t, outside)
}

// And the paths that cannot name anything inside a root are refused here for the
// same reason they are refused for a document: before the filesystem is touched.
func TestMakingADirectoryRefusesAPathThatNamesNothingInside(t *testing.T) {
	t.Parallel()

	root, _ := repository(t)
	for _, value := range []string{"", "   ", "..", "../elsewhere", ".", "/absolute"} {
		if made, err := root.MakeDirectory(value, 0o700); err == nil {
			t.Errorf("MakeDirectory(%q) = %q, want a refusal", value, made)
		}
	}
}

func TestARootThatIsNotAUsableRepositoryIsRefused(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	writeFile(t, filepath.Join(base, "a-file"), "not a repository")

	for _, value := range []string{"", filepath.Join(base, "missing"), filepath.Join(base, "a-file")} {
		if _, err := NewRoot(value); err == nil {
			t.Errorf("NewRoot(%q) accepted something that is not a directory to write into", value)
		}
	}
}

// repository is a root to write into and a directory beside it that a confined
// write must never reach.
func repository(t *testing.T) (Root, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	inside := filepath.Join(base, "repository")
	outside := filepath.Join(base, "outside")
	makeDirectory(t, inside)
	makeDirectory(t, outside)
	root, err := NewRoot(inside)
	if err != nil {
		t.Fatalf("NewRoot() error = %v", err)
	}
	return root, outside
}

// assertUnchanged is what a refusal has to be worth: the refused write put
// nothing where it was pointing.
func assertUnchanged(t *testing.T, outside string) {
	t.Helper()
	err := filepath.WalkDir(outside, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if content := readFile(t, candidate); content == "what this says" {
			t.Errorf("the refused write landed at %s anyway", candidate)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk outside the repository: %v", err)
	}
}

func makeDirectory(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", directory, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	makeDirectory(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

func link(t *testing.T, target, name string) {
	t.Helper()
	makeDirectory(t, filepath.Dir(name))
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
}
