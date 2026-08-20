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

// The streams a product has. Runs and conversations take one each, named for
// the run or the conversation, because they are separate subjects rather than
// one log; the rest are one apiece.
const (
	reportStream     = "reports"
	proposalStream   = "proposals"
	productStream    = "product"
	watchStream      = "watch"
	usageLimitStream = "usage-limits"
	heartbeatStream  = "heartbeat"
)

func runStream(runID string) string { return "run:" + runID }

func conversationStream(conversationID string) string { return "chat:" + conversationID }

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
func (d Delivery) Silent() bool { return d.Notification.Silent() }

// Batch is one pass over the durable records: what is ready to post, and which
// streams still exist so cursors for the rest can be dropped.
type Batch struct {
	Deliveries []Delivery
	Streams    map[string]struct{}
	// Partial reports a pass that read past a record without being able to say
	// which stream that record was. Streams is then an incomplete list of what the
	// product has, so nothing may be forgotten on the strength of it: a cursor
	// dropped because a deploy outpaced the sink would have the run reported from
	// its beginning again the moment a newer sink could read it, which is the whole
	// of what the cursor exists to prevent.
	//
	// It is deliberately narrower than "something was skipped". A run record names
	// its own stream even when it will not decode, and a line of an append-only log
	// belongs to a stream this pass named before it read anything; only a
	// conversation record, whose file is named for the agent and whose stream is
	// the conversation id inside it, takes its stream with it. Otherwise one
	// undecodable record would freeze pruning for the life of the process.
	Partial bool
}

// Feed is where the sink's messages come from. It is polled rather than
// subscribed to because the records it reads are files written by other
// processes, and because a sink that has been away has to catch up from its
// cursors either way.
type Feed interface {
	Poll(ctx context.Context, cursors Cursors) (Batch, error)
}

// HarnessFeed reads the product's own durable records: what became of its runs,
// what its conversations did to the backlog, what its agents reported and
// proposed while their work carried on, and the operator's two switches over the
// whole line.
type HarnessFeed struct {
	Runs *runstate.Store
	// Conversations is where the backlog moving is read from. It is optional
	// only in the sense that a feed assembled without it reports everything else:
	// a product whose queue changes invisibly is the thing this exists to
	// prevent, and every sink the harness builds is given one.
	Conversations *runstate.ConversationStore
	Reports       *runstate.ReportStore
	Proposals     *runstate.AmendmentStore
	Intake        *runstate.IntakeHoldStore
	Holds         *runstate.OperatorHoldStore
	// Watch is where a watch session says what it is doing. It is optional in the
	// same sense the conversations are: a feed assembled without one reports
	// everything else, and what is lost is the one thing nothing else in the
	// record says — that the session choosing work is alive and idle rather than
	// dead.
	Watch *runstate.WatchStore
	// UsageLimits is where a provider refusing the harness outside a run is read
	// from: a conversation turn, an independent review. It is optional in the
	// same sense the two above are, and what a feed assembled without one loses
	// is the only account there is of those refusals — a run says its own by
	// parking, and nothing else says anything at all.
	UsageLimits *runstate.UsageLimitStore
	// Backlog is how much admitted work the tracker calls ready, and it is read
	// for one purpose: telling a line that is waiting on somebody from one that is
	// honestly quiet. It is optional, and a feed assembled without one says
	// everything else and never says the line is waiting — which is silence over a
	// held queue, so every sink the harness builds is given one.
	Backlog Backlog
	// Heartbeat is how often a line that is choosing nothing over ready work says
	// so again. Zero takes DefaultHeartbeat. It is a cadence rather than a switch:
	// there is deliberately no way to turn it off, because what it would buy is
	// silence that means waiting-on-you, which is the state it exists to end.
	Heartbeat time.Duration
	// Now is read for the moment a hold was seen to have lifted, and for the age a
	// heartbeat says a state has stood. Both are things no record holds — what
	// lifts a hold is its absence, and what makes a state worth saying again is how
	// long ago it began — so it is injected and a test can say when they were.
	Now func() time.Time
	// Log is where a record that cannot be addressed at all is said out loud
	// before it is read past. It is the sink's own log, and it is never given a
	// token to print.
	Log func(format string, args ...any)
	// skipped remembers which records this feed has already said it could not
	// decode. A record stays undecodable until somebody acts, so a line per pass
	// would be the same sentence every fifteen seconds for as long as the process
	// runs, which is how a log stops being read — the same reason a standing
	// refusal is said once. A restart says them again, which is right: a restart
	// that did not fix it is news.
	skipped map[string]struct{}
}

