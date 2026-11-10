package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

const litellmFixture = "../../testdata/litellm.json"

func TestKeysPutStoresAValueFromStdin(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	kekPath := filepath.Join(t.TempDir(), "kek")
	var out bytes.Buffer
	canary := "sk-CANARY-k1-5e8d0a"

	if err := run(ctx, []string{"kek", "generate", "--out", kekPath}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"load", "--file", litellmFixture}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"load", "--file", litellmFixture}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "loaded 0 new rows") {
		t.Fatalf("a second load changed rows: %q", out.String())
	}
	put := []string{"keys", "put", "--org", "org_team", "--key", "k_litellm_test", "--kek-file", kekPath}
	if err := run(ctx, put, strings.NewReader(canary+"\n"), &out, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, put, strings.NewReader("v2-"+canary+"\r\n"), &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stored version 2 of key k_litellm_test") {
		t.Fatalf("output %q", out.String())
	}

	st := storetest.Open(t, b)
	kek, err := envelope.LoadKEK(kekPath)
	if err != nil {
		t.Fatal(err)
	}
	got, v, err := st.LocalValue(ctx, kek, "org_team", "k_litellm_test")
	if err != nil || v != 2 || string(got) != "v2-"+canary {
		t.Fatalf("LocalValue = %q v%d, %v", got, v, err)
	}
	g, err := st.ListGrants(ctx, "org_team", "ws_team")
	if err != nil || len(g) != 1 {
		t.Fatalf("grants = %+v, %v", g, err)
	}
	if strings.Contains(out.String(), "CANARY") {
		t.Fatalf("the value reached stdout: %q", out.String())
	}
}

func TestKeysPutRefusals(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	dir := t.TempDir()
	kekPath := filepath.Join(dir, "kek")
	if err := envelope.GenerateKEK(kekPath); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "loose")
	raw, _ := os.ReadFile(kekPath)
	if err := os.WriteFile(loose, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"seed"}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	canary := "sk-CANARY-refused-9c1f"
	put := func(org, key, kek string) []string {
		return []string{"keys", "put", "--org", org, "--key", key, "--kek-file", kek}
	}
	// Each refusal is for its own reason: want is a part of its message.
	cases := map[string]struct {
		args []string
		want string
	}{
		"openbao key":     {put("org_acme", "k_bao1", kekPath), "is not an active Local key"},
		"unknown key":     {put("org_acme", "k_nope", kekPath), "no key k_nope in organization org_acme"},
		"other org":       {put("org_nw", "k_stripe", kekPath), "no key k_stripe in organization org_nw"},
		"loose KEK file":  {put("org_acme", "k_stripe", loose), "must be 0600 or stricter"},
		"missing KEK":     {put("org_acme", "k_stripe", filepath.Join(dir, "none")), "no such file"},
		"no org":          {[]string{"keys", "put", "--key", "k_stripe", "--kek-file", kekPath}, "usage: keyhole keys put"},
		"value in argv":   {append(put("org_acme", "k_stripe", kekPath), canary), "usage: keyhole keys put"},
		"value flag":      {append(put("org_acme", "k_stripe", kekPath), "--value", canary), "unknown flag --value"},
		"not put":         {[]string{"keys", "get", "--org", "org_acme", "--key", "k_stripe"}, "usage: keyhole keys put"},
		"kek over a file": {[]string{"kek", "generate", "--out", kekPath}, "file exists"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(ctx, c.args, strings.NewReader(canary+"\n"), &out, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if strings.Contains(err.Error()+out.String(), "CANARY-refused") {
				t.Fatalf("the value reached the error or output: %v / %q", err, out.String())
			}
		})
	}
	kek, _ := envelope.LoadKEK(kekPath)
	if _, _, err := storetest.Open(t, b).LocalValue(ctx, kek, "org_acme", "k_stripe"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused put stored a value for k_stripe: %v", err)
	}
}

func TestReadValue(t *testing.T) {
	ok := map[string]string{
		"plain":            "abc",
		"one newline":      "abc\n",
		"crlf":             "abc\r\n",
		"only one dropped": "abc\n\n",
		"lone cr kept":     "abc\r",
		"inner newline":    "a\nb\n",
		"max":              strings.Repeat("x", envelope.MaxValue) + "\n",
	}
	want := map[string]string{
		"plain": "abc", "one newline": "abc", "crlf": "abc", "only one dropped": "abc\n",
		"lone cr kept": "abc\r", "inner newline": "a\nb", "max": strings.Repeat("x", envelope.MaxValue),
	}
	for name, in := range ok {
		got, err := readValue(strings.NewReader(in))
		if err != nil || string(got) != want[name] {
			t.Errorf("%s: readValue = %q, %v; want %q", name, got, err, want[name])
		}
	}
	for name, in := range map[string]string{
		"empty":        "",
		"just newline": "\n",
		"just crlf":    "\r\n",
		"over max":     strings.Repeat("x", envelope.MaxValue+1),
		"over max nl":  strings.Repeat("x", envelope.MaxValue+1) + "\r\n",
		"far over":     strings.Repeat("x", 4*envelope.MaxValue),
	} {
		if got, err := readValue(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted %d bytes", name, len(got))
		}
	}
}

