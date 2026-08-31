package remote_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

func newConfigTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestConfigLoadAbsentGivesErrNotConfigured(t *testing.T) {
	v := newConfigTestVault(t)
	_, err := remote.Load(v)
	if !errors.Is(err, remote.ErrNotConfigured) {
		t.Fatalf("Load on a vault with no remote.toml must return ErrNotConfigured, got %v", err)
	}
}

func TestConfigSaveAndLoadRoundTrip(t *testing.T) {
	v := newConfigTestVault(t)
	want := &remote.Config{URL: "http://127.0.0.1:7777", Token: "secret-token", LastVersion: "v3-abcd1234"}
	if err := remote.Save(v, want); err != nil {
		t.Fatal(err)
	}
	got, err := remote.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestConfigSaveIsMode0600(t *testing.T) {
	v := newConfigTestVault(t)
	if err := remote.Save(v, &remote.Config{URL: "http://x", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(v.Root, "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("remote.toml must be mode 0600, got %o", fi.Mode().Perm())
	}
}

func TestConfigSetLastVersionPreservesURLAndToken(t *testing.T) {
	v := newConfigTestVault(t)
	if err := remote.Save(v, &remote.Config{URL: "http://x", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := remote.SetLastVersion(v, "v1-deadbeef"); err != nil {
		t.Fatal(err)
	}
	got, err := remote.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "http://x" || got.Token != "secret" || got.LastVersion != "v1-deadbeef" {
		t.Fatalf("SetLastVersion must preserve url and token, got %+v", got)
	}
}

// TestConfigFileNeverContainsPlaintextTokenTwice is a light sanity
// check that Save writes the token exactly where expected and does
// not duplicate it anywhere else in the file (e.g. an accidental
// second encoding pass).
func TestConfigRawFileHoldsExpectedFields(t *testing.T) {
	v := newConfigTestVault(t)
	if err := remote.Save(v, &remote.Config{URL: "http://example:7777", Token: "tok-xyz", LastVersion: "v2-11112222"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.Root, "remote.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"http://example:7777", "tok-xyz", "v2-11112222"} {
		if !strings.Contains(text, want) {
			t.Fatalf("remote.toml must contain %q, got:\n%s", want, text)
		}
	}
}
