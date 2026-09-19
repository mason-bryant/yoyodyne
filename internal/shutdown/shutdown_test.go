package shutdown

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// What happens as soon as a goroutine is scheduled is waited for here and never
// bounded: two seconds was the bound, and it is the kind a loaded machine under
// the race detector reaches with the goroutine still to be scheduled. The one
// wait below that keeps a clock is the negative one, which a slow machine can
// only pass wrongly and never fail wrongly.

// A stop signal cancels the work and puts the operating system's disposition
// back in the same moment.
//
// The second half is the one that was missing. A process holding SIGTERM away
// from its default disposition swallows every signal after the first, so an
// operator watching a wedged session ignore them has nothing left below SIGKILL
// -- which is what an hour of silence on 2026-09-05 cost, and what this makes
// impossible whatever the process is stuck in.
func TestAStopSignalCancelsTheWorkAndGivesTheSignalBack(t *testing.T) {
	t.Parallel()

	signals := make(chan os.Signal, 1)
	restored := make(chan struct{})
	ctx, stop := answering(context.Background(), signals, time.Hour,
		func() { close(restored) },
		func() { t.Error("the process exited itself while it still had its grace") })
	defer stop()

	signals <- syscall.SIGTERM
	<-ctx.Done()
	// Given back, or a second signal would be swallowed exactly as the first
	// was.
	<-restored
}

// A process that does not stop within its grace exits where it stands.
//
// This is the whole of what the deadline is for: the work has been asked to
// stop, whatever it is doing is not observing the request, and the process
// ending itself is what turns an unkillable session into one restart.
func TestAProcessThatDoesNotStopWithinItsGraceExits(t *testing.T) {
	t.Parallel()

	signals := make(chan os.Signal, 1)
	exited := make(chan struct{})
	_, stop := answering(context.Background(), signals, time.Millisecond,
		func() {},
		func() { close(exited) })
	defer stop()

	signals <- syscall.SIGTERM
	<-exited
}

// A process that stops on the cancellation is not exited by the grace measured
// against it. Nothing about the ordinary stop changes: the work returns, the
// command reports what it did, and the exit code is the command's rather than
// this.
func TestAProcessThatStopsIsNotExitedBehindIt(t *testing.T) {
	t.Parallel()

	signals := make(chan os.Signal, 1)
	exited := make(chan struct{})
	ctx, stop := answering(context.Background(), signals, 20*time.Millisecond,
		func() {},
		func() { close(exited) })

	signals <- syscall.SIGTERM
	<-ctx.Done()
	// The work returning, which is the whole of what a stopping process is
	// expected to do.
	stop()

	select {
	case <-exited:
		t.Fatal("a process that stopped when it was asked to was exited by the grace behind it")
	case <-time.After(200 * time.Millisecond):
	}
}

// Calling the returned function more than once is what a command that both
// returns and defers it does, and it is not a reason to panic on a process that
// has already done its work.
func TestStoppingTwiceIsNotAFailure(t *testing.T) {
	t.Parallel()

	_, stop := answering(context.Background(), make(chan os.Signal, 1), time.Hour, func() {}, func() {})
	stop()
	stop()
}
