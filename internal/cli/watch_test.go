package cli

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/server"
	"loadout.dev/loadout/internal/vault"
)

// TestParseWatchArgsDefaultsToTenSeconds proves no --interval at all
// falls back to the documented default.
func TestParseWatchArgsDefaultsToTenSeconds(t *testing.T) {
	d, ok := parseWatchArgs(nil)
	if !ok || d != defaultWatchInterval {
		t.Fatalf("parseWatchArgs(nil) = %v, %v; want %v, true", d, ok, defaultWatchInterval)
	}
}

// TestParseWatchArgsAcceptsValidDurations proves every Go duration
// spelling at or above the 1s floor is accepted, "10s" and "1m"
// included, per the task brief.
func TestParseWatchArgsAcceptsValidDurations(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1s", time.Second},
		{"10s", 10 * time.Second},
		{"1m", time.Minute},
		{"1m30s", 90 * time.Second},
	}
	for _, c := range cases {
		d, ok := parseWatchArgs([]string{"--interval", c.in})
		if !ok || d != c.want {
			t.Fatalf("parseWatchArgs(--interval %s) = %v, %v; want %v, true", c.in, d, ok, c.want)
		}
	}
}

// TestParseWatchArgsRejectsSubSecondDurations proves the brief's <1s
// floor: any duration under one second, however it is spelled, is a
// usage error rather than a silently accepted hot loop.
func TestParseWatchArgsRejectsSubSecondDurations(t *testing.T) {
	for _, in := range []string{"999ms", "1ms", "0s", "500us"} {
		if _, ok := parseWatchArgs([]string{"--interval", in}); ok {
			t.Fatalf("parseWatchArgs(--interval %s) must be rejected, got ok", in)
		}
	}
}

// TestParseWatchArgsRejectsMalformed proves every other malformed
// shape is a usage error: an unknown flag, a missing or empty value,
// a bad duration string, and stray extra arguments.
func TestParseWatchArgsRejectsMalformed(t *testing.T) {
	cases := [][]string{
		{"--bogus", "10s"},
		{"--interval"},
		{"--interval", ""},
		{"--interval", "not-a-duration"},
		{"10s"},
		{"--interval", "10s", "extra"},
	}
	for _, args := range cases {
		if _, ok := parseWatchArgs(args); ok {
			t.Fatalf("parseWatchArgs(%v) must be rejected, got ok", args)
		}
	}
}

// TestNextBackoffSequence proves the documented sequence: the first
// error backs off to the base interval, each further consecutive
// error doubles it, and the wait never exceeds max however many
// errors pile up.
func TestNextBackoffSequence(t *testing.T) {
	base := time.Second
	max := 5 * time.Minute

	got := nextBackoff(0, base, max)
	if got != base {
		t.Fatalf("first backoff = %v, want base %v", got, base)
	}
	got = nextBackoff(got, base, max)
	if got != 2*base {
		t.Fatalf("second backoff = %v, want %v", got, 2*base)
	}
	got = nextBackoff(got, base, max)
	if got != 4*base {
		t.Fatalf("third backoff = %v, want %v", got, 4*base)
	}

	// Keep doubling until it would exceed max: it must clamp there and
	// stay there, never overshoot.
	for i := 0; i < 20; i++ {
		got = nextBackoff(got, base, max)
		if got > max {
			t.Fatalf("backoff must never exceed max, got %v > %v", got, max)
		}
	}
	if got != max {
		t.Fatalf("backoff must reach max after enough consecutive errors, got %v want %v", got, max)
	}

	// A non-positive current (no active backoff, e.g. after a success)
	// always restarts at base, regardless of any stale prior value.
	if got := nextBackoff(0, base, max); got != base {
		t.Fatalf("nextBackoff(0, ...) = %v, want base %v", got, base)
	}
	if got := nextBackoff(-1, base, max); got != base {
		t.Fatalf("nextBackoff(-1, ...) = %v, want base %v", got, base)
	}
}

// TestRunWatchBeatSkipsQuietlyWhenVaultLocked proves the lock-aware
// requirement directly: while another command holds the vault lock, a
// beat returns beatSkipped (not an error) and prints nothing to
// either stream, so the caller neither blocks nor treats it as a
// failure. This never touches an adapter (TryLock fails before
// syncLocal ever runs), so it needs no HOME redirection.
func TestRunWatchBeatSkipsQuietlyWhenVaultLocked(t *testing.T) {
	v, err := vault.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var out, errOut bytes.Buffer
	outcome, _, _, err := runWatchBeat(v, &out, &errOut, modeText, "", "")
	if err != nil {
		t.Fatalf("a beat that finds the vault locked must not error, got %v", err)
	}
	if outcome != beatSkipped {
		t.Fatalf("a beat that finds the vault locked must report beatSkipped, got %v", outcome)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("a skipped beat must print nothing, got out=%q err=%q", out.String(), errOut.String())
	}
}

