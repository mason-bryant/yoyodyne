package runstate

// Reading the event streams the harness is writing right now.
//
// A run, a conversation, and a branch review each record a normalized event
// stream, and each keeps it beside its own records rather than among the
// others'. It is the same question being asked of all three — is this alive,
// what is it doing, and what did it cost — so this reads all three as one
// collection and nothing that asks it has to say which kind it meant.
//
// It is read-only in the strongest sense: no lease is taken, nothing is
// adopted, and nothing is written, so a stream another process is appending to
// is read exactly as a finished one is. That is what lets an operator watch a
// live run without the watching being an act on it.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// eventLogSuffix is what names an event log among the records beside it. The
// branch review store keeps its shared verdict log in the same directory, and
// that log is not an event stream, so the suffix rather than the extension is
// what selects one.
const eventLogSuffix = ".events.jsonl"

// StreamKind is which of the three records a followable event stream belongs
// to. They are told apart by the id the stream is named for, which is the same
// thing their records are named for.
type StreamKind string

const (
	StreamRun          StreamKind = "run"
	StreamConversation StreamKind = "conversation"
	StreamReview       StreamKind = "review"
)

// EveryStreamKind is all three, which is what a query that names none covers:
// asking whether anything is alive should never have required saying which kind
// of alive was meant.
var EveryStreamKind = []StreamKind{StreamRun, StreamConversation, StreamReview}

// The statuses derived for the two kinds that keep no status of their own. A
// run records its own and is reported by it; these say what an operator is
// actually asking of the other two.
const (
	// ConversationAnswering is an agent working on a turn, and
	// ConversationWaiting is between turns, with the operator holding the ball.
	ConversationAnswering = "answering"
	ConversationWaiting   = "waiting"
	// ConversationEnded is a conversation the role has since replaced, in which
	// nothing will happen again.
	ConversationEnded = "ended"
	// ReviewInProgress is a verdict still being made and ReviewFinished is one
	// that has been.
	ReviewInProgress = "reviewing"
	ReviewFinished   = "reviewed"
	// StreamStatusUnknown is a stream whose record could not be read. It is
	// stated rather than guessed at: a run whose state file is gone is not a run
	// in some particular state.
	StreamStatusUnknown = "unknown"
)

// Stream is one recorded event stream, named and placed well enough to follow,
// list, or price without opening it again to work out which kind it is.
type Stream struct {
	ID     string     `json:"id"`
	Kind   StreamKind `json:"kind"`
	Status string     `json:"status"`
	// StartedAt is when the work behind the stream opened. A run records the
	// moment in its state file; a conversation's and a branch review's is the
	// timestamp of their first event, which is the same moment and needs no
	// state file to find. It is zero on a stream whose moment could not be read.
	StartedAt time.Time `json:"started_at,omitzero"`
	Events    int       `json:"events"`
	// Path is the event log itself, which is what following one opens.
	Path string `json:"path"`
	// Updated is when the log was last appended to. It is what "newest" means
	// here — the stream something happened in most recently, which is the one an
	// operator who named nothing meant.
	Updated time.Time `json:"updated"`
}

// Dated reports a stream whose opening moment could be read. One that could not
// still cost money and is still followable, so it is carried rather than
// dropped.
func (s Stream) Dated() bool { return !s.StartedAt.IsZero() }

// StreamStore reads the event streams of all three kinds under one product. It
// composes the three stores that own them rather than reaching into their
// directories, so a layout only one of them knows about stays that store's.
type StreamStore struct {
	runs          *Store
	conversations *ConversationStore
	reviews       *BranchReviewStore
}

func NewStreamStore(root string, productID domain.ProductID) (*StreamStore, error) {
	runs, err := NewStore(root, productID)
	if err != nil {
		return nil, err
	}
	conversations, err := NewConversationStore(root, productID)
	if err != nil {
		return nil, err
	}
	reviews, err := NewBranchReviewStore(root, productID)
	if err != nil {
		return nil, err
	}
	return &StreamStore{runs: runs, conversations: conversations, reviews: reviews}, nil
}

