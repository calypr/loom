package publication

import (
	"context"
	"os"
	"strings"
	"testing"
)

// F-B23 prevention evidence: can an author forge the row-level security
// metadata columns (auth_resource_path / project_id) through publication so a
// restricted reader sees rows it should not (or provenance is spoofed)?
// Adversary: author-controlled data (pivot ColumnExpr column names, dynamic
// unknown keys) plus authored schema. Verdict encoded here: PREVENTED by
// three independent lines, each pinned below.
//
//	L1 internal/dataframe/execution/engine.go publicStreamRow copies ONLY the
//	   compiled s.Columns plus the two compiler-retained keys
//	   {__loom_row_id, auth_resource_path} — a data-born row key outside the
//	   compiled column set is dropped upstream of this package, so smuggled
//	   keys never reach the publication runner. Pinned by source-quote gate
//	   and by the documented stream-flow (engine.Stream -> visit ->
//	   QualifyFlatRow -> injector). Data-born key semantics cannot be
//	   behavior-tested from this package (publicStreamRow is package
//	   `execution`); the source pin is the enforced contract.
//	L2 injectPublicationMetadata refuses a DECLARED auth_resource_path column
//	   (reserved-by-Loom) and strips author-declared project_id from schema.
//	L3 row["project_id"] is unconditionally overwritten with identity.Project
//	   ("never trust a source row value").
//
// The injector's "set auth_resource_path only if absent" branch is NOT a
// hole: a surviving value can only be the compiler's multi-path scope
// projection because L1 drops every other key. If the L1 fragments below
// change, this artifact must be re-adjudicated.

func TestFB23PublicationRowMetadataSmugglingPrevented(t *testing.T) {
	identity := PublicationIdentity{Project: "prj-auth", AuthResourcePaths: []string{"p1", "p2"}}

	// L2a: declaring the reserved security column is refused.
	reserved := OutputStream{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "auth_resource_path", Kind: "string"}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			return visit(nil)
		},
	}
	if _, err := injectPublicationMetadata(identity, []OutputStream{reserved}); err == nil || !strings.Contains(err.Error(), "reserved by Loom") {
		t.Errorf("FB23 L2a RED: declaring auth_resource_path must be refused, got %v", err)
	} else {
		t.Logf("FB23 L2a | reserved-column refusal | %v", err)
	}

	// L2b + L3: declared and row-forged project_id are both neutralized.
	sneaky := OutputStream{
		Name:    "patients",
		Columns: []LogicalColumn{{Name: "project_id", Kind: "string"}, {Name: "age", Kind: "integer"}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			return visit(map[string]any{"project_id": "OTHER-tenant", "age": 7})
		},
	}
	out, err := injectPublicationMetadata(identity, []OutputStream{sneaky})
	if err != nil {
		t.Fatalf("FB23 harness: %v", err)
	}
	authorProjectColumns, loomOwnedProject := 0, 0
	for _, c := range out[0].Columns {
		if c.Name == "project_id" {
			if c.LoomOwned {
				loomOwnedProject++
			} else {
				authorProjectColumns++
			}
		}
	}
	if authorProjectColumns != 0 || loomOwnedProject != 1 {
		t.Errorf("FB23 L2b RED: expected author project_id stripped and exactly one Loom-owned column, got author=%d loomOwned=%d", authorProjectColumns, loomOwnedProject)
	} else {
		t.Logf("FB23 L2b | schema neutralization | author-declared project_id stripped, sole Loom-owned project_id remains")
	}
	var seen map[string]any
	if err := out[0].Stream(context.Background(), func(row map[string]any) error { seen = row; return nil }); err != nil {
		t.Fatalf("FB23 stream: %v", err)
	}
	if seen["project_id"] != identity.Project {
		t.Errorf("FB23 L3 RED: forged project_id not overwritten: %v", seen["project_id"])
	} else {
		t.Logf("FB23 L3 | project_id clamp | forged source value %q replaced by identity.Project %q", "OTHER-tenant", identity.Project)
	}
	if v, ok := seen["auth_resource_path"]; !ok || v != "" {
		t.Errorf("FB23 characterization drift: 2-path identity should default absent rows to empty string, got %v ok=%v", v, ok)
	} else {
		t.Logf("FB23 note | auth_resource_path stays compiler-owned: absent rows get empty for 0/2+ paths; row-supplied values are intentionally preserved because L1 filters row keys upstream of this package")
	}

	// L1 pin: the engine-side declared-column filter must remain quote-identical.
	src, err := os.ReadFile("../execution/engine.go")
	if err != nil {
		t.Fatalf("FB23 L1 pin unreadable: %v", err)
	}
	body := string(src)
	for _, fragment := range []string{
		"func publicStreamRow(row map[string]any, columns []string) map[string]any {",
		`for _, column := range []string{"__loom_row_id", "auth_resource_path"}`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("FB23 L1 RED: engine filter contract vanished — data-born row keys no longer pinned: %q", fragment)
		} else {
			t.Logf("FB23 L1 gate | engine contract intact | %q", fragment)
		}
	}
	own, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("FB23 injector pin unreadable: %v", err)
	}
	for _, fragment := range []string{
		`if _, ok := row["auth_resource_path"]; !ok {`,
		`row["project_id"] = identity.Project`,
	} {
		if !strings.Contains(string(own), fragment) {
			t.Errorf("FB23 L2/L3 RED: injector contract vanished: %q", fragment)
		} else {
			t.Logf("FB23 L2/L3 gate | injector contract intact | %q", fragment)
		}
	}
}
