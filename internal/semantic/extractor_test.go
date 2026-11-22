package semantic

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestExtensionURLAncestryMatchesScanOracle(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		urls  map[string]string
	}{
		{
			name:  "nested extension scopes",
			owner: "extension.extension.value.ofTypeCoding",
			urls: map[string]string{
				"extension":                                   "urn:outer",
				"extension.extension":                         "urn:inner",
				"extension.extension.value.ofTypeCoding":      "urn:leaf",
				"extension.extension.value.ofTypeCoding.code": "urn:not-owner",
			},
		},
		{
			name:  "prefixes require a dot boundary",
			owner: "extensionFoo.extensionBar",
			urls: map[string]string{
				"extension":                 "urn:wrong-prefix",
				"extensionFoo":              "urn:root",
				"extensionFoo.extensionBar": "urn:leaf",
			},
		},
		{
			name:  "empty URLs are omitted",
			owner: "extension.value",
			urls: map[string]string{
				"extension":       "",
				"extension.value": "urn:value",
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			want := extensionURLAncestryScanOracle(test.owner, test.urls)
			got := extensionURLAncestry(test.owner, test.urls)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ancestry = %#v, want %#v", got, want)
			}
		})
	}
}

func extensionURLAncestryScanOracle(ownerPath string, urls map[string]string) []string {
	type ancestor struct {
		owner string
		url   string
	}
	ancestors := make([]ancestor, 0)
	for owner, url := range urls {
		if url == "" || (owner != ownerPath && !strings.HasPrefix(ownerPath, owner+".")) {
			continue
		}
		ancestors = append(ancestors, ancestor{owner: owner, url: url})
	}
	sort.Slice(ancestors, func(left, right int) bool {
		return len(ancestors[left].owner) < len(ancestors[right].owner)
	})
	result := make([]string, 0, len(ancestors))
	for _, ancestor := range ancestors {
		result = append(result, ancestor.url)
	}
	return result
}
