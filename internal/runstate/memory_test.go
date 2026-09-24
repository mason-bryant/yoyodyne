package runstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestMemoryStoreKeepsRevisionsAppendOnlyWithTheirProvenance(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	first, err := store.Remember(context.Background(), testMemoryRevision())
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first revision is numbered %d, want 1", first.Sequence)
	}

	second := testMemoryRevision()
	second.Text = "the operator reads reports at leisure and asks for the plain word"
	second.Invocation.Turn = 9
	if _, err := store.Remember(context.Background(), second); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want 1", len(memories))
	}
	memory := memories[0]
	if len(memory.Revisions) != 2 {
		t.Fatalf("the memory has %d revisions, want 2", len(memory.Revisions))
	}
	// The earlier revision is still there and still says what it said. That is the
	// whole of what append-only buys: an operator asking what an agent used to
	// believe gets an answer rather than the current answer twice.
	if memory.Revisions[0].Text == memory.Revisions[1].Text {
		t.Errorf("the first revision was overwritten by the second")
	}
	if memory.Current().Sequence != 2 {
		t.Errorf("Current() is revision %d, want 2", memory.Current().Sequence)
	}
	// The audit the design requires: every revision says which invocation wrote it,
	// pinned to the backend, the model, the account, and the configuration.
	for _, revision := range memory.Revisions {
		if revision.Invocation.ID != "chat-0123456789abcdef0123456789abcdef" {
			t.Errorf("revision %d names invocation %q", revision.Sequence, revision.Invocation.ID)
		}
		if revision.Invocation.Backend == "" || revision.Invocation.Model == "" {
			t.Errorf("revision %d records no backend or model", revision.Sequence)
		}
		if revision.Invocation.AccountAlias == "" || revision.Invocation.ConfigRevision == "" {
			t.Errorf("revision %d records no account or configuration", revision.Sequence)
		}
	}
	if memory.Revisions[0].Invocation.Turn == memory.Revisions[1].Invocation.Turn {
		t.Errorf("both revisions name turn %d, so the audit cannot tell them apart", memory.Revisions[0].Invocation.Turn)
	}
}

func TestMemoryStoreNumbersRevisionsItself(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	numbered := testMemoryRevision()
	numbered.Sequence = 7
	if _, err := store.Remember(context.Background(), numbered); err == nil {
		t.Fatal("Remember() accepted a revision that numbered itself")
	}
}

func TestMemoryStoreRedactsBeforeItPersists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewMemoryStore(root, "yoyodyne", "sk-secret-token")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	revision := testMemoryRevision()
	revision.Text = "the forge accepts sk-secret-token for this product"
	recorded, err := store.Remember(context.Background(), revision)
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if strings.Contains(recorded.Text, "sk-secret-token") {
		t.Errorf("Remember() returned the value unredacted: %q", recorded.Text)
	}
	// What matters is the disk rather than the return: a memory is read back by a
	// later invocation out of the file, and a redaction that only reached the
	// caller would be no redaction at all.
	stored, err := os.ReadFile(filepath.Join(store.Root(), "product-manager.memory.jsonl"))
	if err != nil {
		t.Fatalf("read the memory log: %v", err)
	}
	if strings.Contains(string(stored), "sk-secret-token") {
		t.Errorf("the memory log holds the unredacted value: %s", stored)
	}
}

func TestMemoryStoreRefusesAWriteThatWouldExceedTheLiveBudget(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	// One memory per write, each at the per-revision bound, until the live budget
	// refuses one. Everything before the refusal is stored, so what is measured is
	// what the agent would know rather than what it has written over time.
	for index := 0; ; index++ {
		revision := testMemoryRevision()
		revision.Memory = fmt.Sprintf("what-i-learned-%d", index)
		revision.Text = strings.Repeat("a", MaxMemoryTextBytes)
		_, err := store.Remember(context.Background(), revision)
		if err == nil {
			if index > MaxMemoryLiveBytes/MaxMemoryTextBytes {
				t.Fatalf("wrote %d memories of %d bytes with a %d byte budget", index+1, MaxMemoryTextBytes, MaxMemoryLiveBytes)
			}
			continue
		}
		if !errors.Is(err, ErrMemoryBudget) {
			t.Fatalf("Remember() error = %v, want ErrMemoryBudget", err)
		}
		break
	}

	// Retiring one makes room, because the budget is what the agent still knows
	// rather than what the log holds.
	retirement := testMemoryRevision()
	retirement.Memory = "what-i-learned-0"
	retirement.Text = "this stopped being true when the checks changed"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement error = %v", err)
	}
	fresh := testMemoryRevision()
	fresh.Memory = "what-i-learned-next"
	fresh.Text = strings.Repeat("b", MaxMemoryTextBytes)
	if _, err := store.Remember(context.Background(), fresh); err != nil {
		t.Fatalf("Remember() after a retirement error = %v", err)
	}
}

