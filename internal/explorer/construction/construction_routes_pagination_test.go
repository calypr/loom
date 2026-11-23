package construction

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"

)

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
