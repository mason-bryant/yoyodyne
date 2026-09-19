package cli

// Watching the harness work, rather than reading what it did afterwards.
//
// This is the live half of `yoyo status`. The other half reads back what the
// run records hold now; these modes follow the normalized event stream a run, a
// conversation, or a branch review is writing, list what has been recorded
// lately, and price it by the day the money was spent on — the exchanges the
// roles conducted beside the streams, because what two roles spent asking each
// other is money the harness spent.
//
// It used to be `bin/yoyo-status`, a shell script that lived only in a checkout
// of this repository: `go install` and a release download did not carry it, so
// the operator's daily observability surface was absent for everybody who had
// never seen the internals. Folding it in here is what makes it ship. Nothing
// about what it reports is new — the rendering the spend report uses is the
// one the operator asked for and has been reading — and two things about it
// are: it needs no `jq`, and it prices a failed invocation, which cost money
// like any other and which the script left out of every total it belonged in.
//
// The script is retired rather than kept as a wrapper. A wrapper would have to
// be installed to be useful, which is the gap this closes; kept in the checkout
// it would be a second copy of every sentence here, drifting from this one as
// the script's own banner drifted from the harness's wording before. What it
// did that this does not — nothing — is the whole of the case for keeping it.

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
	// poll and look are the two intervals a follow runs on. They are here rather
	// than fixed so a test of the switch can make the look frequent and the poll
	// rare, which is what isolates the drain from the ordinary polling; zero
	// takes the default.
	poll, look time.Duration
}

func (o streamOptions) pollInterval() time.Duration {
	if o.poll > 0 {
		return o.poll
	}
	return streamPollInterval
}

func (o streamOptions) lookInterval() time.Duration {
	if o.look > 0 {
		return o.look
	}
	return streamSwitchInterval
}

// resolveStreamKinds reads what `--kind` narrowed the answer to. Nothing named
// covers everything, which is the default the script had and the reason the
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
	case "exchange", "exchanges":
		return []runstate.StreamKind{runstate.StreamExchange}, nil
	default:
		return nil, fmt.Errorf("unknown kind %q: it is runs, chats, reviews, exchanges, or all", kind)
	}
}

// followableKinds reports whether every named kind records a stream to follow
// or list. An exchange does not, so a mode that reads streams and was narrowed
// to exchanges alone would answer with nothing and call it a listing.
func followableKinds(kinds []runstate.StreamKind) bool {
	for _, kind := range kinds {
		if !kind.Followable() {
			return false
		}
	}
	return true
}

// statusHolds is whether the operator has stopped the harness. It leads every
// live mode of this verb because it is the first thing to say about a machine
// that looks idle: a system somebody paused and forgot looks exactly like a
// system that died, and only one of those needs anybody. The recorded mode
// carries the same two switches on its attention line, from the read model.
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
// belongs on so that machine-readable output on stdout stays clean. Who placed
// the intake hold and why is the hold's own clause, composed once in
// IntakeHold.Says, because a banner that guessed sent an operator looking for a
// decision the brake had made. The remedy is named as something runnable from
// the terminal this was read at: `/release` in a conversation lifts the same
// record, but a person reading a listing may have no conversation open, and a
// remedy they cannot run from where they are standing is not one.
func announceHolds(writer io.Writer, holds statusHolds) {
	if holds.Paused != nil {
		fmt.Fprintf(writer, "PAUSED: all harness activity is paused since %s\n",
			holds.Paused.HeldAt.UTC().Format(time.RFC3339))
		fmt.Fprintln(writer, "nothing reaches the provider until `yoyo resume` lifts it; parked runs keep their claim, branch, and worktree")
	}
	if holds.Intake != nil {
		fmt.Fprintf(writer, "INTAKE HELD since %s: %s\n",
			holds.Intake.HeldAt.UTC().Format(time.RFC3339), holds.Intake.Account())
		fmt.Fprintln(writer, "the harness starts nothing more on its own; work already running carries on, and `yoyo release` lets it choose work again, as does /release in a conversation")
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
		fmt.Fprintln(stdout, describeNoStreams(store, options.kinds, options.match, false))
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

// describeNoStreams says which of the two empties an empty answer is: a machine
// that has recorded nothing of the kinds asked about, or a pattern that named
// nothing, which a different pattern would answer differently. It names the
// kinds actually queried — all of them when none was narrowed to — because an
// answer worded for the unnarrowed question told an operator with fifty runs
// and no branch reviews that nothing at all was recorded. Either way it names
// the directory it read, because the state root comes from the environment and
// a true answer about the wrong directory is the one failure this surface
// cannot afford.
//
// priced says whether the exchanges are among the kinds an unnarrowed query
// covers, which they are for a spend report and are not for a listing.
func describeNoStreams(store *runstate.StreamStore, kinds []runstate.StreamKind, match string, priced bool) string {
	if match != "" {
		return fmt.Sprintf("no %s matching %q is recorded under %s", describeStreamKinds(kinds, priced, false), match, store.Root())
	}
	return fmt.Sprintf("no %s are recorded under %s", describeStreamKinds(kinds, priced, true), store.Root())
}

// describeStreamKinds names the kinds a query covers, in the order the report
// prices them, as one phrase: "runs, conversations, or branch reviews".
func describeStreamKinds(kinds []runstate.StreamKind, priced, plural bool) string {
	if len(kinds) == 0 {
		kinds = runstate.EveryStreamKind
		if priced {
			kinds = runstate.EveryPricedKind
		}
	}
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, streamKindName(kind, plural))
	}
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
	}
}

