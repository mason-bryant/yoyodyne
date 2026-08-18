package config

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// CheckProposal is one shell command derived from a file the project already
// has, together with the artifact it was derived from. The artifact travels with
// the command because the generated configuration says where each proposal came
// from: a command an operator cannot trace back to something in their own
// repository is a guess, and a guess is the thing this is not.
type CheckProposal struct {
	Command string `json:"command"`
	// Source is the repository-relative artifact the command was read out of.
	Source string `json:"source"`
	// Reason is why a command was not proposed outright. It is empty on a
	// confident proposal, which needs no excuse.
	Reason string `json:"reason,omitempty"`
}

// Detection is what reading a project's own files proposed, in three kinds that
// ask three different things of the operator. Checks are written into the
// configuration. Candidates are written beside them, commented out, because
// detection could not tell which command is the gate — those are a decision
// somebody still owes. Alternatives are commands detection read and decided
// against, because what it did write already covers them; nothing is owed there,
// and they are offered only so the operator can swap one in.
type Detection struct {
	Checks       []CheckProposal `json:"checks"`
	Candidates   []CheckProposal `json:"candidates"`
	Alternatives []CheckProposal `json:"alternatives"`
}

// Commands is the check list a detection proposes, in the order it proposed it.
func (d Detection) Commands() []string {
	commands := make([]string, 0, len(d.Checks))
	for _, proposal := range d.Checks {
		commands = append(commands, proposal.Command)
	}
	return commands
}

// Empty reports whether detection found nothing at all, which is the case a
// generated configuration handles by leaving its placeholder in place.
func (d Detection) Empty() bool {
	return len(d.Checks) == 0 && len(d.Candidates) == 0 && len(d.Alternatives) == 0
}

// DetectChecks proposes checks by reading what a project already declares about
// its own toolchain: its Makefile targets, its module and manifest files, its
// lockfiles, its build wrappers.
//
// Nothing is executed. Detection is by artifact presence and by reading those
// artifacts, because running a stranger's build to discover what it is is not a
// first impression worth making, and because a command that has to run to be
// proposed is a command that runs before anybody has reviewed it.
//
// What this produces is a proposal rather than knowledge of a toolchain. The
// harness's only contact with a project's tools is still the list of shell
// commands the configuration declares and the exit codes they return; these are
// convenience defaults derived from the project's own files, which an operator
// reads, edits, or deletes before any of them runs.
func DetectChecks(root string) Detection {
	// Every list is empty rather than absent, so a caller reading the reported
	// JSON iterates three lists in every case instead of three or null.
	detection := Detection{
		Checks:       []CheckProposal{},
		Candidates:   []CheckProposal{},
		Alternatives: []CheckProposal{},
	}
	// A Makefile is the project naming its own entry point, so it is read first
	// and the language-native commands defer to it below.
	makeChecks, makeCandidates := detectMake(root)
	detection.Checks = append(detection.Checks, makeChecks...)
	detection.Candidates = append(detection.Candidates, makeCandidates...)

	for _, detect := range []func(string) ([]CheckProposal, []CheckProposal){
		detectGo,
		detectNode,
		detectPython,
		detectMaven,
		detectGradle,
	} {
		checks, candidates := detect(root)
		if len(makeChecks) > 0 {
			// Two gates running the same suite is the suite run twice, so what the
			// Makefile supersedes becomes an alternative rather than an addition.
			// It is not a candidate: nothing about it is undecided, and asking the
			// operator to choose between a decision already made and its own
			// runner-up is asking for a decision nobody owes.
			detection.Alternatives = append(detection.Alternatives,
				restate(checks, "the Makefile above already names this project's entry point")...)
			checks = nil
		}
		detection.Checks = append(detection.Checks, checks...)
		detection.Candidates = append(detection.Candidates, candidates...)
	}
	return detection
}

// ProposalSources names the artifacts a set of proposals was derived from, once
// each and in the order they were proposed.
func ProposalSources(proposals []CheckProposal) []string {
	sources := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		if !slices.Contains(sources, proposal.Source) {
			sources = append(sources, proposal.Source)
		}
	}
	return sources
}

