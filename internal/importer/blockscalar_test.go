package importer

import "testing"

// TestParseFrontmatterFoldedBlockScalar is the regression test for a
// real breakage found on a live machine: four imported skills (caveman,
// here-now, humanizer, opik-evaluate) wrote frontmatter reading
// "description: >" and nothing else, because the parser took the bare
// block-scalar marker as the value and dropped the text under it. Codex
// then refused to load them: "missing field `description`".
func TestParseFrontmatterFoldedBlockScalar(t *testing.T) {
	raw := []byte("---\n" +
		"name: caveman\n" +
		"description: >\n" +
		"  Ultra-compressed communication mode. Cuts token usage ~75% by dropping\n" +
		"  filler, articles, and pleasantries while keeping full technical accuracy.\n" +
		"---\n\nBody here.\n")

	fields, body := parseFrontmatter(raw)
	want := "Ultra-compressed communication mode. Cuts token usage ~75% by dropping " +
		"filler, articles, and pleasantries while keeping full technical accuracy."
	if fields["description"] != want {
		t.Fatalf("folded scalar not gathered:\n got %q\nwant %q", fields["description"], want)
	}
	if fields["name"] != "caveman" {
		t.Fatalf("name lost: %q", fields["name"])
	}
	if body != "Body here." {
		t.Fatalf("body changed: %q", body)
	}
}

// TestParseFrontmatterLiteralBlockScalar covers the "|" form, plus a
// chomping indicator, which humanizer used.
func TestParseFrontmatterLiteralBlockScalar(t *testing.T) {
	raw := []byte("---\n" +
		"name: humanizer\n" +
		"description: |-\n" +
		"  Remove signs of AI-generated writing from text.\n" +
		"  Use when editing or reviewing text.\n" +
		"---\n\nBody.\n")

	fields, _ := parseFrontmatter(raw)
	want := "Remove signs of AI-generated writing from text. Use when editing or reviewing text."
	if fields["description"] != want {
		t.Fatalf("literal scalar not gathered:\n got %q\nwant %q", fields["description"], want)
	}
}

// TestParseFrontmatterIgnoresNestedKeys proves an indented line inside a
// nested block is not mistaken for a top-level key. sentry-cli carried
// a "requires:" block holding "bins:" and "auth:"; reading those as
// top-level keys would be wrong.
func TestParseFrontmatterIgnoresNestedKeys(t *testing.T) {
	raw := []byte("---\n" +
		"name: sentry-cli\n" +
		"description: Use the sentry CLI.\n" +
		"version: 0.37.0\n" +
		"requires:\n" +
		"  bins: [\"sentry\"]\n" +
		"  auth: true\n" +
		"---\n\nBody.\n")

	fields, _ := parseFrontmatter(raw)
	if fields["description"] != "Use the sentry CLI." {
		t.Fatalf("description wrong: %q", fields["description"])
	}
	if fields["version"] != "0.37.0" {
		t.Fatalf("a flat key beside a nested block must still parse, got %q", fields["version"])
	}
	if _, ok := fields["bins"]; ok {
		t.Fatal("an indented key inside requires: must not become a top-level field")
	}
	if _, ok := fields["auth"]; ok {
		t.Fatal("an indented key inside requires: must not become a top-level field")
	}
}

// TestParseSkillFrontmatterKeepsBlockScalarDescription checks the whole
// skill path, which is what an import actually calls.
func TestParseSkillFrontmatterKeepsBlockScalarDescription(t *testing.T) {
	raw := []byte("---\nname: a\ndescription: >\n  One line.\n  Two line.\n---\n\nBody.\n")
	name, description, body, ok := parseSkillFrontmatter(raw)
	if !ok {
		t.Fatal("want the skill to parse")
	}
	if name != "a" {
		t.Fatalf("name=%q", name)
	}
	if description != "One line. Two line." {
		t.Fatalf("description=%q", description)
	}
	if body != "Body." {
		t.Fatalf("body=%q", body)
	}
}
