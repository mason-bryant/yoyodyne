package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// polled is one idle poll's line in the watch log, which is where both readings
// of what it found come from.
func polled(at time.Time, account runstate.PassedOver) runstate.WatchTransition {
	return runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchIdle,
		At:            at,
		PassedOver:    account,
	}
}

// lastNight is the queue the alarm woke the operator over on 2026-09-06: 47
// admitted, a third of them waiting on a triage decision, the rest carried in a
// conversation or sequenced behind work in flight.
func lastNight() runstate.PassedOver {
	var passed []PassedOverItem
	for item := 0; item < 33; item++ {
		passed = append(passed, PassedOverItem{ID: "held-" + string(rune('a'+item%26)), Class: runstate.PassedOverHeldForAPerson})
	}
	for item := 0; item < 9; item++ {
		passed = append(passed, PassedOverItem{
			ID:    "carried-" + string(rune('a'+item)),
			Class: runstate.PassedOverCarriedInConversation,
			Role:  domain.RoleArchitect,
		})
	}
	for item := 0; item < 5; item++ {
		passed = append(passed, PassedOverItem{ID: "sequenced-" + string(rune('a'+item)), Class: runstate.PassedOverSequencedBehindWork})
	}
	return GroupPassedOver(passed, 47)
}

// The page that bought this, replayed. The alarm said nothing accounted for an
// hour of silence while the session's own idle line held the whole accounting,
// and both readings now come from the one record the poll wrote: the dominant
// class, said as a fraction of the queue, with the person who releases it.
func TestTheDominantCauseIsNamedWithWhoseMoveFollowsIt(t *testing.T) {
	t.Parallel()

	cause, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{polled(moment, lastNight())}, time.Time{}, moment)
	if !accounted {
		t.Fatal("WhyThePollStartedNothing() found no cause in a poll that recorded one")
	}
	if want := "33 of the 47 admitted items are held for a person, waiting on triage decisions"; cause.Says() != want {
		t.Fatalf("Says() = %q, want %q", cause.Says(), want)
	}
	if !strings.HasPrefix(cause.Whose(), "the development manager's") {
		t.Fatalf("Whose() = %q, want the person who decides a triage hold", cause.Whose())
	}
}

// The same record, rendered the other way. The idle line names every class and
// the items in each, because it is read by somebody who asked; the alarm names
// one class and one person, because it is read by somebody holding a phone. One
// derivation, two renderers, and neither derives anything of its own.
func TestTheIdleLineAndTheAlarmRenderOneAccount(t *testing.T) {
	t.Parallel()

	account := lastNight()
	line := IdleLine(account, 0)
	for _, fact := range []string{
		"47 items passed over, of 47 admitted",
		"held for a person (held-a, held-b, held-c, held-d, held-e, and 28 further)",
		"carried in conversation (architect: carried-a",
		"sequenced behind work in flight (sequenced-a",
	} {
		if !strings.Contains(line, fact) {
			t.Fatalf("IdleLine() = %q, which does not carry %q", line, fact)
		}
	}
	cause, _ := WhyThePollStartedNothing([]runstate.WatchTransition{polled(moment, account)}, time.Time{}, moment)
	if strings.Contains(cause.Says(), "held-a") {
		t.Fatalf("Says() = %q, want the class and the count rather than the queue read out", cause.Says())
	}
}

// A run in flight belongs in the line and never in the count of what was passed
// over, and a poll that reached nothing says so rather than claiming the queue
// held nothing startable.
func TestTheIdleLineSaysWhatIsRunningAndWhatWasNotReached(t *testing.T) {
	t.Parallel()

	empty := runstate.PassedOver{}
	if line := IdleLine(empty, 0); line != "the backlog is empty" {
		t.Fatalf("IdleLine() = %q over no admitted work", line)
	}
	if line := IdleLine(runstate.PassedOver{Admitted: 3}, 1); line != "1 run in flight" {
		t.Fatalf("IdleLine() = %q, want the run that is going", line)
	}
	if line := IdleLine(runstate.PassedOver{Admitted: 3}, 0); line != "none of the 3 items admitted was reached at this poll" {
		t.Fatalf("IdleLine() = %q, want the poll saying what it did not reach", line)
	}
}

