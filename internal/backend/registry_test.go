package backend

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// declaredPlugin is a provider a project could write for itself, valid in every
// respect a test is not deliberately spoiling.
func declaredPlugin() ProviderPlugin {
	return ProviderPlugin{
		Adapter:  domain.BackendClaudeCode,
		Binary:   "my-harness",
		Roles:    []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer},
		Postures: []Posture{PostureReadOnly, PostureWorktreeWrite},
		Dialect: DialectSpec{Rules: []DialectRule{
			{Answer: AnswerRefused, Terminal: truth(true), Failed: truth(true)},
		}},
	}
}

// A declaration has to name something that can launch it. A dialect with no
// invocation to observe is a plugin that validates and can never fire, which is
// the failure this refusal exists instead of.
func TestADeclarationNamesTheAdapterThatRunsIt(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{"my-harness": declaredPlugin()})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	descriptor, known := registry.Lookup("my-harness")
	if !known {
		t.Fatal("Lookup() found nothing")
	}
	if !descriptor.Runnable() || descriptor.Adapter != domain.BackendClaudeCode {
		t.Fatalf("Adapter = %q, want the adapter this build ships", descriptor.Adapter)
	}
	if descriptor.Binary != "my-harness" {
		t.Fatalf("Binary = %q, want the executable the declaration named", descriptor.Binary)
	}
	// The dialect is the thing the adapter is handed in place of its own, so a
	// declaration that produced none would be a provider read by the wrong
	// vocabulary.
	if descriptor.Dialect == nil {
		t.Fatal("the declared provider carries no dialect for its adapter to read with")
	}

	// A backend the vocabulary has and this build ships no adapter for names
	// nothing, which is what stops a run being started on it.
	codex, known := registry.Lookup(domain.BackendCodex)
	if !known || codex.Runnable() {
		t.Fatalf("codex descriptor = %#v, want a backend nothing in this build can launch", codex)
	}
	if runnable := RunnableAdapters(); len(runnable) != 1 || runnable[0] != domain.BackendClaudeCode {
		t.Fatalf("RunnableAdapters() = %v, want the one adapter this build ships", runnable)
	}
}

// The capability model has to stay honest as it grows: an unsupported role fails
// validation before work is assigned, for a provider nobody here has seen
// exactly as for one this build ships.
func TestAPluginIsRefusedForAnUnsupportedRoleAsABuiltInIs(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{"my-harness": declaredPlugin()})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	for _, test := range []struct {
		name    string
		backend domain.Backend
		role    domain.AgentRole
		want    bool
	}{
		{name: "the default backend serves every role", backend: domain.BackendClaudeCode, role: domain.RoleArchitect, want: true},
		{name: "codex serves the roles inside a run", backend: domain.BackendCodex, role: domain.RoleDeveloper, want: true},
		{name: "codex does not serve the management roles", backend: domain.BackendCodex, role: domain.RoleProductManager},
		{name: "a plugin serves what it declared", backend: "my-harness", role: domain.RoleReviewer, want: true},
		{name: "a plugin refuses what it did not", backend: "my-harness", role: domain.RoleArchitect},
		// The set of roles is fixed in the harness, so no backend serves a name
		// outside it — not even the one that serves every role there is.
		{name: "nothing serves a name that is not a role", backend: domain.BackendClaudeCode, role: "security-reviewer"},
		{name: "nothing serves no role at all", backend: domain.BackendClaudeCode, role: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			descriptor, known := registry.Lookup(test.backend)
			if !known {
				t.Fatalf("Lookup(%q) found nothing", test.backend)
			}
			if got := descriptor.SupportsRole(test.role); got != test.want {
				t.Fatalf("SupportsRole(%q) = %v, want %v", test.role, got, test.want)
			}
		})
	}

	if _, known := registry.Lookup("nobody-declared-this"); known {
		t.Fatal("Lookup() found a backend nothing ships and nothing declared")
	}
}

// A tool posture is the policy half of the same question, and it is asked of a
// plugin the same way. A provider that cannot refuse every tool cannot run a
// role that reasons over bounded evidence, whatever it says about the role.
func TestAPluginIsRefusedForAnUnsupportedPosture(t *testing.T) {
	t.Parallel()

	plugin := declaredPlugin()
	plugin.Postures = []Posture{PostureWorktreeWrite}
	registry, err := NewRegistry(map[domain.Backend]ProviderPlugin{"writes-only": plugin})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	descriptor, known := registry.Lookup("writes-only")
	if !known {
		t.Fatal("Lookup() found nothing")
	}
	if !descriptor.SupportsPosture(PostureFor(domain.RoleDeveloper)) {
		t.Fatal("the plugin refused the posture it declared")
	}
	if descriptor.SupportsPosture(PostureFor(domain.RoleReviewer)) {
		t.Fatal("the plugin held a posture it never declared, which is how a toolless role gets a shell")
	}
}

