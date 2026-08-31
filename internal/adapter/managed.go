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
	block := beginMark + "\n" + strings.TrimSpace(content) + "\n" + endMark
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

	// Damaged marks: return error without writing.
	return fmt.Errorf("the loadout marks in %s are damaged: repair or remove them", path)
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
