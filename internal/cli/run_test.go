package cli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/cli"
	"loadout.dev/loadout/internal/vault"
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

func TestSyncDryRunFreshVaultLeavesHomeUntouched(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "would sync claude-code (1 to link, 0 to prune; memory: block would change)") {
		t.Fatalf("bad dry-run output: %q", out)
	}
	if !strings.Contains(out, "would sync pi (1 to link, 0 to prune; memory: block would change)") {
		t.Fatalf("bad dry-run output: %q", out)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create CLAUDE.md")
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the claude-code skill link")
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the pi memory file")
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "render", "memory.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write the rendered memory file")
	}
	logOut, _, _ := run(t, "log")
	lines := strings.Split(strings.TrimRight(logOut, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("a dry run must not snapshot, got log %q", logOut)
	}
}

func TestSyncDryRunAfterRealSyncReportsUpToDate(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	run(t, "sync")

	out, errOut, code := run(t, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "would sync claude-code (0 to link, 0 to prune; memory: up to date)") {
		t.Fatalf("bad dry-run output: %q", out)
	}
	if !strings.Contains(out, "would sync pi (0 to link, 0 to prune; memory: up to date)") {
		t.Fatalf("bad dry-run output: %q", out)
	}
}

func TestSyncDryRunReportsBlockedPathAndExitsZero(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	home := filepath.Join(base, "home")
	blockedPath := filepath.Join(home, ".claude", "skills", "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --dry-run must exit 0 despite a blocked skill, got %d (%s)", code, errOut)
	}
	if !strings.Contains(errOut, blockedPath) {
		t.Fatalf("errOut must name the blocked path in the dry report, got %q", errOut)
	}
	if !strings.Contains(out, "would sync claude-code (0 to link, 0 to prune; memory: block would change)") {
		t.Fatalf("bad dry-run output: %q", out)
	}
}

func TestSyncDryRunReportsDamagedMarksAndExitsOne(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	home := filepath.Join(base, "home")
	claudeMd := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two begin marks: damaged, the same way WriteManagedBlock refuses
	// to touch it on a real sync.
	corrupted := "<!-- loadout:begin -->\na\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(claudeMd, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "sync", "--dry-run")
	if code != 1 {
		t.Fatalf("sync --dry-run on a damaged file must exit 1, got %d (out=%q err=%q)", code, out, errOut)
	}
	if !strings.Contains(errOut, claudeMd) || !strings.Contains(errOut, "damaged") {
		t.Fatalf("errOut must name the damaged file, got %q", errOut)
	}
	data, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != corrupted {
		t.Fatal("a dry run must not touch a damaged file, even a byte")
	}
}

func TestSyncDryRunFlagAcceptedInAnyPosition(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	out, errOut, code := run(t, "sync", "--json", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --json --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, `"dry_run": true`) {
		t.Fatalf("--dry-run before --json must still be recognized, got %q", out)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create CLAUDE.md")
	}
}

// TestSyncDryRunMemoryNoneAdapterOmitsMemoryClause proves a memoryNone
// adapter's dry line carries no dangling "; memory: " clause: cursor
// never reports a memory status, so the line must read exactly
// "would sync cursor (N to link, M to prune)", with no clause at all.
func TestSyncDryRunMemoryNoneAdapterOmitsMemoryClause(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	setAdapterConfig(t, base, "cursor", vault.AdapterConfig{
		Enabled:   true,
		SkillsDir: "~/.cursor/skills",
	})

	out, errOut, code := run(t, "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "would sync cursor (1 to link, 0 to prune)\n") {
		t.Fatalf("a memoryNone adapter's dry line must omit the memory clause, got %q", out)
	}
	if strings.Contains(out, "would sync cursor (1 to link, 0 to prune;") {
		t.Fatalf("a memoryNone adapter's dry line must carry no memory clause at all, got %q", out)
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

// TestDoctorReportsMemoryFileIgnoredOnMemoryNoneAdapter proves doctor
// surfaces a memory_file set on a memoryNone adapter such as cursor:
// otherwise loadout would silently ignore a setting the user expects
// to take effect.
func TestDoctorReportsMemoryFileIgnoredOnMemoryNoneAdapter(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	setAdapterConfig(t, base, "cursor", vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  "~/.cursor/skills",
		MemoryFile: "~/.cursor/MEMORY.md",
	})

	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem, got %d", code)
	}
	if !strings.Contains(out, "the adapter cursor takes no memory_file; loadout ignores it.") {
		t.Fatalf("doctor must name the ignored memory_file, got %q", out)
	}
	if !strings.Contains(out, "remove adapters.cursor.memory_file, or use the agents-md adapter for extra instruction files.") {
		t.Fatalf("doctor must print the fix, got %q", out)
	}
}

