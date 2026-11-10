package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/store"
)

// The admin commands work on the database directly, from
// the host Keyhole runs on. None of them has an HTTP route: writing a value
// or a grant is never reachable from the network.

// cliActor is the audit actor for a value written from the command line.
const cliActor = "keyhole-cli"

// loadable are the tables `keyhole load` may write: the non-secret rows a
// tool and its grant need. Accounts, sessions, tokens, issuer keys, audit
// history and every value table are refused; a value only ever comes in
// through `keyhole keys put`.
var loadable = []string{
	"orgs", "org_memberships", "workspaces", "agents", "workspace_memberships",
	"stores", "keys", "tools", "tool_versions",
	"workspace_availability", "workspace_available_keys", "workspace_available_tools",
	"grants", "grant_slots", "cabinets", "cabinet_keys", "cabinet_tools", "cabinet_access",
}

func kekCmd(args []string, stdout io.Writer) error {
	const use = "usage: keyhole kek generate --out FILE"
	if len(args) == 0 || args[0] != "generate" {
		return usageErr("%s", use)
	}
	fs := flag.NewFlagSet("kek generate", flag.ContinueOnError)
	out := fs.String("out", "", "path of the new KEK file (must not exist; created 0600)")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *out == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	if err := envelope.GenerateKEK(*out); err != nil {
		return err
	}
	k, err := envelope.LoadKEK(*out)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote a new KEK to %s (%s)\n", *out, k.Ref())
	return nil
}

func keysCmd(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	const use = "usage: keyhole keys put --org ORG --key KEY --kek-file FILE   (the value is read from standard input)"
	if len(args) == 0 || args[0] != "put" {
		return usageErr("%s", use)
	}
	fs := flag.NewFlagSet("keys put", flag.ContinueOnError)
	org := fs.String("org", "", "organization id (required)")
	key := fs.String("key", "", "key id of a Local key (required)")
	kekFile := fs.String("kek-file", "", "KEK file from `keyhole kek generate` (required)")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *org == "" || *key == "" || *kekFile == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	kek, err := envelope.LoadKEK(*kekFile)
	if err != nil {
		return err
	}
	value, err := readValue(stdin)
	if err != nil {
		return err
	}
	defer clear(value)

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	v, err := st.PutLocalValue(ctx, kek, *org, *key, value, cliActor, time.Now())
	if err != nil {
		return putError(err, *org, *key)
	}
	fmt.Fprintf(stdout, "stored version %d of key %s\n", v, *key)
	return nil
}

// putError says why a put was refused, in terms of the command's flags.
func putError(err error, org, key string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no key %s in organization %s", key, org)
	case errors.Is(err, store.ErrExists):
		return fmt.Errorf("another put of key %s ran at the same time; nothing was stored by this one, run it again", key)
	case errors.Is(err, store.ErrNotLocal):
		return fmt.Errorf("key %s is not an active Local key; only Local keys hold values in Keyhole", key)
	}
	return err
}

// readValue reads a value from a pipe or file on stdin. A terminal is
// refused, so the value is never typed where it echoes. One trailing
// newline ("\n" or "\r\n") is dropped, as `echo` and most secret tools add
// one; anything else is kept byte for byte.
func readValue(stdin io.Reader) ([]byte, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return nil, errors.New("pipe the value on standard input; it is not read from a terminal")
	}
	// One fixed buffer, read once: io.ReadAll would grow it and leave
	// copies of the value behind that nothing clears. Nothing past the
	// buffer is read; a full buffer is over the limit by the check below.
	buf := make([]byte, envelope.MaxValue+3)
	n, err := io.ReadFull(stdin, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		clear(buf)
		return nil, err
	}
	b := buf[:n]
	if t, ok := bytes.CutSuffix(b, []byte("\n")); ok {
		b = bytes.TrimSuffix(t, []byte("\r"))
	}
	if len(b) == 0 {
		clear(buf)
		return nil, errors.New("standard input held no value")
	}
	if len(b) > envelope.MaxValue {
		clear(buf)
		return nil, fmt.Errorf("the value is over %d bytes", envelope.MaxValue)
	}
	return b, nil
}

func grantsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	const use = "usage: keyhole grants disable|enable --org ORG --grant GRANT"
	if len(args) == 0 || (args[0] != "disable" && args[0] != "enable") {
		return usageErr("%s", use)
	}
	enable := args[0] == "enable"
	fs := flag.NewFlagSet("grants "+args[0], flag.ContinueOnError)
	org := fs.String("org", "", "organization id (required)")
	grant := fs.String("grant", "", "grant id (required)")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *org == "" || *grant == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	changed, err := st.SetGrantEnabled(ctx, *org, *grant, enable, cliActor, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no grant %s in organization %s", *grant, *org)
	}
	if err != nil {
		return err
	}
	state := "disabled"
	if enable {
		state = "enabled"
	}
	if !changed {
		fmt.Fprintf(stdout, "grant %s already %s; nothing changed\n", *grant, state)
		return nil
	}
	fmt.Fprintf(stdout, "grant %s %s\n", *grant, state)
	return nil
}

