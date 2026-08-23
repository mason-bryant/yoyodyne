package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Runner performs research on a role's behalf. It is the whole of the
// capability: the role never runs anything, and nothing here consults the role
// about what to run — the command is the operator's, the bounds are the
// operator's, and what the role supplied is one question on a pipe.
type Runner struct {
	// Process runs the configured source command. A runner without one performs
	// nothing and says so, rather than reporting that the sources had nothing.
	Process execution.ProcessRunner
	Clock   execution.Clock
	// Shell is what a source command is run by, matching how a configured check
	// is run: an operator writes a command line, not an argv.
	Shell string
	// Directory is where a source command runs. It is the repository, so a source
	// the operator wrote as a script in their own project works the way they
	// expect.
	Directory string
	Policy    Policy
	// RedactValues are the values that must not leave this process. A question is
	// redacted before it is written to the source, because the source is the one
	// thing here that reaches outside the machine.
	RedactValues []string
}

// defaultShell matches the checks runner's, so a source command and a check
// command are written against the same shell.
const defaultShell = "/bin/sh"

// Permitted reports what this runner may do, which is exactly what a role is
// told it may ask for. It comes from the runner rather than from a second copy
// of the policy beside it, so what a role is offered and what would actually be
// run can never disagree.
func (r Runner) Permitted() Policy { return r.Policy }

// Search puts each question to the source it names and returns what came back,
// one finding per query and in the order they were asked. It never returns fewer
// findings than it was given queries: a question that could not be asked at all
// comes back as a finding saying why, because a role that gets silence for an
// answer concludes there was nothing to find.
//
// The error return is for the capability failing rather than for a source
// failing. A source that exits non-zero, times out, or does not exist is a
// finding with a problem on it; a runner with no process to run anything with is
// an error, because nothing was asked and nothing could be.
func (r Runner) Search(ctx context.Context, queries []Query) ([]Finding, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	if !r.Policy.Enabled() {
		return nil, errors.New("this project has configured no research sources, so nothing was asked")
	}
	if r.Process == nil {
		return nil, errors.New("no process runner is configured for research, so nothing was asked")
	}
	if budget := r.Policy.QueryBudget(); len(queries) > budget {
		return nil, fmt.Errorf("%d research questions were asked in one reply and this project permits %d; none were asked", len(queries), budget)
	}
	redactor := execution.NewRedactor(r.RedactValues...)
	findings := make([]Finding, 0, len(queries))
	for _, query := range queries {
		findings = append(findings, r.ask(ctx, query, redactor))
	}
	return findings, nil
}

// ask performs one query. Everything it can go wrong with is recorded on the
// finding rather than raised, so one source failing never costs the answers
// beside it.
func (r Runner) ask(ctx context.Context, query Query, redactor execution.Redactor) Finding {
	question := redactor.Redact(strings.TrimSpace(query.Question))
	finding := Finding{
		Source:      strings.TrimSpace(query.Source),
		Question:    question,
		Why:         strings.TrimSpace(query.Why),
		RetrievedAt: r.now(),
	}
	source, permitted := r.Policy.Find(query.Source)
	if !permitted {
		finding.Problem = fmt.Sprintf("no source named %q is configured; this project permits %s",
			strings.TrimSpace(query.Source), strings.Join(r.Policy.Names(), ", "))
		return finding
	}
	timeout := r.Policy.SourceTimeout()
	result, err := r.Process.Run(ctx, execution.Command{
		Name: r.shell(),
		Args: []string{"-c", source.Command},
		Dir:  r.Directory,
		// The question goes in on a pipe rather than as an argument, so nothing a
		// role wrote is ever part of a command line the shell parses.
		Stdin:    strings.NewReader(question + "\n"),
		Timeout:  timeout,
		Redactor: redactor,
	}, nil)
	// The moment is taken after the source answered rather than before it was
	// asked: what a citation records is when the evidence was obtained.
	finding.RetrievedAt = r.now()
	if err != nil {
		finding.Problem = fmt.Sprintf("the %s source could not be run: %s", source.Name, singleLine(err.Error()))
		return finding
	}
	if result.Status != execution.ProcessSucceeded {
		finding.Problem = describeFailure(source.Name, timeout, result)
		return finding
	}
	evidence, truncated := bound(result.Stdout)
	if strings.TrimSpace(evidence) == "" {
		finding.Problem = fmt.Sprintf("the %s source answered with nothing", source.Name)
		return finding
	}
	finding.Evidence = evidence
	finding.Truncated = truncated
	return finding
}

// describeFailure says why a source did not answer, in terms of what it means
// for the evidence rather than in the process runner's vocabulary. A source that
// ran out of time and one that refused the question are different things to be
// told, and both are different from one that is not installed.
func describeFailure(name string, timeout time.Duration, result execution.ProcessResult) string {
	switch result.Status {
	case execution.ProcessTimedOut, execution.ProcessStalled:
		return fmt.Sprintf("the %s source did not answer within %s", name, timeout)
	case execution.ProcessCancelled:
		return fmt.Sprintf("the %s source was stopped before it answered", name)
	default:
		problem := fmt.Sprintf("the %s source failed with exit code %d", name, result.ExitCode)
		if detail := singleLine(result.Stderr); detail != "" {
			problem += ": " + detail
		}
		return problem
	}
}

// bound cuts the evidence to what a turn and a durable record may carry, on a
// rune boundary so a cut answer is still text. Whether it was cut travels with
// it, because a bounded answer read as a whole one is the way a partial search
// result becomes a confident conclusion.
func bound(evidence string) (string, bool) {
	trimmed := strings.TrimSpace(evidence)
	if len(trimmed) <= MaxEvidenceBytes {
		return trimmed, false
	}
	cut := MaxEvidenceBytes
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return strings.TrimSpace(trimmed[:cut]), true
}

func (r Runner) shell() string {
	if strings.TrimSpace(r.Shell) == "" {
		return defaultShell
	}
	return r.Shell
}

func (r Runner) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now()
	}
	return r.Clock.Now().UTC()
}
