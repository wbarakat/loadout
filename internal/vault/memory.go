package vault

import (
	"os"
	"path/filepath"
	"strings"
)

// Fact is one curated memory item.
type Fact struct {
	Name        string
	Description string
	Type        string
	Body        string
	Path        string
	// By names who wrote this fact, for example "human" or
	// "claude-code". Empty for a fact scaffolded before provenance
	// tracking existed.
	By string
	// At is the RFC3339 time of the write. Empty for a fact
	// scaffolded before provenance tracking existed.
	At string
	// Review is "kept" or "draft". Empty means the same as "kept":
	// a fact scaffolded before provenance tracking existed has no
	// review field at all, and must count as already reviewed.
	Review string
}

// parseFrontmatter splits simple "key: value" frontmatter from the body.
// It strips a leading UTF-8 byte order mark, and normalizes CRLF line
// endings to LF, so a file from another editor still parses.
func parseFrontmatter(raw []byte) (map[string]string, string) {
	text := string(raw)
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	fields := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return fields, text
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fields, text
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	body := strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return fields, body
}

// ListFacts reads every *.md file in the memory directory, in name order.
func ListFacts(v *Vault) ([]Fact, error) {
	entries, err := os.ReadDir(v.MemoryDir())
	if err != nil {
		return nil, err
	}
	var facts []Fact
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(v.MemoryDir(), e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fields, body := parseFrontmatter(raw)
		name := fields["name"]
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		facts = append(facts, Fact{
			Name:        name,
			Description: fields["description"],
			Type:        fields["type"],
			Body:        body,
			Path:        path,
			By:          fields["by"],
			At:          fields["at"],
			Review:      fields["review"],
		})
	}
	return facts, nil
}

// RenderMemory turns facts into one markdown document.
func RenderMemory(facts []Fact) string {
	var b strings.Builder
	b.WriteString("# Memory (synced by Loadout — do not edit here)\n")
	for _, f := range facts {
		b.WriteString("\n## " + f.Name + "\n\n" + strings.TrimSpace(f.Body) + "\n")
	}
	return b.String()
}
