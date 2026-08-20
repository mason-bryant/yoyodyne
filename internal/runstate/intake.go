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
	"errors"
	"fmt"
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

// IntakeHold is the recorded fact that the operator has stopped the harness
// choosing new work for this product. It carries when they did and why, and
// nothing else: what lifts it is them, so there is no deadline to record.
type IntakeHold struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	HeldAt        time.Time        `json:"held_at"`
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
	if len(h.Reason) > MaxIntakeReasonBytes {
		problems = append(problems, fmt.Errorf("intake hold reason is %d bytes, which exceeds the %d byte bound", len(h.Reason), MaxIntakeReasonBytes))
	}
	return errors.Join(problems...)
}

// IntakeHoldStore is where the hold is recorded: one file under the product,
// because the queue it holds belongs to one.
type IntakeHoldStore struct {
	root      string
	productID domain.ProductID
	// reading is how strictly the record is decoded, for the reason the run store
	// carries one: a reader of other processes' output takes a tolerant view.
	reading
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

// Hold records the operator's hold on intake. Holding what is already held is
// deliberately not an error and deliberately does not restamp it: an operator who
// holds twice means the same thing the second time, and when the hold began is
// what says how long the harness has been choosing nothing. A second reason is
// dropped for the same reason the time is kept — the hold in force is the one
// that was placed, and rewriting its reason would rewrite the account of why
// nothing has started since.
func (s *IntakeHoldStore) Hold(reason string, at time.Time) (IntakeHold, error) {
	if existing, held, err := s.Held(); err != nil || held {
		return existing, err
	}
	recorded := IntakeHold{
		SchemaVersion: IntakeHoldSchemaVersion,
		ProductID:     s.productID,
		HeldAt:        at.UTC(),
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

// Held reports whether the operator is holding intake. No record is the ordinary
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
	decoder := s.decoder(file, maxEncodedStateBytes)
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