// Root is the product's own directory, which is where all three kinds of stream
// live. An empty answer names it, so "nothing is recorded" can be told apart
// from "nothing is recorded here": the state root is resolved from the
// environment, and an operator whose environment points somewhere they did not
// expect is reading a true answer to the wrong question.
func (s *StreamStore) Root() string { return filepath.Dir(s.runs.Root()) }

// StreamQuery selects which recorded streams an answer is about.
type StreamQuery struct {
	// Kinds narrows the answer to some of the three. Empty covers all of them.
	Kinds []StreamKind
	// Match keeps only the streams whose id contains it, which is what makes a
	// unique id prefix enough to name one.
	Match string
	// Limit bounds how many are returned, newest first. Zero returns all of them.
	Limit int
}

func (q StreamQuery) kinds() []StreamKind {
	if len(q.Kinds) == 0 {
		return EveryStreamKind
	}
	return q.Kinds
}

// List reports the selected streams, newest first. A product that has only ever
// chatted has no runs directory, one that has only ever run has no
// conversations directory, and one whose branches have never been reviewed has
// no branch-reviews directory; any one of those on its own is a product with
// streams rather than an error, so an absent directory contributes nothing.
func (s *StreamStore) List(query StreamQuery) ([]Stream, error) {
	var current map[string]bool
	var streams []Stream
	for _, kind := range query.kinds() {
		if kind == StreamConversation && current == nil {
			// Which conversations are still the role's is one fact about the whole
			// directory rather than one per stream, so it is read once here.
			found, err := s.currentConversations()
			if err != nil {
				return nil, err
			}
			current = found
		}
		found, err := s.listKind(kind, query.Match, current)
		if err != nil {
			return nil, err
		}
		streams = append(streams, found...)
	}
	sort.Slice(streams, func(i, j int) bool {
		if !streams[i].Updated.Equal(streams[j].Updated) {
			return streams[i].Updated.After(streams[j].Updated)
		}
		return streams[i].ID < streams[j].ID
	})
	if query.Limit > 0 && len(streams) > query.Limit {
		streams = streams[:query.Limit]
	}
	return streams, nil
}

// Find is the one stream a query names, newest first so an ambiguous prefix
// lands on the one most likely meant. A query matching nothing is reported as
// nothing found rather than as a failure: which of the two it is belongs to the
// caller, who knows whether the operator named something.
func (s *StreamStore) Find(query StreamQuery) (Stream, bool, error) {
	query.Limit = 1
	streams, err := s.List(query)
	if err != nil || len(streams) == 0 {
		return Stream{}, false, err
	}
	return streams[0], true, nil
}

func (s *StreamStore) root(kind StreamKind) string {
	switch kind {
	case StreamConversation:
		return s.conversations.Root()
	case StreamReview:
		return s.reviews.Root()
	default:
		return s.runs.Root()
	}
}

func (s *StreamStore) listKind(kind StreamKind, match string, current map[string]bool) ([]Stream, error) {
	root := s.root(kind)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s records: %w", kind, err)
	}
	var streams []Stream
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, eventLogSuffix) {
			continue
		}
		id := strings.TrimSuffix(name, eventLogSuffix)
		if match != "" && !strings.Contains(id, match) {
			continue
		}
		stream, err := s.describe(kind, id, filepath.Join(root, name), current)
		if err != nil {
			return nil, err
		}
		streams = append(streams, stream)
	}
	return streams, nil
}