// TestAFullLogRollsRatherThanRefusing is the property the log size has to have:
// it is where the history is set aside, never a size at which the store stops
// working. A wall here would be one nothing could climb back over, because the way
// out of a full store is to compact or to retire, and both are writes on the path
// the wall would be in.
func TestAFullLogRollsRatherThanRefusing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	// Small enough that a handful of revisions of one memory crosses it, so the
	// roll is reached without writing the four megabytes the harness's own bound is.
	store.rollAt = 3000

	for round := 0; round < 12; round++ {
		revision := testMemoryRevision()
		revision.Text = fmt.Sprintf("what the operator wanted on round %d: %s", round, strings.Repeat("x", 400))
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() on round %d error = %v", round, err)
		}
	}

	archives, err := store.archivePaths("product-manager")
	if err != nil {
		t.Fatalf("archivePaths() error = %v", err)
	}
	if len(archives) == 0 {
		t.Fatal("the log grew past its size and nothing was rolled aside")
	}
	if size, err := store.logSize("product-manager", filepath.Join(store.Root(), "product-manager.memory.jsonl")); err != nil {
		t.Fatalf("logSize() error = %v", err)
	} else if size > store.rollAt {
		t.Errorf("the live log is %d bytes and the roll is at %d", size, store.rollAt)
	}

	// The history is whole across the archives and the log: twelve revisions were
	// written and twelve are readable, each one still naming the invocation that
	// produced it, and none of them counted twice because the roll carried it
	// across.
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want 1", len(memories))
	}
	revisions := memories[0].Revisions
	if len(revisions) != 12 {
		t.Fatalf("the memory has %d revisions, want the 12 that were written", len(revisions))
	}
	for index, revision := range revisions {
		if revision.Sequence != index+1 {
			t.Fatalf("revision %d is numbered %d; the history is out of order or a number was reused", index, revision.Sequence)
		}
		if revision.Invocation.ID == "" {
			t.Errorf("revision %d lost the invocation that produced it in the roll", revision.Sequence)
		}
	}
	if !strings.Contains(revisions[11].Text, "round 11") {
		t.Errorf("the last revision is %q, want the one written last", revisions[11].Text)
	}
}

// TestARolledStoreStillCompactsAndRetires is the other half of the finding the
// roll answers: the operations that make room have to keep working at the size
// where room runs out, or a full store is a permanently stuck one.
func TestARolledStoreStillCompactsAndRetires(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	store.rollAt = 2000
	for round := 0; round < 6; round++ {
		revision := testMemoryRevision()
		revision.Text = fmt.Sprintf("round %d: %s", round, strings.Repeat("y", 300))
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() on round %d error = %v", round, err)
		}
	}
	compaction := testMemoryRevision()
	compaction.Text = "all of that, said once"
	compaction.Compacts = []int{1, 2, 3, 4, 5, 6}
	if _, err := store.Remember(context.Background(), compaction); err != nil {
		t.Fatalf("Remember() a compaction into a rolled store error = %v", err)
	}
	retirement := testMemoryRevision()
	retirement.Text = "and it stopped being true"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement into a rolled store error = %v", err)
	}
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if got := len(memories[0].Revisions); got != 8 {
		t.Fatalf("the memory has %d revisions, want the 8 that were written", got)
	}
	if !memories[0].Retired() {
		t.Errorf("the memory is not retired after a retirement was recorded")
	}
	// A compaction written across a roll still names what it replaced, which is the
	// provenance the design requires of one.
	if got := memories[0].Revisions[6].Compacts; len(got) != 6 {
		t.Errorf("the compaction names %v, want the six revisions it replaced", got)
	}
}

