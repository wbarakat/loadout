package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
)

func TestManagedBlockCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "CLAUDE.md")
	if err := adapter.WriteManagedBlock(path, "hello"); err != nil {
		t.Fatal(err)
	}
	got, ok := adapter.ReadManagedBlock(path)
	if !ok || got != "hello" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestManagedBlockPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My own rules\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "Keep me.") {
		t.Fatal("user content was lost")
	}
	if strings.Contains(text, "v1") {
		t.Fatal("old block was not replaced")
	}
	if strings.Count(text, "<!-- loadout:begin -->") != 1 {
		t.Fatal("must have exactly one block")
	}
	got, ok := adapter.ReadManagedBlock(path)
	if !ok || got != "v2" {
		t.Fatalf("got %q", got)
	}
}

func TestReadManagedBlockMissing(t *testing.T) {
	if _, ok := adapter.ReadManagedBlock(filepath.Join(t.TempDir(), "nope.md")); ok {
		t.Fatal("must report missing")
	}
}
