package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

const defaultTimeout = 30 * time.Minute

// readOnlyPermissionMode is the only Claude Code mode that leaves a reviewer
// unable to apply an edit it proposes.
const readOnlyPermissionMode = "plan"

// readOnlyTools is the largest tool set a reviewer may hold: enough to read the
// worktree and search it, with nothing that can write a file or run a command.
var readOnlyTools = []string{"Glob", "Grep", "Read"}

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
	if request.Role != domain.RoleDeveloper && request.Role != domain.RoleReviewer {
		return backend.RunResult{}, fmt.Errorf("Claude Code bootstrap backend does not yet support role %q", request.Role)
	}

	permissionMode := request.PermissionMode
	if permissionMode == "" {
		permissionMode = "acceptEdits"
		if request.Role == domain.RoleReviewer {
			permissionMode = readOnlyPermissionMode
		}
	}
	if !validPermissionMode(permissionMode) {
		return backend.RunResult{}, fmt.Errorf("unsupported Claude Code permission mode %q", permissionMode)
	}
	allowedTools := request.AllowedTools
	if allowedTools == nil {
		allowedTools = []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep"}
	}
	for _, tool := range allowedTools {
		if strings.ContainsAny(tool, "\r\n") {
			return backend.RunResult{}, errors.New("allowed tool names cannot contain newlines")
		}
	}
	// A reviewer must not be able to change what it is reviewing, so the
	// adapter refuses a reviewer run that was granted any write-capable tool
	// even if the caller asked for one.
	if request.Role == domain.RoleReviewer {
		if request.AllowedTools == nil {
			allowedTools = readOnlyTools
		}
		for _, tool := range allowedTools {
			if !isReadOnlyTool(tool) {
				return backend.RunResult{}, fmt.Errorf("reviewer runs cannot be granted write-capable tool %q", tool)
			}
		}
		if permissionMode != readOnlyPermissionMode {
			return backend.RunResult{}, fmt.Errorf("reviewer runs require the read-only %q permission mode, not %q", readOnlyPermissionMode, permissionMode)
		}
	}

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", permissionMode,
		"--name", "yoyodyne-" + shortRunID(request.RunID),
	}
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowedTools, ","))
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
	parser := newStreamParser(request.RunID, request.LastSequence, clock, request.EventSink)
	var parseErrors []error
	processResult, err := b.Runner.Run(ctx, execution.Command{
		Name:     b.binary(),
		Args:     args,
		Dir:      request.WorkingDirectory,
		Stdin:    strings.NewReader(request.Prompt),
		Timeout:  timeout,
		Redactor: execution.NewRedactor(request.RedactValues...),
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

func isReadOnlyTool(tool string) bool {
	for _, readOnly := range readOnlyTools {
		if tool == readOnly {
			return true
		}
	}
	return false
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
