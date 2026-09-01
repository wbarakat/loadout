package remote_test

// Permanent regression suite from the 2026-08-31 adversarial review of
// the Task 5 merge. Two bugs were PROVEN here (deterministically, 3/3
// runs) and then fixed in sync.go:
//
//   - CRITICAL 1 (TestReviewS4bRetryAfterSecondConflictKeepsLocalFact):
//     a conflict-retry recorded {version, baseCommit} using the
//     post-merge HEAD as baseCommit, even though that tree held local
//     content the paired version did not. A second retry then read
//     that poisoned pair back as "the base," saw its own kept
//     addition as unchanged since base, and deleted it to match the
//     next incoming snapshot: a real, silent, permanent loss. Fixed
//     by threading one fixed baseCommit (the last CONFIRMED
//     remote-agreed tree) through an entire retry chain, and only
//     ever checkpointing .sync-state.json on a confirmed outcome
//     (either "identical to incoming" or "the push actually
//     succeeded") — never mid-chain, never speculatively.
//   - CRITICAL 2 (TestReviewS3bDeleteVersusEdit): the merge's default
//     branch let an incoming deletion win over a local edit. The
//     binding rule requires the opposite: local wins (re-adding the
//     path upstream on the next push). Fixed by adding an explicit
//     "incoming does not exist" case ahead of the generic
//     both-changed branch in mergeInto.
//
// These tests must never be deleted or weakened: they are the proof
// the fix holds, and the two clearest opportunities for a future edit
// to reintroduce data loss.

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/server"
	"loadout.dev/loadout/internal/vault"
)

// enrollAll adds every vault's identity to every vault's roster: the
// variadic sibling of sync_test.go's enrollMutually, used here where
// three devices (A, B, C) participate.
func enrollAll(t *testing.T, vaults ...*vault.Vault) {
	t.Helper()
	type id struct{ name, recipient string }
	var ids []id
	for _, v := range vaults {
		n, r, err := vault.DeviceIdentity(v)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id{n, r})
	}
	for _, v := range vaults {
		for _, i := range ids {
			if err := vault.AddToRoster(v, i.name, i.recipient, vault.RoleFull); err != nil {
				t.Fatal(err)
			}
		}
		if err := vault.Snapshot(v, "enroll devices"); err != nil {
			t.Fatal(err)
		}
	}
}

// mustSync calls remote.Sync and fails the test immediately on error:
// most of this suite's steps care only that a sync succeeds, not
// about the intermediate errors along the way.
func mustSync(t *testing.T, v *vault.Vault) remote.Result {
	t.Helper()
	res, err := remote.Sync(v)
	if err != nil {
		t.Fatalf("sync of %s failed: %v", v.Root, err)
	}
	return res
}

// TestReviewS1BothEditSameFact: both edit the SAME fact to DIFFERENT
// content, both sync. The winner must be deterministic (incoming
// wins on the later syncer); the loser must stay reachable via git in
// the losing vault.
func TestReviewS1BothEditSameFact(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	writeFact(t, a, "stack", "original\n")
	mustSync(t, a)
	mustSync(t, b)

	writeFact(t, a, "stack", "A's edit\n")
	writeFact(t, b, "stack", "B's edit\n")
	mustSync(t, a) // A publishes first
	mustSync(t, b) // B pulls: incoming (A's) must win
	mustSync(t, a)

	gotB, okB := readFact(t, b, "stack")
	gotA, okA := readFact(t, a, "stack")
	if !okA || !okB {
		t.Fatalf("fact missing entirely: a=%v b=%v — TOTAL LOSS", okA, okB)
	}
	if gotB != "A's edit\n" || gotA != "A's edit\n" {
		t.Fatalf("winner not deterministic: a=%q b=%q", gotA, gotB)
	}
	out, err := runGitInDir(t, b.Root, "log", "--all", "-p", "--", "memory/stack.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "B's edit") {
		t.Fatalf("loser's edit not in B's git history:\n%s", out)
	}
}

// TestReviewS2DisjointAdds: a brand-new fact on A only, plus a
// different brand-new fact on B only. Both must end on both vaults.
func TestReviewS2DisjointAdds(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	writeFact(t, a, "seed", "seed\n")
	mustSync(t, a)
	mustSync(t, b)

	writeFact(t, a, "from-a", "A's new fact\n")
	writeFact(t, b, "from-b", "B's new fact\n")
	mustSync(t, a)
	mustSync(t, b)
	mustSync(t, a)

	for _, v := range []*vault.Vault{a, b} {
		if got, ok := readFact(t, v, "from-a"); !ok || got != "A's new fact\n" {
			t.Fatalf("%s lost from-a: %q ok=%v", v.Root, got, ok)
		}
		if got, ok := readFact(t, v, "from-b"); !ok || got != "B's new fact\n" {
			t.Fatalf("%s lost from-b: %q ok=%v", v.Root, got, ok)
		}
	}
}

