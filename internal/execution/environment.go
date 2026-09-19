package execution

import (
	"os"
	"strings"
)

// The environment a run's subprocess tree is given, built rather than inherited.
//
// The Slack sink is the only process that holds a Slack token, and the design
// states that as structural: no run process, and therefore no agent's subprocess
// tree, ever has one in its environment. An invocation that inherits the
// harness's whole environment makes that a warning in a document instead -- an
// operator who exports SLACK_BOT_TOKEN in a shell profile hands it to every
// agent the harness starts, and nothing between the shell and the agent says no.
//
// So every process the harness launches for a run or a conversation -- the
// provider's own binary, and the project's checks -- is given an environment
// built here from an allowlist: what a program needs to run at all, what the
// provider reads its own settings from, what the toolchains a check runs read,
// and the two things the harness sets for itself, the build cache and the Git
// maintenance fence. Everything else the harness's own environment carried stays
// with the harness. And whatever the allowlist admits, a name that reads as a
// credential is dropped: the same names SensitiveEnvironmentValues redacts from
// a process's output are never in a process's environment, so the token pair is
// kept out twice over -- once by not being on the list, and once by being
// recognized for what it is even if the list ever admitted it.
//
// What that costs is stated rather than hidden. A provider authenticated by an
// API key in the environment is authenticated by a credential, and this drops
// it; provider authentication is CLI-managed, by the provider's own login held
// in its provider home, which is how the accounts machinery names an account in
// the first place. A check whose tooling reads a variable not listed here does
// not see it, and `docs/configuration.md` says so where checks are described.

// explicitEnvironmentNames are carried by exact name.
var explicitEnvironmentNames = map[string]struct{}{
	// What a program needs to run at all.
	"PATH":    {},
	"HOME":    {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"TMPDIR":  {},
	"TERM":    {},
	"TZ":      {},
	// Locale.
	"LANG":     {},
	"LANGUAGE": {},
	// A Git command over an SSH remote -- a private module a check fetches --
	// asks the agent at this socket. It is a path to a socket rather than a
	// credential, and the keys stay with the agent that holds them.
	"SSH_AUTH_SOCK": {},
	// Reaching the provider from behind a proxy, and trusting the certificates
	// that proxy presents.
	"HTTP_PROXY":          {},
	"HTTPS_PROXY":         {},
	"NO_PROXY":            {},
	"ALL_PROXY":           {},
	"http_proxy":          {},
	"https_proxy":         {},
	"no_proxy":            {},
	"all_proxy":           {},
	"SSL_CERT_FILE":       {},
	"SSL_CERT_DIR":        {},
	"NODE_EXTRA_CA_CERTS": {},
}

// explicitEnvironmentPrefixes are carried by family: the locale's `LC_*`, the
// XDG base directories, everything the Go toolchain reads, Git's own
// environment configuration (which is where the maintenance fence lives, and
// where a harness launched by a harness finds the fence it was given), and the
// harness's own variables.
var explicitEnvironmentPrefixes = []string{
	"LC_",
	"XDG_",
	"GO",
	"GIT_CONFIG_",
	"YOYODYNE_",
}

// ExplicitEnvironment returns the environment a process the harness launches
// for a run or a conversation is given: the entries of parent whose names the
// allowlist admits, in the order parent carried them, less any whose name reads
// as a credential. A nil parent starts from this process's own environment,
// which is what a command that would otherwise have inherited it needs.
//
// providerPrefixes are the families the provider being launched reads its own
// settings from, named by the backend that knows them -- `CLAUDE_` and
// `ANTHROPIC_` for Claude Code, say -- because which variables are a provider's
// own is that provider's vocabulary rather than this package's. The credential
// rule applies to them as it does to everything else: a provider's API key is
// under its prefix and is still dropped.
//
// It builds and never appends. The build cache and the fence are added by the
// callers that add them today, on top of what this returns, so an explicit
// environment is the base every run stands on rather than one more thing
// layered over inheritance.
func ExplicitEnvironment(parent []string, providerPrefixes ...string) []string {
	if parent == nil {
		parent = os.Environ()
	}
	kept := make([]string, 0, len(parent))
	for _, entry := range parent {
		name, _, named := strings.Cut(entry, "=")
		if !named || !explicitEnvironmentAdmits(name, providerPrefixes) || sensitiveEnvironmentName(name) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// explicitEnvironmentAdmits reports whether a name is on the allowlist, by exact
// name, by one of the standing families, or by one of the provider's own.
func explicitEnvironmentAdmits(name string, providerPrefixes []string) bool {
	if _, listed := explicitEnvironmentNames[name]; listed {
		return true
	}
	for _, prefix := range explicitEnvironmentPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, prefix := range providerPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
