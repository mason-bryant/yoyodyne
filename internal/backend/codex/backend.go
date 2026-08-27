// Package codex runs agents through the locally installed Codex CLI.
//
// It is deliberately thin. Codex serves the two roles inside a run — the
// developer and the reviewer — and is not required to match every Claude Code
// feature; a role or a policy this adapter cannot hold is refused where the
// configuration is validated, before any work is assigned. Credentials stay
// where the provider keeps them: the harness reports whether the CLI is
// installed and logged in and manages no account of its own.
package codex

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// defaultTimeout is the total budget for one provider invocation and
// defaultIdleTimeout the gap it may go without saying anything. They answer
// different questions — is this run worth continuing, and is it doing anything
// at all — so both are applied rather than either standing in for the other,
// and they are the same bounds the other adapter uses because they are
// properties of how the harness runs agents rather than of which agent it runs.
const (
	defaultTimeout     = 4 * time.Hour
	defaultIdleTimeout = 5 * time.Minute
)

// The Codex sandbox settings this adapter will ask for. `danger-full-access` is
// deliberately absent: it is the one setting that would let an agent write
// anywhere on the machine, and nothing in the harness has a reason to ask for
// it, so it is unreachable from here rather than merely unused.
const (
	sandboxReadOnly       = "read-only"
	sandboxWorkspaceWrite = "workspace-write"
)

// sandboxForPosture is what a role's tool posture means to this provider.
//
// The worktree-write posture maps cleanly: `workspace-write` is Codex confining
// its edits to the directory it was started in, which is the worktree.
//
// The read-only posture does not map cleanly, and it is worth being exact about
// the difference rather than letting the mapping imply one that does not exist.
// The harness's read-only posture is an agent that reasons over the evidence it
// was handed and reaches outside it for nothing; Claude Code holds it by being
// given no tools at all. Codex has no per-tool control, so the strongest thing
// this adapter can ask for is the `read-only` sandbox, which stops every write
// and every network call the agent could make but still lets it read the
// machine. What that leaves is an advisory role that could read a local file
// nobody put in its evidence and quote it back to the provider. The design says
// Codex is not required to match every Claude Code feature and that roles are
// configured knowing the backends differ; this is one of the differences, and
// the operator choosing Codex for a reviewer is choosing it.
func sandboxForPosture(posture backend.Posture) string {
	switch posture {
	case backend.PostureReadOnly:
		return sandboxReadOnly
	case backend.PostureWorktreeWrite:
		return sandboxWorkspaceWrite
	default:
		return ""
	}
}

// requestedSandbox reads the permission mode the harness asked for. The field is
// the harness's request for how permissive an invocation is, stated in the
// vocabulary the harness already speaks, and each adapter reads it in its own
// terms: this one turns it into a Codex sandbox. A mode nobody mapped is refused
// rather than defaulted, because the only default available would be the
// developer's and silently widening a role meant to have none is the failure
// this guard exists to prevent.
//
// `manual` is deliberately absent as well as `danger-full-access`: `codex exec`
// is non-interactive and there is nobody to answer an approval prompt, so a run
// asking for one would hang until its idle bound killed it.
func requestedSandbox(permissionMode string) (string, bool) {
	switch permissionMode {
	case "plan", sandboxReadOnly:
		return sandboxReadOnly, true
	case "acceptEdits", "auto", "dontAsk", sandboxWorkspaceWrite:
		return sandboxWorkspaceWrite, true
	default:
		return "", false
	}
}

// sandboxFor decides what this invocation runs under: the sandbox the role's
// posture requires, and a refusal for anything that would not be it.
//
// The posture decides, and the requested mode may only agree with it. That is
// the whole of the policy check: a role nobody has decided a posture for has no
// sandbox here, a mode this adapter does not know is refused, and a mode that
// names a different sandbox from the one the role requires is refused rather
// than honored — which is the case that would otherwise hand a reviewer a
// writable worktree or leave a developer unable to edit one.
func sandboxFor(role domain.AgentRole, permissionMode string) (string, error) {
	required := sandboxForPosture(backend.PostureFor(role))
	if required == "" {
		return "", fmt.Errorf("Codex backend does not support role %q", role)
	}
	mode := strings.TrimSpace(permissionMode)
	if mode == "" {
		return required, nil
	}
	asked, known := requestedSandbox(mode)
	if !known {
		return "", fmt.Errorf("unsupported Codex permission mode %q; the sandbox is %s or %s", mode, sandboxReadOnly, sandboxWorkspaceWrite)
	}
	if asked != required {
		return "", fmt.Errorf("%s runs require the %q sandbox, not %q", role, required, asked)
	}
	return required, nil
}

