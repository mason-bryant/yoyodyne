package cli

// Watching the harness work, rather than reading what it did afterwards.
//
// This is the live half of `yoyo status`. The other half reads back what the
// run records hold now; these modes follow the normalized event stream a run, a
// conversation, or a branch review is writing, list what has been recorded
// lately, and price it by the day the money was spent on.
//
// It used to be `bin/yoyo-status`, a shell script that lived only in a checkout
// of this repository: `go install` and a release download did not carry it, so
// the operator's daily observability surface was absent for everybody who had
// never seen the internals. Folding it in here is what makes it ship. Nothing
// about what it reports is new — the rendering the spend report uses is the
// one the operator asked for and has been reading — and two things about it
// are: it needs no `jq`, and it prices a failed invocation, which cost money
// like any other and which the script left out of every total it belonged in.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// defaultStreamLines is how many recorded events are replayed before a stream
// is followed, and how many an `--events` listing prints. A screenful of what
// just happened is what makes a stream legible from the moment it is opened;
// the whole log is what `--lines 0` asks for.
const defaultStreamLines = 50

// defaultSpendDays is how many local days a spend report covers when nobody
// says. A week is what an operator budgets against, and a report reaching back
// over every run the machine has ever made would bury the days they are
// actually deciding about under months of settled ones.
const defaultSpendDays = 7

const (
	// streamPollInterval is how often a followed log is asked what else it holds.
	streamPollInterval = 200 * time.Millisecond
	// streamSwitchInterval is how often `--latest` asks whether a later stream
	// has started, and how long waiting for a first one sleeps between looks.
	streamSwitchInterval = 3 * time.Second
)

// streamOptions is what the three live modes were asked for, resolved from the
// flags once so no mode has to re-derive it.
type streamOptions struct {
	kinds  []runstate.StreamKind
	match  string
	lines  int
	limit  int
	days   int
	follow bool
	latest bool
	raw    bool
	all    bool
}

// resolveStreamKinds reads what `--kind` narrowed the answer to. Nothing named
// covers all three, which is the default the script had and the reason the
// question "is this alive" never has to say which kind of alive it means.
func resolveStreamKinds(kind string) ([]runstate.StreamKind, error) {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "", "all":
		return nil, nil
	case "run", "runs":
		return []runstate.StreamKind{runstate.StreamRun}, nil
	case "chat", "chats", "conversation", "conversations":
		return []runstate.StreamKind{runstate.StreamConversation}, nil
	case "review", "reviews", "branch-review", "branch-reviews":
		return []runstate.StreamKind{runstate.StreamReview}, nil
	default:
		return nil, fmt.Errorf("unknown kind %q: it is runs, chats, reviews, or all", kind)
	}
}

// statusHolds is whether the operator has stopped the harness. It leads every
// mode of this verb because it is the first thing to say about a machine that
// looks idle: a system somebody paused and forgot looks exactly like a system
// that died, and only one of those needs anybody.
type statusHolds struct {
	Paused *runstate.OperatorHold `json:"paused,omitempty"`
	Intake *runstate.IntakeHold   `json:"intake_held,omitempty"`
	// Error accompanies a successful answer, for the reason the run listing's
	// triage failure does: an unreadable hold costs this answer a banner rather
	// than the streams it found.
	Error string `json:"holds_error,omitempty"`
}

func readStatusHolds(roots statusRoots) statusHolds {
	var holds statusHolds
	operator, err := runstate.NewOperatorHoldStore(roots.stateRoot)
	if err != nil {
		return statusHolds{Error: fmt.Sprintf("whether the harness is paused could not be read: %v", err)}
	}
	paused, held, err := operator.Held()
	if err != nil {
		return statusHolds{Error: fmt.Sprintf("whether the harness is paused could not be read: %v", err)}
	}
	if held {
		holds.Paused = &paused
	}
	intake, err := runstate.NewIntakeHoldStore(roots.stateRoot, roots.productID)
	if err != nil {
		holds.Error = fmt.Sprintf("whether intake is held could not be read: %v", err)
		return holds
	}
	intakeHold, intakeHeld, err := intake.Held()
	if err != nil {
		holds.Error = fmt.Sprintf("whether intake is held could not be read: %v", err)
		return holds
	}
	if intakeHeld {
		holds.Intake = &intakeHold
	}
	return holds
}

