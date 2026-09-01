package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// AccessEntry is one line of the vault's access log: a record of one
// secret decrypt. It NEVER holds the secret value (INVARIANT 10) —
// only who read what, and when.
type AccessEntry struct {
	At     string `json:"at"`
	Verb   string `json:"verb"`
	Secret string `json:"secret"`
	Tool   string `json:"tool"`
}

// accessLogPath returns the path to the vault's access log: a
// device-local, gitignored file (see vault.gitignoreContent) that
// never syncs and never enters history.
func accessLogPath(v *vault.Vault) string {
	return filepath.Join(v.Root, "access.log")
}

// AppendAccessLog appends one JSON line to the vault's access log,
// creating the file on first use, mode 0600. Every call opens, writes,
// and closes the file, so two commands running at once each append
// their own complete line rather than interleaving partial writes.
//
// INVARIANT 10: entry carries no value field, so a decrypt can never
// leak its plaintext into this file.
func AppendAccessLog(v *vault.Vault, entry AccessEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(accessLogPath(v), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
