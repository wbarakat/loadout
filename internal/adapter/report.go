package adapter

// Report holds what one adapter did on a sync. Applied and Pruned
// name the actions taken (or, under a dry run, the actions that
// would run). Blocked names each skill a real file or a foreign
// link stops Loadout from linking; a blocked skill is not an error.
type Report struct {
	Adapter string   `json:"adapter"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Applied []string `json:"applied,omitempty"` // "skill/x: linked", "memory: block written"
	Pruned  []string `json:"pruned,omitempty"`  // "skill/x: stale link removed"
	Blocked []string `json:"blocked,omitempty"` // "skill/x: a real file or a foreign link occupies PATH. Fix: move or remove PATH."
}
