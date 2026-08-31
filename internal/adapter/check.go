package adapter

import (
	"fmt"
	"strings"

	"loadout.dev/loadout/internal/vault"
)

// scanForMarks looks for a loadout mark in a fact body or a skill
// description. A mark there would flow into a rendered block and
// break it. On a hit, it returns an error that names the item.
func scanForMarks(facts []vault.Fact, skills []vault.Skill) error {
	for _, f := range facts {
		if strings.Contains(f.Body, beginMark) || strings.Contains(f.Body, endMark) {
			return fmt.Errorf("memory/%s: the body holds a loadout mark. Fix: remove the mark text from the item.", f.Name)
		}
	}
	for _, s := range skills {
		if strings.Contains(s.Description, beginMark) || strings.Contains(s.Description, endMark) {
			return fmt.Errorf("skill/%s: the description holds a loadout mark. Fix: remove the mark text from the item.", s.Name)
		}
	}
	return nil
}
