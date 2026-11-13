package benchmark

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// Battery authoring: one workspace per workload in the exact wire shapes
// proven by testdata/acceptance/ncpi-tcga-brca/workspace.json. The live flow
// per workload is four public routes, no builder commands:
//
//	POST .../generations/{g}/explorer-config         (workspace JSON)
//	POST .../explorers {sourceExplorerId:"default"}  (draft = active revision)
//	POST .../authoring/v2/reconcile                  (compile receipt)
//	POST .../authoring/v2/preview {receiptId, outputId, limit}
//
// Every template is validated offline by the repository's own decoder
// (AuthoringVerification); anything the decoder rejects is published as
// planned/not_run with the decoder's error as the named capability, never a
// silent skip and never an approximated shape.

// AuthoringStatus is the offline authoring verdict for one workload.
type AuthoringStatus string

const (
	// AuthoringProven: shapes copied verbatim from the acceptance fixture.
	AuthoringProven AuthoringStatus = "proven"
	// AuthoringUnverified: decodes, but a shape or cast has no acceptance
	// precedent; rehearsal classifies it before any claim.
	AuthoringUnverified AuthoringStatus = "unverified-shape"
	// AuthoringRejected: the decoder refuses it; the workload is
	// planned/not_run with the decoder error as the capability note.
	AuthoringRejected AuthoringStatus = "rejected"
)

type authoringNote struct {
	Status AuthoringStatus
	Detail string
}

// wire shapes, verbatim families from the acceptance fixture
type wsColumnDef struct {
	Column       string         `json:"column"`
	Label        string         `json:"label"`
	LogicalType  string         `json:"logicalType"`
	OccurrenceID string         `json:"occurrenceId"`
	Source       map[string]any `json:"source"`
	Table        wsTable        `json:"table"`
}

type wsTable struct {
	Visible bool `json:"visible"`
	Order   int  `json:"order"`
}

type wsChild struct {
	OccurrenceID string    `json:"occurrenceId"`
	ResourceType string    `json:"resourceType"`
	Relationship string    `json:"relationship"`
	Children     []wsChild `json:"children,omitempty"`
}

type wsDoc struct {
	Kind             string        `json:"kind"`
	Output           wsOutputDef   `json:"output"`
	RootResourceType string        `json:"rootResourceType"`
	Route            wsRouteNode   `json:"route"`
	Columns          []wsColumnDef `json:"columns"`
	FixedFilters     []wsFixed     `json:"fixedFilters,omitempty"`
}

type wsFixed struct {
	Column string   `json:"column"`
	Values []string `json:"values"`
}

type wsRouteNode struct {
	OccurrenceID string    `json:"occurrenceId"`
	ResourceType string    `json:"resourceType"`
	Children     []wsChild `json:"children,omitempty"`
}

type wsOutputDef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	RowLabel string `json:"rowLabel,omitempty"`
}

type wsWorkspaceDoc struct {
	APIVersion       string     `json:"apiVersion"`
	Kind             string     `json:"kind"`
	SemanticsVersion int        `json:"semanticsVersion"`
	Explorer         wsExplorer `json:"explorer"`
	Documents        []wsDoc    `json:"documents"`
	Tabs             []wsTabDef `json:"tabs"`
}

type wsTabDef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	OutputID string `json:"outputId"`
	Order    int    `json:"order"`
	Visible  bool   `json:"visible"`
}

type wsExplorer struct {
	Title string `json:"title"`
}

const (
	wsAPIVersion       = "loom.calypr.org/explorer-authoring/v2"
	wsKind             = "ExplorerBuilderWorkspace"
	wsDocKind          = "ExplorerBuilderDocument"
	wsSemanticsVersion = 3 // the acceptance-proven version; DecodeWorkspace owns migration
)

// OutputIDFor maps a workload id to a decoder-legal output id
// (letters/digits/underscores only).
func OutputIDFor(workloadID string) string {
	return strings.ReplaceAll(workloadID, "-", "_")
}

func fieldCol(column, occurrence, path, mode, logical string, order int) wsColumnDef {
	return wsColumnDef{
		Column: column, Label: column, LogicalType: logical, OccurrenceID: occurrence,
		Source: map[string]any{"kind": "field", "field": map[string]any{"path": path, "projectionMode": mode}},
		Table:  wsTable{Visible: true, Order: order},
	}
}

func aggregateCol(column, label, logical, occurrence, operation, path, wherePath, whereEquals string, order int) wsColumnDef {
	aggregate := map[string]any{"operation": operation}
	if path != "" {
		aggregate["path"] = path
	}
	if wherePath != "" {
		aggregate["where"] = map[string]any{"path": wherePath, "equals": whereEquals}
	}
	return wsColumnDef{
		Column: column, Label: label, LogicalType: logical, OccurrenceID: occurrence,
		Source: map[string]any{"kind": "aggregate", "aggregate": aggregate},
		Table:  wsTable{Visible: true, Order: order},
	}
}

