package construction

import (
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRelatedChoiceCursorCodecsRoundTripAndRejectMalformedInput(t *testing.T) {
	field := relatedFieldChoiceCursor{Version: 1, Snapshot: "snapshot", DraftVersion: 3, DraftDigest: "digest", OutputID: "patients", StageID: "stage", Query: "id", Offset: 4}
	encodedField, err := encodeRelatedFieldChoiceCursor(field)
	if err != nil {
		t.Fatal(err)
	}
	decodedField, err := decodeRelatedFieldChoiceCursor(encodedField)
	if err != nil || !reflect.DeepEqual(decodedField, field) {
		t.Fatalf("field cursor round trip = %+v, %v", decodedField, err)
	}

	expanded := relatedExpandContributorChoiceCursor{Version: 1, Snapshot: "snapshot", DraftVersion: 3, DraftDigest: "digest", OutputID: "patients", StageID: "stage", RouteChoice: "choice", Query: "Patient", Offset: 2}
	encodedExpanded, err := encodeRelatedExpandContributorChoiceCursor(expanded)
	if err != nil {
		t.Fatal(err)
	}
	decodedExpanded, err := decodeRelatedExpandContributorChoiceCursor(encodedExpanded)
	if err != nil || !reflect.DeepEqual(decodedExpanded, expanded) {
		t.Fatalf("expanded cursor round trip = %+v, %v", decodedExpanded, err)
	}

	for _, encoded := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte("{"))} {
		if _, err := decodeRelatedFieldChoiceCursor(encoded); err == nil {
			t.Fatalf("malformed field cursor %q was accepted", encoded)
		}
		if _, err := decodeRelatedExpandContributorChoiceCursor(encoded); err == nil {
			t.Fatalf("malformed expanded cursor %q was accepted", encoded)
		}
	}
}

func TestSavedRelatedExpandPagesRespectLimitAndScope(t *testing.T) {
	routes := [][]capability.ConstructionRouteStep{{{EdgeID: "first"}}, {{EdgeID: "second"}}}
	scope := savedRelatedExpandCursor{Snapshot: "snapshot", Draft: "draft", Output: "patients", Stage: "source", Anchor: "_key", Target: "Observation"}
	first, cursor, err := pageSavedRelatedExpandRoutes(routes, scope, "", 1)
	if err != nil || len(first) != 1 || first[0][0].EdgeID != "first" || cursor == "" {
		t.Fatalf("first saved page = %#v, %q, %v", first, cursor, err)
	}
	second, cursorAfter, err := pageSavedRelatedExpandRoutes(routes, scope, cursor, 1)
	if err != nil || len(second) != 1 || second[0][0].EdgeID != "second" || cursorAfter != "" {
		t.Fatalf("second saved page = %#v, %q, %v", second, cursorAfter, err)
	}
	scope.Draft = "new-draft"
	if _, _, err := pageSavedRelatedExpandRoutes(routes, scope, cursor, 1); err == nil {
		t.Fatal("saved graph cursor admitted a different draft")
	}
}
