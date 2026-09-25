package cli

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

// programManagerConfig is a project that configures one program manager beside
// the shipped agents, on the one backend that can hold its read-only posture.
func programManagerConfig(role string) string {
	return portableConfig + `  pgm-flow:
    role: ` + role + `
    backend: claude-code
    model: opus
    instances: 1
`
}

// The program manager is a role a project may configure: `config validate`
// accepts an agent filling it, and `config show` reports for that agent exactly
// the capability set its design fixes, read off the registry.
func TestConfigAcceptsAProgramManagerAndShowsItsSetExactly(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, programManagerConfig("program-manager"))
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config validate code = %d, stderr = %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "show", "--config", path, "--effective", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config show code = %d, stderr = %q", code, stderr.String())
	}
	var shown struct {
		Effective config.Config `json:"effective"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	agent, configured := shown.Effective.Agents["pgm-flow"]
	if !configured {
		t.Fatalf("config show reports no pgm-flow agent: %v", shown.Effective.Agents)
	}
	// Written out rather than read from the registry, so a capability arriving in
	// the bundle or leaving it fails here as well as there.
	want := []capability.Capability{
		"work-item.read", "repository.read", "repository.list", "readmodel.read",
		"work-item.admit", "work-item.attribute", "work-item.update", "work-item.label",
		"work-item.reprioritize", "work-item.park", "work-item.unpark", "work-item.link",
		"work-item.unlink", "work-item.reparent",
		"agent-context.mutate", "lane-report.write",
		"report.file", "amendment.propose", "exchange.ask", "exchange.answer",
		"service.request-restart",
	}
	got := slices.Clone(agent.Capabilities)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("config show reports the program manager holding %v, want exactly %v", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "show", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config show (text) code = %d, stderr = %q", code, stderr.String())
	}
	for _, held := range want {
		if !strings.Contains(stdout.String(), "- "+string(held)) {
			t.Errorf("config show's text does not list %q:\n%s", held, stdout.String())
		}
	}
}

// Admitting the sixth role admits that name and no other: a near miss is still
// refused, naming what was written.
func TestConfigStillRefusesEveryOtherUnknownRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"program-managr", "pgm", "Program-Manager", "observer"} {
		path := writeProjectConfig(t, programManagerConfig(role))
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code == 0 {
			t.Errorf("config validate accepted role %q", role)
			continue
		}
		if !strings.Contains(stderr.String()+stdout.String(), `unknown role "`+role+`"`) {
			t.Errorf("config validate refused role %q without naming it: stdout %q stderr %q", role, stdout.String(), stderr.String())
		}
	}
}
