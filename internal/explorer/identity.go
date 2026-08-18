package explorer

import (
	"strings"
)

// StableExplorerID turns a user-facing name into the durable URL identity
// used by the V2 lifecycle. Collisions remain store-level conflicts.
func StableExplorerID(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteByte(byte(r))
			lastDash = false
			continue
		}
		if b.Len() > 0 && !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "explorer"
	}
	if id[0] < 'a' || id[0] > 'z' {
		id = "explorer-" + id
	}
	if len(id) > 64 {
		id = strings.TrimRight(id[:64], "-")
	}
	return id
}
