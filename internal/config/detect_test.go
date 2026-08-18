package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The confident cases: a project that says plainly what it is gets commands it
// can run, each traceable to the file it was read out of.
func TestDetectChecksProposesFromWhatAProjectDeclares(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		files map[string]string
		want  []CheckProposal
	}{
		{
			name:  "a Go module",
			files: map[string]string{"go.mod": "module example\n\ngo 1.24\n"},
			want: []CheckProposal{
				{Command: "go test ./...", Source: "go.mod"},
				{Command: "go vet ./...", Source: "go.mod"},
			},
		},
		{
			name:  "a Makefile with a check target",
			files: map[string]string{"Makefile": "GO ?= go\nLDFLAGS := -X main.version=1\n\n.PHONY: test check\n\ntest:\n\t$(GO) test ./...\n\ncheck: test\n"},
			want:  []CheckProposal{{Command: "make check", Source: `Makefile (its "check" target)`}},
		},
		{
			name:  "a Makefile with only a test target",
			files: map[string]string{"GNUmakefile": "test:\n\techo hi\n"},
			want:  []CheckProposal{{Command: "make test", Source: `GNUmakefile (its "test" target)`}},
		},
		{
			name: "a Node project with one lockfile and a test script",
			files: map[string]string{
				"package.json":      `{"name":"example","scripts":{"test":"vitest run"}}`,
				"package-lock.json": "{}",
			},
			want: []CheckProposal{
				{Command: "npm ci", Source: "package-lock.json"},
				{Command: "npm test", Source: "package.json"},
			},
		},
		{
			name: "a TypeScript project",
			files: map[string]string{
				"package.json":   `{"scripts":{"test":"vitest run"}}`,
				"pnpm-lock.yaml": "lockfileVersion: 9.0\n",
				"tsconfig.json":  "{}",
			},
			want: []CheckProposal{
				{Command: "pnpm install --frozen-lockfile", Source: "pnpm-lock.yaml"},
				{Command: "pnpm test", Source: "package.json"},
				{Command: "pnpm exec tsc --noEmit", Source: "tsconfig.json"},
			},
		},
		{
			name:  "a Python project that configures pytest",
			files: map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = \"-q\"\n"},
			want:  []CheckProposal{{Command: "python3 -m pytest -q", Source: "pyproject.toml"}},
		},
		{
			name:  "a Maven project",
			files: map[string]string{"pom.xml": "<project/>\n"},
			want:  []CheckProposal{{Command: "mvn --batch-mode --quiet verify", Source: "pom.xml"}},
		},
		{
			name:  "a Gradle project with a wrapper",
			files: map[string]string{"gradlew": "#!/bin/sh\n", "build.gradle": "plugins {}\n"},
			want:  []CheckProposal{{Command: "./gradlew --no-daemon check", Source: "gradlew"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			detection := DetectChecks(writeDetectableProject(t, test.files))
			if !reflect.DeepEqual(detection.Checks, test.want) {
				t.Errorf("checks = %+v, want %+v", detection.Checks, test.want)
			}
			if len(detection.Candidates) != 0 || len(detection.Alternatives) != 0 {
				t.Errorf("candidates = %+v, alternatives = %+v, want none for a project that says what it is",
					detection.Candidates, detection.Alternatives)
			}
		})
	}
}