type Backend struct {
	Runner execution.ProcessRunner
	Binary string
	Clock  execution.Clock
	// Provider is the backend identifier this invocation is recorded under, and
	// is empty for Codex itself. A project that declared a provider running on
	// this adapter is a different backend from the one that ships here, and what
	// a run, a conversation, and a line of spend record has to be the backend the
	// agent named rather than the adapter that happened to launch it.
	Provider domain.Backend
	// Dialect is how what the provider says is read. Empty is this provider's
	// own, which is every invocation until a project declares one. It decides
	// nothing: whether to wait, how long, and against which budget stay above
	// this adapter.
	Dialect backend.Dialect
}

func (b Backend) dialect() backend.Dialect {
	if b.Dialect == nil {
		return Dialect{}
	}
	return b.Dialect
}

func (b Backend) provider() domain.Backend {
	if b.Provider == "" {
		return domain.BackendCodex
	}
	return b.Provider
}

func (b Backend) binary() string {
	if b.Binary == "" {
		return "codex"
	}
	return b.Binary
}

// CheckAvailability asks the installed CLI whether it is there and whether it is
// logged in. Both answers are the provider's own: Codex holds the credentials,
// whether they came from a ChatGPT subscription or an API key, and the harness
// reports the state without managing any of it.
//
// The login state is read from the command's exit status rather than from its
// prose, because the status is the one part of the answer that does not move
// between versions. The prose is read only to say which credential is in use,
// and a sentence this adapter does not recognize leaves that unnamed rather than
// leaving the account misreported.
func (b Backend) CheckAvailability(ctx context.Context) (backend.Availability, error) {
	if b.Runner == nil {
		return backend.Availability{}, errors.New("Codex process runner is required")
	}
	binary := b.binary()
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"--version"}, Timeout: 10 * time.Second}, nil)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return backend.Availability{Installed: false}, nil
		}
		return backend.Availability{}, fmt.Errorf("check Codex version: %w", err)
	}
	if versionResult.Status != execution.ProcessSucceeded {
		return backend.Availability{Installed: false}, nil
	}
	availability := backend.Availability{
		Installed:   true,
		Version:     strings.TrimSpace(versionResult.Stdout),
		APIProvider: "openai",
	}

	loginResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"login", "status"}, Timeout: 10 * time.Second}, nil)
	if err != nil {
		return availability, fmt.Errorf("check Codex authentication: %w", err)
	}
	availability.Authenticated = loginResult.Status == execution.ProcessSucceeded
	availability.AuthMethod = authMethod(loginResult.Stdout)
	return availability, nil
}

// authMethod names the credential the CLI said it is using. It reads the two
// the provider documents and says nothing about a sentence it does not
// recognize, because a guessed credential in an availability report is worse
// than an unnamed one.
func authMethod(status string) string {
	lowered := strings.ToLower(status)
	switch {
	case strings.Contains(lowered, "api key"):
		return "api-key"
	case strings.Contains(lowered, "chatgpt"):
		return "chatgpt"
	default:
		return ""
	}
}

// Capabilities is what this adapter can do, read from the one description of
// this backend the harness holds rather than restated here. A configuration is
// validated against that description, so an adapter that answered differently
// would be one whose capability check meant nothing.
func (Backend) Capabilities() backend.Capabilities {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendCodex)
	return descriptor.Capabilities
}

