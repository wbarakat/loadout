package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestSnapshotRefusesEmbeddedGitRepo(t *testing.T) {
	v := newVault(t)
	dir := filepath.Join(v.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := vault.Snapshot(v, "add skill deploy-checks")
	if err == nil {
		t.Fatal("Snapshot must refuse a skill folder that is a git repository")
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("error must name the skill folder, got %v", err)
	}
}
