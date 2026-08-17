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
	// Timeout is the total budget for the invocation and IdleTimeout is how long
	// it may go without emitting an event. They answer different questions -- is
	// this run worth continuing, and is it doing anything at all -- so a backend
	// applies both rather than letting either stand in for the other. Zero means
	// the backend's default for each.
	Timeout      time.Duration
	IdleTimeout  time.Duration
	LastSequence uint64
	RedactValues []string
	EventSink    func(execution.Event) error
}

// UsageLimit is a provider's report that a usage limit is exhausted. It is
// deliberately not a failure: the work was never judged, only declined for want
// of capacity, so what it calls for is a wait rather than a failure record. A
// transient throttle never produces one of these — the provider CLI retries
// those itself and reports them as its own api_retry event.
type UsageLimit struct {
	// Kind is the provider's own name for the exhausted limit, carried as
	// evidence rather than interpreted by the harness.
	Kind string
	// ResetsAt is when the provider said the limit resets. It is zero when the
	// provider named no usable reset time, which is not a wait a caller may
	// guess at.
	ResetsAt time.Time
}

type RunResult struct {
	Backend   domain.Backend
	SessionID string
	// ResolvedModel is the model the provider reported actually serving the
	// invocation. A requested selector may be a floating family alias, so the
	// resolved identifier is the only durable evidence of what really ran.
	ResolvedModel string
	FinalText     string
	IsError       bool
	CostUSD       float64
	Usage         []byte
	Process       execution.ProcessResult
	LastEvent     uint64
	StopReason    string
	// UsageLimit is set when the provider reported an exhausted usage limit
	// during this invocation. It is separate from IsError because the two call
	// for opposite responses: a failure ends the run, an exhausted limit asks
	// the caller to wait and ask again.
	UsageLimit *UsageLimit
}

type Backend interface {
	CheckAvailability(ctx context.Context) (Availability, error)
	Capabilities() Capabilities
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}
