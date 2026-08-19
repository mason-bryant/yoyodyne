package runstate

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

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// maxEncodedDocketEntryBytes bounds one encoded entry, including the trailing
// newline. The writer and the reader share it, so an entry that was written is
// always one that can be read back.
const maxEncodedDocketEntryBytes = 128 << 10

// DocketStore is where the work that has stopped moving is collected, in the
// same operating-system state root as the runs and the reports and beside them
// rather than among them. It is one append-only log per product, for the reason
// the report log is: an entry outlives the run that produced it, and a run whose
// state is settled and whose artifacts are cleaned up leaves a stoppage that is
// still the development manager's to decide.
type DocketStore struct {
	root      string
	productID domain.ProductID
}

func NewDocketStore(root string, productID domain.ProductID) (*DocketStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &DocketStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *DocketStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the docket actually is.
func (s *DocketStore) Path() string { return filepath.Join(s.root, "docket.jsonl") }

// RecordOnce records one entry and reports whether this call is what created
// it. Docketing is idempotent because the thing it describes is one event: a
// run stops once, and the process that stopped it and the sweep that settles it
// afterwards must between them produce one entry rather than two. So the entry's
// key decides, and an entry whose key is already in the log changes nothing and
// is not an error — asking twice about one stoppage means the same thing the
// second time.
//
// Two processes appending at the same instant can still both see an absent key
// and both write, which is why List collapses duplicates rather than trusting
// this check alone. Taking a lock here instead would put the docket behind a
// lock every conversation open and every sweep contends for, to prevent a
// duplicate that costs a reader one repeated paragraph.
func (s *DocketStore) RecordOnce(entry triage.Entry) (bool, error) {
	if err := s.validate(entry); err != nil {
		return false, err
	}
	recorded, err := s.List()
	if err != nil {
		return false, err
	}
	for _, existing := range recorded {
		if existing.Key == entry.Key {
			return false, nil
		}
	}
	encoded, err := encodeDocketEntry(entry)
	if err != nil {
		return false, err
	}
	if len(encoded) > maxEncodedDocketEntryBytes {
		return false, fmt.Errorf("encoded docket entry is %d bytes, limit is %d", len(encoded), maxEncodedDocketEntryBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return false, fmt.Errorf("create docket directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("inspect docket: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false, fmt.Errorf("open docket: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return false, fmt.Errorf("append docket entry: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return false, fmt.Errorf("append docket entry: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return false, fmt.Errorf("sync docket: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close docket: %w", err)
	}
	if created {
		return true, syncDirectory(s.root)
	}
	return true, nil
}

// List returns every docketed entry in the order it was recorded, one per
// event: an entry whose key was already listed is the same stoppage written
// twice by two processes that raced, and the first of them is the one that
// describes when it was noticed.
//
// A docket that does not exist yet is a product where nothing has stopped,
// which is not a failure to read.
func (s *DocketStore) List() ([]triage.Entry, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open docket: %w", err)
	}
	defer file.Close()

	var entries []triage.Entry
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedDocketEntryBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeDocketEntry([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode docket: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode docket: %w", err)
		}
		if seen[decoded.Key] {
			continue
		}
		seen[decoded.Key] = true
		entries = append(entries, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read docket: %w", err)
	}
	return entries, nil
}

func decodeDocketEntry(data []byte) (triage.Entry, error) {
	var decoded triage.Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		return triage.Entry{}, err
	}
	if err := decoded.Validate(); err != nil {
		return triage.Entry{}, err
	}
	return decoded, nil
}

func encodeDocketEntry(entry triage.Entry) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entry); err != nil {
		return nil, fmt.Errorf("encode docket entry: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *DocketStore) validate(entry triage.Entry) error {
	if entry.ProductID != s.productID {
		return fmt.Errorf("docket entry product %q does not match store product %q", entry.ProductID, s.productID)
	}
	return entry.Validate()
}
