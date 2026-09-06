package sweep

import (
	"strings"
	"testing"
)

const completeBlock = "I looked at the stopped work.\n\n" +
	"```yoyodyne-sweep\n" +
	`{"status":"complete","summary":"two dead claims, both released","findings":[{"issue":"two claims on runs nothing is running","disposition":"fixed","detail":"released both","filed":["yoyodyne-ifd.300"]}]}` +
	"\n```\n"

func TestExtractReadsTheAccountAndLeavesTheProse(t *testing.T) {
	t.Parallel()

	prose, result, err := Extract(completeBlock)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result == nil {
		t.Fatal("Extract() found no result in a reply that carries one")
	}
	if result.Status != StatusComplete {
		t.Errorf("status = %q, want %q", result.Status, StatusComplete)
	}
	if len(result.Findings) != 1 || result.Findings[0].Disposition != DispositionFixed {
		t.Errorf("findings = %+v, want the one fixed finding", result.Findings)
	}
	if !strings.Contains(prose, "I looked at the stopped work.") {
		t.Errorf("prose = %q, want what the role said before the block", prose)
	}
	if strings.Contains(prose, "yoyodyne-sweep") {
		t.Errorf("prose = %q, want the block taken out of it", prose)
	}
}

// Most replies in this harness carry no block of any given kind, and a turn that
// answered in prose is not a failure: what is lost is the structure, which the
// caller says out loud rather than losing the turn over.
func TestReplyWithoutABlockIsNotAFailure(t *testing.T) {
	t.Parallel()

	prose, result, err := Extract("I found nothing worth reporting.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want none from a reply that carries no block", result)
	}
	if prose != "I found nothing worth reporting." {
		t.Errorf("prose = %q, want the whole reply", prose)
	}
}

// A fix that files nothing for its root cause is a silent repair. It is the one
// thing a week of these reports is read for, so it is a question the record
// answers rather than one every reader computes.
func TestSilentRepairIsAFixThatFiledNothing(t *testing.T) {
	t.Parallel()

	silent := Finding{Issue: "a stuck delivery", Disposition: DispositionFixed}
	if !silent.SilentRepair() {
		t.Error("a fix with nothing filed is not reported as a silent repair")
	}
	filed := Finding{Issue: "a stuck delivery", Disposition: DispositionFixed, Filed: []string{"yoyodyne-ifd.300"}}
	if filed.SilentRepair() {
		t.Error("a fix that filed root-cause work is reported as a silent repair")
	}
	// A thing the role did not fix files nothing by definition, and calling that
	// a silent repair would bury the fixes that are.
	left := Finding{Issue: "a design contradiction", Disposition: DispositionConsulted}
	if left.SilentRepair() {
		t.Error("a finding nobody fixed is reported as a silent repair")
	}
	result := Result{Status: StatusComplete, Summary: "one of each", Findings: []Finding{silent, filed, left}}
	if count := result.SilentRepairs(); count != 1 {
		t.Errorf("silent repairs = %d, want 1", count)
	}
}

func TestDecodeRefusesWhatCannotBeTrusted(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"an empty block":          "",
		"an unknown field":        `{"status":"complete","summary":"fine","verdict":"approve"}`,
		"an unknown status":       `{"status":"finished","summary":"fine"}`,
		"no summary":              `{"status":"complete"}`,
		"an unknown disposition":  `{"status":"complete","summary":"fine","findings":[{"issue":"a thing","disposition":"handled"}]}`,
		"a finding with no issue": `{"status":"complete","summary":"fine","findings":[{"issue":"","disposition":"fixed"}]}`,
		"trailing content":        `{"status":"complete","summary":"fine"} and then some`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(payload); err == nil {
				t.Fatalf("Decode(%q) accepted %s", payload, name)
			}
		})
	}
}

