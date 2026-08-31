# Loadout Phase 1 Implementation Plan — Local Vault + First Adapters

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `loadout` CLI with a local vault and three adapters (Claude Code, pi, generic `AGENTS.md`), so one vault edit reaches every local tool.

**Architecture:** A Go CLI owns a plain-file vault at `~/.loadout` (skills as folders, memory as markdown facts, one TOML manifest). Adapters project the vault into each tool: symlinks for skills, managed markdown blocks for memory. A hidden git repo inside the vault records history for undo.

**Tech Stack:** Go (stdlib CLI, no framework), `github.com/BurntSushi/toml` (the only dependency), `git` on PATH for vault history, Go `testing` package.

**Spec:** `/Users/waleed/loadout/PLAN.md` (Phase 1 section, plus sections 4, 6, 7).

## Global Constraints

- Go 1.22 or later. One external dependency only: `github.com/BurntSushi/toml`.
- Module path: `loadout.dev/loadout`.
- The CLI verbs in Phase 1 are: `init`, `add`, `sync`, `status`, `doctor`. The `edit` verb ships in a later phase. Cloud sync ships in Phase 3; in Phase 1, `sync` projects the vault into local tools only.
- No secrets code in Phase 1. Secrets ship in Phase 4.
- Adapters must never destroy user content. Write only inside managed blocks (`<!-- loadout:begin -->` … `<!-- loadout:end -->`) or as new symlinks. Never overwrite a real file or directory with a symlink.
- All tool target paths come from the manifest and support `~` expansion. Tests must never touch the real home directory: always set `HOME` and `LOADOUT_HOME` to temp dirs with `t.Setenv`.
- Write all prose (docs, comments, CLI output, commit messages) in ASD-STE100 Simplified Technical English: short sentences, active voice, imperative instructions.
- End every commit message with this trailer (blank line before it):
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- Run `gofmt -l .` before each commit. It must print nothing.

## File Structure

```
loadout/
  go.mod
  cmd/loadout/main.go              — entry point, calls cli.Run
  internal/vault/manifest.go       — Manifest, AdapterConfig, Load/Save, ExpandPath
  internal/vault/vault.go          — Vault type, Init, Open, path helpers
  internal/vault/history.go        — git-backed Snapshot
  internal/vault/memory.go         — Fact, ListFacts, RenderMemory, frontmatter parse
  internal/vault/skill.go          — Skill, ListSkills, InvalidSkillDirs
  internal/vault/scaffold.go       — AddSkill, AddFact templates
  internal/adapter/adapter.go      — Adapter interface, Problem, Enabled registry
  internal/adapter/managed.go      — managed block read/write
  internal/adapter/links.go        — LinkSkills symlink helper
  internal/adapter/claudecode.go   — Claude Code adapter
  internal/adapter/pi.go           — pi adapter
  internal/adapter/agentsmd.go     — generic AGENTS.md adapter
  internal/cli/run.go              — command dispatch + usage
  internal/cli/init.go, add.go, sync.go, status.go, doctor.go
  README.md                        — quickstart
```

Test files sit next to their package as `*_test.go` in the external test package (for example `package vault_test`).

---

### Task 1: Module scaffold + manifest

**Files:**
- Create: `go.mod`, `internal/vault/manifest.go`
- Test: `internal/vault/manifest_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Manifest struct { Version int; Adapters map[string]AdapterConfig }`, `type AdapterConfig struct { Enabled bool; SkillsDir, MemoryFile string; Targets []string }`, `DefaultManifest() Manifest`, `LoadManifest(path string) (Manifest, error)`, `SaveManifest(path string, m Manifest) error`, `ExpandPath(p string) string`.

- [ ] **Step 1: Create the module**

```bash
cd /Users/waleed/loadout
go mod init loadout.dev/loadout
go get github.com/BurntSushi/toml@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/vault/manifest_test.go`:

```go
package vault_test

import (
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.toml")
	if err := vault.SaveManifest(path, vault.DefaultManifest()); err != nil {
		t.Fatal(err)
	}
	m, err := vault.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	cc := m.Adapters["claude-code"]
	if !cc.Enabled || cc.SkillsDir != "~/.claude/skills" || cc.MemoryFile != "~/.claude/CLAUDE.md" {
		t.Fatalf("bad claude-code config: %+v", cc)
	}
	if m.Adapters["agents-md"].Enabled {
		t.Fatal("agents-md must start disabled")
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	if got := vault.ExpandPath("~/x"); got != "/tmp/fakehome/x" {
		t.Fatalf("got %q", got)
	}
	if got := vault.ExpandPath("/abs/x"); got != "/abs/x" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify failure**

Run: `go test ./internal/vault/ -v`
Expected: FAIL (package does not compile — functions not defined).

- [ ] **Step 4: Write the implementation**

Create `internal/vault/manifest.go`:

```go
// Package vault owns the loadout vault: its manifest, its content,
// and its history.
package vault

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Manifest struct {
	Version  int                      `toml:"version"`
	Adapters map[string]AdapterConfig `toml:"adapters"`
}

type AdapterConfig struct {
	Enabled    bool     `toml:"enabled"`
	SkillsDir  string   `toml:"skills_dir,omitempty"`
	MemoryFile string   `toml:"memory_file,omitempty"`
	Targets    []string `toml:"targets,omitempty"`
}

func DefaultManifest() Manifest {
	return Manifest{
		Version: 1,
		Adapters: map[string]AdapterConfig{
			"claude-code": {Enabled: true, SkillsDir: "~/.claude/skills", MemoryFile: "~/.claude/CLAUDE.md"},
			"pi":          {Enabled: true, SkillsDir: "~/.pi/agent/skills", MemoryFile: "~/.pi/AGENTS.md"},
			"agents-md":   {Enabled: false},
		},
	}
}

