package runstate

// What a watch session is doing, recorded where somebody who is not at its
// terminal can read it.
//
// A session that stays open until it is told to stop has one failure mode
// nothing before it had: it goes quiet, and quiet is what both a healthy idle
// session and a dead one look like. Nothing else in the harness answers that.
// A run's record says what became of a run, and a session that is choosing
// nothing has no run to say it with; the intake hold says the operator stopped
// the choosing, and says nothing about whether anything is left to obey it.
//
// So the session says it itself, in the same shape everything else here is
// said: an append-only log per product, one entry per transition rather than
// one per poll. A session idling overnight writes one line, not one a minute,
// which is what makes the log readable and what makes the absence of a line
// mean something. The states are few on purpose — choosing, idle, braked,
// blocked, resumed, and stopped — because each one is a different thing for an
// operator to do, and a state nobody would act on differently is noise in a log
// whose value is that it is short.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// WatchSchemaVersion is 1 and has never changed.
const WatchSchemaVersion = 1

// MaxWatchReasonBytes bounds what one transition says about itself. It is the
// bound a hold's reason takes, for the same reason: a line somebody reads in the
// morning is only useful if it says what it was about, and a bounded line says
// it.
const MaxWatchReasonBytes = 4 << 10

// maxEncodedWatchBytes bounds one encoded transition, including the trailing
// newline. The writer and the reader share it, so a transition that was written
// is always one that can be read back.
const maxEncodedWatchBytes = 16 << 10

var watchSessionIDPattern = regexp.MustCompile(`^watch-[a-f0-9]{32}$`)

// WatchState is what a session is doing between one transition and the next.
type WatchState string

const (
	// WatchWatching is a session choosing work: it has read the queue and is
	// starting what the configuration leaves room for.
	WatchWatching WatchState = "watching"
	// WatchIdle is a session that found nothing to start and is polling. It is
	// the ordinary state of a drained queue and the one that most needs saying
	// out loud, because it is indistinguishable from a dead process otherwise.
	WatchIdle WatchState = "idle"
	// WatchBraked is a session choosing nothing because intake is held —
	// whether the operator held it or the session's own failure-storm brake did.
	// Which of the two is in the reason, because they need different things from
	// whoever reads it.
	WatchBraked WatchState = "braked"
	// WatchBlocked is a session that would choose work and cannot start any: the
	// machine itself refuses every run, for something like uncommitted changes
	// sitting in the primary checkout. It is its own state rather than idle
	// because the two are opposite facts about the same silence — idle is a queue
	// with nothing in it and is waiting on the product manager, blocked is a queue
	// with work in it and is waiting on whoever can put the machine right. Told
	// apart only by a session's own terminal, the second reads as the first, which
	// is a stall diagnosed by hand three times before this state existed.
	WatchBlocked WatchState = "blocked"
	// WatchResumed is a session choosing again after a brake lifted. It is its
	// own state rather than a second "watching" so that the lift is legible: a
	// hold that was placed and never lifted reads as one that is still in force.
	WatchResumed WatchState = "resumed"
	// WatchStopped is the session ending, and it is recorded whichever way it
	// ended. A session that stops is not news the way a session that dies is,
	// but a log that only ever said the two loudly enough to tell apart while
	// the process lived would leave the reader guessing afterwards.
	WatchStopped WatchState = "stopped"
)

func (s WatchState) Valid() bool {
	switch s {
	case WatchWatching, WatchIdle, WatchBraked, WatchBlocked, WatchResumed, WatchStopped:
		return true
	default:
		return false
	}
}

// WatchTransition is one moment a session changed what it was doing, and why.
// The reason is prose because what an operator does about a braked session
// depends entirely on what braked it.
type WatchTransition struct {
	SchemaVersion int              `json:"schema_version"`
	ProductID     domain.ProductID `json:"product_id"`
	// SessionID names the session rather than the process, so two sessions
	// interleaved in one log can still be read apart.
	SessionID string     `json:"session_id"`
	State     WatchState `json:"state"`
	At        time.Time  `json:"at"`
	Reason    string     `json:"reason,omitempty"`
}