func membersCmd(ctx context.Context, args []string, stdout io.Writer) error {
	const use = "usage: keyhole members add|remove --org ORG --ws WORKSPACE --sub ID [--kind user|agent]"
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		return usageErr("%s", use)
	}
	add := args[0] == "add"
	fs := flag.NewFlagSet("members "+args[0], flag.ContinueOnError)
	org := fs.String("org", "", "organization id (required)")
	ws := fs.String("ws", "", "workspace id (required)")
	sub := fs.String("sub", "", "the member as vbcdx-control names it: the assertion's sub, e.g. usr_… (required)")
	kind := fs.String("kind", "user", "user or agent: the assertion's kind")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *org == "" || *ws == "" || *sub == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	op, did, already := st.RemoveWorkspaceMember, "removed from", "is not a member of"
	if add {
		op, did, already = st.AddWorkspaceMember, "added to", "is already a member of"
	}
	changed, err := op(ctx, *org, *ws, *kind, *sub, cliActor, time.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no active workspace %s in organization %s", *ws, *org)
	case errors.Is(err, store.ErrBadMember):
		return usageErr("--kind must be user or agent, and --sub an id (1-64 letters, digits, _ or -)")
	case err != nil:
		return err
	}
	if !changed {
		fmt.Fprintf(stdout, "%s %s %s workspace %s; nothing changed\n", *kind, *sub, already, *ws)
		return nil
	}
	fmt.Fprintf(stdout, "%s %s %s workspace %s\n", *kind, *sub, did, *ws)
	return nil
}

func loadCmd(ctx context.Context, args []string, stdout io.Writer) error {
	const use = "usage: keyhole load --file FIXTURE.json"
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	file := fs.String("file", "", "fixture file (required)")
	if err := parseFlags(fs, args, use); err != nil {
		return err
	}
	if *file == "" || fs.NArg() > 0 {
		return usageErr("%s", use)
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	fx, err := store.ParseFixture(f)
	if err != nil {
		return fmt.Errorf("%s: %w", *file, err)
	}
	for _, t := range fx.Tables {
		if !slices.Contains(loadable, t.Table) {
			return fmt.Errorf("%s: table %q can't be loaded (values come in only through `keyhole keys put`)", *file, t.Table)
		}
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.LoadFixture(ctx, fx, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "loaded %d new rows from %s\n", n, *file)
	return nil
}

// cidrs is a repeatable --cidr flag.
type cidrs []string

func (c *cidrs) String() string     { return strings.Join(*c, ",") }
func (c *cidrs) Set(v string) error { *c = append(*c, v); return nil }

func toolsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	const use = "usage: keyhole tools private-allow --org ORG --tool TOOL (--cidr IP|CIDR ... | --none)"
	if len(args) == 0 || args[0] != "private-allow" {
		return usageErr("%s", use)
	}
	fs := flag.NewFlagSet("tools private-allow", flag.ContinueOnError)
	org := fs.String("org", "", "organization id (required)")
	tool := fs.String("tool", "", "tool id (required)")
	var list cidrs
	fs.Var(&list, "cidr", "an IP address or CIDR the tool may reach although it is private (repeatable)")
	none := fs.Bool("none", false, "allow no private address (clears the list)")
	if err := parseFlags(fs, args[1:], use); err != nil {
		return err
	}
	if *org == "" || *tool == "" || fs.NArg() > 0 || (len(list) == 0) == !*none {
		return usageErr("%s", use)
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := st.SetToolPrivateAllow(ctx, *org, *tool, list, cliActor, time.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no published tool %s in organization %s", *tool, *org)
	case errors.Is(err, store.ErrBadPrivateAllow):
		return usageErr("--cidr: %w", err)
	case err != nil:
		return err
	}
	shown := strings.Join(res.PrivateAllow, ", ")
	if shown == "" {
		shown = "none"
	}
	if !res.Changed {
		fmt.Fprintf(stdout, "tool %s version %d already allows %s; nothing changed\n", *tool, res.Version, shown)
	} else {
		fmt.Fprintf(stdout, "tool %s version %d published: private addresses allowed: %s\n", *tool, res.Version, shown)
	}
	if res.Pinned > 0 {
		fmt.Fprintf(stdout, "note: %d grant(s) are pinned to a version and keep its list\n", res.Pinned)
	}
	return nil
}
