package doctor

// Diagnosing the reporting sink, which is the part of an installation that is
// wrong most quietly.
//
// Everything else here fails loudly: an unauthenticated provider refuses an
// invocation, an uninitialized tracker refuses a claim. A sink fails by saying
// nothing, and a channel that says nothing is indistinguishable from a harness
// with nothing to report. So the questions asked here go past "is it running":
//
//   - Are this project's own secrets stored, under the namespaced names, rather
//     than some Slack token being present somewhere on the machine? Several
//     harnesses on one machine is the ordinary case, and under the old generic
//     names every one of them passes a check the way the wrong one would.
//   - Is a sink actually holding this product's lease, or is there only the
//     record of one that died?
//   - Is the sink that is running this build? A sink installed months ago posts
//     the milestones that build knew about and drops every one added since,
//     which reads in the channel as a quiet week.
//   - Is it running with this project's secrets, rather than with whatever the
//     shell it was started from happened to have exported?
//
// None of those is answerable from the lease, which is why the sink writes down
// what it is and this reads it back.
//
// Nothing in this file ever reaches StatusProblem, and that is a rule rather
// than an accident of the cases that happen to be here. A problem is what stops
// work running, and reporting is an observation and never a gate: a sink that
// was never started, a workspace that is down, a token nobody stored — none of
// them changes anything about any run, so an operator with all three still has
// an installation that works and an exit status that says so. Reported as
// problems, these would make `yoyo doctor` fail on a healthy machine, and the
// first thing anybody gating on it would do is stop gating on it.
//
// What they are instead is the whole reason this file is long. A warning here is
// not a small problem; it is the class of failure that produces silence rather
// than an error, which is why each one is named exactly and carries the command
// that ends it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// keychainAccount is the account the namespaced Slack secrets are stored under
// on macOS. It is fixed because the distinguishing part is the service name,
// which carries the product.
const keychainAccount = "yoyo"

// checkSlack diagnoses reporting: the configuration, this instance's secrets,
// and the sink itself. A project that has not turned reporting on is healthy
// with nothing else to check, because reporting is an opt-in observation rather
// than a component the harness needs.
func (d *diagnosis) checkSlack(ctx context.Context, resolved config.Resolved, installed string) []Finding {
	settings := resolved.Config.Slack
	if !settings.Enabled {
		return []Finding{{
			Check:   "slack",
			Status:  StatusOK,
			Summary: "Slack reporting is off for this project",
			Detail:  "set slack.enabled and slack.channel in " + resolved.Path + " to turn it on",
		}}
	}
	findings := []Finding{{
		Check:   "slack",
		Status:  StatusOK,
		Summary: fmt.Sprintf("Slack reporting is on, into %s", settings.Channel),
	}}
	productID := resolved.Config.Product.ID
	findings = append(findings, d.checkSlackSecrets(ctx, productID))
	findings = append(findings, d.checkSlackSink(resolved, productID, installed)...)
	return findings
}

// checkSlackSecrets asks whether this product's own token pair is stored under
// the namespaced names, and asks it without ever producing a token: the keychain
// is queried for the item's attributes rather than its password, and the
// environment file is checked for existence and mode rather than read.
func (d *diagnosis) checkSlackSecrets(ctx context.Context, productID domain.ProductID) Finding {
	bot, app := slack.BotSecret(productID), slack.AppSecret(productID)

	if file, found := d.namespacedEnvFile(productID); found {
		if info, err := os.Stat(file); err == nil && info.Mode().Perm()&0o077 != 0 {
			return Finding{
				Check:   "slack-secrets",
				Status:  StatusWarning,
				Summary: "this project's Slack secrets are readable by other users on this machine",
				Detail:  fmt.Sprintf("%s is mode %04o", file, info.Mode().Perm()),
				Remedy:  fmt.Sprintf("chmod 600 %s", shellQuote(file)),
			}
		}
		return Finding{
			Check:   "slack-secrets",
			Status:  StatusOK,
			Summary: fmt.Sprintf("this project's Slack secrets are stored for %s", productID),
			Detail:  file,
		}
	}

	if d.env.GOOS == "darwin" {
		missing := d.missingKeychainSecrets(ctx, bot, app)
		if len(missing) == 0 {
			return Finding{
				Check:   "slack-secrets",
				Status:  StatusOK,
				Summary: fmt.Sprintf("this project's Slack secrets are in the keychain as %s and %s", bot, app),
			}
		}
		return Finding{
			Check:   "slack-secrets",
			Status:  StatusWarning,
			Summary: fmt.Sprintf("this project's Slack secrets are not stored: %s", strings.Join(missing, " and ")),
			// The names carry the product deliberately. A generic pair on a
			// machine running several harnesses is a pair that passes this check
			// for every project and is right for at most one of them.
			Detail: fmt.Sprintf("the sink for %s reads its tokens from names that carry the product, so a sibling project's pair cannot stand in for them", productID),
			Remedy: keychainAddCommand(missing),
		}
	}

	directory, file := d.envFileRemedyPaths(productID)
	return Finding{
		Check:   "slack-secrets",
		Status:  StatusWarning,
		Summary: fmt.Sprintf("this project's Slack secrets are not stored for %s", productID),
		Detail:  fmt.Sprintf("no %s, and this platform has no keychain to hold %s and %s", file, bot, app),
		Remedy:  fmt.Sprintf("mkdir -p %s && install -m 600 /dev/null %s && ${EDITOR:-vi} %s", directory, file, file),
	}
}