func extensionCol(column, occurrence, matchURL string, order int) wsColumnDef {
	return wsColumnDef{
		Column: column, Label: column, LogicalType: "string", OccurrenceID: occurrence,
		Source: map[string]any{"kind": "extensionByUrl", "lookup": map[string]any{
			"match": matchURL, "path": "extension[]", "projectionMode": "FIRST",
		}},
		Table: wsTable{Visible: true, Order: order},
	}
}

func observationChildren() []wsChild {
	return []wsChild{
		{OccurrenceID: "observation", ResourceType: "Observation", Relationship: "subject_Patient"},
		{OccurrenceID: "specimen", ResourceType: "Specimen", Relationship: "subject_Patient"},
		{OccurrenceID: "condition", ResourceType: "Condition", Relationship: "subject_Patient"},
	}
}

func patientWorkspace(outputID string, columns []wsColumnDef, children []wsChild, filters []wsFixed) []byte {
	if children == nil {
		children = []wsChild{}
	}
	w := wsWorkspaceDoc{
		APIVersion: wsAPIVersion, Kind: wsKind, SemanticsVersion: wsSemanticsVersion,
		Explorer: wsExplorer{Title: "Backend benchmark battery"},
		Documents: []wsDoc{{
			Kind:             wsDocKind,
			Output:           wsOutputDef{ID: outputID, Title: outputID, RowLabel: "row"},
			RootResourceType: "Patient",
			Route:            wsRouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: children},
			Columns:          columns,
			FixedFilters:     filters,
		}},
		Tabs: []wsTabDef{{ID: outputID + "_tab", Title: outputID, OutputID: outputID, Order: 0, Visible: true}},
	}
	raw, err := json.MarshalIndent(w, "", " ")
	if err != nil { // value types only; unreachable
		panic(fmt.Sprintf("benchmark: workspace marshal: %v", err))
	}
	return raw
}

// BuildWorkspace returns the authored workspace JSON for one workload.
// Output ID equals workload ID: receipts, previews, gates, and samples all
// key on it.
func BuildWorkspace(workloadID string) ([]byte, error) {
	switch workloadID {
	case WorkloadFilterActiveGender:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			fieldCol("patient_id", "base", "id", "VALUE", "string", 0),
			fieldCol("gender", "base", "gender", "VALUE", "string", 1),
			// hidden root filter column: the fixed filter over it keeps
			// inactive patients out of the output
			{Column: "active", Label: "Active", LogicalType: "boolean", OccurrenceID: "base",
				Source: map[string]any{"kind": "field", "field": map[string]any{"path": "active", "projectionMode": "VALUE"}},
				Table:  wsTable{Visible: false, Order: 2}},
		}, nil, []wsFixed{
			{Column: "gender", Values: []string{"female"}},
			{Column: "active", Values: []string{"true"}},
		}), nil
	case WorkloadGroupCountByPatient:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			fieldCol("patient_id", "base", "id", "VALUE", "string", 0),
			aggregateCol("obs_count", "Observation count", "integer", "observation", "COUNT", "", "", "", 1),
		}, observationChildren(), nil), nil
	case WorkloadAggregateSumAgeByPatient:
		ageWhere := []struct{ col, op, logical string }{
			{"sum_age", "SUM", "decimal"}, {"count_age", "COUNT", "integer"},
			{"min_age", "MIN", "decimal"}, {"max_age", "MAX", "decimal"},
		}
		columns := []wsColumnDef{fieldCol("patient_id", "base", "id", "VALUE", "string", 0)}
		for i, agg := range ageWhere {
			columns = append(columns, aggregateCol(agg.col, agg.col, agg.logical, "observation", agg.op,
				"valueQuantity.value", "code.coding[].code", ObservationCodeAge, i+1))
		}
		return patientWorkspace(OutputIDFor(workloadID), columns, observationChildren(), nil), nil
	case WorkloadStageCounts:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			aggregateCol("stage_code", "Stage code", "string", "observation", "DISTINCT_VALUES",
				"valueCodeableConcept.coding[].code", "code.coding[].code", ObservationCodeStage, 0),
			aggregateCol("stage_count", "Stage count", "integer", "observation", "COUNT", "",
				"code.coding[].code", ObservationCodeStage, 1),
		}, observationChildren(), nil), nil
	case WorkloadSpecimenTypeFilter:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			aggregateCol("specimen_id", "Specimen id", "string", "specimen", "DISTINCT_VALUES",
				"id", "type.coding[].code", SpecimenTypePrimary, 0),
			fieldCol("patient_id", "base", "id", "VALUE", "string", 1),
			aggregateCol("type_code", "Specimen type", "string", "specimen", "DISTINCT_VALUES",
				"type.coding[].code", "", "", 2),
		}, observationChildren(), nil), nil
	case WorkloadConditionNullHandling:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			aggregateCol("condition_id", "Condition id", "string", "condition", "DISTINCT_VALUES", "id", "", "", 0),
			aggregateCol("verification_status", "Verification status", "string", "condition", "DISTINCT_VALUES",
				"verificationStatus.coding[].code", "", "", 1),
		}, observationChildren(), nil), nil
	case WorkloadSortLimitTopN:
		// Engine order is not comparable across backends; the workload is a
		// full active-row pass compared as a bag, and the oracle checks the
		// deterministic total order client-side. Sort knobs stay a diagnostic.
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			fieldCol("family", "base", "name[].family", "VALUE", "string", 0),
			fieldCol("given", "base", "name[].given[]", "VALUE", "string", 1),
			fieldCol("patient_id", "base", "id", "VALUE", "string", 2),
		}, nil, nil), nil
	case WorkloadDistinctFamily:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			aggregateCol("family", "Family", "string", "base", "DISTINCT_VALUES", "name[].family", "", "", 0),
		}, nil, nil), nil
	case WorkloadStudyCorrelation:
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			fieldCol("patient_id", "base", "id", "VALUE", "string", 0),
			extensionCol("study_ref", "base", partOfStudyURL, 1),
		}, nil, nil), nil
	case WorkloadDeriveAgeBand:
		// No acceptance precedent for value transformations in the workspace
		// wire shape: authored best-effort, decoder verdict decides; on
		// rejection the workload ships planned/not_run with this reason.
		return patientWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			aggregateCol("observation_id", "Observation id", "string", "observation", "DISTINCT_VALUES",
				"id", "code.coding[].code", ObservationCodeAge, 0),
			{Column: "age_band", Label: "Age band", LogicalType: "string", OccurrenceID: "observation",
				Source: map[string]any{"kind": "aggregate", "aggregate": map[string]any{
					"operation": "DISTINCT_VALUES", "path": "valueQuantity.value",
					"where": map[string]any{"path": "code.coding[].code", "equals": ObservationCodeAge},
				}},
				Table: wsTable{Visible: true, Order: 1}},
		}, observationChildren(), nil), nil
	}
	return nil, fmt.Errorf("benchmark: no workspace template for workload %q", workloadID)
}

