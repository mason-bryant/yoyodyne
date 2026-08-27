package composition

// The gates the ledger's Exercised sentences refer to. Each one is here rather
// than inline in a test because what it holds is a claim about this repository
// that somebody may want to run over another set of files — and because a gate
// written as a test body is one no fixture ever proves works.
//
// None of them is deep. A shell file that parses can still be wrong and a
// workflow with a job and a step can still do the wrong thing. What they buy is
// that a change made entirely of shell, YAML, or JSON cannot reach a reviewer
// with nothing having read it, which is the state yoyodyne-ifd.156 was
// integrated in.

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ShellSyntax parses each file named and reports the ones a shell will not
// accept.
//
// `bash -n` reads a script and does not run it, so this is safe against files
// whose whole purpose is to do something — the release verb tags a repository
// and the adoption walkthrough builds one. It is deliberately the only thing
// asked of shell that has no suite of its own: parsing is what nothing else in
// this project did, and a syntax error in a script only a release path executes
// would otherwise be found by the release.
func ShellSyntax(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		parse := exec.Command("bash", "-n", filepath.FromSlash(path))
		parse.Dir = root
		report, err := parse.CombinedOutput()
		if err == nil {
			continue
		}
		problems = append(problems, Problem{
			Path:   path,
			Reason: fmt.Sprintf("a shell will not parse it (%v): %s", err, strings.TrimSpace(string(report))),
		})
	}
	return problems
}

// StructuredData decodes each YAML and JSON file named and reports the ones
// that do not decode.
//
// What a file means is the business of whatever reads it, and most of these are
// read by somebody else's tool. What is this repository's business either way is
// that the file is the thing it claims to be: a configuration nothing can parse
// is broken whoever owns its schema, and it is broken in a way no reviewer
// reading a diff reliably sees.
func StructuredData(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it could not be read: %v", err)})
			continue
		}
		var document any
		if Extension(path) == ".json" {
			err = json.Unmarshal(content, &document)
		} else {
			err = yaml.Unmarshal(content, &document)
		}
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it does not decode: %v", err)})
		}
	}
	return problems
}

// WorkflowDirectory is where a repository's GitHub Actions workflows live.
const WorkflowDirectory = ".github/workflows/"

// Workflows is the workflow files among the paths named.
func Workflows(paths []string) []string {
	var workflows []string
	for _, path := range paths {
		if strings.HasPrefix(path, WorkflowDirectory) {
			workflows = append(workflows, path)
		}
	}
	return workflows
}

// workflowJob is the part of a job that has to be there for the job to do
// anything: something to run it on, and something to run.
type workflowJob struct {
	RunsOn any `yaml:"runs-on"`
	Steps  any `yaml:"steps"`
}

// steps is how many steps the job declares. It is read back out of `any` rather
// than decoded into a typed slice because the claim is only that there are
// some, and a `steps:` written as anything other than a list is a job with none
// of them as surely as one that leaves the key out.
func (j workflowJob) steps() int {
	declared, list := j.Steps.([]any)
	if !list {
		return 0
	}
	return len(declared)
}

// workflowFile is as much of a workflow's shape as is worth holding: enough
// that a file which is valid YAML and not a workflow fails.
type workflowFile struct {
	// A workflow's trigger key is `on`, which YAML 1.2 reads as that string and
	// YAML 1.1 reads as the boolean true. The file is read by parsers of both
	// vintages, so both spellings are the same key and a workflow carrying
	// either has a trigger.
	On   any `yaml:"on"`
	True any `yaml:"true"`

	Jobs map[string]workflowJob `yaml:"jobs"`
}

// WorkflowShape holds each workflow to the shape a workflow needs.
//
// Decoding alone is not enough for these: workflow YAML is only ever executed by
// GitHub, and the release workflow's trigger is a tag push, so a mistyped
// top-level key there is valid YAML that first misbehaves during a real
// publication. A trigger, at least one job, and a runner and steps in each is
// the part of "this is a workflow" that can be checked without GitHub.
func WorkflowShape(root string, paths []string) []Problem {
	var problems []Problem
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it could not be read: %v", err)})
			continue
		}
		var workflow workflowFile
		if err := yaml.Unmarshal(content, &workflow); err != nil {
			problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("it does not decode as a workflow: %v", err)})
			continue
		}
		if workflow.On == nil && workflow.True == nil {
			problems = append(problems, Problem{Path: path, Reason: "it declares no `on:` trigger, so nothing ever runs it"})
		}
		if len(workflow.Jobs) == 0 {
			problems = append(problems, Problem{Path: path, Reason: "it declares no jobs, so a trigger it does have has nothing to do"})
		}
		// Sorted, because a map ranges in a different order every run and what a
		// gate reports has to be the same twice over one checkout.
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if job.RunsOn == nil {
				problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("its %q job names no `runs-on:`, so there is nothing to run it on", name)})
			}
			if job.steps() == 0 {
				problems = append(problems, Problem{Path: path, Reason: fmt.Sprintf("its %q job has no steps", name)})
			}
		}
	}
	return problems
}