// A built-in's claim is held to the same standard, and Codex's read-only claim
// did not meet it: its read-only sandbox stops writes and network and still lets
// the agent read the machine, which is the one thing the posture exists to
// prevent. The descriptor claims what Codex can be held to, so a reviewer
// configured on it is refused rather than run under a sandbox that does not hold
// the posture — and the role stays served, so the refusal names the posture
// instead of sending the operator after a role Codex does serve.
func TestCodexClaimsOnlyThePostureItsSandboxHolds(t *testing.T) {
	t.Parallel()

	codex, known := BuiltInDescriptor(domain.BackendCodex)
	if !known {
		t.Fatal("BuiltInDescriptor() found no codex")
	}
	if codex.SupportsPosture(PostureReadOnly) {
		t.Error("codex claims a read-only posture its sandbox does not enforce")
	}
	if !codex.SupportsPosture(PostureFor(domain.RoleDeveloper)) {
		t.Error("codex refuses the developer posture its sandbox does hold")
	}
	if !codex.SupportsRole(domain.RoleReviewer) {
		t.Error("codex stopped serving the reviewer, so a reviewer on codex is refused for the wrong reason")
	}
}

// Every role the harness has needs a posture, because every posture the harness
// derives from a role is what a backend is checked against. A role with no
// posture would be checked against nothing.
func TestEveryRoleNeedsAPosture(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		if !PostureFor(role).Valid() {
			t.Errorf("PostureFor(%q) = %q, which is not a posture", role, PostureFor(role))
		}
	}
	if PostureFor(domain.RoleDeveloper) != PostureWorktreeWrite {
		t.Error("the developer is the role whose work is editing a worktree")
	}
	// A role nobody has decided a posture for has none, rather than inheriting
	// the developer's and silently getting a shell.
	if posture := PostureFor("security-reviewer"); posture != "" {
		t.Errorf("PostureFor() = %q for a name that is not a role", posture)
	}
}

// A plugin that loads and then does nothing on the day it matters is refused
// where it is declared, with everything wrong with it reported at once.
func TestAPluginThatCouldNeverWorkIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		id     domain.Backend
		spoil  func(*ProviderPlugin)
		wanted string
	}{
		{
			name:   "naming no adapter",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Adapter = "" },
			wanted: "names no adapter",
		},
		{
			// The vocabulary has Codex and this build ships nothing that can launch
			// it, so a declaration running on it is rules with no invocation.
			name:   "running on an adapter this build does not ship",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Adapter = domain.BackendCodex },
			wanted: "ships no adapter for",
		},
		{
			name:   "running on a provider it declared itself",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Adapter = "my-harness" },
			wanted: "ships no adapter for",
		},
		{
			name:   "serving no role",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Roles = nil },
			wanted: "serves no role",
		},
		{
			name:   "serving a name that is not a role",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Roles = []domain.AgentRole{"security-reviewer"} },
			wanted: "not one of the harness's roles",
		},
		{
			name:   "holding no posture",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Postures = nil },
			wanted: "holds no tool posture",
		},
		{
			name:   "holding a posture nothing means",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Postures = []Posture{"trusted"} },
			wanted: "holds posture \"trusted\"",
		},
		{
			name:   "reading nothing its provider says",
			id:     "my-harness",
			spoil:  func(p *ProviderPlugin) { p.Dialect = DialectSpec{} },
			wanted: "reads nothing a provider says",
		},
		{
			name:   "named something that is not an identifier",
			id:     "My Harness",
			spoil:  func(*ProviderPlugin) {},
			wanted: "must match",
		},
		{
			name:   "replacing a backend this build ships",
			id:     domain.BackendClaudeCode,
			spoil:  func(*ProviderPlugin) {},
			wanted: "a backend this build ships",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plugin := declaredPlugin()
			test.spoil(&plugin)
			_, err := NewRegistry(map[domain.Backend]ProviderPlugin{test.id: plugin})
			if err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("NewRegistry() error = %v, want it to contain %q", err, test.wanted)
			}
		})
	}
}

// A project that declares nothing gets the backends this build ships and no
// complaint, which is every project until one reaches a harness yoyo has never
// heard of.
func TestAProjectThatDeclaresNothingGetsTheBuiltIns(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	for _, id := range []domain.Backend{domain.BackendClaudeCode, domain.BackendCodex} {
		descriptor, known := registry.Lookup(id)
		if !known {
			t.Fatalf("Lookup(%q) found nothing", id)
		}
		if !descriptor.BuiltIn {
			t.Fatalf("%q is not marked as a backend this build ships", id)
		}
	}
	if got := len(registry.Backends()); got != 2 {
		t.Fatalf("Backends() = %v, want the two this build ships", registry.Backends())
	}
}

// The adapter reads its own description from the same place a configuration is
// validated against it, so the two cannot drift apart.
func TestTheDefaultBackendsDescriptionIsTheOneTheHarnessValidatesAgainst(t *testing.T) {
	t.Parallel()

	descriptor, known := BuiltInDescriptor(domain.BackendClaudeCode)
	if !known {
		t.Fatal("this build ships no description of its default backend")
	}
	if !descriptor.Capabilities.StructuredEvents || !descriptor.Capabilities.SessionResumption ||
		!descriptor.Capabilities.StructuredOutput || !descriptor.Capabilities.ToolControl ||
		!descriptor.Capabilities.LocalAuth {
		t.Fatalf("Capabilities = %#v, want everything the default backend does", descriptor.Capabilities)
	}
	if _, known := BuiltInDescriptor("my-harness"); known {
		t.Fatal("BuiltInDescriptor() described a backend this build does not ship")
	}
}
