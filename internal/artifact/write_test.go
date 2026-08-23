package artifact

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// aWrite is a well-formed creation the tests vary one field of at a time.
func aWrite() Write {
	return Write{
		Action:    WriteCreate,
		ID:        "v2-goals",
		Kind:      KindGoals,
		Title:     "What v2 is for",
		Supports:  []string{"brief"},
		Directory: "docs/product",
		Body:      "# Goals\n\nShip the thing.",
		Reason:    "drafted with the operator in conversation",
	}
}

func TestExtractWritesTakesADocumentOnlyFromTheBlock(t *testing.T) {
	t.Parallel()

	reply := "Here is the goals document.\n\n" + WriteFence + "\n" +
		`{"documents":[{"action":"create","id":"v2-goals","kind":"goals","title":"What v2 is for","directory":"docs/product","body":"# Goals\n\n` + "```" + `sh\nyoyo status\n` + "```" + `\n","reason":"drafted with the operator"}]}` +
		"\n```\n\nSay the word and I will write it.\n"
	prose, writes, err := ExtractWrites(reply)
	if err != nil {
		t.Fatalf("ExtractWrites() error = %v", err)
	}
	if len(writes) != 1 || writes[0].ID != "v2-goals" {
		t.Fatalf("ExtractWrites() = %#v", writes)
	}
	// A document carrying code fences of its own survives, because its newlines
	// are escaped inside the JSON string rather than being literal ones the fence
	// scanner could stop at. It is the case a documentation repository hits
	// first, and the one that would send everybody back to copy and paste.
	if !strings.Contains(writes[0].Body, "```sh\nyoyo status\n```") {
		t.Fatalf("document body lost its code fence: %q", writes[0].Body)
	}
	if strings.Contains(prose, "documents") || !strings.Contains(prose, "Say the word") {
		t.Fatalf("prose = %q", prose)
	}

	// Markdown in prose is something the role is showing the operator. Reading it
	// as a document to file is exactly the transcription this exists to end.
	plain := "Here is what I would write:\n\n# Goals\n\nShip the thing.\n"
	prose, writes, err = ExtractWrites(plain)
	if err != nil || len(writes) != 0 {
		t.Fatalf("ExtractWrites(prose) = %#v, %v", writes, err)
	}
	if prose != strings.TrimSpace(plain) {
		t.Fatalf("prose = %q", prose)
	}
}

func TestDecodeWritesRefusesWhatTheOperatorWouldNotBeApproving(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"empty":            "  ",
		"no documents":     `{"documents":[]}`,
		"unknown field":    `{"documents":[{"action":"create","id":"v2-goals","kind":"goals","title":"t","directory":"docs/product","body":"b","reason":"r","status":"active"}]}`,
		"trailing content": `{"documents":[{"action":"create","id":"v2-goals","kind":"goals","title":"t","directory":"docs/product","body":"b","reason":"r"}]} and also`,
		"two documents": `{"documents":[` +
			`{"action":"create","id":"a","kind":"goals","title":"t","directory":"docs/product","body":"b","reason":"r"},` +
			`{"action":"create","id":"b","kind":"goals","title":"t","directory":"docs/product","body":"b","reason":"r"}]}`,
		"unknown kind":       `{"documents":[{"action":"create","id":"a","kind":"charter","title":"t","directory":"docs/product","body":"b","reason":"r"}]}`,
		"no reason":          `{"documents":[{"action":"create","id":"a","kind":"goals","title":"t","directory":"docs/product","body":"b"}]}`,
		"no body":            `{"documents":[{"action":"create","id":"a","kind":"goals","title":"t","directory":"docs/product","reason":"r"}]}`,
		"revision with kind": `{"documents":[{"action":"revise","id":"a","kind":"goals","body":"b","reason":"r"}]}`,
		"revision moved":     `{"documents":[{"action":"revise","id":"a","directory":"docs/designs","body":"b","reason":"r"}]}`,
	} {
		if _, err := DecodeWrites(payload); err == nil {
			t.Fatalf("DecodeWrites(%s) accepted %q", name, payload)
		}
	}

	// A revision carries the document whole and names nothing about where it
	// lives, which is the shape a document already on disk is replaced by.
	writes, err := DecodeWrites(`{"documents":[{"action":"revise","id":"v1-goals","body":"# Goals","reason":"the operator narrowed the second goal"}]}`)
	if err != nil {
		t.Fatalf("DecodeWrites(revision) error = %v", err)
	}
	if len(writes) != 1 || writes[0].Action != WriteRevise {
		t.Fatalf("DecodeWrites(revision) = %#v", writes)
	}
}

func TestAWriteIsRefusedForAKindTheRoleDoesNotOwn(t *testing.T) {
	t.Parallel()

	// The ownership table is the whole of the rule, read here rather than
	// restated: the product manager writes intent, the architect writes design,
	// and neither writes the other's.
	if err := aWrite().Authorize(domain.RoleProductManager); err != nil {
		t.Fatalf("the product manager may not write its own goals: %v", err)
	}
	if err := aWrite().Authorize(domain.RoleArchitect); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the architect wrote the goals: %v", err)
	}
	design := aWrite()
	design.Kind = KindDesign
	if err := design.Authorize(domain.RoleArchitect); err != nil {
		t.Fatalf("the architect may not write its own design: %v", err)
	}
	// A role that owns no document cannot use the mechanism at all, on either
	// action, and a proposal to the owner stays its only move.
	for _, role := range []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		if err := design.Authorize(role); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("the %s wrote a design: %v", role, err)
		}
		revision := Write{Action: WriteRevise, ID: "v1-goals", Body: "b", Reason: "r"}
		if err := revision.Authorize(role); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("the %s revised a document: %v", role, err)
		}
	}
	if got := Owned(domain.RoleProductManager); len(got) != 3 {
		t.Fatalf("Owned(product manager) = %v", got)
	}
	if got := Owned(domain.RoleDevelopmentManager); got != nil {
		t.Fatalf("Owned(development manager) = %v, want nothing", got)
	}
}

