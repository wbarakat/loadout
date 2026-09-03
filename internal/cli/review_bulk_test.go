package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedDrafts creates three drafts from two different sources, plus one
// already-kept item that must never be touched by a bulk action.
func seedDrafts(t *testing.T) string {
	t.Helper()
	base := setupEnv(t)
	run(t, "init")
	run(t, "add", "memory", "a", "--by", "import:codex")
	run(t, "add", "memory", "b", "--by", "import:codex")
	run(t, "add", "skill", "c", "--by", "import:claude-code")
	run(t, "add", "skill", "kept-one") // human-authored, already kept
	return base
}

func TestReviewKeepAllKeepsEveryDraft(t *testing.T) {
	seedDrafts(t)

	out, errOut, code := run(t, "review", "keep", "--all")
	if code != 0 {
		t.Fatalf("review keep --all failed: %s", errOut)
	}
	for _, want := range []string{"memory/a", "memory/b", "skill/c"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q named in the output, got %q", want, out)
		}
	}
	if !strings.Contains(out, "loadout undo") {
		t.Fatalf("a bulk action must point at undo, got %q", out)
	}
	listOut, _, _ := run(t, "review")
	if !strings.Contains(listOut, "no drafts") {
		t.Fatalf("every draft should be kept, got %q", listOut)
	}
}

func TestReviewKeepMultipleAddresses(t *testing.T) {
	seedDrafts(t)

	if _, errOut, code := run(t, "review", "keep", "memory/a", "skill/c"); code != 0 {
		t.Fatalf("multi-address keep failed: %s", errOut)
	}
	listOut, _, _ := run(t, "review")
	if strings.Contains(listOut, "memory/a") || strings.Contains(listOut, "skill/c") {
		t.Fatalf("kept items must leave the draft list, got %q", listOut)
	}
	if !strings.Contains(listOut, "memory/b") {
		t.Fatalf("an unnamed draft must stay a draft, got %q", listOut)
	}
}

// TestReviewBulkFilterBySource is the case that makes a 57-draft import
// manageable: act on one tool's batch and leave the rest alone.
func TestReviewBulkFilterBySource(t *testing.T) {
	seedDrafts(t)

	if _, errOut, code := run(t, "review", "drop", "--all", "--by", "import:codex"); code != 0 {
		t.Fatalf("filtered drop failed: %s", errOut)
	}
	listOut, _, _ := run(t, "review")
	if strings.Contains(listOut, "memory/a") || strings.Contains(listOut, "memory/b") {
		t.Fatalf("codex drafts should be gone, got %q", listOut)
	}
	if !strings.Contains(listOut, "skill/c") {
		t.Fatalf("the claude-code draft must survive a codex-only drop, got %q", listOut)
	}
}

func TestReviewBulkFilterByKind(t *testing.T) {
	seedDrafts(t)

	if _, errOut, code := run(t, "review", "keep", "--all", "--kind", "memory"); code != 0 {
		t.Fatalf("kind-filtered keep failed: %s", errOut)
	}
	listOut, _, _ := run(t, "review")
	if strings.Contains(listOut, "memory/") {
		t.Fatalf("memory drafts should be kept, got %q", listOut)
	}
	if !strings.Contains(listOut, "skill/c") {
		t.Fatalf("skill drafts must be untouched, got %q", listOut)
	}
}

// TestReviewBulkDryRunChangesNothing proves --dry-run reports the exact
// set it would act on and leaves every file in place.
func TestReviewBulkDryRunChangesNothing(t *testing.T) {
	seedDrafts(t)

	out, errOut, code := run(t, "review", "drop", "--all", "--dry-run")
	if code != 0 {
		t.Fatalf("dry run failed: %s", errOut)
	}
	if !strings.Contains(out, "nothing changed") {
		t.Fatalf("a dry run must say nothing changed, got %q", out)
	}
	listOut, _, _ := run(t, "review")
	for _, want := range []string{"memory/a", "memory/b", "skill/c"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("a dry run must delete nothing, %q is gone from %q", want, listOut)
		}
	}
}

// TestReviewBulkNeverTouchesKeptItems is the safety gate: a bulk drop
// acts on drafts only, so a human-authored item is never deleted.
func TestReviewBulkNeverTouchesKeptItems(t *testing.T) {
	base := seedDrafts(t)

	if _, errOut, code := run(t, "review", "drop", "--all"); code != 0 {
		t.Fatalf("drop --all failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(base, "vault", "skills", "kept-one")); err != nil {
		t.Fatalf("a kept, human-authored skill must survive drop --all: %v", err)
	}
}

// TestReviewBulkIsOneUndoableStep proves the whole batch is a single
// history entry, so one undo brings every item back.
func TestReviewBulkIsOneUndoableStep(t *testing.T) {
	seedDrafts(t)

	if _, errOut, code := run(t, "review", "drop", "--all"); code != 0 {
		t.Fatalf("drop --all failed: %s", errOut)
	}
	if listOut, _, _ := run(t, "review"); !strings.Contains(listOut, "no drafts") {
		t.Fatalf("drafts should be gone, got %q", listOut)
	}
	if _, errOut, code := run(t, "undo"); code != 0 {
		t.Fatalf("undo failed: %s", errOut)
	}
	listOut, _, _ := run(t, "review")
	for _, want := range []string{"memory/a", "memory/b", "skill/c"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("one undo must restore the whole batch, %q missing from %q", want, listOut)
		}
	}
}

func TestReviewBulkJSON(t *testing.T) {
	seedDrafts(t)

	out, errOut, code := run(t, "review", "keep", "--all", "--json")
	if code != 0 {
		t.Fatalf("bulk keep --json failed: %s", errOut)
	}
	var got struct {
		Action    string   `json:"action"`
		Count     int      `json:"count"`
		Addresses []string `json:"addresses"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bulk JSON did not parse: %v\n%s", err, out)
	}
	if got.Action != "keep" || got.Count != 3 || len(got.Addresses) != 3 {
		t.Fatalf("bad bulk result: %+v", got)
	}
}

func TestReviewBulkUsageErrors(t *testing.T) {
	seedDrafts(t)

	cases := [][]string{
		{"review", "keep"},                             // neither address nor --all
		{"review", "keep", "--all", "memory/a"},        // both
		{"review", "keep", "memory/a", "--by", "x"},    // filter without --all
		{"review", "keep", "--all", "--kind", "bogus"}, // bad kind
		{"review", "keep", "--all", "--by"},            // flag with no value
	}
	for _, args := range cases {
		_, errOut, code := run(t, args...)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("args %v must be a usage error, got code=%d err=%q", args, code, errOut)
		}
	}
}

// TestReviewBulkNoMatchIsNotAnError proves a filter matching nothing
// reports plainly and succeeds, so a script or agent can run it safely.
func TestReviewBulkNoMatchIsNotAnError(t *testing.T) {
	seedDrafts(t)

	out, errOut, code := run(t, "review", "keep", "--all", "--by", "import:nobody")
	if code != 0 {
		t.Fatalf("an empty match must not fail: %s", errOut)
	}
	if !strings.Contains(out, "nothing matched") {
		t.Fatalf("want a plain no-match message, got %q", out)
	}
}
