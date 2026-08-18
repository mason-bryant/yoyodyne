package fenced

import (
	"strings"
	"testing"
)

func TestSplitSeparatesTheBlockFromWhatWasSaid(t *testing.T) {
	t.Parallel()

	reply := "before the block\n\n```yoyodyne-thing\n{\"a\":1}\n```\nafter the block\n"
	block, err := Split(reply, "```yoyodyne-thing", "thing")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if !block.Found || block.Payload != `{"a":1}` {
		t.Fatalf("Split() = %#v", block)
	}
	if block.Before != "before the block" {
		t.Fatalf("Before = %q", block.Before)
	}
	// The reply resumes after the fence, so what was said on either side of the
	// block is one piece of prose with the block lifted out of it.
	if strings.Contains(block.Rest, "yoyodyne-thing") || strings.Contains(block.Rest, `{"a":1}`) {
		t.Fatalf("the block stayed in what was said: %q", block.Rest)
	}
	if !strings.HasPrefix(block.Rest, "before the block") || !strings.HasSuffix(block.Rest, "after the block") {
		t.Fatalf("Rest = %q", block.Rest)
	}
}

func TestAFenceQuotedInsideProseIsText(t *testing.T) {
	t.Parallel()

	// An agent explaining the protocol is not using it, and reading its
	// explanation as a block would decode a worked example as a real one.
	block, err := Split("say it as ```yoyodyne-thing followed by the payload\n", "```yoyodyne-thing", "thing")
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if block.Found {
		t.Fatalf("Split() read a quoted fence as a block: %#v", block)
	}
}

func TestWhatIsSaidSurvivesABlockThatCannotBeRead(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]string{
		"unclosed":     "Done.\n\n```yoyodyne-thing\n{}\n",
		"trailingText": "Done.\n\n```yoyodyne-thing json\n{}\n```\n",
		"second":       "Done.\n\n```yoyodyne-thing\n{}\n```\n\n```yoyodyne-thing\n{}\n```\n",
	} {
		block, err := Split(reply, "```yoyodyne-thing", "thing")
		if err == nil {
			t.Errorf("%s: Split() accepted %q", name, reply)
			continue
		}
		// The failure names the kind, because a reply may carry blocks of more
		// than one kind and whoever reads it has to know which one was lost.
		if !strings.Contains(err.Error(), "thing") {
			t.Errorf("%s: error does not name the kind: %v", name, err)
		}
		if block.Before != "Done." {
			t.Errorf("%s: what was said did not survive: %q", name, block.Before)
		}
	}
}
