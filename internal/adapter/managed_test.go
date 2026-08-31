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

func TestWriteManagedBlockRefusesDamagedMarks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	orig := "<!-- loadout:begin -->\nsome text\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v1"); err == nil {
		t.Fatal("an orphan begin mark must be an error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != orig {
		t.Fatal("the file must stay unchanged")
	}
	if _, ok := adapter.ReadManagedBlock(path); ok {
		t.Fatal("read must report no block")
	}
}

func TestWriteManagedBlockRefusesTwoBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	two := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmiddle\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(path, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v1"); err == nil {
		t.Fatal("two blocks must be an error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != two {
		t.Fatal("the file must stay unchanged")
	}
	if _, ok := adapter.ReadManagedBlock(path); ok {
		t.Fatal("read must report no block")
	}
}

func TestWriteManagedBlockRefusesContentWithMark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := adapter.WriteManagedBlock(path, "some text\n<!-- loadout:end -->\nmore text"); err == nil {
		t.Fatal("content holding a loadout mark must be an error")
	}
	if !strings.Contains(path, "CLAUDE.md") {
		t.Fatal("sanity: path must hold the file name")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the file must not be created")
	}
	// The same check applies when the file already exists.
	if err := os.WriteFile(path, []byte("# Keep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "<!-- loadout:begin -->\ntext"); err == nil {
		t.Fatal("content holding a loadout mark must be an error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "# Keep me.\n" {
		t.Fatal("the file must stay unchanged")
	}
}
