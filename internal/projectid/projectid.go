// Package projectid defines the public project identity used by Loom's
// Explorer APIs.  Existing generations and Explorer records may still be
// keyed by the historical program-project form, so callers can derive that
// storage alias without putting the legacy format back on the wire.
package projectid

import (
	"fmt"
	"net/url"
	"strings"
)

// Canonical returns the GitHub-style program/project identity. Legacy IDs are
// accepted at compatibility boundaries and split at their first hyphen so a
// project name may continue to contain additional hyphens.
func Canonical(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = strings.TrimSpace(decoded)
	}
	raw = strings.Trim(raw, "/")
	if strings.Contains(raw, "/") {
		parts := strings.SplitN(raw, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			return parts[0] + "/" + parts[1]
		}
		return raw
	}
	if index := strings.IndexByte(raw, '-'); index > 0 && index < len(raw)-1 {
		program := raw[:index]
		// Keep opaque single-component test/legacy IDs such as project-a
		// untouched. Real program/project IDs use the established program
		// namespace (for example HTAN_INT or EllrottLab) and are converted.
		if program != strings.ToLower(program) || strings.Contains(program, "_") {
			return program + "/" + raw[index+1:]
		}
	}
	return raw
}

// Legacy returns the historical program-project storage identity for a
// canonical ID. IDs that cannot be split are returned unchanged.
func Legacy(raw string) string {
	canonical := Canonical(raw)
	if index := strings.IndexByte(canonical, '/'); index > 0 && index < len(canonical)-1 {
		return canonical[:index] + "-" + canonical[index+1:]
	}
	return canonical
}

// Aliases returns canonical first, then legacy, without duplicates. It is
// useful for compatibility reads against records written before slash IDs.
func Aliases(raw string) []string {
	canonical := Canonical(raw)
	legacy := Legacy(canonical)
	if canonical == "" {
		return nil
	}
	if canonical == legacy {
		return []string{canonical}
	}
	return []string{canonical, legacy}
}

// RequireCanonical validates a project identity at a public API boundary.
func RequireCanonical(raw string) (string, error) {
	canonical := Canonical(raw)
	parts := strings.SplitN(canonical, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("project must use program/project format")
	}
	return canonical, nil
}
