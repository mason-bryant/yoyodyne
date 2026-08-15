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
	"sort"
	"strings"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

// maxEncodedEventBytes is the complete JSONL record bound, including its
// trailing newline. AppendEvent and LoadEvents share it so the store never
// writes an event that its own reader cannot load.
const maxEncodedEventBytes = 1 << 20

type Store struct {
	root      string
	productID domain.ProductID
}

func NewStore(root string, productID domain.ProductID) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &Store{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID), "runs"),
		productID: productID,
	}, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Create(state State) error {
	if err := s.validateState(state); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.statePath(state.RunID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("run %s already exists", state.RunID)
		}
		return fmt.Errorf("create run state: %w", err)
	}
	if err := writeJSONFile(file, state); err != nil {
		return cleanupFailedCreate(file, path, err)
	}
	if err := file.Close(); err != nil {
		return cleanupFailedCreate(nil, path, fmt.Errorf("close run state: %w", err))
	}
	if err := syncDirectory(s.root); err != nil {
		return cleanupFailedCreate(nil, path, err)
	}
	return nil
}

func (s *Store) Save(state State) error {
	if err := s.validateState(state); err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create run state directory: %w", err)
	}
	path, err := s.statePath(state.RunID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("load existing run state before save: %w", err)
	}
	temporary, err := os.CreateTemp(s.root, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary run state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary run state: %w", err)
	}
	if err := writeJSONFile(temporary, state); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary run state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace run state: %w", err)
	}
	return syncDirectory(s.root)
}

func (s *Store) Load(runID string) (State, error) {
	path, err := s.statePath(runID)
	if err != nil {
		return State{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return State{}, fmt.Errorf("open run state: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode run state %s: %w", runID, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return State{}, fmt.Errorf("decode run state %s: %w", runID, err)
	}
	if err := s.validateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) Incomplete() ([]State, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run state directory: %w", err)
	}
	states := make([]State, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), ".json")
		state, err := s.Load(runID)
		if err != nil {
			return nil, fmt.Errorf("discover incomplete runs: %w", err)
		}
		if !state.Status.Terminal() {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].RunID < states[j].RunID
	})
	return states, nil
}

func (s *Store) AppendEvent(event execution.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if _, err := s.Load(event.RunID); err != nil {
		return fmt.Errorf("load run before appending event: %w", err)
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedEventBytes {
		return fmt.Errorf("encoded event is %d bytes, limit is %d", len(encoded), maxEncodedEventBytes)
	}
	path, err := s.eventPath(event.RunID)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect event log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append event: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append event: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync event log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close event log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

func (s *Store) LoadEvents(runID string) ([]execution.Event, error) {
	path, err := s.eventPath(runID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	var events []execution.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		event, err := execution.DecodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode event log for %s: %w", runID, err)
		}
		if event.RunID != runID {
			return nil, fmt.Errorf("decode event log for %s: event belongs to run %s", runID, event.RunID)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	return events, nil
}

func encodeEvent(event execution.Event) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *Store) validateState(state State) error {
	if state.ProductID != s.productID {
		return fmt.Errorf("run product %q does not match store product %q", state.ProductID, s.productID)
	}
	return state.Validate()
}

func (s *Store) statePath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".json"), nil
}

func (s *Store) eventPath(runID string) (string, error) {
	if !runIDPattern.MatchString(runID) {
		return "", errors.New("run id is invalid")
	}
	return filepath.Join(s.root, runID+".events.jsonl"), nil
}

func writeJSONFile(file *os.File, value any) error {
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode run state: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync run state: %w", err)
	}
	return nil
}

func cleanupFailedCreate(file *os.File, path string, cause error) error {
	var cleanupErrors []error
	if file != nil {
		if err := file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close failed run state: %w", err))
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove failed run state: %w", err))
	}
	if len(cleanupErrors) == 0 {
		return cause
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not supported")
		}
		return err
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open state directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}