func TestLoadRefusesSecretTables(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	dir := t.TempDir()
	for _, table := range []string{"local_key_values", "users", "identities", "sessions", "enrollment_tokens",
		"issuer_keys", "audit_events", "mfa_factors", "org_settings"} {
		t.Run(table, func(t *testing.T) {
			p := filepath.Join(dir, table+".json")
			fx := `{"description":"x","tables":[{"table":"orgs","rows":[{"id":"org_x","name":"X","status":"active",
				"audit_retention_days":365,"created_at":"@now","created_by_kind":"system","created_by_id":"t","updated_at":"@now","rev":0}]},
				{"table":"` + table + `","rows":[{"id":"x"}]}]}`
			if err := os.WriteFile(p, []byte(fx), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := run(ctx, []string{"load", "--file", p}, nil, &bytes.Buffer{}, nil); err == nil ||
				!strings.Contains(err.Error(), "can't be loaded") {
				t.Fatalf("err = %v, want a refusal", err)
			}
		})
	}
	if has, err := storetest.Open(t, b).HasOrgs(ctx); err != nil || has {
		t.Fatalf("a refused fixture loaded rows (orgs present: %v, %v)", has, err)
	}
	if err := run(ctx, []string{"load"}, nil, &bytes.Buffer{}, nil); err == nil {
		t.Fatal("load with no file succeeded")
	}
}

// openPTY returns the follower side of a new pseudo-terminal.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	leader, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx here: %v", err)
	}
	t.Cleanup(func() { leader.Close() })
	fd := int(leader.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	f, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open the pty follower: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestReadValueRefusesATerminal(t *testing.T) {
	tty := openPTY(t)
	if got, err := readValue(tty); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("readValue(terminal) = %q, %v; want a refusal", got, err)
	}
}

// endless is a stdin that never ends.
type endless struct{ n int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	e.n += len(p)
	return len(p), nil
}

func TestReadValueStopsAtTheLimit(t *testing.T) {
	in := &endless{}
	done := make(chan error, 1)
	go func() { _, err := readValue(in); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "over") {
			t.Fatalf("an endless stdin: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readValue kept reading an endless stdin")
	}
	if in.n > envelope.MaxValue+3 {
		t.Fatalf("read %d bytes of an endless stdin; the limit is %d", in.n, envelope.MaxValue+3)
	}
}

func TestPutErrorMessages(t *testing.T) {
	other := errors.New("disk on fire")
	for err, want := range map[error]string{
		store.ErrNotFound: "no key k in organization o",
		store.ErrExists:   "another put of key k ran at the same time",
		store.ErrNotLocal: "key k is not an active Local key",
		other:             "disk on fire",
	} {
		if got := putError(fmt.Errorf("wrapped: %w", err), "o", "k"); !strings.Contains(got.Error(), want) {
			t.Errorf("putError(%v) = %q, want it to contain %q", err, got, want)
		}
	}
}

