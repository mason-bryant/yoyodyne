package runstate

// Reading records that a newer build wrote.
//
// A process that reads other processes' durable output is older than some of
// what it reads, by construction: a deploy lands while it is running, and the
// next record it opens carries a key its own build has never heard of. Refusing
// that record is not caution. A reader decides nothing about what it reads, so
// there is nothing an unknown key could make it decide wrongly; what refusing
// buys is a reader that fails on the same file every pass until somebody
// notices, which is how a reporting outage begins and how one already did.
//
// So a reader of somebody else's records takes a tolerant view of the store. An
// unknown key is the additive change every schema here is evolved by — which is
// exactly what the schema version exists to tell apart from a breaking one — and
// it is read past silently. A record that will not decode at all is read past
// too, said out loud so the gap is not a silent one, rather than stopping every
// record behind it for as long as the process runs.
//
// It is a view rather than a setting on the store, because the processes that
// act on these records keep the strict reading: a key an actor does not
// understand is a key it would act without.
//
// The append-only logs are half of this already: they decode a line with
// json.Unmarshal, which has never minded a key it does not know, so what a
// tolerant view adds there is only the skip. That skip costs something the
// single-record stores' does not, and it is worth stating plainly. The position
// a cursor keeps on a log is a count of records rather than an offset in the
// file, so a line read past shifts the lines behind it. Within one process that
// is consistent, because the same lines are skipped on every pass. Across a
// restart onto the build that can read them it repeats a message rather than
// losing one, which is the at-least-once trade the sink already takes
// deliberately. The one direction it does lose is a downgrade — an older build
// resuming from a cursor a newer one advanced sees fewer records than that
// cursor counted, and reads past the difference — which is a real cost, and a
// narrower one than a log nobody can read at all.

import (
	"encoding/json"
	"fmt"
	"io"
)

// Skipped is told about one record a tolerant reader could not decode at all:
// what the record is called, and why it would not decode. A skip nobody is told
// about is a gap in the account that looks exactly like nothing having
// happened, so a tolerant view is only ever as honest as this is.
type Skipped func(record string, err error)

// reading is how a store decodes what it opens. The zero value is the strict
// reading, which is what every process that acts on these records gets.
type reading struct {
	lenient bool
	skipped Skipped
}

// tolerantReading is the view a reader of other processes' records takes.
func tolerantReading(skipped Skipped) reading {
	return reading{lenient: true, skipped: skipped}
}

// decoder decodes one record, bounded, and strict or tolerant as this reading
// says.
func (r reading) decoder(source io.Reader, limit int64) *json.Decoder {
	decoder := json.NewDecoder(io.LimitReader(source, limit))
	if !r.lenient {
		decoder.DisallowUnknownFields()
	}
	return decoder
}

// readPast reports one record the caller may carry on past, having said so. A
// strict reading never may: a record it cannot decode is a store it cannot
// trust, and it says so by failing the read.
func (r reading) readPast(record string, err error) bool {
	if !r.lenient {
		return false
	}
	if r.skipped != nil {
		r.skipped(record, err)
	}
	return true
}

// readPastLine is readPast for one line of an append-only log, which has no name
// of its own until this gives it one. The label names the log and the number
// names the line in it, because "a record would not decode" sends somebody to a
// file with thousands of them.
func (r reading) readPastLine(label string, number int, err error) bool {
	return r.readPast(fmt.Sprintf("%s line %d", label, number), err)
}

// Tolerant returns a view of the run records for a process that reads them
// rather than acting on them. A record written by a newer build decodes without
// its unknown keys, and a record that will not decode at all is skipped through
// skipped rather than failing the read of every record beside it.
func (s *Store) Tolerant(skipped Skipped) *Store {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}

// Tolerant returns the same view of the conversation records.
func (s *ConversationStore) Tolerant(skipped Skipped) *ConversationStore {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}

// Tolerant returns a view of the intake hold that reads a record a newer build
// wrote. It takes no skip because there is one record and reading past it is not
// the store's to decide: what to do about a hold nobody can read differs by who
// is asking. A process that acts on the hold must never start through it, and a
// process that only reports must say nothing about it rather than report it
// lifted — so both are told the record would not read, and each answers for
// itself.
func (s *IntakeHoldStore) Tolerant() *IntakeHoldStore {
	view := *s
	view.reading = tolerantReading(nil)
	return &view
}

// Tolerant returns the same view of the operator hold.
func (s *OperatorHoldStore) Tolerant() *OperatorHoldStore {
	view := *s
	view.reading = tolerantReading(nil)
	return &view
}

// Tolerant returns the same view of the collected reports. On an append-only log
// it is the skip that a reader gains: unknown keys were never refused there.
func (s *ReportStore) Tolerant(skipped Skipped) *ReportStore {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}

// Tolerant returns the same view of the proposed amendments.
func (s *AmendmentStore) Tolerant(skipped Skipped) *AmendmentStore {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}

// Tolerant returns the same view of what the watch sessions did.
func (s *WatchStore) Tolerant(skipped Skipped) *WatchStore {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}

// Tolerant returns the same view of what the provider refused.
func (s *UsageLimitStore) Tolerant(skipped Skipped) *UsageLimitStore {
	view := *s
	view.reading = tolerantReading(skipped)
	return &view
}
