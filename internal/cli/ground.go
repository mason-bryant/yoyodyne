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
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
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
	// shippedDocumentation is the operator-facing documentation this project
	// says it ships, from product.shipped_documentation. It comes from the
	// configuration rather than from a set the harness holds, because the
	// harness's own set names this repository's documents and would describe an
	// adopting project with paths that are only coincidentally there.
	shippedDocumentation []string
	// roleDocuments are the directories this role reads beyond the
	// specifications. The product manager has none by design, and every other
	// role is answering for documents it would otherwise have to be told the
	// contents of by the operator.
	roleDocuments []contextbundle.DocumentSet
	// docket is the work that has stopped moving. It is wired for the
	// development manager alone, because that is the role that decides what
	// becomes of a stoppage, and gathering it is what puts the docket in front of
	// that role without an operator carrying it there. Every other role leaves it
	// nil and gathers no docket at all.
	docket    *orchestrator.Docketer
	gitBinary string
	clock     execution.Clock
	timeout   time.Duration
}

func newConversationGround(parts components, role domain.AgentRole) conversationGround {
	return conversationGround{
		runner:               parts.runner,
		repository:           parts.repository,
		specifications:       parts.config.Product.Specifications,
		shippedDocumentation: parts.config.Product.ShippedDocumentation,
		roleDocuments:        roleDocumentSets(role, parts.config.Product),
		docket:               conversationDocket(parts, role),
		gitBinary:            "git",
		clock:                execution.RealClock{},
		timeout:              chatTrackerTimeout,
	}
}

// conversationDocket wires the triage docket for the role that decides about
// what it carries, and for no other. A development manager is the only role
// that can act on stopped work, and a docket delivered to a role that cannot is
// a section every conversation pays for and reads past.
func conversationDocket(parts components, role domain.AgentRole) *orchestrator.Docketer {
	if role != domain.RoleDevelopmentManager {
		return nil
	}
	return docketerFrom(parts)
}

// conversationTriage wires the durable per-item triage budget for the role that
// spends it, and for no other. It is the same record `yoyo status` reports and
// the same caps the docket reports against, so a decision the development
// manager records and the figure an operator reads afterwards can never be
// working from different numbers.
func conversationTriage(parts components, role domain.AgentRole) chat.TriageBudgets {
	if role != domain.RoleDevelopmentManager {
		return nil
	}
	return conversationTriageBudgets{
		store:  parts.store.Triage(),
		caps:   orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage),
		rounds: orchestrator.TriageRepairGrantRounds(parts.config.Triage),
		clock:  execution.RealClock{},
	}
}

// conversationTriageBudgets is what a conversation supplies that the role does
// not get to assert: what the project configured an item may be given, and when
// the giving happened. The role decides; the sizes and the clock are the
// harness's.
type conversationTriageBudgets struct {
	store  *runstate.TriageStore
	caps   runstate.TriageCaps
	rounds int
	clock  execution.Clock
}

func (b conversationTriageBudgets) GrantRepair(ctx context.Context, workItemID string) (runstate.RepairGrant, error) {
	return b.store.GrantRepair(ctx, workItemID, b.rounds, b.clock.Now(), b.caps)
}

func (b conversationTriageBudgets) RecordRerun(ctx context.Context, workItemID string) (runstate.TriageCounters, error) {
	return b.store.RecordRerun(ctx, workItemID, b.clock.Now(), b.caps)
}

func (b conversationTriageBudgets) RecordMergeRearm(ctx context.Context, workItemID string) (runstate.TriageCounters, error) {
	return b.store.RecordMergeRearm(ctx, workItemID, b.clock.Now(), b.caps)
}

// conversationStoppages wires the durable run records a triage decision is
// checked against, and that a decomposition's substrate gate is decided from,
// for the role that does both and for no other. It is the same store the docket
// was built from, so what a decision says it is about and what the entry it came
// from said can never be two different accounts of one run.
func conversationStoppages(parts components, role domain.AgentRole) chat.Stoppages {
	if role != domain.RoleDevelopmentManager {
		return nil
	}
	return conversationStoppedRuns{store: parts.store}
}

