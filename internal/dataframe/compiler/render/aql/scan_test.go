package aql

import (
	"reflect"
	"testing"
)

func assertCategoryScanBindVarsMatchQuery(t *testing.T, query string, bindVars map[string]any) {
	t.Helper()
	want := map[string]struct{}{}
	for index := 0; index < len(query); index++ {
		if query[index] != '@' {
			continue
		}
		collection := false
		if index+1 < len(query) && query[index+1] == '@' {
			collection = true
			index++
		}
		start := index + 1
		end := start
		for end < len(query) && categoryScanIdentifierByte(query[end]) {
			end++
		}
		if end > start {
			key := query[start:end]
			if collection {
				key = "@" + key
			}
			want[key] = struct{}{}
			index = end - 1
		}
	}
	got := make(map[string]struct{}, len(bindVars))
	for key := range bindVars {
		got[key] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query bind vars = %v, query references = %v\n%s", got, want, query)
	}
}