// TestReviewS3aPlainDeletePropagates: delete on A, unchanged on B:
// gone on B.
func TestReviewS3aPlainDeletePropagates(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	writeFact(t, a, "doomed", "bye\n")
	mustSync(t, a)
	mustSync(t, b)

	deleteFact(t, a, "doomed")
	mustSync(t, a)
	mustSync(t, b)

	if _, ok := readFact(t, b, "doomed"); ok {
		t.Fatal("plain deletion did not propagate to B")
	}
}

// TestReviewS3bDeleteVersusEdit is CRITICAL 2's regression test:
// delete on A while B edits the SAME fact. The binding rule is
// "deletion in incoming with local changed -> keep (re-add,
// propagates)". The edited fact must survive on B's disk and reach A.
func TestReviewS3bDeleteVersusEdit(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	writeFact(t, a, "contested", "original\n")
	mustSync(t, a)
	mustSync(t, b)

	deleteFact(t, a, "contested")
	writeFact(t, b, "contested", "B's edit while A deleted\n")
	mustSync(t, a) // A publishes the deletion
	mustSync(t, b) // B pulls it; rule says B's edit must survive
	mustSync(t, a)

	got, ok := readFact(t, b, "contested")
	if !ok {
		out, _ := runGitInDir(t, b.Root, "log", "--all", "-p", "--", "memory/contested.md")
		inHist := strings.Contains(out, "B's edit while A deleted")
		t.Fatalf("RULE VIOLATION: B's edit was deleted from B's disk (in git history: %v)", inHist)
	}
	if got != "B's edit while A deleted\n" {
		t.Fatalf("B's fact content = %q", got)
	}
	if gotA, okA := readFact(t, a, "contested"); !okA || gotA != "B's edit while A deleted\n" {
		t.Fatalf("the kept edit must propagate back to A, got %q ok=%v", gotA, okA)
	}
}

// interceptServer wraps a real server so a test can hold the FIRST
// POST /v1/snapshots matching a given parent until released,
// deterministically forcing a 409 rather than racing goroutines and
// hoping.
type interceptServer struct {
	ts       *httptest.Server
	token    string
	arrived  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	armed    bool
	parent   string
	consumed bool
}

