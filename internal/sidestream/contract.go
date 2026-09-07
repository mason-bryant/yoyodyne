package sidestream

// What the harness says to a role holding a side thread, and what it will read
// back from one.
//
// The contract is a Go constant for the same reason every other contract here
// is: a project may rewrite a persona and must not be able to widen what a side
// thread is. It describes a boundary, and ReadReply enforces that boundary
// whether or not the role read the words — which is what
// `configuration-never-grants-authority` requires of a per-agent knob that only
// ever chooses whether an agent holds side threads at all.
//
// It does not state the turn cap as a number. The cap is configurable, and what
// a role needs is not the setting but where this thread has got to against it,
// which the harness says on every turn it delivers out of the stream's own
// durable record rather than out of a contract that would have to be re-rendered
// to stay true.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/fenced"
)

// Fence opens the one block a side turn's reply may carry. It is a distinct
// language tag for the same reason the ask, report, and proposal fences are: a
// side thread's draft can never be confused with JSON the role happens to be
// discussing.
const Fence = "```yoyodyne-side"

// harnessFencePrefix opens every block the harness reads out of an agent's
// reply. A side thread is refused all of them but its own, at once rather than
// block by block, so a channel added later is refused here without this having
// to learn about it.
const harnessFencePrefix = "```yoyodyne-"

// The bounds on the block one reply may carry. A side thread that promised more
// than a handful of things has planned rather than answered, and the main thread
// has to ratify every one of them by hand.
//
// What the merge does with them is bounded again where it composes the revision,
// because the store bounds the assembled text and a refusal naming that would
// tell whoever wrote a commitment nothing about which part of it was too long.
const (
	MaxDraftedCommitments = 6
	MaxCommitmentBytes    = 200
	// MaxBlockBytes bounds the untrusted payload a reply's block may carry.
	MaxBlockBytes = 8 << 10
)

// SideThreadContract is what a role carries on a side thread, after its own
// contract. It states the boundary in the words the harness holds it to.
const SideThreadContract = "# You are being asked something on a side thread\n\n" +
	`Your main conversation is busy, so this question has come to you beside it, on a side thread: your own thread, your own record, and a bounded number of turns. It is not your main conversation and it is not work — it is you judging something while the thread that can act is doing something else.

Two things are true here, and both are enforced rather than requested.

**You take no action.** You judge, you answer, and you plan tentatively, and you read: the tracker and the evidence your role was given are open to you. Nothing else is. Nothing here creates, updates, closes, reorders, or admits a work item; nothing here raises a proposal or a concern, records an evaluation, commissions research, issues a directive, or puts an ask to another role. Every intent you form here is a draft. A reply asking for any of those is refused whole and your question goes unanswered, so do not reach for one: say what you would do in prose, and let the main thread do it.

**What you promise is best effort until your main thread confirms it.** Whoever reads your answer is told so. When this thread ends, what you worked out is written into your own memory — an account of it, with this thread's identifier attached, and never this transcript — and your main thread's next turn reads that and either ratifies what you promised or adjusts it through its own ordinary path. That is the only path there is.

Answer in prose. When you have answered what you were asked, say so in one block after the prose, and name anything you tentatively promised so the main thread has it to ratify:

` + "```" + `yoyodyne-side
{"side":{"concluded":true,"commitments":["what you said you would do, in one line each"]}}
` + "```" + `

"concluded" ends this thread and merges what you said, and is how a side thread ordinarily ends. "commitments" is optional and is what somebody has to ratify rather than everything you thought. Leave the block out entirely to keep the thread open for a further turn, which is what you do when you need one more.

The thread has a hard limit on turns and the harness ends one that reaches it. What you had reached still merges, so a thread cut off half way is legible as one rather than as a thread that said nothing — but it is a poor way for a question to end, so answer what was actually asked rather than working towards it.`

// Reply is one side turn's answer taken apart: the prose whoever asked reads,
// and the draft the thread ended on.
type Reply struct {
	Prose string
	// Concluded says the side thread has answered what it was opened for, which
	// is what makes the turn its last.
	Concluded bool
	// Commitments are what it tentatively promised. They are kept apart from the
	// prose rather than left inside it because the main thread has to ratify each
	// one, and "which sentences were promises" is not a question a later reader
	// should have to answer by re-reading.
	Commitments []string
}

