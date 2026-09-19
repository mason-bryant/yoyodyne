package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// The verb refuses at the terminal, before it binds anything, when the state it
// would serve cannot be read: a dashboard over an unreadable state root is a
// page that looks like a dashboard and is not one.
func TestDashboardRefusesAnUnreadableConfiguration(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runCLI(t, "dashboard", "--config", "/nowhere/config.yaml")
	if code != 1 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout != "" || stderr == "" {
		t.Fatalf("stdout = %q, stderr = %q, want the refusal on stderr alone", stdout, stderr)
	}
	if _, _, code := runCLI(t, "dashboard", "extra"); code != 2 {
		t.Fatalf("positional argument: code = %d", code)
	}
}

// guardedBuffer is a buffer the command writes from its own goroutine while the
// test reads it, which a bare bytes.Buffer is not safe for.
type guardedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *guardedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *guardedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The verb starts, prints its URL and its token, and stops when asked. The
// token is printed once and is not in the URL. The bind is what a sandbox
// refuses, so this skips rather than fails where it is refused.
func TestDashboardPrintsItsURLAndTokenAndStopsWhenAsked(t *testing.T) {
	// Not parallel: the state root the command reads is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr guardedBuffer
	done := make(chan int, 1)
	go func() {
		done <- RunContext(ctx, []string{"dashboard", "--config", configPath}, &stdout, &stderr, "test")
	}()

	// Wait for the token to be printed or the command to give up.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdout.String(), "token: ") {
		select {
		case code := <-done:
			if strings.Contains(stderr.String(), "operation not permitted") {
				t.Skipf("this environment grants no listener: %s", strings.TrimSpace(stderr.String()))
			}
			t.Fatalf("dashboard exited early with %d: stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	printed := stdout.String()
	if !strings.Contains(printed, "serving at http://127.0.0.1:") || !strings.Contains(printed, "token: ") {
		t.Fatalf("stdout = %q, want the URL and the token", printed)
	}
	var token string
	for _, line := range strings.Split(printed, "\n") {
		if rest, found := strings.CutPrefix(line, "token: "); found {
			token = rest
		}
	}
	if len(token) != 64 || strings.Contains(printed[:strings.Index(printed, "token: ")], token) {
		t.Fatalf("token %q is not printed once on its own line, apart from the URL:\n%s", token, printed)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 || !strings.Contains(stdout.String(), "dashboard stopped") {
			t.Fatalf("stop: code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the dashboard did not stop on cancellation")
	}
}
