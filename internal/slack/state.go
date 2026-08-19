package slack

// What the sink remembers between runs: which thread each topic opened, and how
// far it has read each durable stream.
//
// Both live at the state root beside the runs, conversations, and reports they
// are read from, per product, and both are owned by the sink alone. A restart
// therefore posts into the same threads and resumes from the same place, which
// is the whole of what makes an outage a delay rather than a gap.
//
// Known limit, stated rather than solved: the thread map is per machine. Two
// collaborators' harnesses against one shared repository would open two threads
// per work item, because neither can see the other's map. That is team-mode
// coordination and belongs to the work that takes it on; the setup document says
// so, and until then one sink per product per workspace is the supported shape.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// ThreadMapSchemaVersion and CursorsSchemaVersion are versioned separately
	// from run state because they describe neither a run nor a conversation, and
	// a sink is free to be upgraded without every other record moving with it.
	ThreadMapSchemaVersion = 1
	CursorsSchemaVersion   = 1
	// maxRecordBytes bounds either file. A thread map is one line per topic and
	// a cursor set is one line per stream, so anything near this bound is a file
	// something other than the sink has written.
	maxRecordBytes = 4 << 20
)

// Store is the sink's own durable state for one product.
type Store struct {
	root string
}

// NewStore roots the sink's state under the product, beside the runs it reads.
func NewStore(root string, productID domain.ProductID) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &Store{root: filepath.Join(filepath.Clean(root), "products", string(productID), "slack")}, nil
}

func (s *Store) Root() string { return s.root }

// Lease makes this process the product's only sink, reporting whether it got
// it. Two sinks are not a redundancy: they hold separate thread maps, so they
// open separate threads and post everything twice.
func (s *Store) Lease() (*runstate.Lease, bool, error) {
	return runstate.TryLeasePath(filepath.Join(s.root, ".sink.lock"), "slack sink")
}

// Thread is one topic's thread. The channel is recorded with the timestamp
// because a thread timestamp only means anything inside the channel it was
// posted in: a project that changes channels has not moved its threads, it has
// left them behind, and the sink opens new ones rather than replying into a
// timestamp the new channel has never heard of.
type Thread struct {
	Channel  string    `json:"channel"`
	ThreadTS string    `json:"thread_ts"`
	OpenedAt time.Time `json:"opened_at"`
}

// ThreadMap is the topic-to-thread map as it is stored.
type ThreadMap struct {
	SchemaVersion int               `json:"schema_version"`
	Threads       map[string]Thread `json:"threads"`
}

