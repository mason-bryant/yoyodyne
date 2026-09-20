package execution

import (
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A process the harness launches for a role says so in its environment.
//
// Some verbs record a person's decision and nothing else's: `yoyo pause`,
// `yoyo resume`, `yoyo release`, `yoyo artifact approve`. Inside the harness
// that is enforced in Go -- no pipeline path writes those records -- but a
// verb is a binary, and anything that can execute the binary can run the verb.
// A developer with a shell is exactly that. So the guarantee that only a person
// makes those decisions held against the harness's own code and, against an
// agent's shell, held only as a sentence in the documentation.
//
// This is the other half. Every process the harness launches for a role carries
// the role it was launched for in one variable, on top of the explicit
// environment every invocation is built from, and the verbs that record a
// person's act refuse when they find it -- with a sentence saying a person
// makes that decision, rather than a permission error nobody can read.
//
// What it identifies is a process an agent started, which is what a mistake the
// system could make on its own looks like: a developer told to unblock the
// queue and reaching for `yoyo release`. It is not a boundary against an agent
// that means to defeat it -- a shell can strip its own environment -- and the
// documentation says so rather than claiming more. What stands behind it is the
// protected-path gate, which refuses a run's change carrying the document an
// approval is written into whatever wrote it, and is tested end to end. The
// holds live under the state root, and nothing the harness tests stands between
// a stripped environment and a write there: a developer run enables the
// provider's own sandbox over its shell, whose write policy is the provider's
// rather than anything declared or verified here.

// AgentRoleVariable names the role a process was launched for. It is under the
// harness's own prefix, so the explicit environment carries it through to
// everything the process goes on to start: a shell the agent opens, and every
// `yoyo` that shell runs, reads as the agent's.
const AgentRoleVariable = "YOYODYNE_AGENT_ROLE"

// WithAgentRole returns environment with the process marked as launched for
// role. A marker already there is replaced rather than kept beside the new one:
// a harness launched by a harness inherits the outer agent's marker under the
// `YOYODYNE_` family, and the process being launched is the inner role's.
func WithAgentRole(environment []string, role domain.AgentRole) []string {
	if environment == nil {
		environment = os.Environ()
	}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, AgentRoleVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, AgentRoleVariable+"="+string(role))
}

// LaunchedForRole reports whether environment marks its process as one the
// harness launched for a role, and which. A nil environment asks about this
// process's own, which is what a verb deciding whether to refuse needs. An
// empty marker is no marker: unsetting the variable and setting it to nothing
// both say the process is nobody's agent.
func LaunchedForRole(environment []string) (domain.AgentRole, bool) {
	if environment == nil {
		environment = os.Environ()
	}
	for _, entry := range environment {
		name, value, named := strings.Cut(entry, "=")
		if !named || name != AgentRoleVariable {
			continue
		}
		role := domain.AgentRole(strings.TrimSpace(value))
		if role == "" {
			return "", false
		}
		return role, true
	}
	return "", false
}
