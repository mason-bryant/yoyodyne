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

// closureReasonCutNote says a decision's reasoning was cut to what the record
// carries, and where the whole of it is. The decision was recorded on the work
// item before the entry was closed, so there is somewhere to send a reader.
const closureReasonCutNote = "\n[cut; the work item's own notes carry the whole of this reasoning]"

// DocketStore is where the work that has stopped moving is collected, in the
// same operating-system state root as the runs and the reports and beside them
// rather than among them. It is append-only per product, for the reason the
// report log is: an entry outlives the run that produced it, and a run whose
// state is settled and whose artifacts are cleaned up leaves a stoppage that is
// still the development manager's to decide.
//
// There are two logs and not one. The first is what stopped, written where it
// stopped; the second is what was decided about it, written where somebody
// decided. Keeping them apart is what lets a decision take an entry off the
// docket without rewriting the record of the stoppage — which is a record every
// scan re-derives, so an edited one would either be undone by the next build or
// have to be defended against it.
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

// ClosurePath names the log of decisions that settled entries. It is a second
// append-only log beside the first rather than a rewrite of it, for the reason
// the first is append-only: an entry is written once as the work stops, and a
// record that is never revised is one no interrupted process can leave
// half-written. What a closure changes is what a reader is shown, and that is a
// join rather than an edit.
func (s *DocketStore) ClosurePath() string { return filepath.Join(s.root, "docket-closed.jsonl") }

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
	recorded, err := s.entries()
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
	if err := s.append(s.Path(), "docket entry", encoded); err != nil {
		return false, err
	}
	return true, nil
}

// Close records the decision that settled one docketed stoppage and reports
// whether this call is what closed it. Closing is idempotent for the reason
// docketing is: an entry is settled once, and a decision recorded twice about
// one stoppage means the same thing the second time. The first closure is the
// one that stands, because it is the decision that took the entry off the
// docket and every later reader was reading a closed entry.
//
// The entry has to be on this docket. A closure naming nothing is a record about
// a stoppage nobody can look at, and it would be indistinguishable from one whose
// key was mistyped — which is a settled entry still on the docket and a closure
// nothing ever joins.
func (s *DocketStore) Close(closure triage.Closure) (bool, error) {
	// The reasoning is a role's own prose and nothing bounds what a role writes,
	// so it is cut to what a record may carry rather than refused. A settled
	// stoppage left on the docket because the decision behind it was wordy is the
	// one failure this must not have, and the whole of the reasoning is on the work
	// item, where the decision itself was recorded.
	closure.Reason = boundRecordedText(closure.Reason, triage.MaxMessageBytes, closureReasonCutNote)
	if err := s.validateClosure(closure); err != nil {
		return false, err
	}
	entries, err := s.entries()
	if err != nil {
		return false, err
	}
	docketed := false
	for _, entry := range entries {
		if entry.Key == closure.Key {
			docketed = true
			break
		}
	}
	if !docketed {
		return false, fmt.Errorf("no docket entry keyed %s is recorded, so there is no stoppage to close", closure.Key)
	}
	closed, err := s.Closures()
	if err != nil {
		return false, err
	}
	if _, already := closed[closure.Key]; already {
		return false, nil
	}
	encoded, err := encodeDocketClosure(closure)
	if err != nil {
		return false, err
	}
	if err := s.append(s.ClosurePath(), "docket closure", encoded); err != nil {
		return false, err
	}
	return true, nil
}

// List returns every docketed entry in the order it was recorded, one per
// event: an entry whose key was already listed is the same stoppage written
// twice by two processes that raced, and the first of them is the one that
// describes when it was noticed.
//
// Each entry carries the decision that settled it, where one has been recorded.
// The join happens here rather than in each reader because there is more than
// one — the docket the development manager reads, the sweep that delivers a
// stoppage to her, and the two verbs that carry a decision out — and a reader
// that asked only the entry log would show a settled stoppage as though nobody
// had looked at it.
//
// A docket that does not exist yet is a product where nothing has stopped,
// which is not a failure to read.
func (s *DocketStore) List() ([]triage.Entry, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	closed, err := s.Closures()
	if err != nil {
		return nil, err
	}
	for index := range entries {
		if closure, found := closed[entries[index].Key]; found {
			settled := closure
			entries[index].Closed = &settled
		}
	}
	return entries, nil
}

// Closures reports the decisions that have settled entries on this docket, by
// the key of the entry each settled. A closure whose key was already read is the
// same decision written twice by two processes that raced, and the first of them
// is the one that took the entry off the docket.
func (s *DocketStore) Closures() (map[string]triage.Closure, error) {
	closed := make(map[string]triage.Closure)
	err := s.eachLine(s.ClosurePath(), "docket closures", func(line []byte) error {
		decoded, err := decodeDocketClosure(line)
		if err != nil {
			return err
		}
		if err := s.validateClosure(decoded); err != nil {
			return err
		}
		if _, already := closed[decoded.Key]; already {
			return nil
		}
		closed[decoded.Key] = decoded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return closed, nil
}

// entries is the log alone, with nothing joined onto it. It is what the writers
// read: whether a stoppage is already docketed, and whether a closure names an
// entry that exists, are both questions about the log rather than about what a
// reader would be shown.
func (s *DocketStore) entries() ([]triage.Entry, error) {
	var entries []triage.Entry
	seen := make(map[string]bool)
	err := s.eachLine(s.Path(), "docket", func(line []byte) error {
		decoded, err := decodeDocketEntry(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		if seen[decoded.Key] {
			return nil
		}
		seen[decoded.Key] = true
		entries = append(entries, decoded)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// eachLine reads one of the two logs a record at a time. A log that does not
// exist yet is a product nothing has been recorded in, which is not a failure to
// read; anything else is, because a log nobody can read must never be read as
// empty.
func (s *DocketStore) eachLine(path, what string, record func([]byte) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", what, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedDocketEntryBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := record([]byte(line)); err != nil {
			return fmt.Errorf("decode %s: %w", what, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", what, err)
	}
	return nil
}

// append adds one record to one of the two logs, durably: the bound the reader
// shares is checked first, so a record that was written is always one that can be
// read back, and the directory is synced where the log itself is new.
func (s *DocketStore) append(path, what string, encoded []byte) error {
	if len(encoded) > maxEncodedDocketEntryBytes {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", what, len(encoded), maxEncodedDocketEntryBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create docket directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect %s log: %w", what, statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log: %w", what, err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append %s: %w", what, err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append %s: %w", what, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync %s log: %w", what, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s log: %w", what, err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
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

func decodeDocketClosure(data []byte) (triage.Closure, error) {
	var decoded triage.Closure
	if err := json.Unmarshal(data, &decoded); err != nil {
		return triage.Closure{}, err
	}
	if err := decoded.Validate(); err != nil {
		return triage.Closure{}, err
	}
	return decoded, nil
}

func encodeDocketClosure(closure triage.Closure) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(closure); err != nil {
		return nil, fmt.Errorf("encode docket closure: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *DocketStore) validate(entry triage.Entry) error {
	if entry.ProductID != s.productID {
		return fmt.Errorf("docket entry product %q does not match store product %q", entry.ProductID, s.productID)
	}
	return entry.Validate()
}

func (s *DocketStore) validateClosure(closure triage.Closure) error {
	if closure.ProductID != s.productID {
		return fmt.Errorf("docket closure product %q does not match store product %q", closure.ProductID, s.productID)
	}
	return closure.Validate()
}
