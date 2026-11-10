package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The org-id rule: a store resolves only under the
// control plane's own org id, and the example fixture's org_team is only this
// repository's test id. Every place a stranger meets the fixture or `keys put
// --org` says so, and says how to read the id.
func TestDocsStateTheOrgIDRule(t *testing.T) {
	// Whitespace-normalized, so a re-wrapped paragraph still matches.
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ")
	}
	store := read("../../docs/local-store.md")
	for _, want := range []string{
		"## The org id: the control plane's",
		"**the control plane's own org id**",
		"It's the id, not the slug",
		"`data.org.id`",
		"`sidecars list --org SLUG`",
		"`docs/examples/example-litellm.json` uses `org_example`: replace it with yours (every `org_id`, and the `orgs` row's `id`)",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("docs/local-store.md lacks %q", want)
		}
	}
	// The rule comes before the first command that loads rows or puts a value.
	if i, j := strings.Index(store, "## The org id"), strings.Index(store, "keyhole load --file"); i < 0 || j < 0 || i > j {
		t.Errorf("the org-id section must come before the first load (at %d, %d)", i, j)
	}
	// The documented example's own description and its sibling README.
	var fx struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(read("../../docs/examples/example-litellm.json")), &fx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fx.Description, "Replace org_example") || !strings.Contains(fx.Description, "control plane's own org id") {
		t.Errorf("example-litellm.json's description doesn't say to replace org_example: %q", fx.Description)
	}
	if ex := read("../../docs/examples/README.md"); !strings.Contains(ex, "**Its org id is `org_example`.**") ||
		!strings.Contains(ex, "replace `org_example`") {
		t.Errorf("docs/examples/README.md doesn't state the rule:\n%s", ex)
	}
}

// The documented example is a real fixture: it loads into an empty store,
// and says nothing of this deployment.
func TestDocumentedExampleLoadsAndIsGeneric(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATA_DIR", t.TempDir())
	var out bytes.Buffer
	if err := run(context.Background(), []string{"load", "--file", "../../docs/examples/example-litellm.json"}, nil, &out, nil); err != nil {
		t.Fatalf("load: %v (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "loaded") {
		t.Fatalf("output %q", out.String())
	}
	raw, err := os.ReadFile("../../docs/examples/example-litellm.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"192.168."} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(bad)) {
			t.Errorf("the example names %q", bad)
		}
	}
	// The card id as a word, not any "K1…" substring.
	if m := regexp.MustCompile(`\bK1\b`).FindString(string(raw)); m != "" {
		t.Errorf("the example names the card %q", m)
	}
}

// What a public reader reads names no private tracker item or internal
// ruling: every README and docs/ page says what is true, self-contained.
// Code and test comments may keep their references.
func TestUserFacingDocsCarryNoTrackerRefs(t *testing.T) {
	ref := regexp.MustCompile(`(?i)\b(keyhole|vbcdx|vbcdx-core|dispatch|development)#[0-9]+|\(#[0-9]+\)|\bc1[0-9]{5}\b|\bcard K[0-9]|master's ruling|ready campaign`)
	// Every Markdown file in the repository (READMEs anywhere, docs/) and the
	// documented examples, except test data and installed dependencies.
	var files []string
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".md") || strings.Contains(filepath.ToSlash(p), "docs/examples/")) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The shipped config a user copies or reads: the env template, the unit, compose.
	// The shipped config a user copies or installs, found, not listed, so a new
	// one is covered: every *.env.example, everything under
	// deploy/, and the root's compose files and Dockerfiles.
	var config []string
	err = filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel("../..", p)
		rel = filepath.ToSlash(rel)
		base := filepath.Base(rel)
		top := !strings.Contains(rel, "/")
		if strings.HasSuffix(base, ".env.example") || strings.HasPrefix(rel, "deploy/") ||
			(top && (strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "Dockerfile"))) {
			config = append(config, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".env.example", "deploy/systemd/keyhole.service", "deploy/systemd/keyhole.env.example", "docker-compose.yml"} {
		if !slices.ContainsFunc(config, func(p string) bool { return strings.HasSuffix(filepath.ToSlash(p), "/"+want) }) {
			t.Fatalf("the config walk missed %s: %v", want, config)
		}
	}
	files = append(files, config...)
	if len(files) < 8 {
		t.Fatalf("found only %d files: the walk is wrong", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := ref.FindString(line); m != "" {
				t.Errorf("%s:%d names %q", f, i+1, m)
			}
		}
	}
	// In the shipped config nothing legitimate is a bare card or phase id
	// (P2, K1): those mean something only to the private tracker.
	phase := regexp.MustCompile(`\b[PK][0-9]\b`)
	for _, f := range config {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := phase.FindString(line); m != "" {
				t.Errorf("%s:%d names the card/phase id %q", f, i+1, m)
			}
		}
	}
}
