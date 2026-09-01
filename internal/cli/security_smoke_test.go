package cli_test

// Phase 5 Task 5's mandated security smoke: an end-to-end proof of
// INVARIANT 10 across the whole secrets feature (Tasks 1-5 together).
// It runs entirely inside temp, per-test HOME and LOADOUT_HOME
// sandboxes (setupEnv, the same helper every other cli_test uses),
// against a real loadoutd (an httptest.Server backed by a real
// internal/server.Store on a temp data directory) — never the real
// user's home, never a real credential.
//
// It proves a dummy secret value appears NOWHERE on disk, in git
// history, or in the server's stored data, in plaintext — and that
// among every command loadout offers, the value surfaces in exactly
// two places: the child process of an explicit "loadout run", and the
// explicit "--reveal" flag of "loadout secret show". This test must
// never be deleted or weakened: it is the proof the invariant holds
// across the whole feature, not just one function at a time.

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/server"
)

// walkTreeForDummy walks root and returns the relative path of every
// regular file whose raw bytes contain needle — a "find + grep -r"
// pass over the whole tree, dotfiles and the .git plumbing included,
// so nothing gets a free pass. An empty result means needle appears
// nowhere under root at all.
func walkTreeForDummy(t *testing.T, root, needle string) []string {
	t.Helper()
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("cannot read %s while scanning for the dummy: %v", path, readErr)
		}
		if bytes.Contains(data, []byte(needle)) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s failed: %v", root, err)
	}
	return hits
}

// newSmokeServer starts a real loadoutd (server.Store + server.New,
// the exact production wiring cmd/loadoutd/main.go uses) on an
// httptest.Server backed by a temp data directory, returning the
// server, its bearer token, and the data directory path so the test
// can grep the store's own on-disk blobs directly.
func newSmokeServer(t *testing.T) (ts *httptest.Server, token, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	store, err := server.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err = store.Token()
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(store, token, log.New(io.Discard, "", 0))
	ts = httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, token, dataDir
}