// BuildEmptyFloorWorkspace authors the identical-request empty-result floor
// control: the same shape as filter-active-gender whose fixed filter cannot
// match any patient, so the expectation is provably zero rows.
func BuildEmptyFloorWorkspace() ([]byte, error) {
	return patientWorkspace("empty_floor", []wsColumnDef{
		fieldCol("patient_id", "base", "id", "VALUE", "string", 0),
		fieldCol("gender", "base", "gender", "VALUE", "string", 1),
	}, nil, []wsFixed{
		{Column: "gender", Values: []string{"__bench_impossible_gender_value__"}},
	}), nil
}

// AuthoringVerification asks the repository's own decoder about every
// battery workspace. It runs before any service is contacted and feeds the
// report's arm plan: rejected shapes are planned/not_run, named.
func AuthoringVerification() map[string]AuthoringStatus {
	out := map[string]AuthoringStatus{}
	for _, spec := range Battery {
		raw, err := BuildWorkspace(spec.ID)
		if err != nil {
			out[spec.ID] = AuthoringRejected
			continue
		}
		if _, err := authoringv2.DecodeWorkspace(raw); err != nil {
			out[spec.ID] = AuthoringRejected
			continue
		}
		if unverifiedWorkloads[spec.ID] {
			out[spec.ID] = AuthoringUnverified
			continue
		}
		out[spec.ID] = AuthoringProven
	}
	return out
}

// unverifiedWorkloads lists shapes without acceptance-fixture precedent;
// rehearsal classifies them and the outcome is recorded in the report.
var unverifiedWorkloads = map[string]bool{
	WorkloadFilterActiveGender: true,  // fixedFilters over a boolean field column
	WorkloadDistinctFamily:     true,  // DISTINCT_VALUES at the root occurrence
	WorkloadDeriveAgeBand:      true,  // transformation intent approximated
	WorkloadStudyCorrelation:   true,  // extensionByUrl returning a reference string
	WorkloadStageCounts:        true,  // DISTINCT_VALUES + COUNT pair at one occurrence (per-row grain expectation)
	WorkloadSortLimitTopN:      false, // field-only columns: proven shape
}

// BuildAllWorkspaces returns every battery workspace keyed by workload id.
func BuildAllWorkspaces() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, spec := range Battery {
		raw, err := BuildWorkspace(spec.ID)
		if err != nil {
			return nil, err
		}
		out[spec.ID] = raw
	}
	return out, nil
}

// batterySpec resolves one workload's contract.
func batterySpec(workloadID string) (WorkloadSpec, bool) {
	for _, spec := range Battery {
		if spec.ID == workloadID {
			return spec, true
		}
	}
	return WorkloadSpec{}, false
}
