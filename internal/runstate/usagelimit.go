package runstate

// A provider refusing the harness for want of capacity, recorded wherever it
// happened rather than only inside a run.
//
// A run that meets an exhausted usage limit already writes one down: the pause
// it records carries the limit, the deadline, and the item that is waiting, and
// the sink reads it off the run's own state. Nothing else in the harness had
// that. A conversation turn and an independent branch review are provider
// invocations with no run record to cross, so a limit that stopped one of them
// failed at whatever terminal asked for it and left the durable record silent —
// which is the same silence a healthy queue makes, at the exact moment somebody
// most needs to be told the difference.
//
// So the refusal says itself, in the shape every other log here is written in:
// an append-only log per product, one entry per refusal, carrying what was
// stopped and — where the provider named one — when it lifts. It is deliberately
// not the run pause: a pause is a run's own state, revised as the run serves it,
// and this is a moment that happened and is never revised.

import (
	"bufio"
	"bytes"
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

// UsageLimitSchemaVersion is 1 and has never changed.
const UsageLimitSchemaVersion = 1

// MaxUsageLimitWaitingBytes bounds what one refusal says is waiting on it. It
// is a phrase somebody reads in the morning rather than a record they study, so
// it is bounded like a watch transition's reason is.
const MaxUsageLimitWaitingBytes = 4 << 10

// maxEncodedUsageLimitBytes bounds one encoded refusal, including the trailing
// newline. The writer and the reader share it, so a refusal that was written is
// always one that can be read back.
const maxEncodedUsageLimitBytes = 16 << 10

// UsageLimitExhaustion is one moment a provider declined the harness for want
// of capacity. It carries what the refusal stopped rather than what the harness
// did about it: what an operator does with hours of silence depends entirely on
// which of their work is inside it.
type UsageLimitExhaustion struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	At            time.Time        `json:"at"`
	// Waiting names what the refusal stopped, in words: a conversation with a
	// role, a review of a branch. It is prose because the things that can be
	// waiting do not share a shape, and because the answer an operator needs is
	// to "what is not happening", which is a sentence rather than an identifier.
	Waiting string `json:"waiting"`
	// Kind is the provider's own name for the exhausted limit, carried as
	// evidence rather than interpreted. Empty where the provider named none.
	Kind string `json:"kind,omitempty"`
	// ResetsAt is when the provider said the limit lifts. It is absent where the
	// provider named no reset time, which is a different fact from a wait of
	// unknown length: the harness asks again rather than being told when.
	ResetsAt *time.Time `json:"resets_at,omitempty"`
	// The correlation identifiers, so a refusal can be read back to what it
	// stopped. Both are optional, because a refusal that stopped something
	// belonging to no work item and no conversation is still one somebody has to
	// be told about.
	WorkItemID     string `json:"work_item_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

func (e UsageLimitExhaustion) Validate() error {
	var problems []error
	if e.SchemaVersion != UsageLimitSchemaVersion {
		problems = append(problems, fmt.Errorf("usage limit schema version %d is not supported", e.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(e.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if e.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if strings.TrimSpace(e.Waiting) == "" {
		problems = append(problems, errors.New("waiting is required: a refusal nobody can say what it stopped is not worth recording"))
	}
	if len(e.Waiting) > MaxUsageLimitWaitingBytes {
		problems = append(problems, fmt.Errorf("what is waiting is %d bytes, which exceeds the %d byte bound", len(e.Waiting), MaxUsageLimitWaitingBytes))
	}
	if e.ResetsAt != nil && e.ResetsAt.IsZero() {
		problems = append(problems, errors.New("resets_at is present but names no moment; a provider that named none records none"))
	}
	return errors.Join(problems...)
}

// Describe says what the refusal was, as the object of "waiting out". It is the
// same sentence a paused run's cause is written in, taken from there rather than
// restated, so one exhausted limit does not read two ways depending on which
// process met it.
func (e UsageLimitExhaustion) Describe() string {
	described := DescribePause(PauseUsageLimit, e.Kind)
	if e.ResetsAt != nil {
		described += ", until " + e.ResetsAt.UTC().Format(time.RFC3339)
	}
	return described
}

// UsageLimitStore is where a product's refusals are collected, in the same
// operating-system state root as the runs and the reports and beside them rather
// than among them. It is one append-only log per product, because a refusal
// belongs to the account rather than to any run, and because the processes that
// meet one — a conversation, a review, a scheduler — have nothing else in common
// to write it on.
type UsageLimitStore struct {
	root      string
	productID domain.ProductID
}

func NewUsageLimitStore(root string, productID domain.ProductID) (*UsageLimitStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &UsageLimitStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *UsageLimitStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// refusals actually is.
func (s *UsageLimitStore) Path() string { return filepath.Join(s.root, "usage-limits.jsonl") }

// Record appends one refusal. It is an append rather than a rewrite for the
// reason every other log here is: a refusal is written once and never revised,
// and two processes refused at the same moment must not overwrite each other's
// account.
func (s *UsageLimitStore) Record(exhaustion UsageLimitExhaustion) error {
	if err := s.validate(exhaustion); err != nil {
		return err
	}
	encoded, err := encodeUsageLimitExhaustion(exhaustion)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedUsageLimitBytes {
		return fmt.Errorf("encoded usage limit refusal is %d bytes, limit is %d", len(encoded), maxEncodedUsageLimitBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create usage limit directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect usage limit log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open usage limit log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append usage limit refusal: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append usage limit refusal: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync usage limit log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close usage limit log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded refusal in the order it happened. A log that does
// not exist yet is a product no provider has refused, which is not a failure to
// read.
func (s *UsageLimitStore) List() ([]UsageLimitExhaustion, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open usage limit log: %w", err)
	}
	defer file.Close()

	var exhaustions []UsageLimitExhaustion
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedUsageLimitBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeUsageLimitExhaustion([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode usage limit log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode usage limit log: %w", err)
		}
		exhaustions = append(exhaustions, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read usage limit log: %w", err)
	}
	return exhaustions, nil
}

func decodeUsageLimitExhaustion(data []byte) (UsageLimitExhaustion, error) {
	var decoded UsageLimitExhaustion
	if err := json.Unmarshal(data, &decoded); err != nil {
		return UsageLimitExhaustion{}, err
	}
	if err := decoded.Validate(); err != nil {
		return UsageLimitExhaustion{}, err
	}
	return decoded, nil
}

func encodeUsageLimitExhaustion(exhaustion UsageLimitExhaustion) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(exhaustion); err != nil {
		return nil, fmt.Errorf("encode usage limit refusal: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *UsageLimitStore) validate(exhaustion UsageLimitExhaustion) error {
	if exhaustion.ProductID != s.productID {
		return fmt.Errorf("usage limit refusal product %q does not match store product %q", exhaustion.ProductID, s.productID)
	}
	return exhaustion.Validate()
}
