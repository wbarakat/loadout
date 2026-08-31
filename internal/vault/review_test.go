package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestSetReviewKeptRewritesOnlyThatLine(t *testing.T) {
	v := newVault(t)
	path := filepath.Join(v.MemoryDir(), "x.md")
	content := "---\nname: x\ndescription: a fact\nby: pi\nat: 2026-08-31T12:00:00Z\nreview: draft\n---\n\nBody text.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := vault.SetReviewKept(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: x\ndescription: a fact\nby: pi\nat: 2026-08-31T12:00:00Z\nreview: kept\n---\n\nBody text.\n"
	if string(data) != want {
		t.Fatalf("bad rewrite:\ngot  %q\nwant %q", data, want)
	}
}

func TestSetReviewKeptAddsMissingLine(t *testing.T) {
	v := newVault(t)
	path := filepath.Join(v.MemoryDir(), "x.md")
	content := "---\nname: x\ndescription: a fact\n---\n\nBody text.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := vault.SetReviewKept(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "---\nname: x\ndescription: a fact\n") {
		t.Fatalf("bad rewrite prefix: %q", got)
	}
	if !strings.HasSuffix(got, "review: kept\n---\n\nBody text.\n") {
		t.Fatalf("bad rewrite suffix: %q", got)
	}
}

func TestSetReviewKeptPreservesFileMode(t *testing.T) {
	v := newVault(t)
	path := filepath.Join(v.MemoryDir(), "x.md")
	content := "---\nname: x\nreview: draft\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := vault.SetReviewKept(path); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode must survive the rewrite, got %v", fi.Mode().Perm())
	}
}
