package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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

// `yoyo status` prints a line per program manager instance under the four
// lines, with its lane and its status word, and `--json` carries the instance
// under standing.program_managers from the same derivation: here an instance
// whose lane report cites a restart request of its own that nothing has
// answered, which is blocked.
func TestStatusPrintsEachProgramManagerWithItsStatus(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, twoArchitectsConfig+`  reliability-pm:
    role: program-manager
    backend: claude-code
    model: opus
    lane: reliability
    triggers:
      every: 2h
`)
	root, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		t.Fatal(err)
	}
	requests, err := runstate.NewRestartRequestStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requests.Request(runstate.RestartRequest{
		SchemaVersion: runstate.RestartRequestSchemaVersion, ProductID: "yoyodyne", ID: "restart-0123456789abcdef",
		Agent: "reliability-pm", Part: config.ServiceScheduler, Reason: "died twice", RequestedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	reports, err := runstate.NewLaneReportStore(root, "yoyodyne", readmodel.CheckLaneReportMover)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reports.Write(context.Background(), runstate.LaneReport{
		ProductID: "yoyodyne", Agent: "reliability-pm",
		Report: runstate.LaneReportContent{Summary: "moving", Remaining: []string{}, Blockers: []runstate.LaneReportBlocker{
			{What: "the scheduler keeps dying", WaitingOn: "harness", Cites: "restart-0123456789abcdef"},
		}},
		Stamp: runstate.LaneReportStamp{ConversationID: "chat-00000000000000000000000000000001", Turn: 1},
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "status", "--config", configPath)
	if code != 0 {
		t.Fatalf("status code = %d, stderr = %q", code, stderr)
	}
	want := "Program managers (1):\n  reliability-pm — lane reliability — blocked: blocked on 1 open ask (restart-0123456789abcdef)\n"
	if !strings.Contains(stdout, want) {
		t.Fatalf("status = %q, want it to carry %q", stdout, want)
	}
	if strings.Index(stdout, "Program managers") < strings.Index(stdout, "Needs a human") {
		t.Errorf("status = %q; the instances are printed under the four lines", stdout)
	}

	encoded, stderr, code := runCLI(t, "status", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("status --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Standing struct {
			ProgramManagers []readmodel.ProgramManager `json:"program_managers"`
		} `json:"standing"`
	}
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode status JSON: %v", err)
	}
	instances := decoded.Standing.ProgramManagers
	if len(instances) != 1 || instances[0].Agent != "reliability-pm" || instances[0].Lane != "reliability" ||
		instances[0].Status != readmodel.ProgramManagerBlocked || len(instances[0].Blockers) != 1 ||
		instances[0].Blockers[0].WaitingOn != readmodel.MoverHarness || instances[0].ReportWrittenAt == nil ||
		instances[0].ReportPath != reports.ReportPath("reliability-pm") || len(instances[0].RestartRequests) != 1 {
		t.Fatalf("standing.program_managers = %+v, want the blocked instance with its blocker, report, and request", instances)
	}
}
