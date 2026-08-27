package adapters

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Every backend this build says it can launch has something here that launches
// it. The registry is what a configuration is validated against, so a backend it
// calls runnable and this cannot build is a configuration that loads and then
// fails once work has been claimed.
func TestEveryRunnableBackendHasAnAdapter(t *testing.T) {
	t.Parallel()

	for _, descriptor := range backend.BuiltInDescriptors() {
		if !descriptor.Runnable() {
			continue
		}
		if _, built := For(descriptor, descriptor.ID, execution.OSProcessRunner{}); !built {
			t.Errorf("the registry calls %q runnable and nothing here launches it", descriptor.ID)
		}
	}
	if _, built := For(backend.Descriptor{}, "nobody-declared-this", execution.OSProcessRunner{}); built {
		t.Error("a provider naming no adapter was built anyway")
	}
}

// A provider is built as the adapter its description names, which is what lets a
// project declare a provider that runs on either of the ones this build ships.
func TestAProviderIsBuiltAsTheAdapterItNames(t *testing.T) {
	t.Parallel()

	claude, built := For(backend.Descriptor{Adapter: domain.BackendClaudeCode, Binary: "my-harness"}, "my-harness", execution.OSProcessRunner{})
	if !built {
		t.Fatal("the declared provider was not built")
	}
	// The backend an invocation is recorded under is the one the agent named
	// rather than the adapter that started it: a project's own provider is a
	// different backend from the built-in whose adapter runs it.
	if typed, ok := claude.(claudecode.Backend); !ok || typed.Provider != "my-harness" || typed.Binary != "my-harness" {
		t.Fatalf("built %#v, want the Claude Code adapter running the declared executable", claude)
	}

	onCodex, built := For(backend.Descriptor{Adapter: domain.BackendCodex}, domain.BackendCodex, execution.OSProcessRunner{})
	if !built {
		t.Fatal("Codex was not built")
	}
	if typed, ok := onCodex.(codex.Backend); !ok || typed.Provider != domain.BackendCodex {
		t.Fatalf("built %#v, want the Codex adapter", onCodex)
	}
}
