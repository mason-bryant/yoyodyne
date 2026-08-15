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

func TestBackendSupportsRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend Backend
		role    AgentRole
		want    bool
	}{
		{name: "claude developer", backend: BackendClaudeCode, role: RoleDeveloper, want: true},
		{name: "claude custom role", backend: BackendClaudeCode, role: "security-reviewer", want: true},
		{name: "codex developer", backend: BackendCodex, role: RoleDeveloper, want: true},
		{name: "codex reviewer", backend: BackendCodex, role: RoleReviewer, want: true},
		{name: "codex product manager", backend: BackendCodex, role: RoleProductManager, want: false},
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