func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	_, err := toml.DecodeFile(path, &m)
	return m, err
}

func SaveManifest(path string, m Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(m)
}

// ExpandPath replaces a leading "~" with the user home directory.
func ExpandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
```

- [ ] **Step 5: Run the tests to verify pass**

Run: `go test ./internal/vault/ -v`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the module and the vault manifest"
```
(Include the trailer from Global Constraints in this and every commit.)

---

### Task 2: Vault init, open, and git history

**Files:**
- Create: `internal/vault/vault.go`, `internal/vault/history.go`
- Test: `internal/vault/vault_test.go`

**Interfaces:**
- Consumes: `Manifest`, `DefaultManifest`, `SaveManifest`, `LoadManifest`, `ExpandPath` (Task 1).
- Produces: `type Vault struct { Root string; Manifest Manifest }`, `Init(root string) (*Vault, error)`, `Open(root string) (*Vault, error)`, `DefaultRoot() string`, `(*Vault).SkillsDir() string`, `(*Vault).MemoryDir() string`, `(*Vault).RenderDir() string`, `Snapshot(v *Vault, message string) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/vault_test.go`:

```go
package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestInitAndOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{v.SkillsDir(), v.MemoryDir(), v.RenderDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Fatalf("missing directory %s", d)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatal("history repo is missing")
	}
	if _, err := vault.Init(root); err == nil {
		t.Fatal("second init must fail")
	}
	v2, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !v2.Manifest.Adapters["claude-code"].Enabled {
		t.Fatal("manifest did not load")
	}
}

func TestOpenMissingVault(t *testing.T) {
	if _, err := vault.Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("open must fail without a vault")
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv("LOADOUT_HOME", "/tmp/lo")
	if got := vault.DefaultRoot(); got != "/tmp/lo" {
		t.Fatalf("got %q", got)
	}
}

func TestSnapshotRecordsChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a fact"); err != nil {
		t.Fatal(err)
	}
	// A snapshot with no changes must not fail.
	if err := vault.Snapshot(v, "empty"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/vault/ -v`
Expected: FAIL (compile errors: `Init`, `Open`, `Snapshot` not defined).

- [ ] **Step 3: Write the implementation**

Create `internal/vault/vault.go`:

```go
package vault

import (
	"fmt"
	"os"
	"path/filepath"
)

type Vault struct {
	Root     string
	Manifest Manifest
}

// DefaultRoot returns $LOADOUT_HOME, or ~/.loadout.
func DefaultRoot() string {
	if h := os.Getenv("LOADOUT_HOME"); h != "" {
		return h
	}
	return ExpandPath("~/.loadout")
}

func Init(root string) (*Vault, error) {
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err == nil {
		return nil, fmt.Errorf("a vault already exists at %s", root)
	}
	for _, d := range []string{root, filepath.Join(root, "skills"), filepath.Join(root, "memory"), filepath.Join(root, "render")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	m := DefaultManifest()
	if err := SaveManifest(filepath.Join(root, "loadout.toml"), m); err != nil {
		return nil, err
	}
	v := &Vault{Root: root, Manifest: m}
	if err := initHistory(v); err != nil {
		return nil, err
	}
	return v, nil
}

func Open(root string) (*Vault, error) {
	m, err := LoadManifest(filepath.Join(root, "loadout.toml"))
	if err != nil {
		return nil, fmt.Errorf("no vault at %s: run \"loadout init\" first", root)
	}
	return &Vault{Root: root, Manifest: m}, nil
}

func (v *Vault) SkillsDir() string { return filepath.Join(v.Root, "skills") }
func (v *Vault) MemoryDir() string { return filepath.Join(v.Root, "memory") }
func (v *Vault) RenderDir() string { return filepath.Join(v.Root, "render") }
```

Create `internal/vault/history.go`:

```go
package vault

import (
	"bytes"
	"os/exec"
)

func git(v *Vault, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", v.Root}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func initHistory(v *Vault) error {
	if _, err := git(v, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	return Snapshot(v, "init the vault")
}

// Snapshot records the vault state in history. It does nothing when
// nothing changed.
func Snapshot(v *Vault, message string) error {
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
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/vault/ -v`
Expected: PASS (all tests).

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add vault init, open, and git history"
```

---

### Task 3: Memory facts

**Files:**
- Create: `internal/vault/memory.go`
- Test: `internal/vault/memory_test.go`

**Interfaces:**
- Consumes: `Vault`, `Init` (Task 2).
- Produces: `type Fact struct { Name, Description, Type, Body, Path string }`, `ListFacts(v *Vault) ([]Fact, error)`, `RenderMemory(facts []Fact) string`, and the unexported `parseFrontmatter(raw []byte) (map[string]string, string)` reused by Task 4.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/memory_test.go`:

```go
package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestListFacts(t *testing.T) {
	v := newVault(t)
	fact := "---\nname: my-stack\ndescription: the stack I use\ntype: user\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "my-stack.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file without frontmatter must still load.
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "plain.md"), []byte("Just a note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("want 2 facts, got %d", len(facts))
	}
	f := facts[0]
	if f.Name != "my-stack" || f.Type != "user" || !strings.Contains(f.Body, "Go and Postgres") {
		t.Fatalf("bad fact: %+v", f)
	}
	if strings.Contains(f.Body, "---") {
		t.Fatal("body must not contain frontmatter")
	}
	if facts[1].Name != "plain" {
		t.Fatalf("bad fallback name: %q", facts[1].Name)
	}
}

func TestRenderMemory(t *testing.T) {
	out := vault.RenderMemory([]vault.Fact{
		{Name: "a", Body: "Fact A."},
		{Name: "b", Body: "Fact B."},
	})
	if !strings.Contains(out, "## a") || !strings.Contains(out, "Fact B.") {
		t.Fatalf("bad render:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/vault/ -v`
