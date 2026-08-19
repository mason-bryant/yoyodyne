package slack

// What the sink reads, and where the reading stops.
//
// Nothing here decides what an event means or how it is said. Which crossings of
// a durable record are worth reporting, which persona's account each one is, and
// the words it is said in all belong to the notifier, and this reads that
// package rather than repeating it: a second producer of reportable events adds
// a selection function there and nothing at all here.
//
// What is left is the part that is genuinely the sink's — where the reading got
// to. The notifier's selection is a pure comparison of two readings of a record,
// so somebody has to remember the earlier reading; the notifier's other
// producers are logs, so somebody has to remember the position. That is a
// cursor, it is durable, and it is written after each message rather than at the
// end of a pass, because it is the only thing standing between a crash and
// saying everything again.

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The streams a product has. Runs take one each, named for the run, because they
// are separate subjects rather than one log; the rest are one apiece.
const (
	reportStream   = "reports"
	proposalStream = "proposals"
	productStream  = "product"
)

func runStream(runID string) string { return "run:" + runID }

// The prefixes the operator's two switches are marked under. A hold is marked
// with the moment it was placed, so a second hold a week later is a second thing
// to say rather than the same one.
const (
	intakeMark = "intake:"
	holdMark   = "hold:"
)

// Delivery is one step of one stream: what to say, and the cursor that records
// having said it. The cursor is the whole of the delivery guarantee — the sink
// posts and then writes it, so a process killed between the two repeats one
// message rather than losing it.
//
// A delivery with nothing to say is a cursor advance on its own. It is how a run
// that is over stops being carried and how reports older than this sink are read
// past, neither of which anybody should be told about.
type Delivery struct {
	Stream       string
	Cursor       Cursor
	Notification notify.Notification
}

// Silent reports a delivery that advances a cursor and posts nothing.
func (d Delivery) Silent() bool { return d.Notification.Event.Kind == "" }

// Batch is one pass over the durable records: what is ready to post, and which
// streams still exist so cursors for the rest can be dropped.
type Batch struct {
	Deliveries []Delivery
	Streams    map[string]struct{}
}

// Feed is where the sink's messages come from. It is polled rather than
// subscribed to because the records it reads are files written by other
// processes, and because a sink that has been away has to catch up from its
// cursors either way.
type Feed interface {
	Poll(ctx context.Context, cursors Cursors) (Batch, error)
}

// HarnessFeed reads the product's own durable records: what became of its runs,
// what its agents reported and proposed while their work carried on, and the
// operator's two switches over the whole line.
type HarnessFeed struct {
	Runs      *runstate.Store
	Reports   *runstate.ReportStore
	Proposals *runstate.AmendmentStore
	Intake    *runstate.IntakeHoldStore
	Holds     *runstate.OperatorHoldStore
	// Since is when this sink started. What was already over before then is
	// history rather than news, and a channel opened today does not want a month
	// of it: a product with two hundred recorded runs would get two hundred
	// threads before it said anything about today's work. Only what nothing has
	// been said about yet is filtered this way, so a run in flight is still
	// caught up on in full, and a message an outage delayed is still posted when
	// the workspace returns. The zero value reports everything, which is what a
	// caller that wants the whole record asks for.
	Since time.Time
	// Now is read for the moment a hold was seen to have lifted, which is the one
	// thing here no record holds: what lifts a hold is its absence. It is
	// injected so a test can say when that was.
	Now func() time.Time
	// Log is where a record that cannot be addressed at all is said out loud
	// before it is read past. It is the sink's own log, and it is never given a
	// token to print.
	Log func(format string, args ...any)
}

