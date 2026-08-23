package slack

// What the sink remembers between runs: which thread each topic opened, how far
// it has read each durable stream, and which directives were said to it in a
// thread rather than at a terminal.
//
// All three live at the state root beside the runs, conversations, and reports
// they are read from, per product, and each is owned by the sink alone. A
// restart therefore posts into the same threads, resumes from the same place,
// and still knows who asked for what, which is the whole of what makes an outage
// a delay rather than a gap.
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
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const (
	// ThreadMapSchemaVersion and CursorsSchemaVersion are versioned separately
	// from run state because they describe neither a run nor a conversation, and
	// a sink is free to be upgraded without every other record moving with it.
	ThreadMapSchemaVersion = 1
	CursorsSchemaVersion   = 1
	SteerMapSchemaVersion  = 1
	// maxRecordBytes bounds any of them. A thread map is one line per topic, a
	// cursor set one line per stream, and a steer map one line per directive
	// somebody typed into a thread, so anything near this bound is a file
	// something other than the sink has written.
	maxRecordBytes = 4 << 20
)

// Store is the sink's own durable state for one product.
type Store struct {
	root    string
	product domain.ProductID
}

// NewStore roots the sink's state under the product, beside the runs it reads.
func NewStore(root string, productID domain.ProductID) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &Store{
		root:    filepath.Join(filepath.Clean(root), "products", string(productID), "slack"),
		product: productID,
	}, nil
}

func (s *Store) Root() string { return s.root }

// Product is the product this store's state belongs to, and so the product the
// sink built on it reports on. It is kept rather than re-derived from the path
// because it is what every speaker's name is qualified by: a product read back
// out of a directory name would be the same id spelled a second way, and two
// spellings of one thing are two things that can disagree.
func (s *Store) Product() domain.ProductID { return s.product }

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
	// Status is the status this thread's opening message was last marked with. It
	// is remembered for one reason — so a pass that finds nothing has moved does
	// nothing — and it is deliberately not treated as evidence of which symbol is
	// on the message: a sink killed between the workspace taking a mark and this
	// being written would make it a lie, so what replaces a mark sweeps the whole
	// vocabulary rather than trusting this to name what is there.
	//
	// It is an added field rather than a new schema version because a map written
	// before there were status marks is a correct map of unmarked threads: absent
	// reads as nothing to remove, and the first status the item reaches marks it.
	// Refusing to load one would cost every existing product its threads over a
	// mark that had not been invented when they were opened.
	Status notify.Status `json:"status,omitempty"`
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

// Steer is one directive somebody said into a thread: who said it, and where
// they said it. Neither is anywhere else. The durable directive record is the
// product's and is the same record whichever way a directive arrived, so a
// Slack member id and a thread key have no business in it — they are facts
// about this workspace rather than about what the operator directed.
//
// What they are for is the other half of an acknowledgment: when something
// later becomes of that directive, the person who asked for it is told where
// they asked, by name, and the message they asked in stops saying the directive
// is still open.
type Steer struct {
	// Member is the Slack member id of the human whose reply recorded it, which
	// is what a message tags to reach them.
	Member string `json:"member"`
	// Topic is the key of the thread it was said in, so what becomes of it is
	// said in the same narrative rather than in a thread nobody was reading.
	Topic string `json:"topic"`
	// Message is the timestamp of the reply that asked for it, which is the one
	// message in the channel that carries where this directive stands. It wears
	// the thinking face from the moment it arrived until the directive is
	// settled, so the mark can only move if the message that asked is remembered
	// — the acknowledgment posted under it is a different message and says a
	// different thing.
	//
	// It is an added field rather than a new schema version, for the reason a
	// thread's status is: a map written before there were receipts is a correct
	// map of directives whose asks nothing can mark, and refusing to load one
	// would lose the origin of every directive still in flight.
	Message string `json:"message,omitempty"`
	// RecordedAt is when the directive was received, kept so a reader of this
	// file can tell a live steer from one left over from months ago.
	RecordedAt time.Time `json:"recorded_at"`
	// Said marks a directive whose outcome the inbound half has already put in
	// the thread, which is exactly the one it settled itself. Without it the
	// delivery pass would say the same settlement again on its next reading: the
	// two halves post from different goroutines and remember what they have said
	// in different places, and this is the connection's half of that memory.
	Said bool `json:"said,omitempty"`
}