func (b Backend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if b.Runner == nil {
		return backend.RunResult{}, errors.New("Codex process runner is required")
	}
	if strings.TrimSpace(request.RunID) == "" {
		return backend.RunResult{}, errors.New("run id is required")
	}
	if strings.TrimSpace(request.WorkingDirectory) == "" {
		return backend.RunResult{}, errors.New("working directory is required")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return backend.RunResult{}, errors.New("prompt is required")
	}
	sandbox, err := sandboxFor(request.Role, request.PermissionMode)
	if err != nil {
		return backend.RunResult{}, err
	}
	// Codex has no per-tool control: what an agent may do is decided by the
	// sandbox and by nothing else. A request naming tools is therefore refused
	// rather than run with the list quietly dropped, because a caller that asked
	// for a narrower set than the sandbox gives would otherwise get a wider one
	// and be told nothing.
	if len(request.AllowedTools) > 0 {
		return backend.RunResult{}, errors.New("Codex runs cannot be granted a tool list; the sandbox is what scopes what an agent may do")
	}

	args := []string{"exec"}
	// Resuming continues the provider's own session, which is an acceleration
	// and never the record: what the harness knows about this work is in its own
	// durable state, and a session the provider has forgotten costs context
	// rather than work.
	if request.SessionID != "" {
		args = append(args, "resume", request.SessionID)
	}
	args = append(args,
		"--json",
		// The worktree is a Git checkout, so this changes nothing for a real run;
		// it is what lets the harness invoke the provider somewhere that is not
		// one, which the conformance check does.
		"--skip-git-repo-check",
		"--sandbox", sandbox,
	)
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	// The prompt is read from standard input rather than put on the command line.
	// It carries the role's contract and the evidence the role was given, both of
	// which are long and neither of which belongs in a process listing.
	args = append(args, "-")

	timeout := request.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	idleTimeout := request.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = defaultIdleTimeout
	}
	clock := b.Clock
	if clock == nil {
		clock = execution.RealClock{}
	}

	redactor := execution.NewRedactor(request.RedactValues...)
	parser := newStreamParser(request.RunID, request.Role, request.LastSequence, clock, redactor, request.EventSink, request.ReplySink, b.dialect())
	var parseErrors []error
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name:  b.binary(),
		Args:  args,
		Dir:   request.WorkingDirectory,
		Stdin: strings.NewReader(composePrompt(request)),
		// The stream this invocation is asked for is the liveness signal: every
		// line the process writes is an event, so the gap between lines is
		// exactly the gap between events.
		Timeout:     timeout,
		IdleTimeout: idleTimeout,
		Redactor:    redactor,
	}, func(output execution.Output) {
		if output.Stream == execution.StreamStdout {
			if parseErr := parser.ParseLine(output.Text); parseErr != nil {
				parseErrors = append(parseErrors, parseErr)
			}
			return
		}
		if sinkErr := parser.EmitProcessOutput(output); sinkErr != nil {
			parseErrors = append(parseErrors, sinkErr)
		}
	})
	if err != nil {
		return backend.RunResult{}, fmt.Errorf("run Codex: %w", err)
	}
	if len(parseErrors) > 0 {
		return backend.RunResult{}, fmt.Errorf("parse Codex stream: %w", errors.Join(parseErrors...))
	}
	// The normalized result and events are the durable provider output. Do not
	// return the raw JSON stream as a second, potentially escape-obfuscated copy.
	processResult.Stdout = ""
	result := parser.Result()
	result.Backend = b.provider()
	result.Process = processResult
	if processResult.Status == execution.ProcessCancelled || processResult.Status == execution.ProcessTimedOut || processResult.Status == execution.ProcessStalled {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = string(processResult.Status)
		}
	}
	if !parser.SawTerminal() && processResult.Status == execution.ProcessSucceeded {
		return result, errors.New("Codex stream ended without a terminal event")
	}
	if processResult.Status == execution.ProcessFailed {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = fmt.Sprintf("process_exit_%d", processResult.ExitCode)
		}
	}
	return result, nil
}

// composePrompt is what the provider is actually sent. Codex has no separate
// channel for a system prompt, so the role's contract is prepended to the
// prompt rather than dropped: a role invoked without the contract it was given
// is a role doing some other job. It is worth being plain that this is weaker
// than the other adapter, where the contract enters the provider's system
// context and the evidence cannot reach it — here the two arrive as one message,
// so evidence that tried to talk its way past the contract is arguing with text
// in the same message rather than with something above it.
func composePrompt(request backend.RunRequest) string {
	if strings.TrimSpace(request.SystemPrompt) == "" {
		return request.Prompt
	}
	return request.SystemPrompt + "\n\n" + request.Prompt
}
