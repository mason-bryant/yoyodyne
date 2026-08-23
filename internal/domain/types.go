package domain

import (
	"fmt"
	"regexp"
	"strings"
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

// WorkItemClass names a kind of work a project may treat differently at
// admission. It exists because "ask about every work item" turned out to be
// coarser than the operators who set it actually meant: work that only reads and
// reports is not the work a per-item gate was put up against, and a policy that
// cannot say so is one an operator either abandons or works around.
//
// There is one class, deliberately. A class is an exemption from a gate the
// operator put up, so each of them has to be worth the operator's trust on its
// own, and a list that grew by convenience would be the gate coming down by
// instalments.
type WorkItemClass string

// WorkItemClassDiagnosis is work that only looks: it reads what is already
// there, is bounded in what it reads, and produces findings rather than a
// change. Nothing it admits alters the product, which is why an operator can
// hand it over without handing over what the gate was for.
const WorkItemClassDiagnosis WorkItemClass = "diagnosis"

// WorkItemClasses lists every class there is, in the order a refusal names them.
var WorkItemClasses = []WorkItemClass{WorkItemClassDiagnosis}

// Valid reports a class the harness recognizes. The empty class is not one: work
// that names no class is ordinary work, and asking whether that is valid is
// asking the wrong question.
func (c WorkItemClass) Valid() bool {
	for _, known := range WorkItemClasses {
		if c == known {
			return true
		}
	}
	return false
}

// WorkItemExecutor names what actually carries a work item's execution. It
// exists because nothing in the queue said, and the harness's own selection is
// what paid for that: an item whose execution is a conversation with the
// architect was chosen for a developer run, which spent the run and two review
// rounds producing a correctly refused empty diff. The subtler cost is the one
// that made a marker worth having — those rounds count against the item's cap,
// so an item mis-selected twice reaches its cap having done nothing and forces
// an escalation about work nobody ever started.
//
// The absent executor is a developer run. That is what almost all admitted work
// is, and it is what every item admitted before this existed is, so this says
// which work is not that rather than restating what the ordinary case already
// is. An item naming no executor is ordinary work.
type WorkItemExecutor string

// WorkItemExecutorConversation is work a persona conversation carries out: a
// promotion the architect makes to a document it owns, a decomposition the
// development manager settles, a decision the product manager records. No
// developer run can do any of it — the documents are default-deny for a
// developer's diff and the decision is not a change to a file at all — so
// selecting one for a run buys a refusal at best.
const WorkItemExecutorConversation WorkItemExecutor = "conversation"

// WorkItemExecutors lists every executor an item may name, in the order a
// refusal names them.
var WorkItemExecutors = []WorkItemExecutor{WorkItemExecutorConversation}

// Valid reports an executor the harness recognizes. The empty executor is not
// one: work that names no executor is a developer run, and asking whether that
// is valid is asking the wrong question.
func (e WorkItemExecutor) Valid() bool {
	for _, known := range WorkItemExecutors {
		if e == known {
			return true
		}
	}
	return false
}

// DeveloperRun reports work a developer run carries out, which is work that
// names no executor at all.
//
// Anything else answers false, including a marker the harness does not
// recognize. That is the safe direction and the only one worth defaulting: a
// marker nobody can read was still put there by somebody who meant the work was
// not a developer run, and reading it as ordinary work would spend exactly the
// run this marker exists to save. A marker that is refused where it is written
// is what keeps the unrecognized case rare.
func (e WorkItemExecutor) DeveloperRun() bool {
	return strings.TrimSpace(string(e)) == ""
}

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