// TestDoctorReportsStaleLinkAfterSkillDeleted proves the orphan scan:
// deleting a skill folder after a sync leaves its symlink behind in
// every adapter's skills directory, and doctor must now catch that —
// this was invisible before. A second sync prunes the stale links,
// and doctor goes quiet again.
func TestDoctorReportsStaleLinkAfterSkillDeleted(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "sync")

	if err := os.RemoveAll(filepath.Join(base, "vault", "skills", "deploy-checks")); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "doctor")
	if code != 1 || !strings.Contains(out, "stale link") || !strings.Contains(out, "loadout sync") {
		t.Fatalf("doctor must report the stale link, got code=%d out=%q", code, out)
	}

	run(t, "sync")

	out, _, code = run(t, "doctor")
	if code != 0 || !strings.Contains(out, "all good") {
		t.Fatalf("doctor must go quiet after sync, got code=%d out=%q", code, out)
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

// setAdapterConfig overwrites one adapter's config in the vault
// manifest, so a test can enable an adapter beyond init's defaults.
func setAdapterConfig(t *testing.T, base, name string, cfg vault.AdapterConfig) {
	t.Helper()
	root := filepath.Join(base, "vault")
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	v.Manifest.Adapters[name] = cfg
	if err := vault.SaveManifest(filepath.Join(root, "loadout.toml"), v.Manifest); err != nil {
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

func TestShowPrintsFileRaw(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "my-stack")
	path := filepath.Join(base, "vault", "memory", "my-stack.md")
	content := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "show", "memory/my-stack")
	if code != 0 {
		t.Fatalf("show failed: %s", errOut)
	}
	if out != content {
		t.Fatalf("show must print the file raw, got %q", out)
	}
}

func TestShowMissingItemExitsOne(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "show", "memory/nope")
	if code != 1 {
		t.Fatalf("show on a missing item must exit 1, got %d", code)
	}
	want := "memory/nope: no such item. Fix: run loadout list.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

func TestShowRequiresAnAddress(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "show"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("show without an address must be a usage error, got %d %q", code, errOut)
	}
}

func TestShowRejectsBadAddress(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "show", "bogus")
	if code != 1 {
		t.Fatalf("a bad address must exit 1, got %d", code)
	}
	want := "bogus: not an address. Fix: use kind/name, for example memory/my-stack.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

// TestShowReportsUnreadableFileWithFixedMessage proves an unreadable
// item file gets the standard error grammar, naming the address and
// the fix, instead of a bare os.ReadFile error.
func TestShowReportsUnreadableFileWithFixedMessage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a file regardless of its permissions")
	}
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x")
	path := filepath.Join(base, "vault", "memory", "x.md")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o644)

	_, errOut, code := run(t, "show", "memory/x")
	if code != 1 {
		t.Fatalf("an unreadable item file must exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "memory/x: the item file cannot be read:") {
		t.Fatalf("bad error: %q", errOut)
	}
	if !strings.Contains(errOut, "Fix: check the file permissions.") {
		t.Fatalf("bad error: %q", errOut)
	}
}

