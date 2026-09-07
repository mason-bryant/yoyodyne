package config

// Choosing, per agent, what becomes of a question that arrives while its main
// thread is busy.
//
// A conversation serializes: one turn at a time behind a single-holder lease,
// which is what stops two processes interleaving a transcript. The price is that
// a held thread queues everything behind whatever it is doing, so a question
// worth a minute waits out a turn worth twenty. A side thread is the same
// machinery given a second identity — its own stream, its own lease, its own
// transcript — so the question is answered beside the long turn rather than
// after it.
//
// Which agents are worth that is the operator's judgement rather than the
// harness's, exactly as failover is: a management role every other role waits on
// and a developer whose questions can wait are different answers to the same
// question. So it is stated per agent, in the agent's own block, and it is
// `queue` — today's behaviour — until an agent says otherwise. Nothing acquires
// side threads by inheriting a bundle or by upgrading the executable.
//
// What this key never does is widen anything. It chooses whether an agent holds
// side threads at all; what a side thread may do is `internal/sidestream`'s
// Permitted, which is a list in Go that reads no configuration, and the role's
// own authority narrowed by it in `internal/chat`. No value here reaches either,
// which is what `configuration-never-grants-authority` requires: configuration
// selects behaviour and never authority.

import (
	"fmt"
	"strings"
)

// ConversationMode is what an agent does with a question it cannot take on its
// main thread yet.
type ConversationMode string

const (
	// ConversationQueue is today's behaviour and the default: a question waits
	// for the main thread's lease and is answered in its turn.
	ConversationQueue ConversationMode = "queue"
	// ConversationSideThreads lets the agent hold bounded side conversations
	// beside its main thread. A side thread judges, answers, and drafts, and every
	// intent it forms is ratified by the main thread through its own
	// single-threaded path.
	ConversationSideThreads ConversationMode = "side-threads"
)

// ConversationModes is what an agent may choose between, in the order a refusal
// names them.
func ConversationModes() []ConversationMode {
	return []ConversationMode{ConversationQueue, ConversationSideThreads}
}

func (m ConversationMode) Valid() bool {
	switch m {
	case ConversationQueue, ConversationSideThreads:
		return true
	default:
		return false
	}
}

// Effective reads an unstated choice as the default, so every caller asks one
// question rather than remembering that an agent naming nothing queues.
func (m ConversationMode) Effective() ConversationMode {
	if strings.TrimSpace(string(m)) == "" {
		return ConversationQueue
	}
	return m
}

// conversationModeProblems reports an agent that named something that is not a
// mode. An agent naming nothing is not a problem: it queues, which is what every
// agent did before this key existed.
func conversationModeProblems(name string, mode ConversationMode) []string {
	if strings.TrimSpace(string(mode)) == "" || mode.Valid() {
		return nil
	}
	return []string{fmt.Sprintf("agent %q conversations is %q, which is not a mode; the modes are %s",
		name, mode, describeConversationModes())}
}

func describeConversationModes() string {
	named := make([]string, 0, len(ConversationModes()))
	for _, mode := range ConversationModes() {
		named = append(named, fmt.Sprintf("%q", mode))
	}
	return strings.Join(named, ", ")
}

// AgentConversationMode is how one configured agent answers a question its main
// thread cannot take yet, which is queueing for every agent that has not said
// otherwise — including an agent nothing configured at all.
func (c Config) AgentConversationMode(name string) ConversationMode {
	return c.Agents[name].Conversations.Effective()
}

// AgentHoldsSideThreads reports an agent configured to hold side conversations
// beside its main thread.
//
// It answers whether a side thread may be opened for this agent and nothing
// about what one may do once it is: a side thread's authority is its role's,
// narrowed in Go, and this answer never reaches it.
func (c Config) AgentHoldsSideThreads(name string) bool {
	return c.AgentConversationMode(name) == ConversationSideThreads
}
