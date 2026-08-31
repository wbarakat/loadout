package vault_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"loadout.dev/loadout/internal/vault"
)

func TestAddSkill(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddSkill(v, "deploy-checks", "human")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "name: deploy-checks") {
		t.Fatalf("bad template: %v", err)
	}
	if _, err := vault.AddSkill(v, "deploy-checks", "human"); err == nil {
		t.Fatal("duplicate must fail")
	}
	if _, err := vault.AddSkill(v, "Bad Name", "human"); err == nil {
		t.Fatal("bad name must fail")
	}
	skills, err := vault.ListSkills(v)
	if err != nil || len(skills) != 1 || skills[0].Name != "deploy-checks" {
		t.Fatalf("the new skill must list: %v %v", skills, err)
	}
}

func TestAddSkillRecordsProvenance(t *testing.T) {
	v := newVault(t)
	before := time.Now().UTC()
	path, err := vault.AddSkill(v, "deploy-checks", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "by: claude-code") {
		t.Fatalf("template must hold by: claude-code, got:\n%s", text)
	}
	if !strings.Contains(text, "review: draft") {
		t.Fatalf("a non-human write must start as draft, got:\n%s", text)
	}
	skills, err := vault.ListSkills(v)
	if err != nil || len(skills) != 1 {
		t.Fatalf("the new skill must list: %v %v", skills, err)
	}
	s := skills[0]
	if s.By != "claude-code" || s.Review != "draft" {
		t.Fatalf("bad provenance: %+v", s)
	}
	at, err := time.Parse(time.RFC3339, s.At)
	if err != nil {
		t.Fatalf("at must be a valid RFC3339 timestamp: %v", err)
	}
	if at.Before(before.Add(-time.Minute)) || at.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("at must be close to now, got %v", at)
	}
}

func TestAddSkillDefaultByIsKept(t *testing.T) {
	v := newVault(t)
	if _, err := vault.AddSkill(v, "deploy-checks", "human"); err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil || len(skills) != 1 {
		t.Fatalf("the new skill must list: %v %v", skills, err)
	}
	if skills[0].By != "human" || skills[0].Review != "kept" {
		t.Fatalf("a human write must be kept, got: %+v", skills[0])
	}
}

func TestAddFact(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddFact(v, "my-stack", "human")
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

func TestAddFactRecordsProvenance(t *testing.T) {
	v := newVault(t)
	before := time.Now().UTC()
	path, err := vault.AddFact(v, "my-stack", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "by: claude-code") {
		t.Fatalf("template must hold by: claude-code, got:\n%s", text)
	}
	if !strings.Contains(text, "review: draft") {
		t.Fatalf("a non-human write must start as draft, got:\n%s", text)
	}
	facts, err := vault.ListFacts(v)
	if err != nil || len(facts) != 1 {
		t.Fatalf("the new fact must list: %v %v", facts, err)
	}
	f := facts[0]
	if f.By != "claude-code" || f.Review != "draft" {
		t.Fatalf("bad provenance: %+v", f)
	}
	at, err := time.Parse(time.RFC3339, f.At)
	if err != nil {
		t.Fatalf("at must be a valid RFC3339 timestamp: %v", err)
	}
	if at.Before(before.Add(-time.Minute)) || at.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("at must be close to now, got %v", at)
	}
}

func TestAddFactDefaultByIsKept(t *testing.T) {
	v := newVault(t)
	if _, err := vault.AddFact(v, "my-stack", "human"); err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil || len(facts) != 1 {
		t.Fatalf("the new fact must list: %v %v", facts, err)
	}
	if facts[0].By != "human" || facts[0].Review != "kept" {
		t.Fatalf("a human write must be kept, got: %+v", facts[0])
	}
}
