package slack

// The other thing the sink says that no record said first, and the harder one:
// that nothing at all is happening.
//
// The heartbeat beside this says a state the record already holds — held,
// braked, idle — again while it stands. That works while something is alive to
// write the record down. On 2026-09-01 the watch session died on a transient
// tracker read and wrote nothing at all, and for seven and a half hours every
// surface here was correct and silent: no hold, no stop, no idle poll, no run,
// nothing. The operator found it by noticing.
//
// So the absence is read instead: nothing has started since a moment the runs
// themselves date; the tracker says work is ready; no hold, no full machine and
// no run in flight accounts for it. That reading needs no cooperation from the
// process it is about, which is the whole point — a dead scheduler cannot file a
// report about being dead, and a wedged one files that it is watching. It is
// taken by internal/watchdog and written down against the product, and what this
// file does is say the one message the record is worth.
//
// The sink says it and does not produce it, and that division is the whole of
// what yoyodyne-ifd.295 moved. The sink's loop was chosen originally because it
// outlives the scheduler, which is true and is not enough: Slack reporting is
// opt-in throughout — an observation and never a gate — so a product that never
// started a sink recorded no stalls at all and its `yoyo status` history was
// permanently empty. The instrument that exists so silence is impossible cannot
// hang off an optional process, which is the same lesson the provider's usage
// window taught one layer down. So the reading and the record are
// internal/watchdog's, run from machinery that is always running, and what is
// left here is what was always this surface's: reading what the record already
// holds, and deciding that this particular message is worth somebody's phone.
//
// # Nothing here may be a model-based watcher
//
// This loop is plain Go reading durable files, and it has to stay that way. Every
// watcher the harness has that asks a model something — the development
// manager's sweep, any role turn — pauses with the provider's usage window, so a
// watchdog built on one goes to sleep at exactly the moment the thing it watches
// goes quiet, and wakes up when there was nothing left to notice. That is the
// lesson 2026-09-05 taught: the window that produced ninety minutes of silence
// would also have silenced any watcher that had to ask a provider whether the
// harness was alive. So the whole path stays free of provider calls, directly or
// transitively, on this side of it as much as on the checker's.
//
// The other half of that boundary is what this must not do. Noticing is a
// surface's; restarting whatever died is not, and it is not moved here by the
// fact that this is the loop still awake. A sink that restarted the watch session
// would be a second thing that invokes and supervises the harness's processes,
// and the operator's own interim script — thirty minutes of quiet, then force a
// restart — retires by its detection half landing in the checker and its restart
// half landing where restarts already live: the session's own bounded exit
// (yoyodyne-ifd.288) under the supervisor that starts it (yoyodyne-ifd.207).

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stallDeliveries says the stall the record holds, once, and the provider's
// usage window where one accounts for the quiet instead.
//
// It costs nothing but a read of the product's own stall log: what is ready to
// pull, when anything last started and what accounts for it are the checker's
// questions, asked wherever the checker runs. Two readings of one machine is a
// disagreement only the operator could adjudicate, and a sink deriving its own
// would be exactly that — so this reads the answer rather than repeating the
// work.
//
// It is said once per stall rather than again while it stands, which is the
// opposite of the heartbeat and deliberately so. An hourly repetition is right
// for a state somebody may have to sit with — a hold they placed, a queue they
// have not filled — and wrong for this one: a machine that has stopped doing
// anything is either looked at or it is not, and a second message adds nothing
// to the first except a reason to mute the channel. What makes once mean once
// across a restart is the durable record rather than this cursor: the record
// says a stall is already open, so a fresh sink on a fresh cursor re-says
// nothing.
func (f *HarnessFeed) stallDeliveries(ctx context.Context, cursors Cursors, sessions []runstate.WatchTransition, streams map[string]struct{}) ([]Delivery, error) {
	if f.Stalls == nil {
		return nil, nil
	}
	streams[stallStream] = struct{}{}
	now := f.now()

	recorded, open, err := f.Stalls.Standing()
	if err != nil {
		return nil, err
	}
	var standing *runstate.StallEvent
	if open {
		standing = &recorded
	}
	// The window is derived here rather than read from the record because the
	// record holds no window: a window is precisely the case where there is no
	// stall to record. It is the session's own account of it, and it accounts for
	// nothing once it has lifted — which is what keeps this from being a way to
	// silence the surface rather than to inform it.
	window := readmodel.WaitingOnProvider(sessions)
	if !window.Standing(now) {
		window = readmodel.ProviderWindow{}
	}
	// What the last poll that started nothing said was holding the queue, which is
	// the one thing this message was missing. It is read from the same log the
	// window above is and by the same read model the session's own idle line
	// renders: one derivation, two renderers. A surface working the cause out for
	// itself is what paged the operator on 2026-09-06 with "nothing accounting for
	// it" while the accounting sat one surface over.
	//
	// It is asked against the moment the stall's own silence began, so an account
	// from before that is refused rather than stated as the present cause: a
	// session that crashed leaves its last poll behind, and the queue it describes
	// has not been read since. That case wants the reader sent to the chooser, and
	// the message says so by having no cause to name.
	var cause readmodel.Cause
	if standing != nil {
		if read, accounted := readmodel.WhyThePollStartedNothing(sessions, standing.Since, now); accounted {
			cause = read
		}
	}
	return f.stallSaid(ctx, cursors, standing, window, cause, now), nil
}

