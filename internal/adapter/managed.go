// Package adapter projects the vault into each agent tool.
package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	beginMark = "<!-- loadout:begin -->"
	endMark   = "<!-- loadout:end -->"
)

// WriteManagedBlock puts content between the loadout marks in the file.
// It replaces an existing block. It appends a block to an existing
// file. It creates the file when the file is absent. It never touches
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
	i := strings.Index(text, beginMark)
	j := strings.Index(text, endMark)
	if i >= 0 && j > i {
		text = text[:i] + block + text[j+len(endMark):]
	} else {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + block + "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// ReadManagedBlock returns the trimmed block content, and whether a
// block exists.
func ReadManagedBlock(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := string(data)
	i := strings.Index(text, beginMark)
	j := strings.Index(text, endMark)
	if i < 0 || j <= i {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimPrefix(text[i:j], beginMark))
	return inner, true
}
