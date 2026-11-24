package explorerapi

import (
	"encoding/json"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

func TestSelectionMemberToWirePreservesJSON(t *testing.T) {
	for _, test := range []struct {
		name      string
		ordinal   int64
		memberKey string
	}{
		{name: "optional fields omitted"},
		{name: "ordinal", ordinal: 17},
		{name: "member key", memberKey: "patient:123"},
		{name: "both optional fields", ordinal: 17, memberKey: "patient:123"},
	} {
		t.Run(test.name, func(t *testing.T) {
			member := explorerselection.SelectionMember{
				Ref: explorerselection.ResourceRef{
					Project: "project-1", Generation: "generation-2", ResourceType: "Patient", ID: "123",
				},
				Ordinal: test.ordinal, MemberKey: test.memberKey,
			}

			legacy, err := directAuthoringJSON[loomapi.SelectionMember](member)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(selectionMemberToWire(member))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("wire JSON = %s, want %s", got, want)
			}
		})
	}
}

var selectionPageBenchmarkSink []byte

func BenchmarkSelectionPageMapping(b *testing.B) {
	members := make([]explorerselection.SelectionMember, 100)
	for i := range members {
		members[i] = explorerselection.SelectionMember{
			Ref: explorerselection.ResourceRef{
				Project: "project-1", Generation: "generation-2", ResourceType: "Patient", ID: "123",
			},
			Ordinal: int64(i + 1), MemberKey: "patient:123",
		}
	}
	for _, legacy := range []bool{true, false} {
		name := "Typed"
		if legacy {
			name = "JSONRoundTrip"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				page := loomapi.SelectionPage{Members: make([]loomapi.SelectionMember, 0, len(members))}
				for _, member := range members {
					if legacy {
						wire, err := directAuthoringJSON[loomapi.SelectionMember](member)
						if err != nil {
							b.Fatal(err)
						}
						page.Members = append(page.Members, wire)
					} else {
						page.Members = append(page.Members, selectionMemberToWire(member))
					}
				}
				wire, err := json.Marshal(page)
				if err != nil {
					b.Fatal(err)
				}
				selectionPageBenchmarkSink = wire
			}
		})
	}
}