func TestCheckWriteRefusesTheWrongHomeWithoutTouchingTheRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := Store{
		RepositoryRoot: root,
		Homes:          []string{"docs/product", "docs/designs", "docs/decisions"},
		Excluded:       []string{"docs/decisions/invariants"},
	}
	if err := store.CheckWrite(domain.RoleProductManager, aWrite()); err != nil {
		t.Fatalf("CheckWrite() refused a document in an artifact home: %v", err)
	}

	outside := aWrite()
	outside.Directory = "internal/chat"
	if err := store.CheckWrite(domain.RoleProductManager, outside); err == nil {
		t.Fatal("CheckWrite() admitted a document filed outside every artifact home")
	}
	escaping := aWrite()
	escaping.Directory = "../elsewhere"
	if err := store.CheckWrite(domain.RoleProductManager, escaping); err == nil {
		t.Fatal("CheckWrite() admitted a document filed outside the repository")
	}
	// The invariants keep an identity scheme of their own, so the directory that
	// holds them is not one this mechanism files documents in.
	invariants := aWrite()
	invariants.Kind = KindDecision
	invariants.Directory = "docs/decisions/invariants"
	if err := store.CheckWrite(domain.RoleArchitect, invariants); err == nil {
		t.Fatal("CheckWrite() admitted a document in the invariants directory")
	}

	// None of that wrote anything, which is the point of checking before the
	// operator is asked rather than after they approved.
	entries, err := filepath.Glob(filepath.Join(root, "*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("CheckWrite() touched the repository: %v", entries)
	}
}

func TestAnApprovedWriteBecomesADocumentWithFrontmatterAndAnApproval(t *testing.T) {
	t.Parallel()

	store := Store{RepositoryRoot: t.TempDir(), Homes: []string{"docs/product"}}
	written, err := store.Create(domain.RoleProductManager, aWrite().Draft(), moment())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if written.Path != "docs/product/v2-goals.md" || written.Status != StatusActive {
		t.Fatalf("written document = %#v", written)
	}
	if len(written.Revisions) != 1 || written.Revisions[0].By != domain.RoleProductManager {
		t.Fatalf("revisions = %#v", written.Revisions)
	}
	approved, err := store.Approve(written.ID, "approved by the operator in conversation chat-1", moment())
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if approved.ApprovalState() != ApprovalApproved {
		t.Fatalf("approval state = %q", approved.ApprovalState())
	}

	// A revision through the same mechanism replaces the document and appends to
	// the log rather than rewriting it, so the approval above still names the
	// revision it was given for.
	revision := Write{Action: WriteRevise, ID: "v2-goals", Body: "# Goals\n\nShip it twice.", Reason: "the operator narrowed it"}
	amended, err := store.Amend(domain.RoleProductManager, revision.ID, revision.Amendment(), moment())
	if err != nil {
		t.Fatalf("Amend() error = %v", err)
	}
	if len(amended.Revisions) != 2 || amended.Title != written.Title {
		t.Fatalf("amended document = %#v", amended)
	}
	if amended.ApprovalState() != ApprovalAmended {
		t.Fatalf("a document amended after approval reads as %q", amended.ApprovalState())
	}
	// The store is what refuses a role writing somebody else's document, whatever
	// the action layer did or did not catch first.
	if _, err := store.Amend(domain.RoleArchitect, "v2-goals", revision.Amendment(), moment()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the architect revised the goals: %v", err)
	}
}

func TestTheWriteContractStatesTheBoundsItIsHeldTo(t *testing.T) {
	t.Parallel()

	contract := WriteContract(domain.RoleArchitect, []string{"docs/designs", "docs/decisions"})
	for _, required := range []string{
		"yoyodyne-artifact",
		"docs/designs",
		`"design"`,
		maxWritesPerReplyText + " document",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("the write contract does not state %q: %s", required, contract)
		}
	}
	// A role is never told it may write a kind the ownership table gives to
	// somebody else.
	if strings.Contains(contract, `"goals"`) {
		t.Fatalf("the architect's contract offers it the goals: %s", contract)
	}
	// A role that owns nothing, and a project the harness cannot write for, are
	// told nothing at all: a mechanism every attempt would be refused by is an
	// invitation to attempt it.
	if got := WriteContract(domain.RoleDeveloper, []string{"docs/designs"}); got != "" {
		t.Fatalf("the developer was offered a write contract: %s", got)
	}
	if got := WriteContract(domain.RoleArchitect, nil); got != "" {
		t.Fatalf("a conversation with no artifact homes was offered a write contract: %s", got)
	}
	// The bound the contract states is the bound the decoder enforces, so a role
	// held to a number it was never told is impossible.
	if MaxWritesPerReply != 1 || maxWritesPerReplyText != "one" {
		t.Fatalf("the stated bound (%s) and the enforced one (%d) disagree", maxWritesPerReplyText, MaxWritesPerReply)
	}
}
