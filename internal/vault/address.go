package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseAddress splits an item address of the form "kind/name" into
// its kind and name. The only valid kinds are "skill" and "memory".
// The name must not hold a path separator ("/" or "\"), a ".."
// segment, or a leading ".", so an address can never resolve outside
// the vault.
func ParseAddress(s string) (kind, name string, err error) {
	badAddr := fmt.Errorf("%s: not an address. Fix: use kind/name, for example memory/my-stack.", s)
	k, n, ok := strings.Cut(s, "/")
	if !ok || k == "" || n == "" || (k != "skill" && k != "memory") {
		return "", "", badAddr
	}
	if strings.ContainsAny(n, "/\\") || n == ".." || strings.HasPrefix(n, ".") {
		return "", "", badAddr
	}
	return k, n, nil
}

// ItemPath returns the file path for the item named kind/name. kind
// must be "skill" or "memory". It returns an error when no such
// file exists on disk.
func ItemPath(v *Vault, kind, name string) (string, error) {
	notFound := fmt.Errorf("%s/%s: no such item. Fix: run loadout list.", kind, name)
	var path string
	switch kind {
	case "memory":
		path = filepath.Join(v.MemoryDir(), name+".md")
	case "skill":
		path = filepath.Join(v.SkillsDir(), name, "SKILL.md")
	default:
		return "", notFound
	}
	if _, err := os.Stat(path); err != nil {
		return "", notFound
	}
	return path, nil
}
