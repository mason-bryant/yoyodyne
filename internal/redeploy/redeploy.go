// Package redeploy is how a process that outlives a deploy takes the deploy up.
//
// Everything else in the harness is a command: it starts, it acts on the binary
// it was started from, and it exits, so "which build is this running" is
// answered by whichever build the operator last typed. A watch session is not
// that. It stays open for days, and it goes on choosing and dispatching work
// from the binary it was started with while fixes land on the main line behind
// it — which is invisible from everything else the record says, because the runs
// it starts look exactly like runs started by a current one.
//
// Nothing outside the process can close that gap. Killing a session cancels the
// run it is carrying, so an external job may only bounce it while the machine is
// idle; with a deep backlog and more than one developer slot the next run starts
// the moment one settles, and a poll at any interval never lands in that window.
// The only thing standing in the gap between runs is the session itself.
//
// So this is the small half of that: which file this process was started from,
// whether that file has been replaced since, and re-executing it. When the
// session takes it up is the scheduler's — between runs, never during one.
package redeploy

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrUnsupported is a platform where a process cannot be replaced by another
// binary in place. It is named so a caller can tell it from a reading that
// failed: a session here cannot redeploy itself and can do everything else it
// ever did, and refusing to watch at all over it would take away more than this
// package was ever asked to add.
var ErrUnsupported = errors.New("a process cannot replace its own image on this platform")

// Binary is the executable a process is running: where it was resolved from, and
// what the file looked like at the moment the process asked.
//
// Both are read once, at the start, and that is the point. A path resolved after
// a deploy has replaced the file is a path the operating system may no longer
// name the same way, and a file whose size and time are read after the
// replacement is a file that has always looked current.
type Binary struct {
	path string
	// args and env are the invocation this process was started as, carried so the
	// build that replaces it watches the same queue with the same bounds. A
	// restart that quietly dropped `--budget` would be a session spending against
	// a number the operator set and this took away.
	args []string
	env  []string
	// modTime and size are what the file was when the session started. They are
	// the whole of the comparison: a deploy writes the file, and a file that has
	// not been written is the one this process is executing.
	modTime time.Time
	size    int64
}

// Running resolves the binary this process is executing. It is called before the
// work starts rather than when a redeploy is considered, for the reason the
// fields above say: this is the only moment the answer is known to be about the
// build that is actually running.
func Running() (*Binary, error) {
	if !imageReplacement {
		return nil, ErrUnsupported
	}
	path, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve the running executable: %w", err)
	}
	return at(path, os.Args, os.Environ())
}

func at(path string, args, env []string) (*Binary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read the running executable %s: %w", path, err)
	}
	return &Binary{
		path:    path,
		args:    append([]string(nil), args...),
		env:     append([]string(nil), env...),
		modTime: info.ModTime(),
		size:    info.Size(),
	}, nil
}

// Path names the file, so what a session says about restarting can say which
// file it means.
func (b *Binary) Path() string { return b.path }

// Args is the invocation this process was started as, copied so a caller can
// change it and hand it back to Take.
//
// Handing it back rather than replaying it from here is the whole of how a bound
// survives a restart. A session given a budget or a count of runs has spent some
// of it by the time a build lands, and the same command line run again is that
// bound starting over — which on a machine that deploys several times a day is
// not a bound at all. What to reduce and how is the caller's, because the flags
// belong to the command rather than to this.
func (b *Binary) Args() []string { return append([]string(nil), b.args...) }

// Replaced reports the file this process was started from having been written
// since it started, which is what a deploy does to it.
//
// It is the file rather than the repository that is asked, and the difference
// matters: a change merged to the main line is not running anywhere until
// somebody builds it, and a session that restarted on a merge would restart into
// the same build it was already executing. What the session is behind is the
// binary that was installed over it.
//
// A time that moved and a size that changed are both the file having been
// written; either is enough, because a build that produces a byte-identical
// executable is a redeploy that changes nothing and costs one restart between
// runs.
func (b *Binary) Replaced() (bool, error) {
	info, err := os.Stat(b.path)
	if err != nil {
		return false, fmt.Errorf("read the executable at %s: %w", b.path, err)
	}
	return !info.ModTime().Equal(b.modTime) || info.Size() != b.size, nil
}

// Take re-executes this process from the same path, as the invocation given and
// with the environment it was started with. It does not return when it succeeds:
// the process image is replaced where it stands, keeping the process identifier,
// the terminal, and anything watching this process from outside.
//
// It is a replacement rather than a child for exactly that reason. A session
// that spawned a successor and exited would be two processes choosing work for
// the moment they overlapped, and would leave whatever started the first one —
// a terminal, a supervisor — holding a process that is no longer the session.
//
// The invocation is the caller's rather than the one this recorded, because a
// caller that has spent part of a bound has to say so; Args is where the
// original comes from.
func (b *Binary) Take(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("re-execute %s: an invocation with no arguments names no program", b.path)
	}
	if err := replaceImage(b.path, args, b.env); err != nil {
		return fmt.Errorf("re-execute %s: %w", b.path, err)
	}
	// Reached only where the operating system returned from a call that replaces
	// the image, which it does not do without failing. Saying so is cheaper than
	// leaving a caller to conclude the restart worked.
	return fmt.Errorf("re-executing %s returned rather than replacing this process", b.path)
}
