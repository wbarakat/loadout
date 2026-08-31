package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestListFacts(t *testing.T) {
	v := newVault(t)
	fact := "---\nname: my-stack\ndescription: the stack I use\ntype: user\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "my-stack.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file without frontmatter must still load.
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "plain.md"), []byte("Just a note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("want 2 facts, got %d", len(facts))
	}
	f := facts[0]
	if f.Name != "my-stack" || f.Type != "user" || !strings.Contains(f.Body, "Go and Postgres") {
		t.Fatalf("bad fact: %+v", f)
	}
	if strings.Contains(f.Body, "---") {
		t.Fatal("body must not contain frontmatter")
	}
	if facts[1].Name != "plain" {
		t.Fatalf("bad fallback name: %q", facts[1].Name)
	}
}

func TestListFactsHandlesBOMAndCRLF(t *testing.T) {
	v := newVault(t)
	content := "\ufeff---\r\nname: my-stack\r\ndescription: the stack I use\r\ntype: user\r\n---\r\n\r\nI use Go and Postgres.\r\n"
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "my-stack.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 fact, got %d", len(facts))
	}
	f := facts[0]
	if f.Name != "my-stack" || f.Description != "the stack I use" || f.Type != "user" {
		t.Fatalf("bad fact: %+v", f)
	}
	if strings.Contains(f.Body, "\r") {
		t.Fatal("the body must not hold a carriage return")
	}
	if strings.Contains(f.Body, "---") {
		t.Fatal("the body must not hold frontmatter")
	}
	if !strings.Contains(f.Body, "I use Go and Postgres.") {
		t.Fatalf("bad body: %q", f.Body)
	}
}

func TestRenderMemory(t *testing.T) {
	out := vault.RenderMemory([]vault.Fact{
		{Name: "a", Body: "Fact A."},
		{Name: "b", Body: "Fact B."},
	})
	if !strings.Contains(out, "## a") || !strings.Contains(out, "Fact B.") {
		t.Fatalf("bad render:\n%s", out)
	}
}
