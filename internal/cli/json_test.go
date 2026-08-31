package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpExitsZeroAndShowsEveryVerb(t *testing.T) {
	setupEnv(t)
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		out, errOut, code := run(t, args...)
		if code != 0 {
			t.Fatalf("%v: want exit 0, got %d (err %q)", args, code, errOut)
		}
		for _, verb := range []string{"init", "add", "show", "list", "edit", "recall", "context", "sync", "status", "doctor", "log", "undo", "review"} {
			if !strings.Contains(out, verb) {
				t.Fatalf("%v: usage must mention %q, got %q", args, verb, out)
			}
		}
	}
}

func TestStatusJSONHoldsRightCounts(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	run(t, "sync")

	out, errOut, code := run(t, "status", "--json")
	if code != 0 {
		t.Fatalf("status --json failed: %s", errOut)
	}
	var got struct {
		Vault    string `json:"vault"`
		Skills   int    `json:"skills"`
		Facts    int    `json:"facts"`
		Adapters []struct {
			Name     string `json:"name"`
			Problems int    `json:"problems"`
		} `json:"adapters"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("status --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Skills != 1 || got.Facts != 1 {
		t.Fatalf("bad counts: %+v", got)
	}
	if got.Vault == "" {
		t.Fatalf("vault must be set: %+v", got)
	}
	if len(got.Adapters) == 0 {
		t.Fatalf("adapters must be listed: %+v", got)
	}
	for _, a := range got.Adapters {
		if a.Problems != 0 {
			t.Fatalf("a synced adapter must report zero problems, got %+v", a)
		}
	}
}

// --json in a different position must work the same way.
func TestStatusJSONFlagAnyPosition(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "--json", "status")
	if code != 0 {
		t.Fatalf("--json status failed: %s", errOut)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json status did not parse: %v\noutput: %s", err, out)
	}
}

func TestDoctorJSONOnBrokenVault(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	if err := os.RemoveAll(filepath.Join(base, "vault", ".git")); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "doctor", "--json")
	if code != 1 {
		t.Fatalf("doctor --json on a broken vault must exit 1, got %d (%s)", code, errOut)
	}
	var got struct {
		Problems []struct {
			Source string `json:"source"`
			Detail string `json:"detail"`
			Fix    string `json:"fix"`
		} `json:"problems"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("doctor --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Count < 1 {
		t.Fatalf("count must be >= 1, got %+v", got)
	}
	if len(got.Problems) != got.Count {
		t.Fatalf("count must match the number of problems, got %+v", got)
	}
	for _, p := range got.Problems {
		if p.Fix == "" {
			t.Fatalf("every problem must carry a fix string, got %+v", p)
		}
	}
}

func TestDoctorJSONAllGood(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "sync")

	out, errOut, code := run(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json on a clean vault must exit 0, got %d (%s)", code, errOut)
	}
	var got struct {
		Problems []any `json:"problems"`
		Count    int   `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("doctor --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Count != 0 || len(got.Problems) != 0 {
		t.Fatalf("a clean vault must report no problems, got %+v", got)
	}
}

func TestListJSONItemsCarryAddresses(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "list", "--json")
	if code != 0 {
		t.Fatalf("list --json failed: %s", errOut)
	}
	var got struct {
		Items []struct {
			Address string `json:"address"`
			Hook    string `json:"hook"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Items) != 2 {
		t.Fatalf("want 2 items, got %+v", got)
	}
	wantAddrs := map[string]bool{"skill/deploy-checks": true, "memory/my-stack": true}
	for _, it := range got.Items {
		if !wantAddrs[it.Address] {
			t.Fatalf("unexpected address %q in %+v", it.Address, got)
		}
	}
}