// TestAWriteReadsTheTipRatherThanTheHistory is the cost of a write held to what
// the agent knows now rather than to how long it has known it. An agent grows
// through several rolls, one of its memories retired early enough that its last
// number lives only in an archive, and a write afterwards reads no archive, reads
// the same bounded amount however many rolls lie behind it, and still numbers
// every memory as the whole history would.
func TestAWriteReadsTheTipRatherThanTheHistory(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	store.rollAt = 2000
	retired := testMemoryRevision()
	retired.Memory = "what-stopped-being-true"
	for _, retiring := range []bool{false, true} {
		retired.Retired = retiring
		if _, err := store.Remember(context.Background(), retired); err != nil {
			t.Fatalf("Remember() %s error = %v", retired.Memory, err)
		}
	}
	grow := func(rolls int) {
		t.Helper()
		for round := 0; ; round++ {
			archives, err := store.archivePaths("product-manager")
			if err != nil {
				t.Fatalf("archivePaths() error = %v", err)
			}
			if len(archives) >= rolls {
				return
			}
			revision := testMemoryRevision()
			revision.Text = fmt.Sprintf("round %d: %s", round, strings.Repeat("z", 300))
			if _, err := store.Remember(context.Background(), revision); err != nil {
				t.Fatalf("Remember() on round %d error = %v", round, err)
			}
		}
	}
	// costOfAWrite is what one write reads, file by file.
	costOfAWrite := func() (map[string]int64, MemoryRevision) {
		t.Helper()
		read := map[string]int64{}
		store.observeRead = func(file string, bytes int64) { read[file] += bytes }
		defer func() { store.observeRead = nil }()
		revision := testMemoryRevision()
		revision.Text = "the operator reads reports at leisure, however old this agent is"
		recorded, err := store.Remember(context.Background(), revision)
		if err != nil {
			t.Fatalf("Remember() error = %v", err)
		}
		return read, recorded
	}
	total := func(read map[string]int64) (sum int64) {
		for _, bytes := range read {
			sum += bytes
		}
		return sum
	}

	grow(3)
	young, _ := costOfAWrite()
	grow(9)
	old, recorded := costOfAWrite()
	for _, read := range []map[string]int64{young, old} {
		for file := range read {
			if strings.Contains(file, memoryArchiveMiddle) {
				t.Fatalf("a write read the archive %s: %v", file, read)
			}
		}
		// The tip is one live memory and one retired head, and the log is checked
		// by its last line and its last byte, so a write reads a few kilobytes.
		if got := total(read); got > 8<<10 {
			t.Fatalf("a write read %d bytes (%v), want the tip and one line", got, read)
		}
	}
	if total(old) > total(young)+64 {
		t.Errorf("a write after nine rolls read %d bytes and after three %d; the cost is growing with the history", total(old), total(young))
	}

	// The whole history is still what the read surface returns, numbered as it
	// was written, across every archive and the log.
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	var kept Memory
	for _, memory := range memories {
		if memory.Name == "how-the-operator-reads" {
			kept = memory
		}
	}
	if len(kept.Revisions) != recorded.Sequence {
		t.Fatalf("the memory has %d revisions and the last write was numbered %d", len(kept.Revisions), recorded.Sequence)
	}
	for index, revision := range kept.Revisions {
		if revision.Sequence != index+1 {
			t.Fatalf("revision %d is numbered %d; a number was reused or skipped", index, revision.Sequence)
		}
	}

	// A retired memory's last number is in an archive and no longer in the log,
	// and a write that names it again continues from it rather than starting over.
	// Every archive is overwritten with unreadable bytes of the same size first, so
	// a write that read one would be refused rather than quietly right.
	archives, err := store.archivePaths("product-manager")
	if err != nil {
		t.Fatalf("archivePaths() error = %v", err)
	}
	for _, archive := range archives {
		info, err := os.Stat(archive)
		if err != nil {
			t.Fatalf("stat %s: %v", archive, err)
		}
		if err := os.WriteFile(archive, []byte(strings.Repeat("#", int(info.Size()))), 0o600); err != nil {
			t.Fatalf("overwrite %s: %v", archive, err)
		}
	}
	revived := testMemoryRevision()
	revived.Memory = "what-stopped-being-true"
	again, err := store.Remember(context.Background(), revived)
	if err != nil {
		t.Fatalf("Remember() a retired memory again error = %v", err)
	}
	if again.Sequence != 3 {
		t.Errorf("the retired memory written again is numbered %d, want 3 after its two archived revisions", again.Sequence)
	}
}