Expected: FAIL (compile errors: `Fact`, `ListFacts`, `RenderMemory` not defined).

- [ ] **Step 3: Write the implementation**

Create `internal/vault/memory.go`:

```go
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
}

// parseFrontmatter splits simple "key: value" frontmatter from the body.
func parseFrontmatter(raw []byte) (map[string]string, string) {
	text := string(raw)
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
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/vault/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add memory facts and the memory render"
```

---

### Task 4: Skills listing

**Files:**
- Create: `internal/vault/skill.go`
- Test: `internal/vault/skill_test.go`

**Interfaces:**
- Consumes: `Vault` (Task 2), `parseFrontmatter` (Task 3).
- Produces: `type Skill struct { Name, Description, Dir string }`, `ListSkills(v *Vault) ([]Skill, error)`, `InvalidSkillDirs(v *Vault) ([]string, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/skill_test.go` (reuses `newVault` from Task 3's test file):

```go
package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func writeSkill(t *testing.T, v *vault.Vault, name, description string) {
	t.Helper()
	dir := filepath.Join(v.SkillsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListSkills(t *testing.T) {
	v := newVault(t)
	writeSkill(t, v, "deploy-checks", "run checks before a deploy")
	if err := os.MkdirAll(filepath.Join(v.SkillsDir(), "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if s.Name != "deploy-checks" || s.Description != "run checks before a deploy" {
		t.Fatalf("bad skill: %+v", s)
	}
	if s.Dir != filepath.Join(v.SkillsDir(), "deploy-checks") {
		t.Fatalf("bad dir: %q", s.Dir)
	}
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || filepath.Base(bad[0]) != "broken" {
		t.Fatalf("bad invalid list: %v", bad)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/vault/ -v`
Expected: FAIL (compile errors).

- [ ] **Step 3: Write the implementation**

Create `internal/vault/skill.go`:

```go
package vault

import (
	"os"
	"path/filepath"
)

// Skill is one skill folder with a SKILL.md file.
type Skill struct {
	Name        string
	Description string
	Dir         string
}

// ListSkills reads every skill folder, in name order. It skips a
// folder without a SKILL.md file; InvalidSkillDirs reports those.
func ListSkills(v *Vault) ([]Skill, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(v.SkillsDir(), e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		fields, _ := parseFrontmatter(raw)
		skills = append(skills, Skill{Name: e.Name(), Description: fields["description"], Dir: dir})
	}
	return skills, nil
}

// InvalidSkillDirs lists skill folders without a SKILL.md file.
func InvalidSkillDirs(v *Vault) ([]string, error) {
	entries, err := os.ReadDir(v.SkillsDir())
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(v.SkillsDir(), e.Name())
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			bad = append(bad, dir)
		}
	}
	return bad, nil
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/vault/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the skills listing"
```

---

### Task 5: Managed block writer

**Files:**
- Create: `internal/adapter/managed.go`
- Test: `internal/adapter/managed_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `WriteManagedBlock(path, content string) error`, `ReadManagedBlock(path string) (string, bool)`. Marks: `<!-- loadout:begin -->` and `<!-- loadout:end -->`. `ReadManagedBlock` returns the trimmed inner content.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/managed_test.go`:

```go
package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
)

func TestManagedBlockCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "CLAUDE.md")
	if err := adapter.WriteManagedBlock(path, "hello"); err != nil {
		t.Fatal(err)
	}
	got, ok := adapter.ReadManagedBlock(path)
	if !ok || got != "hello" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestManagedBlockPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My own rules\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteManagedBlock(path, "v2"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, "Keep me.") {
		t.Fatal("user content was lost")
	}
	if strings.Contains(text, "v1") {
		t.Fatal("old block was not replaced")
	}
	if strings.Count(text, "<!-- loadout:begin -->") != 1 {
		t.Fatal("must have exactly one block")
	}
	got, ok := adapter.ReadManagedBlock(path)
	if !ok || got != "v2" {
		t.Fatalf("got %q", got)
	}
}

func TestReadManagedBlockMissing(t *testing.T) {
	if _, ok := adapter.ReadManagedBlock(filepath.Join(t.TempDir(), "nope.md")); ok {
		t.Fatal("must report missing")
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/adapter/ -v`
Expected: FAIL (compile errors).

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/managed.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/adapter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the managed block writer"
```

---

### Task 6: Adapter interface + skill symlinks

**Files:**
- Create: `internal/adapter/adapter.go`, `internal/adapter/links.go`
- Test: `internal/adapter/links_test.go`

**Interfaces:**
- Consumes: `vault.Skill` (Task 4).
- Produces: `type Problem struct { Adapter, Detail, Fix string }`, `type Adapter interface { Name() string; Apply(v *vault.Vault) error; Check(v *vault.Vault) []Problem }`, `LinkSkills(skills []vault.Skill, dir string) (blocked []string, err error)`. The `Enabled` registry arrives in Task 9.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/links_test.go`:

```go
package adapter_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestLinkSkills(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "tool", "skills")
	skillDir := filepath.Join(src, "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "deploy-checks", Dir: skillDir}}

	blocked, err := adapter.LinkSkills(skills, dst)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("err=%v blocked=%v", err, blocked)
	}
	got, err := os.Readlink(filepath.Join(dst, "deploy-checks"))
	if err != nil || got != skillDir {
		t.Fatalf("bad link: %q err=%v", got, err)
	}
	// A second run must not fail (idempotent).
	if _, err := adapter.LinkSkills(skills, dst); err != nil {
		t.Fatal(err)
	}
}

func TestLinkSkillsRepairsWrongLink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(src, "a")
	os.MkdirAll(skillDir, 0o755)
	os.Symlink("/wrong/place", filepath.Join(dst, "a"))
	if _, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(filepath.Join(dst, "a"))
	if got != skillDir {
		t.Fatalf("link was not repaired: %q", got)
	}
}

func TestLinkSkillsRefusesRealDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(src, "a")
	os.MkdirAll(skillDir, 0o755)
	os.MkdirAll(filepath.Join(dst, "a"), 0o755) // a real dir owned by the user
	blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0] != "a" {
		t.Fatalf("must report the blocked name, got %v", blocked)
	}
	if fi, _ := os.Lstat(filepath.Join(dst, "a")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("must not replace a real directory")
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/adapter/ -v`
Expected: FAIL (compile errors).

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/adapter.go`:

```go
package adapter

import "loadout.dev/loadout/internal/vault"

// Problem is one finding from a check, with a fix the user can run.
type Problem struct {
	Adapter string
	Detail  string
	Fix     string
}

// Adapter projects the vault into one tool.
type Adapter interface {
	Name() string
	Apply(v *vault.Vault) error
	Check(v *vault.Vault) []Problem
}
```

Create `internal/adapter/links.go`:

```go
package adapter

import (
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// LinkSkills creates one symlink per skill in dir. It repairs a wrong
// link. It never replaces a real file or directory; it returns those
// names as blocked.
func LinkSkills(skills []vault.Skill, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var blocked []string
	for _, s := range skills {
		linkPath := filepath.Join(dir, s.Name)
		fi, err := os.Lstat(linkPath)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			if cur, _ := os.Readlink(linkPath); cur == s.Dir {
				continue
			}
			if err := os.Remove(linkPath); err != nil {
				return blocked, err
			}
		case err == nil:
			blocked = append(blocked, s.Name)
			continue
		}
		if err := os.Symlink(s.Dir, linkPath); err != nil {
			return blocked, err
		}
	}
	return blocked, nil
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/adapter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the adapter interface and the skill links"
```

---

### Task 7: Claude Code adapter

**Files:**
- Create: `internal/adapter/claudecode.go`
- Test: `internal/adapter/claudecode_test.go`

**Interfaces:**
- Consumes: `vault.ListSkills`, `vault.ListFacts`, `vault.RenderMemory`, `vault.ExpandPath`, `(*vault.Vault).RenderDir`, `LinkSkills`, `WriteManagedBlock`, `ReadManagedBlock`, `Problem`.
- Produces: `type ClaudeCode struct { Cfg vault.AdapterConfig }` with `Name`, `Apply`, `Check`. The memory block content is exactly `"@" + filepath.Join(v.RenderDir(), "memory.md")` (a CLAUDE.md import line), and the rendered memory lands in `render/memory.md`.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/claudecode_test.go`:

```go
package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func testVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	// One skill and one fact.
	dir := filepath.Join(v.SkillsDir(), "deploy-checks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: deploy-checks\ndescription: run checks\n---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\ntype: user\n---\nI use Go.\n"), 0o644)
	return v
}

func TestClaudeCodeApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}

	if got := a.Name(); got != "claude-code" {
		t.Fatalf("bad name %q", got)
	}
	if len(a.Check(v)) == 0 {
		t.Fatal("check must report problems before apply")
	}
	if err := a.Apply(v); err != nil {
		t.Fatal(err)
	}
	// The skill is a symlink into the vault.
	got, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks"))
	if err != nil || got != filepath.Join(v.SkillsDir(), "deploy-checks") {
		t.Fatalf("bad link %q err=%v", got, err)
	}
	// The rendered memory exists and holds the fact.
	data, err := os.ReadFile(filepath.Join(v.RenderDir(), "memory.md"))
	if err != nil || !strings.Contains(string(data), "I use Go.") {
		t.Fatalf("bad render: %v", err)
	}
	// CLAUDE.md holds one import line inside the managed block.
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || block != "@"+filepath.Join(v.RenderDir(), "memory.md") {
		t.Fatalf("bad block %q", block)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
}
```

- [ ] **Step 2: Run the test to verify failure**

Run: `go test ./internal/adapter/ -run TestClaudeCode -v`
Expected: FAIL (compile error: `ClaudeCode` not defined).

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/claudecode.go`:

```go
package adapter

import (
	"os"
	"path/filepath"

	"loadout.dev/loadout/internal/vault"
)

// ClaudeCode projects the vault into Claude Code: skills as symlinks,
// memory as one import line in CLAUDE.md.
type ClaudeCode struct {
	Cfg vault.AdapterConfig
}

func (a ClaudeCode) Name() string { return "claude-code" }

func (a ClaudeCode) memoryImport(v *vault.Vault) string {
	return "@" + filepath.Join(v.RenderDir(), "memory.md")
}

func (a ClaudeCode) Apply(v *vault.Vault) error {
	skills, err := vault.ListSkills(v)
	if err != nil {
		return err
	}
	if _, err := LinkSkills(skills, vault.ExpandPath(a.Cfg.SkillsDir)); err != nil {
		return err
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return err
	}
	renderPath := filepath.Join(v.RenderDir(), "memory.md")
	if err := os.WriteFile(renderPath, []byte(vault.RenderMemory(facts)), 0o644); err != nil {
		return err
	}
	return WriteManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile), a.memoryImport(v))
}