// TestSecuritySmokeSecretNeverLeaksPlaintext is the Task 5 security
// smoke: it exercises the whole secrets surface — add, list, context,
// recall, doctor, sync --remote to a real loadoutd, run, show
// --reveal, and rotate — and proves a dummy value never appears in
// plaintext anywhere except the two places INVARIANT 10 allows: the
// child process loadout run injects it into, and the explicit
// --reveal output.
func TestSecuritySmokeSecretNeverLeaksPlaintext(t *testing.T) {
	const dummy = "SMOKE-DUMMY-do-not-leak-4f9c21a8b3"
	const rotatedDummy = "SMOKE-ROTATED-do-not-leak-7b3e0d5c91"

	ts, token, dataDir := newSmokeServer(t)
	t.Logf("[setup] real loadoutd started at %s, temp data dir %s", ts.URL, dataDir)

	base := setupEnv(t) // t.Setenv-scoped HOME/LOADOUT_HOME under t.TempDir(): the real user home is never touched, and both env vars revert automatically when this test ends.
	vaultRoot := filepath.Join(base, "vault")
	t.Logf("[setup] temp HOME=%s LOADOUT_HOME=%s (real home never referenced)", filepath.Join(base, "home"), vaultRoot)
	run(t, "init")
	t.Logf("[1] loadout init: vault created at %s", vaultRoot)

	// --- Step 1: add a dummy secret. ------------------------------
	if _, errOut, code := runWithStdin(t, dummy, "secret", "add", "smoke-key", "--service", "smoke-svc", "--rotate-after", "720h"); code != 0 {
		t.Fatalf("secret add failed: %s", errOut)
	}
	t.Logf("[1] secret add smoke-key --service smoke-svc --rotate-after 720h (value piped on stdin): ok")

	// --- Step 2: grep the ENTIRE vault tree. The dummy must be
	// absent from EVERY file, including value.age: value.age holds
	// ciphertext, so a literal plaintext match there — or anywhere
	// else — is a failure, not an expected hit.
	if hits := walkTreeForDummy(t, vaultRoot, dummy); len(hits) != 0 {
		t.Fatalf("the dummy value must appear nowhere on disk under the vault, found in: %v", hits)
	}
	t.Logf("[2] find+grep -r over the whole vault tree (%s) for the dummy: 0 hits (value.age included)", vaultRoot)

	// --- Step 3: grep the vault's git history (every ref, full
	// patches). The value must never have been committed in
	// plaintext.
	gitLog, err := exec.Command("git", "-C", vaultRoot, "log", "-p", "--all").CombinedOutput()
	if err != nil {
		t.Fatalf("git log -p --all failed: %v: %s", err, gitLog)
	}
	if bytes.Contains(gitLog, []byte(dummy)) {
		t.Fatalf("the dummy value must never appear in git history:\n%s", gitLog)
	}
	t.Logf("[3] git -C %s log -p --all, grepped for the dummy: 0 hits (%d bytes of history scanned)", vaultRoot, len(gitLog))

	// --- Step 4: sync to a REAL loadoutd, then grep every byte the
	// server stored (index, roster, and every blob version) for the
	// dummy. PackSnapshot double-wraps the secret's own ciphertext in
	// an outer age layer, so this must never match either.
	if _, errOut, code := run(t, "remote", "add", ts.URL, token); code != 0 {
		t.Fatalf("remote add failed: %s", errOut)
	}
	if _, errOut, code := run(t, "sync", "--remote"); code != 0 {
		t.Fatalf("sync --remote failed: %s", errOut)
	}
	t.Logf("[4] loadout remote add %s <token>; loadout sync --remote: ok (pushed to the real loadoutd)", ts.URL)
	if hits := walkTreeForDummy(t, dataDir, dummy); len(hits) != 0 {
		t.Fatalf("the dummy value must never appear in the server's stored data, found in: %v", hits)
	}
	t.Logf("[4] find+grep -r over the loadoutd data dir (%s) for the dummy: 0 hits (index.json, roster.json, blobs/* included)", dataDir)

	// --- Step 5: every read-only verb that touches the vault must
	// never leak the dummy, in stdout or stderr, text or JSON.
	for _, args := range [][]string{
		{"secret", "list"},
		{"secret", "list", "--json"},
		{"context"},
		{"recall", dummy[:12]},
		{"doctor"},
	} {
		out, errOut, _ := run(t, args...)
		if strings.Contains(out, dummy) || strings.Contains(errOut, dummy) {
			t.Fatalf("%v must never leak the dummy value, got out=%q err=%q", args, out, errOut)
		}
		t.Logf("[5] loadout %s: stdout/stderr checked, 0 occurrences of the dummy", strings.Join(args, " "))
	}

	// --- Step 6: "secret show --reveal" is the first of the two
	// places the value may legitimately surface, and only there,
	// only under this explicit flag.
	revealOut, revealErr, code := run(t, "secret", "show", "smoke-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal failed: %s", revealErr)
	}
	if revealOut != dummy {
		t.Fatalf("--reveal must print exactly the value, got %q", revealOut)
	}
	t.Logf("[6] loadout secret show smoke-key --reveal: stdout == the dummy (expected: the explicit reveal path)")

	// --- Step 7: "loadout run" is the second, and the primary agent
	// path: the CHILD process sees the value; loadout's own stdout
	// and stderr carry no trace of it at all.
	childOut, loadoutOut, loadoutErr, code := runCapturingChildStdout(t,
		"run", "--secret", "smoke-key=X", "--", "sh", "-c", `printf %s "$X"`)
	if code != 0 {
		t.Fatalf("run failed: %s", loadoutErr)
	}
	if childOut != dummy {
		t.Fatalf("the child process must echo the dummy, got %q", childOut)
	}
	if strings.Contains(loadoutOut, dummy) || strings.Contains(loadoutErr, dummy) {
		t.Fatalf("loadout's own output must never carry the dummy, got out=%q err=%q", loadoutOut, loadoutErr)
	}
	t.Logf(`[7] loadout run --secret smoke-key=X -- sh -c 'printf %%s "$X"': child stdout == the dummy (the ONLY other surfacing point); loadout's own stdout/stderr: 0 occurrences`)

	// --- Step 8: rotate replaces the value. Both the OLD and the NEW
	// value must still be absent from every file on disk except as
	// value.age ciphertext — which, again, must never hold either in
	// the clear.
	if _, errOut, code := runWithStdin(t, rotatedDummy, "secret", "rotate", "smoke-key"); code != 0 {
		t.Fatalf("secret rotate failed: %s", errOut)
	}
	t.Logf("[8] secret rotate smoke-key (new value piped on stdin): ok")
	for _, needle := range []string{dummy, rotatedDummy} {
		if hits := walkTreeForDummy(t, vaultRoot, needle); len(hits) != 0 {
			t.Fatalf("after rotation, %q must appear nowhere on disk, found in: %v", needle, hits)
		}
	}
	t.Logf("[8] post-rotate find+grep -r over the vault tree for BOTH the old and the new dummy: 0 hits")
	rotatedOut, rotatedErr, code := run(t, "secret", "show", "smoke-key", "--reveal")
	if code != 0 {
		t.Fatalf("secret show --reveal after rotate failed: %s", rotatedErr)
	}
	if rotatedOut != rotatedDummy {
		t.Fatalf("after rotation, --reveal must show the NEW value, got %q", rotatedOut)
	}
	t.Logf("[8] loadout secret show smoke-key --reveal: stdout == the ROTATED dummy (the old value is gone)")

	// --- Step 9: the access log holds entries for show, run, and
	// rotate — every use above — and NEVER a value.
	logData, err := os.ReadFile(filepath.Join(vaultRoot, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if strings.Contains(logText, dummy) || strings.Contains(logText, rotatedDummy) {
		t.Fatalf("the access log must never hold a value, got:\n%s", logText)
	}
	lines := strings.Split(strings.TrimRight(logText, "\n"), "\n")
	wantVerbs := map[string]int{"show": 2, "run": 1, "rotate": 1}
	gotVerbs := map[string]int{}
	for _, line := range lines {
		var entry struct {
			Verb   string `json:"verb"`
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("bad access-log line %q: %v", line, err)
		}
		if entry.Secret != "smoke-key" {
			t.Fatalf("bad access-log entry secret: %+v", entry)
		}
		gotVerbs[entry.Verb]++
	}
	for verb, want := range wantVerbs {
		if gotVerbs[verb] != want {
			t.Fatalf("access log verb %q count = %d, want %d; lines=%v", verb, gotVerbs[verb], want, lines)
		}
	}
	t.Logf("[9] access.log: %d entries (show=%d run=%d rotate=%d), 0 occurrences of either dummy value:\n%s",
		len(lines), gotVerbs["show"], gotVerbs["run"], gotVerbs["rotate"], logText)
	t.Logf("[done] real user HOME was never referenced by any step above; every path used was under t.TempDir().")
}
