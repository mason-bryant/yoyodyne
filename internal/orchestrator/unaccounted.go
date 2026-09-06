package orchestrator

// A developer invocation ends on one reply, and every channel out of a developer
// is read off it: the landing claim that decides whether the item closes, the
// reports, the proposals, and the summary that is the run's own account of the
// work. So a reply that accounts for nothing empties all four at once, and it
// does it silently — the invocation exited cleanly, and a run that recorded "the
// check is running; I'll report when it lands" as its summary reads afterwards
// as completed work nobody wrote anything about. That is what this file
// recognizes, so the reply can be asked for again rather than filed as the
// account.

import (
	"regexp"
	"strings"
)

// maxUnaccountedReplyBytes keeps the quoted reply to a readable part of one
// line, the same bound an unreadable landing claim is held to.
const maxUnaccountedReplyBytes = 256

// unaccountedReply is a developer invocation that ended without saying anything
// about the work it was given. It is an error so that it travels the path a
// recorded attempt already travels, and it is deliberately not a failure on its
// own: the change the developer made is in the worktree either way, and what the
// run is missing is the developer's account of it.
type unaccountedReply struct {
	reason string
}

func (e unaccountedReply) Error() string { return e.reason }

// accountedForNothing reports a developer reply that is no account of the work,
// and says which way it is not one.
//
// A landing claim answers it whatever the prose says: a developer that claimed
// evidence or an escalation wrote the reason the claim carries, and that is an
// account of the work by itself. An unreadable claim answers it too — the
// developer wrote a block, the closure is already withheld for it, and asking
// again for something that arrived and could not be parsed would spend an
// invocation on the wrong problem.
//
// What is left is prose, and only two shapes of it account for nothing: a reply
// that said nothing at all, and one whose every sentence is the work still
// happening or a promise to say something later. Anything else is taken as an
// account, however thin — the cost of asking a developer that did account for
// its work to account for it again is a whole invocation, and the shape this
// exists to catch is unmistakable.
func accountedForNothing(summary, landingOutcome, landingProblem string) (string, bool) {
	if landingOutcome != "" || landingProblem != "" {
		return "", false
	}
	account := strings.TrimSpace(summary)
	if account == "" {
		return "the invocation ended without saying anything about the work", true
	}
	if interimProgress(account) {
		return "the invocation ended on interim progress rather than an account of the work: " +
			singleLine(account, maxUnaccountedReplyBytes), true
	}
	return "", false
}

// interimProgress reports text whose every sentence is interim — the work still
// in flight, or a promise to say what became of it later. Every sentence rather
// than any of them, because a reply that says what it changed and then mentions
// a check it left running has accounted for the change: it is a thinner account
// than it should be, which is a thing for a reviewer to say, and not the silence
// this recognizes.
func interimProgress(text string) bool {
	said := 0
	for _, sentence := range sentenceBreak.Split(normalizeQuotes(text), -1) {
		trimmed := strings.TrimSpace(sentence)
		if trimmed == "" {
			continue
		}
		said++
		if !interimSentence(trimmed) {
			return false
		}
	}
	return said > 0
}

func interimSentence(sentence string) bool {
	lowered := strings.ToLower(sentence)
	for _, phrase := range interimPhrases {
		if phrase.MatchString(lowered) {
			return true
		}
	}
	return false
}

// sentenceBreak splits a reply the way a reader would read it. A semicolon
// counts because the line this was written for is two interim clauses joined by
// one — "the check is running; I'll report when it lands".
var sentenceBreak = regexp.MustCompile(`[.!?;\n]+`)

// interimPhrases are the ways a reply says the work is still happening instead
// of saying what it was. They are a written-down list rather than a judgement
// about English: a wider net would eventually refuse a real account and cost a
// run an invocation for a sentence somebody phrased unusually, and the failure
// this stops is a developer promising a report it never sends.
var interimPhrases = []*regexp.Regexp{
	// A promise to say something later. The subject and the verb are kept close
	// together so "I updated the parser" cannot read as "I'll update you".
	regexp.MustCompile(`\b(i'll|i will|we'll|we will|will)\b[^,;.]{0,40}\b(report|update|confirm|summari[sz]e|follow up|come back|let you know|post|share|tell you)\b`),
	// Work the reply says has not finished.
	regexp.MustCompile(`\b(is|are|am|'s|'re)\s+(still\s+|currently\s+|now\s+)?(running|in progress|underway|executing|building|compiling|finishing)\b`),
	regexp.MustCompile(`\b(still|currently)\s+(running|working|in progress|underway|executing|compiling|building)\b`),
	regexp.MustCompile(`\bwaiting (for|on)\b`),
	regexp.MustCompile(`\bonce (it|they|that|this|the check|the checks|the build|the run|the tests)\b`),
	// A bare acknowledgement, which says even less.
	regexp.MustCompile(`^(ok|okay|sure|on it|working on it|one moment|standing by|stand by|thanks)\b`),
}

// normalizeQuotes folds the apostrophe a provider is as likely to send as the
// typewriter one, so "I’ll report back" is read as the promise it is.
func normalizeQuotes(text string) string {
	return strings.NewReplacer("’", "'", "ʼ", "'").Replace(text)
}