// TestAMissingOrDisagreeingTipIsRebuiltAndSaysSo is the tip held to the history it
// stands for. It is derived, so where it is missing, will not decode, or was
// recorded against a log that has since changed, a write rebuilds it from the
// history rather than numbering from it, and the rebuilt tip records why.
func TestAMissingOrDisagreeingTipIsRebuiltAndSaysSo(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	tipPath := filepath.Join(store.Root(), "product-manager.memory.tip.json")
	readTip := func() memoryTip {
		t.Helper()
		stored, err := os.ReadFile(tipPath)
		if err != nil {
			t.Fatalf("read the tip: %v", err)
		}
		var tip memoryTip
		if err := json.Unmarshal(stored, &tip); err != nil {
			t.Fatalf("decode the tip: %v", err)
		}
		return tip
	}
	write := func() MemoryRevision {
		t.Helper()
		recorded, err := store.Remember(context.Background(), testMemoryRevision())
		if err != nil {
			t.Fatalf("Remember() error = %v", err)
		}
		return recorded
	}

	// An agent with no history has no tip to disagree with, so its first write
	// records one without calling it a rebuild.
	write()
	if tip := readTip(); tip.Rebuilt != nil || tip.Heads["how-the-operator-reads"].Sequence != 1 {
		t.Fatalf("the first tip is %+v, want revision 1 recorded and no rebuild", tip)
	}
	write()
	if tip := readTip(); tip.Rebuilt != nil {
		t.Fatalf("a tip that matched its history was rebuilt: %+v", tip.Rebuilt)
	}

	cases := []struct {
		name    string
		disturb func()
		because string
	}{
		{"missing", func() {
			if err := os.Remove(tipPath); err != nil {
				t.Fatalf("remove the tip: %v", err)
			}
		}, "no tip was recorded"},
		{"undecodable", func() {
			if err := os.WriteFile(tipPath, []byte("{not a tip}"), 0o600); err != nil {
				t.Fatalf("overwrite the tip: %v", err)
			}
		}, "would not decode"},
		// A revision on the log the tip never heard of: a crash between the append
		// and the tip, or a line put there by hand. It is history, so it is counted.
		{"behind the log", func() {
			unrecorded := testMemoryRevision()
			unrecorded.Sequence = readTip().Heads["how-the-operator-reads"].Sequence + 1
			encoded, err := encodeMemoryRevision(unrecorded)
			if err != nil {
				t.Fatalf("encodeMemoryRevision() error = %v", err)
			}
			appendToMemoryLog(t, filepath.Join(store.Root(), "product-manager.memory.jsonl"), encoded)
		}, "the live log is"},
	}
	for _, each := range cases {
		memories, _, err := store.Memories("product-manager")
		if err != nil {
			t.Fatalf("Memories() error = %v", err)
		}
		want := memories[0].Current().Sequence + 1
		each.disturb()
		if each.name == "behind the log" {
			want++
		}
		recorded := write()
		if recorded.Sequence != want {
			t.Errorf("%s: the write is numbered %d, want %d from the history", each.name, recorded.Sequence, want)
		}
		tip := readTip()
		if tip.Rebuilt == nil || !strings.Contains(tip.Rebuilt.Because, each.because) {
			t.Errorf("%s: the tip records rebuild %+v, want one saying %q", each.name, tip.Rebuilt, each.because)
		}
		if tip.Heads["how-the-operator-reads"].Sequence != recorded.Sequence {
			t.Errorf("%s: the rebuilt tip heads at %d, want %d", each.name, tip.Heads["how-the-operator-reads"].Sequence, recorded.Sequence)
		}
	}

	// A write past a tip that matches carries the last rebuild's account forward,
	// so the record of one outlives the write that made it.
	before := readTip().Rebuilt
	write()
	if after := readTip().Rebuilt; after == nil || after.Because != before.Because {
		t.Errorf("the rebuild's account was %+v and is now %+v", before, after)
	}
}

func TestMemoryStoreRefusesTextBeyondOneRevisionsBound(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	revision := testMemoryRevision()
	revision.Text = strings.Repeat("a", MaxMemoryTextBytes+1)
	if _, err := store.Remember(context.Background(), revision); err == nil {
		t.Fatal("Remember() accepted a revision past the per-revision bound")
	}
}

