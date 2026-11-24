package text

import (
	"slices"
	"strings"
)

func FirstNonBlank(values ...string) string {
	raw, _ := firstNonBlank(values)
	return raw
}

func FirstTrimmed(values ...string) string {
	_, trimmed := firstNonBlank(values)
	return trimmed
}

func firstNonBlank(values []string) (raw, trimmed string) {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return value, trimmed
		}
	}
	return "", ""
}

func EqualUnordered(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left, right = slices.Clone(left), slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
