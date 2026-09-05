package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "config validate") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestRunVersionPrintsTheBareVersion pins the exact bytes of `yoyo version`,
// because two things outside this package compare them literally: `make
// dist-verify` refuses to package a release whose binary does not report the
// tag it was built from, and the release workflow runs that target. A banner, a
// "yoyo " prefix, or a second line here would not fail any other test and would
// instead fail at a tag push, which is the one place a failure means a botched
// or missing release.
func TestRunVersionPrintsTheBareVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"version"}, &stdout, &stderr, "v0.1.0")
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "v0.1.0\n" {
		t.Fatalf("stdout = %q, want exactly %q", stdout.String(), "v0.1.0\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing", stderr.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"version", "--json"}, &stdout, &stderr, "v0.1.0")
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["version"] != "v0.1.0" {
		t.Fatalf("version = %q", result["version"])
	}
}

func TestRunConfigValidateJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, validConfig)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["status"] != "valid" {
		t.Fatalf("status = %v", result["status"])
	}
	if result["product_id"] != "yoyodyne" {
		t.Fatalf("product_id = %v", result["product_id"])
	}
}

func TestRunConfigValidateInvalidJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "version: 2\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["status"] != "invalid" {
		t.Fatalf("status = %v", result["status"])
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"unknown"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunWorkItemRequiresExactlyOneID(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"run"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "exactly one Beads work item id") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// Reconcile sweeps every outstanding run, so it names no run of its own. A
// configuration it cannot load is reported as the machine-readable failure of a
// sweep that settled nothing, rather than as an empty success.
func TestReconcileRefusesArgumentsAndReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"reconcile", "yoyodyne-task"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code := Run([]string{"reconcile", "--config", missing, "--json"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result reconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Error == "" || len(result.Runs) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

// The supervision sweep is wired into `yoyo reconcile` rather than tested only
// as a loop built by hand, because the wiring is where the two failures nobody
// else catches live: a loop the configuration cannot build at all, and a
// supervision problem joined into the sweep's error so a `reconcile` that
// settled every run cleanly still exits 1.
//
// A product whose roles have never asked each other anything is the ordinary
// case and the one that has to be silent — the exchange directory does not even
// exist yet. What this pins is that the command runs the real
// reconcileRuns/sweepSupervision/supervisionLoopFrom path over a real
// configuration and comes back with nothing said and a zero exit.
//
// It also settles the question the wiring raises: supervisionLoopFrom hands the
// conductor Product.RepositoryID, and an empty one would refuse the whole pass.
// Configuration resolution defaults it to the product id and then validates it
// as an identifier, so a loaded configuration cannot carry an empty one — and
// this is what would fail if that ever stopped being true.
func TestReconcileSweepsSupervisionOverARealConfigurationAndSaysNothingWhenThereIsNothing(t *testing.T) {
	// t.Setenv rules out t.Parallel, and the state root has to be this test's own:
	// the sweep reads and writes the product's durable records.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())

	project := t.TempDir()
	git(t, project, "init", "-b", "main")
	git(t, project, "config", "user.name", "Yoyodyne Test")
	git(t, project, "config", "user.email", "yoyodyne@example.invalid")
	commit(t, project, "first")

	directory := filepath.Join(project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(configPath, []byte(validConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"reconcile", "--config", configPath, "--json"}, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result reconcileOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout.String())
	}
	// The whole point of running the real path: a supervision problem would be
	// joined in here and would have taken the exit status with it.
	if result.Error != "" {
		t.Fatalf("Error = %q, want a sweep with nothing to recover to report nothing", result.Error)
	}
	if len(result.Supervision) != 0 {
		t.Fatalf("Supervision = %#v, want nothing from a product whose roles have never asked each other anything", result.Supervision)
	}
}

// The sweep carries no voice, so every exchange it finds free and deliverable
// comes back undelivered — which for a thread simply waiting its turn is not
// something the sweep did. Those stay out of what a person reads and stay in
// `--json`, exactly as the branches and publications this leaves unprinted do.
func TestReconcileReportsWhatItRecoveredRatherThanWhatItDeclinedToDeliver(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// One of every outcome, so what is printed is decided by the classification
	// rather than by which of them this test happened to list.
	sweep := reconcileSweep{Supervision: []orchestrator.SupervisionResult{
		{
			ExchangeID: "exchange-00000000000000000000000000000001",
			Outcome:    orchestrator.SupervisionUndelivered,
			Detail:     "no voice is wired to this pass",
		},
		{
			ExchangeID: "exchange-00000000000000000000000000000002",
			Outcome:    orchestrator.SupervisionCarried,
			Detail:     "a live process is holding this one",
		},
		{
			ExchangeID: "exchange-00000000000000000000000000000003",
			Outcome:    orchestrator.SupervisionReclaimed,
			Detail:     "round 2 yoyo pid 7 was carrying it and is gone",
		},
		{
			ExchangeID: "exchange-00000000000000000000000000000004",
			Outcome:    orchestrator.SupervisionQueued,
			Detail:     "4 exchange(s) already have a round open",
		},
		{
			ExchangeID: "exchange-00000000000000000000000000000005",
			Outcome:    orchestrator.SupervisionStale,
			Detail:     "nothing current is known about goal/autonomy, so they were not judged",
		},
		{
			ExchangeID: "exchange-00000000000000000000000000000006",
			Outcome:    orchestrator.SupervisionSettled,
			Detail:     "all 2 of its permitted rounds are spent",
		},
	}}
	if code := reportReconcileResult(&stdout, &stderr, false, sweep, nil); code != 0 {
		t.Fatalf("reportReconcileResult() code = %d; stderr = %q", code, stderr.String())
	}
	// Every outcome that describes what the sweep found rather than what it did.
	// A queued thread and a stale one are the two that matter here: a product with
	// several open exchanges has one of each on every sweep, and printing them
	// would bury the lines that say a record changed.
	for _, quiet := range []string{
		"exchange-00000000000000000000000000000001",
		"exchange-00000000000000000000000000000002",
		"exchange-00000000000000000000000000000004",
		"exchange-00000000000000000000000000000005",
	} {
		if strings.Contains(stdout.String(), quiet) {
			t.Errorf("stdout = %q, want %s left out: nothing was done to it", stdout.String(), quiet)
		}
	}
	for _, said := range []string{
		"exchange-00000000000000000000000000000003",
		"exchange-00000000000000000000000000000006",
	} {
		if !strings.Contains(stdout.String(), said) {
			t.Errorf("stdout = %q, want %s reported: a record was changed", stdout.String(), said)
		}
	}

	var jsonOut bytes.Buffer
	if code := reportReconcileResult(&jsonOut, &stderr, true, sweep, nil); code != 0 {
		t.Fatalf("reportReconcileResult() code = %d; stderr = %q", code, stderr.String())
	}
	var result reconcileOutput
	if err := json.Unmarshal(jsonOut.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Supervision) != len(sweep.Supervision) {
		t.Fatalf("Supervision = %#v, want the whole pass carried", result.Supervision)
	}
}

// The printing is decided by a closed classification, so an outcome added later
// and named on neither side fails here rather than being printed as though the
// sweep had acted on it. That is the mistake this replaces: a skip list that did
// not keep up with the outcomes beside it.
func TestEverySupervisionOutcomeIsClassified(t *testing.T) {
	t.Parallel()

	acted := map[orchestrator.SupervisionOutcome]bool{
		orchestrator.SupervisionReclaimed: true,
		orchestrator.SupervisionSettled:   true,
	}
	observed := map[orchestrator.SupervisionOutcome]bool{
		orchestrator.SupervisionCarried:     true,
		orchestrator.SupervisionQueued:      true,
		orchestrator.SupervisionStale:       true,
		orchestrator.SupervisionUndelivered: true,
	}
	for _, outcome := range orchestrator.SupervisionOutcomes() {
		if acted[outcome] == observed[outcome] {
			t.Fatalf("%q is on neither side of the division, or on both; decide whether a sweep reporting it changed a record", outcome)
		}
		if supervisionActed(outcome) != acted[outcome] {
			t.Errorf("supervisionActed(%q) = %t, want %t", outcome, supervisionActed(outcome), acted[outcome])
		}
	}
}

// A settle catches its target branch up itself, so the report says so on the
// run that did it. A catch-up it held is the fact somebody has to read, which
// is why it goes to stderr — and why it is still not a failure: the branch is
// behind, nothing is owed, and the next sweep takes it.
func TestReconcileReportsTheCatchUpASettleMadeAndTheOneItHeld(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	results := []orchestrator.Reconciliation{
		{
			RunID:      "run-advanced",
			WorkItemID: "yoyodyne-task",
			Action:     orchestrator.ActionCompleted,
			Catchup:    &gitworktree.Catchup{TargetBranch: "main", RemoteCommit: "abc1234", Advanced: true},
		},
		{
			RunID:      "run-held",
			WorkItemID: "yoyodyne-other",
			Action:     orchestrator.ActionCompleted,
			Catchup:    &gitworktree.Catchup{TargetBranch: "main", Held: "the primary checkout has unsaved changes"},
		},
	}
	code := reportReconcileResult(&stdout, &stderr, false, reconcileSweep{Runs: results}, nil)
	if code != 0 {
		t.Fatalf("reportReconcileResult() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "main caught up to abc1234") {
		t.Errorf("stdout = %q, want the catch-up the settle made", stdout.String())
	}
	if strings.Contains(stdout.String(), "not caught up") {
		t.Errorf("stdout = %q, want the held catch-up kept off stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), "main not caught up: the primary checkout has unsaved changes") {
		t.Errorf("stderr = %q, want the held catch-up and why", stderr.String())
	}
}

// The sweep's own verdict and what became of the run are two different facts,
// and a recovery view that printed only the first told an operator a run was
// settled and nothing about whether their change survived it. Both words come
// from the read model, so the same run described here and in `yoyo status`
// cannot be described differently.
func TestReconcileSaysWhatBecameOfEachRunAndWhatRemainsOfIt(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	results := []orchestrator.Reconciliation{
		{
			RunID:        "run-stopped",
			WorkItemID:   "yoyodyne-stopped",
			Action:       orchestrator.ActionBlocked,
			Status:       runstate.StatusFailed,
			Outcome:      runstate.OutcomeStopped,
			Branch:       "yoyodyne/stopped",
			WorktreePath: "/tmp/worktrees/run-stopped",
		},
		{
			RunID:         "run-broke",
			WorkItemID:    "yoyodyne-broke",
			Action:        orchestrator.ActionFailed,
			Status:        runstate.StatusFailed,
			Outcome:       runstate.OutcomeFailed,
			Branch:        "yoyodyne/broke",
			BranchRemoved: true,
		},
		// A run still owed a step has no ending yet, so the sweep says what it did
		// and stops there rather than reporting an outcome nothing reached.
		{
			RunID:      "run-held",
			WorkItemID: "yoyodyne-held",
			Action:     orchestrator.ActionHeld,
			Status:     runstate.StatusRunning,
			Outcome:    runstate.RunOutcome(runstate.StatusRunning),
		},
	}
	if code := reportReconcileResult(&stdout, &stderr, false, reconcileSweep{Runs: results}, nil); code != 0 {
		t.Fatalf("reportReconcileResult() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	printed := stdout.String()
	// The word the vocabulary exists for: a run handed to a person with its
	// change intact is never reported with the same word as one that broke.
	if !strings.Contains(printed, "stopped, work preserved") {
		t.Errorf("stdout = %q, want the stoppage said as stopped with its work preserved", printed)
	}
	if !strings.Contains(printed, "failed, work removed") {
		t.Errorf("stdout = %q, want the broken run said as failed with its work removed", printed)
	}
	if strings.Contains(printed, "running,") {
		t.Errorf("stdout = %q, want no ending claimed for a run that has not reached one", printed)
	}
}

// Chat talks to whichever agent fills the product-manager role, with the
// persona that role resolved to. In this repository both are stated in the
// project configuration and read from the project's own personas directory.
func TestChatResolvesTheConfiguredProductManager(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", config.DirectoryName, config.FileName)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	agent := agentForRole(resolved.Config, domain.RoleProductManager)
	if agent.Role != domain.RoleProductManager {
		t.Fatal("no product-manager agent is configured; chat would have nobody to talk to")
	}
	if agent.Backend != domain.BackendClaudeCode {
		t.Fatalf("product-manager backend = %q, want %q", agent.Backend, domain.BackendClaudeCode)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		t.Fatalf("product-manager model: %v", err)
	}
	if origin := resolved.Origins["agents.product-manager.persona"]; origin != resolved.Path {
		t.Fatalf("product-manager persona origin = %q, want the project configuration", origin)
	}
	if !strings.Contains(agent.Persona.Text, "You own product intent, not implementation.") {
		t.Fatalf("product-manager persona = %q", agent.Persona.Text)
	}
	// The persona is guidance underneath the contract, never a replacement for
	// it: the contract is what the conversation actually sends first.
	prompt := chat.SystemPrompt(agent.Role, chat.Admission{}, agent.Persona.Text)
	if !strings.HasPrefix(prompt, "You are the product manager for this product") {
		t.Fatal("the conversation prompt does not begin with the immutable contract")
	}
	// How the product manager briefs the operator -- one question a reply, and a
	// count with a named ordering when it holds several -- is persona guidance,
	// so it reaches the model only by riding under the contract in this prompt.
	for _, want := range []string{
		"Ask exactly one question per reply",
		"open with how many there are",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the conversation prompt does not carry %q", want)
		}
	}
	// A string in a prompt is not a reply, and no check here can become one: a
	// test takes no product-manager turn, and a developer worktree has no
	// provider to take one with, so `yoyo chat` run from inside one fails at
	// authentication before any prompt is evaluated. Whether the opening reply
	// really does arrive as a count, a named ordering, and a single question is
	// verified by a person, with `yoyo chat --new` in a repository that has no
	// specifications directory. That is written down here rather than left
	// implied, because a criterion nobody can see going unmet is one that goes
	// unmet quietly.
}

func TestChatRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "positional message",
			args: []string{"chat", "what is the brief?"},
			want: "does not accept positional arguments",
		},
		{
			// An interactive conversation has no single result, so machine
			// readable output has to name the turn it describes.
			name: "json without a message",
			args: []string{"chat", "--json"},
			want: "requires --message",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr, "test")
			if code != 2 {
				t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), test.want)
			}
		})
	}
}