func TestMemoryStoreCompactionNamesWhatItReplaced(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	for _, text := range []string{"reports are read at leisure", "the plain word is preferred", "metaphor is cut"} {
		revision := testMemoryRevision()
		revision.Text = text
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() error = %v", err)
		}
	}
	compaction := testMemoryRevision()
	compaction.Text = "the operator reads at leisure and wants the plain word, without metaphor"
	compaction.Compacts = []int{1, 2, 3}
	recorded, err := store.Remember(context.Background(), compaction)
	if err != nil {
		t.Fatalf("Remember() a compaction error = %v", err)
	}
	if !recorded.Compacted() {
		t.Errorf("the compaction does not report itself as one")
	}

	memories, _, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	memory := memories[0]
	if !memory.Compacted() {
		t.Errorf("the memory does not say its current revision was compacted")
	}
	// The provenance is the point: the compacted revisions are still readable, and
	// the compaction says which ones it stands for.
	if len(memory.Revisions) != 4 {
		t.Fatalf("the memory has %d revisions, want 4", len(memory.Revisions))
	}
	if got := memory.Current().Compacts; len(got) != 3 {
		t.Errorf("the compaction names %v, want three revisions", got)
	}
}

func TestMemoryStoreRefusesACompactionOfARevisionThatIsNotThere(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	compaction := testMemoryRevision()
	compaction.Compacts = []int{4}
	if _, err := store.Remember(context.Background(), compaction); err == nil {
		t.Fatal("Remember() accepted provenance pointing at a revision that is not there")
	}
}

func TestMemoryStoreSeparatesWhatIsLiveFromWhatIsRecorded(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	kept := testMemoryRevision()
	kept.Memory = "how-the-operator-reads"
	if _, err := store.Remember(context.Background(), kept); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	subject := testMemoryRevision()
	subject.Memory = "what-this-item-needs"
	subject.Continuity = MemoryContinuitySubject
	subject.Subject = "yoyodyne-ifd.308"
	if _, err := store.Remember(context.Background(), subject); err != nil {
		t.Fatalf("Remember() a subject memory error = %v", err)
	}
	retirement := subject
	retirement.Sequence = 0
	retirement.Text = "the item landed, so this is done with"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement error = %v", err)
	}

	live, _, err := store.Live("product-manager")
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(live) != 1 || live[0].Name != "how-the-operator-reads" {
		t.Fatalf("Live() returned %v, want only the memory that was not retired", live)
	}
	// The retired memory is out of the live set and still in the record, which is
	// what makes retiring one different from removing it.
	recorded, _, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("Memories() returned %d memories, want 2", len(recorded))
	}
}

func TestMemoryStoreRefusesAMemoryThatChangesWhatItIsAbout(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	moved := testMemoryRevision()
	moved.Continuity = MemoryContinuitySubject
	moved.Subject = "yoyodyne-ifd.308"
	if _, err := store.Remember(context.Background(), moved); err == nil {
		t.Fatal("Remember() accepted a memory that changed what it is about")
	}
}

func TestMemoryStoreReportsAnUnreadableLineWithoutLosingTheRest(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open the memory log: %v", err)
	}
	if _, err := file.WriteString("{not a memory}\n"); err != nil {
		t.Fatalf("write a corrupt line: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close the memory log: %v", err)
	}

	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want the one that reads", len(memories))
	}
	if len(problems) != 1 || problems[0].Line != 2 {
		t.Fatalf("Memories() reported %v, want one problem on line 2", problems)
	}
	// The file is named as well as the line: an agent's history is its log and the
	// archives rolled off it, so a line number alone sends a reader to the wrong
	// file.
	if problems[0].Log != "product-manager.memory.jsonl" {
		t.Errorf("the problem names log %q, want the live log", problems[0].Log)
	}
	// A writer may not carry on over a line it could not read, because the number
	// it is about to assign is worked out from what it could read.
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err == nil {
		t.Fatal("Remember() wrote into a log with an unreadable line")
	}
}

