// Package adapter projects the vault into each agent tool.
package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	beginMark = "<!-- loadout:begin -->"
	endMark   = "<!-- loadout:end -->"
)

// WriteManagedBlock puts content between the loadout marks in the file.
// If the file has no marks, append the block. If the file has no marks
// and does not exist, create the file with the block. If the file has
// exactly one begin mark before one end mark, replace the block. If
// the marks are damaged, return an error and do not write. Never modify
// text outside the marks.
func WriteManagedBlock(path, content string) error {
	trimmed := strings.TrimSpace(content)
	if strings.Contains(trimmed, beginMark) || strings.Contains(trimmed, endMark) {
		return fmt.Errorf("the content for %s holds a loadout mark: remove the mark text from the source item", path)
	}
	if err := checkManagedBlockDamage(path); err != nil {
		return err
	}
	block := beginMark + "\n" + trimmed + "\n" + endMark
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeFileAtomic(path, []byte(block+"\n"))
	}
	if err != nil {
		return err
	}
	text := string(data)
	beginCount := strings.Count(text, beginMark)
	endCount := strings.Count(text, endMark)

	// No marks: append the block.
	if beginCount == 0 && endCount == 0 {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + block + "\n"
		return writeFileAtomic(path, []byte(text))
	}

	// Exactly one of each mark: replace the block.
	if beginCount == 1 && endCount == 1 {
		i := strings.Index(text, beginMark)
		j := strings.Index(text, endMark)
		if i < j {
			text = text[:i] + block + text[j+len(endMark):]
			return writeFileAtomic(path, []byte(text))
		}
	}

	// Damaged marks: return error without writing. In practice
	// checkManagedBlockDamage above already caught this; this stays as
	// a defensive fallback so a race between the two reads still fails
	// safely instead of writing over a damaged file.
	return fmt.Errorf("the loadout marks in %s are damaged. Fix: repair or remove the marks in %s.", path, path)
}

// checkManagedBlockDamage reads the file at path and reports whether
// its loadout marks are damaged: any count of begin and end marks
// other than zero of each, or exactly one of each with the begin mark
// before the end mark. A missing file is not damaged, and returns
// nil. WriteManagedBlock calls this before it writes; each adapter's
// dry run calls it before it decides whether the block would change,
// so a dry run fails the same way a real sync would on a corrupted
// file.
func checkManagedBlockDamage(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	beginCount := strings.Count(text, beginMark)
	endCount := strings.Count(text, endMark)
	if beginCount == 0 && endCount == 0 {
		return nil
	}
	if beginCount == 1 && endCount == 1 && strings.Index(text, beginMark) < strings.Index(text, endMark) {
		return nil
	}
	return fmt.Errorf("the loadout marks in %s are damaged. Fix: repair or remove the marks in %s.", path, path)
}

// writeFileAtomic writes data to path so a reader never sees a partial
// file. It writes to a temp file in the same directory, then renames
// the temp file over path. It keeps path's existing mode, or uses
// 0o644 for a new file.
func writeFileAtomic(path string, data []byte) error {
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

// managedBlockDryMsg reports what a dry run would do to the managed
// block at path, without writing anything: "memory: up to date" when
// the block already holds want, "memory: block would change"
// otherwise (including when the block or the file is missing). The
// caller must call checkManagedBlockDamage on path first and return
// its error if not nil: managedBlockDryMsg does not check for damaged
// marks itself, and would call a damaged file "would change" instead
// of reporting the damage.
func managedBlockDryMsg(path, want string) string {
	got, ok := ReadManagedBlock(path)
	if ok && got == want {
		return "memory: up to date"
	}
	return "memory: block would change"
}

// ReadManagedBlock returns the trimmed block content, and whether a
// well-formed block exists. Return ("", false) if marks are damaged or
// missing.
func ReadManagedBlock(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := string(data)
	beginCount := strings.Count(text, beginMark)
	endCount := strings.Count(text, endMark)

	// Exactly one of each mark, with begin before end.
	if beginCount == 1 && endCount == 1 {
		i := strings.Index(text, beginMark)
		j := strings.Index(text, endMark)
		if i < j {
			inner := strings.TrimSpace(strings.TrimPrefix(text[i:j], beginMark))
			return inner, true
		}
	}

	return "", false
}
