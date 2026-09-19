package runstate

// The operator's hold on what the harness starts by itself.
//
// There are two switches and they are not the same switch. The operator hold at
// the state root stops everything the harness would spend on a provider,
// including the runs already under way, which park at their next boundary
// keeping everything they have. That is the right verb for an account, a bill,
// or an afternoon away from the machine.
//
// This is the narrower one: stop choosing new work, and let what is running
// finish. It is the case an operator reaches for when something looks wrong but
// not urgent — a decomposition that is heading somewhere odd, a queue they want
// to reorder first — and it is the one that has to exist before it is needed,
// because the alternative in the moment is stopping everything and losing the
// work in flight along with it.
//
// It is product-scoped rather than machine-wide, which is the other difference:
// what a development manager may pull is a fact about one backlog, and holding
// intake on one product must not quietly stop another. It is a flag rather than
// an instruction to any particular run — the pipeline reads it where a run would
// be started for a reason other than the operator naming it, and starts nothing.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// IntakeHoldSchemaVersion is 1 and has never changed.
const IntakeHoldSchemaVersion = 1

// MaxIntakeReasonBytes bounds the reason an operator gives for holding intake.
// A hold somebody comes back to in the morning is only useful if it says what it
// was for, and a bounded line is enough to say it.
const MaxIntakeReasonBytes = 4 << 10

// IntakeHolder is who placed a hold on intake. It is recorded with the hold
// rather than worked out afterwards, because the two things that place one are
// different things for a reader to do something about, and every surface that
// says intake is held has to say which it was. Inferring it is what went wrong
// before: a session that reported its own brake's hold as the operator's sent
// somebody to look for a decision nobody had made.
type IntakeHolder string

const (
	// IntakeHolderOperator is the operator stopping the choosing themselves,
	// which is what the switch exists for.
	IntakeHolderOperator IntakeHolder = "operator"
	// IntakeHolderBrake is the harness's own failure-storm brake placing the
	// operator's switch after runs kept blocking. What lifts it is still a
	// person, which is the whole reason for tripping it.
	IntakeHolderBrake IntakeHolder = "brake"
)

// Recorded reports a holder this harness knows how to name. The empty holder is
// not one: it is a hold written before the holder was recorded, and it is read
// rather than refused — a hold nobody can read must never be treated as absent —
// while nothing pretends to know whose it is.
func (h IntakeHolder) Recorded() bool {
	switch h {
	case IntakeHolderOperator, IntakeHolderBrake:
		return true
	}
	return false
}

// MaxIntakeStops bounds how many stops a brake's hold names, and
// MaxIntakeStopReasonBytes bounds what each says. The brake trips on a handful
// of runs in a row, so the bound is a sanity limit rather than a working one;
// the reason is a line, because what the hold has to say about each run is which
// it was and what stopped it, and the run's own record holds the rest.
const (
	MaxIntakeStops           = 32
	MaxIntakeStopReasonBytes = 1 << 10
)

// IntakeStop is one run the brake counted on its way to tripping: which run, on
// which item, and what stopped it. It is recorded on the hold because the hold
// is the one record that says why the line stopped, and a message that said
// "three runs blocked" without naming them sent the operator to work out which
// three — on 2026-09-19 that took nearly two hours, because nothing said it at
// all.
type IntakeStop struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason,omitempty"`
}

func (s IntakeStop) Validate() error {
	var problems []error
	if strings.TrimSpace(s.RunID) == "" {
		problems = append(problems, errors.New("a stop the brake counted names its run"))
	}
	if strings.TrimSpace(s.WorkItemID) == "" {
		problems = append(problems, errors.New("a stop the brake counted names its work item"))
	}
	if len(s.Reason) > MaxIntakeStopReasonBytes {
		problems = append(problems, fmt.Errorf("stop reason is %d bytes, which exceeds the %d byte bound", len(s.Reason), MaxIntakeStopReasonBytes))
	}
	return errors.Join(problems...)
}

// Says is the one clause every surface prints about one stop: the run, its
// item, and what stopped it, folded to a line.
func (s IntakeStop) Says() string {
	said := fmt.Sprintf("run %s of %s", strings.TrimSpace(s.RunID), strings.TrimSpace(s.WorkItemID))
	if reason := strings.Join(strings.Fields(s.Reason), " "); reason != "" {
		said += " stopped: " + reason
	}
	return said
}