// Poll reads every stream and reports what the cursors say has not been posted.
func (f *HarnessFeed) Poll(ctx context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{
		reportStream:   {},
		proposalStream: {},
		productStream:  {},
	}}

	states, err := f.Runs.Recorded()
	if err != nil {
		return Batch{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		stream := runStream(state.RunID)
		batch.Streams[stream] = struct{}{}
		deliveries, err := f.runDeliveries(state, cursors.Streams[stream])
		if err != nil {
			return Batch{}, err
		}
		batch.Deliveries = append(batch.Deliveries, deliveries...)
	}

	filed, err := f.Reports.List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the collected reports: %w", err)
	}
	reported, err := f.logDeliveries(reportStream, cursors.Streams[reportStream], len(filed),
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromReport(filed[index])
			return filed[index].RecordedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, reported...)

	records, err := f.Proposals.List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the proposed changes: %w", err)
	}
	proposals := amendment.Proposals(records)
	raised, err := f.logDeliveries(proposalStream, cursors.Streams[proposalStream], len(proposals),
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromProposal(proposals[index])
			return proposals[index].RaisedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, raised...)

	switches, err := f.holdDeliveries(cursors.Streams[productStream])
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, switches...)
	return batch, nil
}

// runDeliveries says what one run's record crossed since the reading already
// reported, and advances that reading only once the whole crossing has been
// posted. A crash halfway therefore repeats what it had already said rather than
// losing what it had not, which is the trade the design takes deliberately: the
// durable record is authoritative and this is a view of it.
func (f *HarnessFeed) runDeliveries(state runstate.State, cursor Cursor) ([]Delivery, error) {
	if cursor.Closed {
		return nil, nil
	}
	stream := runStream(state.RunID)
	var before runstate.State
	if cursor.Reported != nil {
		before = *cursor.Reported
	} else if len(cursor.Delivered) == 0 && f.predates(completion(state)) {
		// A run that was already over before this sink started is history, and
		// nothing was ever said about it. Its cursor closes without a word.
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}

	crossed, err := notify.FromRun(before, state)
	if err != nil {
		// A run nothing can be addressed to is one nothing about it will ever be
		// postable to, whatever it goes on to do. Failing the pass would hold up
		// every other stream forever over one record, so it is said once and its
		// cursor closes.
		f.say("run %s could not be addressed and nothing will be reported about it: %v", state.RunID, err)
		return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
	}
	pending := make([]Delivery, 0, len(crossed))
	advanced := cursor
	for _, notification := range crossed {
		mark := markOf(notification.Event)
		if advanced.Has(mark) {
			continue
		}
		advanced = advanced.With(mark)
		pending = append(pending, Delivery{Stream: stream, Cursor: advanced, Notification: notification})
	}

	if len(pending) == 0 {
		// A run that is over and owes nothing has nothing left to cross, so the
		// reading it was being compared against is dropped rather than carried
		// for as long as the product exists.
		if settled(state) {
			return []Delivery{{Stream: stream, Cursor: Cursor{Closed: true}}}, nil
		}
		return nil, nil
	}
	// The last message of a crossing is what makes the crossing said, so it is
	// the one that moves the reading on. The marks it made redundant go with it:
	// they only ever existed to stop a crash repeating what was said against the
	// reading they were recorded under.
	settledState := state
	pending[len(pending)-1].Cursor = Cursor{Reported: &settledState}
	return pending, nil
}