// checkSlackSink asks what is actually reporting for this product. The lease
// answers whether anything is; the record the sink left answers what it is.
func (d *diagnosis) checkSlackSink(resolved config.Resolved, productID domain.ProductID, installed string) []Finding {
	root, err := runstate.DefaultRoot(d.getenv, d.homeDir, d.env.GOOS)
	if err != nil {
		return []Finding{{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "the sink's state could not be found, so nothing here can say whether one is running",
			Detail:  err.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}}
	}
	store, err := slack.NewStore(root, productID)
	if err != nil {
		return []Finding{{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "the sink's state could not be opened, so nothing here can say whether one is running",
			Detail:  err.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}}
	}

	presence, recorded, presenceErr := store.LoadPresence()
	running, err := d.sinkIsRunning(store)
	if err != nil {
		return []Finding{{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "the sink's lease could not be read, so nothing here can say whether one is running",
			Detail:  err.Error(),
			Remedy:  fmt.Sprintf("ls -l %s", shellQuote(filepath.Join(store.Root(), ".sink.lock"))),
		}}
	}

	start := d.startCommand(productID)
	if !running {
		finding := Finding{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "no sink is running for this product, so nothing is being reported",
			Remedy:  start,
		}
		if recorded {
			// A record with no process behind it is the ordinary shape of the
			// outage: something stopped and nobody was told, because a stopped
			// sink and a quiet week look the same in a channel.
			finding.Detail = fmt.Sprintf("the last one recorded here was %s as pid %d, started %s",
				describeVersion(presence.Version), presence.PID, d.describeAge(presence.StartedAt))
		}
		return []Finding{finding}
	}

	// A sink that is running and said nothing about itself still has to be
	// stoppable, or this is a state that can be described and not routed out of:
	// a second sink is refused while the first holds the lease, so "start one"
	// is not a remedy on its own, and with no recorded pid the process has to be
	// found through the lease it is holding.
	unidentified := d.restartUnidentifiedCommand(store, productID)
	if presenceErr != nil {
		return []Finding{{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "a sink is running for this product and its record could not be read",
			Detail:  presenceErr.Error(),
			Remedy:  unidentified,
		}}
	}
	if !recorded {
		return []Finding{{
			Check:   "slack-sink",
			Status:  StatusWarning,
			Summary: "a sink is running for this product and recorded nothing about itself",
			Detail:  "it predates the record, so which build it is and whose secrets it holds are both unknown",
			Remedy:  unidentified,
		}}
	}

	findings := []Finding{{
		Check:   "slack-sink",
		Status:  StatusOK,
		Summary: fmt.Sprintf("a sink is reporting for this product into %s", presence.Channel),
		Detail: strings.TrimSpace(fmt.Sprintf("%s as pid %d, started %s%s",
			describeVersion(presence.Version), presence.PID, d.describeAge(presence.StartedAt), describeWorkspace(presence))),
	}}
	findings = append(findings, d.checkSinkBuild(presence, installed, productID))
	findings = append(findings, d.checkSinkSecrets(presence, productID))
	findings = append(findings, d.checkSinkChannel(presence, resolved, productID)...)
	if configured := strings.TrimSpace(presence.Config); configured != "" && configured != resolved.Path {
		findings = append(findings, Finding{
			Check:   "slack-sink-config",
			Status:  StatusWarning,
			Summary: "the running sink loaded a different configuration from the one being diagnosed",
			Detail:  fmt.Sprintf("the sink read %s; this diagnosis read %s", configured, resolved.Path),
			Remedy:  d.restartCommand(presence, productID),
		})
	}
	return findings
}