// IntakeHold is the recorded fact that the harness has been stopped from
// choosing new work for this product. It carries who stopped it, when, and why,
// and nothing else: what lifts it is a person, so there is no deadline to
// record.
type IntakeHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	HeldAt        time.Time        `json:"held_at"`
	// HeldBy is who placed it. It is absent only on a hold written before this
	// was recorded, which Says names as the absence it is rather than guessing.
	HeldBy IntakeHolder `json:"held_by,omitempty"`
	// Reason is optional, for the reason a stop's is: an operator who holds
	// intake in a hurry owes nobody an explanation.
	Reason string `json:"reason,omitempty"`
	// Stops is the runs the brake counted when it placed this hold, oldest
	// first. It is empty on the operator's own hold, which counts nothing, and on
	// a brake's hold written before the runs were recorded on it.
	Stops []IntakeStop `json:"stops,omitempty"`
}

func (h IntakeHold) Validate() error {
	var problems []error
	if h.SchemaVersion != IntakeHoldSchemaVersion {
		problems = append(problems, fmt.Errorf("intake hold schema version %d is not supported", h.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(h.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if h.HeldAt.IsZero() {
		problems = append(problems, errors.New("held at is required"))
	}
	// An unrecorded holder is a hold from before this was written down and is
	// read as one. A holder that is recorded and is not one this harness places
	// is a record nothing here can speak for, and reading it as an absence would
	// put the guessing back.
	if h.HeldBy != "" && !h.HeldBy.Recorded() {
		problems = append(problems, fmt.Errorf("intake hold holder %q is not one this harness records", h.HeldBy))
	}
	if len(h.Reason) > MaxIntakeReasonBytes {
		problems = append(problems, fmt.Errorf("intake hold reason is %d bytes, which exceeds the %d byte bound", len(h.Reason), MaxIntakeReasonBytes))
	}
	if len(h.Stops) > MaxIntakeStops {
		problems = append(problems, fmt.Errorf("intake hold names %d stops, which exceeds the %d bound", len(h.Stops), MaxIntakeStops))
	}
	// Only the brake counts runs, so stops on any other hold are a record
	// nothing here wrote.
	if len(h.Stops) > 0 && h.HeldBy != IntakeHolderBrake {
		problems = append(problems, errors.New("only the brake's hold names the stops that tripped it"))
	}
	for index, stop := range h.Stops {
		if err := stop.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("stop %d: %w", index, err))
		}
	}
	return errors.Join(problems...)
}

// StopsSay is what the brake counted, as one clause: each run, its item, and
// what stopped it, in the order they stopped. It is empty where the hold names
// none, so a surface composing a sentence can leave the clause out rather than
// print an empty list. It is beside Says rather than inside it because Says is
// folded to a line wherever a hold is printed in passing, and a list of runs
// is exactly what such a fold would cut.
func (h IntakeHold) StopsSay() string {
	if len(h.Stops) == 0 {
		return ""
	}
	said := make([]string, 0, len(h.Stops))
	for _, stop := range h.Stops {
		said = append(said, stop.Says())
	}
	return strings.Join(said, "; ")
}

// Braked reports a hold the harness's own brake placed, which is the one hold
// that is a finding for the operator rather than a decision he made: he has to
// be told it happened, and he has to lift it.
func (h IntakeHold) Braked() bool { return h.HeldBy == IntakeHolderBrake }

// IntakeReleaseSchemaVersion is 1 and has never changed.
const IntakeReleaseSchemaVersion = 1

// MaxIntakeReleasedByBytes bounds who a release names. It is a line: a terminal,
// or a conversation and its turn.
const MaxIntakeReleasedByBytes = 1 << 10

// IntakeRelease is the record of the last hold being lifted: what was lifted,
// when, and by whom. It exists because a hold is a file and its release is that
// file's absence, and an absence says nothing about who made it — which is what
// the channel has to say when a brake the operator was told about is lifted.
// One record rather than a log, because what it answers is who lifted the hold
// that was just standing, and the hold before that is history the channel
// already said.
type IntakeRelease struct {
	SchemaVersion int        `json:"schema_version"`
	Hold          IntakeHold `json:"hold"`
	ReleasedAt    time.Time  `json:"released_at"`
	// ReleasedBy is who lifted it, in the words the surface that lifted it
	// recorded: the terminal, or the conversation and turn. It is absent on a
	// release nothing named, which is read rather than refused.
	ReleasedBy string `json:"released_by,omitempty"`
}

func (r IntakeRelease) Validate() error {
	var problems []error
	if r.SchemaVersion != IntakeReleaseSchemaVersion {
		problems = append(problems, fmt.Errorf("intake release schema version %d is not supported", r.SchemaVersion))
	}
	if err := r.Hold.Validate(); err != nil {
		problems = append(problems, fmt.Errorf("released hold: %w", err))
	}
	if r.ReleasedAt.IsZero() {
		problems = append(problems, errors.New("released at is required"))
	}
	if len(r.ReleasedBy) > MaxIntakeReleasedByBytes {
		problems = append(problems, fmt.Errorf("released by is %d bytes, which exceeds the %d byte bound", len(r.ReleasedBy), MaxIntakeReleasedByBytes))
	}
	return errors.Join(problems...)
}

// Says is who lifted the hold, as a clause: what the surface recorded, or the
// stated absence.
func (r IntakeRelease) Says() string {
	if by := strings.TrimSpace(r.ReleasedBy); by != "" {
		return "released by " + by
	}
	return "released by somebody the record does not name"
}

// Says is the one clause every surface prints about a hold in force: who placed
// it and what caused it, composed once here rather than assembled by whichever
// format string is doing the printing.
//
// It is one clause rather than a whole sentence because every surface that
// prints it already frames it — a banner, a headline, a persona's line — and
// what they were all missing was whose hold it is and why. Three of those
// frames stacked, each introducing the next with its own colon, is what an
// operator read instead, and the innermost one was the only one that named the
// actual holder.
//
// The connective differs by holder because the causes do: a brake counts runs
// that blocked, and an operator says what looked wrong.
func (h IntakeHold) Says() string {
	cause := strings.TrimSpace(h.Reason)
	switch h.HeldBy {
	case IntakeHolderBrake:
		if cause == "" {
			return "the harness's own brake placed it after runs kept blocking"
		}
		return "the harness's own brake placed it after " + cause
	case IntakeHolderOperator:
		if cause == "" {
			return "the operator placed it and gave no reason"
		}
		return "the operator placed it — " + cause
	}
	if cause == "" {
		return "the record does not say who placed it or why"
	}
	return "the record does not say who placed it — " + cause
}

// IntakeHoldStore is where the hold is recorded: one file under the product,
// because the queue it holds belongs to one.
type IntakeHoldStore struct {
	root      string
	productID domain.ProductID
}

func NewIntakeHoldStore(root string, productID domain.ProductID) (*IntakeHoldStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &IntakeHoldStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *IntakeHoldStore) Root() string { return s.root }

// Hold records a hold on intake, and who is placing it. Holding what is already
// held is deliberately not an error and deliberately does not restamp it: an
// operator who holds twice means the same thing the second time, and when the
// hold began is what says how long the harness has been choosing nothing. A
// second reason and a second holder are dropped for the same reason the time is
// kept — the hold in force is the one that was placed, and rewriting either would
// rewrite the account of why nothing has started since. That is also what makes
// the brake tripping over the operator's own hold report the operator, which is
// the truth about who stopped the line.
func (s *IntakeHoldStore) Hold(holder IntakeHolder, reason string, at time.Time) (IntakeHold, error) {
	return s.hold(holder, reason, at, nil)
}

// Brake is the harness's own brake placing the hold, naming the runs it
// counted. It is the same switch Hold places — an operator arriving at a
// stopped line finds one thing to lift — and it holds nothing already held, for
// the reason Hold does not restamp: the brake tripping over the operator's own
// hold reports the operator, which is the truth about who stopped the line.
func (s *IntakeHoldStore) Brake(reason string, stops []IntakeStop, at time.Time) (IntakeHold, error) {
	return s.hold(IntakeHolderBrake, reason, at, stops)
}

func (s *IntakeHoldStore) hold(holder IntakeHolder, reason string, at time.Time, stops []IntakeStop) (IntakeHold, error) {
	if !holder.Recorded() {
		return IntakeHold{}, fmt.Errorf("intake hold holder %q is not one this harness records", holder)
	}
	if existing, held, err := s.Held(); err != nil || held {
		return existing, err
	}
	recorded := IntakeHold{
		SchemaVersion: IntakeHoldSchemaVersion,
		ProductID:     s.productID,
		HeldAt:        at.UTC(),
		HeldBy:        holder,
		Reason:        strings.TrimSpace(reason),
	}
	for _, stop := range stops {
		recorded.Stops = append(recorded.Stops, IntakeStop{
			RunID:      strings.TrimSpace(stop.RunID),
			WorkItemID: strings.TrimSpace(stop.WorkItemID),
			Reason:     boundIntakeStopReason(stop.Reason),
		})
	}
	if err := recorded.Validate(); err != nil {
		return IntakeHold{}, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return IntakeHold{}, fmt.Errorf("create product state directory: %w", err)
	}
	if err := s.replace(s.path(), ".intake-hold-*.tmp", "intake hold", recorded); err != nil {
		return IntakeHold{}, err
	}
	return recorded, nil
}

// boundIntakeStopReason folds what stopped a run to the line the hold carries.
// The run's own record holds the whole; this is what a message names it by.
func boundIntakeStopReason(reason string) string {
	folded := strings.Join(strings.Fields(reason), " ")
	if len(folded) <= MaxIntakeStopReasonBytes {
		return folded
	}
	cut := MaxIntakeStopReasonBytes - len("…")
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimRight(folded[:cut], " ") + "…"
}

// replace writes one record under the product atomically: to a temporary file
// beside it, then renamed over the name, then the directory synced.
func (s *IntakeHoldStore) replace(path, pattern, what string, record any) error {
	temporary, err := os.CreateTemp(s.root, pattern)
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", what, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary %s: %w", what, err)
	}
	if err := writeJSONFile(temporary, what, record); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", what, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", what, err)
	}
	return syncDirectory(s.root)
}