// conversationStoppedRuns answers two questions from the run records: which
// work item a run was made for, and where a work item's own change actually is.
// The records are the harness's own, written as the run went, so both are
// evidence rather than anything the conversation asserted.
type conversationStoppedRuns struct {
	store *runstate.Store
}

func (r conversationStoppedRuns) WorkItemOf(_ context.Context, runID string) (string, error) {
	state, err := r.store.Load(runID)
	if err != nil {
		return "", err
	}
	return state.WorkItemID, nil
}

// UnlandedChange reports the change an item's own work produced that never
// reached the integration target, which is the substrate a child decomposed out
// of that item would be written against.
//
// It walks the item's runs newest first rather than reading the newest one,
// because the newest run is not always the run that says where the change is.
// The question is what the item's work has left lying off the target branch, and
// a run answers it in one of four ways.
//
// A recorded integration is the promotion itself, and it ends the walk: the
// change is on the target branch, and nothing an earlier run did is missing any
// more. A run still going has not failed to land anything yet, so it is not
// evidence either way and the walk carries on past it — holding a decomposition
// against work in flight would hold it against a state that resolves itself,
// while stopping there would report an older stopped run's branch as though
// nothing were being done about it. A terminal run with no recorded change and
// no commit produced nothing, so it says nothing at all about where the work is,
// and the walk carries on past that too; reading it as "nothing is missing" is
// exactly the hole that let a child be carved against a previous run's branch
// while a re-run that wrote no code sat in front of it. What is left is a
// terminal run that made a change and did not promote it, which is the answer.
//
// An item whose every run falls through — never run, only ever in flight, only
// ever empty — has nothing off the target branch, which is nearly every
// decomposition and every item whose execution is a conversation rather than a
// run at all.
func (r conversationStoppedRuns) UnlandedChange(_ context.Context, workItemID string) (chat.UnlandedChange, bool, error) {
	runs, err := r.store.Runs(workItemID)
	if err != nil {
		return chat.UnlandedChange{}, false, err
	}
	for _, state := range runs {
		if state.Integration != nil {
			return chat.UnlandedChange{}, false, nil
		}
		if !state.Status.Terminal() || (state.Changes == nil && state.HarnessCommit == "") {
			continue
		}
		// The branch is named whether or not the harness has since removed it. A
		// removed branch does not make the change any more findable, and a reader
		// deciding which vehicle lands it is owed the name either way.
		unlanded := chat.UnlandedChange{
			RunID:        state.RunID,
			Branch:       state.Branch,
			TargetBranch: state.TargetBranch,
		}
		if state.PullRequest != nil {
			unlanded.PullRequest = state.PullRequest.Number
		}
		return unlanded, true, nil
	}
	// Work the harness has never run, and work whose runs left nothing behind,
	// are both a plain answer about the item rather than a failure to look.
	return chat.UnlandedChange{}, false, nil
}