// logDeliveries reads an append-only log from where the cursor left it. A log
// this sink has never read is read past to its end in one silent advance,
// because a channel opened today does not want a year of history arriving at
// once; a log it has read before is caught up on in full whatever the dates say,
// because an outage has to delay messages rather than lose them, and a record
// filed while the sink was down is exactly the one that would be lost.
func (f *HarnessFeed) logDeliveries(stream string, cursor Cursor, count int, at func(int) (time.Time, notify.Notification, error)) ([]Delivery, error) {
	var deliveries []Delivery
	skipped := cursor.Position
	history := cursor.Position == 0
	for index := int(cursor.Position); index < count; index++ {
		recordedAt, notification, err := at(index)
		position := uint64(index + 1)
		if err != nil {
			// One record nobody can address must not hold up every record behind
			// it for as long as the process runs, so it is said once and read
			// past rather than retried forever.
			f.say("a record on the %s log could not be addressed and was skipped: %v", stream, err)
			skipped = position
			continue
		}
		if history && f.predates(recordedAt) {
			skipped = position
			continue
		}
		deliveries = append(deliveries, Delivery{
			Stream:       stream,
			Cursor:       Cursor{Position: position},
			Notification: notification,
		})
	}
	// Anything read past after the last message has to move the cursor on its
	// own, or it is read past again on every pass for as long as the sink runs.
	reached := cursor.Position
	if len(deliveries) > 0 {
		reached = deliveries[len(deliveries)-1].Cursor.Position
	}
	if skipped > reached {
		deliveries = append(deliveries, Delivery{Stream: stream, Cursor: Cursor{Position: skipped}})
	}
	return deliveries, nil
}

// holdDeliveries says the operator's two switches. Each is said when it is
// placed and again when it is lifted, and the lift is the awkward half: nothing
// records a release, so what says a hold has lifted is the hold's absence
// against a mark saying it was once there. The pair is forgotten once both have
// been said, so the product's cursor does not grow a line for every afternoon
// somebody was away.
func (f *HarnessFeed) holdDeliveries(cursor Cursor) ([]Delivery, error) {
	var deliveries []Delivery
	advanced := cursor

	intake, held, err := f.Intake.Held()
	if err != nil {
		return nil, fmt.Errorf("read the intake hold: %w", err)
	}
	if held {
		if mark := intakeMark + stamp(intake.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.FromIntakeHold(intake),
			})
		}
	} else if mark, said := advanced.Marked(intakeMark); said {
		advanced = advanced.Without(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       productStream,
			Cursor:       advanced,
			Notification: notify.IntakeReleased(f.now()),
		})
	}

	operator, held, err := f.Holds.Held()
	if err != nil {
		return nil, fmt.Errorf("read the operator hold: %w", err)
	}
	if held {
		if mark := holdMark + stamp(operator.HeldAt); !advanced.Has(mark) {
			advanced = advanced.With(mark)
			deliveries = append(deliveries, Delivery{
				Stream:       productStream,
				Cursor:       advanced,
				Notification: notify.FromOperatorHold(operator),
			})
		}
	} else if mark, said := advanced.Marked(holdMark); said {
		advanced = advanced.Without(mark)
		deliveries = append(deliveries, Delivery{
			Stream:       productStream,
			Cursor:       advanced,
			Notification: notify.HoldLifted(f.now()),
		})
	}
	return deliveries, nil
}

func (f *HarnessFeed) say(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

func (f *HarnessFeed) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now().UTC()
}

// predates reports what was already recorded before this sink started.
func (f *HarnessFeed) predates(at time.Time) bool {
	return !f.Since.IsZero() && !at.IsZero() && at.Before(f.Since)
}

// markOf names one crossing so that having posted it survives a crash. The kind
// alone would not do it: a check that fails, is repaired, and fails differently
// has crossed the same kind twice with two different things to say, and a mark
// that could not tell those apart would swallow the second. The moment is left
// out because every crossing of one reading carries the same one, and a mark
// that moved with it would match nothing.
func markOf(event notify.Event) string {
	digest := fnv.New64a()
	fmt.Fprintf(digest, "%v\x00%v\x00%s", event.Severity, event.Detail, event.Text)
	return string(event.Kind) + ":" + strconv.FormatUint(digest.Sum64(), 36)
}

// settled reports a run that is over and owes nothing further: terminal, with no
// publication still to be watched and no integration left half done.
func settled(state runstate.State) bool {
	return state.Status.Terminal() && !state.Outstanding()
}

// completion is when a run stopped, for deciding whether it is history. A run
// that has not stopped has no completion and is never history.
func completion(state runstate.State) time.Time {
	if state.CompletedAt == nil {
		return time.Time{}
	}
	return *state.CompletedAt
}

func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
