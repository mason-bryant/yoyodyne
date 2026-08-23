package domain

import "testing"

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"yoyodyne", "product-1", "a1"} {
		if err := ValidateIdentifier("identifier", value); err != nil {
			t.Errorf("ValidateIdentifier(%q) returned error: %v", value, err)
		}
	}

	for _, value := range []string{"", "Yoyodyne", "two_words", "-leading", "trailing-"} {
		if err := ValidateIdentifier("identifier", value); err == nil {
			t.Errorf("ValidateIdentifier(%q) returned nil", value)
		}
	}
}

func TestAgentRoleValid(t *testing.T) {
	t.Parallel()

	for _, role := range Roles() {
		if !role.Valid() {
			t.Errorf("Valid() = false for role %q", role)
		}
	}

	// A typo, a role from another tool, a role that has not been added to the
	// harness, and no role at all: none of them name authority anybody wrote.
	for _, role := range []AgentRole{"", "developor", "Developer", "security-reviewer", "tech-lead"} {
		if role.Valid() {
			t.Errorf("Valid() = true for role %q", role)
		}
	}
}

func TestRolesIsTheWholeSet(t *testing.T) {
	t.Parallel()

	want := []AgentRole{RoleProductManager, RoleArchitect, RoleDevelopmentManager, RoleDeveloper, RoleReviewer}
	got := Roles()
	if len(got) != len(want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	for index, role := range want {
		if got[index] != role {
			t.Fatalf("Roles()[%d] = %q, want %q", index, got[index], role)
		}
	}
}

func TestBackendSupportsRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend Backend
		role    AgentRole
		want    bool
	}{
		{name: "claude developer", backend: BackendClaudeCode, role: RoleDeveloper, want: true},
		{name: "claude architect", backend: BackendClaudeCode, role: RoleArchitect, want: true},
		// The set of roles is fixed in the harness, so no backend serves a name
		// outside it — not even the one that serves every role there is.
		{name: "claude unknown role", backend: BackendClaudeCode, role: "security-reviewer", want: false},
		{name: "claude typoed role", backend: BackendClaudeCode, role: "developor", want: false},
		{name: "claude empty role", backend: BackendClaudeCode, role: "", want: false},
		{name: "codex developer", backend: BackendCodex, role: RoleDeveloper, want: true},
		{name: "codex reviewer", backend: BackendCodex, role: RoleReviewer, want: true},
		{name: "codex product manager", backend: BackendCodex, role: RoleProductManager, want: false},
		{name: "codex unknown role", backend: BackendCodex, role: "security-reviewer", want: false},
		{name: "unknown backend", backend: "other", role: RoleDeveloper, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.backend.SupportsRole(tt.role); got != tt.want {
				t.Fatalf("SupportsRole() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The two questions an executor answers are deliberately not the same one. What
// the harness recognizes decides whether a marker may be written; what is a
// developer run decides whether an item may be selected — and an unrecognized
// marker answers no to both, so a typo costs an item nobody pulls rather than
// the run the marker exists to save.
func TestAnUnrecognizedExecutorIsStillNotADeveloperRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executor     WorkItemExecutor
		valid        bool
		developerRun bool
	}{
		{name: "none named", executor: "", valid: false, developerRun: true},
		{name: "whitespace only", executor: "  ", valid: false, developerRun: true},
		{name: "a conversation with a named role", executor: ConversationWith(RoleArchitect), valid: true, developerRun: false},
		// The bare marker is the same case as a typo for writing and the opposite of
		// it for selection: nothing may be marked with it now that a marker names a
		// role, and every item marked with it before is still work no run carries.
		{name: "a conversation naming no role", executor: WorkItemExecutorConversation, valid: false, developerRun: false},
		{name: "a conversation with a role that is not one", executor: "conversation:security-reviewer", valid: false, developerRun: false},
		{name: "a role that is not an executor", executor: "architect", valid: false, developerRun: false},
		{name: "a typo", executor: "converstaion", valid: false, developerRun: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.executor.Valid(); got != tt.valid {
				t.Fatalf("Valid() = %v, want %v", got, tt.valid)
			}
			if got := tt.executor.DeveloperRun(); got != tt.developerRun {
				t.Fatalf("DeveloperRun() = %v, want %v", got, tt.developerRun)
			}
		})
	}
}

// The marker's whole point is that somebody can be named from it. Every role has
// one, because which roles carry work in conversation is a product judgement
// rather than a fact about the harness, and a role left out of the vocabulary is
// work that would have to be marked with a lie or not marked at all.
func TestEveryRoleHasAnExecutorThatNamesIt(t *testing.T) {
	t.Parallel()

	for _, role := range Roles() {
		executor := ConversationWith(role)
		if !executor.Valid() {
			t.Fatalf("ConversationWith(%q) = %q, which is not an executor an item may be marked with", role, executor)
		}
		if executor.Role() != role {
			t.Fatalf("%q names %q, want %q", executor, executor.Role(), role)
		}
		if executor.DeveloperRun() {
			t.Fatalf("%q reads as a developer run", executor)
		}
	}
}

// A marker that names no role somebody could open a conversation with says so,
// rather than being read as naming one. Work marked before the marker carried a
// role is the case that matters: it is unattributed, and attributing it to a
// role nobody chose would send an operator to somebody who was never handed it.
func TestAMarkerThatNamesNoRoleSaysSo(t *testing.T) {
	t.Parallel()

	for _, executor := range []WorkItemExecutor{
		"",
		WorkItemExecutorConversation,
		"conversation:",
		"conversation:security-reviewer",
		"conversation:architect:extra",
		"converstaion:architect",
		"architect",
	} {
		if role := executor.Role(); role != "" {
			t.Fatalf("%q names the role %q, want no role at all", executor, role)
		}
	}
}