func TestRecallJSONItemsCarryAddresses(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	out, errOut, code := run(t, "recall", "deploy", "--json")
	if code != 0 {
		t.Fatalf("recall --json failed: %s", errOut)
	}
	var got struct {
		Items []struct {
			Address string `json:"address"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("recall --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Items) != 1 || got.Items[0].Address != "skill/deploy-checks" {
		t.Fatalf("bad items: %+v", got)
	}
}

func TestRecallJSONNoMatchIsEmptyList(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "recall", "--json", "bogusterm")
	if code != 0 {
		t.Fatalf("recall --json failed: %s", errOut)
	}
	var got struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("recall --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("no matches must be an empty list, got %+v (raw %s)", got, out)
	}
}

func TestInitJSON(t *testing.T) {
	base := setupEnv(t)
	out, errOut, code := run(t, "init", "--json")
	if code != 0 {
		t.Fatalf("init --json failed: %s", errOut)
	}
	var got struct {
		Vault string `json:"vault"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("init --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Vault != filepath.Join(base, "vault") {
		t.Fatalf("bad vault path: %+v", got)
	}
}

func TestAddJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")

	out, errOut, code := run(t, "add", "memory", "my-stack", "--json")
	if code != 0 {
		t.Fatalf("add --json failed: %s", errOut)
	}
	var got struct {
		Address string `json:"address"`
		Path    string `json:"path"`
		Review  string `json:"review"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("add --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Address != "memory/my-stack" || got.Review != "kept" || got.Path == "" {
		t.Fatalf("bad add result: %+v", got)
	}

	out, errOut, code = run(t, "add", "memory", "draft-fact", "--by", "pi", "--json")
	if code != 0 {
		t.Fatalf("add --json failed: %s", errOut)
	}
	got = struct {
		Address string `json:"address"`
		Path    string `json:"path"`
		Review  string `json:"review"`
	}{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("add --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Review != "draft" {
		t.Fatalf("a non-human add must report draft, got %+v", got)
	}
}

func TestSyncJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")

	out, errOut, code := run(t, "sync", "--json")
	if code != 0 {
		t.Fatalf("sync --json failed: %s", errOut)
	}
	var got struct {
		Reports []struct {
			Adapter string   `json:"adapter"`
			Applied []string `json:"applied,omitempty"`
		} `json:"reports"`
		Snapshot bool `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("sync --json did not parse: %v\noutput: %s", err, out)
	}
	if !got.Snapshot {
		t.Fatalf("snapshot must be true, got %+v", got)
	}
	if len(got.Reports) == 0 {
		t.Fatalf("reports must be non-empty, got %+v", got)
	}
}

func TestSyncDryRunJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	run(t, "sync")

	out, errOut, code := run(t, "sync", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("sync --dry-run --json failed: %s", errOut)
	}
	var got struct {
		Reports []struct {
			Adapter string   `json:"adapter"`
			DryRun  bool     `json:"dry_run"`
			Applied []string `json:"applied"`
		} `json:"reports"`
		Snapshot bool `json:"snapshot"`
		DryRun   bool `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("sync --dry-run --json did not parse: %v\noutput: %s", err, out)
	}
	if !got.DryRun {
		t.Fatalf("dry_run must be true, got %+v", got)
	}
	if got.Snapshot {
		t.Fatalf("snapshot must be false on a dry run, got %+v", got)
	}
	if len(got.Reports) == 0 {
		t.Fatalf("reports must be non-empty, got %+v", got)
	}
	for _, r := range got.Reports {
		if !r.DryRun {
			t.Fatalf("each report must carry dry_run true, got %+v", r)
		}
		found := false
		for _, a := range r.Applied {
			if a == "memory: up to date" {
				found = true
			}
		}
		if !found {
			t.Fatalf("a dry run after a real sync must report memory up to date, got %+v", r)
		}
	}
}

func TestShowJSON(t *testing.T) {
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "my-stack")
	path := filepath.Join(base, "vault", "memory", "my-stack.md")
	content := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go and Postgres.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run(t, "show", "memory/my-stack", "--json")
	if code != 0 {
		t.Fatalf("show --json failed: %s", errOut)
	}
	var got struct {
		Address string `json:"address"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Address != "memory/my-stack" || got.Content != content {
		t.Fatalf("bad show result: %+v", got)
	}
}

func TestContextJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")

	out, errOut, code := run(t, "context", "--json")
	if code != 0 {
		t.Fatalf("context --json failed: %s", errOut)
	}
	var got struct {
		Vault  string `json:"vault"`
		Skills int    `json:"skills"`
		Facts  int    `json:"facts"`
		Memory []struct {
			Name string `json:"name"`
			Hook string `json:"hook"`
		} `json:"memory"`
		SkillsList []struct {
			Name string `json:"name"`
		} `json:"skills_list"`
		Recent []string `json:"recent"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("context --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Skills != 1 || got.Facts != 1 {
		t.Fatalf("bad counts: %+v", got)
	}
	if len(got.Memory) != 1 || got.Memory[0].Name != "my-stack" {
		t.Fatalf("bad memory: %+v", got)
	}
	if len(got.SkillsList) != 1 || got.SkillsList[0].Name != "deploy-checks" {
		t.Fatalf("bad skills_list: %+v", got)
	}
	if len(got.Recent) == 0 {
		t.Fatalf("recent must not be empty: %+v", got)
	}
}

func TestLogJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "fact1")

	out, errOut, code := run(t, "log", "--json")
	if code != 0 {
		t.Fatalf("log --json failed: %s", errOut)
	}
	var got struct {
		Entries []struct {
			At      string `json:"at"`
			Subject string `json:"subject"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("log --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("want 2 entries, got %+v", got)
	}
	if got.Entries[0].Subject != "add memory fact1" {
		t.Fatalf("newest entry must come first, got %+v", got)
	}
}

func TestUndoJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "fact1")

	out, errOut, code := run(t, "undo", "--json")
	if code != 0 {
		t.Fatalf("undo --json failed: %s", errOut)
	}
	var got struct {
		Restored bool `json:"restored"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("undo --json did not parse: %v\noutput: %s", err, out)
	}
	if !got.Restored {
		t.Fatalf("restored must be true, got %+v", got)
	}
}

func TestReviewJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")

	out, errOut, code := run(t, "review", "--json")
	if code != 0 {
		t.Fatalf("review --json failed: %s", errOut)
	}
	var got struct {
		Drafts []struct {
			Address string `json:"address"`
			Hook    string `json:"hook"`
			By      string `json:"by"`
			At      string `json:"at"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("review --json did not parse: %v\noutput: %s", err, out)
	}
	if len(got.Drafts) != 1 || got.Drafts[0].Address != "memory/x" || got.Drafts[0].By != "pi" {
		t.Fatalf("bad drafts: %+v", got)
	}
}

