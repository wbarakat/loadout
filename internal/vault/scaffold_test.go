package vault_test

import (
	"os"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestAddSkill(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddSkill(v, "deploy-checks")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "name: deploy-checks") {
		t.Fatalf("bad template: %v", err)
	}
	if _, err := vault.AddSkill(v, "deploy-checks"); err == nil {
		t.Fatal("duplicate must fail")
	}
	if _, err := vault.AddSkill(v, "Bad Name"); err == nil {
		t.Fatal("bad name must fail")
	}
}

func TestAddFact(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddFact(v, "my-stack")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "name: my-stack") {
		t.Fatal("bad template")
	}
	facts, err := vault.ListFacts(v)
	if err != nil || len(facts) != 1 || facts[0].Name != "my-stack" {
		t.Fatalf("the new fact must list: %v %v", facts, err)
	}
}
