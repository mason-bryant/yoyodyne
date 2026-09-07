// Package adapters turns a provider description into the compiled adapter that
// launches it.
//
// It is one function and it exists so there is one of it. Which adapter runs a
// backend is asked in more than one place — a run and a conversation build one
// to invoke, a diagnosis builds one to ask whether the provider is installed and
// logged in — and two switches over the backends this build happens to know is
// how a provider comes to be diagnosed as one thing and run as another. The
// registry in internal/backend says which adapter a provider names; this says
// what that adapter is.
package adapters

import (
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// For builds the adapter that launches one provider, and reports false for a
// provider nothing in this build can launch. The backend the invocation is
// recorded under is the one the agent named rather than the adapter that
// happened to start it: a project that declared a provider running on a
// built-in's adapter is a different backend from the built-in, and every run,
// conversation, and line of spend has to say which one it was.
//
// The executable and the dialect are the whole of what a declaration changes
// about an invocation. A built-in declares neither, so it runs exactly as it
// would have: its own binary, its own dialect.
//
// configDir is the provider home this adapter asks about and, where a request
// names none of its own, invokes under. It is separate from the descriptor
// because the account belongs to the caller rather than to the provider
// description: a diagnosis asks one pooled account whether it is authenticated,
// and a conversation runs under the account its agent is assigned to. Empty is
// the machine's own provider home, which is what a single-account installation
// has always used.
func For(descriptor backend.Descriptor, named domain.Backend, runner execution.ProcessRunner, configDir string) (backend.Backend, bool) {
	switch descriptor.Adapter {
	case domain.BackendClaudeCode:
		return claudecode.Backend{
			Runner:    runner,
			Binary:    descriptor.Binary,
			Provider:  named,
			Dialect:   descriptor.Dialect,
			ConfigDir: configDir,
		}, true
	case domain.BackendCodex:
		return codex.Backend{
			Runner:    runner,
			Binary:    descriptor.Binary,
			Provider:  named,
			Dialect:   descriptor.Dialect,
			ConfigDir: configDir,
		}, true
	default:
		return nil, false
	}
}