// checkSinkChannel is the config-edit case, and it is first-class rather than
// exotic: `slack.channel` is one line in a file anybody can edit on a working
// install, and editing it changes nothing about the process that is already
// running. The sink keeps posting into the channel it connected to, the channel
// the project now names stays empty, and neither of those is an error anything
// reports. Comparing the two is the only way that surfaces at all -- the
// configuration path does not, because an edit in place leaves the path exactly
// as it was.
func (d *diagnosis) checkSinkChannel(presence slack.Presence, resolved config.Resolved, productID domain.ProductID) []Finding {
	running := strings.TrimSpace(presence.Channel)
	configured := strings.TrimSpace(resolved.Config.Slack.Channel)
	if running == "" || configured == "" || running == configured {
		return nil
	}
	return []Finding{{
		Check:   "slack-sink-channel",
		Status:  StatusWarning,
		Summary: fmt.Sprintf("the running sink posts into %s, and this project now reports into %s", running, configured),
		// Both halves of the damage are worth saying. The threads already open in
		// the old channel are not moved by a restart -- a thread timestamp only
		// means anything inside the channel it was posted in -- so the operator
		// is deciding about two channels rather than about one process.
		Detail: fmt.Sprintf("%s has been silent since the channel was changed in %s, and the threads already open in %s stay where they are",
			configured, resolved.Path, running),
		Remedy: d.restartCommand(presence, productID),
	}}
}

// checkSinkBuild is the check the stale-sink outage turns on. A sink is a
// long-lived process started from a binary that keeps moving underneath it, so
// the build that is reporting and the build that is installed drift apart with
// no event between them: nothing fails, nothing is logged, and the milestones
// added since it started are simply never posted.
//
// The version is asked first because it is what an operator installs by, and the
// revision behind it is asked second because on the installation this matters
// most for the version cannot answer at all. A harness developing itself runs
// unreleased binaries, so a sink started last week and the binary diagnosing it
// both answer "dev" and compare as identical — a clean report over exactly the
// drift being looked for. Two revisions are two places in one history, so where
// the versions agree and the revisions do not, the revisions settle it.
func (d *diagnosis) checkSinkBuild(presence slack.Presence, installed string, productID domain.ProductID) Finding {
	running := strings.TrimSpace(presence.Version)
	current := strings.TrimSpace(installed)
	runningBuild := strings.TrimSpace(presence.Build)
	currentBuild := strings.TrimSpace(d.env.Build)
	switch {
	case running == "":
		return Finding{
			Check:   "slack-sink-version",
			Status:  StatusWarning,
			Summary: "the running sink did not record which build it is",
			Remedy:  d.restartCommand(presence, productID),
		}
	case current != "" && running != current:
		return Finding{
			Check:   "slack-sink-version",
			Status:  StatusWarning,
			Summary: "the running sink is an older build than the installed one, and reports only what that build knew how to report",
			Detail:  fmt.Sprintf("running: %s; installed: %s", running, current),
			Remedy:  d.restartCommand(presence, productID),
		}
	case runningBuild != "" && currentBuild != "" && runningBuild != currentBuild:
		// One version, two revisions: the sink is not the binary asking about it,
		// however alike the two of them are named. This is the whole of the
		// self-hosted case, and it is a warning rather than a note because what a
		// sink reports is decided entirely by the code it is running.
		return Finding{
			Check:   "slack-sink-version",
			Status:  StatusWarning,
			Summary: "the running sink and this build report one version and are two different revisions",
			Detail: fmt.Sprintf("running: %s built at %s; this build: %s built at %s",
				running, shortRevision(runningBuild), running, shortRevision(currentBuild)),
			Remedy: d.restartCommand(presence, productID),
		}
	case runningBuild == "" || currentBuild == "":
		// The versions agree and there is no pair of revisions to check that by.
		// It is reported as what it is rather than as agreement: on an install
		// where every build answers one version, a version that matches is not
		// evidence of anything, and saying so is what keeps somebody from reading
		// this line as the comparison having been made.
		return Finding{
			Check:   "slack-sink-version",
			Status:  StatusOK,
			Summary: fmt.Sprintf("the running sink reports the installed version, %s, and there is no pair of revisions to check that by", running),
			Detail:  fmt.Sprintf("running: %s; this build: %s", describeRevision(runningBuild), describeRevision(currentBuild)),
		}
	}
	return Finding{
		Check:   "slack-sink-version",
		Status:  StatusOK,
		Summary: fmt.Sprintf("the running sink is the installed build, %s, built at %s", running, shortRevision(runningBuild)),
	}
}

