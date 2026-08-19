package beads

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// SyncRemote is one remote the tracker synchronizes through. Beads keeps its
// data in a Dolt database and moves it over an ordinary Git remote, under refs
// of its own, so this is where one machine's tracker joins everyone else's
// rather than diverging in place.
type SyncRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

var syncRemotePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SyncRemotes reports the remotes the tracker is configured to sync through. It
// is read-only, and a tracker configured with none reports none rather than
// failing: an unconfigured tracker is a state a caller acts on, not an error.
func (c Client) SyncRemotes(ctx context.Context) ([]SyncRemote, error) {
	data, err := c.run(ctx, "dolt", "remote", "list", "--json")
	if err != nil {
		return nil, err
	}
	var remotes []SyncRemote
	if err := decodeJSON(data, &remotes); err != nil {
		return nil, fmt.Errorf("decode bd sync remotes: %w", err)
	}
	return remotes, nil
}

// SetSyncRemote points a named remote at a URL and answers with the remote as
// bd then holds it. Like every other write here it is the mechanism rather than
// the authority: the caller is responsible for the URL being one the project
// meant its tracker to ride on.
func (c Client) SetSyncRemote(ctx context.Context, name, url string) (SyncRemote, error) {
	if !syncRemotePattern.MatchString(name) {
		return SyncRemote{}, fmt.Errorf("invalid sync remote name %q", name)
	}
	trimmed := strings.TrimSpace(url)
	if trimmed == "" {
		return SyncRemote{}, fmt.Errorf("sync remote %s needs a URL", name)
	}
	if strings.HasPrefix(trimmed, "-") || strings.ContainsAny(trimmed, " \t\r\n") {
		return SyncRemote{}, fmt.Errorf("invalid sync remote URL %q", url)
	}
	if _, err := c.run(ctx, "dolt", "remote", "add", name, trimmed); err != nil {
		return SyncRemote{}, err
	}
	// What was asked for is read back rather than assumed, for the reason an
	// edit to a work item is: a remote that was not actually stored would leave
	// the caller reporting a tracker that syncs and the operator with one that
	// does not. It is confirmed by name and reported as bd holds it, because bd
	// normalizes what it is given -- a `git@host:owner/repo` is stored and
	// listed as `git+ssh://git@host/owner/repo` -- and comparing the two
	// spellings would refuse a remote configured exactly as asked.
	remotes, err := c.SyncRemotes(ctx)
	if err != nil {
		return SyncRemote{}, err
	}
	for _, remote := range remotes {
		if remote.Name != name {
			continue
		}
		if strings.TrimSpace(remote.URL) == "" {
			return SyncRemote{}, fmt.Errorf("sync remote %s has no URL after being configured", name)
		}
		return remote, nil
	}
	return SyncRemote{}, fmt.Errorf("the tracker has no sync remote %s after it was configured", name)
}