func TestListShowsSkillsAndFactsInOrder(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	skillPath := filepath.Join(base, "vault", "skills", "deploy-checks", "SKILL.md")
	skillContent := "---\nname: deploy-checks\ndescription: run checks before a deploy\n---\n\nDo the thing.\n"
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	factPath := filepath.Join(base, "vault", "memory", "my-stack.md")
	factContent := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go.\n"
	if err := os.WriteFile(factPath, []byte(factContent), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "list")
	if code != 0 {
		t.Fatalf("list failed: %s", errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", out)
	}
	if lines[0] != "memory/my-stack — the stack I use" {
		t.Fatalf("bad first line: %q", lines[0])
	}
	if lines[1] != "skill/deploy-checks — run checks before a deploy" {
		t.Fatalf("bad second line: %q", lines[1])
	}
}

func TestListShowsNoDescriptionPlaceholder(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "my-stack")
	path := filepath.Join(base, "vault", "memory", "my-stack.md")
	content := "---\nname: my-stack\n---\n\nNo description here.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "list")
	if code != 0 {
		t.Fatalf("list failed: %s", errOut)
	}
	if !strings.Contains(out, "memory/my-stack — (no description)") {
		t.Fatalf("bad output: %q", out)
	}
}

func TestEditMissingAddressExitsOne(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "edit", "memory/nope")
	if code != 1 {
		t.Fatalf("edit on a missing item must exit 1, got %d", code)
	}
	want := "memory/nope: no such item. Fix: run loadout list.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

func TestEditRequiresAnAddress(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "edit"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("edit without an address must be a usage error, got %d %q", code, errOut)
	}
}