func (a ClaudeCode) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	dir := vault.ExpandPath(a.Cfg.SkillsDir)
	for _, s := range skills {
		cur, err := os.Readlink(filepath.Join(dir, s.Name))
		if err != nil || cur != s.Dir {
			ps = append(ps, Problem{a.Name(), "the skill " + s.Name + " is not linked", "run: loadout sync"})
		}
	}
	got, ok := ReadManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile))
	if !ok || got != a.memoryImport(v) {
		ps = append(ps, Problem{a.Name(), "the memory import block is missing or stale", "run: loadout sync"})
	}
	return ps
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/adapter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the Claude Code adapter"
```

---

### Task 8: pi adapter

**Files:**
- Create: `internal/adapter/pi.go`
- Test: `internal/adapter/pi_test.go`
- Possibly modify: `internal/vault/manifest.go` (pi default paths)

**Interfaces:**
- Consumes: same helpers as Task 7.
- Produces: `type Pi struct { Cfg vault.AdapterConfig }` with `Name() == "pi"`, `Apply`, `Check`. The pi memory block holds the full rendered memory (pi has no import syntax).

- [ ] **Step 1: Verify the real pi paths on this machine**

pi is installed here (version 0.84.x). Run:

```bash
ls ~/.pi 2>/dev/null; ls ~/.pi/agent 2>/dev/null; pi --help 2>&1 | grep -in "skill\|agents" | head -20
```

Confirm two facts: the directory pi reads skills from, and the global instructions file pi reads (`AGENTS.md` or similar). If they differ from `~/.pi/agent/skills` and `~/.pi/AGENTS.md`, update `DefaultManifest` in `internal/vault/manifest.go` and the assertion in `TestManifestRoundTrip`. Record the confirmed paths in the commit message.

- [ ] **Step 2: Write the failing test**

Create `internal/adapter/pi_test.go` (reuses `testVault` from Task 7's test file):

```go
package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestPiApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}

	if err := a.Apply(v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("skill link is missing")
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || !strings.Contains(block, "I use Go.") {
		t.Fatalf("bad block %q", block)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
	// Drift: change a fact; check must now flag the stale block.
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Rust now.\n"), 0o644)
	if ps := a.Check(v); len(ps) == 0 {
		t.Fatal("check must flag a stale memory block")
	}
}
```

- [ ] **Step 3: Run the test to verify failure**

Run: `go test ./internal/adapter/ -run TestPi -v`
Expected: FAIL (compile error: `Pi` not defined).

- [ ] **Step 4: Write the implementation**

Create `internal/adapter/pi.go`:

```go
package adapter

import (
	"os"
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// Pi projects the vault into pi: skills as symlinks, memory as a
// managed block with the full rendered content.
type Pi struct {
	Cfg vault.AdapterConfig
}

func (a Pi) Name() string { return "pi" }

func (a Pi) Apply(v *vault.Vault) error {
	skills, err := vault.ListSkills(v)
	if err != nil {
		return err
	}
	if _, err := LinkSkills(skills, vault.ExpandPath(a.Cfg.SkillsDir)); err != nil {
		return err
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return err
	}
	return WriteManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile), vault.RenderMemory(facts))
}