// The tracker a conversation acts through is the one path an approved proposal
// is created by, so what it would actually run is checked here rather than
// assumed: the project's repository, the bd binary, and a bound, so an
// unresponsive tracker cannot hang an operator waiting at the approval prompt.
func TestChatTrackerRunsBoundedCommandsInTheRepository(t *testing.T) {
	t.Parallel()

	// It is what chat approves through, which is a compile-time fact worth
	// stating where the construction lives.
	var _ chat.Tracker = chatTracker(nil, "")

	runner := &recordingRunner{stdout: `{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open","issue_type":"task"}`}
	tracker := chatTracker(runner, "/repo")
	// The bound is stated here rather than inherited from whatever the adapter
	// happens to default to, so a change to that default cannot quietly leave
	// an operator waiting at the approval prompt.
	if tracker.Timeout != chatTrackerTimeout || tracker.Dir != "/repo" {
		t.Fatalf("chatTracker() = %#v", tracker)
	}
	created, err := tracker.Create(context.Background(), beads.NewWorkItem{
		Title:       "Pause on a usage limit",
		Description: "Wait and resume.",
		Type:        "task",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != "yoyodyne-9" {
		t.Fatalf("Create() = %#v", created)
	}
	runner.stdout = `{"issue_id":"yoyodyne-9","depends_on_id":"yoyodyne-1","status":"added"}`
	if err := tracker.AddBlocker(context.Background(), created.ID, "yoyodyne-1"); err != nil {
		t.Fatalf("AddBlocker() error = %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for _, command := range runner.commands {
		if command.Name != "bd" {
			t.Fatalf("command name = %q, want bd", command.Name)
		}
		if command.Dir != "/repo" {
			t.Fatalf("command dir = %q, want the product repository", command.Dir)
		}
		if command.Timeout != chatTrackerTimeout {
			t.Fatalf("command timeout = %s, want %s", command.Timeout, chatTrackerTimeout)
		}
	}
}

// recordingRunner records what a client would run without running it, so a
// construction that never reaches a real bd is still checked for what it asks.
type recordingRunner struct {
	stdout   string
	commands []execution.Command
}

func (r *recordingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, command)
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.stdout}, nil
}

// A one-shot message creates nothing, so proposals it produced are reported as
// proposals and the operator is told how to decide one. What they are told has
// to be something that works from where they are: a proposal is named by its own
// identifier, and the next message is what decides it.
func TestChatReportsProposalsAsUncreatedWork(t *testing.T) {
	t.Parallel()

	proposals := []chat.PendingProposal{{
		ID:             "1.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           1,
		Proposal: chat.Proposal{
			Title:       "Pause on a usage limit",
			Description: "Wait and resume.",
			Rationale:   "Capacity is not failure.",
		},
	}}
	var oneShot bytes.Buffer
	printChatProposals(&oneShot, domain.RoleProductManager, proposals)
	for _, required := range []string{"nothing was created for them", `approve 1.1`, `decline 1.1 <reason>`, "[1.1] Pause on a usage limit"} {
		if !strings.Contains(oneShot.String(), required) {
			t.Fatalf("one-shot output = %q, want it to contain %q", oneShot.String(), required)
		}
	}

	// A conversation that ended without a decision says so rather than leaving
	// the proposal to be assumed either way.
	var undecided bytes.Buffer
	// The theme a stream gets dresses nothing, so what is printed here is the
	// text and only the text.
	printUndecidedProposals(&undecided, console.Theme{}, proposals)
	if !strings.Contains(undecided.String(), "undecided") || !strings.Contains(undecided.String(), "[1.1]") {
		t.Fatalf("undecided output = %q", undecided.String())
	}

	var quiet bytes.Buffer
	printChatProposals(&quiet, domain.RoleProductManager, nil)
	printUndecidedProposals(&quiet, console.Theme{}, nil)
	if quiet.Len() != 0 {
		t.Fatalf("a turn with no proposals printed %q", quiet.String())
	}
}

// A question the product manager stopped to ask has nobody to answer it in a
// one-shot message, and a conversation can end with one unanswered. Both say so:
// silence about a concern reads as agreement, which is the thing it exists to
// prevent.
func TestChatReportsConcernsAsQuestionsNobodyHasAnswered(t *testing.T) {
	t.Parallel()

	concerns := []chat.PendingConcern{{
		ID:             "c1.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           1,
		Concern: chat.Concern{
			Kind:     chat.ConcernConflict,
			Subject:  "Let an agent merge its own work",
			Goal:     "No agent pushes or merges.",
			Detail:   "It is the thing that goal exists to prevent.",
			Question: "Do you want that goal changed?",
		},
	}}
	var oneShot bytes.Buffer
	printChatConcerns(&oneShot, console.Theme{}, domain.RoleProductManager, concerns)
	for _, required := range []string{
		"Nothing was proposed or created",
		"yoyodyne chat",
		"[c1.1] " + chat.ConcernConflict.Headline(),
		"Do you want that goal changed?",
	} {
		if !strings.Contains(oneShot.String(), required) {
			t.Fatalf("one-shot output = %q, want it to contain %q", oneShot.String(), required)
		}
	}

	// The theme a stream gets dresses nothing, so what is printed here is the
	// text and only the text.
	var open bytes.Buffer
	printOpenConcerns(&open, console.Theme{}, concerns)
	for _, required := range []string{"unanswered", "[c1.1]", "Do you want that goal changed?"} {
		if !strings.Contains(open.String(), required) {
			t.Fatalf("open-concern output = %q, want it to contain %q", open.String(), required)
		}
	}

	var quiet bytes.Buffer
	printChatConcerns(&quiet, console.Theme{}, domain.RoleProductManager, nil)
	printOpenConcerns(&quiet, console.Theme{}, nil)
	if quiet.Len() != 0 {
		t.Fatalf("a turn with no concerns printed %q", quiet.String())
	}
}

func TestChatReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code := Run([]string{"chat", "--config", missing, "--json", "--message", "hello"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !strings.Contains(result.Error, "open config") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestRunWorkItemReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code := Run([]string{"run", "--config", missing, "--json", "yoyodyne-task"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result runOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !strings.Contains(result.Error, "open config") {
		t.Fatalf("error = %q", result.Error)
	}
}

// The repository's own configuration is the one Yoyodyne self-hosts on, so it
// must validate under the automatic-integration policy it now enables: an
// independent reviewer agent and at least one deterministic check. It is also
// the worked example of the shape Yoyodyne ships, so it states its agents and
// carries its own personas rather than inheriting either.
func TestRepositoryConfigurationEnforcesAutomaticIntegration(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", config.DirectoryName, config.FileName)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	cfg := resolved.Config
	if cfg.Extends != "" {
		t.Fatalf("extends = %q, want a configuration that inherits nothing", cfg.Extends)
	}
	if len(resolved.Sources) != 1 {
		t.Fatalf("sources = %v, want only the project file", resolved.Sources)
	}
	for _, name := range []string{"developer", "reviewer"} {
		if origin := resolved.Origins["agents."+name+".persona"]; origin != resolved.Path {
			t.Errorf("agent %q persona origin = %q, want the project configuration", name, origin)
		}
		if strings.TrimSpace(cfg.Agents[name].Persona.Text) == "" {
			t.Errorf("agent %q has no effective persona", name)
		}
	}
	if cfg.Approvals.Integration != domain.ApprovalAutomatic {
		t.Fatalf("integration approval = %q, want %q", cfg.Approvals.Integration, domain.ApprovalAutomatic)
	}
	reviewers := 0
	for _, agent := range cfg.Agents {
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	if reviewers == 0 || len(cfg.Checks) == 0 {
		t.Fatalf("automatic integration is not gated: reviewers = %d, checks = %d", reviewers, len(cfg.Checks))
	}
	// Every executable agent declares its own selector, and the wiring uses the
	// reviewer's rather than letting the provider choose one.
	for name, agent := range cfg.Agents {
		if err := config.ValidateModelSelector(agent.Model); err != nil {
			t.Fatalf("agent %q model: %v", name, err)
		}
	}
	if got := agentModel(cfg, domain.RoleReviewer); got != cfg.Agents["reviewer"].Model {
		t.Fatalf("wired reviewer model = %q, want %q", got, cfg.Agents["reviewer"].Model)
	}
	if agentModel(cfg, domain.RoleDeveloper) == "" {
		t.Fatal("developer model selector is not configured")
	}
}

// The budget a project configures has to reach the runner that enforces it. The
// field existed on the runner long before anything wired it, so every project
// got the same flat ten minutes whatever it wrote down — which is how a run
// whose suite was passing package by package under contention was killed.
func TestPipelineGivesChecksTheConfiguredBudget(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(filepath.Join("..", "..", config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The durable records the pipeline is wired over are real ones under a
	// temporary root: the triage docket it dockets a stoppage into reads the
	// item's triage record beside them.
	store, err := runstate.NewStore(t.TempDir(), cfg.Product.ID)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	runner, ok := pipelineFrom(components{config: cfg, store: store}).Checks.(checks.Runner)
	if !ok {
		t.Fatal("the pipeline's check runner is not a checks.Runner")
	}
	if want := cfg.Execution.CheckTimeout.Duration(); runner.Timeout != want {
		t.Fatalf("wired check timeout = %s, want the configured %s", runner.Timeout, want)
	}
	if runner.Timeout <= 0 {
		t.Fatalf("wired check timeout = %s, which bounds nothing", runner.Timeout)
	}
}

func TestReportRunResultIsTruthfulAboutRemovedArtifacts(t *testing.T) {
	t.Parallel()

	integration := &gitworktree.Integration{TargetBranch: "main", SourceCommit: "abc123", TargetCommit: "abc123"}
	for _, test := range []struct {
		name     string
		outcome  orchestrator.Outcome
		err      error
		wantCode int
		want     []string
		reject   []string
	}{
		{
			name:     "integrated and cleaned up",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, BranchRemoved: true},
			wantCode: 0,
			want:     []string{"worktree removed: /wt", "branch removed: b"},
			reject:   []string{"NOT removed", "remaining"},
		},
		{
			name:     "cleanup outstanding after a successful run",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, CleanupFailure: "worktree is busy"},
			wantCode: 0,
			want:     []string{"worktree NOT removed: /wt", "branch NOT removed: b", "cleanup incomplete", "remaining worktree: /wt", "remaining branch: b"},
		},
		{
			// Partial cleanup: the worktree is gone and only the branch is left.
			name:     "partial cleanup leaves only the branch",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, CleanupFailure: "branch is busy"},
			wantCode: 0,
			want:     []string{"worktree removed: /wt", "branch NOT removed: b", "remaining branch: b"},
			reject:   []string{"remaining worktree"},
		},
		{
			// Both removals succeeded and only their confirmation failed, so no
			// artifact may be described as remaining.
			name: "cleanup verification failed with nothing left",
			outcome: orchestrator.Outcome{
				RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration,
				WorktreeRemoved: true, BranchRemoved: true, CleanupFailure: "verify removal of worktree: runner unavailable",
			},
			wantCode: 0,
			want:     []string{"cleanup could not be confirmed", "nothing is known to remain", "worktree removed: /wt", "branch removed: b"},
			reject:   []string{"cleanup incomplete", "remaining branch", "remaining worktree", "NOT removed"},
		},
		{
			// Cleanup finished and only writing it down failed: nothing may be
			// described as incomplete or remaining.
			name: "completion recording failed after a finished cleanup",
			outcome: orchestrator.Outcome{
				RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration,
				WorktreeRemoved: true, BranchRemoved: true, CompletionRecordingFailure: "state store is unavailable",
			},
			wantCode: 0,
			want:     []string{"completion recording failed", "cleanup completed", "worktree removed: /wt", "branch removed: b"},
			reject:   []string{"cleanup incomplete", "remaining", "NOT removed"},
		},
		{
			// A failure must never describe deleted artifacts as preserved.
			name:     "failure after the artifacts were removed",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, BranchRemoved: true},
			err:      errors.New("something later failed"),
			wantCode: 1,
			want:     []string{"worktree was already removed: /wt", "branch was already removed: b"},
			reject:   []string{"preserved worktree", "preserved branch"},
		},
		{
			// A failure after a partial cleanup preserves only what survives.
			name:     "failure after a partial cleanup",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true},
			err:      errors.New("something later failed"),
			wantCode: 1,
			want:     []string{"preserved branch: b", "worktree was already removed: /wt"},
			reject:   []string{"preserved worktree"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := reportRunResult(&stdout, &stderr, false, test.outcome, test.err)
			if code != test.wantCode {
				t.Fatalf("reportRunResult() = %d, want %d", code, test.wantCode)
			}
			combined := stdout.String() + stderr.String()
			for _, want := range test.want {
				if !strings.Contains(combined, want) {
					t.Errorf("output is missing %q: %s", want, combined)
				}
			}
			for _, reject := range test.reject {
				if strings.Contains(combined, reject) {
					t.Errorf("output falsely claims %q: %s", reject, combined)
				}
			}
		})
	}
}

func TestResolvePathRelativeToConfiguration(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	got, err := resolvePath(base, "repository")
	if err != nil {
		t.Fatalf("resolvePath() error = %v", err)
	}
	want := filepath.Join(base, "repository")
	if got != want {
		t.Fatalf("resolvePath() = %q, want %q", got, want)
	}
}

func TestConfigShowExplainsInheritance(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "show", "--config", path, "--effective", "--origins"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"# layer: " + config.BuiltinV1,
		"role: architect",
		"model: claude-opus-5-20260514",
		"source: " + config.BuiltinV1 + "/personas/developer.md",
		"agents.developer.model: " + path,
		"agents.developer.role: " + config.BuiltinV1,
		"approvals.brief: " + config.BuiltinV1,
		"execution.worktree_root: " + config.BuiltinV1,
		"product.repository_id: " + config.OriginDerived,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("config show output is missing %q:\n%s", want, output)
		}
	}
	// The persona body belongs in a prompt, not in a diagnostic listing.
	if strings.Contains(output, "You implement one bounded work item") {
		t.Errorf("config show inlined a persona body:\n%s", output)
	}
	// The revision names these values as one thing, so what a run recorded and
	// what the configuration says can be held up against each other.
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if !strings.Contains(output, "# revision: "+resolved.Config.Revision()) {
		t.Errorf("config show does not name the revision in force:\n%s", output)
	}
	// A project that names no account runs under one all the same, and says so
	// where an operator reads what is in force.
	if !strings.Contains(output, "account: "+config.DefaultAccountAlias) {
		t.Errorf("config show does not say which account the agents run under:\n%s", output)
	}
}

