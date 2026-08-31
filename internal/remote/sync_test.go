package remote_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// newSyncTestVault builds a fresh vault named name (its device.name is
// set before anything else touches device identity, so two vaults in
// the same test process never collide on the hostname-derived
// default), pointed at remote's url and token.
func newSyncTestVault(t *testing.T, name, url, token string) *vault.Vault {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root, "device.name"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := remote.Save(v, &remote.Config{URL: url, Token: token}); err != nil {
		t.Fatal(err)
	}
	return v
}

// enrollMutually makes a and b each able to decrypt the other's
// snapshots, by adding both devices' age recipients to both vaults'
// devices.toml roster and snapshotting. Task 6 automates this via an
// approval flow; Task 5's job is the sync protocol and the merge, so
// the tests wire enrollment directly rather than reimplementing
// Task 6 early.
func enrollMutually(t *testing.T, a, b *vault.Vault) {
	t.Helper()
	aName, aRecipient, err := vault.DeviceIdentity(a)
	if err != nil {
		t.Fatal(err)
	}
	bName, bRecipient, err := vault.DeviceIdentity(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []*vault.Vault{a, b} {
		if err := vault.AddToRoster(v, aName, aRecipient); err != nil {
			t.Fatal(err)
		}
		if err := vault.AddToRoster(v, bName, bRecipient); err != nil {
			t.Fatal(err)
		}
		if err := vault.Snapshot(v, "enroll devices"); err != nil {
			t.Fatal(err)
		}
	}
}

// writeFact writes memory/<name>.md with body content, then
// snapshots: the same shape "loadout add memory" plus an edit would
// leave behind, simplified so the tests can control exact bytes for
// equality checks.
func writeFact(t *testing.T, v *vault.Vault, name, body string) {
	t.Helper()
	path := filepath.Join(v.MemoryDir(), name+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "write "+name); err != nil {
		t.Fatal(err)
	}
}

func readFact(t *testing.T, v *vault.Vault, name string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.MemoryDir(), name+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatal(err)
	}
	return string(data), true
}

func deleteFact(t *testing.T, v *vault.Vault, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(v.MemoryDir(), name+".md")); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "delete "+name); err != nil {
		t.Fatal(err)
	}
}

// TestSyncPushThenPullDeliversNewItem covers: create item on A + sync
// A -> server has it; sync B (empty) -> B receives the item.
func TestSyncPushThenPullDeliversNewItem(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "stack", "I use Go and Postgres.\n")

	resA, err := remote.Sync(a)
	if err != nil {
		t.Fatalf("sync A failed: %v", err)
	}
	if !resA.Pushed {
		t.Fatalf("sync A must push, got %+v", resA)
	}

	if _, ok := readFact(t, b, "stack"); ok {
		t.Fatal("B must not have the fact before it syncs")
	}

	resB, err := remote.Sync(b)
	if err != nil {
		t.Fatalf("sync B failed: %v", err)
	}
	if !resB.Merged {
		t.Fatalf("sync B must merge a remote snapshot, got %+v", resB)
	}

	got, ok := readFact(t, b, "stack")
	if !ok {
		t.Fatal("B must receive the fact after it syncs")
	}
	if got != "I use Go and Postgres.\n" {
		t.Fatalf("B's fact content = %q, want the fact A wrote", got)
	}
}

