package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestTableShapeExclusionEvidencePreservesTypedValuesAndIdentity(t *testing.T) {
	candidate := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:         recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence},
	})
	result := dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionComplete,
		Exclusions: []dataframeexecution.TableShapeExclusion{
			{
				SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-false"},
				Category:       dataframeexecution.CategoryValue{Present: true, Value: false}, CategoryType: "BOOLEAN",
				OutputRowID: "row-false", Reason: "UNLISTED_CATEGORY",
			},
			{
				SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-zero"},
				Category:       dataframeexecution.CategoryValue{Present: true, Value: int64(0)}, CategoryType: "INTEGER",
				OutputRowID: "row-zero", Reason: "UNLISTED_CATEGORY",
			},
			{
				SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-empty"},
				Category:       dataframeexecution.CategoryValue{Present: true, Value: ""}, CategoryType: "STRING",
				OutputRowID: "row-empty", Reason: "UNLISTED_CATEGORY",
			},
			{
				SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-null"},
				Category:       dataframeexecution.CategoryValue{Present: true, Value: nil}, CategoryType: "NULL",
				OutputRowID: "row-null", Reason: "UNLISTED_CATEGORY",
			},
		},
		Complete: true,
	}

	evidence := tableShapeExclusionsFromExecution(candidate, "patients", result)
	if evidence.Status != TableShapeExclusionsComplete || !evidence.Complete || evidence.Sampled || len(evidence.Records) != 4 {
		t.Fatalf("exclusion evidence = %#v", evidence)
	}
	for index, want := range []any{false, int64(0), "", nil} {
		record := evidence.Records[index]
		if record.SourceIdentity == nil || record.Category.Value != want || !record.Category.Present || record.CategoryType == "" || record.OutputRowID == "" || record.Reason != "UNLISTED_CATEGORY" {
			t.Fatalf("record %d lost exact exclusion facts: %#v", index, record)
		}
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Records []struct {
			Category struct {
				Present bool            `json:"present"`
				Value   json.RawMessage `json:"value"`
			} `json:"category"`
		} `json:"records"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	wantValues := []string{"false", "0", `""`, "null"}
	if len(wire.Records) != len(wantValues) {
		t.Fatalf("serialized records = %s", encoded)
	}
	for index, want := range wantValues {
		if !wire.Records[index].Category.Present || string(wire.Records[index].Category.Value) != want {
			t.Fatalf("serialized category %d = %#v, want value %s", index, wire.Records[index].Category, want)
		}
	}
}

func TestTableShapeExclusionEvidencePreservesOmissionAndSampling(t *testing.T) {
	candidate := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:         recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence},
	})
	evidence := tableShapeExclusionsFromExecution(candidate, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionIncomplete,
		Exclusions: []dataframeexecution.TableShapeExclusion{{
			Category: dataframeexecution.CategoryValue{Present: false, Value: ""}, CategoryType: "STRING",
			OutputRowID: "row-1", Reason: "UNLISTED_CATEGORY", OmissionCode: "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE",
		}},
		Complete: false, HasMore: true, NextOffset: 1,
	})
	if evidence.Status != TableShapeExclusionsIncomplete || evidence.Complete || !evidence.Sampled || len(evidence.Records) != 1 || evidence.Records[0].SourceIdentity != nil || evidence.Records[0].OmissionCode != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("incomplete exclusion evidence = %#v", evidence)
	}
	limitations := tableShapeEvidenceLimitations(evidence, completeTableShapeInformationLoss())
	want := []string{"TABLE_SHAPE_EXCLUSIONS_SAMPLED", "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"}
	got := make([]string, 0, len(limitations))
	for _, limitation := range limitations {
		got = append(got, limitation.Code)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incomplete evidence limitations = %#v, want codes %#v", limitations, want)
	}
}

func TestTableShapeUnsupportedAndNoPolicyOnlyProveZeroWhenSemanticsAllowIt(t *testing.T) {
	derivedOnly := tableShapeEvidenceReceipt(nil)
	unsupported := tableShapeExclusionsFromExecution(derivedOnly, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionUnsupported,
	})
	if unsupported.Status != TableShapeExclusionsComplete || !unsupported.Complete || len(unsupported.Records) != 0 {
		t.Fatalf("derived-only unsupported result = %#v, want exact zero exclusions", unsupported)
	}

	pivotError := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:         recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError},
	})
	noPolicy := tableShapeExclusionsFromExecution(pivotError, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionNoExclusionPolicy,
	})
	if noPolicy.Status != TableShapeExclusionsComplete || !noPolicy.Complete || len(noPolicy.Records) != 0 {
		t.Fatalf("error-policy result = %#v, want exact zero exclusions", noPolicy)
	}

	pivotEvidence := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:         recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence},
	})
	unknown := tableShapeExclusionsFromExecution(pivotEvidence, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionUnsupported,
	})
	if unknown.Status != TableShapeExclusionsUnavailable || unknown.Complete || unknown.FailureCode != "TABLE_SHAPE_EXCLUSION_UNSUPPORTED" {
		t.Fatalf("evidence-policy unsupported result = %#v, want limitation", unknown)
	}

	unpivotDrop := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:    recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{NullRowPolicy: recipe.UnpivotNullDrop},
	})
	droppedRows := tableShapeExclusionsFromExecution(unpivotDrop, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionUnsupported,
	})
	if droppedRows.Status != TableShapeExclusionsUnavailable || droppedRows.Complete || droppedRows.FailureCode != "TABLE_SHAPE_EXCLUSION_UNSUPPORTED" {
		t.Fatalf("DROP unpivot result = %#v, want unresolved row exclusion limitation", droppedRows)
	}

	unpivotPreserve := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:    recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{NullRowPolicy: recipe.UnpivotNullPreserve},
	})
	preservedRows := tableShapeExclusionsFromExecution(unpivotPreserve, "patients", dataframeexecution.TableShapeExclusionResult{
		Status: dataframeexecution.TableShapeExclusionUnsupported,
	})
	if preservedRows.Status != TableShapeExclusionsComplete || !preservedRows.Complete || len(preservedRows.Records) != 0 {
		t.Fatalf("PRESERVE unpivot result = %#v, want exact zero exclusions", preservedRows)
	}
}

func TestTableShapeExclusionExecutorFailureDegradesWithStableLimitation(t *testing.T) {
	candidate := tableShapeEvidenceReceipt(nil)
	service := &Service{config: Config{
		TableShapeExclusions: func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			return dataframeexecution.TableShapeExclusionResult{}, errors.New("backend detail must not cross the response boundary")
		},
	}}
	comparison := unavailableTableShapeComparison("PREVIEW_UNAVAILABLE", "preview unavailable")
	if err := service.attachTableShapeReceiptEvidence(context.Background(), tableShapeEvidenceReceipt(nil), candidate, recipe.RuntimeBindings{}, "patients", &comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Exclusions.Status != TableShapeExclusionsUnavailable || comparison.Exclusions.FailureCode != "TABLE_SHAPE_EXCLUSION_EXECUTION_FAILED" {
		t.Fatalf("failed exclusion execution = %#v", comparison.Exclusions)
	}
	if len(comparison.EvidenceLimitations) != 1 || comparison.EvidenceLimitations[0].Code != "TABLE_SHAPE_EXCLUSION_EXECUTION_FAILED" || comparison.EvidenceLimitations[0].Message == "" {
		t.Fatalf("failed exclusion limitation = %#v", comparison.EvidenceLimitations)
	}
	encoded, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsJSONText(encoded, "backend detail") {
		t.Fatalf("callback error text leaked into response: %s", encoded)
	}
}

func TestTableShapeExclusionExecutorCannotExceedFixedComparisonPage(t *testing.T) {
	candidate := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind:         recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence},
	})
	service := &Service{config: Config{
		TableShapeExclusions: func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			if request.Offset != 0 || request.Limit != maxProposalComparisonExclusions {
				t.Fatalf("exclusion request = %#v, want first bounded page of %d", request, maxProposalComparisonExclusions)
			}
			records := make([]dataframeexecution.TableShapeExclusion, maxProposalComparisonExclusions+1)
			for index := range records {
				records[index] = dataframeexecution.TableShapeExclusion{
					Category: dataframeexecution.CategoryValue{Present: true, Value: index}, CategoryType: "INTEGER",
					OutputRowID: "row", Reason: "UNLISTED_CATEGORY",
				}
			}
			return dataframeexecution.TableShapeExclusionResult{
				Status: dataframeexecution.TableShapeExclusionComplete, Exclusions: records, Complete: true,
			}, nil
		},
	}}
	comparison := unavailableTableShapeComparison("PREVIEW_UNAVAILABLE", "preview unavailable")
	if err := service.attachTableShapeReceiptEvidence(context.Background(), tableShapeEvidenceReceipt(nil), candidate, recipe.RuntimeBindings{}, "patients", &comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Exclusions.Status != TableShapeExclusionsIncomplete || comparison.Exclusions.Complete || !comparison.Exclusions.Sampled || len(comparison.Exclusions.Records) != maxProposalComparisonExclusions {
		t.Fatalf("oversized executor page = status %s, complete %t, sampled %t, records %d", comparison.Exclusions.Status, comparison.Exclusions.Complete, comparison.Exclusions.Sampled, len(comparison.Exclusions.Records))
	}
	if len(comparison.EvidenceLimitations) != 1 || comparison.EvidenceLimitations[0].Code != "TABLE_SHAPE_EXCLUSIONS_SAMPLED" {
		t.Fatalf("oversized executor limitations = %#v", comparison.EvidenceLimitations)
	}
}

func TestTableShapeDeclaredInformationLossFollowsCandidateRecipe(t *testing.T) {
	grouped := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			GroupKeys: []string{"patient_id"}, CategoryColumn: "category", ValueColumn: "value",
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	base := tableShapeEvidenceReceipt(nil)
	groupedLoss := declaredTableShapeInformationLoss(base, grouped, "patients")
	if groupedLoss.Status != TableShapeInformationLossComplete || len(groupedLoss.Items) != 1 || groupedLoss.Items[0].Code != "GROUPED_PIVOT_DROPS_NON_GROUP_OUTPUT_COLUMNS" || groupedLoss.Items[0].Label == "" || groupedLoss.Items[0].Detail == "" || !reflect.DeepEqual(groupedLoss.Items[0].AffectedColumns, []string{"category", "measure_a", "measure_b", "value"}) {
		t.Fatalf("grouped pivot loss declaration = %#v", groupedLoss)
	}

	unpivot := tableShapeEvidenceReceipt(&recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			Inputs:        []recipe.UnpivotInput{{Column: "measure_a"}, {Column: "measure_b"}},
			NullRowPolicy: recipe.UnpivotNullDrop,
		},
	})
	unpivotLoss := declaredTableShapeInformationLoss(base, unpivot, "patients")
	if unpivotLoss.Status != TableShapeInformationLossComplete || len(unpivotLoss.Items) != 2 || unpivotLoss.Items[0].Code != "UNPIVOT_REMOVES_SELECTED_INPUT_COLUMNS" || !reflect.DeepEqual(unpivotLoss.Items[0].AffectedColumns, []string{"measure_a", "measure_b"}) || unpivotLoss.Items[1].Code != "UNPIVOT_DROP_NULL_ROWS" {
		t.Fatalf("unpivot loss declarations = %#v", unpivotLoss)
	}

	derivedOnly := tableShapeEvidenceReceipt(nil)
	derivedLoss := declaredTableShapeInformationLoss(base, derivedOnly, "patients")
	if derivedLoss.Status != TableShapeInformationLossComplete || len(derivedLoss.Items) != 0 {
		t.Fatalf("derived-only loss declaration = %#v, want complete empty declaration", derivedLoss)
	}
}

func tableShapeEvidenceReceipt(reshape *recipe.TableReshape) *explorer.CompilationReceipt {
	columns := []string{"patient_id", "category", "value", "measure_a", "measure_b"}
	if reshape != nil {
		switch reshape.Kind {
		case recipe.TableReshapeGroupedPivot:
			columns = []string{"patient_id", "active_measure"}
		case recipe.TableReshapeUnpivot:
			columns = []string{"patient_id", "metric_name", "metric_value"}
		}
	}
	receipt := &explorer.CompilationReceipt{Bundle: recipe.Bundle{
		Outputs: []recipe.Output{{Name: "patients", TableReshape: reshape}},
	}}
	for _, column := range columns {
		receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{OutputID: "patients", PublicColumn: column})
	}
	return receipt
}

func completeTableShapeInformationLoss() TableShapeDeclaredInformationLoss {
	return TableShapeDeclaredInformationLoss{Status: TableShapeInformationLossComplete, Items: []TableShapeInformationLoss{}}
}

func containsJSONText(raw json.RawMessage, value string) bool {
	return strings.Contains(string(raw), value)
}
