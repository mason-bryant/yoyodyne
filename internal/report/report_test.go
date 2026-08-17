package report

import (
	"strings"
	"testing"
	"time"
)

func TestExtractTakesTheBlockOutAndLeavesWhatWasSaid(t *testing.T) {
	t.Parallel()

	reply := "I finished the change and the checks pass.\n\n" +
		Fence + "\n" +
		`{"reports":[{"severity":"warning","message":"bd lint could not run in the sandbox, so the item was never linted."}]}` +
		"\n```\n"

	rest, entries, err := Extract(reply)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Severity != SeverityWarning {
		t.Fatalf("Extract() entries = %#v", entries)
	}
	if !strings.Contains(entries[0].Message, "bd lint") {
		t.Fatalf("message = %q", entries[0].Message)
	}
	if strings.Contains(rest, "yoyodyne-report") || strings.Contains(rest, "severity") {
		t.Fatalf("the block stayed in what was said: %q", rest)
	}
	if rest != "I finished the change and the checks pass." {
		t.Fatalf("rest = %q", rest)
	}
}

func TestExtractReportsNothingWhenNothingWasReported(t *testing.T) {
	t.Parallel()

	// Most replies carry no block at all, and that is not an empty report: it is
	// an agent with nothing to say beyond its own account of the work.
	rest, entries, err := Extract("  Done; nothing surprising.\n")
	if err != nil || entries != nil {
		t.Fatalf("Extract() = %#v, %v", entries, err)
	}
	if rest != "Done; nothing surprising." {
		t.Fatalf("rest = %q", rest)
	}
}

func TestExtractDropsAnUnreadableBlockAndKeepsWhatCameBeforeIt(t *testing.T) {
	t.Parallel()

	// What the caller does with the reply is the role's own business — a summary
	// to record, a verdict to decode — and an unreadable report must never cost
	// it. So the block goes whichever way it was read, and what the agent
	// actually said survives with the error beside it.
	for name, reply := range map[string]string{
		"unclosed":     "Done.\n\n" + Fence + "\n{\"reports\":[]}\n",
		"unknownField": "Done.\n\n" + Fence + "\n{\"reports\":[{\"severity\":\"note\",\"message\":\"x\",\"file\":\"a.go\"}]}\n```\n",
		"badSeverity":  "Done.\n\n" + Fence + "\n{\"reports\":[{\"severity\":\"blocker\",\"message\":\"x\"}]}\n```\n",
		"noMessage":    "Done.\n\n" + Fence + "\n{\"reports\":[{\"severity\":\"note\",\"message\":\"  \"}]}\n```\n",
		"empty":        "Done.\n\n" + Fence + "\n{\"reports\":[]}\n```\n",
		"twoBlocks": "Done.\n\n" + Fence + "\n{\"reports\":[{\"severity\":\"note\",\"message\":\"x\"}]}\n```\n\n" +
			Fence + "\n{\"reports\":[{\"severity\":\"note\",\"message\":\"y\"}]}\n```\n",
		"trailingText": "Done.\n\n" + Fence + " and more\n{\"reports\":[{\"severity\":\"note\",\"message\":\"x\"}]}\n```\n",
	} {
		rest, entries, err := Extract(reply)
		if err == nil {
			t.Fatalf("%s: Extract() accepted %q", name, reply)
		}
		if rest != "Done." || entries != nil {
			t.Fatalf("%s: Extract() = %q, %#v", name, rest, entries)
		}
	}
}

func TestDecodeRefusesMoreReportsThanOneReplyMayCarry(t *testing.T) {
	t.Parallel()

	// Volume is what makes a channel like this worthless, so a reply that files a
	// list of observations is refused whole rather than partly collected.
	var entries []string
	for i := 0; i <= MaxEntriesPerReply; i++ {
		entries = append(entries, `{"severity":"note","message":"something"}`)
	}
	_, err := Decode(`{"reports":[` + strings.Join(entries, ",") + `]}`)
	if err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("Decode() error = %v", err)
	}
}

func TestContractStatesTheBoundItIsHeldTo(t *testing.T) {
	t.Parallel()

	// An agent told one number and refused by another would be refused for
	// following its own contract.
	if !strings.Contains(Contract, "at most "+maxEntriesPerReplyText+" reports") {
		t.Fatalf("the contract does not state the enforced bound of %d", MaxEntriesPerReply)
	}
	if !strings.Contains(Contract, Fence) {
		t.Fatalf("the contract does not name the fence the harness reads")
	}
	for _, severity := range []Severity{SeverityCritical, SeverityWarning, SeverityNote} {
		if !strings.Contains(Contract, string(severity)) {
			t.Fatalf("the contract does not name severity %q", severity)
		}
	}
	// A report must not read as a blocker, in the contract any more than in the
	// record: work carries on, and nothing waits on it.
	if !strings.Contains(Contract, "A report is not a blocker") {
		t.Fatal("the contract does not say a report is not a blocker")
	}
}

func TestCollectAttributesEveryReportToTheInvocationThatMadeIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	collected, err := Collect([]Entry{
		{Severity: SeverityCritical, Message: "  the declared bundle version is inert  "},
		{Severity: SeverityNote, Message: "the fixture repository is not a Go project"},
	}, Attribution{
		Role:         "developer",
		Agent:        "developer",
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorkItemID:   "yoyodyne-ifd.19",
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}, now)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(collected) != 2 {
		t.Fatalf("Collect() = %#v", collected)
	}
	if collected[0].ID == collected[1].ID {
		t.Fatalf("two reports share the identity %q", collected[0].ID)
	}
	first := collected[0]
	if first.Role != "developer" || first.Agent != "developer" || first.WorkItemID != "yoyodyne-ifd.19" {
		t.Fatalf("attribution = %#v", first)
	}
	if first.RunID != "run-0123456789abcdef0123456789abcdef" || !first.RecordedAt.Equal(now) {
		t.Fatalf("provenance = %#v", first)
	}
	if first.Message != "the declared bundle version is inert" {
		t.Fatalf("message = %q", first.Message)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("a collected report failed its own contract: %v", err)
	}
}

func TestCollectedReportsAreRefusedWithoutTheirAttribution(t *testing.T) {
	t.Parallel()

	// The pile is triaged later by exactly these fields, so a record that cannot
	// say who reported it or where from must never reach it.
	valid := Report{
		SchemaVersion: SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      SeverityWarning,
		Message:       "something worth knowing",
		RecordedAt:    time.Now(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a complete report was refused: %v", err)
	}
	for name, damage := range map[string]func(*Report){
		"id":         func(r *Report) { r.ID = "report-nope" },
		"role":       func(r *Report) { r.Role = "" },
		"run":        func(r *Report) { r.RunID = " " },
		"product":    func(r *Report) { r.ProductID = "" },
		"repository": func(r *Report) { r.RepositoryID = "" },
		"severity":   func(r *Report) { r.Severity = "urgent" },
		"message":    func(r *Report) { r.Message = "" },
		"recorded":   func(r *Report) { r.RecordedAt = time.Time{} },
		"schema":     func(r *Report) { r.SchemaVersion = SchemaVersion + 1 },
		"oversized":  func(r *Report) { r.Message = strings.Repeat("x", MaxMessageBytes+1) },
	} {
		damaged := valid
		damage(&damaged)
		if err := damaged.Validate(); err == nil {
			t.Fatalf("%s: an invalid report was accepted", name)
		}
	}
}
