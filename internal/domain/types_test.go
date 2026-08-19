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
