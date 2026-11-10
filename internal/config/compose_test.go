package config

import (
	"os"
	"regexp"
	"testing"
)

// TestComposePostgresHealthcheckUsesTCP pins: the healthcheck that
// gates Keyhole's start must not pass against the image's temporary init
// server, which listens on the unix socket only (listen_addresses is empty).
func TestComposePostgresHealthcheckUsesTCP(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	test := regexp.MustCompile(`(?m)^\s+test: \[.*"(pg_isready[^"]*)"\]`).FindSubmatch(raw)
	if test == nil {
		t.Fatal("no pg_isready healthcheck in docker-compose.yml")
	}
	if !regexp.MustCompile(`(^|\s)-h (127\.0\.0\.1|localhost)(\s|$)`).Match(test[1]) {
		t.Fatalf("the postgres healthcheck %q checks the unix socket: it passes during init", test[1])
	}
	// Keyhole waits for that healthcheck, not just for the container to start.
	if !regexp.MustCompile(`(?s)depends_on:\s+postgres:\s+condition: service_healthy`).Match(raw) {
		t.Fatal("keyhole doesn't wait for postgres to be healthy")
	}
}