func (a Pi) Check(v *vault.Vault) []Problem {
	var ps []Problem
	skills, err := vault.ListSkills(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault skills directory"}}
	}
	dir := vault.ExpandPath(a.Cfg.SkillsDir)
	for _, s := range skills {
		cur, err := os.Readlink(filepath.Join(dir, s.Name))
		if err != nil || cur != s.Dir {
			ps = append(ps, Problem{a.Name(), "the skill " + s.Name + " is not linked", "run: loadout sync"})
		}
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		return append(ps, Problem{a.Name(), err.Error(), "repair the vault memory directory"})
	}
	got, ok := ReadManagedBlock(vault.ExpandPath(a.Cfg.MemoryFile))
	if !ok || got != strings.TrimSpace(vault.RenderMemory(facts)) {
		ps = append(ps, Problem{a.Name(), "the memory block is missing or stale", "run: loadout sync"})
	}
	return ps
}
```

- [ ] **Step 5: Run the tests to verify pass**

Run: `go test ./internal/adapter/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the pi adapter"
```
(Name the confirmed pi paths in the commit body.)

---

### Task 9: Generic AGENTS.md adapter + registry

**Files:**
- Create: `internal/adapter/agentsmd.go`
- Modify: `internal/adapter/adapter.go` (add `Enabled`)
- Test: `internal/adapter/agentsmd_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `type AgentsMD struct { Cfg vault.AdapterConfig }` with `Name() == "agents-md"`; it writes a managed block (memory + a skills index with absolute paths) into every path in `Cfg.Targets`. Also `Enabled(v *vault.Vault) []Adapter` — the registry in stable order: claude-code, pi, agents-md.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/agentsmd_test.go` (reuses `testVault`):

```go
package adapter_test