// TestMemoryStoreReadsAndWritesPastATornLastLine is a crash mid-append: the log
// ends partway through a revision. One crash must not lock an agent out of its
// memory, so the reader names the torn line and carries on, and the next write
// sets the fragment aside and lands whole.
func TestMemoryStoreReadsAndWritesPastATornLastLine(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	interrupted := testMemoryRevision()
	interrupted.Sequence = 2
	interrupted.Text = "the operator reads the interrupted revision"
	encoded, err := encodeMemoryRevision(interrupted)
	if err != nil {
		t.Fatalf("encodeMemoryRevision() error = %v", err)
	}
	fragment := encoded[:len(encoded)/2]
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	appendToMemoryLog(t, path, fragment)

	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(memories) != 1 || len(memories[0].Revisions) != 1 {
		t.Fatalf("Memories() returned %v, want the one revision that reads", memories)
	}
	if len(problems) != 1 || !problems[0].Torn || problems[0].Line != 2 {
		t.Fatalf("Memories() reported %v, want the torn line 2 named", problems)
	}

	written := testMemoryRevision()
	written.Text = "the operator reads reports at leisure, after the crash"
	recorded, err := store.Remember(context.Background(), written)
	if err != nil {
		t.Fatalf("Remember() after a torn append error = %v", err)
	}
	// The torn write was never acknowledged, so nobody numbered after it and the
	// number it would have taken is still free.
	if recorded.Sequence != 2 {
		t.Errorf("the write after the tear is numbered %d, want 2", recorded.Sequence)
	}
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() a second time after the tear error = %v", err)
	}

	memories, problems, err = store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	// The crash stays in front of whoever reads this agent's memory after the
	// write that mended it: the fragment set aside is reported, torn, until an
	// operator removes it, and it blocks no write while it is.
	if len(problems) != 1 || !problems[0].Torn || problems[0].Log != "product-manager.memory.torn-0001" ||
		!strings.Contains(problems[0].String(), "remove it") {
		t.Fatalf("Memories() reported %v after the tear was mended, want the set-aside fragment named with its repair", problems)
	}
	if len(memories) != 1 || len(memories[0].Revisions) != 3 {
		t.Fatalf("Memories() returned %v, want three revisions of one memory", memories)
	}
	if memories[0].Revisions[1].Text != written.Text {
		t.Errorf("revision 2 reads %q, want the write after the tear", memories[0].Revisions[1].Text)
	}
	// The fragment is set aside rather than thrown away: what was on the disk is
	// still on it, where an operator can read it.
	aside, err := os.ReadFile(filepath.Join(store.Root(), "product-manager.memory.torn-0001"))
	if err != nil {
		t.Fatalf("read the torn end set aside: %v", err)
	}
	if string(aside) != string(fragment) {
		t.Errorf("the torn file holds %q, want the fragment %q", aside, fragment)
	}
	agents, err := store.Agents()
	if err != nil {
		t.Fatalf("Agents() error = %v", err)
	}
	if len(agents) != 1 || agents[0] != "product-manager" {
		t.Errorf("Agents() returned %v, want the torn file not read as an agent", agents)
	}
	// Removing the file is the whole of the repair.
	if err := os.Remove(filepath.Join(store.Root(), "product-manager.memory.torn-0001")); err != nil {
		t.Fatalf("remove the torn end set aside: %v", err)
	}
	if _, problems, err = store.Memories("product-manager"); err != nil || len(problems) != 0 {
		t.Fatalf("Memories() reported %v (%v) after the torn file was removed", problems, err)
	}
}

// TestMemoryStoreRefusesToCutOrAppendThroughALinkOutOfItsDirectory holds the
// store's writes to the memory directory: a log replaced by a link to a file
// elsewhere is refused, and the file it points at is left as it was.
func TestMemoryStoreRefusesToCutOrAppendThroughALinkOutOfItsDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	store := newMemoryStore(t, filepath.Join(base, "state"))
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the memory log: %v", err)
	}
	outside := filepath.Join(base, "elsewhere.jsonl")
	torn := append(append([]byte{}, stored...), []byte(`{"schema_ver`)...)
	if err := os.WriteFile(outside, torn, 0o600); err != nil {
		t.Fatalf("write the file outside: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove the memory log: %v", err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	if _, err := store.Remember(context.Background(), testMemoryRevision()); err == nil {
		t.Fatal("Remember() wrote through a link out of the memory directory")
	}
	after, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read the file outside: %v", err)
	}
	if string(after) != string(torn) {
		t.Errorf("the file outside the memory directory was changed: %q", after)
	}
}

