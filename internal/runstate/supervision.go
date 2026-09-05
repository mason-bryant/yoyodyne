package runstate

// Where the management loop's durable records live: the typed requests one role
// puts to another, and the readiness judgments the three management roles make
// about admitted work. They sit in the same operating-system state root as runs,
// conversations, exchanges, and the docket, product-scoped and beside all of
// them rather than inside any of them.
//
// That placement is what makes restart mean anything. A request is written
// before the role it names is invoked and revised as each attempt opens and
// closes, so the process carrying it can die at any point and leave a record the
// next process can read: an attempt nobody holds is delivered again, and an
// answer already written is settled rather than paid for twice. Kept inside a run
// the record would be cleaned up while the question was still open; kept inside
// the asking role's conversation it would vanish from the answering role's
// account of itself.
//
// A file per record, revised by a temporary file and a rename, exactly as an
// exchange is: a request is one thing that changes as it goes rather than a
// stream of events about one, and a process that dies mid-write leaves the
// previous state rather than a truncated file nothing can read.
//
// Every write goes through the shared safe-write primitive with this product's
// supervision directory declared as the root it is confined to. Checking the
// identifier against its own pattern is a lexical check, and the
// repository-writes-are-physically-confined invariant records lexical checks as
// proven insufficient: a directory under the state root can be replaced by a
// symlink at any time, and a path string never proves where the bytes land.
// The primitive resolves every component against the filesystem immediately
// before it writes and refuses anything that leaves.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/supervision"
)

// ErrNoRequest and ErrNoReadiness report an identifier that names nothing
// recorded, which is a plain answer rather than a failure to look.
var (
	ErrNoRequest   = errors.New("no such request")
	ErrNoReadiness = errors.New("no such readiness judgment")
)

// SupervisionStore is one product's management-loop records.
type SupervisionStore struct {
	root      string
	productID domain.ProductID
}

func NewSupervisionStore(root string, productID domain.ProductID) (*SupervisionStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &SupervisionStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "supervision"),
		productID: productID,
	}, nil
}

func (s *SupervisionStore) Root() string { return s.root }

// RequestsRoot and ReadinessRoot name the two directories, so a failure can say
// where the records actually are.
func (s *SupervisionStore) RequestsRoot() string  { return filepath.Join(s.root, "requests") }
func (s *SupervisionStore) ReadinessRoot() string { return filepath.Join(s.root, "readiness") }

// SaveRequest makes one request durable, whether it is being opened, attempted
// again, answered, or settled. It is a replacement rather than an append because
// the record is one request as it now stands, and what makes that safe is that
// only the process holding the request's lease writes it.
func (s *SupervisionStore) SaveRequest(recorded supervision.Request) error {
	if err := s.validateRequest(recorded); err != nil {
		return err
	}
	if !supervision.ValidRequestID(recorded.ID) {
		return fmt.Errorf("request id %q is invalid", recorded.ID)
	}
	return s.write("requests/"+recorded.ID+".json", "request", recorded)
}

// LoadRequest reads one request by its full identifier.
func (s *SupervisionStore) LoadRequest(id string) (supervision.Request, error) {
	path, err := s.requestPath(id)
	if err != nil {
		return supervision.Request{}, err
	}
	var loaded supervision.Request
	if err := readRecord(path, "request", id, ErrNoRequest, &loaded); err != nil {
		return supervision.Request{}, err
	}
	if loaded.ID != id {
		return supervision.Request{}, fmt.Errorf("request file %s holds request %s", id, loaded.ID)
	}
	if err := s.validateRequest(loaded); err != nil {
		return supervision.Request{}, err
	}
	return loaded, nil
}

// Requests returns every recorded request, oldest first. A directory that does
// not exist yet is a product whose roles have never asked each other anything,
// which is not a failure to read.
//
// One record it cannot read fails the whole listing, for the reason the exchange
// listing does: this answers "what is the loop carrying", and an answer quietly
// missing a request is worse than no answer, because the request it left out is
// exactly the one somebody would have gone looking for.
func (s *SupervisionStore) Requests() ([]supervision.Request, error) {
	ids, err := recordIDs(s.RequestsRoot(), "request")
	if err != nil || ids == nil {
		return nil, err
	}
	requests := make([]supervision.Request, 0, len(ids))
	for _, id := range ids {
		loaded, err := s.LoadRequest(id)
		if err != nil {
			return nil, err
		}
		requests = append(requests, loaded)
	}
	supervision.SortRequests(requests)
	return requests, nil
}

// SaveReadiness makes one judgment durable. Judgments are not revised: a role
// that judges an item again records a new one, and the store keeps both, so what
// was judged when stays readable. Which of them stands is supervision.Current's
// answer rather than a matter of what is on disk.
func (s *SupervisionStore) SaveReadiness(recorded supervision.Readiness) error {
	if err := s.validateReadiness(recorded); err != nil {
		return err
	}
	if !supervision.ValidReadinessID(recorded.ID) {
		return fmt.Errorf("readiness id %q is invalid", recorded.ID)
	}
	return s.write("readiness/"+recorded.ID+".json", "readiness", recorded)
}