// The volume bound is what keeps a channel like this worth reading. A pass that
// found forty things has found something systemic, and the summary is where that
// belongs.
func TestDecodeRefusesMoreFindingsThanTheBound(t *testing.T) {
	t.Parallel()

	findings := make([]string, 0, MaxFindings+1)
	for i := 0; i <= MaxFindings; i++ {
		findings = append(findings, `{"issue":"a thing","disposition":"left"}`)
	}
	payload := `{"status":"complete","summary":"many","findings":[` + strings.Join(findings, ",") + `]}`
	if _, err := Decode(payload); err == nil {
		t.Fatalf("Decode() accepted %d findings, and the bound is %d", MaxFindings+1, MaxFindings)
	}
}

// A firing takes several turns, and the record has to hold what all of them
// found: the last turn says how the pass ended, and the findings accumulate.
func TestMergeKeepsEveryTurnsFindings(t *testing.T) {
	t.Parallel()

	first := Result{Status: StatusMore, Summary: "started", Findings: []Finding{{Issue: "one", Disposition: DispositionFixed}}}
	second := Result{Status: StatusComplete, Summary: "finished", Findings: []Finding{{Issue: "two", Disposition: DispositionFiled}}}
	merged := first.Merge(second)
	if merged.Status != StatusComplete || merged.Summary != "finished" {
		t.Errorf("merged = %+v, want the last turn's status and summary", merged)
	}
	if len(merged.Findings) != 2 {
		t.Errorf("findings = %+v, want both turns' findings", merged.Findings)
	}
	// Merging must not write into what it was given: the earlier turn's record is
	// held elsewhere and rewriting it would corrupt what was already reported.
	if len(first.Findings) != 1 {
		t.Errorf("the earlier turn's findings = %+v, want them untouched", first.Findings)
	}
}

// The bounds a role is told about and the bounds enforced on what it sends back
// have to be one statement, or the contract teaches an agent to write something
// that is then refused.
func TestContractStatesTheBoundsItEnforces(t *testing.T) {
	t.Parallel()

	contract := Contract()
	for _, want := range []string{Fence, maxFindingsText, maxQuestionsText, string(StatusComplete), string(StatusMore), string(DispositionFixed)} {
		if !strings.Contains(contract, want) {
			t.Errorf("the contract does not state %q:\n%s", want, contract)
		}
	}
	if maxFindingsText != "20" || MaxFindings != 20 {
		t.Errorf("the contract says %s findings and the code enforces %d", maxFindingsText, MaxFindings)
	}
	if maxQuestionsText != "5" || MaxQuestions != 5 {
		t.Errorf("the contract says %s questions and the code enforces %d", maxQuestionsText, MaxQuestions)
	}
}

// The whole example in the contract has to decode, because it is what an agent
// copies.
func TestContractExampleDecodes(t *testing.T) {
	t.Parallel()

	contract := Contract()
	opens := strings.Index(contract, Fence)
	if opens < 0 {
		t.Fatalf("the contract shows no block:\n%s", contract)
	}
	payload := contract[opens+len(Fence):]
	closes := strings.Index(payload, "\n```")
	if closes < 0 {
		t.Fatalf("the contract's block is not closed:\n%s", contract)
	}
	// The example writes the vocabulary as alternatives -- "complete|more" -- so
	// what is checked is that one concrete choice of them decodes.
	example := strings.NewReplacer(
		"complete|more", string(StatusComplete),
		"fixed|filed|consulted|left", string(DispositionFixed),
	).Replace(payload[:closes])
	if _, err := Decode(example); err != nil {
		t.Fatalf("the contract's own example does not decode: %v\n%s", err, example)
	}
}