// TestMemoryStoreKeepsAWholeRevisionThatLostOnlyItsNewline is the crash that
// fell between a revision and its newline. The revision is whole and read, so
// the next write terminates it rather than setting it aside.
func TestMemoryStoreKeepsAWholeRevisionThatLostOnlyItsNewline(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	unterminated := testMemoryRevision()
	unterminated.Sequence = 2
	encoded, err := encodeMemoryRevision(unterminated)
	if err != nil {
		t.Fatalf("encodeMemoryRevision() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	appendToMemoryLog(t, path, encoded[:len(encoded)-1])

	recorded, err := store.Remember(context.Background(), testMemoryRevision())
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if recorded.Sequence != 3 {
		t.Errorf("the write is numbered %d, want 3 after the unterminated revision 2", recorded.Sequence)
	}
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 || len(memories) != 1 || len(memories[0].Revisions) != 3 {
		t.Fatalf("Memories() returned %v with %v, want three revisions and no problem", memories, problems)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "product-manager.memory.torn-0001")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a whole revision was set aside as torn: %v", err)
	}
}

// TestMemoryStoreStillRefusesACorruptLineBeforeATornEnd holds the exception to
// the torn end alone: an unreadable line anywhere else is history the writer
// could not read, and the write is refused and leaves the log as it was.
func TestMemoryStoreStillRefusesACorruptLineBeforeATornEnd(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	appendToMemoryLog(t, path, []byte("{not a memory}\n{\"schema_ver"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the memory log: %v", err)
	}

	_, err = store.Remember(context.Background(), testMemoryRevision())
	if err == nil {
		t.Fatal("Remember() wrote past a corrupt line that is not the torn end")
	}
	if !strings.Contains(err.Error(), "product-manager.memory.jsonl line 2") {
		t.Errorf("Remember() error = %v, want the corrupt line named", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the memory log: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused write changed the log")
	}
}

func appendToMemoryLog(t *testing.T, path string, content []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open the memory log: %v", err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("append to the memory log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close the memory log: %v", err)
	}
}

func TestMemoryStoreReportsARevisionThatBelongsToAnotherAgent(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	transplanted := testMemoryRevision()
	transplanted.Agent = "architect"
	transplanted.Role = domain.RoleArchitect
	transplanted.Sequence = 2
	encoded, err := encodeMemoryRevision(transplanted)
	if err != nil {
		t.Fatalf("encodeMemoryRevision() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open the memory log: %v", err)
	}
	if _, err := file.Write(encoded); err != nil {
		t.Fatalf("write the transplanted revision: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close the memory log: %v", err)
	}

	_, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Problem, "architect") {
		t.Fatalf("Memories() reported %v, want the transplanted revision named", problems)
	}
}

func TestMemoryStoreListsTheAgentsItHolds(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	agents, err := store.Agents()
	if err != nil {
		t.Fatalf("Agents() error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("Agents() returned %v before anything was written", agents)
	}
	for _, agent := range []string{"product-manager", "architect"} {
		revision := testMemoryRevision()
		revision.Agent = agent
		if agent == "architect" {
			revision.Role = domain.RoleArchitect
		}
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() for %s error = %v", agent, err)
		}
	}
	agents, err = store.Agents()
	if err != nil {
		t.Fatalf("Agents() error = %v", err)
	}
	if len(agents) != 2 || agents[0] != "architect" || agents[1] != "product-manager" {
		t.Fatalf("Agents() returned %v, want both in name order", agents)
	}
}

func TestMemoryStoreRefusesAnotherProductsRevision(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	elsewhere := testMemoryRevision()
	elsewhere.ProductID = "beads"
	if _, err := store.Remember(context.Background(), elsewhere); err == nil {
		t.Fatal("Remember() accepted a revision belonging to another product")
	}
}

func TestMemoryStoreRefusesAnAgentNameThatIsAPath(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	escaping := testMemoryRevision()
	escaping.Agent = "../../elsewhere"
	if _, err := store.Remember(context.Background(), escaping); err == nil {
		t.Fatal("Remember() accepted an agent name that is a path")
	}
	if _, _, err := store.Memories("../../elsewhere"); err == nil {
		t.Fatal("Memories() accepted an agent name that is a path")
	}
}

// TestAConversationRecordRefusesAMemory is the design's "no fourth store" rule
// held to the record it is most likely to be broken in. Memory is a store of its
// own that may reference a conversation and copies none of it; a memory written
// into the conversation record instead would be a second copy of what an agent
// knows, in a record every turn rewrites in place.
//
// Nothing has to be added to refuse it — the conversation decoder reads nothing it
// does not declare — and it is pinned here because that decoder's strictness is
// what the rule now rests on.
func TestAConversationRecordRefusesAMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("create the conversation directory: %v", err)
	}
	record := `{"schema_version":1,"conversation_id":"chat-0123456789abcdef0123456789abcdef",` +
		`"product_id":"yoyodyne","repository_id":"yoyodyne","agent":"product-manager",` +
		`"role":"product-manager","backend":"claude-code","turns":0,"last_sequence":0,` +
		`"started_at":"2026-09-06T09:00:00Z","updated_at":"2026-09-06T09:00:00Z",` +
		`"memories":[{"memory":"how-the-operator-reads","text":"at leisure"}]}`
	path := filepath.Join(store.Root(), "product-manager.json")
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatalf("write the conversation record: %v", err)
	}
	_, err = store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err == nil {
		t.Fatal("Load() accepted a conversation record carrying memories")
	}
	if !strings.Contains(err.Error(), "memories") {
		t.Errorf("Load() error = %v, want the refused field named", err)
	}
}

