package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"yoyodyne/internal/chat"
	"yoyodyne/internal/contextbundle"
	"yoyodyne/internal/execution"
)

// maxInteractionsBytes bounds how much of the tracker's own log is read to
// count what has changed. It is generous for a log of work-item changes and
// bounded for the same reason every other read here is: an operator asking how
// fresh a conversation is must get an answer, not a command that reads until it
// runs out of memory.
const maxInteractionsBytes = 16 << 20

// interactionsLog is where the tracker records what has happened to work items.
// It is the tracker's own file rather than something the harness maintains, and
// it is read rather than asked for because the question — how much has moved
// since a moment — is one line count and no process at all.
const interactionsLog = ".beads/interactions.jsonl"

// conversationGround is where a conversation's picture of the product comes
// from, and what that picture is compared against to say how old it is. It is
// the same specifications and the same tracker the conversation opened with,
// reachable again so a running conversation can be brought up to date instead of
// being thrown away and started over.
//
// None of it is reachable by the product manager. Gathering and comparing are
// the harness's own actions on the operator's instruction, exactly like running
// a work item.
type conversationGround struct {
	runner         execution.ProcessRunner
	repository     string
	specifications string
	gitBinary      string
	clock          execution.Clock
	timeout        time.Duration
}

func newConversationGround(parts components) conversationGround {
	return conversationGround{
		runner:         parts.runner,
		repository:     parts.repository,
		specifications: parts.config.Product.Specifications,
		gitBinary:      "git",
		clock:          execution.RealClock{},
		timeout:        chatTrackerTimeout,
	}
}

// Gather assembles the product context and records what it was assembled
// against: the moment, and the commit the repository was on. A tracker that
// cannot be read is reported in the context and in the problems rather than
// silently rendered as a product with no work in flight, and a specification
// that does not follow the required structure is reported the same way rather
// than dropped.
func (g conversationGround) Gather(ctx context.Context) (chat.Briefing, error) {
	trackerCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	items, listErr := chatTracker(g.runner, g.repository).List(trackerCtx, chatWorkItemStatus)
	briefing := chat.Briefing{GatheredAt: g.now()}
	unavailable := ""
	if listErr != nil {
		unavailable = listErr.Error()
		briefing.Problems = append(briefing.Problems, fmt.Sprintf("Beads state is unavailable, continuing without it: %v", listErr))
	}
	bundle, err := contextbundle.AssembleProduct(contextbundle.ProductRequest{
		RepositoryRoot:          g.repository,
		SpecificationsDirectory: g.specifications,
		WorkItems:               items,
		WorkItemsUnavailable:    unavailable,
	})
	if err != nil {
		return chat.Briefing{}, fmt.Errorf("assemble product context: %w", err)
	}
	for _, problem := range bundle.SpecificationProblems {
		briefing.Problems = append(briefing.Problems, "specification "+problem.String())
	}
	if len(bundle.References) == 0 {
		briefing.Problems = append(briefing.Problems,
			fmt.Sprintf("no specification was found under %s; the product manager has no recorded product intent to reason over", g.specifications))
	}
	briefing.Text = bundle.Text
	// The commit is evidence rather than a requirement: a repository that will
	// not say what it is on still yields a usable picture, and the comparison
	// that needs the commit reports itself as unknown when the time comes.
	commit, err := g.head(ctx)
	if err == nil {
		briefing.Commit = commit
	}
	return briefing, nil
}

// Movement reports what the repository and the tracker have done since a
// picture was taken. Neither half can fail the answer: a comparison that could
// not be made is named, because an operator told "0 commits" by a broken
// comparison is worse off than one told the comparison did not work.
func (g conversationGround) Movement(ctx context.Context, since chat.Briefing) chat.Movement {
	movement := chat.Movement{}
	commits, err := g.commitsSince(ctx, since.Commit)
	if err != nil {
		movement.RepositoryProblem = err.Error()
	} else {
		movement.Commits = commits
	}
	changes, err := trackerChangesSince(g.repository, since.GatheredAt)
	if err != nil {
		movement.TrackerProblem = err.Error()
	} else {
		movement.TrackerChanges = changes
	}
	return movement
}

// commitsSince counts the commits the repository has taken on since a picture
// was taken. It counts against the recorded commit rather than against a time,
// because that is the exact question — what does HEAD hold that the picture did
// not — and a commit's own date answers a different one.
func (g conversationGround) commitsSince(ctx context.Context, commit string) (int, error) {
	if strings.TrimSpace(commit) == "" {
		return 0, errors.New("the commit it was gathered at was not recorded")
	}
	result, err := g.git(ctx, "rev-list", "--count", commit+"..HEAD")
	if err != nil {
		return 0, err
	}
	if result.Status != execution.ProcessSucceeded {
		return 0, fmt.Errorf("git rev-list failed: %s", singleLine(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	count, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		return 0, fmt.Errorf("git rev-list did not answer with a count: %s", singleLine(result.Stdout))
	}
	return count, nil
}

func (g conversationGround) head(ctx context.Context) (string, error) {
	result, err := g.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("git rev-parse failed: %s", singleLine(firstNonEmpty(result.Stderr, result.Stdout)))
	}
	commit := strings.TrimSpace(result.Stdout)
	if commit == "" {
		return "", errors.New("git rev-parse named no commit")
	}
	return commit, nil
}

func (g conversationGround) git(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	result, err := g.runner.Run(ctx, execution.Command{
		Name:    g.gitBinary,
		Args:    args,
		Dir:     g.repository,
		Timeout: g.timeout,
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run Git command: %w", err)
	}
	return result, nil
}

func (g conversationGround) now() time.Time {
	if g.clock == nil {
		return execution.RealClock{}.Now()
	}
	return g.clock.Now()
}

// trackerChangesSince counts what the tracker recorded after a moment, from the
// tracker's own interactions log. The log is an export rather than the tracker's
// live state, so a change it has not written down yet is not counted: the count
// is a floor on what has moved, which is the safe direction for a number an
// operator uses to decide whether to refresh.
func trackerChangesSince(repository string, since time.Time) (int, error) {
	if since.IsZero() {
		return 0, errors.New("when it was gathered was not recorded")
	}
	file, err := os.Open(filepath.Join(repository, interactionsLog))
	if errors.Is(err, os.ErrNotExist) {
		// A repository with no tracker at all has recorded nothing, and nothing
		// having changed is an answer. A repository that has a tracker and no
		// log is a different thing: the log is written by the tracker's own
		// export, a project that has not enabled it has one that is missing or
		// behind, and answering "no tracker changes" there would be exactly the
		// false currency this is here to prevent.
		if _, statErr := os.Stat(filepath.Join(repository, filepath.Dir(interactionsLog))); statErr == nil {
			return 0, errors.New("the tracker has not exported its interactions log")
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open the tracker's interactions log: %w", err)
	}
	defer file.Close()

	changes := 0
	scanner := bufio.NewScanner(io.LimitReader(file, maxInteractionsBytes))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var interaction struct {
			CreatedAt time.Time `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(line), &interaction); err != nil {
			// One unreadable entry is not a log that could not be read, and
			// failing the whole count over it would report the tracker as
			// unknown for a single malformed line.
			continue
		}
		if interaction.CreatedAt.After(since) {
			changes++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read the tracker's interactions log: %w", err)
	}
	return changes, nil
}

// singleLine folds a command's own output into one line, so a Git failure stays
// a clause inside the freshness line rather than becoming several lines of its
// own.
func singleLine(value string) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= 160 {
		return folded
	}
	return strings.TrimSpace(folded[:160]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