// Poll reads every stream and reports what the cursors say has not been posted.
//
// Everything the product recorded before the cursors' watermark is history and
// is read past: a channel turned on today does not want a month of finished work
// arriving at once. The watermark is one durable moment for the whole product
// rather than this process's start time, which is what makes an outage a delay:
// a record filed while the sink was down is after the watermark whatever the
// cursor for its stream happens to say, so it is posted when the sink returns
// rather than mistaken for history somebody has already read.
//
// The records are read tolerantly, because this sink is older than them whenever
// a deploy has landed since it started — which under watch mode is most nights.
// A key it has never heard of is the additive change these schemas evolve by and
// is read straight past; a record it cannot decode at all is skipped, said once
// with the remedy, and does not stop the pass. The alternative is what an
// overnight found: one record in a new format, every pass afterwards failing on
// it, and cursors that never move again.
func (f *HarnessFeed) Poll(ctx context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{
		reportStream:   {},
		proposalStream: {},
		productStream:  {},
	}}
	// A record this pass could not read is said and then left out, and what that
	// costs depends on what else was being derived from it.
	//
	// Two things can be. Cursors are forgotten from the list of streams a pass
	// enumerated, so a record that cannot name its own stream must stop the
	// forgetting: a run can name one, because run records are files named for the
	// run, and a line of a log belongs to a stream this pass named before reading
	// anything, but a conversation cannot — its file is named for the agent and the
	// stream is the id inside the record. And the heartbeat is derived rather than
	// read: what has stopped the line is worked out from the runs in flight, the
	// watch sessions, and the two switches together, so a reading that is missing
	// any of them cannot support the derivation and must not be guessed from.
	derived := true
	readPastRun := func(runID string, err error) {
		batch.Streams[runStream(runID)] = struct{}{}
		// A run this sink cannot decode is still a run. Left out of the count it
		// looks like no run at all, and the heartbeat would then say the line is
		// stalled while work is executing — which is worse than silence, because it
		// is false rather than absent.
		derived = false
		f.skip(runID, err)
	}
	readPastConversation := func(record string, err error) {
		batch.Partial = true
		f.skip(record, err)
	}
	stopAtLine := runstate.Skipped(f.stopped)
	// The watch log is the other reading the line is derived from, so a log cut
	// short at a line leaves the sessions as underived as a missing run does.
	stopAtWatchLine := func(record string, err error) {
		derived = false
		f.stopped(record, err)
	}
	since := cursors.Since

	states, err := f.Runs.Tolerant(readPastRun).Recorded()
	if err != nil {
		return Batch{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	// What is still in flight is counted from the same reading the crossings are
	// selected from, rather than asked for a second time: a run is in flight or it
	// is not, and two readings of the same files a moment apart could disagree
	// about it.
	inFlight := 0
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		if !state.Status.Terminal() {
			inFlight++
		}
		stream := runStream(state.RunID)
		batch.Streams[stream] = struct{}{}
		deliveries, err := f.runDeliveries(state, cursors.Streams[stream], since)
		if err != nil {
			return Batch{}, err
		}
		batch.Deliveries = append(batch.Deliveries, deliveries...)
	}

	conversed, err := f.conversationDeliveries(ctx, cursors, batch.Streams, readPastConversation, stopAtLine)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, conversed...)

	filed, err := f.Reports.Tolerant(stopAtLine).List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the collected reports: %w", err)
	}
	reported, err := f.logDeliveries(reportStream, cursors.Streams[reportStream], len(filed), since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromReport(filed[index])
			return filed[index].RecordedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, reported...)

	records, err := f.Proposals.Tolerant(stopAtLine).List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the proposed changes: %w", err)
	}
	proposals := amendment.Proposals(records)
	raised, err := f.logDeliveries(proposalStream, cursors.Streams[proposalStream], len(proposals), since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromProposal(proposals[index])
			return proposals[index].RaisedAt, notification, err
		})
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, raised...)

	// The watch log is read once and used twice: for what each session said it was
	// doing, and for whether any session is still doing it. Reading it twice would
	// let one pass post a session stopping and then derive a line from a log that
	// had moved underneath it.
	sessions, err := f.sessions(stopAtWatchLine)
	if err != nil {
		return Batch{}, err
	}
	watched, err := f.watchDeliveries(cursors, batch.Streams, sessions)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, watched...)

	refused, err := f.usageLimitDeliveries(cursors, batch.Streams, stopAtLine)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, refused...)

	// The operator's two switches are read once and used twice: for the messages
	// that say they were placed and lifted, and for what has stopped the line when
	// the heartbeat below asks. Reading them apart would let one pass post a hold
	// and derive a line nothing is holding.
	held := f.switches(f.skip)
	batch.Deliveries = append(batch.Deliveries, f.holdDeliveries(cursors.Streams[productStream], held)...)

	beat, err := f.heartbeatDeliveries(ctx, cursors.Streams[heartbeatStream], held, sessions, inFlight, derived, batch.Streams)
	if err != nil {
		return Batch{}, err
	}
	batch.Deliveries = append(batch.Deliveries, beat...)
	return batch, nil
}

