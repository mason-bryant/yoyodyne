package evaluation

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

func TestExtractSeparatesProseFromTheRecommendation(t *testing.T) {
	t.Parallel()

	reply := "I would not do this yet, and here is why.\n\n" +
		Fence + "\n" + completeBlock + "\n```\n\nSay if you want it anyway.\n"

	prose, entry, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-evaluation") || strings.Contains(prose, "\"recommendation\"") {
		t.Fatalf("prose kept the evaluation block: %q", prose)
	}
	if !strings.HasPrefix(prose, "I would not do this yet") || !strings.HasSuffix(prose, "want it anyway.") {
		t.Fatalf("prose = %q", prose)
	}
	if entry == nil || entry.Recommendation != RecommendDefer {
		t.Fatalf("entry = %#v", entry)
	}
	// The three kinds of statement stay apart, which is most of what the record
	// is worth: a paragraph is where the difference between them goes to die.
	if len(entry.Facts) != 1 || len(entry.Inferences) != 1 || len(entry.Uncertainties) != 1 {
		t.Fatalf("entry = %#v", entry)
	}

	// A reply that evaluates nothing is prose, whole and unchanged, which is
	// nearly every reply.
	prose, none, err := Extract("  Still clarifying what you mean by that.\n")
	if err != nil || none != nil || prose != "Still clarifying what you mean by that." {
		t.Fatalf("Extract() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestEvaluationsRefuseWhatNobodyCouldJudge(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		block string
		want  string
	}{
		{
			name:  "no recommendation",
			block: `{"evaluation":{"idea":"i","alignment":"a","reasoning":"r"}}`,
			want:  "is not one the harness recognizes",
		},
		{
			// Four answers exist because there are four real answers. A fifth is a
			// vocabulary the harness would have to guess the meaning of.
			name:  "an invented recommendation",
			block: `{"evaluation":{"idea":"i","recommendation":"maybe","alignment":"a","reasoning":"r"}}`,
			want:  "adopt, reject, defer, experiment",
		},
		{
			// A recommendation with no reasoning asks the operator to take advice on
			// trust, which is the one thing this record exists to make unnecessary.
			name:  "no reasoning",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a"}}`,
			want:  "reasoning is required",
		},
		{
			// Whether the idea is good in the abstract is not what the product
			// manager was asked.
			name:  "no alignment with the product",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","reasoning":"r"}}`,
			want:  "alignment is required",
		},
		{
			name:  "no idea",
			block: `{"evaluation":{"recommendation":"adopt","alignment":"a","reasoning":"r"}}`,
			want:  "idea is required",
		},
		{
			// A fact with nothing behind it is an inference. A durable record that
			// looks sourced and is not is worse than one that admits it read nothing.
			name:  "facts with nothing behind them",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a","reasoning":"r","facts":[{"claim":"c","source":"s"}]}}`,
			want:  "a fact nobody can trace back is an inference",
		},
		{
			name:  "a fact with no source of its own",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a","reasoning":"r","facts":[{"claim":"c"}],"sources":[{"reference":"https://example.test"}]}}`,
			want:  "facts[0]: source is required",
		},
		{
			name:  "unknown field",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a","reasoning":"r","confidence":0.9}}`,
			want:  "unknown field",
		},
		{
			name:  "oversized reasoning",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a","reasoning":"` + strings.Repeat("x", MaxTextBytes+1) + `"}}`,
			want:  "limit is " + strconv.Itoa(MaxTextBytes),
		},
		{
			name:  "too many uncertainties",
			block: `{"evaluation":{"idea":"i","recommendation":"adopt","alignment":"a","reasoning":"r","uncertainties":[` + strings.Repeat(`"u",`, MaxStatements) + `"u"]}}`,
			want:  "limit is " + strconv.Itoa(MaxStatements),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, entry, err := Extract(Fence + "\n" + test.block + "\n```")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Extract() error = %v, want it to contain %q", err, test.want)
			}
			if entry != nil {
				t.Fatalf("a refused block still yielded %#v", entry)
			}
		})
	}
}

// Two blocks, an unclosed one, and an empty one are all refused: what becomes a
// durable record of what somebody was advised has to be exactly what was
// written.
func TestABlockThatCannotBeReadRecordsNothing(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]string{
		"two blocks":   "prose\n" + Fence + "\n" + completeBlock + "\n```\nmore\n" + Fence + "\n" + completeBlock + "\n```\n",
		"unclosed":     "prose\n" + Fence + "\n" + completeBlock + "\n",
		"empty":        Fence + "\n\n```",
		"trailing":     Fence + "\n" + completeBlock + " {}\n```",
		"not an entry": Fence + "\n{\"evaluations\":[]}\n```",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, entry, err := Extract(reply)
			if err == nil {
				t.Fatalf("Extract() accepted %q", reply)
			}
			if entry != nil {
				t.Fatalf("a refused block still yielded %#v", entry)
			}
		})
	}
}