// TestEditReportsEditorSpawnFailureWithFixedMessage proves a failing
// editor gets the standard error grammar, naming the editor and the
// fix, instead of a bare exec error.
func TestEditReportsEditorSpawnFailureWithFixedMessage(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x")
	t.Setenv("EDITOR", "/no/such/editor-binary")

	_, errOut, code := run(t, "edit", "memory/x")
	if code != 1 {
		t.Fatalf("a failing editor must exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "/no/such/editor-binary: the editor did not start:") {
		t.Fatalf("bad error: %q", errOut)
	}
	if !strings.Contains(errOut, "Fix: set $EDITOR to a working editor.") {
		t.Fatalf("bad error: %q", errOut)
	}
}

func writeDeployChecksAndMyStack(t *testing.T, base string) {
	t.Helper()
	skillPath := filepath.Join(base, "vault", "skills", "deploy-checks", "SKILL.md")
	skillContent := "---\nname: deploy-checks\ndescription: run checks before a deploy\n---\n\nDo the thing.\n"
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	factPath := filepath.Join(base, "vault", "memory", "my-stack.md")
	factContent := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(factPath, []byte(factContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecallFindsFactByBodyWord(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	writeDeployChecksAndMyStack(t, base)

	out, errOut, code := run(t, "recall", "postgres")
	if code != 0 {
		t.Fatalf("recall failed: %s", errOut)
	}
	if strings.TrimRight(out, "\n") != "memory/my-stack — the stack I use" {
		t.Fatalf("bad output: %q", out)
	}
}

func TestRecallFindsSkillByName(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	writeDeployChecksAndMyStack(t, base)

	out, errOut, code := run(t, "recall", "deploy-checks")
	if code != 0 {
		t.Fatalf("recall failed: %s", errOut)
	}
	if strings.TrimRight(out, "\n") != "skill/deploy-checks — run checks before a deploy" {
		t.Fatalf("bad output: %q", out)
	}
}

func TestRecallMatchesEveryTerm(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	writeDeployChecksAndMyStack(t, base)

	// "deploy" matches the skill; "postgres" matches only the fact.
	// Both terms together must match nothing.
	out, errOut, code := run(t, "recall", "deploy", "postgres")
	if code != 0 {
		t.Fatalf("recall failed: %s", errOut)
	}
	if out != "no items match. Fix: run loadout list to see every item.\n" {
		t.Fatalf("bad output: %q", out)
	}
}

func TestRecallNoMatchPrintsFixedMessage(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "recall", "bogusterm")
	if code != 0 {
		t.Fatalf("recall failed: %s", errOut)
	}
	if out != "no items match. Fix: run loadout list to see every item.\n" {
		t.Fatalf("bad output: %q", out)
	}
}

func TestRecallRequiresATerm(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "recall"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("recall without a term must be a usage error, got %d %q", code, errOut)
	}
}

func TestContextPrintsCompactPicture(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	writeDeployChecksAndMyStack(t, base)

	out, errOut, code := run(t, "context")
	if code != 0 {
		t.Fatalf("context failed: %s", errOut)
	}
	vaultRoot := filepath.Join(base, "vault")
	if !strings.Contains(out, "vault: "+vaultRoot+" (1 skills, 1 facts)") {
		t.Fatalf("missing counts line: %q", out)
	}
	if !strings.Contains(out, "the stack I use") {
		t.Fatalf("missing memory hook: %q", out)
	}
	if !strings.Contains(out, "run checks before a deploy") {
		t.Fatalf("missing skill hook: %q", out)
	}
	if !strings.Contains(out, "add memory my-stack") {
		t.Fatalf("missing a known history subject: %q", out)
	}
	if !strings.HasSuffix(out, "next: loadout show <kind/name> reads one item; loadout recall <terms> searches.\n") {
		t.Fatalf("bad next line: %q", out)
	}
}

func TestLogShowsSubjectsNewestFirst(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "fact1")
	run(t, "add", "memory", "fact2")

	out, errOut, code := run(t, "log")
	if code != 0 {
		t.Fatalf("log failed: %s", errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", out)
	}
	if !strings.HasSuffix(lines[0], "  add memory fact2") {
		t.Fatalf("newest entry must come first, got %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "  add memory fact1") {
		t.Fatalf("bad second entry, got %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "  init the vault") {
		t.Fatalf("bad third entry, got %q", lines[2])
	}
}

func TestUndoRestoresPreviousVaultState(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "fact1")
	run(t, "add", "memory", "fact2")

	out, errOut, code := run(t, "undo")
	if code != 0 {
		t.Fatalf("undo failed: %s", errOut)
	}
	if out != "restored the previous vault state\nnext: run loadout sync to project it\n" {
		t.Fatalf("bad undo output: %q", out)
	}

	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "fact2.md")); !os.IsNotExist(err) {
		t.Fatal("the undone fact must be gone from disk")
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "fact1.md")); err != nil {
		t.Fatal("the first fact must survive undo")
	}

	logOut, _, _ := run(t, "log")
	if !strings.Contains(logOut, "undo") {
		t.Fatalf("log must gain an undo entry, got %q", logOut)
	}
}

