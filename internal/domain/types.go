package domain

import (
	"fmt"
	"regexp"
)

type ProductID string

type RepositoryID string

type AgentRole string

const (
	RoleProductManager     AgentRole = "product-manager"
	RoleArchitect          AgentRole = "architect"
	RoleDevelopmentManager AgentRole = "development-manager"
	RoleDeveloper          AgentRole = "developer"
	RoleReviewer           AgentRole = "reviewer"
)

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

func (b Backend) SupportsRole(role AgentRole) bool {
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
