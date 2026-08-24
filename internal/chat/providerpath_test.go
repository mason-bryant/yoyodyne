package chat

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

// grantLine is a work item's text admitting the one path the harness's own grant
// cannot deliver, written the way an item admits any other.
const grantLine = "\n\n" + protectedpath.GrantMarker + " .claude/settings.json\n"

// The wall is found at the gate rather than three repair rounds into a run, on
// every door into the queue: admitting work, rewriting admitted work, and
// proposing it. A door that asked a weaker question is the door such an item
// would arrive through.
func TestAGrantNoProviderHonoursIsRefusedAtEveryDoorIntoTheQueue(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		door string
		err  func() error
	}{
		{
			door: "create",
			err: func() error {
				return TrackerAction{
					Action:      actionCreate,
					Title:       "Wire the goal guard into the Claude Code hook",
					Description: "The hook has to be configured for every developer run." + grantLine,
					Goal:        recordedGoal,
					Reason:      "the guard is worth nothing unconfigured",
				}.Validate()
			},
		},
		{
			door: "update",
			err: func() error {
				return TrackerAction{
					Action:      actionUpdate,
					ID:          "yoyodyne-ifd.153",
					Description: "The hook has to be configured for every developer run." + grantLine,
					Reason:      "the item needed the path",
				}.Validate()
			},
		},
		{
			door: "proposal",
			err: func() error {
				return Proposal{
					Title:       "Wire the goal guard into the Claude Code hook",
					Description: "The hook has to be configured for every developer run." + grantLine,
					Rationale:   "the guard is worth nothing unconfigured",
					Goal:        recordedGoal,
				}.Validate()
			},
		},
	} {
		err := refused.err()
		if err == nil {
			t.Fatalf("%s carrying a grant no provider honours = nil error, want it refused", refused.door)
		}
		// The refusal is only worth having if it says whose boundary this is: the
		// item's judgement about the path may well be right, and what the role has to
		// learn is that no wording of a grant reaches it.
		for _, want := range []string{".claude/settings.json", "Claude Code", "operator"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s refusal %q never names %q", refused.door, err, want)
			}
		}
	}
}

// The item that asked for this gate names the path in prose throughout its own
// description, and so will every item about the boundary after it. Only the
// marker grants, so only the marker is refused.
func TestAnItemThatMerelyDiscussesTheProviderPathIsAdmitted(t *testing.T) {
	t.Parallel()

	description := "Claude Code refuses agent writes to .claude/settings.json above yoyodyne's boundary, and a grant cannot lift it."
	if err := (TrackerAction{
		Action:      actionCreate,
		Title:       "Refuse a grant naming a provider-protected path at admission",
		Description: description,
		Goal:        recordedGoal,
		Reason:      "a boundary the harness cannot lift should not be admitted against",
	}).Validate(); err != nil {
		t.Fatalf("create discussing the path = %v, want it admitted", err)
	}
	if err := (Proposal{
		Title:       "Refuse a grant naming a provider-protected path at admission",
		Description: description,
		Rationale:   "153 spent three rounds discovering it",
		Goal:        recordedGoal,
	}).Validate(); err != nil {
		t.Fatalf("proposal discussing the path = %v, want it admitted", err)
	}
}

// The contracts are what a role writes an item's text from, so a path the
// harness refuses and no contract names is one a role finds out about only by
// being refused. They are prose rather than a generated list, which is exactly
// why this is checked.
func TestTheContractsNameEveryPathBeyondAGrant(t *testing.T) {
	t.Parallel()

	for _, contract := range []struct {
		who  string
		text string
	}{
		{who: "product manager", text: productManagerContract},
		{who: "development manager", text: developmentManagerContract},
	} {
		for _, entry := range protectedpath.ProviderPaths {
			if !strings.Contains(contract.text, entry.Path) {
				t.Fatalf("the %s contract never names %q, which the harness refuses a grant of", contract.who, entry.Path)
			}
			if !strings.Contains(contract.text, entry.Provider) {
				t.Fatalf("the %s contract never names %q, which is what refuses %q", contract.who, entry.Provider, entry.Path)
			}
		}
	}
}
