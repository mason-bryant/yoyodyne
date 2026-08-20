package runstate

// Where proposed changes to the canonical artifacts are kept, and what became
// of each of them.
//
// It is durable for the reason the whole mechanism exists: a run that noticed
// the design was wrong ends, and its worktree and its state are cleaned up,
// long before anybody decides what to do about what it noticed. A proposal that
// lived in the run would be lost exactly when it mattered — the argument made,
// the run finished, and nobody left holding the question.

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

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// maxEncodedAmendmentBytes bounds one encoded record, including the trailing
// newline. The writer and the reader share it, so a record that was written is
// always one that can be read back.
const maxEncodedAmendmentBytes = 64 << 10

// AmendmentStore is the log of proposed artifact changes and the decisions on
// them, in the same operating-system state root as runs and conversations and
// beside them rather than among them. It is one append-only log per product,
// like the collected reports: a proposal outlives the run that raised it, and a
// decision outlives the proposal.
type AmendmentStore struct {
	root      string
	productID domain.ProductID
	// reading is how strictly lines are decoded, for the reason the run store
	// carries one: a reader of other processes' output takes a tolerant view.
	reading
}

func NewAmendmentStore(root string, productID domain.ProductID) (*AmendmentStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &AmendmentStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *AmendmentStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the proposals actually
// are.
func (s *AmendmentStore) Path() string { return filepath.Join(s.root, "amendments.jsonl") }

// Append records one proposed change durably.
func (s *AmendmentStore) Append(proposal amendment.Proposal) error {
	if proposal.ProductID != s.productID {
		return fmt.Errorf("proposal product %q does not match store product %q", proposal.ProductID, s.productID)
	}
	return s.write(amendment.Record{Proposal: &proposal})
}

// Decide records what became of a proposal. The log is read first, because a
// decision on a proposal nobody raised settles nothing and a second decision on
// one already settled would be a decision somebody has already acted on being
// answered again.
//
// Two processes deciding the same proposal at the same instant can still write
// two decisions, and that is deliberately not locked against: the reader takes
// the first, so the decision that was acted on is the one that stands, and the
// cost of the race is a line in the log rather than a decision changing under
// somebody. Locking here would put a lock on the operator's own commands to
// guard against a second operator who, if they existed, would be having the
// argument out loud rather than through the log.
func (s *AmendmentStore) Decide(decision amendment.Decision) error {
	records, err := s.List()
	if err != nil {
		return err
	}
	proposal, found := amendment.Find(records, decision.ProposalID)
	if !found {
		return fmt.Errorf("no proposed amendment %q was raised for this product", decision.ProposalID)
	}
	if settled, decided := amendment.DecisionOn(records, decision.ProposalID); decided {
		return fmt.Errorf("%s was already %s on %s", decision.ProposalID, settled.Verdict, settled.DecidedAt.UTC().Format("2006-01-02"))
	}
	// The authority a decision claims has to be the one the proposal was
	// addressed to. Anything else is a role deciding somebody else's document,
	// which is the boundary this whole path exists to hold.
	if decision.Authority != proposal.Owner {
		return fmt.Errorf("%s is the %s's to decide, and the decision claims the %s's authority",
			decision.ProposalID, proposal.Owner, decision.Authority)
	}
	return s.write(amendment.Record{Decision: &decision})
}

// List returns every record in the order it was written. A log that does not
// exist yet is a product nobody has proposed anything about, which is not a
// failure to read.
func (s *AmendmentStore) List() ([]amendment.Record, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open amendment log: %w", err)
	}
	defer file.Close()

	var records []amendment.Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedAmendmentBytes)
	number := 0
	for scanner.Scan() {
		number++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeAmendmentRecord([]byte(line))
		if err == nil && decoded.Proposal != nil && decoded.Proposal.ProductID != s.productID {
			err = fmt.Errorf("proposal product %q does not match store product %q",
				decoded.Proposal.ProductID, s.productID)
		}
		if err != nil {
			// A reader carries on past a line it cannot read, for the reason the
			// report log does.
			if s.readPastLine("the amendment log", number, err) {
				continue
			}
			return nil, fmt.Errorf("decode amendment log: %w", err)
		}
		records = append(records, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read amendment log: %w", err)
	}
	return records, nil
}

// write appends one validated record. It is an append rather than a rewrite for
// the same reason the event logs are: a proposal and a decision are each written
// once and never revised, and two processes writing at the same time must not
// overwrite each other's record.
func (s *AmendmentStore) write(record amendment.Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	encoded, err := encodeAmendmentRecord(record)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedAmendmentBytes {
		return fmt.Errorf("encoded amendment record is %d bytes, limit is %d", len(encoded), maxEncodedAmendmentBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create amendment directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect amendment log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open amendment log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append amendment record: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append amendment record: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync amendment log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close amendment log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

func decodeAmendmentRecord(data []byte) (amendment.Record, error) {
	var decoded amendment.Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		return amendment.Record{}, err
	}
	if err := decoded.Validate(); err != nil {
		return amendment.Record{}, err
	}
	return decoded, nil
}

func encodeAmendmentRecord(record amendment.Record) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("encode amendment record: %w", err)
	}
	return buffer.Bytes(), nil
}
