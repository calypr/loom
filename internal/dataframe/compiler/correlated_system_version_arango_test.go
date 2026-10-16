package compiler

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/google/uuid"
)

func TestCorrelatedSystemVersionCategoryValuesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project := "loom_system_version_category_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx, `
FOR document IN Observation
  FILTER document.project == @project
  FILTER document.dataset_generation == @generation
  REMOVE document IN Observation`, map[string]any{"project": project, "generation": generation}); err != nil {
			t.Errorf("remove SYSTEM_VERSION category fixtures: %v", err)
		}
	})

	const system = "urn:loom:category-fixture"
	const versionedCode = "code-alpha"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{{
		"id": "category-codings",
		"component": []any{map[string]any{"code": map[string]any{"coding": []any{
			map[string]any{"system": system, "version": "v1", "code": versionedCode, "display": "Alpha display"},
			map[string]any{"system": system, "version": "v1", "code": "code-beta", "display": "  "},
			map[string]any{"system": "urn:loom:other-system", "version": "v1", "code": "wrong-system", "display": "Other system decoy"},
			map[string]any{"system": system, "version": "v2", "code": "wrong-version", "display": "Other version decoy"},
			map[string]any{"system": system, "code": "unversioned-missing", "display": ""},
			map[string]any{"system": system, "version": "", "code": "unversioned-empty", "display": "Unversioned explicit"},
		}}}},
	}})

	categoryBinding := &fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]", VersionPath: "version",
		SelectionScope: fhirschema.CorrelatedSelectionScopeSystemVersion,
		ValueScope:     fhirschema.CorrelatedValueScopeCoding,
		SystemPath:     "system", CodePath: "code", ValuePath: "display",
		ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	legacyExactBinding := &fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]", VersionPath: "version",
		ValueScope: fhirschema.CorrelatedValueScopeCoding,
		SystemPath: "system", CodePath: "code", ValuePath: "display",
		ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	root := semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation",
		Pivots: []semantic.SemanticPivot{
			{
				Name: "system_version_v1", Columns: []string{"all_codes"},
				ColumnAliases: map[string]string{"all_codes": "version_v1_values"}, ProjectionMode: "ALL",
				Correlation: categoryBinding, CorrelationSystem: system, CorrelationVersion: "v1",
			},
			{
				Name: "system_version_empty", Columns: []string{"all_codes"},
				ColumnAliases: map[string]string{"all_codes": "empty_version_values"}, ProjectionMode: "ALL",
				Correlation: categoryBinding, CorrelationSystem: system, CorrelationVersion: "",
			},
			{
				Name: "legacy_exact_code", Columns: []string{versionedCode},
				ColumnAliases: map[string]string{versionedCode: "legacy_exact_values"}, ProjectionMode: "ALL",
				Correlation: legacyExactBinding, CorrelationSystem: system, CorrelationCode: versionedCode, CorrelationVersion: "v1",
			},
		},
	}
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(
		semantic.OutputPlan{Root: root},
		semantic.ExecutionContext{Project: project, DatasetGeneration: generation},
		ir.DefaultPhysicalOptimizationPolicy(),
	)
	if err != nil {
		t.Fatalf("compile SYSTEM_VERSION semantic plan: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		t.Fatalf("render SYSTEM_VERSION semantic plan: %v", err)
	}
	for name, wantVersion := range map[string]string{"system_version_v1": "v1", "system_version_empty": ""} {
		prefix := "pivot_root_" + name + "_columns"
		if got, ok := rendered.BindVars[prefix+"_system"]; !ok || got != system {
			t.Fatalf("%s system bind = %#v (present=%t), want %q", name, got, ok, system)
		}
		if got, ok := rendered.BindVars[prefix+"_version"]; !ok || got != wantVersion {
			t.Fatalf("%s exact version bind = %#v (present=%t), want %q", name, got, ok, wantVersion)
		}
		if _, ok := rendered.BindVars[prefix+"_code"]; ok {
			t.Fatalf("%s SYSTEM_VERSION category unexpectedly bound an exact code", name)
		}
	}

	rows := []map[string]any{}
	if err := client.QueryRows(ctx, rendered.Query, 100, rendered.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute compiler-rendered AQL: %v\n%s", err, rendered.Query)
	}
	if len(rows) != 1 {
		t.Fatalf("compiler query returned %d rows, want one scoped Observation: %#v", len(rows), rows)
	}
	for column, want := range map[string][]string{
		"version_v1_values":    {"Alpha display", "code-beta"},
		"empty_version_values": {"Unversioned explicit", "unversioned-missing"},
		"legacy_exact_values":  {"Alpha display"},
	} {
		if got := sortedAnyStrings(rows[0][column]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want exact values %#v\n%s", column, got, want, rendered.Query)
		}
	}
}
