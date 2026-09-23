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

// GitEnvironment returns the environment a Git command the harness runs itself
// is given: the same allowlist an agent invocation is built from.
//
// It is the same answer ExplicitEnvironment gives and it is named separately
// because the reason is different. An agent invocation is built explicitly so
// that nothing the harness's environment carried reaches an agent; a Git
// command is built explicitly because Git runs hooks, and a hook lives in the
// repository rather than in the harness. A `git worktree add` runs
// post-checkout, a ref update runs reference-transaction, and both of those are
// programs a run's own checkout supplies -- so a Git command that inherited the
// harness's environment handed a Slack token to a program the harness never
// wrote, by a path the run's own explicit environment says nothing about.
//
// Nothing about the forge is here. A command that talks to a remote gets
// ForgeEnvironment, which is this plus the credential that command needs and
// nothing else gets.
func GitEnvironment(parent []string) []string {
	return ExplicitEnvironment(parent)
}

// forgeEnvironmentNames are what a command that talks to the forge is given on
// top of the allowlist: the credential an installation authenticates the forge
// CLI with, the two settings that say which forge and which stored login, and
// the transport settings a Git command reaching a remote is pointed at its keys
// by.
//
// The credential ones are all names sensitiveEnvironmentName recognizes, so
// they are dropped from every other process the harness starts and reinstated
// only here. That is the whole of the arrangement: the forge commands are the
// ones that need a forge credential, and they are a short, named list rather
// than everything that happens to run.
var forgeEnvironmentNames = []string{
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_ENTERPRISE_TOKEN",
	"GITHUB_ENTERPRISE_TOKEN",
	"GH_HOST",
	"GH_CONFIG_DIR",
	"GIT_ASKPASS",
	"SSH_ASKPASS",
	"GIT_SSH",
	"GIT_SSH_COMMAND",
	"GIT_TERMINAL_PROMPT",
}

// ForgeEnvironment returns the environment a command that talks to the forge is
// given: the allowlisted environment every harness-launched process gets, plus
// the entries forgeEnvironmentNames admits, in the order parent carried them.
//
// A nil parent starts from this process's own environment, as ExplicitEnvironment
// does. A name the allowlist already admitted is not added twice: which of two
// entries of one name a process reads is the operating system's to decide, and a
// credential that depended on that would be one nobody could reason about.
func ForgeEnvironment(parent []string) []string {
	if parent == nil {
		parent = os.Environ()
	}
	environment := ExplicitEnvironment(parent)
	carried := make(map[string]struct{}, len(environment))
	for _, entry := range environment {
		if name, _, named := strings.Cut(entry, "="); named {
			carried[name] = struct{}{}
		}
	}
	for _, entry := range parent {
		name, _, named := strings.Cut(entry, "=")
		if !named || !forgeEnvironmentName(name) {
			continue
		}
		if _, already := carried[name]; already {
			continue
		}
		carried[name] = struct{}{}
		environment = append(environment, entry)
	}
	return environment
}

func forgeEnvironmentName(name string) bool {
	for _, admitted := range forgeEnvironmentNames {
		if name == admitted {
			return true
		}
	}
	return false
}

// ProviderKeyNames are the environment variables an installation may have been
// authenticating a provider with. Every one of them reads as a credential, so
// every one of them is dropped from the environment an invocation is built with
// -- which is the point and is also what makes the list worth stating.
//
// Before the explicit environment, a key exported in a shell profile
// authenticated every provider invocation the harness made. It now authenticates
// none of them, and an installation that had been relying on it does not
// degrade: its next run is refused by the provider. So the names are here, in
// one place, and the surfaces that warn about it read them from here rather than
// each spelling its own list.
//
// Provider authentication is the provider's own login, held in its provider
// home. That is what the accounts machinery names an account by, and it is the
// only authentication a run receives.
var ProviderKeyNames = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY",
}

// ProviderKeysInEnvironment names the provider keys parent carries, in the order
// ProviderKeyNames states them. A nil parent is this process's own environment.
//
// It names the variables and never reads a value out to a caller: what a surface
// reports is that a key is set, and a diagnostic that helpfully printed one would
// put it in a terminal and a scrollback.
func ProviderKeysInEnvironment(parent []string) []string {
	if parent == nil {
		parent = os.Environ()
	}
	set := make(map[string]struct{}, len(parent))
	for _, entry := range parent {
		name, value, named := strings.Cut(entry, "=")
		if !named || strings.TrimSpace(value) == "" {
			continue
		}
		set[name] = struct{}{}
	}
	present := make([]string, 0, len(ProviderKeyNames))
	for _, name := range ProviderKeyNames {
		if _, carried := set[name]; carried {
			present = append(present, name)
		}
	}
	return present
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
