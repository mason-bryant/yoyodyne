package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// answeringBackend answers every turn with the same text.
type answeringBackend struct {
	text string
}

func (b answeringBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	return backendapi.RunResult{Backend: domain.BackendClaudeCode, SessionID: "session-1", FinalText: b.text}, nil
}

const laneReportSweep = "\n\n```yoyodyne-sweep\n" + `{"status":"complete","summary":"looked at the lane"}` + "\n```\n"

// firePassOfProgramManager fires one pass of a program manager's recurring task
// over a provider that answers with the reply given, and returns what the pass
// recorded and the lane report store.
func firePassOfProgramManager(t *testing.T, reply string) (runstate.Sweep, *runstate.LaneReportStore) {
	t.Helper()

	root := t.TempDir()
	conversations, err := runstate.NewConversationStore(root, "example")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	laneReports, err := runstate.NewLaneReportStore(root, "example", readmodel.CheckLaneReportMover)
	if err != nil {
		t.Fatalf("NewLaneReportStore() error = %v", err)
	}
	open := func(_ context.Context, role domain.AgentRole, _, _ string) (*chat.Session, *runstate.ConversationHold, error) {
		session, err := chat.Open(chat.Options{
			Role:         role,
			Agent:        "factory",
			Backend:      answeringBackend{text: reply},
			Store:        conversations,
			Model:        "opus",
			Provider:     domain.BackendClaudeCode,
			AccountAlias: config.DefaultAccountAlias,
			Repository:   filepath.Join(root, "repository"),
			ProductID:    "example",
			RepositoryID: "example",
			Briefing:     chat.Briefing{Text: "the product is a harness", GatheredAt: time.Now().UTC()},
			LaneReports:  laneReports,
		})
		return session, nil, err
	}
	sweeps, err := runstate.NewSweepStore(root, "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	trigger := orchestrator.Trigger{
		Tasks: map[string]config.RecurringTask{
			"factory-watch": {Role: domain.RoleProgramManager, Every: config.Duration(time.Hour), Enabled: true, Prompt: "watch the line"},
		},
		Claims:  sweeps,
		Reports: sweeps,
		Roles:   roleConversation{open: open},
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	recorded, _, err := sweeps.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %d sweeps, %v; want the one pass", len(recorded), err)
	}
	return recorded[0], laneReports
}

// A pass's lane report is stamped with the pass that wrote it as well as the
// conversation turn.
func TestAPassStampsTheLaneReportItWrites(t *testing.T) {
	t.Parallel()

	pass, laneReports := firePassOfProgramManager(t, "The line is moving.\n\n```yoyodyne-lane-report\n"+
		`{"summary":"the line is moving","remaining":[],"blockers":[]}`+"\n```"+laneReportSweep)
	if pass.Problem != "" {
		t.Errorf("the pass recorded a problem: %s", pass.Problem)
	}
	current, found, err := laneReports.Current("factory")
	if err != nil || !found {
		t.Fatalf("Current() = %v, %v", found, err)
	}
	if current.Stamp.Pass != "factory-watch#1" || current.Stamp.Turn != 1 || current.Stamp.ConversationID != pass.ConversationID {
		t.Errorf("the report is stamped %+v, want the first firing of factory-watch in the pass's conversation %s", current.Stamp, pass.ConversationID)
	}
}

// A lane report refused whole is on the pass record, and nothing is written.
func TestARefusedLaneReportIsOnThePassRecord(t *testing.T) {
	t.Parallel()

	pass, laneReports := firePassOfProgramManager(t, "The line is moving.\n\n```yoyodyne-lane-report\n"+
		`{"summary":"the line is moving","remaining":[]}`+"\n```"+laneReportSweep)
	if !strings.Contains(pass.Problem, "lane report") || !strings.Contains(pass.Problem, "refused whole") {
		t.Errorf("the pass record does not carry the refusal: %q", pass.Problem)
	}
	if pass.Result == nil {
		t.Error("the refusal cost the pass its account")
	}
	if _, found, err := laneReports.Current("factory"); found || err != nil {
		t.Errorf("Current() = %v, %v; a refused report was written", found, err)
	}
}
