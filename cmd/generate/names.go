package main

import "strings"

func refName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func targetTypeFromLabel(label string) string {
	parts := strings.Split(label, "_")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func toGoName(s string) string {
	if s == "" {
		return ""
	}
	prefix := ""
	if strings.HasPrefix(s, "_") {
		prefix = "X"
		s = s[1:]
	}
	parts := strings.Split(s, "-")
	for i, part := range parts {
		parts[i] = capitalize(part)
	}
	s = strings.Join(parts, "")
	parts = strings.Split(s, "_")
	for i, part := range parts {
		parts[i] = capitalize(part)
	}
	s = strings.Join(parts, "")
	s = strings.ReplaceAll(s, "Id", "ID")
	s = strings.ReplaceAll(s, "Uri", "URI")
	s = strings.ReplaceAll(s, "Url", "URL")
	s = strings.ReplaceAll(s, "Uuid", "UUID")
	return prefix + s
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

func isRequired(def *Definition, propName string) bool {
	for _, req := range def.Required {
		if req == propName {
			return true
		}
	}
	return false
}
