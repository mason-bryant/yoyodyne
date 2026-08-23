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
	return errors.Join(problems...)
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
	if err := recorded.Validate(); err != nil {
		return IntakeHold{}, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return IntakeHold{}, fmt.Errorf("create product state directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".intake-hold-*.tmp")
	if err != nil {
		return IntakeHold{}, fmt.Errorf("create temporary intake hold: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return IntakeHold{}, fmt.Errorf("secure temporary intake hold: %w", err)
	}
	if err := writeJSONFile(temporary, "intake hold", recorded); err != nil {
		temporary.Close()
		return IntakeHold{}, err
	}
	if err := temporary.Close(); err != nil {
		return IntakeHold{}, fmt.Errorf("close temporary intake hold: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path()); err != nil {
		return IntakeHold{}, fmt.Errorf("replace intake hold: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return IntakeHold{}, err
	}
	return recorded, nil
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

// Release lifts the hold and reports what was lifted. Releasing what is not held
// is not an error for the same reason holding twice is not: the operator means
// the harness to be choosing work, and it is.
func (s *IntakeHoldStore) Release() (IntakeHold, bool, error) {
	held, found, err := s.Held()
	if err != nil {
		return IntakeHold{}, false, err
	}
	if !found {
		return IntakeHold{}, false, nil
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return IntakeHold{}, false, fmt.Errorf("release intake hold: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return IntakeHold{}, false, err
	}
	return held, true, nil
}

// path names the hold's file. It is one fixed name under the product: nothing
// about it is derived from anything a caller supplies.
func (s *IntakeHoldStore) path() string {
	return filepath.Join(s.root, "intake-hold.json")
}
