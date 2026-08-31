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

func TestLockNoGitignoreNoOp(t *testing.T) {
	v := newVault(t)
	release, err := vault.Lock(v)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := os.Stat(filepath.Join(v.Root, ".gitignore")); err == nil {
		t.Fatal("Lock must not create a .gitignore file; Task 3 owns creating it")
	}
}