// sessions reads what the watch sessions did, in the order they did it. A
// product nobody has watched has no log rather than a broken one, and a feed
// assembled without a watch store reads none.
func (f *HarnessFeed) sessions(readPast runstate.Skipped) ([]runstate.WatchTransition, error) {
	if f.Watch == nil {
		return nil, nil
	}
	transitions, err := f.Watch.Tolerant(readPast).List()
	if err != nil {
		return nil, fmt.Errorf("read what the watch sessions did: %w", err)
	}
	return transitions, nil
}

// switches reads the operator's two holds. Neither absence is a failure: not
// holding anything is the ordinary state of both.
//
// A record that cannot be read is neither absent nor held, and it is carried as
// exactly that third thing. Reading it as absent would post a release the
// operator never made; failing the pass over it would stop every other stream
// for as long as the record stayed unreadable, which is the starvation this
// reading exists to end. So it is said once, carried as unknown, and nothing is
// claimed about that switch until it reads again.
func (f *HarnessFeed) switches(readPast runstate.Skipped) switches {
	var read switches
	if intake, held, err := f.Intake.Tolerant().Held(); err != nil {
		readPast("the intake hold", err)
		read.intakeUnreadable = true
	} else {
		read.intake, read.intakeHeld = intake, held
	}
	if operator, held, err := f.Holds.Tolerant().Held(); err != nil {
		readPast("the operator hold", err)
		read.operatorUnreadable = true
	} else {
		read.operator, read.operatorHeld = operator, held
	}
	return read
}

// skip says one record was read past and names the remedy. A record left out of
// a listing is not recovered by a restart on its own — the reading is what the
// restart fixes, and until then that record is simply not reported — so the
// notice says skipped rather than promising it will arrive later.
func (f *HarnessFeed) skip(record string, err error) {
	f.once(record, err, "%s could not be decoded and was skipped; nothing else about the product was held up by it. A sink cannot read a record a build newer than its own wrote, so if a deploy has landed since this one started, restart it on the current build and that record is read: %v")
}

// stopped says one append-only log was cut short at a line, which is a different
// thing from a record being skipped and is said differently. The cursor stays
// behind the line, so what is behind it is delayed rather than lost and a restart
// genuinely does pick it up — but that log says nothing at all until somebody
// restarts, which is the part an operator has to know.
func (f *HarnessFeed) stopped(record string, err error) {
	f.once(record, err, "%s could not be decoded, so this sink stopped reading that log there rather than reading past it and losing the record. Everything behind that line waits, every other stream still reports, and restarting on the current build resumes from exactly that line: %v")
}

// once says one thing about one record, and says it once for as long as this
// process lives. The record stays unreadable until somebody acts, so a line per
// pass would be the same sentence every fifteen seconds — which is how a log
// stops being read, and the same reason a standing refusal is said once. A
// restart says it again, which is right: a restart that did not fix it is news.
func (f *HarnessFeed) once(record string, err error, format string) {
	notice := record + ": " + err.Error()
	if _, said := f.skipped[notice]; said {
		return
	}
	if f.skipped == nil {
		f.skipped = map[string]struct{}{}
	}
	f.skipped[notice] = struct{}{}
	f.say(format, record, err)
}

// watchDeliveries says what the sessions that choose work have been doing. It is
// an append-only log like the reports pile and advances by position, and it is a
// stream of its own rather than part of the product's marks because it is a
// history rather than a switch that is on or off: a session that idled all night
// and one that stopped at midnight are both things somebody reads afterwards.
func (f *HarnessFeed) watchDeliveries(cursors Cursors, streams map[string]struct{}, transitions []runstate.WatchTransition) ([]Delivery, error) {
	if f.Watch == nil {
		return nil, nil
	}
	streams[watchStream] = struct{}{}
	return f.logDeliveries(watchStream, cursors.Streams[watchStream], len(transitions), cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromWatch(transitions[index])
			return transitions[index].At, notification, err
		})
}

// usageLimitDeliveries says where a provider refused the harness outside a run.
// It is an append-only log like the watch transitions and advances by position,
// and it is a stream of its own rather than part of any run's: the processes
// that meet a refusal have no run between them, which is the whole reason the
// log exists.
func (f *HarnessFeed) usageLimitDeliveries(cursors Cursors, streams map[string]struct{}, readPast runstate.Skipped) ([]Delivery, error) {
	if f.UsageLimits == nil {
		return nil, nil
	}
	streams[usageLimitStream] = struct{}{}
	exhaustions, err := f.UsageLimits.Tolerant(readPast).List()
	if err != nil {
		return nil, fmt.Errorf("read what the provider refused: %w", err)
	}
	return f.logDeliveries(usageLimitStream, cursors.Streams[usageLimitStream], len(exhaustions), cursors.Since,
		func(index int) (time.Time, notify.Notification, error) {
			notification, err := notify.FromUsageLimit(exhaustions[index])
			return exhaustions[index].At, notification, err
		})
}

