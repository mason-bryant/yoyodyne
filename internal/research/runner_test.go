package research

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestSearchPutsTheQuestionToTheSourceAndTimesTheAnswer(t *testing.T) {
	t.Parallel()

	process := &fakeProcess{stdout: "AGPL and Apache 2.0 are compatible one way only.\nhttps://example.test/licences"}
	runner := Runner{
		Process:   process,
		Clock:     fixedClock{at: time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)},
		Directory: "/repo",
		Policy:    Policy{Sources: []Source{{Name: "web", Command: "search-the-web"}}},
	}
	findings, err := runner.Search(context.Background(), []Query{
		{Source: "web", Question: "AGPL and Apache 2.0 compatibility", Why: "the recommendation turns on it"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(findings) != 1 || !findings[0].Answered() {
		t.Fatalf("findings = %#v", findings)
	}
	if findings[0].Source != "web" || !strings.Contains(findings[0].Evidence, "compatible one way only") {
		t.Fatalf("finding = %#v", findings[0])
	}
	// The retrieval time is the harness's own clock. "When was this true" is the
	// question a citation is worth nothing without, and it is not something the
	// source or the role gets to assert.
	if !findings[0].RetrievedAt.Equal(runner.Clock.Now()) {
		t.Fatalf("retrieved at %s, want %s", findings[0].RetrievedAt, runner.Clock.Now())
	}
	// The question goes in on a pipe, so nothing a role wrote is ever part of a
	// command line the shell parses.
	if len(process.commands) != 1 {
		t.Fatalf("commands = %#v", process.commands)
	}
	command := process.commands[0]
	if len(command.Args) != 2 || command.Args[1] != "search-the-web" {
		t.Fatalf("the source command was not run as written: %#v", command.Args)
	}
	for _, arg := range command.Args {
		if strings.Contains(arg, "AGPL") {
			t.Fatalf("the question reached the command line: %#v", command.Args)
		}
	}
	if process.stdin != "AGPL and Apache 2.0 compatibility\n" {
		t.Fatalf("the source was given %q on standard input", process.stdin)
	}
	if command.Dir != "/repo" {
		t.Fatalf("the source ran in %q", command.Dir)
	}
	if command.Timeout != DefaultTimeout {
		t.Fatalf("the source was given %s, want the configured %s", command.Timeout, DefaultTimeout)
	}
}

// A source is the one thing here that reaches outside the machine, so what must
// not leave it is removed from the question before it does.
func TestTheQuestionIsRedactedBeforeItLeavesTheMachine(t *testing.T) {
	t.Parallel()

	process := &fakeProcess{stdout: "nothing found"}
	runner := Runner{
		Process:      process,
		Policy:       Policy{Sources: []Source{{Name: "web", Command: "search"}}},
		RedactValues: []string{"hunter2"},
	}
	findings, err := runner.Search(context.Background(), []Query{
		{Source: "web", Question: "does hunter2 appear in any breach corpus", Why: "checking an assumption"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if strings.Contains(process.stdin, "hunter2") {
		t.Fatalf("the secret reached the source: %q", process.stdin)
	}
	// And it is not written back into the record of what was asked either, which
	// is durable and is read by whoever audits the evaluation later.
	if strings.Contains(findings[0].Question, "hunter2") {
		t.Fatalf("the secret was recorded on the finding: %q", findings[0].Question)
	}
}

// A source that will not answer is a finding rather than a failure. A role that
// gets silence for an answer concludes there was nothing to find, which is the
// one conclusion it must never draw from a source that broke.
func TestASourceThatWillNotAnswerIsSaidRatherThanSwallowed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		process *fakeProcess
		policy  Policy
		query   Query
		want    string
	}{
		{
			name:    "no such source",
			process: &fakeProcess{},
			policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}},
			query:   Query{Source: "intranet", Question: "q", Why: "w"},
			want:    "no source named \"intranet\" is configured",
		},
		{
			name:    "the command failed",
			process: &fakeProcess{status: execution.ProcessFailed, exitCode: 127, stderr: "search: not found"},
			policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}},
			query:   Query{Source: "web", Question: "q", Why: "w"},
			want:    "exit code 127: search: not found",
		},
		{
			name:    "the source ran out of time",
			process: &fakeProcess{status: execution.ProcessTimedOut},
			policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}, Timeout: 5 * time.Second},
			query:   Query{Source: "web", Question: "q", Why: "w"},
			want:    "did not answer within 5s",
		},
		{
			name:    "the source answered with nothing",
			process: &fakeProcess{stdout: "  \n"},
			policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}},
			query:   Query{Source: "web", Question: "q", Why: "w"},
			want:    "answered with nothing",
		},
		{
			name:    "the source could not be run at all",
			process: &fakeProcess{err: errors.New("fork/exec: permission denied")},
			policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}},
			query:   Query{Source: "web", Question: "q", Why: "w"},
			want:    "could not be run",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := Runner{Process: test.process, Policy: test.policy}
			findings, err := runner.Search(context.Background(), []Query{test.query})
			// The capability worked; the source did not. One source failing must
			// never cost the reply that carried the question.
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(findings) != 1 || findings[0].Answered() {
				t.Fatalf("findings = %#v", findings)
			}
			if !strings.Contains(findings[0].Problem, test.want) {
				t.Fatalf("problem = %q, want it to contain %q", findings[0].Problem, test.want)
			}
			// Even a finding that retrieved nothing says when the attempt was made,
			// so the record of an evaluation says the source was tried.
			if findings[0].RetrievedAt.IsZero() {
				t.Fatal("a failed retrieval recorded no moment")
			}
		})
	}
}

