package agentcontext

// The merge back from a side conversation, which is a memory write and nothing
// else.
//
// `docs/designs/management-and-supervision.md` settles the shape in one
// sentence: when a side thread concludes, by finishing or by exhausting its
// budget, it writes its substance into the agent's context through the
// agent-memory machinery — budgeted, redacted, audited, revisioned, referencing
// the side stream rather than copying its transcript, with drafted commitments
// marked as tentative. The main thread's next turn reads that revision as
// evidence and ratifies or adjusts anything the side thread promised through its
// own ordinary path.
//
// So it is deliberately not a second door. A merge composes a revision and hands
// it to Remember above, which is where the authority is checked and where the
// store redacts, budgets, and numbers it. Everything a merge adds is composition:
// what the memory is called, what it says, and what it cites. A merge that wrote
// its own way to the disk would be the fourth write path this package exists to
// prevent, and the first one that eventually forgets to record an invocation.
//
// It is not the no-action rule weakened either, which is the reading a reviewer
// arrives at first. `internal/sidestream` keeps `agent-context.mutate` off what a
// side thread may ask for, and that stays exactly true: a live side thread cannot
// invoke a memory write, and nothing here gives it one. The merge happens once
// the thread has ended, performed by the harness under the agent's own role
// authority — which is why Remember refuses it for a role that keeps no memory
// at all, however much its side thread found out.
//
// What it never carries is the transcript. The side thread's dialogue stays in
// the side thread's own log, and what reaches the main thread is an account of it
// with the stream's identifier attached — which is what makes the main thread's
// context a summary somebody can trace rather than a second conversation replayed
// into the first.
//
// Concluding is what performs the write, and Conclude below is that in one
// operation rather than two a caller has to remember to pair. A side thread that
// ended without merging is a thread whose whole substance is on a disk nothing
// reads, and it looks exactly like a thread that found nothing out — so ending
// one and writing what it found are the same call, and neither can be reached
// without the other.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// The bounds on what one merge may carry. They are here rather than left to the
// store's own because the store bounds the assembled revision, and a refusal
// naming the assembled text tells whoever produced the substance nothing about
// which part of it was too long.
//
// They are also chosen so the framing always fits: the widest merge this composes
// — the longest substance, the most commitments each at its own limit, and the
// account of where it came from around them — is smaller than one revision's
// budget, so a merge is never refused for the size of a sentence this package
// wrote.
const (
	// MaxSubstanceBytes is what the side thread concluded, in its own words.
	MaxSubstanceBytes = 4 << 10
	// MaxMergedCommitments is how many drafted commitments one merge carries, and
	// MaxCommitmentBytes bounds each. A side thread that promised more than a
	// handful of things has planned rather than answered, and the main thread has
	// to ratify every one of them by hand.
	MaxMergedCommitments = 6
	MaxCommitmentBytes   = 200
)

// ErrNotConcluded reports a merge asked for on a side stream that is still open.
// It is its own error because it is a caller's sequencing mistake rather than a
// malformed record: the merge is what conclusion does, so a stream still being
// held has nothing settled to write.
var ErrNotConcluded = errors.New("the side stream has not concluded")

// Conclusion is one concluded side conversation as it merges back: the stream
// itself, what it worked out, and what it tentatively promised.
//
// The stream is carried whole rather than as the few fields the revision needs,
// because what the merge records about the invocation — the backend, the model,
// the account, the configuration — is on the stream and must not be restated by a
// caller. A caller that could pass them separately is one that could pass a
// different model than the one that answered.
type Conclusion struct {
	Stream sidestream.Stream
	// Substance is what the side thread concluded, written for the main thread to
	// read. It is the side thread's account of its own work and never its
	// transcript: the transcript stays in the side stream's log, which this cites.
	Substance string
	// Commitments are what the side thread said it would do or have done. Every one
	// of them is a draft — the design makes a side thread's commitments best effort
	// until the main thread confirms them — so they are recorded as tentative and
	// framed as tentative, rather than being folded into the substance where the
	// distinction would be a matter of wording.
	Commitments []string
}

// Streams is the durable record of one product's side conversations, narrowed to
// the one thing concluding does to it. It is satisfied by
// *runstate.SideStreamStore.
//
// It is an interface rather than the store because this package writes memory and
// nothing else: what it may do to a side stream's own record is revise it as
// ended, and a narrowed interface is that sentence in Go. It is also what a
// caller holding the stream's lease hands in, which is where the exclusion that
// makes this safe actually lives.
type Streams interface {
	Save(stream sidestream.Stream) error
}

