package backend

import (
	"context"
	"time"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

type Capabilities struct {
	StructuredEvents  bool `json:"structured_events"`
	SessionResumption bool `json:"session_resumption"`
	StructuredOutput  bool `json:"structured_output"`
	ToolControl       bool `json:"tool_control"`
	LocalAuth         bool `json:"local_auth"`
}

type Availability struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"`
	APIProvider   string `json:"api_provider,omitempty"`
}

type RunRequest struct {
	RunID            string
	Role             domain.AgentRole
	WorkingDirectory string
	Prompt           string
	SystemPrompt     string
	SessionID        string
	Model            string
	PermissionMode   string
	AllowedTools     []string
	Timeout          time.Duration
	LastSequence     uint64
	RedactValues     []string
	EventSink        func(execution.Event) error
}

type RunResult struct {
	Backend    domain.Backend
	SessionID  string
	FinalText  string
	IsError    bool
	CostUSD    float64
	Usage      []byte
	Process    execution.ProcessResult
	LastEvent  uint64
	StopReason string
}

type Backend interface {
	CheckAvailability(ctx context.Context) (Availability, error)
	Capabilities() Capabilities
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}
