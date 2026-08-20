package cli

// The tests here read this repository's own operators mapping rather than a
// fixture, for the reason the artifact tests beside them do: a fixture is
// written against whatever the loader currently requires, and the file that
// actually governs is the one a tightened loader stops reading.
//
// This mapping has a second way to go wrong that a loader cannot catch. The
// Slack allow-list used to be authored under `slack` and is derived from the
// grants now, so a migration that merely deleted the old key would load
// perfectly and quietly authorize nobody — an authority decision lost rather
// than moved. That is what the derivation is asserted against below.

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

func TestThisRepositoryOwnOperatorsBindTheirNamespaces(t *testing.T) {
	t.Parallel()

	cfg := repositoryConfig(t)
	if len(cfg.Operators) == 0 {
		t.Fatal("this repository recognizes nobody; the operators mapping is missing or empty")
	}
	for _, name := range cfg.OperatorNames() {
		operator := cfg.Operators[name]
		// Every namespace an act can arrive through, so an entry that binds only
		// the one surface somebody happened to be thinking about is visible here
		// rather than at the boundary the other two arrive on.
		for namespace, bound := range map[config.Namespace]string{
			config.NamespaceGitEmail:     operator.GitEmail,
			config.NamespaceForgeAccount: operator.ForgeAccount,
			config.NamespaceSlackMember:  operator.SlackMemberID,
		} {
			if bound == "" {
				t.Errorf("operator %q binds no %s, so an act arriving through it resolves to nobody", name, namespace)
			}
		}
		if len(operator.Grants) == 0 {
			t.Errorf("operator %q holds no grant, so recognizing them authorizes nothing", name)
		}
	}
}

// The allow-list this project reports under is derived from the grants. A
// project that enables reporting and derives nobody has either lost the
// authority decision in migration or written grants that do not reach Slack;
// both read as an empty list, and neither fails anywhere else.
func TestThisRepositorySlackAllowListDerivesFromItsGrants(t *testing.T) {
	t.Parallel()

	cfg := repositoryConfig(t)
	if !cfg.Slack.Enabled {
		t.Skip("this repository does not report to a workspace, so it derives no allow-list")
	}
	allowed := cfg.SlackOperators()
	if len(allowed) == 0 {
		t.Fatal("slack reporting is enabled and the derived allow-list is empty; no human granted direct-work bound a member id")
	}
	// Every derived id has to resolve back to the human it was derived from,
	// which is the whole of the claim the mapping makes: the list is the grants
	// read through one namespace rather than a list beside them.
	for _, member := range allowed {
		if !cfg.OperatorHolds(config.NamespaceSlackMember, member, config.GrantDirectWork) {
			t.Errorf("derived member id %q does not resolve to a human granted %q", member, config.GrantDirectWork)
		}
	}
}

// repositoryConfig loads this repository's own configuration the way the harness
// loads it. A test that parsed the file itself would keep passing after the
// schema moved underneath it, which is the failure this file exists to catch.
func repositoryConfig(t *testing.T) config.Config {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	return resolved.Config
}