// windowKey names one provider usage window, so a window that is said is said
// once and a later one is said again.
//
// It is the deadline where the provider named one, and the moment the session
// recorded entering the window where it did not. Both are moments the record
// holds rather than anything derived here, and a window with neither is not one
// this says anything about — which is the empty key, matching nothing.
func windowKey(window readmodel.ProviderWindow) string {
	switch {
	case !window.Waiting:
		return ""
	case !window.ResetsAt.IsZero():
		return window.ResetsAt.UTC().Format(time.RFC3339)
	case !window.Since.IsZero():
		return "opened " + window.Since.UTC().Format(time.RFC3339)
	default:
		return ""
	}
}

// stallSaid is the one message, and the cursor that records having said it.
//
// The mark names the stall rather than the state, so a second stall is a second
// thing to say and the same one is not. It is dropped the moment that stall is
// no longer standing, which keeps the cursor from growing a line for every night
// something went quiet — and is safe because a stall that has closed is history
// and is never said again.
//
// The provider's usage window is the other thing this can say, and it is here
// rather than beside this because the two are one decision: they are read from
// one silence, and the window is precisely the case where there is no stall to
// report. Saying the window is what turns the reading from a warning that no
// longer fires into an answer — an operator who sees nothing cannot tell a
// window from a watchdog somebody switched off.
func (f *HarnessFeed) stallSaid(ctx context.Context, cursors Cursors, standing *runstate.StallEvent, window readmodel.ProviderWindow, cause readmodel.Cause, now time.Time) []Delivery {
	cursor := cursors.Streams[stallStream]
	advanced := cursor
	if mark, said := advanced.Marked(stallMark); said {
		if standing == nil || mark != stallMark+standing.EventID {
			advanced = advanced.Without(mark)
		}
	}
	// The window is marked by the deadline the provider named, so a second window
	// is a second thing to say and the same one is not. It is forgotten the moment
	// that window is no longer the one standing, which is what keeps the cursor
	// from growing a line for every window a product ever waited out.
	if mark, said := advanced.Marked(windowMark); said && mark != windowMark+windowKey(window) {
		advanced = advanced.Without(mark)
	}
	switch {
	case standing == nil && window.Waiting:
		// Nothing is stalled because the provider is not serving, which is the one
		// quiet stretch that has an answer nobody has to act on. It is said once per
		// window and in the channel rather than to somebody directly: the fact is
		// worth having and the interruption is not, which is the whole difference
		// between this and the stall below it.
		if advanced.Has(windowMark + windowKey(window)) {
			break
		}
		return []Delivery{{
			Stream: stallStream,
			Cursor: advanced.With(windowMark + windowKey(window)),
			Notification: notify.FromProviderWindow(notify.ProviderWindow{
				Says:  window.Says(),
				Since: window.Since,
				// The four lines without the banner, which this message's own first
				// sentence already is.
				Standing: f.standingLines(ctx),
			}, now),
		}}
	case standing == nil:
		// Nothing is standing. What cleared it said so itself — the run that
		// started, the hold that went on — so there is nothing to say here beyond
		// forgetting the mark.
	case advanced.Has(stallMark + standing.EventID):
		// Already said, once. This is every pass after the first for as long as the
		// stall lasts, which is the case the whole record exists for, and nothing
		// about it has moved: the mark is the same and there is nothing to persist.
		return nil
	case predates(cursors.Since, standing.OpenedAt):
		// Open before this product's reporting began. It is read past on age as
		// every stream here is, and marked so it is read past once rather than on
		// every pass.
		advanced = advanced.With(stallMark + standing.EventID)
	default:
		return []Delivery{{
			Stream: stallStream,
			Cursor: advanced.With(stallMark + standing.EventID),
			// The class of message this surface takes to somebody directly is a
			// harness that is degraded rather than work that is going badly, and a
			// harness doing nothing at all is the sharpest case there is of it. A
			// channel is somewhere somebody chooses to look, and this is exactly what
			// they would not think to look for.
			Direct: true,
			Notification: notify.FromStall(notify.Stall{
				Since:   standing.Since,
				Ready:   standing.Ready,
				Chooser: standing.Chooser,
				// The cause the last poll recorded and whose move follows it, both
				// worded by the read model that derived them. A stall with no poll to
				// read leaves both empty, and the message says what it always said:
				// that nothing the record holds accounts for the silence.
				Cause:    cause.Says(),
				Mover:    cause.Whose(),
				Standing: f.standing(ctx),
			}, now),
		}}
	}
	if len(advanced.Delivered) == len(cursor.Delivered) {
		return nil
	}
	return []Delivery{{Stream: stallStream, Cursor: advanced}}
}