// runDeliveries says what one run's record crossed since the reading already
// reported, and advances that reading only once the whole crossing has been
// posted. A crash halfway therefore repeats what it had already said rather than
// losing what it had not, which is the trade the design takes deliberately: the
// durable record is authoritative and this is a view of it.
func (f *HarnessFeed) runDeliveries(state runstate.State, cursor Cursor, since time.Time) ([]Delivery, error) {
	if cursor.Closed {
		return nil, nil
	}
	stream := runStream(state.RunID)
	var before runstate.State
	if cursor.Reported != nil {
		before = *cursor.Reported
	} else if len(cursor.Delivered) == 0 && predates(since, completion(state)) {
		// A run that was over before the watermark is history nobody turned
		// reporting on to read, so its cursor closes without a word. A run that
		// both started and finished while the sink was down is not that: it
		// finished after the watermark, so it is caught up on in full.
		//
		// A run this sink has already said something about is never history
		// whatever its dates say. Nothing backdates a completion today, so the
		// two cannot disagree, but if one ever did, a thread that stopped
		// mid-narrative is a worse answer than a run reported to its end.
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

// conversationDeliveries says what each of the product's conversations did to
// the backlog since the reading already reported. A conversation's log is an
// append-only log like the reports pile, so it advances by position: what it
// holds is mostly the turn itself — provider messages, tools, the reply as it
// was written — and the milestones are the few records among them where the
// queue actually moved. Everything else advances the position and says nothing.
// A conversation is read past on two different terms, which is why it is given
// two skips. A record that will not decode takes its `chat:` stream with it, so
// the pass can no longer enumerate what this product has; a line of its event log
// does not, because the record beside it already named the stream.
func (f *HarnessFeed) conversationDeliveries(ctx context.Context, cursors Cursors, streams map[string]struct{}, readPastRecord, readPastLine runstate.Skipped) ([]Delivery, error) {
	if f.Conversations == nil {
		return nil, nil
	}
	conversations, err := f.Conversations.Tolerant(readPastRecord).Recorded()
	if err != nil {
		return nil, fmt.Errorf("read the recorded conversations: %w", err)
	}
	logs := f.Conversations.Tolerant(readPastLine)
	var deliveries []Delivery
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stream := conversationStream(conversation.ConversationID)
		streams[stream] = struct{}{}
		events, err := logs.LoadEvents(conversation.ConversationID)
		if err != nil {
			return nil, fmt.Errorf("read the log of conversation %s: %w", conversation.ConversationID, err)
		}
		said, err := f.logDeliveries(stream, cursors.Streams[stream], len(events), cursors.Since,
			func(index int) (time.Time, notify.Notification, error) {
				notification, err := notify.FromConversation(conversation, events, index)
				return events[index].Timestamp, notification, err
			})
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, said...)
	}
	return deliveries, nil
}

// logDeliveries reads an append-only log from where the cursor left it. A record
// selection had nothing to say about is read past exactly as one that predates
// the watermark is: the position still moves, so a log that is mostly not
// milestones is not re-read from its beginning on every pass. What was filed
// before the watermark is read past in one silent advance, because a
// channel turned on today does not want a year of history arriving at once.
// Nothing after the watermark is ever read past on age: the watermark is a fixed
// moment rather than this process's start, so a record filed while the sink was
// down is still news when it comes back, which is the difference between an
// outage that delays messages and one that loses them.
func (f *HarnessFeed) logDeliveries(stream string, cursor Cursor, count int, since time.Time, at func(int) (time.Time, notify.Notification, error)) ([]Delivery, error) {
	var deliveries []Delivery
	skipped := cursor.Position
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
		if notification.Silent() || predates(since, recordedAt) {
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
//
// A switch this pass could not read says nothing either way and leaves its mark
// where it is. That absence is the reading failing rather than the operator
// lifting anything, and the lift is derived from an absence, so a switch that
// went unread has to be left out of the derivation entirely.
func (f *HarnessFeed) holdDeliveries(cursor Cursor, read switches) []Delivery {
	var deliveries []Delivery
	advanced := cursor

	if !read.intakeUnreadable {
		if intake, held := read.intake, read.intakeHeld; held {
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
	}

	if !read.operatorUnreadable {
		if operator, held := read.operator, read.operatorHeld; held {
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
	}
	return deliveries
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

// predates reports what the product recorded before the watermark. A record with
// no moment on it is never history, because absence of a date is not evidence of
// age; nor is anything at all when there is no watermark, which is what a caller
// asking for the whole record leaves behind.
func predates(since, at time.Time) bool {
	return !since.IsZero() && !at.IsZero() && at.Before(since)
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