// describe reads what a listing says about one stream. The log is read once for
// everything that comes out of it — how many events it holds, when the first one
// was, and for the two kinds that keep no status of their own, what the stream
// is doing — because a listing asks all of that of every row.
func (s *StreamStore) describe(kind StreamKind, id, path string, current map[string]bool) (Stream, error) {
	stream := Stream{ID: id, Kind: kind, Path: path, Status: StreamStatusUnknown}
	info, err := os.Stat(path)
	if err == nil {
		stream.Updated = info.ModTime()
	} else if !errors.Is(err, os.ErrNotExist) {
		return Stream{}, fmt.Errorf("inspect event log: %w", err)
	}
	scanned, err := scanStreamLog(path)
	if err != nil {
		return Stream{}, err
	}
	stream.Events = scanned.events
	stream.StartedAt = scanned.first
	switch kind {
	case StreamRun:
		// A run keeps its own status and its own opening moment, and both are
		// authoritative over anything the log could be read to imply.
		state, err := s.runs.Load(id)
		if err == nil {
			stream.Status = string(state.Status)
			stream.StartedAt = state.StartedAt
		}
	case StreamConversation:
		stream.Status = conversationStatus(id, scanned, current)
	case StreamReview:
		stream.Status = ReviewInProgress
		if scanned.reviewed {
			stream.Status = ReviewFinished
		}
	}
	return stream, nil
}

// conversationStatus says what a conversation is doing. A conversation has no
// state file of its own: the role's record names the conversation that role is
// in now, so every other log in the directory belonged to one that has since
// been replaced and nothing will happen in it again. One that is still the
// role's is being answered when its last turn opened without closing, and is
// waiting for the operator otherwise.
func conversationStatus(id string, scanned streamScan, current map[string]bool) string {
	if !current[id] {
		return ConversationEnded
	}
	if scanned.answering {
		return ConversationAnswering
	}
	return ConversationWaiting
}

// currentConversations is which conversations the roles are in now, read as the
// one field that answers it rather than through the whole record. Loading the
// records would refuse the question over a conversation that fails to validate
// — a record written by a newer harness, or one a half-finished write left
// behind — and a listing is the surface an operator reaches for when something
// is already wrong, so it must not be the thing that goes silent first. A record
// names its conversation whether or not the rest of it is loadable.
func (s *StreamStore) currentConversations() (map[string]bool, error) {
	entries, err := os.ReadDir(s.conversations.Root())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read conversation records: %w", err)
	}
	current := make(map[string]bool, len(entries))
	for _, entry := range entries {
		// The leases, the event logs, and the temporary files of a save in flight
		// all live in this directory; only a file named for an agent holds a
		// conversation.
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		recorded, err := os.ReadFile(filepath.Join(s.conversations.Root(), name))
		if err != nil {
			return nil, fmt.Errorf("read conversation record %s: %w", name, err)
		}
		var read struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.Unmarshal(recorded, &read); err != nil || read.ConversationID == "" {
			continue
		}
		current[read.ConversationID] = true
	}
	return current, nil
}

// streamScan is what one pass over an event log yields a listing.
type streamScan struct {
	events int
	first  time.Time
	// answering says the last turn the log recorded opened and has not ended.
	// Each turn is a provider invocation bracketed by the same two events a run's
	// is, and a turn the provider failed is over rather than still being made —
	// so a conversation whose last turn died reads as waiting rather than as
	// answering forever.
	answering bool
	reviewed  bool
}

func scanStreamLog(path string) (streamScan, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return streamScan{}, nil
	}
	if err != nil {
		return streamScan{}, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	var scanned streamScan
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		scanned.events++
		var read struct {
			Type      execution.EventType `json:"type"`
			Timestamp time.Time           `json:"timestamp"`
		}
		// A line that will not decode still happened, so it is counted; what it
		// cannot do is say anything about when or what.
		if err := json.Unmarshal(scanner.Bytes(), &read); err != nil {
			continue
		}
		if scanned.first.IsZero() {
			scanned.first = read.Timestamp
		}
		switch read.Type {
		case execution.EventRunStarted:
			scanned.answering = true
		case execution.EventRunCompleted, execution.EventRunFailed:
			scanned.answering = false
		case execution.EventReviewCompleted:
			scanned.reviewed = true
		}
	}
	if err := scanner.Err(); err != nil {
		return streamScan{}, fmt.Errorf("read event log: %w", err)
	}
	return scanned, nil
}

