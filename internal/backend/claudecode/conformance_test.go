package claudecode

import (
	"context"
	"os"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestLocalConformance(t *testing.T) {
	if os.Getenv("YOYODYNE_CLAUDE_CONFORMANCE") != "1" {
		t.Skip("set YOYODYNE_CLAUDE_CONFORMANCE=1 to run against the installed Claude Code CLI")
	}
	provider := Backend{Runner: execution.OSProcessRunner{}}
	availability, err := provider.CheckAvailability(context.Background())
	if err != nil {
		t.Fatalf("CheckAvailability() error = %v", err)
	}
	if !availability.Installed || !availability.Authenticated {
		t.Skipf("Claude Code unavailable or unauthenticated: %#v", availability)
	}
	result, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.SessionID == "" || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}

	// A read-only role is invoked with flags the developer's is not, and the one
	// that keeps its prompt prefix stable is the newest of them. An installed CLI
	// that does not know a flag refuses the whole invocation rather than ignoring
	// it, so this is where a version too old to carry it surfaces -- as one
	// skipped conformance run rather than as every review of the day failing.
	advisory, err := provider.Run(context.Background(), backendapi.RunRequest{
		RunID:            testRunID,
		Role:             domain.RoleReviewer,
		WorkingDirectory: t.TempDir(),
		Prompt:           "Reply with exactly: ok",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() as a reviewer error = %v", err)
	}
	if advisory.IsError || advisory.FinalText == "" {
		t.Fatalf("Run() as a reviewer result = %#v", advisory)
	}
}