func streamKindName(kind runstate.StreamKind, plural bool) string {
	name := map[runstate.StreamKind][2]string{
		runstate.StreamRun:          {"run", "runs"},
		runstate.StreamConversation: {"conversation", "conversations"},
		runstate.StreamReview:       {"branch review", "branch reviews"},
		runstate.StreamExchange:     {"exchange", "exchanges"},
	}[kind]
	if plural {
		return name[1]
	}
	return name[0]
}

type spendOutput struct {
	Report runstate.SpendReport `json:"spend"`
	statusHolds
	Error string `json:"error,omitempty"`
}

// reportSpend prices what the recorded streams and exchanges spent, grouped by
// the local day the money was spent on. The rendering is the one the operator
// reads today and asked to keep: a day per group, that day's spend closing it,
// the most recent day last where the eye already is, and the split under the
// total saying how much of it was each kind of work.
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
	if report.Empty() {
		// Something named but not found is a question that could not be asked,
		// rather than a machine that spent nothing.
		if options.match != "" {
			fmt.Fprintln(stderr, describeNoStreams(store, options.kinds, options.match, true))
			return 1
		}
		// A machine that has recorded nothing of the kinds asked about says so,
		// rather than reporting an empty week and inviting a wider window that
		// would be just as empty.
		fmt.Fprintln(stdout, describeNoStreams(store, options.kinds, "", true))
		return 0
	}
	if len(report.Rows) == 0 {
		// An empty report says which of the three empties it is: every exchange
		// there was could not be read, nothing has been spent at all, or nothing was
		// spent in the days asked about, which a wider window would answer
		// differently.
		switch {
		case report.Floor():
			printUnreadableExchanges(stdout, report)
		case report.Oldest != "":
			fmt.Fprintf(stdout, "no completed provider invocations since %s; pass a number of days to reach further back\n", report.Oldest)
		default:
			fmt.Fprintln(stdout, "no completed provider invocations yet")
		}
		return 0
	}
	printSpendRows(stdout, report, now)
	printSpendTotals(stdout, report)
	printUnreadableExchanges(stdout, report)
	return 0
}

// The id column is as wide as the widest id there is, which is an exchange's: a
// column sized to a run's id would be shoved two characters right by every
// exchange row and take the rest of the table with it. The token columns of a
// row are strings, so a row that records no usage can say "-" where it has
// nothing to say.
const (
	spendHeader   = "%-41s %-17s %6s %8s %9s %10s %11s %9s  %s\n"
	spendRow      = "%-41s %-17s %6d %8s %9s %10s %11s %9.2f  %s\n"
	spendSubtotal = "%-41s %-17s %6d %8d %9d %10d %11d %9.2f\n"
	spendTotal    = "%-41s %-17s %6d %8d %9d %10d %11d %9s\n"
)

var spendRule = strings.Repeat("-", 114)

func printSpendRows(writer io.Writer, report runstate.SpendReport, now time.Time) {
	fmt.Fprintf(writer, spendHeader, "id", "started", "calls", "in", "out", "cache_w", "cache_r", "USD", "status")
	today := runstate.LocalDay(now)
	day := ""
	var subtotal runstate.SpendRow
	for _, row := range report.Rows {
		if row.Day != day {
			printSpendSubtotal(writer, day, subtotal)
			day, subtotal = row.Day, runstate.SpendRow{Usage: &runstate.TokenUsage{}}
			marker := ""
			if day == today {
				marker = "  (today)"
			}
			fmt.Fprintf(writer, "%s%s\n", day, marker)
		}
		in, out, written, read := renderSpendTokens(row.Usage)
		fmt.Fprintf(writer, spendRow, row.StreamID, renderSpendMoment(row.At), row.Calls,
			in, out, written, read, row.CostUSD, row.Status)
		subtotal.Calls += row.Calls
		subtotal.CostUSD += row.CostUSD
		if row.Usage != nil {
			subtotal.Usage.Merge(*row.Usage)
		}
	}
	printSpendSubtotal(writer, day, subtotal)
}