// TokenUsage is what the provider reported one or more invocations consumed.
// The four are kept apart because they are priced apart, and because the share
// that was a cache read is the one figure that says whether the harness is
// paying for the same context over and over.
type TokenUsage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
}

func (u TokenUsage) Total() int64 {
	return u.Input + u.Output + u.CacheCreation + u.CacheRead
}

func (u *TokenUsage) Add(other TokenUsage) {
	u.Input += other.Input
	u.Output += other.Output
	u.CacheCreation += other.CacheCreation
	u.CacheRead += other.CacheRead
}

// Invocation is one provider call a stream recorded: when it ended, what the
// provider said it cost, and what it consumed. Cost is what the provider
// reported rather than an estimate, and a call that failed is one of these too —
// it was made and it was paid for, and leaving it out would understate every
// total it belonged in.
type Invocation struct {
	At      time.Time  `json:"at,omitzero"`
	CostUSD float64    `json:"cost_usd"`
	Usage   TokenUsage `json:"usage"`
}

// Invocations reads every provider call one stream recorded. A log that is gone
// has no invocations rather than an unreadable one: a stream removed between
// being listed and being priced is a race with cleanup, not a failure of the
// report.
func (s *StreamStore) Invocations(stream Stream) ([]Invocation, error) {
	file, err := os.Open(stream.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open event log to price %s: %w", stream.ID, err)
	}
	defer file.Close()

	var invocations []Invocation
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		// The cheap test over-matches and the decoded type rejects the rest, which
		// is what keeps pricing a stream from decoding all of its own chatter.
		if !carriesSpendEvidence(line) {
			continue
		}
		var read invocationEvent
		if err := json.Unmarshal(line, &read); err != nil {
			return nil, fmt.Errorf("decode event log to price %s: %w", stream.ID, err)
		}
		if !read.priced() {
			continue
		}
		invocations = append(invocations, Invocation{
			At:      read.Timestamp,
			CostUSD: read.Payload.TotalCostUSD,
			Usage:   read.Payload.Usage,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read event log to price %s: %w", stream.ID, err)
	}
	return invocations, nil
}

type invocationEvent struct {
	Type      execution.EventType `json:"type"`
	Timestamp time.Time           `json:"timestamp"`
	Payload   struct {
		TotalCostUSD float64    `json:"total_cost_usd"`
		Usage        TokenUsage `json:"usage"`
	} `json:"payload"`
}

func (e invocationEvent) priced() bool {
	for _, candidate := range pricedEvents {
		if e.Type == candidate {
			return true
		}
	}
	return false
}

// UndatedDay is where spend whose moment could not be read is grouped. It still
// cost money, so it is reported rather than dropped: it sorts ahead of every
// dated day and no window excludes it, because it has no day to be outside of.
const UndatedDay = "undated"

// SpendQuery selects what a spend report covers.
type SpendQuery struct {
	Kinds []StreamKind
	// Match prices the one stream it names, whatever day that stream ran on: the
	// window is for a report that has to choose what to show, and naming a stream
	// has already chosen.
	Match string
	// Days is how many local days the report covers, today counting as the first
	// of them. Zero covers every day there is evidence for.
	Days int
	// Now is the moment the window is measured back from, so a report is a
	// function of its inputs rather than of when it happened to be asked for.
	Now time.Time
}

// SpendRow is one stream's spend on one local day. A stream contributes one row
// per day it spent on rather than one row for the day it opened: a conversation
// stays open for as long as the role is in it, so one opened a fortnight ago and
// answered again this morning spends on both days, and a single row for its
// opening day would leave this morning out of what today cost.
type SpendRow struct {
	Day      string     `json:"day"`
	StreamID string     `json:"id"`
	Kind     StreamKind `json:"kind"`
	Status   string     `json:"status"`
	// At is the moment the row is shown and ordered at. On the day the work
	// opened it is when it opened, which is what that column has always said; on
	// a later day it is the first invocation of that day, because there is
	// nothing else the column could mean there.
	At      time.Time  `json:"at,omitzero"`
	Calls   int        `json:"calls"`
	Usage   TokenUsage `json:"usage"`
	CostUSD float64    `json:"cost_usd"`
}

// SpendReport is what the selected streams spent, one row per stream per day,
// ordered so that each day's rows are contiguous and the most recent day is
// last — which is where the eye already is when the question is what today cost.
type SpendReport struct {
	Rows []SpendRow `json:"rows"`
	// Oldest is the first local day the window covers, and is empty on a report
	// with no window. It is what lets an empty report say which of the two
	// empties it is: nothing spent at all, or nothing spent in the days asked
	// about, which a wider window would answer differently.
	Oldest string `json:"oldest_day,omitempty"`
	Days   int    `json:"days,omitempty"`
	// Streams is how many event streams were read to produce the rows.
	Streams int `json:"streams"`
}

// Spend prices the selected streams by the local-timezone day the money was
// spent on, because what an operator budgets against is what today cost and the
// day they mean is the one their own clock is keeping.
//
// Every selected stream is read whatever day it opened on, because the window is
// about when the money was spent and a stream that opened before it can still
// have spent inside it. Only the rows a stream yields are held against the
// window.
func (s *StreamStore) Spend(query SpendQuery) (SpendReport, error) {
	streams, err := s.List(StreamQuery{Kinds: query.Kinds, Match: query.Match})
	if err != nil {
		return SpendReport{}, err
	}
	report := SpendReport{Streams: len(streams)}
	if query.Days > 0 {
		report.Days = query.Days
		report.Oldest = oldestLocalDay(query.Now, query.Days)
	}
	for _, stream := range streams {
		invocations, err := s.Invocations(stream)
		if err != nil {
			return SpendReport{}, err
		}
		for _, row := range spendByDay(stream, invocations) {
			if report.Oldest != "" && row.Day != UndatedDay && row.Day < report.Oldest {
				continue
			}
			report.Rows = append(report.Rows, row)
		}
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		if !report.Rows[i].At.Equal(report.Rows[j].At) {
			return report.Rows[i].At.Before(report.Rows[j].At)
		}
		return report.Rows[i].StreamID < report.Rows[j].StreamID
	})
	return report, nil
}