// SteerMap is the directive-to-reply map as it is stored.
//
// It is written by the connection alone — the goroutine that reads replies — and
// read by the delivery pass, which is the same division the thread map is held
// to the other way round. Nothing prunes it: it grows by one line per directive
// somebody typed into a thread, which is a human typing rather than a machine
// recording, and losing the origin of a directive that is still unresolved would
// lose exactly the case this exists for.
type SteerMap struct {
	SchemaVersion int              `json:"schema_version"`
	Steers        map[string]Steer `json:"steers"`
}

// LoadSteers reads the steer map. A product nobody has steered from a thread has
// no map rather than a broken one, so an absent file is an empty map.
func (s *Store) LoadSteers() (SteerMap, error) {
	var stored SteerMap
	found, err := readJSON(s.steerPath(), "slack steer map", &stored)
	if err != nil {
		return SteerMap{}, err
	}
	if !found {
		return SteerMap{SchemaVersion: SteerMapSchemaVersion, Steers: map[string]Steer{}}, nil
	}
	if stored.SchemaVersion != SteerMapSchemaVersion {
		return SteerMap{}, fmt.Errorf("slack steer map schema version %d is not supported", stored.SchemaVersion)
	}
	if stored.Steers == nil {
		stored.Steers = map[string]Steer{}
	}
	return stored, nil
}

// SaveSteers replaces the steer map durably.
func (s *Store) SaveSteers(steers SteerMap) error {
	steers.SchemaVersion = SteerMapSchemaVersion
	if steers.Steers == nil {
		steers.Steers = map[string]Steer{}
	}
	return s.write(s.steerPath(), "slack steer map", steers)
}

// Lookup reports where one directive was said, and by whom. A directive nobody
// said in a thread is not in the map, which is every directive recorded at a
// terminal.
func (m SteerMap) Lookup(directiveID string) (Steer, bool) {
	steer, found := m.Steers[directiveID]
	if !found || strings.TrimSpace(steer.Topic) == "" {
		return Steer{}, false
	}
	return steer, true
}

// Record remembers where one directive was said.
func (m *SteerMap) Record(directiveID string, steer Steer) {
	if m.Steers == nil {
		m.Steers = map[string]Steer{}
	}
	m.Steers[directiveID] = steer
}

// Cursor is how far the sink has read one durable stream, and the streams are
// read four ways because they are four shapes.
//
// A log advances by position. The operator's switches are a fixed pair of facts
// about one subject, and what has been said about them is recorded by name,
// because nothing records a hold being lifted — the lift is knowable only by
// having said the hold. A run is neither: what is worth saying about it is the
// difference between two readings of its record, so the reading already reported
// is kept and the next pass is a comparison against it. And the line's own state
// is none of the three: it is not a record at all but a condition derived from
// several, said again while it stands rather than once when it began, so what is
// kept is which state is standing and when it was last said.
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
	// Standing and Said are the fourth shape, and they are the only one that is a
	// state rather than a history: what the line is currently stopped by, and when
	// the clock on saying it again was last set — which is the moment the state was
	// first seen as well as the moment it was last said, because the first sighting
	// is what starts the interval rather than something to announce.
	//
	// A state that clears forgets both, so the next one to stand is armed afresh
	// rather than inheriting an interval that has already run out.
	Standing string    `json:"standing,omitempty"`
	Said     time.Time `json:"said,omitempty"`
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

// Cursors is every stream's position as it is stored, and the moment reading
// this product began.
type Cursors struct {
	SchemaVersion int               `json:"schema_version"`
	Streams       map[string]Cursor `json:"streams"`
	// Since is the watermark: the moment somebody first pointed a sink at this
	// product. What the product recorded before it is history nobody turned
	// reporting on to read, and a channel opened today would rather not have a
	// month of finished work arriving at once.
	//
	// It is durable and it is written once, which is the whole of its value. A
	// watermark taken freshly on every process start would move forward past
	// every outage, so a report filed while the sink was down would be older than
	// the restart and be read past as history — which is the one record an
	// operator most needs when they come back. Fixed once, downtime is a gap the
	// sink reads across rather than a gap in what it says.
	Since time.Time `json:"since,omitempty"`
}

// LoadCursors reads the cursors. A sink that has never run has read nothing,
// which is not the same as having read everything: the first pass therefore
// reports the milestones a product already has in flight. That is deliberate — a
// sink started for the first time in the middle of a working day should say what
// is going on, not stay silent until the next run starts — and the setup document
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
func (s *Store) steerPath() string  { return filepath.Join(s.root, "steers.json") }

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
