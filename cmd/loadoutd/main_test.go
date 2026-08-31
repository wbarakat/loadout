package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsRequiresData(t *testing.T) {
	var stderr bytes.Buffer
	_, _, ok := parseArgs([]string{"-addr", ":9999"}, &stderr)
	if ok {
		t.Fatal("expected parseArgs to refuse when -data is missing")
	}
	if !strings.Contains(stderr.String(), "-data is required") {
		t.Fatalf("expected a clear error naming -data, got %q", stderr.String())
	}
}

func TestParseArgsDefaultsAddr(t *testing.T) {
	var stderr bytes.Buffer
	dataDir, addr, ok := parseArgs([]string{"-data", "/tmp/whatever"}, &stderr)
	if !ok {
		t.Fatalf("expected parseArgs to accept -data alone, stderr: %s", stderr.String())
	}
	if dataDir != "/tmp/whatever" {
		t.Fatalf("unexpected dataDir %q", dataDir)
	}
	if addr != ":7777" {
		t.Fatalf("expected default addr :7777, got %q", addr)
	}
}

func TestNewServerFirstRunPrintsTokenOnce(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	if _, err := newServer(dir, ":7777", &stdout); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "loadoutd: generated an access token: ") {
		t.Fatalf("expected the token-generation line on first run, got %q", out)
	}
	if strings.Contains(out, "listening on") {
		t.Fatalf("did not expect the listening line on first run, got %q", out)
	}
}

func TestNewServerLaterRunPrintsAddrWithoutToken(t *testing.T) {
	dir := t.TempDir()
	var first bytes.Buffer
	if _, err := newServer(dir, ":7777", &first); err != nil {
		t.Fatal(err)
	}
	// Extract the token that was printed, to confirm it never repeats.
	tokenData, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenData))

	var second bytes.Buffer
	if _, err := newServer(dir, ":8888", &second); err != nil {
		t.Fatal(err)
	}
	out := second.String()
	if !strings.Contains(out, "loadoutd: listening on :8888") {
		t.Fatalf("expected the listening line on a later run, got %q", out)
	}
	if strings.Contains(out, token) {
		t.Fatal("the access token must never be printed on a later run")
	}
}

func TestNewServerRefusesUnwritableDataDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	roParent := filepath.Join(dir, "ro")
	if err := os.Mkdir(roParent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roParent, 0o700) })

	var stdout bytes.Buffer
	_, err := newServer(filepath.Join(roParent, "data"), ":7777", &stdout)
	if err == nil {
		t.Fatal("expected newServer to refuse an unwritable data directory")
	}
}
