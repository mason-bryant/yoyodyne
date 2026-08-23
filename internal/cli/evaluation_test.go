package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/research"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What a conversation left behind is what the operator finds: the reasoning, the
// sources, and what the harness actually retrieved, read by a process that had
// nothing to do with the conversation that reached it.
func TestAnEvaluationIsReadLongAfterTheConversationThatReachedIt(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	deferred := recordEvaluation(t, evaluation.RecommendDefer, "a plugin marketplace")
	rejected := recordEvaluation(t, evaluation.RecommendReject, "a second query language")

	stdout, stderr, code := runCLI(t, "evaluation", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		deferred.ID,
		rejected.ID,
		"idea: a plugin marketplace",
		// The caveat is on every evaluation. An operator who read "recorded" as
		// "settled" would think a decision had been made for them.
		"advisory: nothing was admitted, approved, or changed",
		// The harness's own account of what was fetched, beside the citation.
		"source: https://example.test/prior-art",
		"retrieved: web:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// One recommendation at a time, for an operator asking what was turned down.
	stdout, _, code = runCLI(t, "evaluation", "list", "--config", configPath, "--recommendation", "reject")
	if code != 0 || !strings.Contains(stdout, rejected.ID) || strings.Contains(stdout, deferred.ID) {
		t.Fatalf("list --recommendation reject = %q (code %d)", stdout, code)
	}
	// A recommendation nothing recognizes is refused rather than filtered on: an
	// operator who mistyped it would otherwise read "nothing was evaluated" as an
	// answer about the record instead of about their typing.
	_, stderr, code = runCLI(t, "evaluation", "list", "--config", configPath, "--recommendation", "maybe")
	if code == 0 || !strings.Contains(stderr, "adopt, reject, defer, experiment") {
		t.Fatalf("list --recommendation maybe = %q (code %d)", stderr, code)
	}

	stdout, stderr, code = runCLI(t, "evaluation", "show", "--config", configPath, deferred.ID)
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "reasoning: the evidence is thin") {
		t.Fatalf("show stdout = %q", stdout)
	}
	_, stderr, code = runCLI(t, "evaluation", "show", "--config", configPath, "evaluation-00000000000000000000000000000000")
	if code == 0 || !strings.Contains(stderr, "was recorded for this product") {
		t.Fatalf("show of an unrecorded id = %q (code %d)", stderr, code)
	}
}

// A script reads the same record a person does, with the facts, the inferences,
// and the uncertainties still apart.
func TestTheEvaluationRecordIsReadableByAScript(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)
	recorded := recordEvaluation(t, evaluation.RecommendDefer, "a plugin marketplace")

	stdout, stderr, code := runCLI(t, "evaluation", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("list --json code = %d, stderr = %q", code, stderr)
	}
	var output evaluationOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout)
	}
	if len(output.Evaluations) != 1 || output.Evaluations[0].ID != recorded.ID {
		t.Fatalf("evaluations = %#v", output.Evaluations)
	}
	entry := output.Evaluations[0].Entry
	if len(entry.Facts) != 1 || len(entry.Inferences) != 1 || len(entry.Uncertainties) != 1 {
		t.Fatalf("the three kinds of statement did not survive: %#v", entry)
	}
	if len(output.Evaluations[0].Research) != 1 || output.Evaluations[0].Research[0].RetrievedAt.IsZero() {
		t.Fatalf("the harness research did not survive: %#v", output.Evaluations[0].Research)
	}
}

// A product nobody has brought an idea to reads as exactly that, rather than as
// a failure.
func TestAProductWithNoEvaluationsSaysSo(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "evaluation", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "no ideas have been evaluated") {
		t.Fatalf("list stdout = %q", stdout)
	}
}

// recordEvaluation puts an evaluation in the durable log the way a conversation
// leaves one behind, so what the commands read is what a conversation actually
// wrote rather than something they were handed.
func recordEvaluation(t *testing.T, recommendation evaluation.Recommendation, idea string) evaluation.Evaluation {
	t.Helper()

	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatalf("SystemDefaultRoot() error = %v", err)
	}
	store, err := runstate.NewEvaluationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewEvaluationStore() error = %v", err)
	}
	at := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	recorded, err := evaluation.Record(evaluation.Entry{
		Idea:            idea,
		Recommendation:  recommendation,
		Alignment:       "no goal covers third-party extensions yet",
		Reasoning:       "the evidence is thin and nothing in the goals asks for it",
		Facts:           []evaluation.Claim{{Claim: "three projects have tried it", Source: "https://example.test/prior-art"}},
		Inferences:      []string{"the maintenance cost lands on us"},
		Uncertainties:   []string{"how many of our users would write one"},
		Counterevidence: []string{"two of the three report it grew adoption"},
		Sources:         []evaluation.Citation{{Reference: "https://example.test/prior-art", Source: "web", Note: "prior art"}},
	}, evaluation.Attribution{
		Role:           domain.RoleProductManager,
		Agent:          "product-manager",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           4,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
	}, []research.Finding{
		{Source: "web", Question: "prior art", RetrievedAt: at, Evidence: "three projects have tried it"},
	}, at)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return recorded
}