// The ambiguous cases: detection found a toolchain and could not tell which
// command is the gate, so it proposes nothing and says why.
func TestDetectChecksLeavesWhatItCannotDecideAsCandidates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		files      map[string]string
		candidates []string
		reason     string
	}{
		{
			name:       "Python tests with nothing naming the runner",
			files:      map[string]string{"tests/test_calc.py": "import unittest\n"},
			candidates: []string{"python3 -m pytest -q", "python3 -m unittest discover -q -s tests -t ."},
			reason:     "nothing here names the test runner",
		},
		{
			name:       "a Node project with no lockfile",
			files:      map[string]string{"package.json": `{"scripts":{"test":"vitest run"}}`},
			candidates: []string{"npm ci", "npm test"},
			reason:     "no lockfile says which package manager installs this project",
		},
		{
			name: "a Node project with two lockfiles",
			files: map[string]string{
				"package.json":      `{"scripts":{"test":"vitest run"}}`,
				"package-lock.json": "{}",
				"yarn.lock":         "",
			},
			candidates: []string{"npm ci", "npm test"},
			reason:     "more than one lockfile is present",
		},
		{
			name: "a Node project whose test script only exists to fail",
			files: map[string]string{
				"package.json":      `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`,
				"package-lock.json": "{}",
			},
			candidates: []string{"npm ci", "npm test"},
			reason:     "package.json declares no test script",
		},
		{
			name:       "a Gradle project with no wrapper",
			files:      map[string]string{"build.gradle.kts": "plugins {}\n"},
			candidates: []string{"gradle --no-daemon check"},
			reason:     "there is no ./gradlew wrapper",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			detection := DetectChecks(writeDetectableProject(t, test.files))
			if len(detection.Checks) != 0 {
				t.Errorf("checks = %+v, want nothing written for an undecidable toolchain", detection.Checks)
			}
			if len(detection.Alternatives) != 0 {
				t.Errorf("alternatives = %+v, want nothing displaced where nothing was written", detection.Alternatives)
			}
			var commands []string
			for _, candidate := range detection.Candidates {
				commands = append(commands, candidate.Command)
				if candidate.Reason == "" {
					t.Errorf("candidate %q does not say why it could not be chosen", candidate.Command)
				}
			}
			if !reflect.DeepEqual(commands, test.candidates) {
				t.Errorf("candidates = %v, want %v", commands, test.candidates)
			}
			if len(detection.Candidates) > 0 && !strings.Contains(detection.Candidates[0].Reason, test.reason) {
				t.Errorf("reason = %q, want it to say %q", detection.Candidates[0].Reason, test.reason)
			}
		})
	}
}

// A Makefile is the project naming its own entry point. Running the suite twice
// is not a stronger gate, so the language-native commands are offered rather
// than added -- as alternatives rather than candidates, because a decision that
// has been made is not one the operator owes.
func TestDetectChecksLetsAMakefileSupersedeTheLanguageCommands(t *testing.T) {
	t.Parallel()

	detection := DetectChecks(writeDetectableProject(t, map[string]string{
		"Makefile": "check:\n\tgo test ./...\n",
		"go.mod":   "module example\n",
	}))
	if got := detection.Commands(); len(got) != 1 || got[0] != "make check" {
		t.Fatalf("checks = %v, want only the Makefile entry point", got)
	}
	var superseded []string
	for _, alternative := range detection.Alternatives {
		superseded = append(superseded, alternative.Command)
		if !strings.Contains(alternative.Reason, "Makefile") {
			t.Errorf("alternative %q does not say what displaced it", alternative.Command)
		}
	}
	if !reflect.DeepEqual(superseded, []string{"go test ./...", "go vet ./..."}) {
		t.Errorf("alternatives = %v, want the Go commands offered rather than dropped", superseded)
	}
	if len(detection.Candidates) != 0 {
		t.Errorf("candidates = %+v, want nothing awaiting a decision that init already made", detection.Candidates)
	}
}

// A repository that announces nothing gets nothing, which is what keeps the
// generated placeholder honest.
func TestDetectChecksProposesNothingForAProjectThatSaysNothing(t *testing.T) {
	t.Parallel()

	detection := DetectChecks(writeDetectableProject(t, map[string]string{
		"README.md":  "# example\n",
		"Makefile":   "build:\n\tcc -o example example.c\n",
		"example.c":  "int main(void) { return 0; }\n",
		"notes/a.py": "x = 1\n",
	}))
	if !detection.Empty() {
		t.Errorf("detection = %+v, want nothing proposed", detection)
	}
}

// Detection reads; it does not run. A project whose build would leave a trace is
// the only way to check that from outside, so one is given a target that would.
func TestDetectChecksExecutesNothing(t *testing.T) {
	t.Parallel()

	root := writeDetectableProject(t, map[string]string{
		"Makefile":     "check:\n\ttouch sentinel-make\n",
		"package.json": `{"scripts":{"preinstall":"touch sentinel-npm","test":"touch sentinel-test"}}`,
		"go.mod":       "module example\n",
	})
	DetectChecks(root)
	for _, sentinel := range []string{"sentinel-make", "sentinel-npm", "sentinel-test"} {
		if _, err := os.Stat(filepath.Join(root, sentinel)); !os.IsNotExist(err) {
			t.Errorf("detection ran something: %s exists", sentinel)
		}
	}
}

// writeDetectableProject lays out a throwaway repository from a path-to-contents map.
func writeDetectableProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return root
}
