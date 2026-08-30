package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// defaultTimeout is the total budget for one provider invocation: how long a
// run may go on at all, whether or not it is getting anywhere. It is generous
// because it answers a different question from whether the run is stuck --
// defaultIdleTimeout answers that, far sooner -- and because a run stopped for
// either reason is left resumable rather than discarded. What it exists to catch
// is an invocation that stays live and unproductive, retrying or looping, which
// no liveness signal will ever catch.
const defaultTimeout = 4 * time.Hour

// defaultIdleTimeout bounds the gap between one event and the next. A working
// agent emits events continuously -- a thought, a tool call, its result -- so
// silence for this long means nothing is happening, and it can be far shorter
// than the total budget for exactly that reason.
const defaultIdleTimeout = 5 * time.Minute

// developerSettings is what a developer run is given beyond its tools: the
// OS-level sandbox that confines Bash, and the guard that stands in front of it.
//
// The guard is here rather than only in the repository's own agent settings
// because this is the settings source the harness owns. A developer run has
// Bash and is told to record its work with the tracker, so it is the most
// routine path to `bd update <id> --notes`, which replaces an item's notes and
// takes the goal recorded in them with it; `yoyo goals guard` reads the command
// and refuses that one. It rests on `yoyo` being on the PATH of the run, which
// is where the harness itself was invoked from -- and where it is not, Claude
// Code reports the hook as failed and runs the command, which is the behaviour
// there was before this. The guard can therefore be missing, but not wrong.
const developerSettings = `{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false},` +
	`"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"yoyo goals guard"}]}]}}`

// developerTools scopes built-in writes to the worktree project root. Bash is
// separately confined by Claude Code's OS-level sandbox settings below.
var developerTools = []string{"Bash", "Read", "Edit(/**)", "Write(/**)", "Glob", "Grep"}

// readOnlyPermissionMode is the only Claude Code mode that leaves a toolless
// role unable to apply an edit itself.
const readOnlyPermissionMode = "plan"

// readOnlyTools is intentionally empty. A reviewer receives a bounded context,
// patch, and check results, and the product manager, the architect, and the
// development manager receive bounded repository and tracker evidence;
// disabling tools prevents injected evidence from reading outside that evidence
// and exfiltrating unrelated local files, and is what makes "this role runs
// nothing" enforced rather than asked for. A product manager does change the
// work tracker and an architect does decide what a design says, and none of
// that happens here: the harness carries out validated actions on their behalf,
// so the authority never takes the form of a tool this process could be talked
// into using.
var readOnlyTools = []string{}

// readOnlyRole reports whether a role reasons over supplied evidence rather than
// reaching outside it. Such a role gets no tools and cannot be given them.
//
// Which roles those are is the contract's to say rather than this adapter's: it
// is the same statement a backend makes when it declares which postures it can
// hold, and a provider validated against one table and run against another is a
// provider whose configuration says nothing. A role nobody has decided a posture
// for has no posture at all, so it is neither read-only here nor supported by
// this backend, and it reaches the refusal in Run.
func readOnlyRole(role domain.AgentRole) bool {
	return backend.PostureFor(role) == backend.PostureReadOnly
}

// supportedRole reports whether this backend knows how to assemble an
// invocation for a role: which tools it gets and which permission mode it may
// run under. A role nobody has decided that for is refused rather than
// defaulted, because the only default available would be the developer's, and
// silently granting a shell to a role meant to have none is the failure this
// guard exists to prevent.
func supportedRole(role domain.AgentRole) bool {
	return role == domain.RoleDeveloper || readOnlyRole(role)
}

type Backend struct {
	Runner execution.ProcessRunner
	Binary string
	Clock  execution.Clock
	// Provider is the backend identifier this invocation is recorded under, and
	// is empty for Claude Code itself. A project that declared a provider running
	// on this adapter is a different backend from the one that ships here, and
	// what a run, a conversation, and a line of spend record has to be the
	// backend the agent named rather than the adapter that happened to launch it.
	Provider domain.Backend
	// Dialect is how what the provider says is read: which reports are limits,
	// which are retries the provider is taking itself, and when a limit lifts.
	// Empty is this provider's own dialect, which is every invocation until a
	// project declares one; a declared provider supplies its own as data.
	//
	// It decides nothing. Whether to wait, how long, and against which budget
	// stay above this adapter, which is what keeps a declared provider from being
	// able to spend an account.
	Dialect backend.Dialect
	// ConfigDir is the provider configuration directory this backend value asks
	// about and, where a request names none of its own, invokes under. It is what
	// lets a diagnosis ask one configured account whether it is authenticated:
	// CheckAvailability takes no request, so the account it is asking about has to
	// be on the value being asked. Empty is the machine's own provider home.
	ConfigDir string
}