// TestSyncBothChangedSameItemIncomingWinsLoserInHistory covers: edit
// the same item on both, sync both -> the later (both-changed)
// resolves to the newer, and the loser stays reachable in B's git
// history.
func TestSyncBothChangedSameItemIncomingWinsLoserInHistory(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "stack", "original\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatalf("initial sync A failed: %v", err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatalf("initial sync B failed: %v", err)
	}

	// Both devices now agree on "original". Edit it differently on
	// each side.
	writeFact(t, a, "stack", "A's edit\n")
	writeFact(t, b, "stack", "B's edit\n")

	if _, err := remote.Sync(a); err != nil {
		t.Fatalf("sync A after edit failed: %v", err)
	}
	resB, err := remote.Sync(b)
	if err != nil {
		t.Fatalf("sync B after edit failed: %v", err)
	}
	if !resB.Merged {
		t.Fatalf("sync B must merge, got %+v", resB)
	}

	got, ok := readFact(t, b, "stack")
	if !ok {
		t.Fatal("B must still have the fact")
	}
	if got != "A's edit\n" {
		t.Fatalf("both changed: incoming (A's edit) must win on B, got %q", got)
	}

	// B's own losing edit must still be reachable in B's git history.
	out, err := runGitInDir(t, b.Root, "log", "--all", "-p", "--", "memory/stack.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "B's edit") {
		t.Fatalf("B's losing edit must remain reachable in git history, log:\n%s", out)
	}
}

// TestSyncFactEditedOnlyOnALandsOnB covers: a fact edited only on A,
// sync both -> A's edit lands on B.
func TestSyncFactEditedOnlyOnALandsOnB(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "stack", "original\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	writeFact(t, a, "stack", "A's edit only\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	got, ok := readFact(t, b, "stack")
	if !ok || got != "A's edit only\n" {
		t.Fatalf("B must receive A's unopposed edit, got %q ok=%v", got, ok)
	}
}

// TestSyncDeleteOnAPropagatesToB covers: delete on A, sync both ->
// gone on B.
func TestSyncDeleteOnAPropagatesToB(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "stack", "to be deleted\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}
	if _, ok := readFact(t, b, "stack"); !ok {
		t.Fatal("B must have the fact before the deletion propagates")
	}

	deleteFact(t, a, "stack")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	if _, ok := readFact(t, b, "stack"); ok {
		t.Fatal("the deletion must propagate to B")
	}
}

// TestSyncUnchangedLocalKeepsRemoteDeletionEvenWhenBEditsSomethingElse
// proves the deletion rule is per-path: B editing an unrelated fact
// at the same time must not block A's deletion of a different one
// from propagating.
func TestSyncDeletionPropagatesAlongsideAnUnrelatedLocalEdit(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "to-delete", "gone soon\n")
	writeFact(t, a, "stays", "unchanged\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	deleteFact(t, a, "to-delete")
	writeFact(t, b, "stays", "B changed this one\n")

	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	if _, ok := readFact(t, b, "to-delete"); ok {
		t.Fatal("the deletion must still propagate to B despite B's unrelated edit")
	}
	got, ok := readFact(t, b, "stays")
	if !ok || got != "B changed this one\n" {
		t.Fatalf("B's own unopposed edit to a different fact must survive, got %q ok=%v", got, ok)
	}
}

