package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loadout.dev/loadout/internal/vault"
)

func TestLockBlocksSecondHolder(t *testing.T) {
	v := newVault(t)
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		r2, err2 := vault.Lock(v)
		if err2 == nil {
			r2()
		}
		done <- err2
	}()
	select {
	case err2 := <-done:
		t.Fatalf("second lock must wait, got %v", err2)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err2 := <-done; err2 != nil {
		t.Fatalf("second lock must win after release: %v", err2)
	}
}

func TestLockTimesOut(t *testing.T) {
	v := newVault(t)
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	start := time.Now()
	_, err = vault.Lock(v)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("second lock must time out while the first is held")
	}
	want := "the vault at " + v.Root + " is locked by another loadout command. Fix: wait for it to finish, or remove loadout.lock if no loadout process runs."
	if err.Error() != want {
		t.Fatalf("error text = %q, want %q", err.Error(), want)
	}
	if elapsed < 9*time.Second {
		t.Fatalf("timed out too early: %v", elapsed)
	}
}

func TestLockAddsGitignoreEntry(t *testing.T) {
	v := newVault(t)
	gitignorePath := filepath.Join(v.Root, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(".DS_Store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "loadout.lock") {
		t.Fatalf(".gitignore does not name loadout.lock: %q", string(data))
	}
}

// TestTryLockSucceedsWhenFree proves TryLock takes the lock at once
// when nothing else holds it, and that release actually frees it
// again for a later Lock call.
func TestTryLockSucceedsWhenFree(t *testing.T) {
	v := newVault(t)
	release, ok, err := vault.TryLock(v)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("TryLock must succeed on a free vault")
	}
	release()

	// The lock must really be free again: a normal Lock call must not
	// block or time out.
	release2, err := vault.Lock(v)
	if err != nil {
		t.Fatalf("Lock after TryLock's release must succeed, got %v", err)
	}
	release2()
}

// TestTryLockFailsQuietlyWhenHeld proves TryLock returns immediately
// with ok=false (never an error, never a block) while Lock already
// holds the vault.
func TestTryLockFailsQuietlyWhenHeld(t *testing.T) {
	v := newVault(t)
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	start := time.Now()
	release2, ok, err := vault.TryLock(v)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("TryLock on a held vault must not error, got %v", err)
	}
	if ok {
		release2()
		t.Fatal("TryLock must report false while another holder has the lock")
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("TryLock must return at once, not poll or wait; took %v", elapsed)
	}
}

func TestLockNoGitignoreNoOp(t *testing.T) {
	// A bare vault, not run through Init, so no .gitignore exists yet.
	// This isolates Lock's own behavior from Task 3's Init/Open
	// healing, which is covered in vault_test.go.
	v := &vault.Vault{Root: t.TempDir()}
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := os.Stat(filepath.Join(v.Root, ".gitignore")); err == nil {
		t.Fatal("Lock must not create a .gitignore file; Task 3 owns creating it")
	}
}
