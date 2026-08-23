package runstate

// Where inter-role ask exchanges live: in the same operating-system state root
// as runs, conversations, and collected reports, product-scoped and beside all
// three rather than inside any of them.
//
// That placement is the durability the channel's first property needs. An
// exchange is between two roles and belongs to neither of their conversations:
// kept in the asker's record it would vanish from the answerer's account of
// itself, and kept in a run it would be settled and cleaned up while the
// question it asked was still worth reading. Kept here, it outlives both, and
// every process that can read this product can read what two of its roles said
// to each other.
//
// It is a file per exchange rather than an append-only log, because an exchange
// is revised as it goes: each round is written before it is taken and again when
// the answer is in, and the record is closed exactly once. The revision is a
// temporary file and a rename, as every other revised record here is, so a
// process that dies mid-write leaves the previous state rather than a truncated
// file nothing can read.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// ErrNoExchange reports an identifier that names nothing recorded, which is a
// plain answer rather than a failure to look.
var ErrNoExchange = exchange.ErrNoExchange

// ExchangeStore is one product's exchanges.
type ExchangeStore struct {
	root      string
	productID domain.ProductID
}

func NewExchangeStore(root string, productID domain.ProductID) (*ExchangeStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ExchangeStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "exchanges"),
		productID: productID,
	}, nil
}

func (s *ExchangeStore) Root() string { return s.root }

// Save makes one exchange durable, whether it is being opened, taken another
// round, or closed. It is a replacement rather than an append because the record
// is one thread rather than a stream of events about one: what a reader wants is
// the exchange as it now stands, and what makes that safe is that only the
// conductor writes it and only one round of one exchange is ever in flight.
func (s *ExchangeStore) Save(recorded exchange.Exchange) error {
	if err := s.validate(recorded); err != nil {
		return err
	}
	path, err := s.path(recorded.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create exchange directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".exchange-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary exchange: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary exchange: %w", err)
	}
	if err := writeJSONFile(temporary, "exchange", recorded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary exchange: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace exchange: %w", err)
	}
	return syncDirectory(s.root)
}

// Load reads one exchange by its full identifier.
func (s *ExchangeStore) Load(id string) (exchange.Exchange, error) {
	path, err := s.path(id)
	if err != nil {
		return exchange.Exchange{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return exchange.Exchange{}, fmt.Errorf("%w: %s", ErrNoExchange, id)
	}
	if err != nil {
		return exchange.Exchange{}, fmt.Errorf("open exchange: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var loaded exchange.Exchange
	if err := decoder.Decode(&loaded); err != nil {
		return exchange.Exchange{}, fmt.Errorf("decode exchange %s: %w", id, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return exchange.Exchange{}, fmt.Errorf("decode exchange %s: %w", id, err)
	}
	if loaded.ID != id {
		return exchange.Exchange{}, fmt.Errorf("exchange file %s holds exchange %s", id, loaded.ID)
	}
	if err := s.validate(loaded); err != nil {
		return exchange.Exchange{}, err
	}
	return loaded, nil
}

// List returns every recorded exchange, the ones still open first. A directory
// that does not exist yet is a product whose roles have never asked each other
// anything, which is not a failure to read.
func (s *ExchangeStore) List() ([]exchange.Exchange, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read exchange directory: %w", err)
	}
	exchanges := make([]exchange.Exchange, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		loaded, err := s.Load(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		exchanges = append(exchanges, loaded)
	}
	exchange.Sort(exchanges)
	return exchanges, nil
}

// Find resolves a reference an operator typed, exactly as a directive's is: a
// full identifier matches exactly, and anything shorter is a prefix that is only
// an answer when it names one exchange. Nobody types thirty-two hex digits, and
// an ambiguous prefix is reported as ambiguous rather than resolved to whichever
// exchange happened to sort first.
func (s *ExchangeStore) Find(reference string) (exchange.Exchange, error) {
	wanted := strings.TrimSpace(reference)
	if wanted == "" {
		return exchange.Exchange{}, errors.New("name the exchange; a listing shows what is recorded")
	}
	recorded, err := s.List()
	if err != nil {
		return exchange.Exchange{}, err
	}
	var matched []exchange.Exchange
	for _, candidate := range recorded {
		if candidate.ID == wanted {
			return candidate, nil
		}
		if strings.HasPrefix(candidate.ID, wanted) {
			matched = append(matched, candidate)
		}
	}
	switch len(matched) {
	case 0:
		return exchange.Exchange{}, fmt.Errorf("%w: %s", ErrNoExchange, wanted)
	case 1:
		return matched[0], nil
	default:
		names := make([]string, 0, len(matched))
		for _, candidate := range matched {
			names = append(names, candidate.ID)
		}
		return exchange.Exchange{}, fmt.Errorf("%q names %d exchanges: %s", wanted, len(matched), strings.Join(names, ", "))
	}
}

func (s *ExchangeStore) validate(recorded exchange.Exchange) error {
	if recorded.ProductID != s.productID {
		return fmt.Errorf("exchange product %q does not match store product %q", recorded.ProductID, s.productID)
	}
	return recorded.Validate()
}

// path names one exchange's file. The identifier is checked against its own
// pattern first, so nothing that came from outside can name a path.
func (s *ExchangeStore) path(id string) (string, error) {
	if !exchange.ValidID(id) {
		return "", fmt.Errorf("exchange id %q is invalid", id)
	}
	return filepath.Join(s.root, id+".json"), nil
}