// announceHolds says what the operator has stopped, on the stream a banner
// belongs on so that machine-readable output on stdout stays clean. The remedy
// is named as something runnable from the terminal this was read at: `/release`
// in a conversation lifts the same record, but a person reading a listing may
// have no conversation open, and a remedy they cannot run from where they are
// standing is not one.
func announceHolds(writer io.Writer, holds statusHolds) {
	if holds.Paused != nil {
		fmt.Fprintf(writer, "PAUSED: all harness activity is paused since %s\n",
			holds.Paused.HeldAt.UTC().Format(time.RFC3339))
		fmt.Fprintln(writer, "nothing reaches the provider until `yoyo resume` lifts it; parked runs keep their claim, branch, and worktree")
	}
	if holds.Intake != nil {
		fmt.Fprintf(writer, "INTAKE HELD: the harness starts nothing more on its own since %s\n",
			holds.Intake.HeldAt.UTC().Format(time.RFC3339))
		if reason := strings.TrimSpace(holds.Intake.Reason); reason != "" {
			fmt.Fprintln(writer, reason)
		}
		fmt.Fprintln(writer, "work already running carries on; `yoyo release` lets it choose work again, as does /release in a conversation")
	}
	if holds.Error != "" {
		fmt.Fprintln(writer, holds.Error)
	}
}

type streamListOutput struct {
	Streams []runstate.Stream `json:"streams"`
	statusHolds
	Error string `json:"error,omitempty"`
}

// listStreams reports what has been recorded lately, newest first, of all three
// kinds together. Which kind each one is, and what it is doing, are the two
// columns that make the listing an answer rather than a directory.
func listStreams(store *runstate.StreamStore, options streamOptions, holds statusHolds, jsonOutput bool, stdout, stderr io.Writer) int {
	streams, err := store.List(runstate.StreamQuery{Kinds: options.kinds, Match: options.match, Limit: options.limit})
	if err != nil {
		return reportStreamFailure(stdout, stderr, jsonOutput, streamListOutput{Error: err.Error()}, err)
	}
	if jsonOutput {
		if streams == nil {
			streams = []runstate.Stream{}
		}
		return writeJSON(stdout, stderr, streamListOutput{Streams: streams, statusHolds: holds})
	}
	if len(streams) == 0 {
		fmt.Fprintln(stdout, describeNoStreams(store, options.match))
		return 0
	}
	fmt.Fprintf(stdout, streamListRow, "id", "kind", "status", "events", "started")
	for _, stream := range streams {
		fmt.Fprintf(stdout, streamListRow, stream.ID, string(stream.Kind), stream.Status,
			strconv.Itoa(stream.Events), renderStreamStart(stream.StartedAt))
	}
	return 0
}

// streamListRow is the shape of every line of a stream listing, header included,
// so the columns cannot drift apart between them.
const streamListRow = "%-40s %-13s %-11s %8s  %s\n"

func renderStreamStart(started time.Time) string {
	if started.IsZero() {
		return "-"
	}
	return started.Local().Format("2006-01-02 15:04")
}

// describeNoStreams says which of the two empties an empty listing is: a
// machine that has recorded nothing, or a pattern that named nothing, which a
// different pattern would answer differently. Either way it names the directory
// it read, because the state root comes from the environment and a true answer
// about the wrong directory is the one failure this surface cannot afford.
func describeNoStreams(store *runstate.StreamStore, match string) string {
	if match != "" {
		return fmt.Sprintf("no run, conversation, or branch review matching %q is recorded under %s", match, store.Root())
	}
	return fmt.Sprintf("no runs, conversations, or branch reviews are recorded under %s", store.Root())
}

type spendOutput struct {
	Report runstate.SpendReport `json:"spend"`
	statusHolds
	Error string `json:"error,omitempty"`
}

