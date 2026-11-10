package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Exit codes (the top-level usage lists them): 0 success, 1 a command that
// failed, 2 a wrong command line. 2 is the usage code of the vbcdx CLIs
// (vbcdx, vbcdx-svc, vbcdx-sidecar, vbcdx-control), so a script reads them
// alike.
const (
	exitFailure = 1
	exitUsage   = 2
)

// usageError is a wrong command line: an unknown command or flag, a missing
// or bad argument. main exits exitUsage for it. It wraps the message's error,
// so a %w in usageErr's format keeps errors.Is working.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }

func (e usageError) Unwrap() error { return e.err }

func usageErr(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// exitCode is the process exit status for run's error.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var u usageError
	if errors.As(err, &u) {
		return exitUsage
	}
	return exitFailure
}

// parseFlags parses a command's flags. Go's own output never prints (it
// writes flags with one dash and takes a placeholder from backquoted usage
// text), and its error text is never passed on: it can carry the value given
// (`invalid boolean value "<value>" for -none`), and a value can be a
// secret. The error names the flag as --flag, and only when the name looks
// like a flag name, then gives the command's usage.
func parseFlags(fs *flag.FlagSet, args []string, use string) error {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr("%s; %s", flagProblem(err, fs), use)
	}
	return nil
}

// flagProblem says what was wrong with the flags, from Go's error: the kind
// of problem and the flag's name, never the value or the raw argument. For
// a bad value, the name is the one right after Go's quoted value (read with
// strconv.QuotedPrefix), so text inside the value, or in a flag's own Set
// error after it, can't choose which name is shown, and
// it must be a flag fs defines. (Go reports "needs an argument" only for a
// flag fs defines, so that name needs no such check.) The prefixes are Go's
// own wording; TestABadValueNamesTheRealFlag runs Go's real errors through
// here, so a toolchain that rewords them fails that test rather than
// quietly falling back to "a bad flag value".
func flagProblem(err error, fs *flag.FlagSet) string {
	msg := err.Error()
	if m := notDefined.FindStringSubmatch(msg); m != nil {
		if name, ok := shownName(m[1]); ok {
			return fmt.Sprintf("unknown flag --%s", name)
		}
		return "an unknown flag"
	}
	if m := needsValue.FindStringSubmatch(msg); m != nil {
		if name, ok := shownName(m[1]); ok {
			return fmt.Sprintf("--%s needs a value", name)
		}
		return "a flag needs a value"
	}
	for _, p := range []string{"invalid boolean value ", "invalid value "} {
		rest, ok := strings.CutPrefix(msg, p)
		if !ok {
			continue
		}
		if name := nameAfterQuoted(rest); name != "" && fs.Lookup(name) != nil {
			if shown, ok := shownName(name); ok {
				return fmt.Sprintf("a bad value for --%s", shown)
			}
		}
		return "a bad flag value"
	}
	return "bad flags"
}

// nameAfterQuoted reads `"<value>" for [flag ]-<name>: …` and returns
// <name>, or "" when the text isn't in that shape.
func nameAfterQuoted(rest string) string {
	q, err := strconv.QuotedPrefix(rest)
	if err != nil {
		return ""
	}
	rest = rest[len(q):]
	for _, p := range []string{" for flag -", " for -"} {
		if after, ok := strings.CutPrefix(rest, p); ok {
			if i := strings.Index(after, ": "); i > 0 {
				return after[:i]
			}
		}
	}
	return ""
}

var (
	notDefined = regexp.MustCompile(`^flag provided but not defined: -(\S+)$`)
	needsValue = regexp.MustCompile(`^flag needs an argument: -(\S+)$`)
)

// shownName is a flag or command name, with no leading dash, if it looks
// like one and can't be a token: lowercase letters, digits and dashes,
// starting with a letter, 1 to 32 long, and not made only of hex digits (a
// hex key or token of that size would pass the other rules). The same rule
// as vbcdx-core's shown_name, so the CLIs agree. Anything else (a mistyped
// token, a path) is never echoed.
func shownName(s string) (string, bool) {
	s = strings.TrimLeft(s, "-")
	if !nameShape.MatchString(s) || allHex.MatchString(s) {
		return "", false
	}
	return s, true
}

var (
	nameShape = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	allHex    = regexp.MustCompile(`^[0-9a-f]+$`)
)

// columns splits a usage line into its synopsis and description columns.
var columns = regexp.MustCompile(`\s{2,}`)

// isHelp reports whether arg asks for help.
func isHelp(arg string) bool { return arg == "-h" || arg == "-help" || arg == "--help" }

// commandHelp is one command's entry from the top-level usage: its first
// line, `  <command> …`, and the indented lines under it. ok is false for a
// command the usage doesn't list.
func commandHelp(cmd string) (text string, ok bool) {
	lines := strings.Split(usage, "\n")
	start := -1
	for i, l := range lines {
		fields := strings.Fields(l)
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") && len(fields) > 0 && fields[0] == cmd {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	// The first line is the synopsis, then (after a run of spaces) the
	// start of the description; the lines under it continue it.
	parts := columns.Split(strings.TrimSpace(lines[start]), 2)
	var b strings.Builder
	b.WriteString("usage: keyhole " + parts[0] + "\n")
	var desc []string
	if len(parts) == 2 {
		desc = append(desc, parts[1])
	}
	for _, l := range lines[start+1:] {
		// The entry goes on while lines are indented past the command
		// column.
		if !strings.HasPrefix(l, "   ") {
			break
		}
		desc = append(desc, strings.TrimSpace(l))
	}
	if len(desc) > 0 {
		// One paragraph: the top-level layout's line breaks fall mid-sentence.
		b.WriteString("\n  " + strings.Join(desc, " ") + "\n")
	}
	return b.String(), true
}