func (t WatchTransition) Validate() error {
	var problems []error
	if t.SchemaVersion != WatchSchemaVersion {
		problems = append(problems, fmt.Errorf("watch transition schema version %d is not supported", t.SchemaVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(t.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if !watchSessionIDPattern.MatchString(t.SessionID) {
		problems = append(problems, errors.New("session_id is invalid"))
	}
	if !t.State.Valid() {
		problems = append(problems, fmt.Errorf("watch state %q is not one a session takes", t.State))
	}
	if t.At.IsZero() {
		problems = append(problems, errors.New("at is required"))
	}
	if len(t.Reason) > MaxWatchReasonBytes {
		problems = append(problems, fmt.Errorf("watch transition reason is %d bytes, which exceeds the %d byte bound", len(t.Reason), MaxWatchReasonBytes))
	}
	return errors.Join(problems...)
}

// NewWatchSessionID names one session of watching.
func NewWatchSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate watch session id: %w", err)
	}
	return "watch-" + hex.EncodeToString(bytes), nil
}

// WatchStore is where a session's transitions are collected, in the same
// operating-system state root as the runs and the reports and beside them
// rather than among them. It is one append-only log per product, because what
// is being watched is one product's queue and because the transitions outlive
// the process that made them: a session that died is read from the entry it
// never wrote after.
type WatchStore struct {
	root      string
	productID domain.ProductID
}

func NewWatchStore(root string, productID domain.ProductID) (*WatchStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &WatchStore{
		root:      filepath.Join(filepath.Clean(root), "products", string(productID)),
		productID: productID,
	}, nil
}

func (s *WatchStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the account of the
// session actually is.
func (s *WatchStore) Path() string { return filepath.Join(s.root, "watch.jsonl") }

// Record appends one transition. It is an append rather than a rewrite for the
// reason every other log here is: a transition is written once and never
// revised, and two sessions watching one product must not overwrite each
// other's account.
func (s *WatchStore) Record(transition WatchTransition) error {
	if err := s.validate(transition); err != nil {
		return err
	}
	encoded, err := encodeWatchTransition(transition)
	if err != nil {
		return err
	}
	if len(encoded) > maxEncodedWatchBytes {
		return fmt.Errorf("encoded watch transition is %d bytes, limit is %d", len(encoded), maxEncodedWatchBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create watch directory: %w", err)
	}
	path := s.Path()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect watch log: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open watch log: %w", err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append watch transition: %w", err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append watch transition: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync watch log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close watch log: %w", err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every recorded transition in the order it happened. A log that
// does not exist yet is a product nobody has watched, which is not a failure to
// read.
func (s *WatchStore) List() ([]WatchTransition, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open watch log: %w", err)
	}
	defer file.Close()

	var transitions []WatchTransition
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8*1024), maxEncodedWatchBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		decoded, err := decodeWatchTransition([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("decode watch log: %w", err)
		}
		if err := s.validate(decoded); err != nil {
			return nil, fmt.Errorf("decode watch log: %w", err)
		}
		transitions = append(transitions, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read watch log: %w", err)
	}
	return transitions, nil
}

// Latest is the last transition recorded, which is where a session got to. A
// product nobody has watched has none, which is reported as an absence rather
// than as a session in some default state: never having watched and having
// stopped watching are different facts.
func (s *WatchStore) Latest() (WatchTransition, bool, error) {
	transitions, err := s.List()
	if err != nil {
		return WatchTransition{}, false, err
	}
	if len(transitions) == 0 {
		return WatchTransition{}, false, nil
	}
	return transitions[len(transitions)-1], true, nil
}

func decodeWatchTransition(data []byte) (WatchTransition, error) {
	var decoded WatchTransition
	if err := json.Unmarshal(data, &decoded); err != nil {
		return WatchTransition{}, err
	}
	if err := decoded.Validate(); err != nil {
		return WatchTransition{}, err
	}
	return decoded, nil
}

func encodeWatchTransition(transition WatchTransition) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(transition); err != nil {
		return nil, fmt.Errorf("encode watch transition: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *WatchStore) validate(transition WatchTransition) error {
	if transition.ProductID != s.productID {
		return fmt.Errorf("watch transition product %q does not match store product %q", transition.ProductID, s.productID)
	}
	return transition.Validate()
}