func newMemoryStore(t *testing.T, root string) *MemoryStore {
	t.Helper()
	store, err := NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	return store
}

// testMemoryRevision is one revision as an agent would offer it: unnumbered,
// with the invocation that produced it pinned to everything a durable provider
// invocation records.
func testMemoryRevision() MemoryRevision {
	return MemoryRevision{
		SchemaVersion: MemorySchemaVersion,
		ProductID:     "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Memory:        "how-the-operator-reads",
		Continuity:    MemoryContinuityAgent,
		Text:          "the operator reads reports at leisure",
		Sources: []MemorySource{
			{Kind: MemorySourceConversation, ID: "chat-0123456789abcdef0123456789abcdef"},
			{Kind: MemorySourceWorkItem, ID: "yoyodyne-ifd.308"},
		},
		Invocation: MemoryInvocation{
			Kind:           MemoryInvocationConversation,
			ID:             "chat-0123456789abcdef0123456789abcdef",
			Turn:           4,
			Backend:        "claude-code",
			Model:          "opus",
			ResolvedModel:  "claude-opus-5-20260514",
			AccountAlias:   "research",
			ConfigRevision: "cfg-0123456789ab",
			Build:          "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900",
		},
		RecordedAt: time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
	}
}

// A side conversation is a record this harness keeps, so a memory may cite one
// and may say one wrote it — and both are held to a side stream's own identifier
// shape. A citation that named a conversation while claiming to be a side stream
// would be provenance pointing at the wrong thread, which is the failure the
// merge back into an agent's context is most able to hide.
func TestAMemoryCitesASideStreamByItsOwnIdentifier(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	revision := testMemoryRevision()
	revision.Memory = "side-0123456789abcdef0123456789abcdef"
	revision.Sources = []MemorySource{{Kind: MemorySourceSideStream, ID: "side-0123456789abcdef0123456789abcdef"}}
	revision.Invocation = MemoryInvocation{
		Kind:    MemoryInvocationSideStream,
		ID:      "side-0123456789abcdef0123456789abcdef",
		Turn:    3,
		Backend: "claude-code",
		Model:   "opus",
	}
	if _, err := store.Remember(context.Background(), revision); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	// A conversation identifier is not a side stream identifier, at either end.
	wrong := revision
	wrong.Sequence = 0
	wrong.Sources = []MemorySource{{Kind: MemorySourceSideStream, ID: "chat-0123456789abcdef0123456789abcdef"}}
	if _, err := store.Remember(context.Background(), wrong); err == nil {
		t.Error("Remember() recorded a side-stream source naming a conversation")
	}
	wrong = revision
	wrong.Sequence = 0
	wrong.Invocation.ID = "chat-0123456789abcdef0123456789abcdef"
	if _, err := store.Remember(context.Background(), wrong); err == nil {
		t.Error("Remember() recorded a side-stream invocation naming a conversation")
	}
	// A side thread takes turns, so a merge that claims none is a merge of nothing.
	wrong = revision
	wrong.Sequence = 0
	wrong.Invocation.Turn = 0
	if _, err := store.Remember(context.Background(), wrong); err == nil {
		t.Error("Remember() recorded a side-stream invocation that took no turn")
	}
}