// reportSpend prices what the recorded streams spent, grouped by the local day
// the money was spent on. The rendering is the one the operator reads today and
// asked to keep: a day per group, that day's spend closing it, the most recent
// day last where the eye already is, and the split under the total saying how
// much of it was each kind of work.
func reportSpend(store *runstate.StreamStore, options streamOptions, holds statusHolds, now time.Time, jsonOutput bool, stdout, stderr io.Writer) int {
	report, err := store.Spend(runstate.SpendQuery{
		Kinds: options.kinds,
		Match: options.match,
		Days:  options.days,
		Now:   now,
	})
	if err != nil {
		return reportStreamFailure(stdout, stderr, jsonOutput, spendOutput{Error: err.Error()}, err)
	}
	if jsonOutput {
		if report.Rows == nil {
			report.Rows = []runstate.SpendRow{}
		}
		return writeJSON(stdout, stderr, spendOutput{Report: report, statusHolds: holds})
	}
	if report.Streams == 0 {
		// A stream named but not found is a question that could not be asked,
		// rather than a machine that spent nothing.
		if options.match != "" {
			fmt.Fprintln(stderr, describeNoStreams(store, options.match))
			return 1
		}
		// A machine that has recorded nothing says so, rather than reporting an
		// empty week and inviting a wider window that would be just as empty.
		fmt.Fprintln(stdout, describeNoStreams(store, ""))
		return 0
	}
	if len(report.Rows) == 0 {
		// An empty report says which of the two empties it is: nothing has been
		// spent at all, or nothing was spent in the days asked about, which a wider
		// window would answer differently.
		if report.Oldest != "" {
			fmt.Fprintf(stdout, "no completed provider invocations since %s; pass a number of days to reach further back\n", report.Oldest)
		} else {
			fmt.Fprintln(stdout, "no completed provider invocations yet")
		}
		return 0
	}
	printSpendRows(stdout, report, now)
	printSpendTotals(stdout, report)
	return 0
}

const (
	spendHeader   = "%-39s %-17s %6s %8s %9s %10s %11s %9s  %s\n"
	spendRow      = "%-39s %-17s %6d %8d %9d %10d %11d %9.2f  %s\n"
	spendSubtotal = "%-39s %-17s %6d %8d %9d %10d %11d %9.2f\n"
	spendRule     = "----------------------------------------------------------------------------------------------------------------"
)

func printSpendRows(writer io.Writer, report runstate.SpendReport, now time.Time) {
	fmt.Fprintf(writer, spendHeader, "id", "started", "calls", "in", "out", "cache_w", "cache_r", "USD", "status")
	today := runstate.LocalDay(now)
	day := ""
	var subtotal runstate.SpendRow
	for _, row := range report.Rows {
		if row.Day != day {
			printSpendSubtotal(writer, day, subtotal)
			day, subtotal = row.Day, runstate.SpendRow{}
			marker := ""
			if day == today {
				marker = "  (today)"
			}
			fmt.Fprintf(writer, "%s%s\n", day, marker)
		}
		fmt.Fprintf(writer, spendRow, row.StreamID, renderSpendMoment(row.At), row.Calls,
			row.Usage.Input, row.Usage.Output, row.Usage.CacheCreation, row.Usage.CacheRead, row.CostUSD, row.Status)
		subtotal.Calls += row.Calls
		subtotal.CostUSD += row.CostUSD
		subtotal.Usage.Add(row.Usage)
	}
	printSpendSubtotal(writer, day, subtotal)
}

func printSpendSubtotal(writer io.Writer, day string, subtotal runstate.SpendRow) {
	if day == "" {
		return
	}
	fmt.Fprintf(writer, spendSubtotal, day+" total", "", subtotal.Calls,
		subtotal.Usage.Input, subtotal.Usage.Output, subtotal.Usage.CacheCreation, subtotal.Usage.CacheRead, subtotal.CostUSD)
	fmt.Fprintln(writer)
}

// renderSpendMoment is what a row says in its started column. Something whose
// moment could not be read still cost money and is still reported; it simply has
// no moment to name.
func renderSpendMoment(moment time.Time) string {
	if moment.IsZero() {
		return "-"
	}
	return moment.Local().Format("01 02 2006 15:04")
}

