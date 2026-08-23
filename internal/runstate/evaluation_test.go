package runstate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/evaluation"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

func TestEvaluationsOutliveTheConversationThatReachedThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestEvaluationStore(t, root, "yoyodyne")
	// A product nobody has evaluated an idea for is not a failure to read.
	recorded, err := store.List()
	if err != nil || len(recorded) != 0 {
		t.Fatalf("List() on an empty log = %#v, %v", recorded, err)
	}

	first := newTestEvaluation(t, "yoyodyne", evaluation.RecommendDefer)
	second := newTestEvaluation(t, "yoyodyne", evaluation.RecommendReject)
	for _, one := range []evaluation.Evaluation{first, second} {
		if err := store.Append(one); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	// A later process reads the same record: the conversation and the provider
	// session it was reached in are long gone by the time anybody asks what a
	// decision was decided from.
	reopened := newTestEvaluationStore(t, root, "yoyodyne")
	recorded, err = reopened.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 || recorded[0].ID != first.ID || recorded[1].ID != second.ID {
		t.Fatalf("List() = %#v", recorded)
	}
	// The research travels with the recommendation, retrieval times and all.
	if len(recorded[0].Research) != 1 || recorded[0].Research[0].Source != "web" {
		t.Fatalf("research did not survive the round trip: %#v", recorded[0].Research)
	}
	if recorded[0].Research[0].RetrievedAt.IsZero() {
		t.Fatal("the retrieval time did not survive the round trip")
	}

	found, exists, err := reopened.Find(second.ID)
	if err != nil || !exists || found.Entry.Recommendation != evaluation.RecommendReject {
		t.Fatalf("Find() = %#v, %v, %v", found, exists, err)
	}
	if _, exists, err = reopened.Find("evaluation-00000000000000000000000000000000"); err != nil || exists {
		t.Fatalf("Find() on an unrecorded id = %v, %v", exists, err)
	}
}

// The log is one product's. A record from another product in it is a mixed-up
// state root rather than an evaluation to read, and reading it as this
// product's would attribute somebody else's reasoning to this one.
func TestAnEvaluationBelongsToOneProduct(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestEvaluationStore(t, root, "yoyodyne")
	elsewhere := newTestEvaluation(t, "another-product", evaluation.RecommendAdopt)
	err := store.Append(elsewhere)
	if err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Append() error = %v, want it to refuse another product's evaluation", err)
	}

	// An invalid record is refused before it is written rather than after it is
	// read: what is durable here is what somebody was advised.
	incomplete := newTestEvaluation(t, "yoyodyne", evaluation.RecommendAdopt)
	incomplete.Entry.Reasoning = ""
	if err := store.Append(incomplete); err == nil {
		t.Fatal("Append() accepted an evaluation with no reasoning")
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("a refused evaluation created the log: %v", err)
	}
}

func newTestEvaluationStore(t *testing.T, root string, productID domain.ProductID) *EvaluationStore {
	t.Helper()

	store, err := NewEvaluationStore(root, productID)
	if err != nil {
		t.Fatalf("NewEvaluationStore() error = %v", err)
	}
	return store
}

func newTestEvaluation(t *testing.T, productID domain.ProductID, recommendation evaluation.Recommendation) evaluation.Evaluation {
	t.Helper()

	at := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	recorded, err := evaluation.Record(evaluation.Entry{
		Idea:           "a plugin marketplace",
		Recommendation: recommendation,
		Alignment:      "no goal covers third-party extensions yet",
		Reasoning:      "the evidence is thin and nothing in the goals asks for it",
	}, evaluation.Attribution{
		Role:           domain.RoleProductManager,
		Agent:          "product-manager",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           4,
		ProductID:      productID,
		RepositoryID:   "yoyodyne",
	}, []research.Finding{
		{Source: "web", Question: "prior art", RetrievedAt: at, Evidence: "three projects have tried it"},
	}, at)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return recorded
}