// describeRevision names a revision, or says an absence is one. A blank where a
// revision would be reads as a revision nobody looked up rather than as a binary
// that carries none.
func describeRevision(build string) string {
	if build == "" {
		return "no recorded revision"
	}
	return shortRevision(build)
}

// shortRevision names a revision the way somebody quoting one does.
func shortRevision(build string) string {
	if len(build) <= 12 {
		return build
	}
	return build[:12]
}

// checkSinkSecrets asks whether the sink that is running is running against this
// project's secrets. "A Slack token exists" is the check that passes wrongly:
// with several harnesses on one machine, a sink started from a shell that had a
// sibling project's pair exported connects, authenticates, and posts this
// project's work into that project's workspace.
func (d *diagnosis) checkSinkSecrets(presence slack.Presence, productID domain.ProductID) Finding {
	namespace := strings.TrimSpace(presence.SecretNamespace)
	switch {
	case namespace == string(productID):
		return Finding{
			Check:   "slack-sink-secrets",
			Status:  StatusOK,
			Summary: fmt.Sprintf("the running sink was launched with %s's own secrets", productID),
		}
	case namespace == "":
		return Finding{
			Check:   "slack-sink-secrets",
			Status:  StatusWarning,
			Summary: "the running sink was started from a shell rather than from this project's launcher, so whose tokens it holds is not recorded",
			Detail: fmt.Sprintf("it would record %s=%s if it had been launched with this project's secrets%s",
				slack.SecretNamespaceVariable, productID, describeWorkspace(presence)),
			Remedy: d.restartCommand(presence, productID),
		}
	}
	return Finding{
		Check:   "slack-sink-secrets",
		Status:  StatusWarning,
		Summary: fmt.Sprintf("the running sink holds %s's secrets, not %s's", namespace, productID),
		Detail: fmt.Sprintf("it is reporting %s's work through another project's Slack app%s",
			productID, describeWorkspace(presence)),
		Remedy: d.restartCommand(presence, productID),
	}
}

// sinkIsRunning finds out whether anybody holds this product's sink lease, by
// trying to take it and letting it go again. Taking a lease is how the question
// is asked -- the lock is advisory and the operating system drops it when its
// holder dies, so an answer of "nobody" is an answer about processes rather than
// about files somebody forgot to clean up.
func (d *diagnosis) sinkIsRunning(store *slack.Store) (bool, error) {
	lease, held, err := store.Lease()
	if err != nil {
		return false, err
	}
	if !held {
		return true, nil
	}
	// Held for as long as it took to find out it was free, which is the whole of
	// what this diagnosis does with it. A sink starting a moment later takes it
	// as it always would.
	if err := lease.Release(); err != nil {
		return false, err
	}
	return false, nil
}

// missingKeychainSecrets names which of the two namespaced items the keychain
// does not have. The query deliberately asks for the item rather than for its
// password: `-w` would print the token, and this is a diagnostic whose output
// goes to a terminal, a scrollback, and whatever collects them.
func (d *diagnosis) missingKeychainSecrets(ctx context.Context, names ...string) []string {
	var missing []string
	for _, name := range names {
		result, err := d.run(ctx, "", "security", "find-generic-password", "-s", name, "-a", keychainAccount)
		if err != nil || result.Status != execution.ProcessSucceeded {
			missing = append(missing, name)
		}
	}
	return missing
}

// namespacedEnvFile is the other place this project's secrets are kept: a file
// only the sink's own launch reads, named for the product for the same reason
// the keychain items are.
func (d *diagnosis) namespacedEnvFile(productID domain.ProductID) (string, bool) {
	path, err := d.namespacedEnvFilePath(productID)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return path, false
	}
	return path, true
}

// namespacedEnvFilePath is one path and deliberately not two. Honoring
// XDG_CONFIG_HOME as well would mean an operator who followed the documented
// path exactly is told their secrets are not stored, by a check reading a
// directory the document never named -- and the failure would only appear on
// the machines that set it. The documented path is the checked path.
func (d *diagnosis) namespacedEnvFilePath(productID domain.ProductID) (string, error) {
	home, err := d.homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "yoyo", string(productID), "slack.env"), nil
}

