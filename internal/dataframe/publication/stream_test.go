package publication

import (
	"context"
	"strings"
	"testing"
)

func TestFB23PublicationRowMetadataSmugglingPrevented(t *testing.T) {
	identity := PublicationIdentity{Project: "prj-auth", AuthResourcePaths: []string{"p1", "p2"}}

	reserved := OutputStream{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "auth_resource_path", Kind: "string"}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			return visit(nil)
		},
	}
	if _, err := injectPublicationMetadata(identity, []OutputStream{reserved}); err == nil || !strings.Contains(err.Error(), "reserved by Loom") {
		t.Errorf("declaring auth_resource_path must be refused, got %v", err)
	}

	sneaky := OutputStream{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "project_id", Kind: "string"}, {Name: "age", Kind: "integer"}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"project_id": "OTHER-tenant", "age": 7})
		},
	}
	out, err := injectPublicationMetadata(identity, []OutputStream{sneaky})
	if err != nil {
		t.Fatalf("inject publication metadata: %v", err)
	}
	authorProjectColumns, loomOwnedProject := 0, 0
	for _, column := range out[0].Columns {
		if column.Name == "project_id" {
			if column.LoomOwned {
				loomOwnedProject++
			} else {
				authorProjectColumns++
			}
		}
	}
	if authorProjectColumns != 0 || loomOwnedProject != 1 {
		t.Fatalf("expected author project_id stripped and exactly one Loom-owned column, got author=%d loomOwned=%d", authorProjectColumns, loomOwnedProject)
	}
	var seen map[string]any
	if err := out[0].Stream(context.Background(), func(row map[string]any) error { seen = row; return nil }); err != nil {
		t.Fatalf("stream publication metadata: %v", err)
	}
	if seen["project_id"] != identity.Project {
		t.Fatalf("forged project_id = %v, want trusted project %q", seen["project_id"], identity.Project)
	}
	if value, ok := seen["auth_resource_path"]; !ok || value != "" {
		t.Fatalf("absent auth_resource_path for multi-path identity = %v (present %t), want empty string", value, ok)
	}
}
