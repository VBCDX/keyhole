package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
)

// envFileMax bounds --env-file: an installer-written file is a dozen lines.
const envFileMax = 64 << 10

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile reads the KEY=VALUE file a systemd unit names in
// EnvironmentFile= (the one `vbcdx install service keyhole-server` writes),
// so an admin command or `keyhole check` sees exactly the settings the
// service runs with. Only the subset that reads the same in systemd and here
// is accepted: blank lines, # or ; comments, and KEY=VALUE with the spaces
// and tabs around the key and value trimmed. A quoted value, a backslash, a
// control character other than a tab (a bare \r included), a repeated key or
// a line without = is refused rather than read differently from systemd.
// The file is opened once with O_NOFOLLOW and must be a regular file, owned
// by root or this user, and writable by nobody else. Its directories are not
// checked: they must be root's (the installer's /etc/vbcdx-keyhole/<i> is).
//
// An error names --env-file and a line number, never the path given or a key
// from the file: either could be a pasted token (the value-echo
// class). No value from the file is ever printed.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("--env-file can't be opened (a symlink is refused): %w", osCause(err))
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("--env-file: %w", osCause(err))
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("--env-file is not a regular file")
	}
	// Whoever can write the file chooses the KEK, key and database paths:
	// only root or this user may own it, and nobody else may write it.
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return nil, fmt.Errorf("--env-file has mode %04o; group and others must not be able to write it", perm)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || (st.Uid != 0 && int(st.Uid) != os.Geteuid()) {
		return nil, fmt.Errorf("--env-file must be owned by root or this user (uid %d)", os.Geteuid())
	}
	raw, err := io.ReadAll(io.LimitReader(f, envFileMax+1))
	if err != nil {
		return nil, fmt.Errorf("--env-file: %w", osCause(err))
	}
	if len(raw) > envFileMax {
		return nil, fmt.Errorf("--env-file is larger than %d bytes", envFileMax)
	}
	env := map[string]string{}
	first := map[string]int{} // the line that set each key
	// Lines split on \n alone (a \r directly before it is dropped, as for a
	// CRLF file). Every other control byte but the tab is refused: systemd
	// would also end a line at a bare \r, and a NUL, \v or \f is read
	// differently by the two. Only ASCII spaces and tabs are trimmed, as
	// systemd trims; any other byte (a no-break space, say) stays in the value
	// for both.
	lines := bytes.Split(raw, []byte("\n"))
	for i, line := range lines {
		n := i + 1
		if i < len(lines)-1 { // a \r counts as CRLF only directly before a \n
			line = bytes.TrimSuffix(line, []byte("\r"))
		}
		for _, c := range line {
			if (c < 0x20 && c != '\t') || c == 0x7f {
				return nil, fmt.Errorf("--env-file line %d has a control character (0x%02x); only tabs are allowed", n, c)
			}
		}
		text := strings.Trim(string(line), " \t")
		if text == "" || text[0] == '#' || text[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(text, "=")
		k, v = strings.Trim(k, " \t"), strings.Trim(v, " \t")
		switch {
		case !ok || !envKeyRe.MatchString(k):
			return nil, fmt.Errorf("--env-file line %d is not KEY=VALUE", n)
		case strings.ContainsAny(v, `"'\`):
			return nil, fmt.Errorf("--env-file line %d: quotes and backslashes are not supported", n)
		}
		if was, dup := first[k]; dup {
			return nil, fmt.Errorf("--env-file line %d sets the key line %d set", n, was)
		}
		env[k], first[k] = v, n
	}
	return env, nil
}

// applyEnvFile sets every key the file names, over the process environment,
// as systemd's EnvironmentFile= does for the unit.
func applyEnvFile(path string, setenv func(k, v string) error) error {
	env, err := readEnvFile(path)
	if err != nil {
		return err
	}
	for k, v := range env {
		if err := setenv(k, v); err != nil {
			return errors.Join(errors.New("--env-file: setting a key from it failed"), osCause(err))
		}
	}
	return nil
}

// osCause is an *os.PathError's cause (the errno, say) without its path,
// which is the path the operator typed; any other error as it is.
func osCause(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
