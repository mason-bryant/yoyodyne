package chat

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func restartBlock(payload string) string {
	return restartFence + "\n" + payload + "\n```\n"
}

// programManagerSession opens a program manager's conversation over a restart
// request store, answering with the replies given in order.
func programManagerSession(t *testing.T, root string, replies ...string) (*Session, *fakeBackend, *runstate.RestartRequestStore) {
	t.Helper()

	requests, err := runstate.NewRestartRequestStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewRestartRequestStore() error = %v", err)
	}
	results := make([]backendapi.RunResult, 0, len(replies))
	for _, reply := range replies {
		results = append(results, backendapi.RunResult{SessionID: "session-1", FinalText: reply})
	}
	provider := &fakeBackend{results: results}
	options := testOptions(t, provider)
	options.Role = domain.RoleProgramManager
	options.Agent = "factory-pgm"
	options.Store = newTestStore(t, root)
	options.RestartRequests = requests
	return openTestSession(t, options), provider, requests
}

// A block naming a part the services section declares writes one durable
// request naming the instance, the part, and when, and the role is told in the
// result that it is recorded and that nothing acts on it until
// yoyodyne-ifd.413 lands.
func TestARestartRequestIsRecordedAndTheRoleIsToldNothingActsOnItYet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session, provider, requests := programManagerSession(t, root,
		"The scheduler has died four times this hour.\n\n"+restartBlock(`{"part":"scheduler","reason":"died four times in an hour with nothing restarting it"}`),
		"Noted.",
	)

	reply, err := session.Send(context.Background(), "How is the line?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Text, "yoyodyne-restart") {
		t.Errorf("the restart block reached the operator's prose:\n%s", reply.Text)
	}
	if !strings.Contains(provider.requests[0].SystemPrompt, "yoyodyne-restart") {
		t.Error("the program manager's contract does not say how to ask for a restart")
	}
	if reply.Restart == nil || !reply.Restart.Recorded {
		t.Fatalf("reply.Restart = %+v, want the request recorded", reply.Restart)
	}

	open, err := requests.Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open requests = %+v, want one", open)
	}
	recorded := open[0]
	if recorded.Agent != "factory-pgm" || recorded.Part != "scheduler" || recorded.RequestedAt.IsZero() {
		t.Errorf("recorded request = %+v, want the instance, the part, and when", recorded)
	}
	if recorded.ConversationID != session.state.ConversationID || recorded.Turn != 1 {
		t.Errorf("recorded request names conversation %q turn %d, want this conversation's first turn", recorded.ConversationID, recorded.Turn)
	}

	// The result reaches the role on its next turn, saying it is recorded and
	// awaits the supervisor's pass.
	if _, err := session.Send(context.Background(), "And now?"); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	next := provider.requests[1].Prompt
	for _, required := range []string{"# Restart request", "Recorded as " + recorded.ID, "Nothing acts on it yet", "yoyodyne-ifd.413", "has not landed"} {
		if !strings.Contains(next, required) {
			t.Errorf("the next turn is not told %q:\n%s", required, next)
		}
	}
}

// A part the services section does not declare is refused, naming the four it
// does, and nothing is recorded.
func TestARestartOfAnUndeclaredPartIsRefusedNamingTheFourParts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session, _, requests := programManagerSession(t, root,
		restartBlock(`{"part":"database","reason":"slow"}`),
	)
	reply, err := session.Send(context.Background(), "restart the database")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.Restart == nil || reply.Restart.Recorded {
		t.Fatalf("reply.Restart = %+v, want a refusal", reply.Restart)
	}
	for _, part := range []string{`"slack"`, `"dashboard"`, `"scheduler"`, `"maintenance"`, `"database"`} {
		if !strings.Contains(reply.Restart.Failure, part) {
			t.Errorf("the refusal %q does not name %s", reply.Restart.Failure, part)
		}
	}
	if open, err := requests.Open(); err != nil || len(open) != 0 {
		t.Errorf("open requests = %+v, %v; want none", open, err)
	}
}