// Held reports whether intake is held, by whoever placed it. No record is the ordinary
// answer and means the harness may choose work, which is why it is reported as an
// absence rather than as a failure to look. A record that cannot be read is
// neither: it is an error, because a hold nobody can read must never be started
// through as though it were absent.
func (s *IntakeHoldStore) Held() (IntakeHold, bool, error) {
	file, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return IntakeHold{}, false, nil
	}
	if err != nil {
		return IntakeHold{}, false, fmt.Errorf("open intake hold: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var held IntakeHold
	if err := decoder.Decode(&held); err != nil {
		return IntakeHold{}, false, fmt.Errorf("decode intake hold: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return IntakeHold{}, false, fmt.Errorf("decode intake hold: %w", err)
	}
	if err := held.Validate(); err != nil {
		return IntakeHold{}, false, err
	}
	if held.ProductID != s.productID {
		return IntakeHold{}, false, fmt.Errorf("intake hold belongs to product %q, not %q", held.ProductID, s.productID)
	}
	return held, true, nil
}

// Release lifts the hold and reports what was lifted, recording who lifted it
// beside the absence. Releasing what is not held is not an error for the same
// reason holding twice is not: the operator means the harness to be choosing
// work, and it is — and nothing is recorded, because nothing was lifted.
//
// The release record is written before the hold is removed, so a process
// killed between the two leaves a hold that is still standing and a release
// that names it; the next release overwrites the record, and a reader matching
// the record to the hold it lifted finds the hold still there and reads past it.
func (s *IntakeHoldStore) Release(by string, at time.Time) (IntakeHold, bool, error) {
	held, found, err := s.Held()
	if err != nil {
		return IntakeHold{}, false, err
	}
	if !found {
		return IntakeHold{}, false, nil
	}
	release := IntakeRelease{
		SchemaVersion: IntakeReleaseSchemaVersion,
		Hold:          held,
		ReleasedAt:    at.UTC(),
		ReleasedBy:    strings.Join(strings.Fields(by), " "),
	}
	if err := release.Validate(); err != nil {
		return IntakeHold{}, false, err
	}
	if err := s.replace(s.releasePath(), ".intake-release-*.tmp", "intake release", release); err != nil {
		return IntakeHold{}, false, err
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return IntakeHold{}, false, fmt.Errorf("release intake hold: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return IntakeHold{}, false, err
	}
	return held, true, nil
}

// LastRelease is the record of the last hold lifted, or nothing where none has
// ever been. A record that cannot be read is an error rather than an absence,
// for the reason a hold's is: a reader would otherwise say nobody lifted a
// hold somebody did.
func (s *IntakeHoldStore) LastRelease() (IntakeRelease, bool, error) {
	file, err := os.Open(s.releasePath())
	if errors.Is(err, os.ErrNotExist) {
		return IntakeRelease{}, false, nil
	}
	if err != nil {
		return IntakeRelease{}, false, fmt.Errorf("open intake release: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var release IntakeRelease
	if err := decoder.Decode(&release); err != nil {
		return IntakeRelease{}, false, fmt.Errorf("decode intake release: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return IntakeRelease{}, false, fmt.Errorf("decode intake release: %w", err)
	}
	if err := release.Validate(); err != nil {
		return IntakeRelease{}, false, err
	}
	if release.Hold.ProductID != s.productID {
		return IntakeRelease{}, false, fmt.Errorf("intake release belongs to product %q, not %q", release.Hold.ProductID, s.productID)
	}
	return release, true, nil
}

// path names the hold's file. It is one fixed name under the product: nothing
// about it is derived from anything a caller supplies.
func (s *IntakeHoldStore) path() string {
	return filepath.Join(s.root, "intake-hold.json")
}

// releasePath names the record of the last release, beside the hold it lifted.
func (s *IntakeHoldStore) releasePath() string {
	return filepath.Join(s.root, "intake-release.json")
}
