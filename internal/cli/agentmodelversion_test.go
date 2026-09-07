package cli

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// An operator asking what an agent is has to be able to tell a pinned agent from
// a floating one, and to read the pin as the preference it is: the scope it
// covers and the model it falls back to are both said rather than assumed. A pin
// somebody read as a guarantee is one they would take a version they never saw
// served as evidence against.
func TestAPinnedAgentSaysWhatItIsPinnedToAndWhatAnswersInstead(t *testing.T) {
	t.Parallel()

	rendered := renderAgent(agentReport{
		Name:         "architect",
		Role:         domain.RoleArchitect,
		Backend:      domain.BackendClaudeCode,
		Model:        "opus",
		ModelVersion: "claude-opus-5-20260401",
		Instances:    1,
	})
	for _, want := range []string{"claude-opus-5-20260401", "falling back to opus", "run invocations ask for opus"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered = %q, which does not say %q", rendered, want)
		}
	}
}

// And an agent that pinned nothing says nothing about a pin, because the
// floating alias is the ordinary case and a line for it would be noise on every
// agent in the project.
func TestAnUnpinnedAgentSaysNothingAboutAPin(t *testing.T) {
	t.Parallel()

	rendered := renderAgent(agentReport{
		Name:      "architect",
		Role:      domain.RoleArchitect,
		Backend:   domain.BackendClaudeCode,
		Model:     "opus",
		Instances: 1,
	})
	if strings.Contains(rendered, "pinned") {
		t.Fatalf("rendered = %q, want nothing said about a pin the agent does not have", rendered)
	}
}
