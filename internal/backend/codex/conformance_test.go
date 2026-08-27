package codex

import (
	"context"
	"os"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The stream vocabulary this adapter reads was taken from the provider's
// documented protocol rather than from a recorded run, so the one thing the unit
// tests above cannot show is that a real CLI still speaks it. This is where that
// is checked, against whatever Codex is installed on the machine running it.
//
// It is opt-in for the reason the Claude Code conformance check is: it starts a
// real provider, spends real capacity, and depends on an account this repository
// does not own. What it is not is optional evidence — a change to the flags, the
// sandbox mapping, or the event names is a change nothing else here can catch.
func TestLocalConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CODEX_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CODEX_CONFORMANCE=1 to run against the installed Codex CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Codex unavailable or unauthenticated: %#v", availability)
	}

	// A reviewer, because it is the stricter of the two roles Codex serves: it
	// must reach the read-only sandbox, and an adapter that quietly ran it
	// writable would pass every other check here.
	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		PermissionMode:   "plan",
		AllowedTools:     []string{},
		Timeout:          5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}
	// The session is what a later invocation resumes, and the resolved model is
	// the only durable evidence of what actually served this one. A stream whose
	// vocabulary has moved on still produces a terminal from the process exit,
	// so these two are what actually show the events were read.
	if result.SessionID == "" || result.ResolvedModel == "" {
		t.Fatalf("Run() read no session or model from the stream: %#v", result)
	}
}
