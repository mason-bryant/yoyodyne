package chat

// A report is a claim about the build that filed it. These hold that the claim
// travels: a conversation's report carries the conversation's build, and both
// places a report is read from here — the operator's /reports and the reports
// carried into the product manager's turn — say how far that build is behind
// the target branch before anybody admits work from it.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

const (
	staleBuild = "0123456789abcdef0123456789abcdef01234567"
	heldBuild  = "fedcba9876543210fedcba9876543210fedcba98"
)

type tableBuilds map[string]int

func (b tableBuilds) Behind(_ context.Context, build string) (int, error) {
	return b[build], nil
}

func TestAConversationsReportCarriesTheBuildHoldingIt(t *testing.T) {
	t.Parallel()

	reports := &fakeReports{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: reportReply("Noted.",
			`{"severity":"note","message":"the goals still name a milestone the brief dropped."}`)},
	}})
	options.Reports = reports
	options.Build = heldBuild
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "anything?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reports.appended) != 1 || reports.appended[0].Build != heldBuild {
		t.Fatalf("collected = %#v, want the conversation's build %s", reports.appended, heldBuild)
	}
}

func TestReportsAreReadWithHowFarTheirBuildIsBehind(t *testing.T) {
	t.Parallel()

	stale := collectedReport("report-00000000000000000000000000000001", report.SeverityWarning, "the invariants index is reported as unreadable", 1)
	stale.Build = staleBuild
	unrecorded := collectedReport("report-00000000000000000000000000000002", report.SeverityNote, "filed before reports carried a build", 2)
	builds := tableBuilds{staleBuild: 31}

	t.Run("delivered to the product manager", func(t *testing.T) {
		t.Parallel()

		reports := &fakeReports{}
		seedReports(t, reports, stale, unrecorded)
		provider := &fakeBackend{results: []backendapi.RunResult{{FinalText: "noted", SessionID: "session-1"}}}
		options := testOptions(t, provider)
		options.Reports = reports
		options.Builds = builds
		session, err := Open(options)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		if _, err := session.Send(context.Background(), "what needs deciding?"); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		prompt := provider.requests[0].Prompt
		for _, want := range []string{
			"build 0123456789ab, 31 change(s) behind the target branch",
			stale.RunID + ", no build recorded",
			"check whether the fix has landed before admitting work from it",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt is missing %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("listed for the operator", func(t *testing.T) {
		t.Parallel()

		reports := &fakeReports{}
		seedReports(t, reports, stale, unrecorded)
		options := testOptions(t, &fakeBackend{})
		options.Reports = reports
		options.Builds = builds
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), testConsole(strings.NewReader("/reports\n/exit\n"), &out)); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		for _, want := range []string{
			"build 0123456789ab, 31 change(s) behind the target branch",
			"no build recorded",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("transcript is missing %q:\n%s", want, out.String())
			}
		}
	})
}