func TestGrantsCommand(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	if err := run(ctx, []string{"load", "--file", litellmFixture}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	g := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, append([]string{"grants"}, args...), nil, &out, nil)
		return out.String(), err
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"disable", "--org", "org_team", "--grant", "gr_team_litellm"}, "grant gr_team_litellm disabled\n"},
		{[]string{"disable", "--org", "org_team", "--grant", "gr_team_litellm"}, "grant gr_team_litellm already disabled; nothing changed\n"},
		{[]string{"enable", "--org", "org_team", "--grant", "gr_team_litellm"}, "grant gr_team_litellm enabled\n"},
		{[]string{"enable", "--org", "org_team", "--grant", "gr_team_litellm"}, "grant gr_team_litellm already enabled; nothing changed\n"},
	} {
		if out, err := g(c.args...); err != nil || out != c.want {
			t.Errorf("%v: %q, %v; want %q", c.args, out, err, c.want)
		}
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"unknown grant": {[]string{"disable", "--org", "org_team", "--grant", "gr_nope"}, "no grant gr_nope in organization org_team"},
		"no verb":       {nil, "usage: keyhole grants"},
		"other verb":    {[]string{"delete", "--org", "org_team", "--grant", "gr_team_litellm"}, "usage: keyhole grants"},
		"no org":        {[]string{"disable", "--grant", "gr_team_litellm"}, "usage: keyhole grants"},
		"extra arg":     {[]string{"disable", "--org", "org_team", "--grant", "gr_team_litellm", "x"}, "usage: keyhole grants"},
	} {
		if _, err := g(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestMembersCommand(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	if err := run(ctx, []string{"load", "--file", litellmFixture}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	m := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, append([]string{"members"}, args...), nil, &out, nil)
		return out.String(), err
	}
	add := []string{"add", "--org", "org_team", "--ws", "ws_team", "--sub", "usr_eee6"}
	rm := []string{"remove", "--org", "org_team", "--ws", "ws_team", "--sub", "usr_eee6"}
	for _, c := range []struct {
		args []string
		want string
	}{
		{add, "user usr_eee6 added to workspace ws_team\n"},
		{add, "user usr_eee6 is already a member of workspace ws_team; nothing changed\n"},
		{rm, "user usr_eee6 removed from workspace ws_team\n"},
		{rm, "user usr_eee6 is not a member of workspace ws_team; nothing changed\n"},
		{append(add[:len(add):len(add)], "--kind", "agent"), "agent usr_eee6 added to workspace ws_team\n"},
		{append(rm[:len(rm):len(rm)], "--kind", "agent"), "agent usr_eee6 removed from workspace ws_team\n"},
	} {
		if out, err := m(c.args...); err != nil || out != c.want {
			t.Errorf("%v: %q, %v; want %q", c.args, out, err, c.want)
		}
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"unknown ws": {[]string{"add", "--org", "org_team", "--ws", "ws_nope", "--sub", "u"}, "no active workspace ws_nope in organization org_team"},
		"bad kind":   {append(add[:len(add):len(add)], "--kind", "admin"), "--kind must be user or agent"},
		"bad sub":    {[]string{"add", "--org", "org_team", "--ws", "ws_team", "--sub", "a/b"}, "--sub an id"},
		"no verb":    {nil, "usage: keyhole members"},
		"other verb": {[]string{"list", "--org", "org_team", "--ws", "ws_team", "--sub", "usr_eee6"}, "usage: keyhole members"},
		"no sub":     {[]string{"add", "--org", "org_team", "--ws", "ws_team"}, "usage: keyhole members"},
		"extra arg":  {append(add[:len(add):len(add)], "x"), "usage: keyhole members"},
	} {
		if _, err := m(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestToolsPrivateAllowCommand(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	ctx := context.Background()
	if err := run(ctx, []string{"load", "--file", litellmFixture}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	pa := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, append([]string{"tools", "private-allow", "--org", "org_team", "--tool", "t_litellm"}, args...), nil, &out, nil)
		return out.String(), err
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--cidr", "192.168.10.90"}, "tool t_litellm version 2 published: private addresses allowed: 192.168.10.90/32\n"},
		{[]string{"--cidr", "192.168.10.90/32"}, "tool t_litellm version 2 already allows 192.168.10.90/32; nothing changed\n"},
		{[]string{"--cidr", "10.20.0.0/16", "--cidr", "192.168.10.90"}, "tool t_litellm version 3 published: private addresses allowed: 10.20.0.0/16, 192.168.10.90/32\n"},
		{[]string{"--none"}, "tool t_litellm version 4 published: private addresses allowed: none\n"},
	} {
		if out, err := pa(c.args...); err != nil || out != c.want {
			t.Errorf("%v: %q, %v; want %q", c.args, out, err, c.want)
		}
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a /0":          {[]string{"--cidr", "0.0.0.0/0"}, "--cidr: store: a privateAllow entry"},
		"a name":        {[]string{"--cidr", "litellm.example.invalid"}, "--cidr: store: a privateAllow entry"},
		"both":          {[]string{"--cidr", "10.0.0.0/8", "--none"}, "usage: keyhole tools private-allow"},
		"neither":       {nil, "usage: keyhole tools private-allow"},
		"an extra word": {[]string{"--none", "x"}, "usage: keyhole tools private-allow"},
	} {
		if _, err := pa(c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	var out bytes.Buffer
	err := run(ctx, []string{"tools", "private-allow", "--org", "org_team", "--tool", "t_nope", "--none"}, nil, &out, nil)
	if err == nil || !strings.Contains(err.Error(), "no published tool t_nope in organization org_team") {
		t.Errorf("unknown tool: %v", err)
	}
	if err := run(ctx, []string{"tools", "list"}, nil, &out, nil); err == nil || !strings.Contains(err.Error(), "usage: keyhole tools private-allow") {
		t.Errorf("another verb: %v", err)
	}
}
