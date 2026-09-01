package redeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The whole of the comparison: a file written since the session started is a
// deploy the session is behind, and a file nobody has touched is the build it is
// executing. The time is set explicitly rather than left to the write, because a
// filesystem that stamps whole seconds would otherwise have the two writes
// looking identical and the test passing for the wrong reason.
func TestReplacedReportsABinaryThatWasWrittenOverIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "yoyo")
	writeBinary(t, path, "the build the session started from", time.Now().Add(-time.Hour))
	binary, err := at(path, []string{path, "work", "--watch"}, os.Environ())
	if err != nil {
		t.Fatalf("at() error = %v", err)
	}

	if replaced, err := binary.Replaced(); err != nil || replaced {
		t.Fatalf("Replaced() = %v, %v, want a file nobody has written reported as the one this process is running", replaced, err)
	}

	writeBinary(t, path, "the build somebody deployed over it", time.Now())
	replaced, err := binary.Replaced()
	if err != nil {
		t.Fatalf("Replaced() error = %v", err)
	}
	if !replaced {
		t.Fatal("Replaced() = false, want the deploy that landed over this session reported")
	}
}

// A build of the same size written in the same second is still a different
// build. The size is the other half of the answer for exactly that case, and
// neither half is asked to carry it alone.
func TestReplacedReportsABinaryOfADifferentSizeWrittenAtTheSameMoment(t *testing.T) {
	t.Parallel()

	moment := time.Now().Add(-time.Hour)
	path := filepath.Join(t.TempDir(), "yoyo")
	writeBinary(t, path, "the build the session started from", moment)
	binary, err := at(path, []string{path}, os.Environ())
	if err != nil {
		t.Fatalf("at() error = %v", err)
	}

	writeBinary(t, path, "a longer build deployed in the same second", moment)
	replaced, err := binary.Replaced()
	if err != nil {
		t.Fatalf("Replaced() error = %v", err)
	}
	if !replaced {
		t.Fatal("Replaced() = false, want a build of a different size reported however its time reads")
	}
}

// A binary that cannot be read answers neither way, and says so. A session told
// "not replaced" by a failed reading would go on choosing work believing a
// comparison nobody made, which is the state this whole package exists to end.
func TestReplacedReportsAnExecutableItCannotRead(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "yoyo")
	writeBinary(t, path, "the build the session started from", time.Now())
	binary, err := at(path, []string{path}, os.Environ())
	if err != nil {
		t.Fatalf("at() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove the executable: %v", err)
	}

	replaced, err := binary.Replaced()
	if err == nil {
		t.Fatalf("Replaced() = %v, nil, want the reading that could not be made reported as one", replaced)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want the file somebody has to look at named", err)
	}
}

// Taking up a build that is no longer there fails rather than ending the process
// quietly. The session has already drained itself by then, so a caller that was
// told nothing would leave a stopped line nobody was told about.
func TestTakeReportsABinaryItCannotExecute(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "yoyo")
	writeBinary(t, path, "the build the session started from", time.Now())
	binary, err := at(path, []string{path}, os.Environ())
	if err != nil {
		t.Fatalf("at() error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove the executable: %v", err)
	}

	if err := binary.Take(binary.Args()); err == nil {
		t.Fatal("Take() error = nil, want a restart that could not be made reported")
	}
	// And an invocation with nothing in it is refused rather than handed to the
	// operating system, because argv[0] is the program's own name and a caller
	// that lost it has lost the invocation.
	if err := binary.Take(nil); err == nil {
		t.Fatal("Take(nil) error = nil, want an invocation naming no program refused")
	}
}

// The invocation is the caller's to change: what a session was started as, in a
// copy, so reducing a bound it has spent part of cannot reach back into the
// binary this was read from.
func TestArgsHandsBackACopyOfTheInvocation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "yoyo")
	writeBinary(t, path, "the build the session started from", time.Now())
	binary, err := at(path, []string{path, "work", "--watch", "--budget", "50"}, os.Environ())
	if err != nil {
		t.Fatalf("at() error = %v", err)
	}

	args := binary.Args()
	args[4] = "4.99"
	if again := binary.Args(); again[4] != "50" {
		t.Fatalf("Args() = %v, want the recorded invocation unchanged by what a caller did to its copy", again)
	}
}

// Running answers about this process, which is the case every session takes:
// the test binary is the running executable, nobody has written it, and it is
// the file a restart would re-execute.
func TestRunningResolvesTheProcessesOwnBinary(t *testing.T) {
	t.Parallel()

	binary, err := Running()
	if err != nil {
		t.Fatalf("Running() error = %v", err)
	}
	if binary.Path() == "" {
		t.Fatal("Path() is empty, want the file this process is executing")
	}
	replaced, err := binary.Replaced()
	if err != nil {
		t.Fatalf("Replaced() error = %v", err)
	}
	if replaced {
		t.Fatal("Replaced() = true, want a process nobody has deployed over reported as current")
	}
}

func writeBinary(t *testing.T, path, content string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write the executable: %v", err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("stamp the executable: %v", err)
	}
}