// sideDocument is the block's shape. The nesting is the house style for a
// harness block: one named key, so a reply that carried JSON about something
// else could never be read as this.
type sideDocument struct {
	Side struct {
		Concluded   bool     `json:"concluded"`
		Commitments []string `json:"commitments"`
	} `json:"side"`
}

// ReadReply takes what a side thread said and returns it, or refuses it.
//
// This is where "it takes no action" stops being a description and becomes a
// rule: a side thread may say what it thinks and draft what the main thread will
// ratify, so a reply carrying any harness block but its own is refused whole.
// Nothing in it is carried out, the turn records why, and whoever asked is told
// its question went unanswered rather than being handed half an answer.
//
// The prose is returned even where the block could not be read, because the text
// around a broken block is the answer somebody wrote and the turn was spent on
// it either way. What the refusal costs is the block, which decides nothing here.
func ReadReply(reply string) (Reply, error) {
	block, err := fenced.Split(reply, Fence, "side thread")
	if err != nil {
		return Reply{Prose: strings.TrimSpace(block.Before)}, err
	}
	said := block.Before
	if block.Found {
		said = block.Rest
	}
	said = strings.TrimSpace(said)
	if at := indexHarnessFence(said); at >= 0 {
		// The prose before the block is handed back and the block is not: what a
		// side thread said is worth reading, and what it asked for is refused
		// rather than passed on as though it were prose.
		return Reply{Prose: strings.TrimSpace(said[:at])}, fmt.Errorf("the side thread asked for %q, and a side thread takes no action: it judges, answers, and drafts, and the main thread ratifies",
			strings.TrimSpace(said[at:at+lineLength(said[at:])]))
	}
	if said == "" {
		return Reply{}, errors.New("the side thread said nothing")
	}
	if len(said) > MaxAnswerBytes {
		return Reply{}, fmt.Errorf("the side thread's answer is %d bytes, limit is %d", len(said), MaxAnswerBytes)
	}
	parsed := Reply{Prose: said}
	if !block.Found {
		return parsed, nil
	}
	drafted, err := decodeSide(block.Payload)
	if err != nil {
		return parsed, err
	}
	parsed.Concluded = drafted.Concluded
	parsed.Commitments = drafted.Commitments
	return parsed, nil
}

// decodeSide strictly decodes the block payload. Unknown fields, trailing
// content, and oversized input are refused rather than tolerated: what the main
// thread is going to ratify has to be exactly what the side thread said.
func decodeSide(payload string) (Reply, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Reply{}, errors.New("decode side thread: the block is empty")
	}
	if len(trimmed) > MaxBlockBytes {
		return Reply{}, fmt.Errorf("decode side thread: block is %d bytes, limit is %d", len(trimmed), MaxBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document sideDocument
	if err := decoder.Decode(&document); err != nil {
		return Reply{}, fmt.Errorf("decode side thread: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Reply{}, errors.New("decode side thread: unexpected trailing content after the block")
	}
	if len(document.Side.Commitments) > MaxDraftedCommitments {
		return Reply{}, fmt.Errorf("the side thread drafted %d commitments, limit is %d; a thread that promised more than a handful has planned rather than answered",
			len(document.Side.Commitments), MaxDraftedCommitments)
	}
	drafted := Reply{Concluded: document.Side.Concluded}
	for at, commitment := range document.Side.Commitments {
		if err := boundedText(fmt.Sprintf("commitment %d", at+1), commitment, MaxCommitmentBytes, true); err != nil {
			return Reply{}, err
		}
		drafted.Commitments = append(drafted.Commitments, strings.TrimSpace(commitment))
	}
	return drafted, nil
}

// indexHarnessFence finds a harness block that opens its own line, so a fence
// quoted inside prose is text rather than a request.
func indexHarnessFence(text string) int {
	if strings.HasPrefix(text, harnessFencePrefix) {
		return 0
	}
	if at := strings.Index(text, "\n"+harnessFencePrefix); at >= 0 {
		return at + 1
	}
	return -1
}

func lineLength(text string) int {
	if at := strings.IndexByte(text, '\n'); at >= 0 {
		return at
	}
	return len(text)
}
