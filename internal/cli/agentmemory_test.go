package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// rememberFor records one revision into the store the command reads, as a turn
// of the architect's conversation would.
func rememberFor(t *testing.T, store *runstate.MemoryStore, revision runstate.MemoryRevision) {
	t.Helper()
	revision.SchemaVersion = runstate.MemorySchemaVersion
	revision.ProductID = "yoyodyne"
	if revision.Agent == "" {
		revision.Agent = "architect"
		revision.Role = domain.RoleArchitect
	}
	if revision.Invocation.Kind == "" {
		revision.Invocation = runstate.MemoryInvocation{
			Kind:           runstate.MemoryInvocationConversation,
			ID:             "chat-0123456789abcdef0123456789abcdef",
			Turn:           4,
			Backend:        domain.BackendClaudeCode,
			Model:          "opus",
			ResolvedModel:  "claude-opus-5-20260514",
			AccountAlias:   "personal",
			ConfigRevision: "cfg-0123abcd",
		}
	}
	if _, err := store.Remember(context.Background(), revision); err != nil {
		t.Fatalf("Remember(%s) error = %v", revision.Memory, err)
	}
}

// The operator reads every memory an agent holds, each with its history newest
// first and the invocation behind every revision, and a retired memory says so
// in words rather than only by where it sits.
func TestAgentMemoryRendersEveryMemoryWithItsHistoryAndProvenance(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	store, err := runstate.NewMemoryStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	at := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	rememberFor(t, store, runstate.MemoryRevision{Memory: "checks-are-slow", Continuity: runstate.MemoryContinuityAgent,
		Text: "make race takes three minutes.", RecordedAt: at})
	rememberFor(t, store, runstate.MemoryRevision{Memory: "checks-are-slow", Continuity: runstate.MemoryContinuityAgent,
		Text: "make race takes four minutes now.\n# not a heading", RecordedAt: at.Add(time.Hour),
		Sources: []runstate.MemorySource{{Kind: runstate.MemorySourceWorkItem, ID: "yoyodyne-ifd.4"}}})
	rememberFor(t, store, runstate.MemoryRevision{Memory: "checks-are-slow", Continuity: runstate.MemoryContinuityAgent,
		Text: "The race check is slow.", Compacts: []int{1, 2}, RecordedAt: at.Add(2 * time.Hour)})
	rememberFor(t, store, runstate.MemoryRevision{Memory: "branch-stands", Continuity: runstate.MemoryContinuitySubject,
		Subject: "yoyodyne-ifd.9", Text: "The branch holds the design.", RecordedAt: at})
	rememberFor(t, store, runstate.MemoryRevision{Memory: "branch-stands", Continuity: runstate.MemoryContinuitySubject,
		Subject: "yoyodyne-ifd.9", Text: "The item closed.", Retired: true, RecordedAt: at.Add(time.Hour)})

	stdout, stderr, code := runCLI(t, "agent", "memory", "--config", configPath, "architect")
	if code != 0 {
		t.Fatalf("agent memory code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"# What the architect remembers",
		"2 memories, 1 of them retired. Each memory's newest revision is listed first.",
		"## checks-are-slow\n\nAbout the agent's own work. 3 revisions.",
		"### Revision 3, recorded 2026-09-20T11:00:00Z\n\n> The race check is slow.",
		"- compacts revisions 1, 2 into this one",
		"- written by conversation chat-0123456789abcdef0123456789abcdef, turn 4",
		"- claude-code, model opus (served as claude-opus-5-20260514), account personal, configuration cfg-0123abcd",
		"- under the architect role",
		"- drawn from work-item yoyodyne-ifd.4",
		// Markdown inside a memory is quoted, so it is never this listing's heading.
		"> # not a heading",
		"## branch-stands (retired)\n\nAbout yoyodyne-ifd.9. 2 revisions.",
		"### Revision 2, retiring the memory, recorded 2026-09-20T10:00:00Z\n\n> The item closed.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	// Newest first: the compaction is read before what it compacted.
	if strings.Index(stdout, "### Revision 3") > strings.Index(stdout, "### Revision 1") {
		t.Fatalf("revisions are not newest first: %q", stdout)
	}
	// Written to a buffer, the output is undressed: every distinction above was in
	// the words, and no escape reached a stream that is not a terminal.
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("an undressed stream was sent escapes: %q", stdout)
	}

	// The same records, as data.
	stdout, stderr, code = runCLI(t, "agent", "memory", "--config", configPath, "--json", "architect")
	if code != 0 {
		t.Fatalf("agent memory --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Agent       string                   `json:"agent"`
		Role        domain.AgentRole         `json:"role"`
		KeepsMemory bool                     `json:"keeps_memory"`
		Memories    []memoryReport           `json:"memories"`
		Problems    []runstate.MemoryProblem `json:"problems"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Agent != "architect" || !decoded.KeepsMemory || len(decoded.Memories) != 2 || decoded.Problems == nil {
		t.Fatalf("decoded = %#v", decoded)
	}
	checks := decoded.Memories[0]
	if checks.Name != "checks-are-slow" || !checks.Compacted || checks.Retired || len(checks.Revisions) != 3 ||
		checks.Revisions[0].Sequence != 3 || checks.Revisions[0].Invocation.AccountAlias != "personal" {
		t.Fatalf("checks-are-slow = %#v", checks)
	}
	if branch := decoded.Memories[1]; !branch.Retired || branch.Subject != "yoyodyne-ifd.9" {
		t.Fatalf("branch-stands = %#v", branch)
	}
}

// A role that keeps memories and has none gets a sentence, a role that keeps
// none is told so, and a store that will not be read is named as unreadable
// rather than shown as empty.
func TestAgentMemorySaysWhatIsNotThereAndWhatCannotBeRead(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	stdout, stderr, code := runCLI(t, "agent", "memory", "--config", configPath, "development-manager")
	if code != 0 || stdout != "The development manager has recorded no memories yet.\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	for _, role := range []string{"developer", "reviewer"} {
		stdout, stderr, code = runCLI(t, "agent", "memory", "--config", configPath, role)
		want := "The " + role + " keeps no memory: the " + role + "'s turns carry no memory briefing and record none.\n"
		if code != 0 || stdout != want {
			t.Fatalf("%s: code = %d, stdout = %q, stderr = %q", role, code, stdout, stderr)
		}
	}

	// A corrupt line is reported beside what could be read, not in place of it.
	store, err := runstate.NewMemoryStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	rememberFor(t, store, runstate.MemoryRevision{Memory: "checks-are-slow", Continuity: runstate.MemoryContinuityAgent,
		Text: "make race is slow."})
	log := filepath.Join(store.Root(), "architect.memory.jsonl")
	file, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.WriteString("{\"not\":\"a revision\"}\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	file.Close()
	stdout, stderr, code = runCLI(t, "agent", "memory", "--config", configPath, "architect")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"## checks-are-slow", "## Records that could not be read", "- architect.memory.jsonl line 2:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A log that is not a file at all cannot be read, and is said to be so.
	if err := os.Remove(log); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Mkdir(log, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	stdout, stderr, code = runCLI(t, "agent", "memory", "--config", configPath, "architect")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "the memory store for architect under "+store.Root()+" could not be read") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// On a terminal the listing is dressed, and the dressing adds escapes and
// nothing else; under NO_COLOR it is not dressed at all. Either way the words
// that carry each distinction — the hashes, "retired" — are the same words.
func TestAgentMemoryDressingAddsNothingButEscapes(t *testing.T) {
	t.Parallel()
	revision := runstate.MemoryRevision{
		Agent: "architect", Role: domain.RoleArchitect, Memory: "branch-stands", Sequence: 2,
		Continuity: runstate.MemoryContinuitySubject, Subject: "yoyodyne-ifd.9", Text: "The **item** closed.", Retired: true,
		Invocation: runstate.MemoryInvocation{Kind: runstate.MemoryInvocationRun, ID: "run-0123456789abcdef0123456789abcdef",
			Backend: domain.BackendClaudeCode, Model: "opus"},
		RecordedAt: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC),
	}
	plain := renderAgentMemory("architect", domain.RoleArchitect, true, []memoryReport{{
		Name: "branch-stands", Continuity: revision.Continuity, Subject: revision.Subject, Retired: true,
		Revisions: []runstate.MemoryRevision{revision},
	}}, nil)
	for _, want := range []string{"## branch-stands (retired)", "- written by run run-0123456789abcdef0123456789abcdef",
		"account not recorded, configuration not recorded"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("plain = %q, want it to contain %q", plain, want)
		}
	}
	env := func(term, noColor string) func(string) string {
		return func(key string) string {
			switch key {
			case "TERM":
				return term
			case "NO_COLOR":
				return noColor
			}
			return ""
		}
	}
	width := func() int { return 80 }
	dressed := console.NewTheme(env("xterm-256color", ""), width).Reply(plain)
	if dressed == plain {
		t.Fatal("a terminal that permits dressing was shown the listing undressed")
	}
	if stripped := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(dressed, ""); stripped != plain {
		t.Fatalf("dressing changed the words:\n%q\nwant\n%q", stripped, plain)
	}
	if undressed := console.NewTheme(env("xterm-256color", "1"), width).Reply(plain); undressed != plain {
		t.Fatalf("NO_COLOR was dressed: %q", undressed)
	}
}