// renderSpendTokens is a row's four token columns. A row that records no usage
// says "-" in each rather than 0: an exchange did not use no tokens, and a zero
// there would read as a measurement.
func renderSpendTokens(usage *runstate.TokenUsage) (in, out, written, read string) {
	if usage == nil {
		return "-", "-", "-", "-"
	}
	return strconv.FormatInt(usage.InputTokens, 10), strconv.FormatInt(usage.OutputTokens, 10),
		strconv.FormatInt(usage.CacheCreationTokens, 10), strconv.FormatInt(usage.CacheReadTokens, 10)
}

func printSpendSubtotal(writer io.Writer, day string, subtotal runstate.SpendRow) {
	if day == "" {
		return
	}
	fmt.Fprintf(writer, spendSubtotal, day+" total", "", subtotal.Calls,
		subtotal.Usage.InputTokens, subtotal.Usage.OutputTokens, subtotal.Usage.CacheCreationTokens, subtotal.Usage.CacheReadTokens, subtotal.CostUSD)
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
	total := runstate.SpendRow{Usage: &runstate.TokenUsage{}}
	byKind := map[runstate.StreamKind]runstate.SpendRow{}
	for _, row := range report.Rows {
		total.Calls += row.Calls
		total.CostUSD += row.CostUSD
		if row.Usage != nil {
			total.Usage.Merge(*row.Usage)
		}
		summed := byKind[row.Kind]
		summed.Calls += row.Calls
		summed.CostUSD += row.CostUSD
		byKind[row.Kind] = summed
	}
	window := ""
	if report.Days > 0 {
		window = fmt.Sprintf(" (last %d days)", report.Days)
	}
	// A total that is missing an exchange nobody could read is a lower bound,
	// and is marked the way every other floor in this project is marked. The
	// mark is empty otherwise, so a report with everything in front of it prints
	// exactly the figure it always did.
	mark := ""
	if report.Floor() {
		mark = "≥ "
	}
	fmt.Fprintln(writer, spendRule)
	fmt.Fprintf(writer, spendTotal, "TOTAL"+window, "", total.Calls,
		total.Usage.InputTokens, total.Usage.OutputTokens, total.Usage.CacheCreationTokens, total.Usage.CacheReadTokens,
		mark+fmt.Sprintf("%.2f", total.CostUSD))
	money := mark + fmt.Sprintf("$%.2f", total.CostUSD)
	tokens := total.Usage.InputTotal() + total.Usage.OutputTokens
	// An exchange records what the provider charged and not what it used, so a
	// report of nothing but exchanges has money and no tokens. Saying so beats
	// dividing by a total of none of them.
	if tokens > 0 {
		fmt.Fprintf(writer, "\ntokens: %s total (%.1f%% cache reads)   cost: %s\n",
			groupThousands(tokens), float64(total.Usage.CacheReadTokens)*100/float64(tokens), money)
	} else {
		fmt.Fprintf(writer, "\ncost: %s; no token usage is recorded for what this covers\n", money)
	}
	if split := renderKindSplit(byKind); split != "" {
		fmt.Fprintln(writer, split)
	}
	if asks, spent := byKind[runstate.StreamExchange]; spent && asks.Calls > 0 {
		fmt.Fprintln(writer, "an exchange records what the provider charged and not what it used, so its rows carry no tokens")
	}
}

// renderKindSplit says how much of a total was each kind of work, and says it
// only when the total is actually mixed, so the one number is never left
// ambiguous about what went into it. A conversation turn, a branch review, and
// a round of one role asking another are each a provider invocation like any
// other and are priced beside the runs: a total that skipped any of them would
// be wrong rather than unattributed.
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
		{runstate.StreamExchange, "exchanges", "round(s)"},
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

// printUnreadableExchanges names the exchange records the report could not
// read, so somebody can go and look at them. They are counted rather than
// dropped: a record nobody can parse did not cost nothing, and a total short by
// a thread nobody was told about is the mistake the whole unknown-never-zero
// discipline exists to stop.
func printUnreadableExchanges(writer io.Writer, report runstate.SpendReport) {
	if !report.Floor() {
		return
	}
	fmt.Fprintf(writer, "%d exchange record(s) could not be read; they are left out rather than counted as nothing, so any total here is a floor:\n",
		len(report.UnreadableExchanges))
	for _, id := range report.UnreadableExchanges {
		fmt.Fprintf(writer, "  %s\n", id)
	}
	if report.UnreadableReason != "" {
		fmt.Fprintf(writer, "  the first of them: %s\n", singleLine(report.UnreadableReason))
	}
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
		fmt.Fprintln(stderr, describeNoStreams(store, options.kinds, options.match, false))
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
			fmt.Fprintf(stderr, "waiting for a %s to start under %s ...\n",
				describeStreamKinds(options.kinds, false, false), store.Root())
			announced = true
		}
		select {
		case <-ctx.Done():
			return runstate.Stream{}, false, ctx.Err()
		case <-time.After(options.lookInterval()):
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

	poll := time.NewTicker(options.pollInterval())
	defer poll.Stop()
	look := time.NewTicker(options.lookInterval())
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
