package slack

// The claims the harness gave back because nothing was working on them.
//
// It is the other half of the stall watchdog beside it, and it exists because
// that one is blind to this case by construction. A stall is derived from work
// the tracker calls ready going unstarted; an item the harness claimed is not
// ready, so a machine whose only startable work is sitting under dead claims
// reads from every surface here as a drained queue. Four nights of the week of
// 2026-09-01 went that way, and on the last of them both developer slots died
// inside an hour and nothing said a word until somebody looked in the morning.
//
// Nothing here notices anything, which is the difference from the stall stream.
// The audit runs in the watch loop, because giving a claim back is directing work
// and a reporting surface does not direct work; what is left here is what this
// surface has always done — reading a durable log, and deciding that this
// particular message is worth somebody's phone.

import (
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// claimDeliveries says each claim the harness gave back, once each.
//
// It advances by position like the reports pile, which is the whole of "once"
// across a restart: the release is a line in a log that is never rewritten, and
// the cursor records how far this sink has read. Nothing re-says a release, and
// nothing here decides whether a claim should have been released — that decision
// is already made and already durable by the time this reads it.
func (f *HarnessFeed) claimDeliveries(cursor Cursor, since time.Time, streams map[string]struct{}) ([]Delivery, error) {
	if f.Claims == nil {
		return nil, nil
	}
	streams[claimStream] = struct{}{}
	released, err := f.Claims.List()
	if err != nil {
		return nil, err
	}
	deliveries, err := f.logDeliveries(claimStream, cursor, len(released), since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromReleasedClaim(released[index])
			return released[index].ReleasedAt, notification, err
		})
	if err != nil {
		return nil, err
	}
	for index := range deliveries {
		// It goes to the operators as well as to the channel. A claim that outlived
		// its run is the harness having been degraded in the quietest way there is —
		// an item nothing would ever pull again, on a line that reads as busy — and a
		// channel is somewhere somebody chooses to look, which is exactly what an
		// overnight does not include. A cursor advance with nothing to say is left
		// alone: reading past a release older than this sink is not news.
		if !deliveries[index].Silent() {
			deliveries[index].Direct = true
		}
	}
	return deliveries, nil
}

// ReleasedClaims is where the claims the harness gave back are read from. It is
// an interface rather than the store itself so a test can hand the feed a log
// without a filesystem, exactly as the other readings here are given one.
type ReleasedClaims interface {
	List() ([]runstate.ReleasedClaim, error)
}
