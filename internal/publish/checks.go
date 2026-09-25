package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	// maxReadCheckRuns bounds the check runs one reading takes for a head. The
	// forge pages at a hundred, and a head with more than that is a repository
	// whose checks are not what this reading is for.
	maxReadCheckRuns = 100
	// maxAnnotatedChecks bounds how many failing checks have their annotations
	// read, because each one is a call of its own. A request failing more than
	// this many checks is red whichever files they name.
	maxAnnotatedChecks = 10
	// maxAnnotationsRead bounds the annotations read for one failing check.
	maxAnnotationsRead = 50
)

// FailedCheck is one check the forge reports failing on a pull request's head,
// with the files its annotations name. The files are what says whose failure it
// is: a check whose annotations point only at files the change does not touch
// failed on something the change did not bring, which is what a head behind its
// base branch meets when the base has since been fixed.
//
// A check that annotates nothing names no files, and that is recorded as it is
// rather than guessed at: nothing here says whose failure it is.
type FailedCheck struct {
	Name  string
	Paths []string
}

// CheckReading is what the forge says about a pull request's checks at the
// moment it was asked: the head the checks ran on, the files the change
// touches, each check's standing, and how far the base branch has moved on
// without the head.
type CheckReading struct {
	HeadCommit string
	// Files are the files the pull request's change touches, as the forge lists
	// them.
	Files   []string
	Failing []FailedCheck
	// Pending names the checks that have not finished.
	Pending []string
	Passing int
	// BehindBy is how many commits the base branch carries that the head does
	// not. Zero is a head level with its base.
	BehindBy int
}

// failingConclusions are the forge's conclusions for a check that did not pass.
// A cancelled or timed-out check is failing here: a required check that ended
// either way holds the merge exactly as a failure does.
var failingConclusions = map[string]bool{
	"failure":         true,
	"timed_out":       true,
	"cancelled":       true,
	"action_required": true,
	"startup_failure": true,
}