// The record keeps what the harness knows apart from what the product manager
// says. The citations are its account of what it read; the findings are what was
// actually fetched, from where, and when.
func TestARecordedEvaluationKeepsTheHarnessResearchBesideTheCitations(t *testing.T) {
	t.Parallel()

	retrieved := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	entry := completeEntry()
	recorded, err := Record(entry, Attribution{
		Role:           domain.RoleProductManager,
		Agent:          "product-manager",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           4,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
	}, []research.Finding{
		{Source: "web", Question: "prior art", RetrievedAt: retrieved, Evidence: "three projects have tried it"},
	}, retrieved)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !strings.HasPrefix(recorded.ID, "evaluation-") {
		t.Fatalf("id = %q", recorded.ID)
	}
	if len(recorded.Research) != 1 || !recorded.Research[0].RetrievedAt.Equal(retrieved) {
		t.Fatalf("research = %#v", recorded.Research)
	}
	if err := recorded.Validate(); err != nil {
		t.Fatalf("a recorded evaluation was invalid: %v", err)
	}

	rendered := recorded.Render()
	for _, required := range []string{
		recorded.ID,
		"advisory: nothing was admitted, approved, or changed",
		"idea: a plugin marketplace",
		"fact: three projects have tried it [https://example.test/prior-art]",
		"inferred:",
		"uncertain:",
		"against:",
		"retrieved: web:",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("Render() = %q, want it to contain %q", rendered, required)
		}
	}
	// The caveat is on every evaluation rather than only where it might be
	// misread: a caveat that appears sometimes is one a reader learns to skip.
	bare, err := Record(minimalEntry(), Attribution{
		Role:           domain.RoleProductManager,
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		ProductID:      "yoyodyne",
	}, nil, retrieved)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !strings.Contains(bare.Render(), "advisory:") {
		t.Fatalf("Render() of a bare evaluation = %q", bare.Render())
	}
}

// Each recommendation says something different, because a listing that called
// all four "evaluated" would hide exactly what the operator reads it for.
func TestTheRecommendationsSayDifferentThings(t *testing.T) {
	t.Parallel()

	headlines := make(map[string]struct{})
	for _, recommendation := range recommendations() {
		if !recommendation.Valid() {
			t.Fatalf("%s is not valid", recommendation)
		}
		headline := recommendation.Headline()
		if headline == "" {
			t.Fatalf("%s has no headline", recommendation)
		}
		headlines[headline] = struct{}{}
	}
	if len(headlines) != len(recommendations()) {
		t.Fatalf("the recommendations share a headline: %#v", headlines)
	}
	if Recommendation("adopt-later").Valid() {
		t.Fatal("an invented recommendation is valid")
	}
	// Adopting is the start of the governed path to doing the work rather than
	// any part of doing it, and the headline says so where the operator reads it.
	if !strings.Contains(RecommendAdopt.Headline(), "nothing is admitted or approved") {
		t.Fatalf("adopt reads as a decision: %q", RecommendAdopt.Headline())
	}
}

// The contract has to tell the role the same thing the harness enforces, and it
// has to say what recording one does not do — which is everything.
func TestTheContractSaysAnEvaluationDecidesNothing(t *testing.T) {
	t.Parallel()

	for _, required := range []string{
		"admits no work, changes no document, and approves nothing",
		"through the proposal block",
		"that is the operator's to make",
		"that is the architect's",
	} {
		if !strings.Contains(Contract, required) {
			t.Fatalf("the contract does not say %q", required)
		}
	}
	for _, recommendation := range recommendations() {
		if !strings.Contains(Contract, string(recommendation)) {
			t.Fatalf("the contract does not offer %q", recommendation)
		}
	}
}

const completeBlock = `{"evaluation":{
  "idea":"a plugin marketplace",
  "recommendation":"defer",
  "alignment":"no goal covers third-party extensions yet",
  "reasoning":"the evidence is thin and nothing in the goals asks for it",
  "facts":[{"claim":"three projects have tried it","source":"https://example.test/prior-art"}],
  "inferences":["the maintenance cost lands on us rather than on the plugin authors"],
  "uncertainties":["how many of our users would write one"],
  "counterevidence":["two of the three projects report it grew adoption"],
  "sources":[{"reference":"https://example.test/prior-art","source":"web","note":"prior art"}],
  "evidence_gap":"nothing addressed our own users",
  "follow_up":"a bounded survey before anything is admitted"
}}`

func completeEntry() Entry {
	entry, err := Decode(completeBlock)
	if err != nil {
		panic(err)
	}
	return *entry
}

func minimalEntry() Entry {
	return Entry{
		Idea:           "a plugin marketplace",
		Recommendation: RecommendReject,
		Alignment:      "cuts against keeping the surface small",
		Reasoning:      "nothing in the goals asks for it",
	}
}
