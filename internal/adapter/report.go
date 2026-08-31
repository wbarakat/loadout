package adapter

// Report holds what one adapter did on a sync. Applied and Pruned
// name the actions taken (or, under a dry run, the actions that
// would run). Blocked names each skill a real file or a foreign
// link stops Loadout from linking; a blocked skill is not an error.
// Applied, Pruned, and Blocked are always present, never null: every
// Apply call starts them as empty slices, so a caller can range over
// them without a nil check. Linked counts the skills Applied names
// as linked (or, on a dry run, as needing a link); it excludes the
// memory entries Applied also carries. Error holds the message from
// a non-nil error Apply returned; it is empty on success.
type Report struct {
	Adapter string   `json:"adapter"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Linked  int      `json:"linked"`
	Applied []string `json:"applied"` // "skill/x: linked", "memory: block written"
	Pruned  []string `json:"pruned"`  // "skill/x: stale link removed"
	Blocked []string `json:"blocked"` // "skill/x: a real file or a foreign link occupies PATH. Fix: move or remove PATH."
	Error   string   `json:"error,omitempty"`
}

// newReport starts a Report with its array fields set to empty, non-
// nil slices, so they always marshal as "[]" and never as JSON null.
// Every Apply method must build its Report with this, not a bare
// struct literal.
func newReport(adapterName string, dry bool) Report {
	return Report{
		Adapter: adapterName,
		DryRun:  dry,
		Applied: []string{},
		Pruned:  []string{},
		Blocked: []string{},
	}
}

// orEmpty returns s, or an empty (non-nil) slice when s is nil. A
// helper such as LinkSkills returns a plain nil slice when it has
// nothing to report; an adapter assigns its result straight to a
// Report field with this wrapper, so that field stays "[]" in JSON
// rather than reverting to null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
