package claudecode

import (
	"context"
	"os"
	"testing"
	"time"

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
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
		PermissionMode:   "dontAsk",
		AllowedTools:     []string{},
		Timeout:          2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.IsError || result.SessionID == "" || result.FinalText == "" {
		t.Fatalf("Run() result = %#v", result)
	}
}
