package projectid

import (
	"net/url"
	"strings"
)

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
		if program != strings.ToLower(program) || strings.Contains(program, "_") {
			return program + "/" + raw[index+1:]
		}
	}
	return raw
}

func Legacy(raw string) string {
	canonical := Canonical(raw)
	if index := strings.IndexByte(canonical, '/'); index > 0 && index < len(canonical)-1 {
		return canonical[:index] + "-" + canonical[index+1:]
	}
	return canonical
}
