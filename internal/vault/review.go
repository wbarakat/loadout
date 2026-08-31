package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetReviewKept rewrites path's review frontmatter line to
// "review: kept". It changes only that one line. Every other byte of
// the file stays the same, including exact whitespace. When the file
// has no review line yet, SetReviewKept adds one just before the
// closing "---". The write is atomic, so a reader never sees a
// partial file.
func SetReviewKept(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return fmt.Errorf("%s: has no frontmatter to review", path)
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fmt.Errorf("%s: the frontmatter has no closing marker", path)
	}

	lines := strings.Split(rest[:end], "\n")
	found := false
	for i, line := range lines {
		k, _, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "review" {
			lines[i] = "review: kept"
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, "review: kept")
	}

	newText := "---\n" + strings.Join(lines, "\n") + rest[end:]
	return writeFileAtomicVault(path, []byte(newText))
}

// writeFileAtomicVault writes data to path so a reader never sees a
// partial file. It writes to a temp file in the same directory, then
// renames the temp file over path. It keeps path's existing file
// mode. This is a vault-local copy of the adapter package's atomic
// write helper: the vault package must not import adapter.
func writeFileAtomicVault(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".loadout-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
