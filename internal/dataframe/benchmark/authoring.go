package benchmark

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	Column              string         `json:"column"`
	Label               string         `json:"label"`
	LogicalType         string         `json:"logicalType"`
	OccurrenceID        string         `json:"occurrenceId"`
	Source              map[string]any `json:"source"`
	ValueTransformation map[string]any `json:"valueTransformation,omitempty"`
	Table               wsTable        `json:"table"`
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

// identifierCol projects identifier[].value where system == matchSystem via
// the acceptance-proven identifierBySystem source shape.
func identifierCol(column, occurrence, matchSystem string, order int) wsColumnDef {
	return wsColumnDef{
		Column: column, Label: column, LogicalType: "string", OccurrenceID: occurrence,
		Source: map[string]any{"kind": "identifierBySystem", "lookup": map[string]any{
			"match": matchSystem, "path": "identifier[]", "projectionMode": "FIRST",
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
	return rootWorkspace("Patient", outputID, columns, children, filters)
}

// observationRootWorkspace authors an output whose rows are the Observation
// resources themselves (one row per observation, no child occurrences).
func observationRootWorkspace(outputID string, columns []wsColumnDef, filters []wsFixed) []byte {
	return rootWorkspace("Observation", outputID, columns, nil, filters)
}

func rootWorkspace(rootType, outputID string, columns []wsColumnDef, children []wsChild, filters []wsFixed) []byte {
	if children == nil {
		children = []wsChild{}
	}
	w := wsWorkspaceDoc{
		APIVersion: wsAPIVersion, Kind: wsKind, SemanticsVersion: wsSemanticsVersion,
		Explorer: wsExplorer{Title: "Backend benchmark battery"},
		Documents: []wsDoc{{
			Kind:             wsDocKind,
			Output:           wsOutputDef{ID: outputID, Title: outputID, RowLabel: "row"},
			RootResourceType: rootType,
			Route:            wsRouteNode{OccurrenceID: "base", ResourceType: rootType, Children: children},
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
		// Derivation intent, authored with the repository's closed value
		// transformation: age_band recodes the scalar-string age identifier
		// (fixtures.go) through the exhaustive exact-category map generated
		// from the same AgeBandFor function the oracle thresholds
		// independently. unknownPolicy ERROR: any value outside the pinned
		// fixture domain is a contract violation, never silent misbanding.
		// The fixed filter keeps exactly the age observations (missing-value
		// observations have no identifier, so their null band is excluded
		// and the oracle skips them too). The map itself has no
		// acceptance-fixture precedent, so the workload stays
		// AuthoringUnverified — planned/not_run with a named reason — until
		// the ClickHouse-lane rehearsal; TestDeriveAgeBandOfflineWitness
		// proves offline that its output bag equals the oracle's.
		ageBandCol := identifierCol("age_band", "base", BenchAgeIdentifierSystem, 1)
		ageBandCol.ValueTransformation = map[string]any{
			"kind": "EXACT_CATEGORY_RECODE",
			"exactCategoryRecode": map[string]any{
				"mappings":      ageRecodeMappings(),
				"unknownPolicy": "ERROR",
			},
		}
		return observationRootWorkspace(OutputIDFor(workloadID), []wsColumnDef{
			fieldCol("observation_id", "base", "id", "VALUE", "string", 0),
			ageBandCol,
		}, []wsFixed{
			{Column: "age_band", Values: []string{"young", "middle", "senior"}},
		}), nil
	}
	return nil, fmt.Errorf("benchmark: no workspace template for workload %q", workloadID)
}

// ageRecodeMappings enumerates the exhaustive exact-category map over the
// pinned fixture domain [1, AgeBandFixtureMax], deriving every label from
// AgeBandFor — the same function the oracle thresholds independently. A
// drift between workspace and oracle is unrepresentable by construction;
// TestDeriveAgeBandOfflineWitness re-checks coverage against the fixtures.
func ageRecodeMappings() []map[string]string {
	mappings := make([]map[string]string, 0, AgeBandFixtureMax)
	for v := 1; v <= AgeBandFixtureMax; v++ {
		mappings = append(mappings, map[string]string{
			"from": strconv.Itoa(v), "to": AgeBandFor(float64(v)),
		})
	}
	return mappings
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
// Anything not proven ships planned/not_run (live battery law below), so
// the live legs can never fail a gate a shape could not pass by
// construction (review r4559430560).
var unverifiedWorkloads = map[string]bool{
	WorkloadFilterActiveGender: true, // fixedFilters over a boolean field column
	WorkloadDistinctFamily:     true, // DISTINCT_VALUES at the root occurrence
	WorkloadDeriveAgeBand:      true, // EXACT_CATEGORY_RECODE map: no acceptance precedent (passability witnessed offline)
	WorkloadStudyCorrelation:   true, // extensionByUrl returning a reference string
	WorkloadStageCounts:        true, // DISTINCT_VALUES + COUNT pair at one occurrence (per-row grain expectation)
	WorkloadSortLimitTopN:      true, // VALUE over multi-valued name[].family: row expansion unproven (review r4559430560)
}

// workloadNotRunReasons names why each not-proven workload is excluded
// from live legs. Every non-proven verdict must carry an entry: an
// exclusion without a named reason is a silent coverage hole, which is
// the exact deception class the report verifier exists to refuse.
var workloadNotRunReasons = map[string]string{
	WorkloadFilterActiveGender: "shape has no acceptance-fixture precedent: fixedFilters over a boolean field column; live rehearsal (ClickHouse lane) must prove the oracle/output agreement before this leg runs",
	WorkloadDistinctFamily:     "shape has no acceptance-fixture precedent: DISTINCT_VALUES at the root occurrence; live rehearsal must prove the oracle/output agreement before this leg runs",
	WorkloadDeriveAgeBand:      "shape has no acceptance-fixture precedent: the EXACT_CATEGORY_RECODE map (identifierBySystem source, ERROR unknown policy, fixed filter on the derived column) has never run end to end; TestDeriveAgeBandOfflineWitness proves offline that its output bag equals the independently thresholded oracle over the pinned fixture domain [1,90], so the workload can pass; the ClickHouse-lane rehearsal must confirm the backends apply recode+filter as specified before this leg runs",
	WorkloadStudyCorrelation:   "shape has no acceptance-fixture precedent: extensionByUrl returning a reference string; live rehearsal must prove the oracle/output agreement before this leg runs",
	WorkloadStageCounts:        "shape has no acceptance-fixture precedent: DISTINCT_VALUES + COUNT pair at one occurrence with a per-row grain expectation; live rehearsal must prove the oracle/output agreement before this leg runs",
	WorkloadSortLimitTopN:      "oracle/output agreement unproven: VALUE columns over multi-valued name[].family with a second family entry on every 4th patient; the oracle assumes first-only while preview row expansion (first-only vs row-per-value) has no acceptance precedent; live rehearsal must pin the scalarization contract",
}

// LiveBatteryExclusions returns the workloads that may not run in a live
// leg and the named reason for each: anything the decoder rejects, and
// anything without an acceptance-proven shape or disproven oracle/output
// agreement. The driver skips authoring, publication, preview, and timing
// for these; the report verifier requires one disclosure row per excluded
// workload per running leg and refuses any timing evidence for them.
func LiveBatteryExclusions() map[string]string {
	out := map[string]string{}
	for workloadID, status := range AuthoringVerification() {
		if status == AuthoringProven {
			continue
		}
		reason := workloadNotRunReasons[workloadID]
		if reason == "" {
			// Fail closed in the disclosure itself: an unproven verdict
			// with no reason is a bug, not a licence to run silently.
			reason = "verdict " + string(status) + " without a named reason (harness bug; refusing to run it)"
		}
		out[workloadID] = reason
	}
	return out
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