// Checks reads a pull request's check state: the head it carries and the
// files its change touches, every check run on that head and how each ended,
// the files each failing check's annotations name, and how far base has moved
// ahead of the head.
//
// It is several reads rather than one, and any of them failing fails the
// reading: a caller deciding whether to withdraw a queued merge or replay a
// change is gating on this, and a reading with a hole in it must refuse rather
// than read as a head with nothing wrong.
func (g GitHub) Checks(ctx context.Context, number int, base string) (CheckReading, error) {
	if number <= 0 {
		return CheckReading{}, fmt.Errorf("pull request number %d is not a request", number)
	}
	if err := validateArgument("base branch", base); err != nil {
		return CheckReading{}, err
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return CheckReading{}, err
	}
	viewed, err := g.exec(ctx, append([]string{"pr", "view", strconv.Itoa(number)}, append(scope, "--json", "headRefOid,files")...)...)
	if err != nil {
		return CheckReading{}, fmt.Errorf("ask the forge about the head of pull request %d: %w", number, err)
	}
	if viewed.Status != execution.ProcessSucceeded {
		return CheckReading{}, fmt.Errorf("ask the forge about the head of pull request %d: exit code %d: %s",
			number, viewed.ExitCode, g.redact(strings.TrimSpace(viewed.Stderr)))
	}
	var request struct {
		HeadRefOid string `json:"headRefOid"`
		Files      []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewed.Stdout)), &request); err != nil {
		return CheckReading{}, fmt.Errorf("decode the head of pull request %d: %w", number, err)
	}
	head := strings.TrimSpace(request.HeadRefOid)
	if !commitPattern.MatchString(head) {
		return CheckReading{}, fmt.Errorf("the forge named %q as the head of pull request %d, which is not a commit", head, number)
	}
	reading := CheckReading{HeadCommit: head}
	for _, file := range request.Files {
		if path := strings.TrimSpace(file.Path); path != "" {
			reading.Files = append(reading.Files, path)
		}
	}

	runs, err := g.api(ctx, "repos/{owner}/{repo}/commits/"+head+"/check-runs?per_page="+strconv.Itoa(maxReadCheckRuns))
	if err != nil {
		return CheckReading{}, fmt.Errorf("ask the forge for the checks on %s: %w", head, err)
	}
	if runs.Status != execution.ProcessSucceeded {
		return CheckReading{}, fmt.Errorf("ask the forge for the checks on %s: exit code %d: %s",
			head, runs.ExitCode, g.redact(firstLine(strings.TrimSpace(runs.Stderr))))
	}
	var reported struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(runs.Stdout)), &reported); err != nil {
		return CheckReading{}, fmt.Errorf("decode the checks on %s: %w", head, err)
	}
	for _, run := range reported.CheckRuns {
		name := strings.TrimSpace(run.Name)
		switch {
		case !strings.EqualFold(run.Status, "completed"):
			reading.Pending = append(reading.Pending, name)
		case failingConclusions[strings.ToLower(strings.TrimSpace(run.Conclusion))]:
			failed := FailedCheck{Name: name}
			if len(reading.Failing) < maxAnnotatedChecks && run.ID > 0 {
				paths, err := g.annotatedPaths(ctx, run.ID)
				if err != nil {
					return CheckReading{}, fmt.Errorf("ask the forge which files check %q annotates: %w", name, err)
				}
				failed.Paths = paths
			}
			reading.Failing = append(reading.Failing, failed)
		default:
			reading.Passing++
		}
	}

	// How far the base has moved on without the head is the comparison of the
	// head with the base: what the base carries past where the two met.
	compared, err := g.api(ctx, "repos/{owner}/{repo}/compare/"+head+"..."+base)
	if err != nil {
		return CheckReading{}, fmt.Errorf("compare %s with %s: %w", head, base, err)
	}
	if compared.Status != execution.ProcessSucceeded {
		return CheckReading{}, fmt.Errorf("compare %s with %s: exit code %d: %s",
			head, base, compared.ExitCode, g.redact(firstLine(strings.TrimSpace(compared.Stderr))))
	}
	var distance struct {
		AheadBy *int `json:"ahead_by"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(compared.Stdout)), &distance); err != nil {
		return CheckReading{}, fmt.Errorf("decode the comparison of %s with %s: %w", head, base, err)
	}
	if distance.AheadBy == nil {
		return CheckReading{}, fmt.Errorf("the comparison of %s with %s did not say how far %s has moved on", head, base, base)
	}
	reading.BehindBy = *distance.AheadBy
	return reading, nil
}

// annotatedPaths reads the files one check run's annotations name, each once
// and in order.
func (g GitHub) annotatedPaths(ctx context.Context, checkRun int64) ([]string, error) {
	result, err := g.api(ctx, fmt.Sprintf("repos/{owner}/{repo}/check-runs/%d/annotations?per_page=%d", checkRun, maxAnnotationsRead))
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("exit code %d: %s", result.ExitCode, g.redact(firstLine(strings.TrimSpace(result.Stderr))))
	}
	var annotations []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &annotations); err != nil {
		return nil, fmt.Errorf("decode the annotations of check run %d: %w", checkRun, err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, annotation := range annotations {
		path := strings.TrimSpace(annotation.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// DisableAutoMerge withdraws the merge the forge is holding for a pull
// request, so nothing lands it until it is asked for again. It is what the
// harness does before it hands a red request back or rewrites its head: a
// merge left armed would land the request the moment its checks turned green,
// which for a rewritten head is a change no reviewer has seen.
//
// A request with no merge armed has nothing to withdraw, and the forge saying
// so is not a failure.
func (g GitHub) DisableAutoMerge(ctx context.Context, number int) error {
	if number <= 0 {
		return fmt.Errorf("pull request number %d is not a request", number)
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return err
	}
	result, err := g.exec(ctx, append([]string{"pr", "merge", strconv.Itoa(number)}, append(scope, "--disable-auto")...)...)
	if err != nil {
		return fmt.Errorf("withdraw the queued merge of pull request %d: %w", number, err)
	}
	if result.Status == execution.ProcessSucceeded {
		return nil
	}
	reason := g.redact(strings.TrimSpace(result.Stderr))
	if nothingArmed(reason) {
		return nil
	}
	return fmt.Errorf("withdraw the queued merge of pull request %d failed with exit code %d: %s", number, result.ExitCode, reason)
}

// nothingArmed recognizes the forge saying a request has no queued merge to
// withdraw.
func nothingArmed(reason string) bool {
	normalized := normalizeAutoMerge(reason)
	return strings.Contains(normalized, "auto merge is not enabled for") ||
		strings.Contains(normalized, "auto merge is not enabled on") ||
		strings.Contains(normalized, "does not have auto merge")
}
