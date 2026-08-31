package cli_test

import (
	"bytes"
	"os"
	"os/exec"
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

func TestAddByDefaultsToHumanAndIsKept(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "add", "memory", "my-stack"); code != 0 {
		t.Fatalf("add memory failed: %s", errOut)
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "memory", "my-stack.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "by: human") || !strings.Contains(string(data), "review: kept") {
		t.Fatalf("a default add must be by human and kept, got:\n%s", data)
	}
}

func TestAddByFlagRecordsDraftProvenance(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "add", "memory", "x", "--by", "pi"); code != 0 {
		t.Fatalf("add memory failed: %s", errOut)
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "memory", "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "by: pi") {
		t.Fatalf("the file must hold by: pi, got:\n%s", text)
	}
	if !strings.Contains(text, "review: draft") {
		t.Fatalf("a non-human add must start as draft, got:\n%s", text)
	}
}

func TestAddByFlagAppliesToSkillsToo(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "add", "skill", "deploy-checks", "--by", "pi"); code != 0 {
		t.Fatalf("add skill failed: %s", errOut)
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "skills", "deploy-checks", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "by: pi") || !strings.Contains(string(data), "review: draft") {
		t.Fatalf("bad provenance for skill add: %s", data)
	}
}

func TestAddByFlagRejectsMalformedFlag(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "add", "memory", "x", "--by"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("a --by flag without a value must be a usage error, got %d %q", code, errOut)
	}
	if _, errOut, code := run(t, "add", "memory", "x", "--nope", "pi"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("an unknown flag must be a usage error, got %d %q", code, errOut)
	}
}

