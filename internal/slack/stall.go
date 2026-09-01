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
// So this reads the absence instead. Nothing has started since a moment the runs
// themselves date; the tracker says work is ready; no hold, no full machine and
// no run in flight accounts for it. That reading needs no cooperation from the
// process it is about, which is the whole point — a dead scheduler cannot file a
// report about being dead, and a wedged one files that it is watching.
//
// The sink is where it runs because the sink's loop is the one that outlives the
// scheduler. It does not decide anything: the reading is the read model's, one
// stall at a time is the durable record's, and what is left here is the two
// things that were always this surface's — reading what a pass already holds, and
// deciding that this particular message is worth somebody's phone.

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stallDeliveries notices that nothing is happening, records it, and says so
// once.
//
// The order is the cost model the rest of this pass keeps. Everything except how
// much work is ready is derived from the runs, sessions and switches this pass
// has already read, so a healthy machine polling every fifteen seconds asks the
// tracker nothing at all; the read is spent only where nothing else already
// accounts for the quiet.
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
func (f *HarnessFeed) stallDeliveries(ctx context.Context, cursors Cursors, held switches, sessions []runstate.WatchTransition, states []runstate.State, inFlight int, streams map[string]struct{}) ([]Delivery, error) {
	if f.Stalls == nil {
		return nil, nil
	}
	streams[stallStream] = struct{}{}
	now := f.now()

	activity := readmodel.Activity{
		Since:        readmodel.LastStart(states, sessions),
		Running:      inFlight,
		OperatorHeld: held.operatorHeld,
		IntakeHeld:   held.intakeHeld,
		Watched:      len(sessions) > 0,
		Threshold:    f.stallThreshold(),
		Now:          now,
	}
	if activity.Unexplained() {
		if f.Backlog == nil {
			// Nothing was wired to say whether there is anything to start, so there
			// is no stall to be sure of. A sink assembled this way says everything
			// else, exactly as it does without a heartbeat.
			return nil, nil
		}
		ready, err := f.Backlog.Ready(ctx)
		if err != nil {
			// A tracker that cannot be read leaves this unable to tell a stalled
			// machine from a drained queue, and it must not guess in either
			// direction: inventing ready work would wake somebody for nothing, and
			// assuming none would be the silence this exists to end. So it is said
			// where the sink says everything else about itself, nothing is recorded,
			// and it is asked again at the next pass.
			f.say("what is ready to pull could not be read, so nothing was decided about whether this product has stalled: %v", err)
			return nil, nil
		}
		activity.Ready = ready
	}
	silence := readmodel.ReadSilence(activity)

	reconciled, err := f.Stalls.Reconcile(runstate.StallObservation{
		Stalled:  silence.Stalled,
		Since:    silence.Since,
		Ready:    silence.Ready,
		Chooser:  readmodel.LastWord(sessions),
		Explains: silence.Explains,
		At:       now,
	})
	if err != nil {
		return nil, err
	}
	return f.stallSaid(ctx, cursors, reconciled.Standing, now), nil
}

// stallSaid is the one message, and the cursor that records having said it.
//
// The mark names the stall rather than the state, so a second stall is a second
// thing to say and the same one is not. It is dropped the moment that stall is
// no longer standing, which keeps the cursor from growing a line for every night
// something went quiet — and is safe because a stall that has closed is history
// and is never said again.
func (f *HarnessFeed) stallSaid(ctx context.Context, cursors Cursors, standing *runstate.StallEvent, now time.Time) []Delivery {
	cursor := cursors.Streams[stallStream]
	advanced := cursor
	if mark, said := advanced.Marked(stallMark); said {
		if standing == nil || mark != stallMark+standing.EventID {
			advanced = advanced.Without(mark)
		}
	}
	switch {
	case standing == nil:
		// Nothing is standing. What cleared it said so itself — the run that
		// started, the hold that went on — so there is nothing to say here beyond
		// forgetting the mark.
	case advanced.Has(stallMark + standing.EventID):
		// Already said, once. This is every pass after the first for as long as the
		// stall lasts, which is the case the whole record exists for.
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
				Since:    standing.Since,
				Ready:    standing.Ready,
				Chooser:  standing.Chooser,
				Standing: f.standing(ctx),
			}, now),
		}}
	}
	if len(advanced.Delivered) == len(cursor.Delivered) {
		return nil
	}
	return []Delivery{{Stream: stallStream, Cursor: advanced}}
}

// stallThreshold is how long nothing may start, over ready work and with nothing
// accounting for it, before this says so.
func (f *HarnessFeed) stallThreshold() time.Duration {
	if f.StallThreshold > 0 {
		return f.StallThreshold
	}
	return readmodel.DefaultStallThreshold
}
