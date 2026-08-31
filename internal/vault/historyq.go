package vault

import (
	"errors"
	"strconv"
	"strings"
)

// HistoryEntry is one line of vault history. At holds the commit
// date, in short (YYYY-MM-DD) form.
type HistoryEntry struct {
	At      string
	Subject string
}

// History returns the last n vault history entries, most recent
// first. It returns fewer than n entries when the history holds
// fewer commits than that.
func History(v *Vault, n int) ([]HistoryEntry, error) {
	out, err := git(v, "log", "--format=%ad|%s", "--date=short", "-n", strconv.Itoa(n))
	if err != nil {
		return nil, noHistoryErr(v, err)
	}
	var entries []HistoryEntry
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		at, subject, found := strings.Cut(line, "|")
		if !found {
			continue
		}
		entries = append(entries, HistoryEntry{At: at, Subject: subject})
	}
	return entries, nil
}

// Undo reverts the vault's tracked files to the state they held
// before its last history entry, then records that as a new entry.
// History stays forward-only: Undo never rewrites or drops a commit,
// it adds one on top.
//
// The revert uses "git read-tree --reset -u HEAD~1", which makes the
// working tree and the index match the HEAD~1 tree exactly: files
// changed since then are restored, and files added since then are
// removed (along with any directory that add left empty). Plain "git
// checkout HEAD~1 -- ." cannot do the second part, since it never
// touches a path absent from HEAD~1. A file git never tracked (a
// foreign file, or one loadout.lock/.gitignore excludes) has no index
// entry to compare, so read-tree never touches it either.
func Undo(v *Vault) error {
	out, err := git(v, "log", "--format=%H", "-n", "2")
	if err != nil {
		return noHistoryErr(v, err)
	}
	var hashes []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			hashes = append(hashes, line)
		}
	}
	if len(hashes) < 2 {
		return errors.New("nothing to undo: the vault has no earlier state.")
	}
	if _, err := git(v, "read-tree", "--reset", "-u", "HEAD~1"); err != nil {
		return err
	}
	return Snapshot(v, "undo")
}