// A second request for the same part while the first is open is refused naming
// the first, and the first is left as it was.
func TestASecondRestartOfTheSamePartIsRefusedNamingTheFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session, _, requests := programManagerSession(t, root,
		restartBlock(`{"part":"slack","reason":"the sink has said nothing for a day"}`),
		restartBlock(`{"part":"slack","reason":"still nothing"}`),
	)
	first, err := session.Send(context.Background(), "is slack alive?")
	if err != nil || first.Restart == nil || !first.Restart.Recorded {
		t.Fatalf("first Send() = %+v, %v; want the request recorded", first.Restart, err)
	}
	second, err := session.Send(context.Background(), "and now?")
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if second.Restart == nil || second.Restart.Recorded {
		t.Fatalf("second.Restart = %+v, want a refusal", second.Restart)
	}
	if !strings.Contains(second.Restart.Failure, first.Restart.Request.ID) {
		t.Errorf("the refusal %q does not name the open request %s", second.Restart.Failure, first.Restart.Request.ID)
	}
	open, err := requests.Open()
	if err != nil || len(open) != 1 || open[0].ID != first.Restart.Request.ID {
		t.Errorf("open requests = %+v, %v; want only the first", open, err)
	}
}

// Only a role holding service.request-restart may ask: every other role's
// restart block is refused before anything is recorded.
func TestOnlyTheProgramManagerMayAskForARestart(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		session := &Session{}
		session.state.Role = role
		err := session.authorize(parsedReply{Restart: &RestartAsk{Part: "scheduler", Reason: "because"}})
		var refusal *AuthorityError
		switch {
		case role == domain.RoleProgramManager && err != nil:
			t.Errorf("authorize() of the program manager's restart = %v, want it permitted", err)
		case role != domain.RoleProgramManager && !errors.As(err, &refusal):
			t.Errorf("authorize() of the %s's restart = %v, want an authority refusal", role, err)
		}
	}
}

// A restart block is read strictly: a field nothing asked for, or trailing
// content, is an unreadable block rather than a request.
func TestARestartBlockIsReadStrictly(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		``,
		`{"part":"scheduler","reason":"x","now":true}`,
		`{"part":"scheduler","reason":"x"} {}`,
	} {
		if _, err := decodeRestart(payload); err == nil {
			t.Errorf("decodeRestart(%q) accepted the block", payload)
		}
	}
}

// Nothing on the request path starts, stops, or signals a process. The request
// is a record: the files that carry it import nothing that can reach a process,
// and call nothing that would.
func TestTheRestartRequestPathTouchesNoProcess(t *testing.T) {
	t.Parallel()

	forbiddenImports := map[string]bool{
		"os/exec": true, "os/signal": true, "syscall": true, "golang.org/x/sys/unix": true,
		"github.com/mason-bryant/yoyodyne/internal/execution":    true,
		"github.com/mason-bryant/yoyodyne/internal/supervise":    true,
		"github.com/mason-bryant/yoyodyne/internal/shutdown":     true,
		"github.com/mason-bryant/yoyodyne/internal/redeploy":     true,
		"github.com/mason-bryant/yoyodyne/internal/watchdog":     true,
		"github.com/mason-bryant/yoyodyne/internal/backend":      true,
		"github.com/mason-bryant/yoyodyne/internal/checks":       true,
		"github.com/mason-bryant/yoyodyne/internal/forgehygiene": true,
	}
	forbiddenCalls := map[string]bool{
		"FindProcess": true, "StartProcess": true, "Kill": true, "Signal": true,
		"Command": true, "CommandContext": true, "Exec": true, "ForkExec": true,
	}
	for _, path := range []string{"restart.go", "../runstate/restartrequest.go", "../readmodel/programmanagers.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range file.Imports {
			name, _ := strconv.Unquote(imported.Path.Value)
			if forbiddenImports[name] {
				t.Errorf("%s imports %s, and the restart request path reaches no process", path, name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok && forbiddenCalls[selector.Sel.Name] {
				t.Errorf("%s calls %s, and the restart request path starts, stops, or signals nothing", path, selector.Sel.Name)
			}
			return true
		})
	}
}