// --- beatChangeSince: the pure changed-detection function ---

// TestBeatChangeSinceSilentWhenNothingChanged proves the baseline
// quiet case: the head is unchanged and nothing was linked or pruned.
func TestBeatChangeSinceSilentWhenNothingChanged(t *testing.T) {
	shouldPrint, line := beatChangeSince("h1", "h1", "v1", "v1", 0, 0)
	if shouldPrint {
		t.Fatalf("must stay silent when nothing changed, got print %q", line)
	}
}

// TestBeatChangeSinceIgnoresBareVersionDifference is the deliberate,
// documented deviation from a literal "OR the remote version
// advanced" rule: internal/remote's Sync mints a brand-new version
// string on every successful call even when zero content changed
// (push() republishes whenever this device is already caught up —
// Task 5's own, unmodified behavior). A version difference alone,
// with the head unchanged and nothing linked or pruned, must never
// trigger a print, or every idle beat would look like news.
func TestBeatChangeSinceIgnoresBareVersionDifference(t *testing.T) {
	shouldPrint, line := beatChangeSince("h1", "h1", "v1", "v2", 0, 0)
	if shouldPrint {
		t.Fatalf("a bare version difference must not trigger a print, got %q", line)
	}
}

// TestBeatChangeSincePrintsWhenHeadMoved is the regression case: a
// beat must announce whenever the vault's head moved since the last
// ANNOUNCED beat, which is exactly what lets a change a separate
// command committed between two beats still get reported on the next
// one.
func TestBeatChangeSincePrintsWhenHeadMoved(t *testing.T) {
	shouldPrint, line := beatChangeSince("h1", "h2", "v1", "v2", 0, 0)
	if !shouldPrint {
		t.Fatal("a moved head must trigger a print")
	}
	if !strings.Contains(line, "v2") {
		t.Fatalf("the line must name the version, got %q", line)
	}
}

// TestBeatChangeSincePrintsWhenAdapterTouchedSomething proves a
// projection-only change (nothing new in the vault's own history, but
// a symlink relinked after something outside the vault deleted it)
// still announces, even with the head unchanged.
func TestBeatChangeSincePrintsWhenAdapterTouchedSomething(t *testing.T) {
	shouldPrint, line := beatChangeSince("h1", "h1", "v1", "v1", 2, 1)
	if !shouldPrint {
		t.Fatal("linking or pruning something must trigger a print even with the head unchanged")
	}
	if !strings.Contains(line, "2 linked") || !strings.Contains(line, "1 pruned") {
		t.Fatalf("the line must name what was linked/pruned, got %q", line)
	}
}

// --- the reviewer's live repro, as a committed regression test ---

// newWatchTestServer builds a fresh in-process loadoutd, the same
// shape internal/remote's and internal/cli's own suites use.
func newWatchTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, token
}