// The state that woke the operator on 2026-09-05 is one of the causes this can
// name, rather than a silence the alarm has to work out for itself a second
// time. It answers ahead of anything the poll passed over, because nothing in
// the queue is startable while the window stands.
func TestAProviderWindowIsACauseAndAnswersAheadOfTheQueue(t *testing.T) {
	t.Parallel()

	lifts := moment.Add(90 * time.Minute)
	poll := polled(moment, lastNight())
	poll.ProviderWindow = true
	poll.ProviderWindowResetsAt = &lifts

	cause, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{poll}, time.Time{}, moment.Add(time.Minute))
	if !accounted {
		t.Fatal("WhyThePollStartedNothing() found no cause in a poll waiting out a usage window")
	}
	if !strings.HasPrefix(cause.Says(), "Paused on the provider's usage window") {
		t.Fatalf("Says() = %q, want the window's own sentence", cause.Says())
	}
	if !strings.HasPrefix(cause.Whose(), "nobody's") {
		t.Fatalf("Whose() = %q, want nobody sent to look at a machine that is behaving", cause.Whose())
	}

	// And a window that has lifted accounts for nothing: what is left is the queue
	// the poll actually read.
	lifted, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{poll}, time.Time{}, lifts.Add(time.Minute))
	if !accounted || lifted.Class != runstate.PassedOverHeldForAPerson {
		t.Fatalf("WhyThePollStartedNothing() = %+v after the window lifted, want the queue's own cause", lifted)
	}
}

// A store that would not answer is the other state that answers ahead of the
// queue, for the same reason: what is in the queue is not what stopped the
// choosing, and nothing a person admits reaches a store that will not answer.
func TestAPollThatCouldNotReadTheQueueIsItsOwnCause(t *testing.T) {
	t.Parallel()

	poll := polled(moment, runstate.PassedOver{})
	poll.Unreadable = true
	cause, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{poll}, time.Time{}, moment)
	if !accounted || !cause.Unreadable {
		t.Fatalf("WhyThePollStartedNothing() = %+v, %v, want the unreadable queue named", cause, accounted)
	}
	if !strings.HasPrefix(cause.Whose(), "the harness's") {
		t.Fatalf("Whose() = %q, want the harness reading again rather than a person acting", cause.Whose())
	}
}

// Where no poll left an account, nothing is invented. A session that stopped
// cleanly is a line somebody has to start rather than a queue with something in
// the way of it, and a session that is choosing work has nothing to say about
// what it passed over.
func TestNoPollLeavesNoCauseRatherThanAnInventedOne(t *testing.T) {
	t.Parallel()

	stopped := polled(moment, runstate.PassedOver{})
	stopped.State = runstate.WatchStopped
	watching := polled(moment, runstate.PassedOver{})
	watching.State = runstate.WatchWatching

	for name, sessions := range map[string][]runstate.WatchTransition{
		"no session at all":                                     nil,
		"a session that stopped":                                {stopped},
		"a session choosing work":                               {watching},
		"an idle poll over a queue it passed nothing over from": {polled(moment, runstate.PassedOver{Admitted: 4})},
	} {
		if cause, accounted := WhyThePollStartedNothing(sessions, time.Time{}, moment); accounted {
			t.Fatalf("%s: WhyThePollStartedNothing() = %+v, want no cause", name, cause)
		}
	}
}

// An account a start overtook. The poll is still the newest thing a live session
// said, but work ran after it and the line then went quiet, so the queue it
// describes has not been read since it moved and stating it as the present cause
// would have the reader release a queue nothing has looked at since.
func TestAnAccountAStartOvertookIsNotThePresentCause(t *testing.T) {
	t.Parallel()

	poll := polled(moment, lastNight())
	// The harness started something after that poll, and then went quiet. The
	// silence being reported begins at the start rather than at the poll.
	started := moment.Add(5 * time.Minute)
	if cause, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{poll}, started, started.Add(time.Hour)); accounted {
		t.Fatalf("WhyThePollStartedNothing() = %+v, want no cause from a poll older than the silence", cause)
	}
	// A poll made after the last start is not overtaken however long it has stood,
	// which is the case the bound is careful to keep: a session idling over an
	// unchanging queue writes one line and then nothing. It reads the same whether
	// that session is still polling or died after writing it, and what tells those
	// apart is the chooser's last word rather than anything here.
	polledAfter := polled(started.Add(time.Minute), lastNight())
	cause, accounted := WhyThePollStartedNothing([]runstate.WatchTransition{polledAfter}, started, started.Add(time.Hour))
	if !accounted || cause.Class != runstate.PassedOverHeldForAPerson {
		t.Fatalf("WhyThePollStartedNothing() = %+v, %v, want the account the idle session actually holds", cause, accounted)
	}
}

