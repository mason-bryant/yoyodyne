package slack

// The one thing the sink says that is not about the work at all: the template
// this project was generated from has improved a value nobody here ever edited.
//
// Everything else this surface carries is news — a run crossed a milestone, a
// hold was placed, a session went quiet. This is an offer, and it is the mildest
// thing in the vocabulary: nothing is wrong, nothing is degraded, and nothing is
// waiting on anybody. A project is entitled to decline it forever.
//
// What makes it worth a message anyway is who never hears it. The comparison is
// already printed by `yoyo config drift` and said as an aside by the commands an
// operator runs by hand — but a harness left running for a fortnight runs none of
// them, so a fix the template has made to a persona sits unheard for as long as
// nobody types anything. The operator asked for it to arrive instead, and the
// architect's ruling of 2026-09-03 admitted it to the direct-message tier as the
// first member of the advisory-once class: a fact addressed to a person that
// speaks exactly once per fact, deduplicated durably, never repeated, never
// urgent in presentation.
//
// Exactly once is the whole of the class, so it is the whole of what this file
// is careful about. The dedup is a mark per improvement in the sink's own
// durable cursor — the same record that stops a crash repeating every other
// message — which is what makes a restart silent about an improvement already
// said, and what makes having said one provable from a file rather than from a
// process's memory.

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/notify"
)

// Improvements is the three-way comparison between what a project's template
// supplied when the project was generated, what the project holds today, and
// what the template supplies now.
//
// It is the whole comparison rather than the offered values alone because the
// comparison is what words them: which class a value is in and what a message
// says about one both live in the configuration, and a sink that took a list of
// keys and wrote its own sentence would be a second surface answering "is this
// project current" its own way. See the `surfaces-project-one-read-model`
// invariant.
type Improvements interface {
	Offered(ctx context.Context) (config.Drift, error)
}

// improvementDeliveries says each value the template has improved that this
// project never edited, once each and never again.
//
// The order of the work is the cost model the rest of this package keeps. The
// comparison means re-reading the project's configuration and the baseline
// beside it, so it is asked at most once a heartbeat rather than once a poll: a
// sink polling every fifteen seconds reads nothing at all on the passes in
// between, and what an hour's delay costs on a fact nobody is waiting for is
// nothing.
//
// A mark is dropped once its improvement is no longer offered, which is the
// operator having adopted the value, edited it themselves, or regenerated the
// baseline. That keeps the cursor a record of what is standing rather than a
// list of every value the template has ever moved — and it is safe to forget
// because the state it recorded is over: for the same improvement to be offered
// again the project would have to go back to the value it had, at which point it
// is available again and saying so once is right.
func (f *HarnessFeed) improvementDeliveries(ctx context.Context, cursor Cursor, streams map[string]struct{}) ([]Delivery, error) {
	if f.Improvements == nil {
		return nil, nil
	}
	streams[improvementStream] = struct{}{}

	now := f.now()
	if !cursor.Said.IsZero() && now.Sub(cursor.Said) < f.heartbeat() {
		return nil, nil
	}
	read := cursor
	read.Said = now

	drift, err := f.Improvements.Offered(ctx)
	if err != nil {
		// A comparison that cannot be made is not a project that is current, and
		// this must not guess in either direction: inventing improvements would
		// offer values nobody supplied, and reporting none would be the silence
		// this exists to end. So it is said where the sink says everything else
		// about itself, and asked again at the next interval rather than at the
		// next poll.
		f.say("what this project's template has improved could not be read, so nothing was said about it: %v", err)
		return []Delivery{{Stream: improvementStream, Cursor: read}}, nil
	}

	offered := drift.Available()
	advanced := forgetAdopted(read, offered)
	var deliveries []Delivery
	for _, value := range offered {
		mark := improvementMarkOf(value)
		if advanced.Has(mark) {
			continue
		}
		advanced = advanced.With(mark)
		deliveries = append(deliveries, Delivery{
			Stream: improvementStream,
			Cursor: advanced,
			// It goes to the operators as well as to the channel, which is what the
			// operator asked for and what the ruling admitted. It is the quietest
			// thing that tier carries: a note, said once, with no move in it for
			// anybody — and a channel is somewhere somebody chooses to look, which
			// is exactly what an unattended fortnight does not include.
			Direct: true,
			Notification: notify.FromImprovement(notify.Improvement{
				Setting: value.Key,
				Says:    drift.Improvement(value),
			}, now),
		})
	}
	if len(deliveries) == 0 {
		// Nothing to offer, or nothing new to offer, which is the ordinary answer
		// on every pass after the first. The clock still moves, because what a
		// reading costs is the configuration read again and there is no reason to
		// spend one every fifteen seconds on a project that has heard all of it.
		return []Delivery{{Stream: improvementStream, Cursor: advanced}}, nil
	}
	return deliveries, nil
}

// forgetAdopted drops the marks of improvements that are no longer offered, so
// the cursor holds what is standing rather than everything ever said.
func forgetAdopted(cursor Cursor, offered []config.Value) Cursor {
	standing := make(map[string]struct{}, len(offered))
	for _, value := range offered {
		standing[improvementMarkOf(value)] = struct{}{}
	}
	kept := cursor
	for _, mark := range cursor.Delivered {
		if !strings.HasPrefix(mark, improvementMark) {
			continue
		}
		if _, still := standing[mark]; !still {
			kept = kept.Without(mark)
		}
	}
	return kept
}

// improvementMarkOf names one improvement so that having said it survives a
// restart. The key alone would not do it: a template that improves one setting
// again a month later has improved it twice, and a mark that could not tell the
// two apart would swallow the second one forever. What the template supplies now
// is digested rather than carried, because a cursor is a record of what was said
// and not a second copy of the configuration.
func improvementMarkOf(value config.Value) string {
	digest := fnv.New64a()
	fmt.Fprint(digest, value.Bundle)
	return improvementMark + value.Key + ":" + strconv.FormatUint(digest.Sum64(), 36)
}
