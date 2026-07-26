package cmd

import "fmt"

// label renders the CLI-wide "Name (identifier)" display form for an entity,
// degrading to the bare identifier when the name is unknown.
func label(name string, id any) string {
	if name == "" {
		return fmt.Sprint(id)
	}
	return fmt.Sprintf("%s (%v)", name, id)
}
