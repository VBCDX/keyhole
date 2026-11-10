package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every environment variable read anywhere in the repo (Go code, the Vite
// config, docker-compose.yml) must be listed in .env.example.
func TestEnvExampleListsEveryVariable(t *testing.T) {
	root := filepath.Join("..", "..")
	example, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)=`).FindAllStringSubmatch(string(example), -1) {
		listed[m[1]] = true
	}

	readers := []*regexp.Regexp{
		regexp.MustCompile(`(?:[Gg]etenv|get|Setenv)\("([A-Z][A-Z0-9_]*)"`), // Go
		regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]*)`),               // web/vite.config.ts
		regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)[:}?-]`),                   // docker-compose.yml
	}
	found := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "mocks" || d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !(strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "vite.config.ts") || strings.HasSuffix(path, "docker-compose.yml")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range readers {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				found[m[1]] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 15 {
		t.Fatalf("found only %d variables; the scan is broken: %v", len(found), found)
	}
	for name, path := range found {
		if !listed[name] {
			t.Errorf("%s is read in %s but missing from .env.example", name, path)
		}
	}
}
