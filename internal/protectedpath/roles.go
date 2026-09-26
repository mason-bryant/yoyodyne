package protectedpath

// The directory no grant reaches because this harness refuses it.
//
// A role definition says what a role may do. A run that could write one could
// widen its own authority, or the authority of whichever role reads the file
// next, which is the one thing `configuration-never-grants-authority` exists to
// forbid. The rest of the configuration directory is default-deny with the item
// as the way out, because an item is written and reviewed before its run starts
// and a grant in it is somebody's decision. That is not enough here: a decision
// to let a run rewrite what a role is permitted is still a run rewriting it, and
// the design (configurable-workflows, "The authority model") makes the exception
// absolute rather than decided — no grant, no decided-change override. What
// changes a role definition is a person, and what makes one effective is the
// operator's activation of its digest, neither of which is a run.
//
// So it is refused twice, the way the provider's paths are, and once more where
// they are not. A grant naming the directory, or anything inside it, is refused
// at every door into the queue and by a run before it claims the item, through
// the same predicate the provider's paths are asked through. And a change
// touching the directory is refused by the diff gate whatever the item says,
// because a grant of `.yoyodyne` — which this does not refuse, since it is how an
// item admits the rest of the configuration — would otherwise reach inside it.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// RoleDefinitions is the repository-relative directory role definitions live
// in, in the slash form a grant and a changed path are compared in.
const RoleDefinitions = config.DirectoryName + "/roles"

// RoleInstruction is what an item's author, or a developer whose change touched
// the directory, is told to do about it. It says who does change a role
// definition, because a role told only "no" writes the grant again in other
// words and buys the same refusal twice.
const RoleInstruction = "No grant reaches " + RoleDefinitions + "/: a role definition decides what a role may do, so a run that could write one could widen its own authority. " +
	"A person changes a role definition by hand, and it takes effect only once the operator activates it. Take the grant out, or the path out of the change, say in the item or your summary what the definition should say and that the operator makes that change, and do the rest of the work as ordinary work. Nothing an item can say lifts this refusal."

// RoleDefinitionsAmong reports which of these paths are role definitions, which
// is what tells a refusal that it caught the one directory no grant would have
// admitted. An empty result is the ordinary answer.
func RoleDefinitionsAmong(paths []string) []string {
	var roles []string
	for _, candidate := range paths {
		clean, ok := normalize(candidate)
		if !ok || !isRoleDefinition(clean) {
			continue
		}
		roles = appendUnique(roles, clean)
	}
	sort.Strings(roles)
	return roles
}

// isRoleDefinition reports whether a normalized path is the role-definitions
// directory or inside it. It folds case, unlike every other comparison here,
// because this one admits no exception: on a case-insensitive filesystem —
// macOS by default — `.yoyodyne/Roles/x.md` is the same file the loader reads,
// and a refusal that the spelling of a directory could step around is not
// absolute.
func isRoleDefinition(clean string) bool {
	return within(strings.ToLower(clean), []string{RoleDefinitions})
}

// roleGrantProblems refuses each grant naming the role-definitions directory or
// a path inside it. A grant of an ancestor — `.yoyodyne` — is not refused here:
// it names the configuration directory rather than this one, it is how an item
// admits the rest of the configuration, and the diff gate refuses the role
// definitions inside it whatever it grants.
func roleGrantProblems(granted []string) []error {
	var problems []error
	for _, grant := range RoleDefinitionsAmong(granted) {
		problems = append(problems, fmt.Errorf("a %q line names %s, which is a path no grant reaches. %s", GrantMarker, grant, RoleInstruction))
	}
	return problems
}