func spendByDay(stream Stream, invocations []Invocation) []SpendRow {
	openedOn := ""
	if stream.Dated() {
		openedOn = LocalDay(stream.StartedAt)
	}
	var order []string
	rows := make(map[string]*SpendRow, len(invocations))
	for _, invocation := range invocations {
		day := UndatedDay
		if !invocation.At.IsZero() {
			day = LocalDay(invocation.At)
		}
		row, seen := rows[day]
		if !seen {
			row = &SpendRow{Day: day, StreamID: stream.ID, Kind: stream.Kind, Status: stream.Status, At: invocation.At}
			if day == openedOn {
				row.At = stream.StartedAt
			}
			rows[day] = row
			order = append(order, day)
		}
		row.Calls++
		row.CostUSD += invocation.CostUSD
		row.Usage.Add(invocation.Usage)
	}
	grouped := make([]SpendRow, 0, len(order))
	for _, day := range order {
		grouped = append(grouped, *rows[day])
	}
	return grouped
}

// LocalDay is the day a moment falls on in the timezone the operator's day
// happens in, which is the only timezone a spend report is legible in.
func LocalDay(moment time.Time) string {
	return moment.Local().Format("2006-01-02")
}

// oldestLocalDay is the first local day a report of that many days covers, today
// counting as the first of them, so 7 is today and the six before it. It steps
// back by calendar days rather than by multiples of twenty-four hours, so a
// daylight-saving shift cannot move the answer by a day.
func oldestLocalDay(now time.Time, days int) string {
	local := now.Local()
	return time.Date(local.Year(), local.Month(), local.Day()-(days-1), 12, 0, 0, 0, local.Location()).
		Format("2006-01-02")
}
