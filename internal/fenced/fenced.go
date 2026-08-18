// Package fenced splits an agent's reply around the one block of a kind it may
// carry.
//
// Several channels out of an agent have the same shape: prose a person reads,
// and one fenced block in a distinct language tag that the harness decodes.
// They are separate channels because they mean different things — a report
// decides nothing, a proposed amendment waits for a decision — and the
// splitting is identical for all of them: a fence that opens its own line, a
// payload up to the closing fence, and a refusal for a second block of the same
// kind. It is here once rather than copied per channel, so a channel added
// later inherits these rules instead of a copy of them that drifts.
package fenced

import (
	"errors"
	"fmt"
	"strings"
)

// Block is a reply taken apart around one fenced block.
type Block struct {
	// Before is what the agent said up to the opening fence. It is filled in
	// even when the block could not be read, because the text before a broken
	// block is the reply's real content — a summary that still has to read as
	// prose, a verdict that still has to decode — and a channel that decides
	// nothing must not cost it.
	Before string
	// Rest is the whole reply without the block: what came before it joined to
	// what came after. It is empty unless the block was read.
	Rest string
	// Payload is what the block carried, for the caller to decode.
	Payload string
	// Found says the reply carried a block of this kind at all. Most replies
	// carry none, which is not an empty block.
	Found bool
}

// Split takes a reply apart around its one block of the named kind. The kind is
// named in every failure, because a reply may carry blocks of more than one kind
// and whoever reads the failure has to be told which one could not be read.
func Split(reply, fence, kind string) (Block, error) {
	opensAt := indexFence(reply, fence)
	if opensAt < 0 {
		return Block{Before: strings.TrimSpace(reply)}, nil
	}
	block := Block{Before: strings.TrimSpace(reply[:opensAt])}
	tail := reply[opensAt+len(fence):]
	if line := tail[:lineEnd(tail)]; strings.TrimSpace(line) != "" {
		return block, fmt.Errorf("%s block opens with trailing text %q", kind, strings.TrimSpace(line))
	}
	tail = tail[lineEnd(tail):]
	closesAt := strings.Index(tail, "\n```")
	if closesAt < 0 {
		return block, fmt.Errorf("%s block is not closed", kind)
	}
	// Whatever shares the closing fence's line belongs to the fence; the reply
	// resumes on the line after it.
	after := tail[closesAt+len("\n```"):]
	after = after[lineEnd(after):]
	if indexFence(after, fence) >= 0 {
		return block, errors.New("a reply carries at most one " + kind + " block")
	}
	block.Rest = strings.TrimSpace(reply[:opensAt] + "\n" + after)
	block.Payload = tail[:closesAt]
	block.Found = true
	return block, nil
}

// indexFence finds a fence that opens its own line, so a fence quoted inside
// prose is text rather than a block boundary.
func indexFence(text, fence string) int {
	if strings.HasPrefix(text, fence) {
		return 0
	}
	if at := strings.Index(text, "\n"+fence); at >= 0 {
		return at + 1
	}
	return -1
}

func lineEnd(text string) int {
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		return at + 1
	}
	return len(text)
}
