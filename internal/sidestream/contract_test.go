package sidestream

import (
	"strings"
	"testing"
)

func TestAReplyIsProseAndAtMostOneDraft(t *testing.T) {
	t.Parallel()

	reply, err := ReadReply("The hold is read before the item is chosen.\n\n" + Fence + `
{"side":{"concluded":true,"commitments":["say so in the next brief","re-read 26 before answering again"]}}
` + "```" + "\n")
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}
	if reply.Prose != "The hold is read before the item is chosen." {
		t.Fatalf("ReadReply().Prose = %q, want the prose with the block taken out", reply.Prose)
	}
	if !reply.Concluded {
		t.Fatal("ReadReply().Concluded = false, want the thread ended")
	}
	if len(reply.Commitments) != 2 || reply.Commitments[0] != "say so in the next brief" {
		t.Fatalf("ReadReply().Commitments = %v, want both drafts", reply.Commitments)
	}
}

// Most turns carry no block at all: a thread that needs another turn says
// nothing about ending, which is not an empty draft.
func TestAReplyWithNoBlockKeepsTheThreadOpen(t *testing.T) {
	t.Parallel()

	reply, err := ReadReply("  I need to read the two items before I can say.  ")
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}
	if reply.Concluded || len(reply.Commitments) != 0 {
		t.Fatalf("ReadReply() = %#v, want an unfinished turn that promised nothing", reply)
	}
	if reply.Prose != "I need to read the two items before I can say." {
		t.Fatalf("ReadReply().Prose = %q, want it trimmed", reply.Prose)
	}
}

// A side thread takes no action, so every harness block but its own is refused
// whole — and the refusal names the one it found, because a role told only that
// "a block" was refused cannot tell which of them it reached for.
func TestAReplyReachingForAuthorityIsRefusedAndTheProseSurvives(t *testing.T) {
	t.Parallel()

	for _, refused := range []string{
		"```yoyodyne-ask",
		"```yoyodyne-report",
		"```yoyodyne-amendment",
		"```yoyodyne-landing",
	} {
		t.Run(refused, func(t *testing.T) {
			t.Parallel()

			reply, err := ReadReply("I would admit it.\n\n" + refused + "\n{}\n" + "```" + "\n")
			if err == nil {
				t.Fatal("ReadReply() = nil, want the block refused")
			}
			if !strings.Contains(err.Error(), refused) || !strings.Contains(err.Error(), "takes no action") {
				t.Fatalf("ReadReply() error = %v, want it to name %q and say why", err, refused)
			}
			// The prose is still handed back: the turn was spent on an answer
			// somebody wrote, and what the refusal costs is the block.
			if reply.Prose != "I would admit it." {
				t.Fatalf("ReadReply().Prose = %q, want the answer that was written", reply.Prose)
			}
		})
	}
}

// A fence quoted inside prose is text. A side thread explaining what a block
// looks like is not asking for one.
func TestAFenceQuotedInsideProseIsText(t *testing.T) {
	t.Parallel()

	reply, err := ReadReply("The developer would end on a ```yoyodyne-landing block, which is theirs and not mine.")
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}
	if reply.Concluded {
		t.Fatal("ReadReply() read prose about a block as a block")
	}
}

func TestReadReplyRefusesWhatTheMainThreadCouldNotRatify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{"nothing said", "   \n  ", "said nothing"},
		{"an answer past its bound", strings.Repeat("a", MaxAnswerBytes+1), "limit is"},
		{"two drafts", "Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}}\n```\n\n" + Fence + "\n{\"side\":{\"concluded\":false}}\n```\n", "at most one"},
		{"an empty draft", "Done.\n\n" + Fence + "\n\n```\n", "the block is empty"},
		{"a field nobody declared", "Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true,\"admitted\":\"ifd.401\"}}\n```\n", "decode side thread"},
		{"a draft that is not one", "Done.\n\n" + Fence + "\n[\"concluded\"]\n```\n", "decode side thread"},
		{"trailing content", "Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true}} {\"side\":{}}\n```\n", "trailing content"},
		{
			"more promises than a handful",
			"Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true,\"commitments\":[\"a\",\"b\",\"c\",\"d\",\"e\",\"f\",\"g\"]}}\n```\n",
			"planned rather than answered",
		},
		{
			"a promise that is a document",
			"Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true,\"commitments\":[\"" + strings.Repeat("x", MaxCommitmentBytes+1) + "\"]}}\n```\n",
			"commitment 1 is",
		},
		{"a promise that says nothing", "Done.\n\n" + Fence + "\n{\"side\":{\"concluded\":true,\"commitments\":[\"  \"]}}\n```\n", "commitment 1 is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ReadReply(test.reply)
			if err == nil {
				t.Fatalf("ReadReply() = nil, want %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadReply() error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// The contract's own example has to decode, because a role that follows it and
// is told the block is unreadable will stop using the block — and the thing the
// example shows is how a side thread ordinarily ends.
func TestTheContractsOwnExampleIsReadable(t *testing.T) {
	t.Parallel()

	opensAt := strings.Index(SideThreadContract, Fence)
	if opensAt < 0 {
		t.Fatal("the contract shows no block, so it tells a role nothing about how to end a thread")
	}
	payload := SideThreadContract[opensAt+len(Fence):]
	closesAt := strings.Index(payload, "\n```")
	if closesAt < 0 {
		t.Fatal("the contract's example block is not closed")
	}
	reply, err := ReadReply("What I found out.\n\n" + Fence + payload[:closesAt] + "\n```\n")
	if err != nil {
		t.Fatalf("the contract's own example is refused: %v", err)
	}
	if !reply.Concluded || len(reply.Commitments) != 1 {
		t.Fatalf("the contract's example decodes to %#v, want a concluded thread with the promise it shows", reply)
	}
}