// TestSyncLocalAdditionSurvivesAMergeAndReachesTheOtherDevice proves a
// device that pulls someone else's push does not lose its own
// not-yet-published addition: the merge keeps it, and the follow-up
// republish carries it to the other device on its next sync.
func TestSyncLocalAdditionSurvivesAMergeAndReachesTheOtherDevice(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	writeFact(t, a, "common", "base\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	// A pushes an edit to "common"; B independently adds a brand-new
	// fact the server has never seen.
	writeFact(t, a, "common", "A's edit\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	writeFact(t, b, "b-only", "B's own new fact\n")

	resB, err := remote.Sync(b)
	if err != nil {
		t.Fatalf("sync B failed: %v", err)
	}
	if !resB.Merged || !resB.Pushed {
		t.Fatalf("B must both merge A's edit and republish its own addition, got %+v", resB)
	}
	if got, ok := readFact(t, b, "common"); !ok || got != "A's edit\n" {
		t.Fatalf("B must pick up A's edit, got %q ok=%v", got, ok)
	}
	if got, ok := readFact(t, b, "b-only"); !ok || got != "B's own new fact\n" {
		t.Fatalf("B must keep its own addition through the merge, got %q ok=%v", got, ok)
	}

	// A syncs again and must now receive B's addition, proving it
	// really reached the server.
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if got, ok := readFact(t, a, "b-only"); !ok || got != "B's own new fact\n" {
		t.Fatalf("A must receive B's addition on its next sync, got %q ok=%v", got, ok)
	}
}

// TestSyncConcurrentPushConflictConverges covers: a conflict (both
// push against the same parent) -> one 409, the loser pulls, merges,
// and republishes, and both devices converge once each has synced
// again.
func TestSyncConcurrentPushConflictConverges(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	// Get both devices onto the same starting version.
	writeFact(t, a, "common", "base\n")
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	// Each adds its own distinct new fact, then both sync at once,
	// racing against the same parent.
	writeFact(t, a, "a-only", "A's fact\n")
	writeFact(t, b, "b-only", "B's fact\n")

	var wg sync.WaitGroup
	results := make([]remote.Result, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = remote.Sync(a)
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = remote.Sync(b)
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent sync %d failed: %v", i, err)
		}
	}

	// Exactly one of the two must have taken the direct-push path
	// only (Pushed && !Merged); the other must have hit the conflict
	// and gone through the pull-merge-republish path (Merged).
	pushedOnly, merged := 0, 0
	for _, r := range results {
		switch {
		case r.Merged:
			merged++
		case r.Pushed:
			pushedOnly++
		}
	}
	if merged != 1 || pushedOnly != 1 {
		t.Fatalf("expected exactly one merged and one pushed-only result, got %+v", results)
	}

	// The winner has not yet seen the loser's repush: one more sync
	// round each brings both devices to the same final state.
	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"a-only", "b-only", "common"} {
		aContent, aOK := readFact(t, a, name)
		bContent, bOK := readFact(t, b, name)
		if !aOK || !bOK {
			t.Fatalf("both devices must converge on holding %q, a=%v b=%v", name, aOK, bOK)
		}
		if aContent != bContent {
			t.Fatalf("both devices must converge on the same content for %q, a=%q b=%q", name, aContent, bContent)
		}
	}
}

// TestSyncSkillLinksLocallyAndSyncsRemotely proves a skill added
// locally both projects into an adapter (the local sync half) and
// syncs to the remote (this package's half), through the CLI's
// composition in internal/cli. This lives here (rather than only in
// internal/cli) because it is the clearest place to prove
// remote.Sync itself receives and correctly packs a skill folder, not
// just memory facts.
func TestSyncSkillContentRoundTripsThroughRemoteSync(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollMutually(t, a, b)

	skillDir := filepath.Join(a.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: deploy-checks\ndescription: run checks before a deploy\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(a, "add skill deploy-checks"); err != nil {
		t.Fatal(err)
	}

	if _, err := remote.Sync(a); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Sync(b); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(b.SkillsDir(), "deploy-checks", "SKILL.md"))
	if err != nil {
		t.Fatalf("B must receive the skill folder: %v", err)
	}
	if string(got) != content {
		t.Fatalf("B's skill content = %q, want %q", got, content)
	}
}

// TestSyncRecordsLastVersionAndDeviceRegistration proves Sync updates
// remote.toml's last_version and registers this device with the
// remote's bootstrap roster.
func TestSyncRecordsLastVersionAndDeviceRegistration(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)

	writeFact(t, a, "x", "hello\n")
	res, err := remote.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := remote.Load(a)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LastVersion != res.Version {
		t.Fatalf("remote.toml last_version = %q, want %q", cfg.LastVersion, res.Version)
	}

	c := remote.NewClient(ts.URL, token)
	devices, err := c.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range devices {
		if d.Name == "device-a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Sync must register this device with the remote, got %+v", devices)
	}
}

// runGitInDir shells out to git for a test assertion only (reading
// history), never to mutate anything remote.Sync itself did not
// already commit.
func runGitInDir(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}
