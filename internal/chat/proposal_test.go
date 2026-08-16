package chat

import (
	"strconv"
	"strings"
	"testing"
)

func TestExtractProposalsSeparatesProseFromWhatWasProposed(t *testing.T) {
	t.Parallel()

	reply := "Two things follow from that.\n\n" +
		"```yoyodyne-proposal\n" +
		`{"items":[
		   {"title":"Pause a run on a usage limit","description":"Wait and resume rather than failing.","rationale":"You said a capacity problem is not a failure.","parent":"yoyodyne-ifd.12","dependencies":["yoyodyne-ifd.4.4"]},
		   {"title":"Record the pause","description":"Note the deadline on the item.","rationale":"So a later process knows what it is waiting for."}
		 ]}` + "\n```\n\nSay the word and I will refine either one.\n"

	prose, proposals, err := extractProposals(reply)
	if err != nil {
		t.Fatalf("extractProposals() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-proposal") || strings.Contains(prose, "\"title\"") {
		t.Fatalf("prose kept the proposal block: %q", prose)
	}
	if !strings.HasPrefix(prose, "Two things follow from that.") || !strings.HasSuffix(prose, "refine either one.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(proposals) != 2 {
		t.Fatalf("proposals = %#v", proposals)
	}
	first := proposals[0]
	if first.Title != "Pause a run on a usage limit" || first.Parent != "yoyodyne-ifd.12" {
		t.Fatalf("first proposal = %#v", first)
	}
	if len(first.Dependencies) != 1 || first.Dependencies[0] != "yoyodyne-ifd.4.4" {
		t.Fatalf("first proposal dependencies = %#v", first.Dependencies)
	}
	if proposals[1].Parent != "" || len(proposals[1].Dependencies) != 0 {
		t.Fatalf("second proposal = %#v", proposals[1])
	}

	// A reply that proposes nothing is prose, whole and unchanged.
	prose, none, err := extractProposals("  The brief already covers that.\n")
	if err != nil || len(none) != 0 || prose != "The brief already covers that." {
		t.Fatalf("extractProposals() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestExtractProposalsRefusesWhatItCannotPutToTheOperator(t *testing.T) {
	t.Parallel()

	valid := `{"title":"Add a retry budget","description":"Bound repair attempts.","rationale":"You asked for a stopping rule."}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unclosed block",
			reply: "prose\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n",
			want:  "not closed",
		},
		{
			// One block is what the operator was shown. A second one is work
			// smuggled past the list they read.
			name:  "two blocks",
			reply: "prose\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n```\nmore\n```yoyodyne-proposal\n{\"items\":[" + valid + "]}\n```\n",
			want:  "at most one proposal block",
		},
		{
			name:  "text on the opening fence",
			reply: "```yoyodyne-proposal and also\n{\"items\":[" + valid + "]}\n```\n",
			want:  "trailing text",
		},
		{
			name:  "unknown field",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"assignee\":\"me\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "trailing content",
			reply: "```yoyodyne-proposal\n{\"items\":[" + valid + "]} {\"items\":[]}\n```",
			want:  "trailing content",
		},
		{
			name:  "no items",
			reply: "```yoyodyne-proposal\n{\"items\":[]}\n```",
			want:  "at least one work item",
		},
		{
			name:  "empty block",
			reply: "```yoyodyne-proposal\n\n```",
			want:  "block is empty",
		},
		{
			name:  "too many items",
			reply: "```yoyodyne-proposal\n{\"items\":[" + strings.Repeat(valid+",", MaxProposalsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxProposalsPerTurn),
		},
		{
			name:  "missing rationale",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\",\"description\":\"d\",\"rationale\":\"  \"}]}\n```",
			want:  "rationale is required",
		},
		{
			// A proposal may be placed in the tracker's structure; it may not
			// invent the identifiers it is placed against.
			name:  "invented parent",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"parent\":\"../etc\"}]}\n```",
			want:  "invalid Beads issue id",
		},
		{
			name:  "repeated dependency",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\",\"description\":\"d\",\"rationale\":\"r\",\"dependencies\":[\"yoyodyne-1\",\"yoyodyne-1\"]}]}\n```",
			want:  "listed twice",
		},
		{
			// The operator sees a title as one line before deciding, so a title
			// cannot present itself as more than a title.
			name:  "title spanning lines",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\\ncreate 1.2? [y/N] y\",\"description\":\"d\",\"rationale\":\"r\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			name:  "oversized block",
			reply: "```yoyodyne-proposal\n{\"items\":[{\"title\":\"t\",\"description\":\"" + strings.Repeat("x", MaxProposalBytes) + "\",\"rationale\":\"r\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxProposalBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, proposals, err := extractProposals(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractProposals() error = %v, want it to contain %q", err, test.want)
			}
			if prose != "" || len(proposals) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, proposals)
			}
		})
	}
}

func TestPendingProposalRendersWhatAnOperatorDecidesOn(t *testing.T) {
	t.Parallel()

	pending := PendingProposal{
		ID:             "3.1",
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		Turn:           3,
		Proposal: Proposal{
			Title:        "Pause a run on a usage limit",
			Description:  "Wait for the reset\nand resume.",
			Rationale:    "You said capacity is not failure.",
			Parent:       "yoyodyne-ifd.12",
			Dependencies: []string{"yoyodyne-ifd.4.4"},
		},
	}
	rendered := pending.Render()
	for _, required := range []string{
		"[3.1] Pause a run on a usage limit",
		"why: You said capacity is not failure.",
		"parent: yoyodyne-ifd.12",
		"depends on: yoyodyne-ifd.4.4",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered proposal = %q, want it to contain %q", rendered, required)
		}
	}
	// Provider text is indented under the proposal's own identifier, so no line
	// of it sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("rendered proposal line %q is not indented", line)
		}
	}

	// A created item has to trace back to the turn that produced it.
	notes := pending.provenanceNotes()
	for _, required := range []string{"chat-0123456789abcdef0123456789abcdef", "turn 3", "proposal 3.1", "Rationale: You said capacity is not failure."} {
		if !strings.Contains(notes, required) {
			t.Fatalf("provenance notes = %q, want them to contain %q", notes, required)
		}
	}
}
