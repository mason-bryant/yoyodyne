package cli

// An installation that authenticated a provider by exporting a key in a shell
// profile had a working harness until yoyodyne-ifd.408 built every invocation
// from an allowlist. Nothing between that change and the next run's refusal said
// so. These are the two surfaces that say it before the refusal: the command an
// operator runs to be told whether their configuration is right, and the start
// of the one process they leave running.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// withoutProviderKeys clears every provider key for the length of one test, so
// what a surface says is decided by the test rather than by whatever the
// machine running it exports. A test using it cannot be parallel, which is the
// price of asserting on a process-wide environment.
func withoutProviderKeys(t *testing.T) {
	t.Helper()
	for _, name := range execution.ProviderKeyNames {
		t.Setenv(name, "")
	}
}

func TestConfigValidateSaysAProviderKeyHereAuthenticatesNothing(t *testing.T) {
	withoutProviderKeys(t)

	project := gitProject(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	path := filepath.Join(project, config.DirectoryName, config.FileName)

	// Nothing exported: the command is silent about it, because a line on every
	// healthy machine is one nobody is still reading when it matters.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "ANTHROPIC_API_KEY") {
		t.Errorf("stderr = %q, want nothing said where no provider key is exported", stderr.String())
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-exported-in-a-shell-profile")
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test")
	// It is a warning about an installation whose configuration is valid, so the
	// answer and the exit code are what they would have been.
	if code != 0 {
		t.Fatalf("Run() code = %d, want the exit code unchanged by a warning; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration valid") {
		t.Errorf("stdout = %q, want the validity answer unchanged", stdout.String())
	}
	warning := stderr.String()
	for _, want := range []string{"ANTHROPIC_API_KEY", "reaches no invocation", "provider home", "yoyo doctor"} {
		if !strings.Contains(warning, want) {
			t.Errorf("stderr = %q, want it to name %q", warning, want)
		}
	}
	if strings.Contains(warning, "sk-exported-in-a-shell-profile") {
		t.Errorf("stderr = %q, want the variable named and never its value", warning)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Status       string   `json:"status"`
		ProviderKeys []string `json:"provider_keys"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Status != "valid" {
		t.Errorf("status = %q, want the configuration still reported valid", result.Status)
	}
	if len(result.ProviderKeys) != 1 || result.ProviderKeys[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("provider_keys = %v, want the one exported key named", result.ProviderKeys)
	}
	if strings.Contains(stdout.String(), "sk-exported-in-a-shell-profile") {
		t.Errorf("the machine-readable report carries the value of a provider key: %q", stdout.String())
	}
}

// The sink says it once, on the start of the process an operator leaves
// running, because the shell that starts a sink is usually the shell the
// harness was started from.
func TestTheSinkSaysOnceThatAProviderKeyHereAuthenticatesNothing(t *testing.T) {
	withoutProviderKeys(t)

	var quiet bytes.Buffer
	sayProviderKeysReachNoInvocation(&quiet)
	if quiet.Len() != 0 {
		t.Errorf("the sink said %q with no provider key exported", quiet.String())
	}

	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-exported-in-a-shell-profile")
	var said bytes.Buffer
	sayProviderKeysReachNoInvocation(&said)
	line := said.String()
	if strings.Count(line, "\n") != 1 {
		t.Errorf("the sink said %q, want one line", line)
	}
	for _, want := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "reaches no invocation", "provider home", "yoyo doctor"} {
		if !strings.Contains(line, want) {
			t.Errorf("the sink said %q, want it to name %q", line, want)
		}
	}
	if strings.Contains(line, "oauth-exported-in-a-shell-profile") {
		t.Errorf("the sink said %q, want the variable named and never its value", line)
	}
}