// providerConfigDirVariable is how Claude Code is told which provider home to
// read. It is the provider's own variable rather than anything the harness
// invented, which is the whole of why an account is a directory here: the
// provider already keeps one account's authentication per home, so pooling is
// naming the homes rather than handling anybody's credentials.
//
// It is this adapter's rather than the contract's for the same reason the
// dialect is: how a provider is pointed at one account's credentials is that
// provider's own vocabulary, and a contract that named this variable would be
// naming Claude Code's spelling of an answer. What generalizes is the request's
// AccountConfigDir; what does not is that this provider reads it from here.
const providerConfigDirVariable = "CLAUDE_CONFIG_DIR"

// environmentFor is the environment one invocation is made in. Naming no
// directory returns nil, which leaves the process environment exactly as it is —
// so an installation with one account runs byte for byte the command it always
// did, and the account plumbing costs it nothing.
func environmentFor(configDir string) []string {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	return append(os.Environ(), providerConfigDirVariable+"="+configDir)
}

// dialect is what reads this invocation's stream: whatever the caller resolved
// for the backend the agent named, and this provider's own when nothing did.
func (b Backend) dialect() backend.Dialect {
	if b.Dialect == nil {
		return Dialect{}
	}
	return b.Dialect
}

// provider is the backend a result is recorded under.
func (b Backend) provider() domain.Backend {
	if b.Provider == "" {
		return domain.BackendClaudeCode
	}
	return b.Provider
}

func (b Backend) CheckAvailability(ctx context.Context) (backend.Availability, error) {
	if b.Runner == nil {
		return backend.Availability{}, errors.New("Claude Code process runner is required")
	}
	binary := b.binary()
	// Both questions are asked in the home this value was built for, so a
	// diagnosis asking about one pooled account cannot be answered by another
	// account's login. Naming no directory asks where the machine is signed in,
	// which is what a single-account installation has always done.
	environment := environmentFor(b.ConfigDir)
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"--version"}, Env: environment, Timeout: 10 * time.Second}, nil)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return backend.Availability{Installed: false}, nil
		}
		return backend.Availability{}, fmt.Errorf("check Claude Code version: %w", err)
	}
	if versionResult.Status != execution.ProcessSucceeded {
		return backend.Availability{Installed: false}, nil
	}
	availability := backend.Availability{Installed: true, Version: strings.TrimSpace(versionResult.Stdout)}

	authResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"auth", "status", "--json"}, Env: environment, Timeout: 10 * time.Second}, nil)
	if err != nil {
		return availability, fmt.Errorf("check Claude Code authentication: %w", err)
	}
	var status struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if err := json.Unmarshal([]byte(authResult.Stdout), &status); err != nil {
		return availability, fmt.Errorf("decode Claude Code authentication status: %w", err)
	}
	availability.Authenticated = status.LoggedIn
	availability.AuthMethod = status.AuthMethod
	availability.APIProvider = status.APIProvider
	return availability, nil
}

// Capabilities is what this adapter can do, read from the one description of
// this backend the harness holds rather than restated here. A configuration is
// validated against that description, so an adapter that answered differently
// would be one whose capability check meant nothing.
func (Backend) Capabilities() backend.Capabilities {
	descriptor, _ := backend.BuiltInDescriptor(domain.BackendClaudeCode)
	return descriptor.Capabilities
}

