package domain

import (
	"fmt"
	"regexp"
)

type ProductID string

type RepositoryID string

// AgentRole names one of the harness's fixed roles. The set is closed: what a
// project configures is which agents fill these roles, how many, and each one's
// backend, model selector, and persona. Role authority and per-role tool posture
// are derived from the role in code, so a name outside this set names authority
// nobody wrote, and adding a role is a change to the harness rather than to a
// configuration file.
type AgentRole string

const (
	RoleProductManager     AgentRole = "product-manager"
	RoleArchitect          AgentRole = "architect"
	RoleDevelopmentManager AgentRole = "development-manager"
	RoleDeveloper          AgentRole = "developer"
	RoleReviewer           AgentRole = "reviewer"
)

// Roles are the harness's roles in the order the hierarchy runs: product intent,
// then design, then decomposition, then the two roles that do the work inside a
// run. It is the whole set, so a caller that has to name what is allowed reads
// it from here rather than repeating the list.
func Roles() []AgentRole {
	return []AgentRole{
		RoleProductManager,
		RoleArchitect,
		RoleDevelopmentManager,
		RoleDeveloper,
		RoleReviewer,
	}
}

// Valid reports whether a name is one of the harness's roles. An unrecognized
// name — a typo in an agents block, most often — is refused rather than carried
// as a role nothing knows how to run, because every posture the harness derives
// from a role has no answer for it.
func (r AgentRole) Valid() bool {
	switch r {
	case RoleProductManager, RoleArchitect, RoleDevelopmentManager, RoleDeveloper, RoleReviewer:
		return true
	default:
		return false
	}
}

type Backend string

const (
	BackendClaudeCode Backend = "claude-code"
	BackendCodex      Backend = "codex"
)

type ApprovalMode string

const (
	ApprovalHuman     ApprovalMode = "human"
	ApprovalAutomatic ApprovalMode = "automatic"
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func ValidateIdentifier(kind, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q must match %s", kind, value, identifierPattern.String())
	}
	return nil
}

func (b Backend) Valid() bool {
	switch b {
	case BackendClaudeCode, BackendCodex:
		return true
	default:
		return false
	}
}

// SupportsRole reports whether a backend serves a role. No backend serves a name
// that is not a role at all, so the answer for one is false whichever backend
// asks: the Claude Code backend already refuses an unrecognized role when it
// assembles an invocation, and answering true here would describe a combination
// that cannot run.
func (b Backend) SupportsRole(role AgentRole) bool {
	if !role.Valid() {
		return false
	}
	switch b {
	case BackendClaudeCode:
		return true
	case BackendCodex:
		return role == RoleDeveloper || role == RoleReviewer
	default:
		return false
	}
}

func (m ApprovalMode) Valid() bool {
	return m == ApprovalHuman || m == ApprovalAutomatic
}