// Conclude ends one side conversation: it stamps the outcome and the moment on
// the stream, merges the substance into the agent's memory, and records the
// stream as ended. It answers the concluded record and the revision that was
// stored.
//
// The merge happens before the record is saved, and the order is the whole of
// what this decides. A stream saved as ended whose merge then failed is a thread
// nothing will merge afterwards — it is no longer open, so no retry reaches it —
// and its substance is lost with no trace but a record saying it concluded. The
// other way round costs nothing that matters: the memory holds the merge while
// the record still says open, and whoever holds the lease concludes it again,
// which appends a second revision of the same memory rather than a second memory.
//
// It takes the outcome and the moment rather than reading them off the stream,
// because a caller that could pass a stream it had already stamped could pass one
// stamped as concluded that never was.
func (c Conclusion) Conclude(ctx context.Context, streams Streams, store *runstate.MemoryStore, outcome sidestream.Outcome, at time.Time) (sidestream.Stream, runstate.MemoryRevision, error) {
	if streams == nil {
		return sidestream.Stream{}, runstate.MemoryRevision{}, errors.New("concluding a side stream has no record to write it to")
	}
	if !outcome.Valid() {
		return sidestream.Stream{}, runstate.MemoryRevision{}, fmt.Errorf("outcome %q is not one a side stream ends with", outcome)
	}
	if !c.Stream.Open() {
		return sidestream.Stream{}, runstate.MemoryRevision{}, fmt.Errorf("side stream %s ended already, as %q, and merged when it did", c.Stream.ID, c.Stream.Outcome)
	}
	if at.IsZero() {
		return sidestream.Stream{}, runstate.MemoryRevision{}, errors.New("concluding a side stream records the moment it ended")
	}
	concluded := c
	closed := at.UTC()
	concluded.Stream.Outcome = outcome
	concluded.Stream.ClosedAt = &closed
	concluded.Stream.UpdatedAt = closed
	recorded, err := concluded.merge(ctx, store)
	if err != nil {
		return sidestream.Stream{}, runstate.MemoryRevision{}, err
	}
	if err := streams.Save(concluded.Stream); err != nil {
		// The merge is already durable, so the failure says so: what is wrong is a
		// record that still reads as open, and the way out of it is to conclude the
		// stream again rather than to go looking for the substance.
		return concluded.Stream, recorded, fmt.Errorf(
			"record side stream %s as ended, whose merge is already stored as revision %d: %w",
			concluded.Stream.ID, recorded.Sequence, err)
	}
	return concluded.Stream, recorded, nil
}

// Merge writes the conclusion into the agent's memory and returns the revision as
// it was stored.
//
// What comes back is what is on the disk: redacted and numbered by the store. The
// main thread's next turn reads that rather than anything the caller composed,
// which is the difference between a merge that happened and one that was
// attempted.
func (c Conclusion) merge(ctx context.Context, store *runstate.MemoryStore) (runstate.MemoryRevision, error) {
	revision, err := c.Revision()
	if err != nil {
		return runstate.MemoryRevision{}, err
	}
	write := &Write{Store: store, Revision: revision}
	if err := write.Remember(ctx); err != nil {
		return runstate.MemoryRevision{}, fmt.Errorf("merge side stream %s: %w", c.Stream.ID, err)
	}
	return write.Recorded, nil
}

// Revision is the memory revision this conclusion merges as, unnumbered and
// unredacted — the store does both.
//
// It is exported so a caller can see what would be written without writing it,
// which is what the operator surfaces and the tests want. Nothing about it is a
// second write path: it produces a value, and the only thing that makes a value
// durable is Remember.
func (c Conclusion) Revision() (runstate.MemoryRevision, error) {
	if err := c.Stream.Validate(); err != nil {
		return runstate.MemoryRevision{}, err
	}
	if c.Stream.Open() {
		return runstate.MemoryRevision{}, fmt.Errorf("%w: %s is still being held", ErrNotConcluded, c.Stream.ID)
	}
	if err := c.bounded(); err != nil {
		return runstate.MemoryRevision{}, fmt.Errorf("merge side stream %s: %w", c.Stream.ID, err)
	}
	return runstate.MemoryRevision{
		SchemaVersion: runstate.MemorySchemaVersion,
		ProductID:     c.Stream.ProductID,
		Agent:         c.Stream.Agent,
		Role:          c.Stream.Role,
		// The memory is named for the stream it came from. A side stream identifier
		// is already an identifier of the shape a memory name is held to, so the name
		// an operator asks about is the record they would go and read — and two side
		// threads on one topic stay two memories rather than one overwriting the
		// other.
		Memory: c.Stream.ID,
		// Subject continuity, because what a side thread worked out is about the thing
		// it was opened for rather than about the project: it stops being worth
		// carrying when its topic is finished, and an agent that filed it as its own
		// standing knowledge would remember one question's answer as a general rule.
		Continuity: runstate.MemoryContinuitySubject,
		Subject:    subjectOf(c.Stream.Topic),
		Text:       c.text(),
		Sources: []runstate.MemorySource{
			{Kind: runstate.MemorySourceSideStream, ID: c.Stream.ID},
			{Kind: runstate.MemorySourceConversation, ID: c.Stream.Conversation},
		},
		Invocation: runstate.MemoryInvocation{
			Kind:           runstate.MemoryInvocationSideStream,
			ID:             c.Stream.ID,
			Turn:           c.Stream.Turns,
			Backend:        c.Stream.Backend,
			Model:          c.Stream.ProviderModel,
			ResolvedModel:  c.Stream.ProviderResolvedModel,
			AccountAlias:   c.Stream.AccountAlias,
			ConfigRevision: c.Stream.ConfigRevision,
			Build:          c.Stream.Build,
		},
		// The moment the thread ended, because merging is what concluding does. A
		// merge stamped with the moment it reached the disk would drift from the
		// stream it cites every time the write was retried.
		RecordedAt: *c.Stream.ClosedAt,
	}, nil
}

