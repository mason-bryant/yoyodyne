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
// resumed, and stopped — because each one is a different thing for an operator
// to do, and a state nobody would act on differently is noise in a log whose
// value is that it is short.

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

const (
	// watchLeaseFile is the lock one watching session holds for as long as it
	// watches, and watchHolderFile is that session saying which one it is. They
	// sit beside the log rather than among the runs because what is singular is
	// the session, not any run it starts.
	watchLeaseFile  = ".watch.lock"
	watchHolderFile = ".watch.holder"
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
	// WatchIdle is a session that started nothing at a poll and is polling again.
	// It is the ordinary state of a drained queue and the one that most needs
	// saying out loud, because it is indistinguishable from a dead process
	// otherwise — and it is also a queue with plenty in it that this session
	// cannot start, which is why the reason says what was passed over and the two
	// fields below say what was nonetheless going and who has to act.
	WatchIdle WatchState = "idle"
	// WatchBraked is a session choosing nothing because intake is held —
	// whether the operator held it or the session's own failure-storm brake did.
	// Which of the two is in the reason, because they need different things from
	// whoever reads it.
	WatchBraked WatchState = "braked"
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
	case WatchWatching, WatchIdle, WatchBraked, WatchResumed, WatchStopped:
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
	// Build is the repository revision the session's binary was built from. It is
	// here rather than only on the transition that opened the session because a
	// reader arriving in the middle of a night reads the entry the session
	// happened to write last, and a field only the first entry carried would be a
	// field that answers nothing for the sessions that most need it answered.
	//
	// A session that stays open runs whatever it was started with, so a fix that
	// landed after it started is not in it until somebody restarts it — which is
	// invisible from every other thing the record says, because the session goes
	// on choosing work and the runs it starts go on looking ordinary. This is what
	// makes that measurable.
	//
	// It is empty where the binary recorded no revision, which is a comparison
	// nobody can make rather than a session that is current.
	Build string `json:"build,omitempty"`
	// Running is how many developer runs the session could see in flight when it
	// recorded this. It is on the transition because a session idle on one slot
	// while a run works on the other is the state that was read as the whole line
	// having stopped: the reason says what was passed over, and this says that the
	// harness is nonetheless moving. Zero is a session with nothing going, which is
	// the ordinary idle.
	Running int `json:"running,omitempty"`
	// Executor is the conversation that carries the work this session passed over,
	// where the work it passed over is carried by one. It is the marker an item
	// itself is marked with, so the role named here is the role the tracker names
	// rather than one anything derived, and it is what decides whose move follows
	// an idle poll: a queue whose only unstarted work is an architect's to carry is
	// waiting on the architect, and telling the reader it waits on an admission
	// sends them to the one person who can do nothing about it.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// Unreadable marks the poll that chose nothing because the harness could not
	// be read at all, which is the third state whose next move is nobody's to
	// make: a store that will not answer is not waiting on an admission, a
	// release, or a conversation, and it is read again until it answers or the
	// session gives up on it. Every other transition leaves it false.
	Unreadable bool `json:"unreadable,omitempty"`
	// ProviderWindow marks the poll that chose nothing because the provider is
	// refusing the harness for want of capacity, which is the fourth state whose
	// next move is nobody's to make. It travels for the reason the three above do:
	// nothing a person admits, releases, or opens shortens a usage window, so a
	// reader told to admit work would be told the one thing that cannot help.
	//
	// It is the fact that was missing on 2026-09-05. A session waited out a window
	// from 12:13Z to 13:43Z and recorded nothing about it, so every surface read
	// the ninety minutes as a queue nobody was pulling and one of them woke the
	// operator over it — while the pause was the whole of the accounting. Every
	// other transition leaves it false.
	//
	// It is a field beside the idle state rather than a state of its own, for the
	// reason Restarting below is: a state nothing recognizes fails this log's
	// validation permanently, and an unknown field is ignored.
	ProviderWindow bool `json:"provider_window,omitempty"`
	// ProviderWindowResetsAt is when the provider said that window lifts. It is
	// absent where the provider named no time, which is a different fact from a
	// wait of unknown length: the harness asks again rather than being told when.
	// It is what lets a surface say "until 13:43Z" rather than only "waiting".
	ProviderWindowResetsAt *time.Time `json:"provider_window_resets_at,omitempty"`
	// Restarting marks the one stop that is not an ending: the session is being
	// re-executed into a build deployed over it, having waited out every run it
	// started, and the process comes straight back watching the same queue. Every
	// other transition leaves it false.
	//
	// It is a field beside the state rather than a state of its own, and that is
	// deliberate. A state nothing recognizes fails this log's validation, so a
	// reader from before the field existed — a Slack sink or a `yoyo status` from
	// an older build, which is exactly what is running while a redeploy is
	// happening — would stop being able to read the log at all, permanently,
	// because the entry stays in it. An unknown field is ignored by the same
	// reader, so what an older one loses is the distinction and not the log.
	Restarting bool `json:"restarting,omitempty"`
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
	// A session recorded before the build was written down carries none, and that
	// is not a malformed entry: what it costs is the one comparison, which is
	// reported as unmakeable where it is read.
	if t.Build != "" && !buildPattern.MatchString(t.Build) {
		problems = append(problems, fmt.Errorf("watch transition build %q is not a revision", t.Build))
	}
	// A negative count of runs is not a session that saw fewer than none of them,
	// it is a caller with a bug, and a surface that printed it would be saying
	// something about the machine that nothing observed.
	if t.Running < 0 {
		problems = append(problems, fmt.Errorf("watch transition reports %d runs in flight, which is not a count", t.Running))
	}
	// A marker the harness does not recognize names a role nothing can address,
	// and the whole reason this field is here is to name somebody a reader can go
	// to. An empty marker is ordinary: it is a session whose idleness no
	// conversation accounts for.
	if t.Executor != "" && !t.Executor.Valid() {
		problems = append(problems, fmt.Errorf("watch transition executor %q is not a marker an item is carried by", t.Executor))
	}
	// Only an idle poll can be one made inside a provider's window. A session
	// marked as waiting out a window while it is watching, braked, or stopped
	// would have every surface accounting for a silence that something else is
	// causing, which is the alarm this field exists to keep honest rather than to
	// turn off.
	if t.ProviderWindow && t.State != WatchIdle {
		problems = append(problems, fmt.Errorf("a %s transition cannot be a poll made inside the provider's usage window, which is a thing only an idle one is", t.State))
	}
	// A reset time on a transition that is not waiting out a window is a moment
	// nothing is waiting for, and a surface reading it would say the harness is
	// held until a time nobody is holding it to.
	if t.ProviderWindowResetsAt != nil {
		if !t.ProviderWindow {
			problems = append(problems, errors.New("a watch transition names when the provider's usage window lifts without saying it is waiting out one"))
		}
		if t.ProviderWindowResetsAt.IsZero() {
			problems = append(problems, errors.New("the provider's usage window is present and names no moment; a provider that named none records none"))
		}
	}
	// Only a stop can be a restart. A session marked as coming back while it is
	// still watching, idle, or braked would have every reader saying a restart is
	// under way that nothing is going to make.
	if t.Restarting && t.State != WatchStopped {
		problems = append(problems, fmt.Errorf("a %s transition cannot be a restart, which is a thing only a stop is", t.State))
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

// Lease makes this process the product's only watching session, reporting
// whether it got it.
//
// Two sessions watching one product are not two workers. They read one queue and
// choose from it independently, and a run is not in the run store until it
// reserves — several steps after the item was chosen — so both can pick the same
// item inside that window, and the occupied-item bookkeeping each keeps is its
// own. Two of them briefly coexisted while the 2026-09-05 wedge was being
// cleared, which is what this refuses.
//
// The session stamps itself beside the lock as it takes it, under the lock, so a
// session refused here can say which one has it rather than only that somebody
// does. A lease that cannot be stamped is dropped rather than kept: a session
// nothing can name is exactly what the refusal exists to stop somebody meeting.
func (s *WatchStore) Lease(sessionID string) (*Lease, bool, error) {
	if !watchSessionIDPattern.MatchString(sessionID) {
		return nil, false, fmt.Errorf("watch session id %q is invalid", sessionID)
	}
	lease, held, err := TryLeasePath(filepath.Join(s.root, watchLeaseFile), "watch session")
	if err != nil || !held {
		return nil, held, err
	}
	holder := filepath.Join(s.root, watchHolderFile)
	if err := s.stampHolder(holder, sessionID); err != nil {
		return nil, false, errors.Join(err, lease.Release())
	}
	lease.holder = holder
	return lease, true, nil
}

// WatchHolder is the session holding this product's watch, as it stamped itself
// when it took the lease. It carries the session identifier the log and `yoyo
// status` also carry, so a refusal and every other surface name one session
// rather than two, and the process, which is what somebody stops when stopping
// it is what they want.
type WatchHolder struct {
	SessionID string    `json:"session_id"`
	PID       int       `json:"pid"`
	HeldAt    time.Time `json:"held_at"`
}

// Holder is the session that stamped itself as watching this product, reported
// as an absence where none has rather than as a failure. It says nothing about
// whether that session is still alive — the lease is what decides that, and this
// is only how the holder of one is named — so it is read after a lease was
// refused rather than instead of trying to take one.
func (s *WatchStore) Holder() (WatchHolder, bool, error) {
	file, err := os.Open(filepath.Join(s.root, watchHolderFile))
	if errors.Is(err, os.ErrNotExist) {
		return WatchHolder{}, false, nil
	}
	if err != nil {
		return WatchHolder{}, false, fmt.Errorf("open the watch holder: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxEncodedStateBytes))
	decoder.DisallowUnknownFields()
	var holder WatchHolder
	if err := decoder.Decode(&holder); err != nil {
		return WatchHolder{}, false, fmt.Errorf("decode the watch holder: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return WatchHolder{}, false, fmt.Errorf("decode the watch holder: %w", err)
	}
	if holder.PID <= 0 {
		return WatchHolder{}, false, errors.New("the watch holder names no process")
	}
	return holder, true, nil
}

// stampHolder writes this process's stamp for the watch it now holds. It is
// replaced by rename rather than written in place, so a reader sees the whole of
// one stamp or none of it and never half of one.
func (s *WatchStore) stampHolder(path, sessionID string) error {
	temporary, err := os.CreateTemp(s.root, ".watch-holder-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary watch holder: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary watch holder: %w", err)
	}
	holder := WatchHolder{SessionID: sessionID, PID: os.Getpid(), HeldAt: time.Now().UTC()}
	if err := writeJSONFile(temporary, "watch holder", holder); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary watch holder: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace watch holder: %w", err)
	}
	return syncDirectory(s.root)
}

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
