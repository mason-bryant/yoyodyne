package protectedpath

import (
	"strings"
	"testing"
)

// The path the criticals were filed about, and the one every case here is
// decided against.
const settingsPath = ".claude/settings.json"

func TestAGrantNamingAProviderRefusedPathIsBeyondIt(t *testing.T) {
	t.Parallel()

	beyond := BeyondGrant([]string{settingsPath})
	if len(beyond) != 1 || beyond[0].Path != settingsPath {
		t.Fatalf("BeyondGrant() = %#v, want the settings file", beyond)
	}
	if beyond[0].Provider == "" {
		t.Fatalf("BeyondGrant() returned an entry naming no provider: %#v", beyond[0])
	}
	// A grant of the directory admits what is inside it, so it grants the settings
	// file exactly as surely as one that names the file.
	if beyond := BeyondGrant([]string{".claude"}); len(beyond) == 0 {
		t.Fatalf("BeyondGrant() on the directory = %#v, want the file inside it reached", beyond)
	}
	// Nearly every grant is an ordinary one, and none of those is refused here.
	if beyond := BeyondGrant([]string{"docs/designs/v1-harness-design.md", ".yoyodyne"}); len(beyond) != 0 {
		t.Fatalf("BeyondGrant() on ordinary grants = %#v, want nothing", beyond)
	}
	// The separator is required on both sides, so a sibling is not inside.
	if beyond := BeyondGrant([]string{".claude-settings"}); len(beyond) != 0 {
		t.Fatalf("BeyondGrant() on a sibling = %#v, want nothing", beyond)
	}
}

func TestGrantProblemsReadsAnItemsTextAndNamesTheProvider(t *testing.T) {
	t.Parallel()

	// Prose that discusses the path grants nothing, which is what lets an item
	// about this boundary be admitted at all — including the one that asked for
	// this gate, whose description names the file repeatedly.
	description := "Claude Code refuses agent writes to " + settingsPath + " at the tool-permission layer."
	if problems := GrantProblems("A grant naming a provider-protected path", description); len(problems) != 0 {
		t.Fatalf("GrantProblems() on prose = %v, want nothing granted", problems)
	}

	granted := description + "\n\n" + GrantMarker + " " + settingsPath + "\n"
	problems := GrantProblems("Write the hook into the settings", granted)
	if len(problems) != 1 {
		t.Fatalf("GrantProblems() = %v, want the one grant refused", problems)
	}
	// The refusal has three jobs: name the path, name whose refusal it is, and say
	// what to do instead. A refusal missing the last of them is one the role
	// answers by wording the grant differently.
	refusal := problems[0].Error()
	for _, want := range []string{settingsPath, "Claude Code", "operator", GrantMarker} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal %q never names %q", refusal, want)
		}
	}
}

// Every recorded path has to be a path, and it has to be written the way a grant
// normalizes to, or the comparison silently never matches and the whole gate is
// a table nothing reads.
func TestEveryRecordedProviderPathIsComparableToAGrant(t *testing.T) {
	t.Parallel()

	for _, entry := range ProviderPaths {
		clean, ok := normalize(entry.Path)
		if !ok || clean != entry.Path {
			t.Fatalf("ProviderPaths entry %q normalizes to %q (ok=%v), want it recorded in the form a grant is compared in", entry.Path, clean, ok)
		}
		if entry.Provider == "" || entry.Refusal == "" {
			t.Fatalf("ProviderPaths entry %q says nothing about who refuses it or how: %#v", entry.Path, entry)
		}
		if beyond := BeyondGrant([]string{entry.Path}); len(beyond) != 1 || beyond[0].Path != entry.Path {
			t.Fatalf("BeyondGrant() on the recorded path %q = %#v, want it reached", entry.Path, beyond)
		}
	}
}