// The bound a whole firing is held to is not the bound one turn is held to, and
// the difference is the heavy pass this whole mechanism exists for: a role that
// legitimately reported the per-turn maximum on each of several turns produces a
// merged account several times that size, and holding it to the per-turn cap
// discarded the durable report of exactly those passes.
func TestAFullPassWorthOfTurnsStillValidates(t *testing.T) {
	t.Parallel()

	merged := Result{Status: StatusComplete, Summary: "a heavy pass"}
	for turn := 0; turn < MaxMergedTurns; turn++ {
		next := Result{Status: StatusComplete, Summary: "a heavy pass"}
		for i := 0; i < MaxFindings; i++ {
			next.Findings = append(next.Findings, Finding{Issue: "a thing", Disposition: DispositionFiled})
		}
		for i := 0; i < MaxQuestions; i++ {
			next.Questions = append(next.Questions, "something only a person can settle")
		}
		merged = merged.Merge(next)
	}
	if len(merged.Findings) != MaxPassFindings {
		t.Errorf("findings = %d, want every turn's kept up to %d", len(merged.Findings), MaxPassFindings)
	}
	if len(merged.Questions) != MaxPassQuestions {
		t.Errorf("questions = %d, want every turn's kept up to %d", len(merged.Questions), MaxPassQuestions)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a full pass worth of turns does not validate, so its durable report would be discarded: %v", err)
	}
}

// The per-turn cap still binds what one turn may send, which is the number the
// contract states and the one that keeps a single reply readable.
func TestOneTurnIsStillHeldToThePerTurnBound(t *testing.T) {
	t.Parallel()

	overTurn := Result{Status: StatusComplete, Summary: "one very long turn"}
	for i := 0; i <= MaxFindings; i++ {
		overTurn.Findings = append(overTurn.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	if err := overTurn.validateTurn(); err == nil {
		t.Fatalf("a turn carrying %d findings passed the per-turn bound of %d", len(overTurn.Findings), MaxFindings)
	}
	// The same account is fine as a whole pass's, which is the distinction the two
	// contracts exist to draw.
	if err := overTurn.Validate(); err != nil {
		t.Fatalf("an account within the pass bound does not validate as one: %v", err)
	}
}

// Beyond the pass bound the merge drops rather than growing without limit, and
// says so: a silently shortened list reads as a pass that found less than it did.
func TestMergeSaysWhatItDropped(t *testing.T) {
	t.Parallel()

	full := Result{Status: StatusComplete, Summary: "the pass"}
	for i := 0; i < MaxPassFindings; i++ {
		full.Findings = append(full.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	for i := 0; i < MaxPassQuestions; i++ {
		full.Questions = append(full.Questions, "a question")
	}
	merged := full.Merge(Result{
		Status:    StatusComplete,
		Summary:   "one turn too many",
		Findings:  []Finding{{Issue: "the one over", Disposition: DispositionLeft}},
		Questions: []string{"the question over"},
	})
	if len(merged.Findings) != MaxPassFindings || len(merged.Questions) != MaxPassQuestions {
		t.Errorf("merged = %d findings and %d questions, want them held at the pass bound",
			len(merged.Findings), len(merged.Questions))
	}
	if !strings.Contains(merged.Summary, "not listed") {
		t.Errorf("summary = %q, want it to say what was dropped", merged.Summary)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a merge held at its own bound does not validate: %v", err)
	}
}

// The note about what was dropped must not itself push the summary past what the
// record accepts, which would lose the report the note exists to preserve.
func TestTheDroppedNoteKeepsTheSummaryInsideItsBound(t *testing.T) {
	t.Parallel()

	full := Result{Status: StatusComplete, Summary: strings.Repeat("x", MaxSummaryBytes)}
	for i := 0; i < MaxPassFindings; i++ {
		full.Findings = append(full.Findings, Finding{Issue: "a thing", Disposition: DispositionLeft})
	}
	merged := full.Merge(Result{
		Status:   StatusComplete,
		Summary:  strings.Repeat("y", MaxSummaryBytes),
		Findings: []Finding{{Issue: "the one over", Disposition: DispositionLeft}},
	})
	if len(merged.Summary) > MaxSummaryBytes {
		t.Errorf("summary is %d bytes, and the record's bound is %d", len(merged.Summary), MaxSummaryBytes)
	}
	if !strings.Contains(merged.Summary, "not listed") {
		t.Errorf("summary = %q, want the note kept whole when the summary is cut", merged.Summary)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("a merge with a full-length summary does not validate: %v", err)
	}
}