func printSpendTotals(writer io.Writer, report runstate.SpendReport) {
	var total runstate.SpendRow
	byKind := map[runstate.StreamKind]runstate.SpendRow{}
	for _, row := range report.Rows {
		total.Calls += row.Calls
		total.CostUSD += row.CostUSD
		total.Usage.Add(row.Usage)
		summed := byKind[row.Kind]
		summed.Calls += row.Calls
		summed.CostUSD += row.CostUSD
		byKind[row.Kind] = summed
	}
	window := ""
	if report.Days > 0 {
		window = fmt.Sprintf(" (last %d days)", report.Days)
	}
	fmt.Fprintln(writer, spendRule)
	fmt.Fprintf(writer, spendSubtotal, "TOTAL"+window, "", total.Calls,
		total.Usage.Input, total.Usage.Output, total.Usage.CacheCreation, total.Usage.CacheRead, total.CostUSD)
	tokens := total.Usage.Total()
	cached := 0.0
	if tokens > 0 {
		cached = float64(total.Usage.CacheRead) * 100 / float64(tokens)
	}
	fmt.Fprintf(writer, "\ntokens: %s total (%.1f%% cache reads)   cost: $%.2f\n",
		groupThousands(tokens), cached, total.CostUSD)
	if split := renderKindSplit(byKind); split != "" {
		fmt.Fprintln(writer, split)
	}
}

// renderKindSplit says how much of a total was each kind of work, and says it
// only when the total is actually mixed, so the one number is never left
// ambiguous about what went into it. A conversation turn and a branch review are
// each a provider invocation like any other and are priced beside the runs: a
// total that skipped either would be wrong rather than unattributed.
func renderKindSplit(byKind map[runstate.StreamKind]runstate.SpendRow) string {
	var parts []string
	for _, kind := range []struct {
		kind runstate.StreamKind
		name string
		unit string
	}{
		{runstate.StreamRun, "runs", "invocation(s)"},
		{runstate.StreamConversation, "conversations", "turn(s)"},
		{runstate.StreamReview, "branch reviews", "invocation(s)"},
	} {
		summed, spent := byKind[kind.kind]
		if !spent || summed.Calls == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: $%.2f from %d %s", kind.name, summed.CostUSD, summed.Calls, kind.unit))
	}
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts, "   ")
}

// groupThousands puts separators in a token count. Seven undivided digits is a
// number nobody reads, and the whole point of the line it appears on is to be
// read at a glance.
func groupThousands(value int64) string {
	digits := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var grouped strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return sign + grouped.String()
}

// followStreams is the mode that watches. It replays the recent events of the
// stream it resolved and then, unless it was asked only for those, keeps
// emitting what arrives. `--latest` makes it a watch on the harness rather than
// on one stream: it moves to a later stream when one starts, and drains the one
// it is leaving first so no events are lost between them.
func followStreams(ctx context.Context, store *runstate.StreamStore, options streamOptions, stdout, stderr io.Writer) int {
	stream, found, err := resolveStream(ctx, store, options, stderr)
	if errors.Is(err, context.Canceled) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "status failed: %v\n", err)
		return 1
	}
	if !found {
		fmt.Fprintln(stderr, describeNoStreams(store, options.match))
		return 1
	}
	for {
		next, err := followOne(ctx, store, stream, options, stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "status failed: %v\n", err)
			return 1
		}
		if next == nil {
			return 0
		}
		stream = *next
	}
}

// resolveStream is the stream a follow was asked for. Naming one takes it;
// naming nothing takes the newest. Nothing recorded yet is normal when a run is
// about to start, so a follow waits for one rather than refusing — but only when
// nothing was named and only when it was actually asked to follow: a listing of
// recent events has nothing to wait for.
func resolveStream(ctx context.Context, store *runstate.StreamStore, options streamOptions, stderr io.Writer) (runstate.Stream, bool, error) {
	query := runstate.StreamQuery{Kinds: options.kinds, Match: options.match}
	announced := false
	for {
		stream, found, err := store.Find(query)
		if err != nil || found {
			return stream, found, err
		}
		if options.match != "" || !options.follow {
			return runstate.Stream{}, false, nil
		}
		if !announced {
			fmt.Fprintln(stderr, "waiting for a run, a conversation, or a branch review to start ...")
			announced = true
		}
		select {
		case <-ctx.Done():
			return runstate.Stream{}, false, ctx.Err()
		case <-time.After(streamSwitchInterval):
		}
	}
}

