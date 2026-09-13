package adapters

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Every provider this build says it can launch has an adapter here, and nothing
// else has one. The two halves are one claim: a descriptor that reports itself
// runnable and reaches no adapter is an endpoint every check upstream believes
// is fine and nothing can start.
func TestEveryRunnableBuiltInReachesAnAdapter(t *testing.T) {
	t.Parallel()

	for _, descriptor := range backend.BuiltInDescriptors() {
		adapter, built := For(descriptor, descriptor.ID, execution.OSProcessRunner{}, "")
		if built != descriptor.Runnable() {
			t.Errorf("%q reports runnable %t and builds an adapter %t", descriptor.ID, descriptor.Runnable(), built)
		}
		if built && adapter == nil {
			t.Errorf("%q built a nil adapter", descriptor.ID)
		}
	}
	if _, built := For(backend.Descriptor{ID: "my-harness"}, "my-harness", execution.OSProcessRunner{}, ""); built {
		t.Error("a descriptor naming no adapter built one anyway")
	}
}

// Which adapter runs a provider is asked in more than one place, so it is
// answered in one: the descriptor says which compiled adapter, and this says
// what that adapter is. A provider a project declared runs on a built-in's
// adapter and is still recorded under its own name.
func TestTheAdapterIsTheOneTheDescriptorNames(t *testing.T) {
	t.Parallel()

	claude, _ := For(backend.Descriptor{Adapter: domain.BackendClaudeCode}, domain.BackendClaudeCode, execution.OSProcessRunner{}, "")
	if _, isClaude := claude.(claudecode.Backend); !isClaude {
		t.Fatalf("the claude-code adapter = %T", claude)
	}
	built, _ := For(backend.Descriptor{Adapter: domain.BackendCodex}, domain.BackendCodex, execution.OSProcessRunner{}, "")
	if _, isCodex := built.(codex.Backend); !isCodex {
		t.Fatalf("the codex adapter = %T", built)
	}

	// The declaration's executable and dialect are the whole of what it changes
	// about an invocation, and the backend it is recorded under is the one the
	// agent named rather than the adapter that started it.
	declared, _ := For(backend.Descriptor{
		Adapter: domain.BackendCodex,
		Binary:  "my-harness",
		Dialect: codex.Dialect{},
	}, "my-harness", execution.OSProcessRunner{}, "/homes/two")
	adapter, isCodex := declared.(codex.Backend)
	if !isCodex {
		t.Fatalf("a provider declared on the codex adapter = %T", declared)
	}
	if adapter.Provider != "my-harness" || adapter.Binary != "my-harness" || adapter.Dialect == nil {
		t.Fatalf("the declared provider's adapter = %#v", adapter)
	}
	// The account is the caller's rather than the description's: a diagnosis asks
	// one pooled account whether it is authenticated, and a conversation runs
	// under the account its agent is assigned to.
	if adapter.ConfigDir != "/homes/two" {
		t.Fatalf("the adapter was pointed at %q, want the account's own provider home", adapter.ConfigDir)
	}
}
