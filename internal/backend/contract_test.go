package backend

import (
	"testing"
	"time"
)

var contractNow = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

// The two hard cases belong to the contract rather than to any dialect, because
// each was learned at the cost of a run and a provider nobody here wrote
// inherits both instead of getting them wrong privately.
func TestReadResetNamesTheTwoCasesEveryDialectInherits(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		resetsAt time.Time
		want     ResetKind
	}{
		{name: "a deadline in the future is one to wait on", resetsAt: contractNow.Add(time.Hour), want: ResetKnown},
		// No reset time is not fatal: the overage allowance reports this way
		// while the ordinary window keeps rolling, so the harness asks again on
		// an interval it states rather than being told when.
		{name: "no reset time at all is unknown", resetsAt: time.Time{}, want: ResetUnknown},
		// A limit still refusing work while naming a reset that has passed is
		// not describing a wait; honoring it would reissue straight back into
		// the same refusal with nothing bounding the attempts.
		{name: "a reset already passed is malformed", resetsAt: contractNow.Add(-time.Second), want: ResetMalformed},
		{name: "a reset exactly now is malformed", resetsAt: contractNow, want: ResetMalformed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ReadReset(test.resetsAt, contractNow); got != test.want {
				t.Fatalf("ReadReset() = %q, want %q", got, test.want)
			}
		})
	}
}

// A provider re-reports its limits as they change, so the last word describes
// the account now and an earlier exhausted report must not survive it.
func TestAServingReportSupersedesAnExhaustedOne(t *testing.T) {
	t.Parallel()

	var result RunResult
	Observation{Answer: AnswerLimitReached, Kind: "five_hour", ResetsAt: contractNow}.Record(&result)
	if result.UsageLimit == nil || result.UsageLimit.Kind != "five_hour" {
		t.Fatalf("UsageLimit = %#v, want the limit the provider reported", result.UsageLimit)
	}
	Observation{Answer: AnswerServed}.Record(&result)
	if result.UsageLimit != nil {
		t.Fatalf("UsageLimit = %#v, want the serving report to supersede it", result.UsageLimit)
	}
}

// The provider's own retry is evidence and nothing else: the attempt has not
// ended and nothing about the account is exhausted, so a limit already reported
// stands and no refusal is invented beside it.
func TestARetryInProgressChangesNothing(t *testing.T) {
	t.Parallel()

	result := RunResult{UsageLimit: &UsageLimit{Kind: "five_hour"}}
	Observation{Answer: AnswerRetrying}.Record(&result)
	if result.UsageLimit == nil || result.ServerOverload != nil || result.TransientFailure != nil {
		t.Fatalf("result = %#v, want the limit untouched and nothing else added", result)
	}
}

// An overload is the transient death the harness already has a wait for. A
// result carrying both would leave which answer a run took depending on the
// order the caller read them, so the contract holds the exclusivity rather than
// each adapter remembering it.
func TestAnOverloadAndATransientDeathNeverStandTogether(t *testing.T) {
	t.Parallel()

	var overloadedFirst RunResult
	Observation{Answer: AnswerUnavailable, Detail: "529"}.Record(&overloadedFirst)
	Observation{Answer: AnswerInterrupted, Detail: "connection closed"}.Record(&overloadedFirst)
	if overloadedFirst.ServerOverload == nil || overloadedFirst.TransientFailure != nil {
		t.Fatalf("result = %#v, want the overload alone", overloadedFirst)
	}

	var interruptedFirst RunResult
	Observation{Answer: AnswerInterrupted, Detail: "connection closed"}.Record(&interruptedFirst)
	Observation{Answer: AnswerUnavailable, Detail: "529"}.Record(&interruptedFirst)
	if interruptedFirst.ServerOverload == nil || interruptedFirst.TransientFailure != nil {
		t.Fatalf("result = %#v, want the overload alone", interruptedFirst)
	}
}

// A refusal that stands clears the two transient readings and leaves a limit
// alone: a limit reported beside a failed attempt is exactly the refusal the
// caller waits on.
func TestARefusalThatStandsLeavesALimitAlone(t *testing.T) {
	t.Parallel()

	result := RunResult{
		UsageLimit:       &UsageLimit{Kind: "five_hour"},
		TransientFailure: &TransientFailure{Detail: "earlier"},
	}
	Observation{Answer: AnswerRefused, Detail: "invalid request"}.Record(&result)
	if result.UsageLimit == nil {
		t.Fatal("UsageLimit was cleared by a refusal, and it is what the caller waits on")
	}
	if result.TransientFailure != nil || result.ServerOverload != nil {
		t.Fatalf("result = %#v, want nothing transient left standing", result)
	}
}

// The contract holds no duration anywhere, which is what keeps a plugin from
// being able to spend an account. A dialect states what a provider said; how
// long to wait for it is the harness's and is never expressible here.
func TestAnObservationCannotStateAWait(t *testing.T) {
	t.Parallel()

	// The reset time is the one instant an observation carries, and it is the
	// provider's claim rather than a wait: it means nothing until ReadReset says
	// what the harness makes of it.
	observation := Observation{Answer: AnswerLimitReached, ResetsAt: contractNow.Add(-time.Hour)}
	var result RunResult
	observation.Record(&result)
	if kind := ReadReset(result.UsageLimit.ResetsAt, contractNow); kind != ResetMalformed {
		t.Fatalf("ReadReset() = %q, want a claim in the past to be refused rather than waited on", kind)
	}
}

func TestEveryAnswerIsValidAndNothingElseIs(t *testing.T) {
	t.Parallel()

	for _, answer := range Answers {
		if !answer.Valid() {
			t.Errorf("Valid() = false for answer %q", answer)
		}
	}
	for _, answer := range []Answer{"", "wait", "limit_reached", "Served"} {
		if answer.Valid() {
			t.Errorf("Valid() = true for answer %q", answer)
		}
	}
}
