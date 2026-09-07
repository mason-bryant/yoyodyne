package config

import (
	"strings"
	"testing"
)

// Nothing acquires side threads by inheriting a bundle or by upgrading the
// executable. Queueing is what every agent did before this key existed, and it
// is what every agent that has not written the key still does.
func TestEveryAgentQueuesUntilOneSaysOtherwise(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	for name := range cfg.Agents {
		if mode := cfg.AgentConversationMode(name); mode != ConversationQueue {
			t.Errorf("agent %q conversations = %q, want %q until the agent says otherwise", name, mode, ConversationQueue)
		}
		if cfg.AgentHoldsSideThreads(name) {
			t.Errorf("agent %q holds side threads and nothing configured it to", name)
		}
	}
	// An agent nobody configured at all is asked the same question by anything
	// resolving a role, and it queues rather than answering with an empty mode.
	if mode := cfg.AgentConversationMode("nobody"); mode != ConversationQueue {
		t.Errorf("an unconfigured agent's conversations = %q, want %q", mode, ConversationQueue)
	}
}

// The choice is the agent's own, beside the persona and the model, because which
// roles are worth answering two questions at once is a judgement about the work
// rather than something the harness derives from a role name.
func TestAnAgentChoosesSideThreadsForItselfAndForNobodyElse(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  architect:
    conversations: side-threads
`, nil).Config
	if !cfg.AgentHoldsSideThreads("architect") {
		t.Fatalf("architect conversations = %q, want the mode the agent named", cfg.AgentConversationMode("architect"))
	}
	if cfg.AgentHoldsSideThreads("product-manager") {
		t.Fatal("configuring one agent for side threads gave another one side threads")
	}
}

// A value that is not a mode is refused at load with the rest of the
// configuration, naming the modes there are: a file that chose something the
// harness does not recognize is a project believing a behaviour is in force that
// nothing implements.
func TestAModeTheHarnessDoesNotRecognizeIsRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  architect:
    conversations: concurrent
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() error = nil, want a mode nothing implements refused")
	}
	if !strings.Contains(err.Error(), `"concurrent"`) || !strings.Contains(err.Error(), string(ConversationSideThreads)) {
		t.Fatalf("error = %v, want it to name what was written and the modes there are", err)
	}
}

// Stating it empty in a later layer puts an agent back to queueing, so a project
// can undo an inherited choice without having to know which layer underneath
// made it.
func TestAnEmptyModeInALaterLayerPutsAnAgentBackToQueueing(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    conversations: side-threads
`)},
		{origin: "project", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    conversations: ""
`)},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	if resolved.Config.AgentHoldsSideThreads("architect") {
		t.Fatal("architect still holds side threads after a layer put it back to queueing")
	}
	if origin := resolved.Origins["agents.architect.conversations"]; origin != "project" {
		t.Fatalf("conversations origin = %q, want project", origin)
	}
}

// The knob is part of what a run was configured by, like every other effective
// value: two runs under the same revision were configured identically, and an
// agent that gained side threads is not the same configuration it was.
func TestChoosingSideThreadsMovesTheConfigurationRevision(t *testing.T) {
	t.Parallel()

	queueing := loadProject(t, minimalProjectConfig, nil).Config
	aside := loadProject(t, minimalProjectConfig+`agents:
  architect:
    conversations: side-threads
`, nil).Config
	if queueing.Revision() == aside.Revision() {
		t.Fatalf("revision = %q for both, want an agent that holds side threads to be a different configuration", queueing.Revision())
	}
}
