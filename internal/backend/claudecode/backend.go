package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

const defaultTimeout = 30 * time.Minute

const developerSandboxSettings = `{"sandbox":{"enabled":true,"failIfUnavailable":true,"allowUnsandboxedCommands":false}}`

// developerTools scopes built-in writes to the worktree project root. Bash is
// separately confined by Claude Code's OS-level sandbox settings below.
var developerTools = []string{"Bash", "Read", "Edit(/**)", "Write(/**)", "Glob", "Grep"}

// readOnlyPermissionMode is the only Claude Code mode that leaves a toolless
// role unable to apply an edit itself.
const readOnlyPermissionMode = "plan"

// readOnlyTools is intentionally empty. A reviewer receives a bounded context,
// patch, and check results, and a product manager receives bounded repository
// and tracker evidence; disabling tools prevents injected evidence from reading
// outside that evidence and exfiltrating unrelated local files, and is what
// makes "this role runs nothing" enforced rather than asked for. A product
// manager does change the work tracker, and none of that happens here: the
// harness carries out validated actions on its behalf, so the authority never
// takes the form of a tool this process could be talked into using.
var readOnlyTools = []string{}

// readOnlyRole reports whether a role reasons over supplied evidence rather than
// reaching outside it. Such a role gets no tools and cannot be given them.
func readOnlyRole(role domain.AgentRole) bool {
	return role == domain.RoleReviewer || role == domain.RoleProductManager
}

type Backend struct {
	Runner execution.ProcessRunner
	Binary string
	Clock  execution.Clock
}

func (b Backend) CheckAvailability(ctx context.Context) (backend.Availability, error) {
	if b.Runner == nil {
		return backend.Availability{}, errors.New("Claude Code process runner is required")
	}
	binary := b.binary()
	versionResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"--version"}, Timeout: 10 * time.Second}, nil)
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

	authResult, err := b.Runner.Run(ctx, execution.Command{Name: binary, Args: []string{"auth", "status", "--json"}, Timeout: 10 * time.Second}, nil)
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

func (Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		StructuredEvents:  true,
		SessionResumption: true,
		StructuredOutput:  true,
		ToolControl:       true,
		LocalAuth:         true,
	}
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
	switch request.Role {
	case domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager:
	default:
		return backend.RunResult{}, fmt.Errorf("Claude Code bootstrap backend does not yet support role %q", request.Role)
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
		args = append(args, "--settings", developerSandboxSettings)
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

	clock := b.Clock
	if clock == nil {
		clock = execution.RealClock{}
	}
	redactor := execution.NewRedactor(request.RedactValues...)
	parser := newStreamParser(request.RunID, request.LastSequence, clock, redactor, request.EventSink)
	var parseErrors []error
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name:     b.binary(),
		Args:     args,
		Dir:      request.WorkingDirectory,
		Stdin:    strings.NewReader(request.Prompt),
		Timeout:  timeout,
		Redactor: redactor,
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
	result.Backend = domain.BackendClaudeCode
	result.Process = processResult
	if processResult.Status == execution.ProcessCancelled || processResult.Status == execution.ProcessTimedOut {
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
