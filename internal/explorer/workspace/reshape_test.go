package workspace

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

//go:embed testdata/*.json
var storedTableShapeFixtures embed.FS

func TestMigrateStoredTableShapeWorkspaceMatchesLegacyGoldens(t *testing.T) {
	for _, name := range []string{"pivot-derived-chain", "explicit-pivot", "typed-unpivot"} {
		t.Run(name, func(t *testing.T) {
			raw := storedTableShapeFixture(t, name+".input.json")
			want := storedTableShapeFixture(t, name+".golden.json")
			got, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.CanonicalJSON, want) {
				t.Fatalf("private migration differs from old upgrader golden:\nwant=%s\n got=%s", want, got.CanonicalJSON)
			}
			if got.MigratedCanonicalDigest != workspaceDigest(want) {
				t.Fatalf("migrated digest=%q want=%q", got.MigratedCanonicalDigest, workspaceDigest(want))
			}
			if !slices.Contains(got.AcceptedOriginalDigests, got.OriginalCanonicalDigest) || got.OriginalCanonicalDigest == got.MigratedCanonicalDigest || !got.DigestChanged {
				t.Fatalf("receipt digest change is not explicit: %#v", got)
			}
		})
	}
}

func TestMigrateStoredTableShapeWorkspacePreservesOperationsAndTypedKeys(t *testing.T) {
	pivotRaw := storedTableShapeFixture(t, "pivot-derived-chain.input.json")
	pivot, err := MigrateStoredTableShapeWorkspace(pivotRaw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pivot.Workspace.Documents) != 1 || pivot.Workspace.Documents[0].Construction == nil {
		t.Fatalf("pivot construction is missing: %#v", pivot.Workspace.Documents)
	}
	steps := pivot.Workspace.Documents[0].Construction.Steps
	if len(steps) != 3 || steps[0].Operation.Pivot == nil || steps[1].Operation.Derive == nil || steps[2].Operation.Derive == nil {
		t.Fatalf("legacy pivot/derived chain changed: %#v", steps)
	}
	if steps[1].Operation.Derive.OutputColumnID == "" || steps[2].Operation.Derive.OutputColumnID == "" || steps[1].Operation.Derive.OutputColumnID == steps[2].Operation.Derive.OutputColumnID {
		t.Fatalf("derived stable output IDs changed: %#v", steps)
	}
	if steps[0].Operation.Pivot.DuplicatePolicy != ConstructionPivotDuplicateMax || steps[0].Operation.Pivot.UnlistedCategoryPolicy != ConstructionPivotUnlistedError {
		t.Fatalf("pivot policy changed: %#v", steps[0].Operation.Pivot)
	}

	unpivotRaw := storedTableShapeFixture(t, "typed-unpivot.input.json")
	unpivot, err := MigrateStoredTableShapeWorkspace(unpivotRaw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	operation := unpivot.Workspace.Documents[0].Construction.Steps[0].Operation.Unpivot
	if operation == nil || operation.NullRowPolicy != ConstructionUnpivotPreserve || len(operation.Inputs) != 2 {
		t.Fatalf("typed unpivot conversion changed: %#v", operation)
	}
	first, second := operation.Inputs[0].Key, operation.Inputs[1].Key
	if first.Kind != TableScalarInteger || first.Integer == nil || *first.Integer != 10 || second.Kind != TableScalarInteger || second.Integer == nil || *second.Integer != 20 {
		t.Fatalf("unpivot typed keys changed: %#v", operation.Inputs)
	}
}

func TestMigrateStoredTableShapeWorkspaceIsDetachedAndIdempotent(t *testing.T) {
	raw := storedTableShapeFixture(t, "pivot-derived-chain.input.json")
	before := append([]byte(nil), raw...)
	first, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	canonical := append([]byte(nil), first.CanonicalJSON...)
	first.Workspace.Explorer.Title = "mutated returned value"
	if !bytes.Equal(first.CanonicalJSON, canonical) || !bytes.Equal(raw, before) {
		t.Fatal("migration result aliases caller bytes or returned workspace")
	}
	second, err := MigrateStoredTableShapeWorkspace(canonical, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.CanonicalJSON, canonical) || second.DigestChanged {
		t.Fatalf("rerun was not idempotent: changed=%t\nfirst=%s\nsecond=%s", second.DigestChanged, canonical, second.CanonicalJSON)
	}
}

func TestMigrateStoredTableShapeWorkspaceDoesNotReturnPartialConversion(t *testing.T) {
	raw := storedTableShapeFixture(t, "partial-failure.input.json")
	before := append([]byte(nil), raw...)
	result, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err == nil || !strings.Contains(err.Error(), "legacy arithmetic literal kind") {
		t.Fatalf("unsupported legacy literal error=%v", err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("failed migration changed caller input")
	}
	if len(result.Workspace.Documents) != 2 || result.Workspace.Documents[0].Construction != nil || result.Workspace.Documents[1].Construction != nil {
		t.Fatalf("failed migration returned partial Construction: %#v", result.Workspace)
	}
}

func TestMigrateStoredTableShapeWorkspaceRejectsMixedConstructionAndShape(t *testing.T) {
	raw := storedTableShapeFixture(t, "mixed-construction-shape.input.json")
	if _, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{}); err == nil || !strings.Contains(err.Error(), "construction and tableShape") {
		t.Fatalf("mixed Construction/storedTableShape error=%v", err)
	}
}

func TestStoredTableShapePrivateDecoderRejectsUnknownAndMalformedUnion(t *testing.T) {
	raw := storedTableShapeFixture(t, "explicit-pivot.input.json")
	var envelope struct {
		Documents []struct {
			ArchiveRetiredShape json.RawMessage `json:"tableShape"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Documents) != 1 {
		t.Fatalf("fixture decode err=%v documents=%d", err, len(envelope.Documents))
	}
	var shape storedTableShape
	if err := strictDecode(envelope.Documents[0].ArchiveRetiredShape, &shape); err != nil || shape.Reshape == nil || shape.Reshape.Pivot == nil {
		t.Fatalf("private legacy decode shape=%#v err=%v", shape, err)
	}
	for _, malformed := range [][]byte{
		[]byte(`{"reshape":{"kind":"PIVOT","pivot":{},"unpivot":{}}}`),
		[]byte(`{"reshape":{"kind":"PIVOT","pivot":{},"unknown":true}}`),
	} {
		var rejected storedTableShape
		if err := strictDecode(malformed, &rejected); err == nil {
			t.Fatalf("private legacy decoder accepted malformed input: %s", malformed)
		}
	}
}

func TestStoredTableShapePrivateWireMatchesArchiveJSON(t *testing.T) {
	raw := storedTableShapeFixture(t, "pivot-derived-chain.input.json")
	var envelope struct {
		Documents []struct {
			ArchiveRetiredShape json.RawMessage `json:"tableShape"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Documents) != 1 {
		t.Fatalf("fixture decode err=%v documents=%d", err, len(envelope.Documents))
	}
	var stored storedTableShape
	if err := strictDecode(envelope.Documents[0].ArchiveRetiredShape, &stored); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(normalizedJSON(t, envelope.Documents[0].ArchiveRetiredShape), normalizedJSON(t, encoded)) {
		t.Fatalf("private stored wire changed archive payload:\nold=%s\nnew=%s", envelope.Documents[0].ArchiveRetiredShape, encoded)
	}
}

func storedTableShapeFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := storedTableShapeFixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func normalizedJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestStoredTableShapeMigrationErrorHasDocumentIndex(t *testing.T) {
	raw := storedTableShapeFixture(t, "partial-failure.input.json")
	_, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("documents[%d].tableShape", 1)) {
		t.Fatalf("migration error lacks document path: %v", err)
	}
}