func TestConfigShowJSONReportsEffectiveValuesAndOrigins(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "show", "--config", path, "--effective", "--origins", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Config    string            `json:"config"`
		Sources   []string          `json:"sources"`
		Effective config.Config     `json:"effective"`
		Origins   map[string]string `json:"origins"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Config != path || len(result.Sources) != 2 || result.Sources[0] != config.BuiltinV1 {
		t.Fatalf("config = %q, sources = %v", result.Config, result.Sources)
	}
	if len(result.Effective.Agents) != 5 {
		t.Fatalf("effective agents = %d, want the five inherited defaults", len(result.Effective.Agents))
	}
	if result.Origins["agents.reviewer.backend"] != config.BuiltinV1 {
		t.Fatalf("reviewer backend origin = %q", result.Origins["agents.reviewer.backend"])
	}
}

// Discovery is what lets Yoyodyne run from anywhere inside a project, so the
// no-flag path is exercised from a nested directory rather than assumed.
func TestConfigValidateDiscoversTheProjectConfiguration(t *testing.T) {
	path := writeProjectConfig(t, portableConfig)
	nested := filepath.Join(filepath.Dir(filepath.Dir(path)), "internal", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Chdir(nested)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout = %q, want the discovered configuration %q", stdout.String(), path)
	}
}

func TestConfigValidateReportsAMissingConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no Yoyodyne configuration found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// Every command that takes an id reads its flags after that id, because that is
// the order the usage texts and the documentation say to type and the order
// anybody types when they have just read an id out of a listing. Go's flag
// package stops at the first word that is not a flag, so each of these was
// refused for naming two things.
//
// The assertion is the exit code: a command that parsed what it was given gets
// as far as loading the configuration and fails at 1 on a path that is not
// there, and one that did not refuses at 2 with a usage error before it looks at
// anything. Nothing here has to reach a provider or a tracker to say which
// happened.
func TestFlagsAreReadAfterTheIdEveryCommandThatTakesOne(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for name, args := range map[string][]string{
		"artifact show":     {"artifact", "show", "brief", "--config", missing},
		"artifact approve":  {"artifact", "approve", "brief", "--config", missing, "--reason", "approved in conversation"},
		"amendment show":    {"amendment", "show", "amendment-0123456789abcdef0123456789abcdef", "--config", missing},
		"amendment approve": {"amendment", "approve", "amendment-0123456789abcdef0123456789abcdef", "--config", missing, "--reason", "the ordering was never settled"},
		"amendment decline": {"amendment", "decline", "amendment-0123456789abcdef0123456789abcdef", "--config", missing, "--reason", "the design is right"},
		"invariant show":    {"invariant", "show", "one-writer-per-item", "--config", missing, "--json"},
		"invariant create":  {"invariant", "create", "one-writer-per-item", "--config", missing, "--title", "one writer", "--statement", "one writer", "--rationale", "one writer", "--established-by", "yoyodyne-ifd.2.7", "--reason", "extracted"},
		"invariant amend":   {"invariant", "amend", "one-writer-per-item", "--config", missing, "--scope", "internal/runstate", "--reason", "the other half moved"},
		"invariant retire":  {"invariant", "retire", "one-writer-per-item", "--config", missing, "--reason", "the reservation moved into the store"},
		"directive record":  {"directive", "record", "do publishing differently", "--config", missing, "--kind", "ambiguous", "--unresolved", "which behaviour was meant"},
		"directive resolve": {"directive", "resolve", "directive-0123456789abcdef0123456789abcdef", "--config", missing, "--resolution", "the second behaviour was meant"},
		"exchange show":     {"exchange", "show", "exchange-0123456789abcdef0123456789abcdef", "--config", missing, "--json"},
		"agent show":        {"agent", "show", "developer", "--config", missing, "--json"},
		"agent chat":        {"agent", "chat", "developer", "--config", missing, "--message", "what are you working on?"},
		"run":               {"run", "yoyodyne-ifd.74", "--config", missing, "--json"},
		"triage rerun":      {"triage", "rerun", "run-0123456789abcdef0123456789abcdef", "--config", missing, "--reason", "the ground moved"},
		"triage repair":     {"triage", "repair", "run-0123456789abcdef0123456789abcdef", "--config", missing, "--reason", "the findings still stand"},
		"status":            {"status", "yoyodyne-ifd.74", "--config", missing, "--failed"},
		"cost":              {"cost", "yoyodyne-ifd.74", "--config", missing, "--record"},
		"resume":            {"resume", "yoyodyne-ifd.74", "--config", missing},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(args, &stdout, &stderr, "test")
		if code != 1 {
			t.Fatalf("%s: code = %d, want 1 — the flags after the id were not read; stderr = %q", name, code, stderr.String())
		}
	}
}

func writeProjectConfig(t *testing.T, content string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".yoyodyne.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// portableConfig is what a project outside the Yoyodyne source tree writes: its
// own identity plus one sparse override, with every agent default inherited.
const portableConfig = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
agents:
  developer:
    model: claude-opus-5-20260514
`

const validConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`
