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
// harness was alive. So detection of nothing-running lives in machinery that
// keeps running through the window, which means no provider call on this path,
// directly or transitively, ever.
//
// The other half of that boundary is what this must not do. Noticing is a
// surface's; restarting whatever died is not, and it is not moved here by the
// fact that this is the loop still awake. A sink that restarted the watch session
// would be a second thing that invokes and supervises the harness's processes,
// and the operator's own interim script — thirty minutes of quiet, then force a
// restart — retires by its detection half landing here and its restart half
// landing where restarts already live: the session's own bounded exit
// (yoyodyne-ifd.288) under the supervisor that starts it (yoyodyne-ifd.207).

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
// has already read, so the read is spent only where nothing else already
// accounts for the quiet — and then at most once per stall threshold, which is
// the same figure that decides what counts as a stall in the first place.
//
// That second gate is the one that matters, because the first is not enough on
// its own: a drained queue is deliberately not one of the accounted-for states,
// since nothing but the tracker can say the queue is drained. Without the
// interval an ordinary idle product — nothing held, nothing running, nothing
// ready — would spawn `bd` every fifteen seconds forever, which is the cost this
// surface promises it does not have. What it costs at the ten-minute default is
// six tracker reads an hour on a product that is quiet with a drained queue, and
// none at all on one that is starting runs; the sink's other tracker read is
// still the hourly one the heartbeat keeps.
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
func (f *HarnessFeed) stallDeliveries(ctx context.Context, cursors Cursors, held switches, sessions []runstate.WatchTransition, states []runstate.State, ready func(context.Context) (int, error), streams map[string]struct{}) ([]Delivery, error) {
	if f.Stalls == nil {
		return nil, nil
	}
	streams[stallStream] = struct{}{}
	now := f.now()
	cursor := cursors.Streams[stallStream]

	activity := readmodel.Activity{
		Since: readmodel.LastStart(states, sessions),
		// In flight *and still moving*: a run whose process was killed leaves a
		// record saying it is in flight until `yoyo reconcile` settles it, and
		// taking that at face value would silence this for the crash it exists to
		// catch.
		Running:      readmodel.ActiveRuns(states, now, f.runActivityWindow()),
		OperatorHeld: held.operatorHeld,
		IntakeHeld:   held.intakeHeld,
		// The provider refusing to serve any more work, as the session that met it
		// recorded. It is the accounting this reading used to have no way to see: a
		// session waiting out a usage window starts nothing over a ready queue and
		// looks from here exactly like one that has died, and on 2026-09-05 ninety
		// minutes of it was reported as a stall and woke somebody.
		ProviderWindow: readmodel.WaitingOnProvider(sessions),
		Watched:        len(sessions) > 0,
		Threshold:      f.stallThreshold(),
		Now:            now,
	}
	asked := cursor.Said
	if activity.Unexplained() {
		if f.Backlog == nil {
			// Nothing was wired to say whether there is anything to start, so there
			// is no stall to be sure of. A sink assembled this way says everything
			// else, exactly as it does without a heartbeat.
			return nil, nil
		}
		if !cursor.Said.IsZero() && now.Sub(cursor.Said) < f.stallThreshold() {
			// Asked recently enough. Nothing else here can answer whether the queue
			// is drained, so rather than guess this pass makes no reading at all and
			// the next due one decides. A stall already standing is unaffected: it is
			// in the record, it has been said, and nothing re-says it.
			//
			// The interval is the threshold rather than the heartbeat, and that is the
			// operator's bar rather than a tidiness. Gating this read at an hour made a
			// half-hour threshold mean up to ninety minutes from the harness stopping
			// to somebody hearing about it, which is what he was actually complaining
			// about on 2026-09-05 — the threshold he could set was not the number that
			// decided when he was told. Tied to the threshold, the whole latency is one
			// figure he can move: a page arrives within twice it at the very worst, and
			// within the threshold plus one fifteen-second poll in the ordinary case,
			// because a machine that has been starting runs has not spent a read in
			// hours and the first pass past the threshold asks immediately.
			return nil, nil
		}
		count, err := ready(ctx)
		if err != nil {
			// A tracker that cannot be read leaves this unable to tell a stalled
			// machine from a drained queue, and it must not guess in either
			// direction: inventing ready work would wake somebody for nothing, and
			// assuming none would be the silence this exists to end. So it is said
			// where the sink says everything else about itself, nothing is recorded,
			// and it is asked again at the next interval rather than at the next
			// poll — the clock is set either way, so a tracker that is down does not
			// become a tracker asked every fifteen seconds.
			f.say("what is ready to pull could not be read, so nothing was decided about whether this product has stalled: %v", err)
			return []Delivery{{Stream: stallStream, Cursor: cursorAsked(cursor, now)}}, nil
		}
		activity.Ready = count
		asked = now
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
	window, _ := activity.StandingWindow()
	return f.stallSaid(ctx, cursors, reconciled.Standing, window, now, asked), nil
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

// cursorAsked records the moment the tracker was last asked what is ready, which
// is what keeps that read to one a heartbeat rather than one a poll. The stall
// stream has no other use for this field: what it has already said is in
// Delivered, so the clock is free to mean this here.
func cursorAsked(cursor Cursor, at time.Time) Cursor {
	asked := cursor
	asked.Said = at
	return asked
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
func (f *HarnessFeed) stallSaid(ctx context.Context, cursors Cursors, standing *runstate.StallEvent, window readmodel.ProviderWindow, now, asked time.Time) []Delivery {
	cursor := cursors.Streams[stallStream]
	advanced := cursorAsked(cursor, asked)
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
		// stall lasts, which is the case the whole record exists for. Only the clock
		// on the tracker read can still have moved, and it is persisted when it has
		// so the next interval is measured from the ask that actually happened.
		if advanced.Said.Equal(cursor.Said) {
			return nil
		}
		return []Delivery{{Stream: stallStream, Cursor: advanced}}
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
	if len(advanced.Delivered) == len(cursor.Delivered) && advanced.Said.Equal(cursor.Said) {
		return nil
	}
	return []Delivery{{Stream: stallStream, Cursor: advanced}}
}

// runActivityWindow is how long a run's record may go unmoved before it stops
// accounting for a quiet line.
func (f *HarnessFeed) runActivityWindow() time.Duration {
	if f.RunActivityWindow > 0 {
		return f.RunActivityWindow
	}
	return readmodel.DefaultRunActivityWindow
}

// stallThreshold is how long nothing may start, over ready work and with nothing
// accounting for it, before this says so.
func (f *HarnessFeed) stallThreshold() time.Duration {
	if f.StallThreshold > 0 {
		return f.StallThreshold
	}
	return readmodel.DefaultStallThreshold
}
