package vault

import (
	"bytes"
	"os/exec"
)

func git(v *Vault, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", v.Root}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func initHistory(v *Vault) error {
	if _, err := git(v, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	return Snapshot(v, "init the vault")
}

// Snapshot records the vault state in history. It does nothing when
// nothing changed.
func Snapshot(v *Vault, message string) error {
	if _, err := git(v, "add", "-A"); err != nil {
		return err
	}
	out, err := git(v, "status", "--porcelain")
	if err != nil || out == "" {
		return err
	}
	_, err = git(v, "-c", "user.name=loadout", "-c", "user.email=history@loadout.local",
		"commit", "-q", "-m", message)
	return err
}