// LoadReadiness reads one judgment by its full identifier.
func (s *SupervisionStore) LoadReadiness(id string) (supervision.Readiness, error) {
	path, err := s.readinessPath(id)
	if err != nil {
		return supervision.Readiness{}, err
	}
	var loaded supervision.Readiness
	if err := readRecord(path, "readiness", id, ErrNoReadiness, &loaded); err != nil {
		return supervision.Readiness{}, err
	}
	if loaded.ID != id {
		return supervision.Readiness{}, fmt.Errorf("readiness file %s holds judgment %s", id, loaded.ID)
	}
	if err := s.validateReadiness(loaded); err != nil {
		return supervision.Readiness{}, err
	}
	return loaded, nil
}

// Readiness returns every recorded judgment, superseded ones included, in the
// order they are read.
func (s *SupervisionStore) Readiness() ([]supervision.Readiness, error) {
	ids, err := recordIDs(s.ReadinessRoot(), "readiness")
	if err != nil || ids == nil {
		return nil, err
	}
	records := make([]supervision.Readiness, 0, len(ids))
	for _, id := range ids {
		loaded, err := s.LoadReadiness(id)
		if err != nil {
			return nil, err
		}
		records = append(records, loaded)
	}
	supervision.SortReadiness(records)
	return records, nil
}

// HoldRequest takes the exclusive lease on one request without waiting,
// reporting whether it got it.
//
// This is what tells a delivery in flight from one whose process is gone, and
// it is an advisory file lock for the reason every other lease here is: the
// operating system drops it when its holder exits, so a killed harness leaves
// nothing for anybody to clear and the next pass simply finds the request free.
// Asking whether somebody holds it and taking it afterwards would leave a window
// between the two, so taking it is the question.
func (s *SupervisionStore) HoldRequest(requestID string) (*Lease, bool, error) {
	if !supervision.ValidRequestID(requestID) {
		return nil, false, fmt.Errorf("request id %q is invalid", requestID)
	}
	return TryLeasePath(filepath.Join(s.root, "leases", requestID+".lease"), "request "+requestID)
}

func (s *SupervisionStore) validateRequest(recorded supervision.Request) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("request product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	return recorded.Validate()
}

func (s *SupervisionStore) validateReadiness(recorded supervision.Readiness) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("readiness product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	return recorded.Validate()
}

// The identifier is checked against its own pattern first, so nothing that came
// from outside can name a path.
func (s *SupervisionStore) requestPath(id string) (string, error) {
	if !supervision.ValidRequestID(id) {
		return "", fmt.Errorf("request id %q is invalid", id)
	}
	return filepath.Join(s.RequestsRoot(), id+".json"), nil
}

func (s *SupervisionStore) readinessPath(id string) (string, error) {
	if !supervision.ValidReadinessID(id) {
		return "", fmt.Errorf("readiness id %q is invalid", id)
	}
	return filepath.Join(s.ReadinessRoot(), id+".json"), nil
}

// write puts one record where the supervision-relative path names, through the
// primitive that decides confinement against the filesystem rather than against
// the path string.
//
// The root has to exist before it can be resolved, so it is created here with
// the permissions the rest of the state root carries. That is the one directory
// this makes itself, and it is the same thing every other caller of the
// primitive does with the root it declares. What it buys is that the records
// below it are owner-only whatever permissions the primitive gives the files
// themselves, since these are runtime state rather than documents committed
// beside the code.
func (s *SupervisionStore) write(relative, label string, value any) error {
	encoded, err := encodeRecord(label, value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create the %s directory: %w", label, err)
	}
	root, err := repowrite.NewRoot(s.root)
	if err != nil {
		return fmt.Errorf("resolve the %s root: %w", label, err)
	}
	written, err := root.WriteFile(relative, encoded)
	if err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	return syncDirectory(filepath.Dir(written))
}

// encodeRecord renders one record as the bytes that will be written, and holds
// it to the same size limit every other record in this store is held to: what
// was written has to be readable back, and the reader stops at that limit.
func encodeRecord(label string, value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode %s: %w", label, err)
	}
	if buffer.Len() > maxEncodedStateBytes {
		return nil, fmt.Errorf("encoded %s is %d bytes, limit is %d", label, buffer.Len(), maxEncodedStateBytes)
	}
	return buffer.Bytes(), nil
}

// readRecord decodes one record strictly: an unknown field or trailing content
// is a record this build cannot claim to understand, and reading it loosely
// would mean acting on a request whose terms have changed under us.
func readRecord(path, label, id string, absent error, into any) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", absent, id)
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("decode %s %s: %w", label, id, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode %s %s: %w", label, id, err)
	}
	return nil
}

// recordIDs names every record in one directory. It answers nil for a directory
// that does not exist yet, which is how a product nothing has been recorded for
// is told from one whose records were all removed.
func recordIDs(directory, label string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s directory: %w", label, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return ids, nil
}