func (b Backend) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	if b.Runner == nil {
		return backend.RunResult{}, errors.New("Claude Code process runner is required")
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
	if !supportedRole(request.Role) {
		return backend.RunResult{}, fmt.Errorf("Claude Code backend does not support role %q", request.Role)
	}

	permissionMode := request.PermissionMode
	if permissionMode == "" {
		permissionMode = "acceptEdits"
		if readOnlyRole(request.Role) {
			permissionMode = readOnlyPermissionMode
		}
	}
	if !validPermissionMode(permissionMode) {
		return backend.RunResult{}, fmt.Errorf("unsupported Claude Code permission mode %q", permissionMode)
	}
	allowedTools := request.AllowedTools
	if allowedTools == nil {
		if readOnlyRole(request.Role) {
			allowedTools = readOnlyTools
		} else {
			allowedTools = developerTools
		}
	}
	for _, tool := range allowedTools {
		if tool == "" || strings.Contains(tool, ",") || strings.IndexFunc(tool, unicode.IsSpace) >= 0 {
			return backend.RunResult{}, fmt.Errorf("allowed tool rule %q cannot contain list delimiters", tool)
		}
	}
	// Advisory roles consume only the bounded supplied evidence. Refuse every
	// tool, including nominally read-only tools that could inspect outside that
	// evidence and send unrelated local data to the provider.
	if readOnlyRole(request.Role) {
		if len(allowedTools) > 0 {
			return backend.RunResult{}, fmt.Errorf("%s runs cannot be granted tools; the role reasons over bounded supplied evidence", request.Role)
		}
		if permissionMode != readOnlyPermissionMode {
			return backend.RunResult{}, fmt.Errorf("%s runs require the read-only %q permission mode, not %q", request.Role, readOnlyPermissionMode, permissionMode)
		}
	}
	if request.Role == domain.RoleDeveloper {
		for _, tool := range allowedTools {
			if !developerWriteToolIsScoped(tool) {
				return backend.RunResult{}, fmt.Errorf("developer write tool %q must be scoped to the worktree", tool)
			}
		}
	}

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", permissionMode,
		"--name", "yoyodyne-" + shortRunID(request.RunID),
	}
	if request.Role == domain.RoleDeveloper {
		args = append(args, "--settings", developerSettings)
	} else {
		// Repository instruction files are evidence, not harness policy. Safe
		// mode prevents a checked-in CLAUDE.md from entering the provider's
		// system context alongside an immutable harness contract.
		args = append(args, "--safe-mode")
	}
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, allowedTools...)
	} else {
		args = append(args, "--tools", "")
	}
	if request.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", request.SystemPrompt)
	}
	if request.SessionID != "" {
		args = append(args, "--resume", request.SessionID)
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
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
	// The account this invocation is made under is the request's, falling back to
	// the one this backend value was built for. A request that names neither runs
	// where the machine is already signed in, which is what a single-account
	// installation has always done.
	configDir := request.AccountConfigDir
	if strings.TrimSpace(configDir) == "" {
		configDir = b.ConfigDir
	}
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name:  b.binary(),
		Args:  args,
		Dir:   request.WorkingDirectory,
		Env:   environmentFor(configDir),
		Stdin: strings.NewReader(request.Prompt),
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
		return backend.RunResult{}, fmt.Errorf("run Claude Code: %w", err)
	}
	if len(parseErrors) > 0 {
		return backend.RunResult{}, fmt.Errorf("parse Claude Code stream: %w", errors.Join(parseErrors...))
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
	if !parser.SawResult() && processResult.Status == execution.ProcessSucceeded {
		return result, errors.New("Claude Code stream ended without a result event")
	}
	if processResult.Status == execution.ProcessFailed {
		result.IsError = true
		if result.StopReason == "" {
			result.StopReason = fmt.Sprintf("process_exit_%d", processResult.ExitCode)
		}
	}
	return result, nil
}

func (b Backend) binary() string {
	if b.Binary == "" {
		return "claude"
	}
	return b.Binary
}

func developerWriteToolIsScoped(tool string) bool {
	for _, name := range []string{"Edit", "Write"} {
		if tool == name {
			return false
		}
		prefix := name + "("
		if !strings.HasPrefix(tool, prefix) {
			continue
		}
		if !strings.HasSuffix(tool, ")") {
			return false
		}
		pattern := strings.TrimSuffix(strings.TrimPrefix(tool, prefix), ")")
		return strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "//") && !strings.Contains(pattern, "..")
	}
	return true
}

func validPermissionMode(mode string) bool {
	switch mode {
	case "acceptEdits", "auto", "dontAsk", "manual", "plan":
		return true
	default:
		return false
	}
}

func shortRunID(runID string) string {
	value := strings.TrimPrefix(runID, "run-")
	if len(value) > 8 {
		return value[:8]
	}
	return value
}