func TestAddIsTransactionalWhenHistoryFails(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	vaultRoot := filepath.Join(base, "vault")
	if err := os.RemoveAll(filepath.Join(vaultRoot, ".git")); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := run(t, "add", "skill", "deploy-checks")
	if code != 1 {
		t.Fatalf("add must fail without history, got %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("the skill must not remain on disk after a failed add")
	}

	_, errOut, code = run(t, "add", "memory", "my-stack")
	if code != 1 {
		t.Fatalf("add must fail without history, got %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "memory", "my-stack.md")); !os.IsNotExist(err) {
		t.Fatal("the fact must not remain on disk after a failed add")
	}

	// Restore history; a retry must now succeed.
	if out, err := exec.Command("git", "-C", vaultRoot, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v: %s", err, out)
	}
	_, errOut, code = run(t, "add", "skill", "deploy-checks")
	if code != 0 {
		t.Fatalf("retry must succeed, got %d %q", code, errOut)
	}
}

func TestSyncProjectsVault(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync failed: %s", errOut)
	}
	if !strings.Contains(out, "claude-code") || !strings.Contains(out, "pi") {
		t.Fatalf("sync must name each adapter, got %q", out)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Readlink(filepath.Join(home, ".claude", "skills", "deploy-checks")); err != nil {
		t.Fatal("the Claude Code skill link is missing")
	}
	if _, err := os.Readlink(filepath.Join(home, ".pi", "agent", "skills", "deploy-checks")); err != nil {
		t.Fatal("the pi skill link is missing")
	}
	for _, f := range []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".pi", "agent", "AGENTS.md")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
}

func TestSyncLinksSymlinkedSkillDir(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	vaultRoot := filepath.Join(base, "vault")
	realDir := filepath.Join(vaultRoot, "skills", "deploy-checks")
	aliasDir := filepath.Join(vaultRoot, "skills", "deploy-checks-alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync failed: %s", errOut)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Readlink(filepath.Join(home, ".claude", "skills", "deploy-checks-alias")); err != nil {
		t.Fatal("the symlinked alias must also be linked into the tool")
	}
}

func TestSyncContinuesAfterAdapterFailure(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	home := filepath.Join(base, "home")
	// A real directory occupies the Claude Code skill link path, so
	// the claude-code adapter must fail to apply.
	blockedPath := filepath.Join(home, ".claude", "skills", "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "sync")
	if code != 1 {
		t.Fatalf("sync must exit 1 when an adapter fails, got %d", code)
	}
	if !strings.Contains(errOut, "claude-code") {
		t.Fatalf("sync must report the claude-code failure, got %q", errOut)
	}
	if !strings.Contains(out, "synced pi") {
		t.Fatalf("sync must still sync pi, got %q", out)
	}
	if _, err := os.Readlink(filepath.Join(home, ".pi", "agent", "skills", "deploy-checks")); err != nil {
		t.Fatal("pi must still get its skill link despite the claude-code failure")
	}
}

func TestSyncExitsOneOnBlocked(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	home := filepath.Join(base, "home")
	// A real directory occupies the pi skill link path, so the pi
	// adapter must report the skill as blocked.
	blockedPath := filepath.Join(home, ".pi", "agent", "skills", "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "sync")
	if code != 1 {
		t.Fatalf("sync must exit 1 when a skill is blocked, got %d", code)
	}
	if !strings.Contains(errOut, blockedPath) {
		t.Fatalf("errOut must name the blocked address, got %q", errOut)
	}
	if !strings.Contains(out, "synced pi (0 linked, 0 pruned)") {
		t.Fatalf("sync must report zero linked skills for pi despite the memory write, got %q", out)
	}
}

func TestStatusAndDoctor(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	// Before sync: doctor must report problems and exit 1.
	out, _, code := run(t, "doctor")
	if code != 1 || !strings.Contains(out, "loadout sync") {
		t.Fatalf("doctor before sync: code=%d out=%q", code, out)
	}

	run(t, "sync")

	out, _, code = run(t, "status")
	if code != 0 {
		t.Fatal("status failed")
	}
	if !strings.Contains(out, "skills: 1") || !strings.Contains(out, "memory facts: 1") {
		t.Fatalf("bad status: %q", out)
	}
	if !strings.Contains(out, "claude-code: in sync") || !strings.Contains(out, "pi: in sync") {
		t.Fatalf("bad adapter status: %q", out)
	}

	out, _, code = run(t, "doctor")
	if code != 0 || !strings.Contains(out, "all good") {
		t.Fatalf("doctor after sync: code=%d out=%q", code, out)
	}
}

func TestDoctorReportsEmbeddedGitRepo(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	dir := filepath.Join(base, "vault", "skills", "deploy-checks")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem, got %d", code)
	}
	if !strings.Contains(out, dir) || !strings.Contains(out, "git repository") {
		t.Fatalf("doctor must name the embedded repo, got %q", out)
	}
}

// appendManifestKey adds a raw TOML line to the vault's manifest, so
// tests can plant an unknown key for the warning path.
func appendManifestKey(t *testing.T, base, line string) {
	t.Helper()
	path := filepath.Join(base, "vault", "loadout.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(line)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncPrintsManifestWarnings(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	appendManifestKey(t, base, "enable = true\n")

	_, errOut, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync failed: %s", errOut)
	}
	if !strings.Contains(errOut, "unknown") {
		t.Fatalf("sync must print the manifest warning, got %q", errOut)
	}
}

func TestStatusPrintsManifestWarnings(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	appendManifestKey(t, base, "enable = true\n")

	_, errOut, code := run(t, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", errOut)
	}
	if !strings.Contains(errOut, "unknown") {
		t.Fatalf("status must print the manifest warning, got %q", errOut)
	}
}

func TestDoctorPrintsManifestWarnings(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	appendManifestKey(t, base, "enable = true\n")

	_, errOut, _ := run(t, "doctor")
	if !strings.Contains(errOut, "unknown") {
		t.Fatalf("doctor must print the manifest warning, got %q", errOut)
	}
}

func TestDoctorReportsMissingHistory(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if err := os.RemoveAll(filepath.Join(base, "vault", ".git")); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem, got %d", code)
	}
	if !strings.Contains(out, "the vault history is missing") {
		t.Fatalf("doctor must report the missing history, got %q", out)
	}
}