// A source failing is a finding; nothing to ask with at all is an error,
// because nothing was asked and nothing could be.
func TestNothingToAskWithIsAnErrorRatherThanAnEmptyAnswer(t *testing.T) {
	t.Parallel()

	permitted := Policy{Sources: []Source{{Name: "web", Command: "search"}}}
	query := []Query{{Source: "web", Question: "q", Why: "w"}}
	for _, test := range []struct {
		name   string
		runner Runner
		want   string
	}{
		{name: "no source permitted", runner: Runner{Process: &fakeProcess{}}, want: "configured no research sources"},
		{name: "nothing to run it with", runner: Runner{Policy: permitted}, want: "no process runner is configured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := test.runner.Search(context.Background(), query)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Search() error = %v, want it to contain %q", err, test.want)
			}
		})
	}

	// A round that asks more than the project permits asks none of it, rather
	// than the first few: the block is one request, and spending part of a budget
	// on part of a question is not what was asked for.
	narrow := Runner{Process: &fakeProcess{}, Policy: Policy{Sources: permitted.Sources, MaxQueriesPerTurn: 1}}
	_, err := narrow.Search(context.Background(), []Query{
		{Source: "web", Question: "one", Why: "w"},
		{Source: "web", Question: "two", Why: "w"},
	})
	if err == nil || !strings.Contains(err.Error(), "none were asked") {
		t.Fatalf("Search() error = %v, want it to refuse the whole round", err)
	}
}

// Evidence nobody bounded is a prompt whose size is decided by a stranger. A
// bounded answer read as a whole one is how a partial result becomes a confident
// conclusion, so the cut travels with it.
func TestOversizedEvidenceIsCutWithTheCutDeclared(t *testing.T) {
	t.Parallel()

	runner := Runner{
		Process: &fakeProcess{stdout: strings.Repeat("e", MaxEvidenceBytes*3)},
		Policy:  Policy{Sources: []Source{{Name: "web", Command: "search"}}},
	}
	findings, err := runner.Search(context.Background(), []Query{{Source: "web", Question: "q", Why: "w"}})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(findings[0].Evidence) > MaxEvidenceBytes {
		t.Fatalf("evidence is %d bytes, limit is %d", len(findings[0].Evidence), MaxEvidenceBytes)
	}
	if !findings[0].Truncated {
		t.Fatal("a cut answer does not say it was cut")
	}
	if !strings.Contains(Render(findings), "cut to") {
		t.Fatalf("the delivery does not declare the cut: %q", Render(findings))
	}
}

// fakeProcess stands in for the operator's configured source command and records
// exactly what it was asked to run, which is what makes "the question never
// reached a command line" an assertion rather than a claim.
type fakeProcess struct {
	commands []execution.Command
	stdin    string
	stdout   string
	stderr   string
	status   execution.ProcessStatus
	exitCode int
	err      error
}

func (f *fakeProcess) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	f.commands = append(f.commands, command)
	if command.Stdin != nil {
		given, err := io.ReadAll(command.Stdin)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		f.stdin = string(given)
	}
	if f.err != nil {
		return execution.ProcessResult{}, f.err
	}
	status := f.status
	if status == "" {
		status = execution.ProcessSucceeded
	}
	return execution.ProcessResult{
		Status:   status,
		ExitCode: f.exitCode,
		Stdout:   f.stdout,
		Stderr:   f.stderr,
	}, nil
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at.UTC() }
