package protectedpath

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

const roleDefinition = RoleDefinitions + "/developer.yaml"

// A grant naming the directory, or anything in it, is refused with the reason,
// wherever in the item's text it is written. The configuration directory around
// it is not refused: that is how an item admits the rest of the configuration.
func TestAGrantNamingTheRoleDefinitionsIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	for _, grant := range []string{RoleDefinitions, RoleDefinitions + "/", roleDefinition, "`.yoyodyne/Roles/reviewer.yaml`."} {
		problems := GrantProblems("Change the developer's role", "The role needs a new capability.\n\n"+GrantMarker+" "+grant+"\n")
		if len(problems) != 1 {
			t.Fatalf("GrantProblems() granting %q = %v, want it refused", grant, problems)
		}
		for _, want := range []string{RoleDefinitions, "no grant reaches", "operator", GrantMarker} {
			if !strings.Contains(problems[0].Error(), want) {
				t.Fatalf("refusal %q never says %q", problems[0], want)
			}
		}
	}
	// Prose about the directory grants nothing, which is what lets this item exist.
	if problems := GrantProblems("Refuse grants of "+RoleDefinitions, "No run may write "+roleDefinition+"."); len(problems) != 0 {
		t.Fatalf("GrantProblems() on prose = %v, want nothing", problems)
	}
	for _, grant := range []string{config.DirectoryName, config.DirectoryName + "/config.yaml", config.DirectoryName + "/roles-archive"} {
		if problems := GrantProblems("", GrantMarker+" "+grant+"\n"); len(problems) != 0 {
			t.Fatalf("GrantProblems() granting %q = %v, want it admitted", grant, problems)
		}
	}
}

// The diff gate refuses a role definition whatever the item grants: nothing, the
// directory itself, or the configuration directory around it.
func TestAChangeTouchingARoleDefinitionIsRefusedWhateverTheItemGrants(t *testing.T) {
	t.Parallel()

	set := Protect(config.Config{})
	changed := []string{roleDefinition, ".yoyodyne/ROLES/reviewer.yaml", ".yoyodyne/config.yaml", "feature.go"}
	for _, granted := range [][]string{nil, {RoleDefinitions}, {config.DirectoryName}, {roleDefinition}} {
		refused := set.Refused(changed, granted)
		if !contains(refused, roleDefinition) || !contains(refused, ".yoyodyne/ROLES/reviewer.yaml") {
			t.Fatalf("Refused() granting %v = %v, want every role definition refused", granted, refused)
		}
		if contains(refused, "feature.go") {
			t.Fatalf("Refused() granting %v = %v, refused an unprotected path", granted, refused)
		}
	}
	// The rest of the configuration directory keeps its grant.
	if refused := set.Refused(changed, []string{config.DirectoryName}); contains(refused, ".yoyodyne/config.yaml") {
		t.Fatalf("Refused() granting %s = %v, want the configuration file admitted", config.DirectoryName, refused)
	}
	if roles := RoleDefinitionsAmong(changed); len(roles) != 2 {
		t.Fatalf("RoleDefinitionsAmong() = %v, want the two role definitions", roles)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
