package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/cli"
)

// run points the vault and the home at temp dirs, then runs the CLI.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(&out, &errOut, args)
	return out.String(), errOut.String(), code
}

func setupEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault"))
	os.MkdirAll(filepath.Join(base, "home"), 0o755)
	return base
}

func TestUsage(t *testing.T) {
	setupEnv(t)
	_, errOut, code := run(t)
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	_, errOut, code = run(t, "bogus")
	if code != 2 || !strings.Contains(errOut, "bogus") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestInitAndAdd(t *testing.T) {
	base := setupEnv(t)
	out, errOut, code := run(t, "init")
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if !strings.Contains(out, filepath.Join(base, "vault")) {
		t.Fatalf("init must print the vault path, got %q", out)
	}
	if _, _, code := run(t, "add", "skill", "deploy-checks"); code != 0 {
		t.Fatal("add skill failed")
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "skills", "deploy-checks", "SKILL.md")); err != nil {
		t.Fatal("the skill file is missing")
	}
	if _, _, code := run(t, "add", "memory", "my-stack"); code != 0 {
		t.Fatal("add memory failed")
	}
	if _, errOut, code := run(t, "add", "wat", "x"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("bad kind must be a usage error, got %d %q", code, errOut)
	}
}