// startCommand is how this project's sink is started with this project's
// secrets: the launcher, written out. It is long because it is the whole of the
// arrangement -- the tokens are read into exactly one process's environment and
// the namespace says which project they were read for -- and an operator who
// pastes it gets a correct sink rather than an approximation of one.
func (d *diagnosis) startCommand(productID domain.ProductID) string {
	if d.env.GOOS == "darwin" {
		if _, found := d.namespacedEnvFile(productID); !found {
			return fmt.Sprintf(
				`SLACK_BOT_TOKEN="$(security find-generic-password -s %s -a %s -w)" SLACK_APP_TOKEN="$(security find-generic-password -s %s -a %s -w)" %s=%s yoyo slack`,
				slack.BotSecret(productID), keychainAccount, slack.AppSecret(productID), keychainAccount,
				slack.SecretNamespaceVariable, productID)
		}
	}
	_, file := d.envFileRemedyPaths(productID)
	return fmt.Sprintf("(set -a; . %s; %s=%s exec yoyo slack)", file, slack.SecretNamespaceVariable, productID)
}

// envFileRemedyPaths spells the environment file the way a remedy has to. Home
// is resolved where this machine will say what it is, and left to the shell to
// expand where it will not: a command the operator can still run beats a path
// nobody can act on, which is what an unresolved home would otherwise produce.
func (d *diagnosis) envFileRemedyPaths(productID domain.ProductID) (directory, file string) {
	if path, err := d.namespacedEnvFilePath(productID); err == nil {
		return shellQuote(filepath.Dir(path)), shellQuote(path)
	}
	base := `"$HOME/.config/yoyo/` + string(productID)
	return base + `"`, base + `/slack.env"`
}

// restartCommand stops the sink that is running and starts the right one. It
// names the process by the pid the sink recorded rather than by matching a
// command line, because on a machine running several harnesses a pattern that
// matches this sink matches the sibling project's too.
func (d *diagnosis) restartCommand(presence slack.Presence, productID domain.ProductID) string {
	start := d.startCommand(productID)
	if presence.PID <= 0 {
		return start
	}
	return fmt.Sprintf("kill %d && %s", presence.PID, start)
}

// restartUnidentifiedCommand restarts a sink that did not say which process it
// is. The lease is how it is found: the holder has that file open, so the
// operating system can name it even though the sink could not. It is still this
// product's sink and nobody else's, because the lease is per product -- which is
// what a `pkill` over a command line would not be on a machine running more than
// one harness.
func (d *diagnosis) restartUnidentifiedCommand(store *slack.Store, productID domain.ProductID) string {
	lease := shellQuote(filepath.Join(store.Root(), ".sink.lock"))
	return fmt.Sprintf("kill $(lsof -t %s) && %s", lease, d.startCommand(productID))
}

// keychainAddCommand writes the store commands for whichever items are missing.
// `-w` with no value makes the keychain prompt for the token, so the token never
// reaches a shell history.
func keychainAddCommand(missing []string) string {
	commands := make([]string, 0, len(missing))
	for _, name := range missing {
		commands = append(commands, fmt.Sprintf("security add-generic-password -s %s -a %s -w", name, keychainAccount))
	}
	return strings.Join(commands, " && ")
}

// describeVersion renders a build for a sentence, including the case where the
// sink recorded none.
func describeVersion(version string) string {
	if strings.TrimSpace(version) == "" {
		return "a sink of unrecorded build"
	}
	return "build " + version
}

// describeWorkspace names the workspace a sink authenticated into, when the
// workspace told it. It is the one fact about a sink's tokens that came from the
// tokens rather than from what somebody said about them, which is exactly what
// makes it worth putting beside a claim about whose secrets are in use.
func describeWorkspace(presence slack.Presence) string {
	if strings.TrimSpace(presence.Team) == "" {
		return ""
	}
	return fmt.Sprintf("; it authenticated into workspace %s", presence.Team)
}

// describeAge reads a recorded time back as how long ago it was, which is the
// form the question is asked in.
func (d *diagnosis) describeAge(at time.Time) string {
	if at.IsZero() {
		return "at an unrecorded time"
	}
	elapsed := d.env.Now().Sub(at)
	if elapsed < time.Minute {
		return "just now"
	}
	return fmt.Sprintf("%s ago", elapsed.Round(time.Minute))
}
