package cmd

import (
	"fmt"
	"strconv"
)

// label renders the CLI-wide "Name (identifier)" display form for an entity,
// degrading to the bare identifier when the name is unknown.
func label(name string, id any) string {
	if name == "" {
		return fmt.Sprint(id)
	}
	return fmt.Sprintf("%s (%v)", name, id)
}

// nameRef returns the name half of an id-or-name reference: the ref itself for
// names, "" for all-digit id refs. Keeps a typed id out of label's name slot
// ("101 (101)") and out of server-side name searches, which can never match it.
func nameRef(ref string) string {
	if _, err := strconv.Atoi(ref); err == nil {
		return ""
	}
	return ref
}
