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
		return os.WriteFile(path, []byte(block+"\n"), 0o644)
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
		return os.WriteFile(path, []byte(text), 0o644)
	}

	// Exactly one of each mark: replace the block.
	if beginCount == 1 && endCount == 1 {
		i := strings.Index(text, beginMark)
		j := strings.Index(text, endMark)
		if i < j {
			text = text[:i] + block + text[j+len(endMark):]
			return os.WriteFile(path, []byte(text), 0o644)
		}
	}

	// Damaged marks: return error without writing.
	return fmt.Errorf("the loadout marks in %s are damaged: repair or remove them", path)
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