// LoadThreads reads the thread map. A product that has never reported has no
// map rather than a broken one, so an absent file is an empty map.
func (s *Store) LoadThreads() (ThreadMap, error) {
	var stored ThreadMap
	found, err := readJSON(s.threadPath(), "slack thread map", &stored)
	if err != nil {
		return ThreadMap{}, err
	}
	if !found {
		return ThreadMap{SchemaVersion: ThreadMapSchemaVersion, Threads: map[string]Thread{}}, nil
	}
	if stored.SchemaVersion != ThreadMapSchemaVersion {
		return ThreadMap{}, fmt.Errorf("slack thread map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Threads == nil {
		stored.Threads = map[string]Thread{}
	}
	return stored, nil
}

// SaveThreads replaces the thread map durably.
func (s *Store) SaveThreads(threads ThreadMap) error {
	threads.SchemaVersion = ThreadMapSchemaVersion
	if threads.Threads == nil {
		threads.Threads = map[string]Thread{}
	}
	return s.write(s.threadPath(), "slack thread map", threads)
}

// Lookup reports the thread a topic already opened in this channel. The topic is
// its key, which is what the notifier puts on a message and what an inbound
// reply will be correlated back through.
func (m ThreadMap) Lookup(channel, topic string) (Thread, bool) {
	thread, found := m.Threads[topic]
	if !found || thread.Channel != channel || strings.TrimSpace(thread.ThreadTS) == "" {
		return Thread{}, false
	}
	return thread, true
}

// Record remembers the thread a topic opened.
func (m *ThreadMap) Record(topic string, thread Thread) {
	if m.Threads == nil {
		m.Threads = map[string]Thread{}
	}
	m.Threads[topic] = thread
}

// Cursor is how far the sink has read one durable stream, and the three streams
// are read three ways because they are three shapes.
//
// A log advances by position. The operator's switches are a fixed pair of facts
// about one subject, and what has been said about them is recorded by name,
// because nothing records a hold being lifted — the lift is knowable only by
// having said the hold. A run is neither: what is worth saying about it is the
// difference between two readings of its record, so the reading already reported
// is kept and the next pass is a comparison against it.
type Cursor struct {
	Position  uint64   `json:"position,omitempty"`
	Delivered []string `json:"delivered,omitempty"`
	// Reported is the run state everything already said was said about. The
	// crossings between it and the record's current reading are what has not been
	// said yet, so it is advanced only once the whole of a crossing has been
	// posted: advancing it earlier would lose the rest of it to a crash.
	Reported *runstate.State `json:"reported,omitempty"`
	// Closed marks a run that is over and owes nothing, whose reported reading is
	// dropped because nothing more can cross. Without it the sink would carry a
	// copy of every run a product ever made.
	Closed bool `json:"closed,omitempty"`
}

// Has reports whether one named milestone has already been posted.
func (c Cursor) Has(mark string) bool {
	for _, delivered := range c.Delivered {
		if delivered == mark {
			return true
		}
	}
	return false
}

// Marked reports the first delivered mark under a prefix, which is how a hold
// that was said is found again when what lifted it is only its absence.
func (c Cursor) Marked(prefix string) (string, bool) {
	for _, delivered := range c.Delivered {
		if strings.HasPrefix(delivered, prefix) {
			return delivered, true
		}
	}
	return "", false
}

// With returns the cursor including one further milestone. It is a value
// rather than a mutation so an advance is only durable once it is written.
func (c Cursor) With(mark string) Cursor {
	if c.Has(mark) {
		return c
	}
	advanced := c
	advanced.Delivered = append(append([]string(nil), c.Delivered...), mark)
	sort.Strings(advanced.Delivered)
	return advanced
}

// Without returns the cursor with one milestone forgotten. What it forgets is a
// pair that has been said in full — a hold placed and the same hold lifted —
// because keeping it would grow the cursor by a line for every afternoon
// somebody was away from the machine.
func (c Cursor) Without(mark string) Cursor {
	dropped := c
	dropped.Delivered = nil
	for _, delivered := range c.Delivered {
		if delivered != mark {
			dropped.Delivered = append(dropped.Delivered, delivered)
		}
	}
	return dropped
}

// Cursors is every stream's position as it is stored.
type Cursors struct {
	SchemaVersion int               `json:"schema_version"`
	Streams       map[string]Cursor `json:"streams"`
}

// LoadCursors reads the cursors. A sink that has never run has read nothing,
// which is not the same as having read everything: the first pass therefore
// reports the milestones a product already has. That is deliberate — a sink
// started for the first time in the middle of a working day should say what is
// going on, not stay silent until the next run starts — and the setup document
// says so, because it is the one surprise in an otherwise quiet channel.
func (s *Store) LoadCursors() (Cursors, error) {
	var stored Cursors
	found, err := readJSON(s.cursorPath(), "slack cursors", &stored)
	if err != nil {
		return Cursors{}, err
	}
	if !found {
		return Cursors{SchemaVersion: CursorsSchemaVersion, Streams: map[string]Cursor{}}, nil
	}
	if stored.SchemaVersion != CursorsSchemaVersion {
		return Cursors{}, fmt.Errorf("slack cursor schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Streams == nil {
		stored.Streams = map[string]Cursor{}
	}
	return stored, nil
}

// SaveCursors replaces the cursors durably. It is written after each post
// rather than at the end of a pass, because the cursor is what stops a message
// being sent twice and a pass that posted ten messages and then died would
// otherwise repeat all ten.
func (s *Store) SaveCursors(cursors Cursors) error {
	cursors.SchemaVersion = CursorsSchemaVersion
	if cursors.Streams == nil {
		cursors.Streams = map[string]Cursor{}
	}
	return s.write(s.cursorPath(), "slack cursors", cursors)
}

// Keep drops the cursors of streams that no longer exist, so a machine that has
// run for months does not carry a cursor for every run it ever made. It reports
// whether it dropped anything, so a sink that has nothing to forget writes
// nothing: the ordinary pass finds no news, and a state file rewritten every few
// seconds for years is churn nobody asked for.
func (c *Cursors) Keep(streams map[string]struct{}) bool {
	dropped := false
	for name := range c.Streams {
		if _, live := streams[name]; !live {
			delete(c.Streams, name)
			dropped = true
		}
	}
	return dropped
}

func (s *Store) threadPath() string { return filepath.Join(s.root, "threads.json") }
func (s *Store) cursorPath() string { return filepath.Join(s.root, "cursors.json") }

// write replaces one record atomically: a sink killed mid-write leaves the
// previous file rather than half of the new one, which for the thread map is the
// difference between reopening one thread and losing every thread it holds.
func (s *Store) write(path, label string, value any) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create the slack state directory: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxRecordBytes {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", label, len(encoded), maxRecordBytes)
	}
	temporary, err := os.CreateTemp(s.root, ".slack-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary %s: %w", label, err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", label, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync %s: %w", label, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", label, err)
	}
	return nil
}

// readJSON reads one record, reporting whether it was there at all.
func readJSON(path, label string, value any) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxRecordBytes))
	if err := decoder.Decode(value); err != nil {
		return false, fmt.Errorf("decode %s: %w", label, err)
	}
	return true, nil
}