// restate copies proposals with a reason attached, keeping their provenance. It
// is how a proposal moves out of the written list, whether because nothing here
// could decide it or because something else already did.
func restate(proposals []CheckProposal, reason string) []CheckProposal {
	restated := make([]CheckProposal, 0, len(proposals))
	for _, proposal := range proposals {
		proposal.Reason = reason
		restated = append(restated, proposal)
	}
	return restated
}

// makefileNames are the names make itself looks for, in make's own order, so a
// project with two of them is read the way it will be built.
var makefileNames = []string{"GNUmakefile", "makefile", "Makefile"}

// makefileTarget matches a target definition at the start of a line. A variable
// assignment is excluded by refusing an "=" straight after the colon, and a
// special target such as .PHONY by requiring the name to start with a letter,
// digit, or underscore.
var makefileTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_][A-Za-z0-9_./+-]*)[ \t]*:{1,2}(?:[^=]|$)`)

// detectMake proposes the Makefile target a project would tell a newcomer to
// run. "check" is the GNU convention for the full gate and wins where both
// exist; "test" is taken where it is the only one.
func detectMake(root string) ([]CheckProposal, []CheckProposal) {
	name, contents, found := readMakefile(root)
	if !found {
		return nil, nil
	}
	targets := map[string]bool{}
	for _, match := range makefileTarget.FindAllStringSubmatch(string(contents), -1) {
		targets[match[1]] = true
	}
	for _, target := range []string{"check", "test"} {
		if targets[target] {
			return []CheckProposal{{
				Command: "make " + target,
				Source:  name + ` (its "` + target + `" target)`,
			}}, nil
		}
	}
	return nil, nil
}

// readMakefile reads the makefile under the exact name it is stored as. A
// case-insensitive filesystem answers to "makefile" for a file called
// "Makefile", and provenance naming a file the operator does not have is worse
// than none.
func readMakefile(root string) (string, []byte, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", nil, false
	}
	stored := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			stored[entry.Name()] = true
		}
	}
	for _, name := range makefileNames {
		if !stored[name] {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		return name, contents, true
	}
	return "", nil, false
}

// detectGo proposes the two commands every Go module can run without anything
// being installed beyond the toolchain that builds it. Formatting is left out
// deliberately: a project that is not gofmt-clean today would meet Yoyodyne as a
// failing check it never asked for.
func detectGo(root string) ([]CheckProposal, []CheckProposal) {
	if !exists(root, "go.mod") {
		return nil, nil
	}
	return []CheckProposal{
		{Command: "go test ./...", Source: "go.mod"},
		{Command: "go vet ./...", Source: "go.mod"},
	}, nil
}

// nodeLockfiles maps a lockfile to the package manager that writes it. Which one
// is present is what says how this project installs, and a project with none or
// with several has not said.
var nodeLockfiles = []struct {
	file    string
	manager string
	install string
	exec    string
}{
	{file: "package-lock.json", manager: "npm", install: "npm ci", exec: "npx"},
	{file: "yarn.lock", manager: "yarn", install: "yarn install --frozen-lockfile", exec: "yarn"},
	{file: "pnpm-lock.yaml", manager: "pnpm", install: "pnpm install --frozen-lockfile", exec: "pnpm exec"},
}

// detectNode reads package.json for the scripts it declares and the lockfile
// beside it for how they are installed. A check runs in a fresh worktree with no
// node_modules in it, so the install is part of the proposal rather than an
// assumption about the machine.
func detectNode(root string) ([]CheckProposal, []CheckProposal) {
	contents, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil, nil
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		// A manifest that does not parse is a manifest this cannot read anything
		// out of, and inventing commands from its mere presence would be the guess
		// detection is meant to avoid.
		return nil, nil
	}

	var present []int
	for index, lockfile := range nodeLockfiles {
		if exists(root, lockfile.file) {
			present = append(present, index)
		}
	}

	// npm writes a "test" script that exists only to fail, and proposing it would
	// hand the operator a check that can never pass.
	script, declared := manifest.Scripts["test"]
	if strings.Contains(script, "no test specified") {
		declared = false
	}

	// With no lockfile, or with more than one, npm is the form the commands are
	// written in and the whole set becomes a candidate below. The install is
	// attributed to package.json in that case, because attributing it to a
	// lockfile that is not there would be provenance for something absent.
	manager, installSource := nodeLockfiles[0], "package.json"
	if len(present) == 1 {
		manager = nodeLockfiles[present[0]]
		installSource = manager.file
	}
	proposals := []CheckProposal{
		{Command: manager.install, Source: installSource},
		{Command: manager.manager + " test", Source: "package.json"},
	}
	if exists(root, "tsconfig.json") {
		proposals = append(proposals, CheckProposal{
			Command: manager.exec + " tsc --noEmit",
			Source:  "tsconfig.json",
		})
	}

	var reason string
	switch {
	case !declared:
		reason = "package.json declares no test script, so nothing here says how this project is tested"
	case len(present) == 0:
		reason = "no lockfile says which package manager installs this project"
	case len(present) > 1:
		reason = "more than one lockfile is present, so which package manager installs this project is unsettled"
	}
	if reason != "" {
		return nil, restate(proposals, reason)
	}
	return proposals, nil
}

// pytestSignals are the files that, mentioning pytest at all, settle which
// runner a Python project uses -- either by configuring it or by depending on
// it.
var pytestSignals = []string{"pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini"}

// detectPython proposes pytest where the project says it uses pytest, and offers
// both runners where only the tests themselves are evidence. The distinction
// matters more here than elsewhere: unittest discovery over pytest-style tests
// collects nothing and exits 0, which is a gate that passes everything.
func detectPython(root string) ([]CheckProposal, []CheckProposal) {
	for _, name := range pytestSignals {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !strings.Contains(string(contents), "pytest") {
			continue
		}
		return []CheckProposal{{Command: "python3 -m pytest -q", Source: name}}, nil
	}

	directory, file, found := pythonTestFile(root)
	if !found {
		return nil, nil
	}
	discover := "python3 -m unittest discover -q"
	if directory != "." {
		discover += " -s " + directory + " -t ."
	}
	const reason = "nothing here names the test runner, and unittest discovery over pytest-style tests collects nothing and passes"
	return nil, []CheckProposal{
		{Command: "python3 -m pytest -q", Source: file, Reason: reason},
		{Command: discover, Source: file, Reason: reason},
	}
}

// pythonTestFile finds where a project keeps tests named the way both runners
// look for them, preferring a conventional directory over the root, and names
// one of them so the proposal points at something the operator can open.
func pythonTestFile(root string) (string, string, bool) {
	for _, directory := range []string{"tests", "test", "."} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".py") {
				continue
			}
			if strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") {
				return directory, path.Join(strings.TrimPrefix(directory, "."), name), true
			}
		}
	}
	return "", "", false
}

func detectMaven(root string) ([]CheckProposal, []CheckProposal) {
	if !exists(root, "pom.xml") {
		return nil, nil
	}
	return []CheckProposal{{
		Command: "mvn --batch-mode --quiet verify",
		Source:  "pom.xml",
	}}, nil
}

// detectGradle proposes the wrapper where there is one, because the wrapper is
// what pins the Gradle a build expects. A build script without one leaves the
// version to whatever the machine has, which is not something to write into a
// gate unasked.
func detectGradle(root string) ([]CheckProposal, []CheckProposal) {
	if exists(root, "gradlew") {
		return []CheckProposal{{
			Command: "./gradlew --no-daemon check",
			Source:  "gradlew",
		}}, nil
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		if exists(root, name) {
			return nil, []CheckProposal{{
				Command: "gradle --no-daemon check",
				Source:  name,
				Reason:  "there is no ./gradlew wrapper, so the Gradle a check would run is whatever the machine has installed",
			}}
		}
	}
	return nil, nil
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}
