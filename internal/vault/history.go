package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitQuietOpts are passed to every git call the vault makes. They stop
// git from starting background housekeeping inside the vault:
//
//   - gc.auto=0 stops "git gc --auto", which git runs on its own after
//     a commit once enough loose objects pile up.
//   - maintenance.auto=false stops the newer background maintenance
//     task that replaces it.
//
// A vault is small and Loadout commits often, so this housekeeping buys
// nothing. It also writes and then deletes lock files under .git
// (.git/objects/maintenance.lock, for example) while other code is
// reading the vault tree, which makes any walk of the vault race with a
// file that disappears. Passing the options on each call, rather than
// writing them into the repository config at init, covers vaults that
// already exist as well as new ones.
var gitQuietOpts = []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}

func git(v *Vault, args ...string) (string, error) {
	full := append([]string{"-C", v.Root}, gitQuietOpts...)
	cmd := exec.Command("git", append(full, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s failed: %s", args[0], msg)
	}
	return out.String(), nil
}

// noHistoryErr turns a git failure into a fixed, friendly error when
// the cause is a vault with no history at all — for example, one
// someone removed .git from by hand. It leaves any other git failure
// alone, so a real git problem still shows its own message. log,
// context, and undo call this on a git error, so a dead vault never
// stares back with a bare git failure.
func noHistoryErr(v *Vault, gitErr error) error {
	if _, err := os.Stat(filepath.Join(v.Root, ".git")); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the vault at %s has no history. Fix: run loadout doctor.", v.Root)
	}
	return gitErr
}

func initHistory(v *Vault) error {
	if _, err := git(v, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	return Snapshot(v, "init the vault")
}

// EmbeddedSkillRepos lists every skill folder that holds its own git
// repository. The vault keeps one history for everything inside it;
// a nested repository there would confuse that history.
func EmbeddedSkillRepos(v *Vault) ([]string, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(v.SkillsDir(), e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			found = append(found, dir)
		}
	}
	return found, nil
}

// RecentSubjects returns the subject line of each of the last n
// commits, most recent first. It returns fewer than n lines when the
// history holds fewer commits than that.
func RecentSubjects(v *Vault, n int) ([]string, error) {
	out, err := git(v, "log", "--format=%s", "-n", strconv.Itoa(n))
	if err != nil {
		return nil, noHistoryErr(v, err)
	}
	var subjects []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}

// Snapshot records the vault state in history. It does nothing when
// nothing changed.
func Snapshot(v *Vault, message string) error {
	repos, err := EmbeddedSkillRepos(v)
	if err != nil {
		return err
	}
	if len(repos) > 0 {
		return fmt.Errorf("the skill folder %s is a git repository. Fix: remove its .git directory; the vault keeps history for you.", repos[0])
	}
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
