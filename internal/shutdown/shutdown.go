// Package shutdown is how a process that has stopped choosing still answers
// being asked to stop.
//
// A stop signal here cancels the context every command is running under, and for
// a command that is the whole of what it needs to be: the command is the only
// thing the process is doing, it returns on the cancellation, and the process
// exits. A watch session is not that. It stays open for days, and the last thing
// it does — waiting out its runs, correcting its own log, replacing its image
// with the build deployed over it — happens after the loop that was watching the
// cancellation has already returned.
//
// On 2026-09-05 one of them stopped there. It logged the stop that begins a
// self-redeploy, went silent for an hour, ignored SIGTERM, and took a SIGKILL.
// Ignored is the part worth naming rather than the hour: signal.NotifyContext
// takes SIGTERM away from the operating system's default disposition for the
// life of the process, so every signal an operator sends a wedged process does
// exactly what the first one did — cancel a context that is already cancelled —
// and the operator's first recovery tool is defeated by the same machinery that
// made the graceful stop possible.
//
// So a stop signal has two effects behind the cancellation. The disposition goes
// back the moment the first one arrives, so the next signal ends the process the
// way it would have if nothing here had ever run; and the process gives itself
// Grace to stop on its own, after which it exits where it stands rather than
// waiting to be killed. Neither replaces a command that returns on cancellation.
// They are what happens when one does not.
package shutdown

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Grace is how long a process has to stop itself after it is first asked to.
//
// It is generous on purpose. A watch session asked to stop cancels the runs it
// is carrying and waits out each one's teardown and the record of how it ended,
// and a bound short enough to interrupt that would trade a process nobody can
// kill for a run nobody wrote down. What it has to beat is the hour of silence
// this exists to end, and two minutes beats it by a margin nothing legitimate
// needs.
const Grace = 2 * time.Minute

// ExitNotStopped is what a process exits with when the grace ran out and it
// ended itself. It is a failure rather than a clean return: the command never
// finished, and whatever started this process is being told to start another
// rather than told this one was done.
const ExitNotStopped = 1

// Answering wires this process's stop signals to the context its work runs
// under, and returns that context with the function that puts the wiring down.
//
// Call the returned function once the work has returned. Leaving it uncalled
// costs nothing but a goroutine in a process that is exiting anyway; calling it
// is what keeps a process that finished on its own from being exited by a grace
// that was measured against work already done.
func Answering(parent context.Context, stderr io.Writer) (context.Context, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return answering(parent, signals, Grace, func() { signal.Stop(signals) }, func() {
		fmt.Fprintf(stderr, "this process was asked to stop and had not stopped %s later, so it is exiting where it stands; whatever started it starts the next one\n", Grace)
		os.Exit(ExitNotStopped)
	})
}

// answering is the wiring itself, over a signal channel and the two effects a
// signal has behind the cancellation. It is separate so those two can be
// observed from a test: the real ones put the operating system's disposition
// back and end the process, and neither is something a test can watch happen.
func answering(parent context.Context, signals <-chan os.Signal, grace time.Duration, restore, exit func()) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	returned := make(chan struct{})
	// Both the signal and the work returning put the disposition back, and either
	// can be second. They are the same act rather than two, so it happens once.
	var restored, done sync.Once
	restoring := func() { restored.Do(restore) }
	stop := func() {
		done.Do(func() { close(returned) })
		restoring()
		cancel()
	}
	go func() {
		select {
		case <-signals:
		case <-returned:
			return
		case <-parent.Done():
			return
		}
		// The cancellation is what a stopping process is meant to answer, and the
		// disposition goes back beside it rather than after the grace: an operator
		// who sends a second signal because the first one appeared to do nothing is
		// asking for the process to end, and from here that is exactly what one
		// does.
		cancel()
		restoring()
		grace := time.NewTimer(grace)
		defer grace.Stop()
		select {
		case <-grace.C:
			exit()
		case <-returned:
		}
	}()
	return ctx, stop
}