func (c Conclusion) bounded() error {
	var problems []error
	// A thread that never took a turn worked nothing out, so what it would merge is
	// somebody else's prose under the side thread's name. The store refuses the
	// revision either way, on the invocation it could not have produced; this says
	// which end the mistake is at.
	if c.Stream.Turns < 1 {
		problems = append(problems, errors.New("the side thread took no turn, so it concluded nothing to merge"))
	}
	substance := strings.TrimSpace(c.Substance)
	if substance == "" {
		problems = append(problems, errors.New("a merge records what the side thread concluded, so its substance is required"))
	}
	if len(substance) > MaxSubstanceBytes {
		problems = append(problems, fmt.Errorf("the substance is %d bytes, limit is %d", len(substance), MaxSubstanceBytes))
	}
	if len(c.Commitments) > MaxMergedCommitments {
		problems = append(problems, fmt.Errorf("%d commitments are drafted, limit is %d", len(c.Commitments), MaxMergedCommitments))
	}
	for index, commitment := range c.Commitments {
		trimmed := strings.TrimSpace(commitment)
		if trimmed == "" {
			problems = append(problems, fmt.Errorf("commitments[%d] says nothing", index))
			continue
		}
		if len(trimmed) > MaxCommitmentBytes {
			problems = append(problems, fmt.Errorf("commitments[%d] is %d bytes, limit is %d", index, len(trimmed), MaxCommitmentBytes))
		}
	}
	return errors.Join(problems...)
}

// text is the merge as the main thread will read it: an account of where this
// came from, what the side thread concluded, and what it promised, with the
// promises marked as the drafts they are.
//
// The framing is written here rather than left to whatever reads it, because the
// revision is what outlives this process. A memory that said only what the side
// thread decided would read, a month later, as something the agent decided — and
// the difference between those two is the whole of what the main thread ratifies.
func (c Conclusion) text() string {
	var merged strings.Builder
	merged.WriteString("A side conversation you held beside this one has concluded, and this is what it worked out. It is that thread's own account of its work, not its transcript, and it took no action: a side thread judges, answers, and plans tentatively, and nothing it decided has happened.\n\n")
	fmt.Fprintf(&merged, "Side stream %s, on %q, %s after %d of %d turn(s).\n\n",
		c.Stream.ID, singleLine(c.Stream.Topic), concluding(c.Stream.Outcome), c.Stream.Turns, c.Stream.MaxTurns)
	merged.WriteString(strings.TrimSpace(c.Substance))
	merged.WriteString("\n")
	if len(c.Commitments) > 0 {
		merged.WriteString("\nWhat it committed to, tentatively and on nobody's behalf but its own. Ratify or adjust each of these here, which is the only path that acts:\n")
		for _, commitment := range c.Commitments {
			merged.WriteString("- " + singleLine(commitment) + "\n")
		}
	}
	return merged.String()
}

// concluding says how the thread ended in the words the main thread should read
// it in. A thread cut off by its budget is legible as one rather than as a thread
// that finished early with less to say.
func concluding(outcome sidestream.Outcome) string {
	if outcome == sidestream.OutcomeSpent {
		return "stopped when it reached its budget"
	}
	return "concluded"
}

// subjectOf holds a stream's topic to what a memory subject may be. A topic is
// allowed more than a subject is, so the long ones are cut rather than refused:
// the subject is how an operator recognizes the memory in a listing, and a merge
// lost for the length of its topic would be the thread's whole substance thrown
// away over a label.
func subjectOf(topic string) string {
	trimmed := singleLine(topic)
	if len(trimmed) <= runstate.MaxMemorySubjectBytes {
		return trimmed
	}
	// Cut on a rune boundary rather than a byte one: a subject is read by a person,
	// and half a character is not a shorter label but a broken one.
	const ellipsis = "..."
	room := runstate.MaxMemorySubjectBytes - len(ellipsis)
	kept := 0
	for index := range trimmed {
		if index > room {
			break
		}
		kept = index
	}
	return strings.TrimSpace(trimmed[:kept]) + ellipsis
}

// singleLine flattens the control characters out of one value the merge prints on
// a line of its own, so a topic or a commitment carrying a newline cannot forge a
// second entry in the list it sits in.
func singleLine(value string) string {
	flattened := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	return strings.TrimSpace(strings.Join(strings.Fields(flattened), " "))
}