// followOne emits one stream's events, and reports the stream to move on to
// when `--latest` found a later one. A nil stream is the end of the answer:
// either the replay was all that was asked for, or the operator interrupted it.
func followOne(ctx context.Context, store *runstate.StreamStore, stream runstate.Stream, options streamOptions, stdout, stderr io.Writer) (*runstate.Stream, error) {
	fmt.Fprintf(stderr, "==> %s [%s]\n", stream.ID, stream.Status)
	tail := &logTail{path: stream.Path}
	lines, err := tail.read()
	if err != nil {
		return nil, err
	}
	if options.lines > 0 && len(lines) > options.lines {
		lines = lines[len(lines)-options.lines:]
	}
	writeEvents(stdout, lines, options)
	if !options.follow {
		return nil, nil
	}

	poll := time.NewTicker(streamPollInterval)
	defer poll.Stop()
	look := time.NewTicker(streamSwitchInterval)
	defer look.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case <-poll.C:
			lines, err := tail.read()
			if err != nil {
				return nil, err
			}
			writeEvents(stdout, lines, options)
		case <-look.C:
			if !options.latest {
				continue
			}
			newest, found, err := store.Find(runstate.StreamQuery{Kinds: options.kinds})
			if err != nil {
				return nil, err
			}
			if !found || newest.ID == stream.ID {
				continue
			}
			// Whatever the stream being left behind wrote while this was deciding is
			// emitted before moving on, so nothing is dropped between the two.
			lines, err := tail.read()
			if err != nil {
				return nil, err
			}
			writeEvents(stdout, lines, options)
			return &newest, nil
		}
	}
}

// logTail reads an event log's complete lines from where it last got to, so a
// stream is followed by asking again rather than by holding a handle open on it.
// A log that was replaced or truncated underneath is read from its beginning
// again rather than from an offset that now means something else.
type logTail struct {
	path    string
	offset  int64
	partial []byte
}

func (t *logTail) read() ([][]byte, error) {
	file, err := os.Open(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect event log: %w", err)
	}
	if info.Size() < t.offset {
		t.offset, t.partial = 0, nil
	}
	if _, err := file.Seek(t.offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	appended, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read event log: %w", err)
	}
	t.offset += int64(len(appended))
	// Only whole lines are emitted: a record the harness is halfway through
	// writing is not an event yet.
	pending := append(t.partial, appended...)
	var lines [][]byte
	for {
		end := bytes.IndexByte(pending, '\n')
		if end < 0 {
			break
		}
		lines = append(lines, append([]byte(nil), pending[:end]...))
		pending = pending[end+1:]
	}
	t.partial = append([]byte(nil), pending...)
	return lines, nil
}

// shapedEvent is what a followed event is rendered as: the five fields that say
// what happened, without the envelope every line repeats.
type shapedEvent struct {
	Sequence  uint64          `json:"sequence"`
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func writeEvents(writer io.Writer, lines [][]byte, options streamOptions) {
	for _, line := range lines {
		if options.raw {
			fmt.Fprintf(writer, "%s\n", line)
			continue
		}
		var shaped shapedEvent
		// A line that will not decode is emitted as it was recorded rather than
		// dropped: a stream is being followed to find out what went wrong, and a
		// record this cannot read is itself part of the answer.
		if err := json.Unmarshal(line, &shaped); err != nil {
			fmt.Fprintf(writer, "%s\n", line)
			continue
		}
		// Thinking-token pings carry nothing but their own arrival and drown
		// everything else, so they are left out unless they were asked for.
		if !options.all && thinkingTokens(shaped.Payload) {
			continue
		}
		encoded, err := json.Marshal(shaped)
		if err != nil {
			fmt.Fprintf(writer, "%s\n", line)
			continue
		}
		fmt.Fprintf(writer, "%s\n", encoded)
	}
}

func thinkingTokens(payload json.RawMessage) bool {
	if len(payload) == 0 {
		return false
	}
	var read struct {
		ProviderSubtype string `json:"provider_subtype"`
	}
	if err := json.Unmarshal(payload, &read); err != nil {
		return false
	}
	return read.ProviderSubtype == "thinking_tokens"
}

func reportStreamFailure(stdout, stderr io.Writer, jsonOutput bool, payload any, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, payload); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "status failed: %v\n", err)
	return 1
}