// roleDocumentSets names the documents a role reads beyond the specifications.
//
// The product manager reads none of them, and that is a decision rather than an
// omission: its evidence is product intent and a description of what ships, and
// giving it the designs would let how the product is built argue about what it
// is for. Every other role is the opposite case — an architect that cannot see
// the designs it owns is answering from memory.
//
// The invariants are listed before the decision records they are extracted from
// so they arrive labelled as the constraints they are; a directory nested inside
// another is carried once, under the label it was first read as.
func roleDocumentSets(role domain.AgentRole, product config.Product) []contextbundle.DocumentSet {
	designs := contextbundle.DocumentSet{Label: "Design", Directory: product.Designs}
	invariants := contextbundle.DocumentSet{Label: "Architectural invariant", Directory: product.Invariants}
	decisions := contextbundle.DocumentSet{Label: "Decision record", Directory: product.Decisions}
	switch role {
	case domain.RoleProductManager:
		return nil
	case domain.RoleArchitect:
		return []contextbundle.DocumentSet{designs, invariants, decisions}
	default:
		return []contextbundle.DocumentSet{designs, invariants}
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
	docket, docketUnavailable, docketProblem := g.triageDocket()
	if docketProblem != "" {
		briefing.Problems = append(briefing.Problems, docketProblem)
	}
	bundle, err := contextbundle.AssembleProduct(contextbundle.ProductRequest{
		RepositoryRoot:          g.repository,
		SpecificationsDirectory: g.specifications,
		ShippedDocumentation:    g.shippedDocumentation,
		RoleDocuments:           g.roleDocuments,
		WorkItems:               items,
		WorkItemsUnavailable:    unavailable,
		TriageDocket:            docket,
		TriageDocketUnavailable: docketUnavailable,
		CommandHelp:             commandHelp(),
	})
	if err != nil {
		return chat.Briefing{}, fmt.Errorf("assemble product context: %w", err)
	}
	for _, problem := range bundle.SpecificationProblems {
		briefing.Problems = append(briefing.Problems, "specification "+problem.String())
	}
	if bundle.SpecificationsIncluded == 0 {
		briefing.Problems = append(briefing.Problems,
			fmt.Sprintf("no specification was found under %s; this conversation has no recorded product intent to reason over", g.specifications))
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

// triageDocket builds the docket and reports what it found: the entries, why
// there are none when reading them failed, and a problem to tell the operator
// about. Building it is a scan rather than an event, which is the whole reason
// it happens here — the harness has no scheduled process, so a publication the
// forge quietly never merged is found when somebody who can act on it opens a
// conversation, and the configured stuck-merge age is a floor rather than a
// promise about when that happens.
//
// A docket that could only be built in part still reaches the conversation. The
// entries that were found are exactly the ones somebody needs, and the problem
// is reported beside them rather than instead of them.
func (g conversationGround) triageDocket() (entries []triage.Entry, unavailable, problem string) {
	if g.docket == nil {
		return nil, "", ""
	}
	built, err := g.docket.Build()
	if err == nil {
		return built.Entries, "", ""
	}
	if len(built.Entries) == 0 {
		return nil, err.Error(), fmt.Sprintf("the triage docket could not be read, continuing without it: %v", err)
	}
	return built.Entries, "", fmt.Sprintf("the triage docket is incomplete: %v", err)
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
	changes, err := trackerChangesSince(g.repository, since.GatheredAt, maxInteractionsBytes)
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
func trackerChangesSince(repository string, since time.Time, limit int) (int, error) {
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
	// The log is read to a bound, and how much was actually read is what says
	// whether the bound was reached. Stopping at it silently would drop the
	// newest entries — the only ones this counts — and answer "nothing has
	// moved" from a comparison that was truncated, which is the one answer this
	// must never give without having earned it.
	counted := &countingReader{reader: io.LimitReader(file, int64(limit)+1)}
	scanner := bufio.NewScanner(counted)
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
	if counted.read > limit {
		return 0, fmt.Errorf("the tracker's interactions log is larger than the %d bytes this reads, so what it holds cannot be counted", limit)
	}
	return changes, nil
}

// countingReader reports how much was actually read, which is what tells a
// bounded read apart from one that stopped at its bound.
type countingReader struct {
	reader io.Reader
	read   int
}

func (c *countingReader) Read(buffer []byte) (int, error) {
	read, err := c.reader.Read(buffer)
	c.read += read
	return read, err
}

// maxSingleLineBytes bounds a folded line. It is what keeps a listing a listing:
// a reviewer's verdict and a Git failure are both as long as whoever wrote them
// made them, and neither is allowed to wrap across a terminal.
const maxSingleLineBytes = 160

// singleLine folds a command's own output into one line and bounds it, so a Git
// failure stays a clause inside the freshness line rather than becoming several
// lines of its own, and a recorded reason stays one row of a listing. The cut
// falls on a rune boundary and is marked, because what is folded here is prose
// somebody wrote — this repository's own em dashes among it — and half a rune is
// not a shorter reason but a broken one.
// singleLine and internal/backend's boundFailureDetail share the same
// walk-back-to-a-rune-start fold with different bounds (160 bytes here, 512
// there); a fix to either almost certainly belongs in both.
func singleLine(value string) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= maxSingleLineBytes {
		return folded
	}
	cut := maxSingleLineBytes
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