func newInterceptServer(t *testing.T) *interceptServer {
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
	inner := srv.Handler()
	is := &interceptServer{
		token:   token,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
	is.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/snapshots" {
			is.mu.Lock()
			hold := is.armed && !is.consumed && r.Header.Get("X-Loadout-Parent") == is.parent
			if hold {
				is.consumed = true
			}
			is.mu.Unlock()
			if hold {
				close(is.arrived)
				<-is.release
			}
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(is.ts.Close)
	return is
}

func (is *interceptServer) arm(parent string) {
	is.mu.Lock()
	is.armed = true
	is.parent = parent
	is.consumed = false
	is.mu.Unlock()
}

// TestReviewS4aDeterministic409Converges forces a genuine 409
// deterministically: both devices sit at v1 with disjoint new facts.
// B's push is held at the server until A's full sync lands v2; B's
// held push is then released, gets the 409, and must pull, merge, and
// repush with no lost item.
func TestReviewS4aDeterministic409Converges(t *testing.T) {
	is := newInterceptServer(t)
	a := newSyncTestVault(t, "device-a", is.ts.URL, is.token)
	b := newSyncTestVault(t, "device-b", is.ts.URL, is.token)
	enrollAll(t, a, b)

	writeFact(t, a, "seed", "seed\n")
	mustSync(t, a)
	mustSync(t, b)
	v1, err := remote.Load(b)
	if err != nil {
		t.Fatal(err)
	}

	writeFact(t, a, "from-a", "A's fact\n")
	writeFact(t, b, "from-b", "B's fact\n")

	is.arm(v1.LastVersion) // hold the first push built on v1: B's

	done := make(chan remote.Result, 1)
	go func() {
		res, err := remote.Sync(b)
		if err != nil {
			t.Errorf("sync B failed: %v", err)
		}
		done <- res
	}()
	<-is.arrived // B's push is now held at parent v1
	mustSync(t, a)
	close(is.release)
	resB := <-done
	if !resB.Merged {
		t.Fatalf("B must have gone through the 409 pull-merge-repush path, got %+v", resB)
	}
	mustSync(t, a) // A picks up B's repush

	for _, v := range []*vault.Vault{a, b} {
		for name, want := range map[string]string{"from-a": "A's fact\n", "from-b": "B's fact\n", "seed": "seed\n"} {
			if got, ok := readFact(t, v, name); !ok || got != want {
				t.Fatalf("%s lost %q after the 409 cycle: %q ok=%v", v.Root, name, got, ok)
			}
		}
	}
}

// TestReviewS4bRetryAfterSecondConflictKeepsLocalFact is CRITICAL 1's
// regression test: the retry path. B merges v2 while holding its own
// unpushed fact; before B's repush lands, a third device's push lands
// v3. B's repush gets the 409 and retries. B's own fact must survive
// the retry, live, on every device.
func TestReviewS4bRetryAfterSecondConflictKeepsLocalFact(t *testing.T) {
	is := newInterceptServer(t)
	a := newSyncTestVault(t, "device-a", is.ts.URL, is.token)
	b := newSyncTestVault(t, "device-b", is.ts.URL, is.token)
	c := newSyncTestVault(t, "device-c", is.ts.URL, is.token)
	enrollAll(t, a, b, c)

	writeFact(t, a, "common", "base\n")
	mustSync(t, a)
	mustSync(t, b)
	mustSync(t, c)

	writeFact(t, a, "a-only", "A's fact\n")
	mustSync(t, a) // server now at v2
	v2, err := remote.Load(a)
	if err != nil {
		t.Fatal(err)
	}

	writeFact(t, c, "c-only", "C's fact\n")
	writeFact(t, b, "b-only", "B's fact\n")

	// B: Latest=v2 != its v1 -> pull v2, merge (keeps b-only),
	// repush parent=v2. Hold that repush; land C's push first.
	is.arm(v2.LastVersion)

	done := make(chan remote.Result, 1)
	go func() {
		res, err := remote.Sync(b)
		if err != nil {
			t.Errorf("sync B failed: %v", err)
		}
		done <- res
	}()
	<-is.arrived   // B's repush (parent v2) held
	mustSync(t, c) // C pulls v2 (c-only differs) and lands v3
	close(is.release)
	resB := <-done
	t.Logf("B's result: %+v", resB)

	// Everyone syncs once more; b-only must exist SOMEWHERE live.
	mustSync(t, a)
	mustSync(t, b)
	mustSync(t, c)

	if got, ok := readFact(t, b, "b-only"); !ok {
		out, _ := runGitInDir(t, b.Root, "log", "--all", "-p", "--", "memory/b-only.md")
		inHist := strings.Contains(out, "B's fact")
		t.Fatalf("SILENT LOSS: b-only vanished from B's live tree after the retry (in git history: %v)", inHist)
	} else if got != "B's fact\n" {
		t.Fatalf("b-only content = %q", got)
	}
	for _, v := range []*vault.Vault{a, c} {
		if _, ok := readFact(t, v, "b-only"); !ok {
			t.Fatalf("b-only never propagated to %s", v.Root)
		}
	}
}

// TestReviewS5WholeSkillDirArrives: a skill directory (SKILL.md plus a
// nested resource file) added on A must arrive whole on B.
func TestReviewS5WholeSkillDirArrives(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	dir := filepath.Join(a.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: deploy-checks\n---\n\nRun the checks.\n"
	resource := "#!/bin/sh\necho check\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "check.sh"), []byte(resource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(a, "add skill"); err != nil {
		t.Fatal(err)
	}

	mustSync(t, a)
	mustSync(t, b)

	got1, err := os.ReadFile(filepath.Join(b.SkillsDir(), "deploy-checks", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md missing on B: %v", err)
	}
	got2, err := os.ReadFile(filepath.Join(b.SkillsDir(), "deploy-checks", "resources", "check.sh"))
	if err != nil {
		t.Fatalf("resource file missing on B — partial skill: %v", err)
	}
	if string(got1) != skill || string(got2) != resource {
		t.Fatalf("content mismatch: %q %q", got1, got2)
	}
}

// TestReviewS6SkillDeleteLeavesNoEmptyDir is IMPORTANT 3's regression
// test: deleting a whole skill on A must not leave an empty skill
// directory behind on B (doctor treats one as a problem: "no SKILL.md
// file").
func TestReviewS6SkillDeleteLeavesNoEmptyDir(t *testing.T) {
	ts, token := newTestServer(t)
	a := newSyncTestVault(t, "device-a", ts.URL, token)
	b := newSyncTestVault(t, "device-b", ts.URL, token)
	enrollAll(t, a, b)

	dir := filepath.Join(a.SkillsDir(), "doomed-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: doomed-skill\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(a, "add skill"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(a, "remove skill"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, a)
	mustSync(t, b)

	if _, err := os.Stat(filepath.Join(b.SkillsDir(), "doomed-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("SKILL.md must be gone on B, stat err=%v", err)
	}
	if fi, err := os.Stat(filepath.Join(b.SkillsDir(), "doomed-skill")); err == nil && fi.IsDir() {
		t.Fatal("empty skill directory left behind on B after the deletion propagated")
	}
}
