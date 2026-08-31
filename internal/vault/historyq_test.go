package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestHistoryShowsEveryEntryNewestFirst(t *testing.T) {
	v := newVault(t)
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a fact"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "b.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add b fact"); err != nil {
		t.Fatal(err)
	}

	entries, err := vault.History(v, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %v", entries)
	}
	want := []string{"add b fact", "add a fact", "init the vault"}
	for i, subject := range want {
		if entries[i].Subject != subject {
			t.Fatalf("entry %d: want subject %q, got %q", i, subject, entries[i].Subject)
		}
		if entries[i].At == "" {
			t.Fatalf("entry %d: At must not be empty", i)
		}
	}
}

func TestHistoryCapsAtN(t *testing.T) {
	v := newVault(t)
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(v.MemoryDir(), name), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := vault.Snapshot(v, "add "+name); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := vault.History(v, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Subject != "add b.md" {
		t.Fatalf("bad entries: %v", entries)
	}
}

func TestUndoOnFreshVaultErrors(t *testing.T) {
	v := newVault(t)
	err := vault.Undo(v)
	if err == nil {
		t.Fatal("Undo on a fresh vault must fail")
	}
	if err.Error() != "nothing to undo: the vault has no earlier state." {
		t.Fatalf("bad error: %v", err)
	}
}

// TestUndoRemovesLastFactKeepsEarlierState writes a foreign file into
// the vault root before the first fact, so both land in the same
// history entry. It then adds a second fact as its own entry and
// undoes it. The undone fact must vanish; the first fact and the
// foreign file, both part of the earlier state, must survive exactly
// as plain git semantics dictate (they are unchanged between the two
// commits, so nothing touches them).
func TestUndoRemovesLastFactKeepsEarlierState(t *testing.T) {
	v := newVault(t)
	foreign := filepath.Join(v.Root, "notes.txt")
	if err := os.WriteFile(foreign, []byte("not a loadout item"), 0o644); err != nil {
		t.Fatal(err)
	}
	fact1 := filepath.Join(v.MemoryDir(), "fact1.md")
	if err := os.WriteFile(fact1, []byte("first fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add fact1"); err != nil {
		t.Fatal(err)
	}
	fact2 := filepath.Join(v.MemoryDir(), "fact2.md")
	if err := os.WriteFile(fact2, []byte("second fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add fact2"); err != nil {
		t.Fatal(err)
	}

	if err := vault.Undo(v); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(fact2); !os.IsNotExist(err) {
		t.Fatal("the undone fact must be gone from disk")
	}
	if _, err := os.Stat(fact1); err != nil {
		t.Fatal("the first fact must survive undo")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("the foreign file must survive undo")
	}

	entries, err := vault.History(v, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[0].Subject != "undo" {
		t.Fatalf("history must gain an undo entry on top of the earlier ones, got %v", entries)
	}
}

// TestUndoKeepsUntrackedFileInEmptiedDir checks the case where undo
// must remove a tracked file from a directory that also holds an
// untracked file. Init is state A. Adding a skill is state B, its own
// folder under skills/. An untracked file lands in that same folder
// after state B, so it was never part of any commit. Undo must remove
// the tracked SKILL.md, but it must neither destroy nor strand the
// untracked file: read-tree only ever acts on tracked index entries,
// so a directory git wants gone but that still holds an untracked
// file must survive, with that file still in it.
func TestUndoKeepsUntrackedFileInEmptiedDir(t *testing.T) {
	v := newVault(t) // state A: init the vault
	if _, err := vault.AddSkill(v, "deploy-checks", "human"); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add skill deploy-checks"); err != nil {
		t.Fatal(err)
	} // state B: the skill folder is tracked

	skillDir := filepath.Join(v.SkillsDir(), "deploy-checks")
	skillFile := filepath.Join(skillDir, "SKILL.md")
	untracked := filepath.Join(skillDir, "notes.txt")
	if err := os.WriteFile(untracked, []byte("scratch notes, never committed"), 0o644); err != nil {
		t.Fatal(err)
	} // untracked: written after state B, no further snapshot

	if err := vault.Undo(v); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(skillFile); !os.IsNotExist(err) {
		t.Fatal("the tracked SKILL.md must be gone after undo")
	}
	if _, err := os.Stat(untracked); err != nil {
		t.Fatalf("the untracked file must survive undo in place: %v", err)
	}
	if fi, err := os.Stat(skillDir); err != nil || !fi.IsDir() {
		t.Fatalf("the skill folder must still exist, since the untracked file is still inside: %v", err)
	}
}