func TestReviewNoDraftsJSONIsEmptyList(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x")

	out, errOut, code := run(t, "review", "--json")
	if code != 0 {
		t.Fatalf("review --json failed: %s", errOut)
	}
	var got struct {
		Drafts []any `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("review --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Drafts == nil || len(got.Drafts) != 0 {
		t.Fatalf("no drafts must be an empty list, got %+v", got)
	}
}

func TestReviewKeepJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")

	out, errOut, code := run(t, "review", "keep", "memory/x", "--json")
	if code != 0 {
		t.Fatalf("review keep --json failed: %s", errOut)
	}
	var got struct {
		Address string `json:"address"`
		Review  string `json:"review"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("review keep --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Address != "memory/x" || got.Review != "kept" {
		t.Fatalf("bad review keep result: %+v", got)
	}
}

func TestReviewDropJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x", "--by", "pi")

	out, errOut, code := run(t, "review", "drop", "memory/x", "--json")
	if code != 0 {
		t.Fatalf("review drop --json failed: %s", errOut)
	}
	var got struct {
		Address string `json:"address"`
		Dropped bool   `json:"dropped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("review drop --json did not parse: %v\noutput: %s", err, out)
	}
	if got.Address != "memory/x" || !got.Dropped {
		t.Fatalf("bad review drop result: %+v", got)
	}
}

func TestEditJSONIsAPlainError(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "x")

	out, errOut, code := run(t, "edit", "memory/x", "--json")
	if code != 2 {
		t.Fatalf("edit --json must exit 2, got %d", code)
	}
	if out != "" {
		t.Fatalf("edit --json must print nothing to stdout, got %q", out)
	}
	want := "edit has no json output. Fix: run edit without --json.\n"
	if errOut != want {
		t.Fatalf("bad error: got %q want %q", errOut, want)
	}
}

// The text output of every verb must stay byte-identical to before
// when --json is absent. status and doctor are spot-checked here
// against the same assertions run_test.go already makes; the rest of
// run_test.go, unchanged, covers the remaining verbs.
func TestTextOutputUnchangedWithoutJSON(t *testing.T) {
	setupEnv(t)
	run(t, "init")
	run(t, "add", "skill", "deploy-checks")
	run(t, "add", "memory", "my-stack")
	run(t, "sync")

	out, _, code := run(t, "status")
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
