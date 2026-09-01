package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/cli"
	"loadout.dev/loadout/internal/vault"
)

// TestAppendAccessLogWritesOneJSONLine proves AppendAccessLog writes
// exactly one JSON line per call, with every AccessEntry field
// present and no value field ever in the line.
func TestAppendAccessLogWritesOneJSONLine(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	entry := cli.AccessEntry{At: "2026-08-31T00:00:00Z", Verb: "show", Secret: "test-key", Tool: "human"}
	if err := cli.AppendAccessLog(v, entry); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), data)
	}
	if strings.Contains(lines[0], `"value"`) {
		t.Fatalf("an access-log line must never carry a value field, got %q", lines[0])
	}
	var got cli.AccessEntry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got != entry {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}

// TestAppendAccessLogAppendsRatherThanOverwrites proves three calls
// produce three lines, in order, none clobbering another.
func TestAppendAccessLogAppendsRatherThanOverwrites(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		entry := cli.AccessEntry{At: "t", Verb: "show", Secret: "k", Tool: "human"}
		if err := cli.AppendAccessLog(v, entry); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), data)
	}
}

// TestAppendAccessLogFileMode0600 proves access.log is written mode
// 0600: it is a device-local record, never meant for other users on
// the same machine to read.
func TestAppendAccessLogFileMode0600(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	v, err := vault.Open(filepath.Join(base, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.AppendAccessLog(v, cli.AccessEntry{At: "t", Verb: "show", Secret: "k", Tool: "human"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(base, "vault", "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("access.log must be mode 0600, got %o", fi.Mode().Perm())
	}
}
