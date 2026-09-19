package supervise

// Stopping one process that holds a lease, and waiting for the lease to go.
//
// Every child answers whether it is running from its own lease, so the wait
// after a stop signal is on that lease rather than on the process table: a
// child that has let go of its lease has stopped for every purpose the
// supervisor has, and a process still holding it is still running whatever
// the process table says about its state.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/shutdown"
)

// StopGrace bounds how long a stop waits for one child to let go of its lease.
// It is the grace every harness process gives itself to stop and a margin over
// it, because the scheduler asked to stop cancels the runs it hosts and waits
// out each one's teardown, and a stop that gave up before the process did
// would report a child running that was on its way down.
const StopGrace = shutdown.Grace + 30*time.Second

// stopInterval is how often the lease is asked again while waiting.
const stopInterval = 250 * time.Millisecond

// StopHolder asks the process holding a lease to stop and waits for the lease
// to go. It reports a wait that ran out in the detail rather than as a
// failure, because the signal was sent and the process is stopping on its own
// schedule; what the caller cannot say is that it has stopped.
func StopHolder(ctx context.Context, pid int, held func() (bool, error)) (Stopped, error) {
	if pid <= 0 {
		return Stopped{}, fmt.Errorf("the record names no process to stop")
	}
	if err := terminate(pid); err != nil {
		return Stopped{}, fmt.Errorf("ask pid %d to stop: %w", pid, err)
	}
	stopped := Stopped{WasRunning: true, PID: pid}
	deadline := time.Now().Add(StopGrace)
	for {
		holding, err := held()
		if err != nil {
			return stopped, fmt.Errorf("ask whether pid %d has let go of its lease: %w", pid, err)
		}
		if !holding {
			return stopped, nil
		}
		if time.Now().After(deadline) {
			stopped.Detail = fmt.Sprintf("it was still holding its lease %s after being asked to stop, so it is stopping on its own schedule", StopGrace)
			return stopped, nil
		}
		timer := time.NewTimer(stopInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			stopped.Detail = "the wait for it to stop was interrupted; it was asked, and is stopping on its own schedule"
			return stopped, nil
		}
	}
}