func TestUndoOnFreshVaultErrorsWithFixedMessage(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	_, errOut, code := run(t, "undo")
	if code != 1 {
		t.Fatalf("undo on a fresh vault must exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "nothing to undo: the vault has no earlier state.") {
		t.Fatalf("bad error: %q", errOut)
	}
}

func TestReviewListsDraftItemsWithByAndAt(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")
	run(t, "add", "skill", "deploy-checks") // default human, kept: must not appear

	out, errOut, code := run(t, "review")
	if code != 0 {
		t.Fatalf("review failed: %s", errOut)
	}
	if strings.Contains(out, "deploy-checks") {
		t.Fatalf("a kept item must not appear in review, got %q", out)
	}
	if !strings.Contains(out, "memory/x") || !strings.Contains(out, "by pi") {
		t.Fatalf("review must list the draft with its by, got %q", out)
	}
	if !strings.Contains(out, "at 20") {
		t.Fatalf("review must list the draft with its at, got %q", out)
	}
}

func TestReviewNoDraftsPrintsFixedMessage(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x")

	out, errOut, code := run(t, "review")
	if code != 0 {
		t.Fatalf("review failed: %s", errOut)
	}
	if out != "no drafts. Every item is kept.\n" {
		t.Fatalf("bad output: %q", out)
	}
}

func TestReviewKeepFlipsFieldAndEmptiesTheList(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")

	_, errOut, code := run(t, "review", "keep", "memory/x")
	if code != 0 {
		t.Fatalf("review keep failed: %s", errOut)
	}

	data, err := os.ReadFile(filepath.Join(base, "vault", "memory", "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "review: kept") || strings.Contains(text, "review: draft") {
		t.Fatalf("review must flip to kept, got:\n%s", text)
	}
	if !strings.Contains(text, "by: pi") {
		t.Fatalf("keep must preserve every other line, got:\n%s", text)
	}

	out, errOut, code := run(t, "review")
	if code != 0 {
		t.Fatalf("review failed: %s", errOut)
	}
	if out != "no drafts. Every item is kept.\n" {
		t.Fatalf("a kept item must leave the draft list empty, got %q", out)
	}

	logOut, _, _ := run(t, "log")
	if !strings.Contains(logOut, "review keep memory/x") {
		t.Fatalf("keep must snapshot, got %q", logOut)
	}
}

func TestReviewKeepUnknownAddressExitsOne(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "review", "keep", "memory/nope")
	if code != 1 {
		t.Fatalf("keep of a missing address must exit 1, got %d", code)
	}
	want := "memory/nope: no such item. Fix: run loadout list.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

func TestReviewDropRemovesSkillFolder(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks", "--by", "pi")

	out, errOut, code := run(t, "review", "drop", "skill/deploy-checks")
	if code != 0 {
		t.Fatalf("review drop failed: %s", errOut)
	}
	if out != "dropped skill/deploy-checks\nnext: run loadout sync\n" {
		t.Fatalf("bad output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "skills", "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("the dropped skill folder must be gone")
	}

	logOut, _, _ := run(t, "log")
	if !strings.Contains(logOut, "review drop skill/deploy-checks") {
		t.Fatalf("drop must snapshot, got %q", logOut)
	}
}

func TestReviewDropRemovesFactFile(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")

	out, errOut, code := run(t, "review", "drop", "memory/x")
	if code != 0 {
		t.Fatalf("review drop failed: %s", errOut)
	}
	if out != "dropped memory/x\nnext: run loadout sync\n" {
		t.Fatalf("bad output: %q", out)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "x.md")); !os.IsNotExist(err) {
		t.Fatal("the dropped fact file must be gone")
	}
}

func TestReviewDropMissingAddressExitsOne(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	_, errOut, code := run(t, "review", "drop", "memory/nope")
	if code != 1 {
		t.Fatalf("drop of a missing address must exit 1, got %d", code)
	}
	want := "memory/nope: no such item. Fix: run loadout list.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

// TestReviewDropRejectsKeptItem proves review drop is guarded to
// drafts: a human's kept item must not be destroyed by a drop.
func TestReviewDropRejectsKeptItem(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x") // default human, kept

	_, errOut, code := run(t, "review", "drop", "memory/x")
	if code != 1 {
		t.Fatalf("dropping a kept item must exit 1, got %d", code)
	}
	want := "memory/x: not a draft. Fix: remove the item file directly, or run loadout review to see the drafts.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "x.md")); err != nil {
		t.Fatal("a rejected drop must leave the kept item on disk")
	}
}

func TestReviewUnknownSubcommandIsUsageError(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "review", "bogus"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("an unknown review subcommand must be a usage error, got %d %q", code, errOut)
	}
	if _, errOut, code := run(t, "review", "keep"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("keep without an address must be a usage error, got %d %q", code, errOut)
	}
	if _, errOut, code := run(t, "review", "drop"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("drop without an address must be a usage error, got %d %q", code, errOut)
	}
}

func TestAddByFlagRejectsMalformedValue(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	for _, by := range []string{"pi\nother", "pi\rother", "   ", strings.Repeat("a", 65)} {
		_, errOut, code := run(t, "add", "memory", "x", "--by", by)
		if code != 2 {
			t.Fatalf("a bad --by value %q must exit 2, got %d %q", by, code, errOut)
		}
		if !strings.Contains(errOut, "Fix:") {
			t.Fatalf("a bad --by value %q must use the standard error grammar, got %q", by, errOut)
		}
	}
}

func TestAddByFlagTrimsSurroundingWhitespace(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if _, errOut, code := run(t, "add", "memory", "x", "--by", " pi "); code != 0 {
		t.Fatalf("a --by value with surrounding whitespace must succeed, got %d %q", code, errOut)
	}
	data, err := os.ReadFile(filepath.Join(base, "vault", "memory", "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "by: pi\n") {
		t.Fatalf("the --by value must be trimmed before it is stored, got:\n%s", text)
	}
	if strings.Contains(text, "by:  pi") || strings.Contains(text, "pi \n") {
		t.Fatalf("the stored value must not retain surrounding whitespace, got:\n%s", text)
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

// TestNoArgVerbsRejectLeftoverArguments proves that a verb with no
// positional arguments never silently ignores one: every extra
// argument is a usage error, exit 2.
func TestNoArgVerbsRejectLeftoverArguments(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	for _, args := range [][]string{
		{"init", "extra"},
		{"status", "extra"},
		{"doctor", "extra"},
		{"list", "extra"},
		{"context", "extra"},
		{"log", "extra"},
		{"help", "extra"},
	} {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%v must be a usage error, got %d %q", args, code, errOut)
		}
	}
}

// TestSyncRejectsUnknownFlagAndDoesNotSync proves the critical case:
// a mistyped flag on a mutating verb must never ride along and run
// the command anyway.
func TestSyncRejectsUnknownFlagAndDoesNotSync(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	_, errOut, code := run(t, "sync", "--dry")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("sync --dry must be a usage error, got %d %q", code, errOut)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("sync --dry must not sync anything")
	}
}

// TestUndoRejectsExtraArgsAndDoesNotUndo proves the same for undo:
// undo takes no flags at all, so even a flag sync understands must
// still be a usage error here, and must not touch the vault.
func TestUndoRejectsExtraArgsAndDoesNotUndo(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "fact1")

	_, errOut, code := run(t, "undo", "--dry-run")
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("undo --dry-run must be a usage error, got %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "memory", "fact1.md")); err != nil {
		t.Fatal("undo --dry-run must not undo anything")
	}
}

// TestContextOnMissingHistoryGivesFixedMessage proves that context,
// one of the three history readers, turns a bare git failure on a
// vault with no .git directory into the fixed, friendly message.
func TestContextOnMissingHistoryGivesFixedMessage(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if err := os.RemoveAll(filepath.Join(base, "vault", ".git")); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := run(t, "context")
	if code != 1 {
		t.Fatalf("context on a vault with no history must exit 1, got %d", code)
	}
	if !strings.Contains(errOut, "has no history") || !strings.Contains(errOut, "loadout doctor") {
		t.Fatalf("bad error: got %q", errOut)
	}
}

// TestDoctorReportsDamagedMarksWithRepairFix proves doctor gives the
// right repair for a damaged managed block: the fix names the repair
// itself, not the dead-end "run: loadout sync".
func TestDoctorReportsDamagedMarksWithRepairFix(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "sync")

	home := filepath.Join(base, "home")
	claudeMd := filepath.Join(home, ".claude", "CLAUDE.md")
	corrupted := "<!-- loadout:begin -->\na\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(claudeMd, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := run(t, "doctor")
	if code != 1 {
		t.Fatalf("doctor must report a problem, got %d", code)
	}
	if !strings.Contains(out, claudeMd) || !strings.Contains(out, "damaged") {
		t.Fatalf("doctor must name the damaged file, got %q", out)
	}
	if strings.Contains(out, "run: loadout sync") {
		t.Fatalf("doctor must not offer the dead-end sync fix for a damaged file, got %q", out)
	}
}
