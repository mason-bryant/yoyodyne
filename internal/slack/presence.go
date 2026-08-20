package slack

// What a running sink records about itself, so something other than the sink
// can say what is reporting for this product.
//
// The lease already answers whether a sink is alive, and that turned out to be
// the easy half. A sink can hold the lease and still be wrong in three ways
// nobody watching a channel would see, because all three look like a channel
// that has simply gone quiet: an old binary that posts the milestones it knew
// about and silently drops every one added since it was installed; a sink
// reading a different project's configuration; and — with several harnesses on
// one machine — a sink holding a sibling project's token pair, because the shell
// it was started from had those exported. So the sink writes down which build it
// is, which configuration it read, and whose secrets it was launched with, and
// `yoyo doctor` reads that back.
//
// It holds no credential and never will. What it records about the tokens is the
// name of the namespace they were read from and the workspace identity Slack
// itself reported back for them; both are addressing rather than secrets, and
// neither can be turned into a token.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// PresenceSchemaVersion is versioned separately from the thread map and the
// cursors for the reason those are versioned separately from each other: a sink
// may learn to record more about itself without every other record moving.
const PresenceSchemaVersion = 1

// SecretNamespaceVariable is how a launcher tells the sink whose secrets it
// read. It is an environment variable rather than a flag because the launcher
// that reads the tokens is already setting environment for exactly one process,
// and a project name is the one part of that which is safe to write down.
const SecretNamespaceVariable = "YOYO_SLACK_SECRET_NAMESPACE"

// BotSecret and AppSecret name this product's two Slack tokens in whatever
// store holds them: a macOS keychain item, an environment file, a password
// manager entry somebody copies by hand.
//
// They are namespaced by product because the generic pair is indistinguishable
// between projects, and indistinguishable is how a sink ends up posting a second
// project's work into the first project's channel. Several harnesses on one
// machine is the ordinary case rather than the exotic one — the harness develops
// itself in a worktree beside whatever else the operator is running — and under
// generic names every check of the form "a Slack token exists" passes for all of
// them while at most one of them is right.
func BotSecret(productID domain.ProductID) string {
	return "yoyo-slack-bot." + string(productID)
}

func AppSecret(productID domain.ProductID) string {
	return "yoyo-slack-app." + string(productID)
}

// Presence is one running sink's account of itself.
type Presence struct {
	SchemaVersion int `json:"schema_version"`
	// PID is the process holding the lease, so a remedy that has to stop it can
	// name it rather than matching on a command line that would also match the
	// sibling project's sink.
	PID int `json:"pid,omitempty"`
	// Version is the build. This is the field the stale-sink case turns on: a
	// sink installed months ago reports what that build knew how to report and
	// nothing since, and the channel it posts into looks healthy.
	Version string `json:"version,omitempty"`
	// Config is the configuration file this sink loaded, and Channel is where it
	// posts. A sink reading another project's configuration is reporting the
	// wrong product's work under this product's lease.
	Config  string `json:"config,omitempty"`
	Channel string `json:"channel,omitempty"`
	// SecretNamespace is the product whose namespaced secrets the launcher read,
	// as it declared them. It is empty for a sink started from a shell that
	// already had the two tokens exported, which is not a failure but is the one
	// case where nothing at all is known about whose tokens are in use.
	SecretNamespace string `json:"secret_namespace,omitempty"`
	// Team and TeamID are what the workspace said this app belongs to. They are
	// recorded because they are the only fact here that comes from the tokens
	// themselves rather than from what somebody claimed about them.
	Team      string    `json:"team,omitempty"`
	TeamID    string    `json:"team_id,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

// SavePresence records what this sink is running as.
func (s *Store) SavePresence(presence Presence) error {
	presence.SchemaVersion = PresenceSchemaVersion
	return s.write(s.presencePath(), "slack sink presence", presence)
}

// LoadPresence reads it back, reporting whether a sink ever wrote one. A record
// left behind by a sink that was killed is returned like any other: whether the
// process is still there is the lease's answer, not this file's, and the two
// read together are what tells a live sink from the corpse of one.
func (s *Store) LoadPresence() (Presence, bool, error) {
	var stored Presence
	found, err := readJSON(s.presencePath(), "slack sink presence", &stored)
	if err != nil {
		return Presence{}, false, err
	}
	if !found {
		return Presence{}, false, nil
	}
	if stored.SchemaVersion != PresenceSchemaVersion {
		return Presence{}, false, fmt.Errorf("slack sink presence schema version %d is not supported", stored.SchemaVersion)
	}
	return stored, true, nil
}

// ClearPresence forgets it, which a sink does on its way out so that a stopped
// sink reads as stopped rather than as one whose build somebody should check.
func (s *Store) ClearPresence() error {
	if err := os.Remove(s.presencePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the slack sink presence: %w", err)
	}
	return nil
}

func (s *Store) presencePath() string { return filepath.Join(s.root, "presence.json") }
