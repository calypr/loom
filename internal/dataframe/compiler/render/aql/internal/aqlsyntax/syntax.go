package aqlsyntax

import (
	"strconv"
	"strings"
)

func PathHasPrefix(path, prefix []string) bool {
	if len(prefix) == 0 || len(path) < len(prefix) {
		return false
	}
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}

func RenderPropertyPath(base string, path []string) string {
	var expression strings.Builder
	expression.WriteString(base)
	for _, segment := range path {
		if IsSafePropertyIdentifier(segment) {
			expression.WriteByte('.')
			expression.WriteString(segment)
			continue
		}
		expression.WriteByte('[')
		expression.WriteString(strconv.Quote(segment))
		expression.WriteByte(']')
	}
	return expression.String()
}

func IsSafePropertyIdentifier(segment string) bool {
	if len(segment) == 0 || !IsIdentifierStart(segment[0]) {
		return false
	}
	for index := 1; index < len(segment); index++ {
		char := segment[index]
		if !IsIdentifierStart(char) && (char < '0' || char > '9') {
			return false
		}
	}
	_, reserved := aqlReservedWords[strings.ToUpper(segment)]
	return !reserved
}

func IsIdentifierStart(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

var aqlReservedWords = map[string]struct{}{
	"AGGREGATE": {}, "ALL": {}, "ALL_SHORTEST_PATHS": {}, "AND": {}, "ANY": {}, "ASC": {},
	"COLLECT": {}, "DESC": {}, "DISTINCT": {}, "FALSE": {}, "FILTER": {}, "FOR": {},
	"GRAPH": {}, "IN": {}, "INBOUND": {}, "INSERT": {}, "INTO": {}, "K_PATHS": {},
	"K_SHORTEST_PATHS": {}, "LET": {}, "LIKE": {}, "LIMIT": {}, "NONE": {}, "NOT": {},
	"NULL": {}, "OR": {}, "OUTBOUND": {}, "REMOVE": {}, "REPLACE": {}, "RETURN": {},
	"SHORTEST_PATH": {}, "SORT": {}, "TRUE": {}, "UPDATE": {}, "UPSERT": {}, "WINDOW": {},
	"WITH": {}, "CURRENT": {}, "NEW": {}, "OLD": {},
}