import (
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestAgentsMDApply(t *testing.T) {
	v := testVault(t)
	target := filepath.Join(t.TempDir(), "proj", "AGENTS.md")
	a := adapter.AgentsMD{Cfg: vault.AdapterConfig{Enabled: true, Targets: []string{target}}}

	if err := a.Apply(v); err != nil {
		t.Fatal(err)
	}
	block, ok := adapter.ReadManagedBlock(target)
	if !ok {
		t.Fatal("no block written")
	}
	if !strings.Contains(block, "I use Go.") {
		t.Fatal("memory is missing from the block")
	}
	if !strings.Contains(block, "deploy-checks") || !strings.Contains(block, "SKILL.md") {
		t.Fatal("the skills index is missing from the block")
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
}

func TestEnabledRegistry(t *testing.T) {
	v := testVault(t)
	got := adapter.Enabled(v)
	if len(got) != 2 || got[0].Name() != "claude-code" || got[1].Name() != "pi" {
		names := []string{}
		for _, a := range got {
			names = append(names, a.Name())
		}
		t.Fatalf("bad registry: %v", names)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/adapter/ -v`
Expected: FAIL (compile errors: `AgentsMD`, `Enabled` not defined).

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/agentsmd.go`:

```go
package adapter

import (
	"path/filepath"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// AgentsMD writes memory and a skills index into any AGENTS.md file
// the user lists as a target. It serves tools without a dedicated
// adapter.
type AgentsMD struct {
	Cfg vault.AdapterConfig
}

func (a AgentsMD) Name() string { return "agents-md" }

func renderAgentsMD(v *vault.Vault) (string, error) {
	facts, err := vault.ListFacts(v)
	if err != nil {
		return "", err
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(vault.RenderMemory(facts))
	b.WriteString("\n## Skills (synced by Loadout)\n\n")
	for _, s := range skills {
		b.WriteString("- " + s.Name + ": " + filepath.Join(s.Dir, "SKILL.md") + " — " + s.Description + "\n")
	}
	return b.String(), nil
}

func (a AgentsMD) Apply(v *vault.Vault) error {
	content, err := renderAgentsMD(v)
	if err != nil {
		return err
	}
	for _, target := range a.Cfg.Targets {
		if err := WriteManagedBlock(vault.ExpandPath(target), content); err != nil {
			return err
		}
	}
	return nil
}

func (a AgentsMD) Check(v *vault.Vault) []Problem {
	content, err := renderAgentsMD(v)
	if err != nil {
		return []Problem{{a.Name(), err.Error(), "repair the vault"}}
	}
	var ps []Problem
	for _, target := range a.Cfg.Targets {
		got, ok := ReadManagedBlock(vault.ExpandPath(target))
		if !ok || got != strings.TrimSpace(content) {
			ps = append(ps, Problem{a.Name(), "the block in " + target + " is missing or stale", "run: loadout sync"})
		}
	}
	return ps
}
```

Add to `internal/adapter/adapter.go`:

```go
// Enabled returns the enabled adapters from the manifest, in a
// stable order.
func Enabled(v *vault.Vault) []Adapter {
	var out []Adapter
	for _, name := range []string{"claude-code", "pi", "agents-md"} {
		cfg, ok := v.Manifest.Adapters[name]
		if !ok || !cfg.Enabled {
			continue
		}
		switch name {
		case "claude-code":
			out = append(out, ClaudeCode{Cfg: cfg})
		case "pi":
			out = append(out, Pi{Cfg: cfg})
		case "agents-md":
			out = append(out, AgentsMD{Cfg: cfg})
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/adapter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the generic AGENTS.md adapter and the registry"
```

---

### Task 10: Scaffold templates (AddSkill, AddFact)

**Files:**
- Create: `internal/vault/scaffold.go`
- Test: `internal/vault/scaffold_test.go`

**Interfaces:**
- Consumes: `Vault` (Task 2).
- Produces: `AddSkill(v *Vault, name string) (path string, err error)`, `AddFact(v *Vault, name string) (path string, err error)`. Names must match `^[a-z0-9]+(-[a-z0-9]+)*$`.

- [ ] **Step 1: Write the failing tests**

Create `internal/vault/scaffold_test.go` (reuses `newVault`):

```go
package vault_test

import (
	"os"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestAddSkill(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddSkill(v, "deploy-checks")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "name: deploy-checks") {
		t.Fatalf("bad template: %v", err)
	}
	if _, err := vault.AddSkill(v, "deploy-checks"); err == nil {
		t.Fatal("duplicate must fail")
	}
	if _, err := vault.AddSkill(v, "Bad Name"); err == nil {
		t.Fatal("bad name must fail")
	}
}

func TestAddFact(t *testing.T) {
	v := newVault(t)
	path, err := vault.AddFact(v, "my-stack")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "name: my-stack") {
		t.Fatal("bad template")
	}
	facts, err := vault.ListFacts(v)
	if err != nil || len(facts) != 1 || facts[0].Name != "my-stack" {
		t.Fatalf("the new fact must list: %v %v", facts, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/vault/ -v`
Expected: FAIL (compile errors).

- [ ] **Step 3: Write the implementation**

Create `internal/vault/scaffold.go`:

```go
package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// AddSkill creates a skill folder with a SKILL.md template.
func AddSkill(v *Vault, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: deploy-checks")
	}
	dir := filepath.Join(v.SkillsDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("the skill %s already exists", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: <one line: when an agent must use this skill>\n---\n\n# " + name + "\n\n<write the instructions here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}

// AddFact creates a memory fact file with a template.
func AddFact(v *Vault, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("use a kebab-case name, for example: my-stack")
	}
	path := filepath.Join(v.MemoryDir(), name+".md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("the fact %s already exists", name)
	}
	content := "---\nname: " + name + "\ndescription: <one line summary>\ntype: user\n---\n\n<write the fact here>\n"
	return path, os.WriteFile(path, []byte(content), 0o644)
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./internal/vault/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the skill and fact templates"
```

---

### Task 11: CLI dispatch, init, and add

**Files:**
- Create: `cmd/loadout/main.go`, `internal/cli/run.go`, `internal/cli/init.go`, `internal/cli/add.go`
- Test: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `vault.DefaultRoot`, `vault.Init`, `vault.Open`, `vault.AddSkill`, `vault.AddFact`, `vault.Snapshot`.
- Produces: `cli.Run(out, errOut io.Writer, args []string) int`. Exit codes: 0 ok, 1 failure, 2 usage error. Later tasks add cases to the same switch.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/run_test.go`:

```go
package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/cli"
)

// run points the vault and the home at temp dirs, then runs the CLI.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(&out, &errOut, args)
	return out.String(), errOut.String(), code
}

func setupEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("LOADOUT_HOME", filepath.Join(base, "vault"))
	os.MkdirAll(filepath.Join(base, "home"), 0o755)
	return base
}

func TestUsage(t *testing.T) {
	setupEnv(t)
	_, errOut, code := run(t)
	if code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	_, errOut, code = run(t, "bogus")
	if code != 2 || !strings.Contains(errOut, "bogus") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestInitAndAdd(t *testing.T) {
	base := setupEnv(t)
	out, errOut, code := run(t, "init")
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if !strings.Contains(out, filepath.Join(base, "vault")) {
		t.Fatalf("init must print the vault path, got %q", out)
	}
	if _, _, code := run(t, "add", "skill", "deploy-checks"); code != 0 {
		t.Fatal("add skill failed")
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "skills", "deploy-checks", "SKILL.md")); err != nil {
		t.Fatal("the skill file is missing")
	}
	if _, _, code := run(t, "add", "memory", "my-stack"); code != 0 {
		t.Fatal("add memory failed")
	}
	if _, errOut, code := run(t, "add", "wat", "x"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("bad kind must be a usage error, got %d %q", code, errOut)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/cli/ -v`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Write the implementation**

Create `internal/cli/run.go`:

```go
// Package cli implements the loadout commands.
package cli

import (
	"fmt"
	"io"
)

const usage = `usage: loadout <command>

commands:
  init            create the vault
  add skill NAME  add a skill
  add memory NAME add a memory fact
  sync            project the vault into every enabled tool
  status          show the vault and the adapter state
  doctor          find problems and show the fix for each one
`

func Run(out, errOut io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	switch args[0] {
	case "init":
		return cmdInit(out, errOut)
	case "add":
		return cmdAdd(out, errOut, args[1:])
	default:
		fmt.Fprintf(errOut, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}
```

Create `internal/cli/init.go`:

```go
package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

func cmdInit(out, errOut io.Writer) int {
	v, err := vault.Init(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created the vault at %s\n", v.Root)
	fmt.Fprintln(out, "next: loadout add skill <name>, then loadout sync")
	return 0
}
```

Create `internal/cli/add.go`:

```go
package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/vault"
)

func cmdAdd(out, errOut io.Writer, args []string) int {
	if len(args) != 2 || (args[0] != "skill" && args[0] != "memory") {
		fmt.Fprintln(errOut, "usage: loadout add skill|memory <name>")
		return 2
	}
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	kind, name := args[0], args[1]
	var path string
	if kind == "skill" {
		path, err = vault.AddSkill(v, name)
	} else {
		path, err = vault.AddFact(v, name)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := vault.Snapshot(v, "add "+kind+" "+name); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "created %s\n", path)
	return 0
}
```

Create `cmd/loadout/main.go`:

```go
package main

import (
	"os"

	"loadout.dev/loadout/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Stdout, os.Stderr, os.Args[1:]))
}
```

- [ ] **Step 4: Run the tests and the build**

Run: `go test ./internal/cli/ -v && go build ./...`
Expected: PASS, clean build.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the CLI with init and add"
```

---

### Task 12: sync command

**Files:**
- Create: `internal/cli/sync.go`
- Modify: `internal/cli/run.go` (add the `sync` case)
- Test: add to `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `adapter.Enabled`, `vault.Snapshot`.
- Produces: `loadout sync` applies every enabled adapter, prints one line per adapter, snapshots history, and exits 0. On an adapter error it prints the error and exits 1.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/run_test.go`:

```go
func TestSyncProjectsVault(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync failed: %s", errOut)
	}
	if !strings.Contains(out, "claude-code") || !strings.Contains(out, "pi") {
		t.Fatalf("sync must name each adapter, got %q", out)
	}
	home := filepath.Join(base, "home")
	if _, err := os.Readlink(filepath.Join(home, ".claude", "skills", "deploy-checks")); err != nil {
		t.Fatal("the Claude Code skill link is missing")
	}
	if _, err := os.Readlink(filepath.Join(home, ".pi", "agent", "skills", "deploy-checks")); err != nil {
		t.Fatal("the pi skill link is missing")
	}
	for _, f := range []string{filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".pi", "AGENTS.md")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
}
```

Note: the default manifest paths start with `~`, and `HOME` points at the temp dir, so `ExpandPath` keeps the test inside the sandbox. If Task 8 changed the pi default paths, update the assertions here to match.

- [ ] **Step 2: Run the test to verify failure**

Run: `go test ./internal/cli/ -run TestSync -v`
Expected: FAIL ("unknown command" — exit code 2).

- [ ] **Step 3: Write the implementation**

Create `internal/cli/sync.go`:

```go
package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdSync(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, a := range adapter.Enabled(v) {
		if err := a.Apply(v); err != nil {
			fmt.Fprintf(errOut, "%s: %v\n", a.Name(), err)
			return 1
		}
		fmt.Fprintf(out, "synced %s\n", a.Name())
	}
	if err := vault.Snapshot(v, "sync"); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
```

Add to the switch in `internal/cli/run.go`:

```go
	case "sync":
		return cmdSync(out, errOut)
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `go test ./... -v`
Expected: PASS (all packages).

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the sync command"
```

---

### Task 13: status and doctor commands

**Files:**
- Create: `internal/cli/status.go`, `internal/cli/doctor.go`
- Modify: `internal/cli/run.go` (add both cases)
- Test: add to `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `vault.ListSkills`, `vault.ListFacts`, `vault.InvalidSkillDirs`, `adapter.Enabled`, `Adapter.Check`.
- Produces: `loadout status` (always exit 0) and `loadout doctor` (exit 0 when clean, 1 with problems).

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/run_test.go`:

```go
func TestStatusAndDoctor(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	// Before sync: doctor must report problems and exit 1.
	out, _, code := run(t, "doctor")
	if code != 1 || !strings.Contains(out, "loadout sync") {
		t.Fatalf("doctor before sync: code=%d out=%q", code, out)
	}

	run(t, "sync")

	out, _, code = run(t, "status")
	if code != 0 {
		t.Fatal("status failed")
	}
	if !strings.Contains(out, "skills: 1") || !strings.Contains(out, "memory facts: 1") {
		t.Fatalf("bad status: %q", out)
	}
	if !strings.Contains(out, "claude-code: in sync") || !strings.Contains(out, "pi: in sync") {
		t.Fatalf("bad adapter status: %q", out)
	}

	out, _, code = run(t, "doctor")
	if code != 0 || !strings.Contains(out, "all good") {
		t.Fatalf("doctor after sync: code=%d out=%q", code, out)
	}
}
```

- [ ] **Step 2: Run the tests to verify failure**

Run: `go test ./internal/cli/ -run TestStatusAndDoctor -v`
Expected: FAIL ("unknown command").

- [ ] **Step 3: Write the implementation**

Create `internal/cli/status.go`:

```go
package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdStatus(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "vault: %s\nskills: %d\nmemory facts: %d\n", v.Root, len(skills), len(facts))
	for _, a := range adapter.Enabled(v) {
		if ps := a.Check(v); len(ps) == 0 {
			fmt.Fprintf(out, "%s: in sync\n", a.Name())
		} else {
			fmt.Fprintf(out, "%s: %d problems (run: loadout doctor)\n", a.Name(), len(ps))
		}
	}
	return 0
}
```

Create `internal/cli/doctor.go`:

```go
package cli

import (
	"fmt"
	"io"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func cmdDoctor(out, errOut io.Writer) int {
	v, err := vault.Open(vault.DefaultRoot())
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	count := 0
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, d := range bad {
		count++
		fmt.Fprintf(out, "vault: the skill directory %s has no SKILL.md file\n  fix: add a SKILL.md file, or remove the directory\n", d)
	}
	for _, a := range adapter.Enabled(v) {
		for _, p := range a.Check(v) {
			count++
			fmt.Fprintf(out, "%s: %s\n  fix: %s\n", p.Adapter, p.Detail, p.Fix)
		}
	}
	if count == 0 {
		fmt.Fprintln(out, "all good")
		return 0
	}
	fmt.Fprintf(out, "%d problems\n", count)
	return 1
}
```

Add to the switch in `internal/cli/run.go`:

```go
	case "status":
		return cmdStatus(out, errOut)
	case "doctor":
		return cmdDoctor(out, errOut)
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS, no vet findings.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && git add -A && git commit -m "Add the status and doctor commands"
```

---

### Task 14: README + dogfood verification

**Files:**
- Create: `README.md`
- No code changes.

**Interfaces:**
- Consumes: the finished CLI.
- Produces: a quickstart README and a verified install on this machine.

- [ ] **Step 1: Write the README**

Create `README.md`:

```markdown
# Loadout

One secure home for your agent gear. Store your skills and your memory
in one vault. Sync them to every agent tool.

Phase 1 is local only. Cloud sync, secrets, and more adapters come next.
See PLAN.md for the roadmap.

## Install

    go build -o loadout ./cmd/loadout
    mv loadout /usr/local/bin/

## Quickstart

    loadout init                    # create the vault at ~/.loadout
    loadout add skill deploy-checks # scaffold a skill
    loadout add memory my-stack     # scaffold a memory fact
    loadout sync                    # project into Claude Code, pi, ...
    loadout status                  # see what is where
    loadout doctor                  # find problems, with the fix for each

Edit the files in ~/.loadout with any editor. Skills reach the tools
through symlinks, so a skill edit is live at once. After a memory edit,
run "loadout sync" again.

## How it stays safe

- Loadout writes only inside marked blocks in shared files.
- Loadout never replaces a real file or directory with a symlink.
- Every change lands in a local git history inside the vault. Undo with
  git if you need to.
```

- [ ] **Step 2: Run a sandboxed smoke test**

```bash
cd /Users/waleed/loadout && go build -o /tmp/loadout ./cmd/loadout
export LOADOUT_HOME=/tmp/lo-smoke
HOME=/tmp/lo-home /tmp/loadout init
HOME=/tmp/lo-home /tmp/loadout add skill test-skill
HOME=/tmp/lo-home /tmp/loadout add memory test-fact
HOME=/tmp/lo-home /tmp/loadout sync
HOME=/tmp/lo-home /tmp/loadout doctor
```

Expected: `doctor` prints "all good" and exits 0. Then clean up: `rm -rf /tmp/lo-smoke /tmp/lo-home /tmp/loadout; unset LOADOUT_HOME`.

- [ ] **Step 3: Dogfood on the real machine — ASK FIRST**

STOP. Ask the human partner before this step. It writes a managed block
into the real `~/.claude/CLAUDE.md` and links into the real skill
directories. On a yes: run `loadout init`, migrate one real memory fact
and one real skill into the vault, run `loadout sync`, then open a new
Claude Code session and a new pi session and confirm both see the skill
and the memory.

- [ ] **Step 4: Commit**

```bash
gofmt -l . && git add README.md && git commit -m "Add the README"
```

---

## Self-Review Notes

- Spec coverage: Phase 1 = CLI + vault format + adapter kit + three adapters (Claude Code, pi, generic `AGENTS.md`) — Tasks 1–13; "content-addressed history for undo" — git-backed `Snapshot` (Task 2); dogfood success test — Task 14. The `edit` verb and cloud sync are later phases per spec sections 6 and 8.
- Type consistency: `AdapterConfig` fields (Tasks 1, 7, 8, 9), `Problem{Adapter, Detail, Fix}` (Tasks 6, 7, 8, 9, 13), `cli.Run(out, errOut, args)` (Tasks 11–13) verified consistent.
- Known risk: the pi default paths are a best guess; Task 8 Step 1 verifies them on the real install before the adapter is written.