// Every class says something and names a move. A class added to the taxonomy
// without either is a state the harness can record and no message can act on,
// which is the whole failure both tables exist to end.
func TestEveryPassedOverClassSaysSomethingAndNamesAMove(t *testing.T) {
	t.Parallel()

	for _, class := range runstate.PassedOverClasses() {
		cause := Cause{Class: class, Count: 2, Admitted: 5}
		if strings.TrimSpace(cause.Says()) == "" {
			t.Fatalf("class %q says nothing about the items in it", class)
		}
		if strings.TrimSpace(cause.Whose()) == "" {
			t.Fatalf("class %q names no move", class)
		}
	}
	// And nothing outside the set borrows an answer from it.
	outside := Cause{Class: runstate.PassedOverClass("something-nobody-named"), Count: 1, Admitted: 1}
	if outside.Says() != "" || outside.Whose() != "" {
		t.Fatalf("a class outside the taxonomy was given words: %q / %q", outside.Says(), outside.Whose())
	}
}

// The count agrees with the sentence around it, because a message whose
// arithmetic reads wrong is one somebody stops trusting the rest of.
func TestOneItemHeldIsSaidInTheSingular(t *testing.T) {
	t.Parallel()

	cause := Cause{Class: runstate.PassedOverParked, Count: 1, Admitted: 4}
	if want := "1 of the 4 admitted items is parked, and no pull selects a parked item however far the queue drains"; cause.Says() != want {
		t.Fatalf("Says() = %q, want %q", cause.Says(), want)
	}
}

// A tie is settled by the order the pull met the classes, which is the product
// manager's own order, rather than by whichever the grouping happened to reach
// first.
func TestATieIsSettledByTheOrderTheQueuePutsThemIn(t *testing.T) {
	t.Parallel()

	account := GroupPassedOver([]PassedOverItem{
		{ID: "first", Class: runstate.PassedOverParked},
		{ID: "second", Class: runstate.PassedOverHeldForAPerson},
	}, 2)
	cause, _ := WhyThePollStartedNothing([]runstate.WatchTransition{polled(moment, account)}, time.Time{}, moment)
	if cause.Class != runstate.PassedOverParked {
		t.Fatalf("Class = %q, want the class the queue puts first", cause.Class)
	}
}

// The conversation that carries the work is the one class whose move is a named
// person rather than a wait, and it is read off the marker the item carries
// rather than derived from anything here.
func TestWorkCarriedInConversationNamesTheRoleThatCarriesIt(t *testing.T) {
	t.Parallel()

	account := GroupPassedOver([]PassedOverItem{
		{ID: "one", Class: runstate.PassedOverCarriedInConversation, Role: domain.RoleArchitect},
	}, 3)
	if carrier := Carrier(account); carrier != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("Carrier() = %q, want the conversation the item names", carrier)
	}
	cause, _ := WhyThePollStartedNothing([]runstate.WatchTransition{polled(moment, account)}, time.Time{}, moment)
	if !strings.Contains(cause.Says(), "carried in conversation by the architect") {
		t.Fatalf("Says() = %q, want the role that carries it", cause.Says())
	}
	if !strings.HasPrefix(cause.Whose(), "the architect's, in conversation") {
		t.Fatalf("Whose() = %q, want the architect", cause.Whose())
	}
}

// The names are bounded where they are written down rather than at each
// renderer, so what the record holds and what a reader is shown are the same
// account.
func TestTheNamesAreBoundedAndTheCountStaysExact(t *testing.T) {
	t.Parallel()

	var passed []PassedOverItem
	for item := 0; item < MaxPassedOverNamed+7; item++ {
		passed = append(passed, PassedOverItem{ID: "item-" + string(rune('a'+item)), Class: runstate.PassedOverParked})
	}
	account := GroupPassedOver(passed, len(passed))
	if len(account.Groups) != 1 {
		t.Fatalf("GroupPassedOver() = %d groups, want one class", len(account.Groups))
	}
	if got := account.Groups[0]; len(got.Items) != MaxPassedOverNamed || got.Count != MaxPassedOverNamed+7 {
		t.Fatalf("group = %+v, want %d named of %d counted", got, MaxPassedOverNamed, MaxPassedOverNamed+7)
	}
	if account.Passed() != MaxPassedOverNamed+7 {
		t.Fatalf("Passed() = %d, want every item counted", account.Passed())
	}
}
