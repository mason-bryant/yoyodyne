package cli

import (
	"strings"
	"testing"
)

// The product manager is given this instead of a way to run a command, so a
// command missing from it is a surface the role deciding what to build next
// cannot know exists -- which is what happened on 2026-08-18, when "yoyo cost"
// had to be described by the operator. The command list is the source of what
// must be here, so a command added later is caught by the same check.
func TestCommandHelpCarriesEveryCommand(t *testing.T) {
	t.Parallel()

	help := commandHelp()
	// These take flags and nothing else, so they have no usage of their own to
	// print and are named in the command list above rather than repeated.
	withoutUsage := map[string]bool{"init": true, "version": true, "help": true}

	var listed []string
	var commands strings.Builder
	printUsage(&commands)
	inCommands := false
	for _, line := range strings.Split(commands.String(), "\n") {
		if strings.TrimSpace(line) == "Commands:" {
			inCommands = true
			continue
		}
		if !inCommands || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		listed = append(listed, fields[0])
	}
	if len(listed) == 0 {
		t.Fatal("the command list names no commands; this check is reading it wrong")
	}

	for _, command := range listed {
		if withoutUsage[command] {
			if !strings.Contains(help, "  "+command+" ") {
				t.Fatalf("%q is not named in the command help:\n%s", command, help)
			}
			continue
		}
		if !strings.Contains(help, "Usage: yoyo "+command) {
			t.Fatalf("the command help carries no usage for %q:\n%s", command, help)
		}
	}
}
