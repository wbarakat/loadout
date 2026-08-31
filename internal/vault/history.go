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

func git(v *Vault, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", v.Root}, args...)...)
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
		return nil, err
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
