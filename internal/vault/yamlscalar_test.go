package vault

import "testing"

// TestYamlScalarQuotesAmbiguousValues proves a description that reads
// naturally in prose cannot produce frontmatter a tool refuses to parse.
// The colon-space case is real: an imported humanizer description read
// "...patterns including: inflated symbolism...", which is ambiguous as
// a plain YAML scalar.
func TestYamlScalarQuotesAmbiguousValues(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain prose passes through", "Remove AI writing patterns.", "Remove AI writing patterns."},
		{"colon space is quoted", "Fixes patterns including: inflated symbolism",
			`"Fixes patterns including: inflated symbolism"`},
		{"empty becomes an explicit empty string", "", `""`},
		{"a block-scalar marker is quoted", ">", `">"`},
		{"a trailing colon is quoted", "See also:", `"See also:"`},
		{"an embedded quote is escaped", `He said "hi": ok`, `"He said \"hi\": ok"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := yamlScalar(c.in); got != c.want {
				t.Fatalf("yamlScalar(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