// newIsolatedWatchVault builds a fresh vault for a live sync/watch
// test, following the mandatory sandbox rule for any test that lets
// an adapter actually run: it points $HOME at this test's own scratch
// directory (belt) AND rewrites the manifest's two enabled adapters
// (claude-code, pi — the only two DefaultManifest turns on) to
// absolute paths under that same scratch directory (suspenders), so
// neither a "~" expansion nor a stray real-HOME leak can ever land a
// symlink or a memory file outside the sandbox. It asserts the
// rewrite took before returning, standing in for "verify with loadout
// doctor" in a test that talks to the vault package directly rather
// than through the CLI binary.
func newIsolatedWatchVault(t *testing.T, name, url, token string) *vault.Vault {
	t.Helper()
	scratch := t.TempDir()
	t.Setenv("HOME", scratch)

	root := filepath.Join(scratch, "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root, "device.name"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeClaudeSkills := filepath.Join(scratch, "fake-claude", "skills")
	fakeClaudeMemory := filepath.Join(scratch, "fake-claude", "CLAUDE.md")
	fakePiSkills := filepath.Join(scratch, "fake-pi", "skills")
	fakePiMemory := filepath.Join(scratch, "fake-pi", "AGENTS.md")
	v.Manifest.Adapters["claude-code"] = vault.AdapterConfig{Enabled: true, SkillsDir: fakeClaudeSkills, MemoryFile: fakeClaudeMemory}
	v.Manifest.Adapters["pi"] = vault.AdapterConfig{Enabled: true, SkillsDir: fakePiSkills, MemoryFile: fakePiMemory}
	if err := vault.SaveManifest(filepath.Join(v.Root, "loadout.toml"), v.Manifest); err != nil {
		t.Fatal(err)
	}

	// Verify the rewrite actually took, and that it points under the
	// scratch dir, before this vault is ever handed to a live beat.
	for adapterName, cfg := range map[string]vault.AdapterConfig{"claude-code": v.Manifest.Adapters["claude-code"], "pi": v.Manifest.Adapters["pi"]} {
		if !strings.HasPrefix(cfg.SkillsDir, scratch) || !strings.HasPrefix(cfg.MemoryFile, scratch) {
			t.Fatalf("adapter %s targets are not under the scratch dir: %+v", adapterName, cfg)
		}
	}

	if err := remote.Save(v, &remote.Config{URL: url, Token: token}); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRunWatchBeatAnnouncesAChangeCommittedBetweenBeats is the
// committed regression test for the reviewer's live repro: with watch
// running, a separate loadout command (simulated here exactly the way
// "loadout add memory X" leaves the vault — a new fact file, then a
// real vault.Snapshot commit) lands between two beats.
//
// Before the fix, a beat only compared ITS OWN before/after head, so
// beat 2 (whose own execution commits nothing new — the fact was
// already committed before beat 2 even started) saw no change within
// itself and stayed silent, despite syncLocal and remote.Sync having
// just pushed that new content to the remote underneath it. The fix
// compares against the state as of the last ANNOUNCED beat instead,
// so beat 2's current head (which already differs from beat 1's,
// since the external commit happened in between) correctly triggers
// an announcement.
func TestRunWatchBeatAnnouncesAChangeCommittedBetweenBeats(t *testing.T) {
	ts, token := newWatchTestServer(t)
	v := newIsolatedWatchVault(t, "device-a", ts.URL, token)

	cfg, err := remote.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	lastHead, err := vault.HeadHash(v)
	if err != nil {
		t.Fatal(err)
	}
	lastVersion := cfg.LastVersion

	// Beat 1: an idle vault, nothing to do yet. Must stay silent.
	var out1, errOut1 bytes.Buffer
	outcome, head1, version1, err := runWatchBeat(v, &out1, &errOut1, modeText, lastHead, lastVersion)
	if err != nil {
		t.Fatalf("beat 1 failed: %v (stderr: %s)", err, errOut1.String())
	}
	if outcome != beatRan {
		t.Fatalf("beat 1 must run, got outcome %v", outcome)
	}
	if out1.Len() != 0 {
		t.Fatalf("beat 1 on an idle vault must stay silent, got %q", out1.String())
	}

	// Simulate a SEPARATE "loadout add memory watch-test" landing
	// between beats: a new fact file, committed exactly the way any
	// mutating loadout verb commits a real change.
	factPath := filepath.Join(v.MemoryDir(), "watch-test.md")
	factContent := "---\nname: watch-test\ndescription: added between beats\n---\n\nadded between beats.\n"
	if err := os.WriteFile(factPath, []byte(factContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add memory watch-test"); err != nil {
		t.Fatal(err)
	}

	// Beat 2 must both notice and announce the externally-committed
	// change, AND actually have pushed it to the remote.
	var out2, errOut2 bytes.Buffer
	outcome, _, version2, err := runWatchBeat(v, &out2, &errOut2, modeText, head1, version1)
	if err != nil {
		t.Fatalf("beat 2 failed: %v (stderr: %s)", err, errOut2.String())
	}
	if outcome != beatRan {
		t.Fatalf("beat 2 must run, got outcome %v", outcome)
	}
	if out2.Len() == 0 {
		t.Fatal("beat 2 must announce the change a separate command committed between beats, but it printed nothing")
	}
	if !strings.Contains(out2.String(), "watch:") {
		t.Fatalf("beat 2's line must be the standard watch line, got %q", out2.String())
	}
	if version2 == "" || version2 == version1 {
		t.Fatalf("beat 2 must have pushed a new version, got version1=%q version2=%q", version1, version2)
	}
	cfgAfter, err := remote.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if cfgAfter.LastVersion != version2 {
		t.Fatalf("remote.toml's last_version = %q, want %q (the version beat 2 reports it pushed)", cfgAfter.LastVersion, version2)
	}
}
