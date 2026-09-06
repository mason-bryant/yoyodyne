package slack

// Reading the environment file the sink's own launch sources, for the one thing
// anything outside that launch may ask it: which of the two token variables it
// leaves without a value.
//
// It lives here rather than in whatever is asking because two surfaces ask it --
// `yoyo doctor` diagnosing an installation and `yoyo setup` walking one -- and
// two readings of one file are two answers an operator has to reconcile. The
// names being read are this package's, and so is the launch that reads the file
// for real, so this is where the question has one answer.
//
// Nothing here returns, keeps, or compares a value: a token this produced would
// be a token whatever called it could print, and both callers print what they
// get.

import "strings"

// UnassignedTokenVariables names which of the two variables the sink reads its
// credentials from the given environment file leaves without a value. An empty
// result is a file a sink can start from; anything else is exactly what that
// sink would refuse to start for want of.
//
// Existence is not this question and must not stand in for it -- the documented
// way to make the file installs an empty one and then opens an editor, so an
// operator who left the editor without saving has a file every check about the
// file passes and a sink that dies saying SLACK_BOT_TOKEN is not set.
func UnassignedTokenVariables(contents []byte) []string {
	assigned := map[string]bool{}
	for _, line := range strings.Split(string(contents), "\n") {
		if name, ok := assignment(line); ok {
			assigned[name] = true
		}
	}
	var missing []string
	for _, name := range []string{BotTokenVariable, AppTokenVariable} {
		if !assigned[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// assignment reads one line the way the launcher's `set -a; .` does, near enough
// for what is being asked: `export` is optional, the quotes around a value are
// the shell's rather than part of it, and a comment assigns nothing. It answers
// with the name and whether anything was assigned to it rather than with what
// was.
func assignment(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	if rest, found := strings.CutPrefix(line, "export "); found {
		line = strings.TrimSpace(rest)
	}
	name, value, found := strings.Cut(line, "=")
	if !found {
		return "", false
	}
	return strings.TrimSpace(name), unquote(strings.TrimSpace(value)) != ""
}

// unquote strips the quotes the shell would strip, so that a name assigned `""`
// is read as the empty assignment it becomes rather than as two characters.
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}
