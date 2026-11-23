package lifecycle

import (
	"context"
	"errors"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/rowsource"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func fs1r6LaneCode(err error) string {
	if err == nil {
		return ""
	}
	var le *ports.Error
	if errors.As(err, &le) {
		return le.Code
	}
	return "untyped:" + err.Error()
}

func fs1r6Projection(columnID, ownerStepID, fieldPath, logicalType string) ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: fieldPath, FHIRType: logicalType, LogicalType: logicalType, Label: "BodyStructure." + fieldPath,
	}
}

func TestFS1SignedSlotCollisionPredicateIsOrderInvariant(t *testing.T) {
	doc := filterSourceDocument()
	step := func(id string, kind ws.ConstructionOperationKind) ws.ConstructionStep {
		return ws.ConstructionStep{ID: id, Operation: ws.ConstructionOperation{Kind: kind}}
	}
	newSlot := step("filter-new", ws.ConstructionOperationFilter)
	carrier := step("derive-carrier", ws.ConstructionOperationDerive)
	pivot := step("pivot-one", ws.ConstructionOperationPivot)

	carrierPair := []ws.ConstructionSourceProjection{
		fs1r6Projection("slot_a", "derive-carrier", filterSourceField, "boolean"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}
	pivotPair := []ws.ConstructionSourceProjection{
		fs1r6Projection("pivot_input", "pivot-one", filterSourceField, "boolean"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}
	distinct := []ws.ConstructionSourceProjection{
		fs1r6Projection("slot_a", "derive-carrier", "description", "string"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}

	for _, row := range []struct {
		name        string
		steps       []ws.ConstructionStep
		projections []ws.ConstructionSourceProjection
		slot        string
		want        bool
	}{
		{"carrier forward [carrier, new]", []ws.ConstructionStep{carrier, newSlot}, carrierPair, "slot_b", true},
		{"carrier reverse [new, carrier]", []ws.ConstructionStep{newSlot, carrier}, carrierPair, "slot_b", true},
		{"carrier reverse checked from the other slot", []ws.ConstructionStep{newSlot, carrier}, carrierPair, "slot_a", true},
		{"pivot-first vs pivot-owned field", []ws.ConstructionStep{pivot, newSlot}, pivotPair, "slot_b", false},
		{"signed-first vs pivot-owned field", []ws.ConstructionStep{newSlot, pivot}, pivotPair, "slot_b", false},
		{"distinct field reverse", []ws.ConstructionStep{newSlot, carrier}, distinct, "slot_b", false},
	} {
		if got := constructionRootFieldAlreadyProjected(doc, row.steps, row.projections, row.slot, filterSourceField); got != row.want {
			t.Errorf("%s: constructionRootFieldAlreadyProjected = %v, want %v (set comparison must not depend on arrival order)", row.name, got, row.want)
		}
	}
}

func TestFS1ReverseOrderSignedSlotRefusedAtPivotBoundary(t *testing.T) {
	buildBase := func(t *testing.T, token string) (*Service, constructionBase, capability.Snapshot) {
		t.Helper()
		snapshot := filterSourceSnapshot(token)
		service := filterSourceService(t)
		baseConstruction := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps:   []ws.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn)},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		return service, filterSourceBase(snapshot, baseConstruction), snapshot
	}

	t.Run("filter lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-filter-boundary")
		candidate := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
				filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
			filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
		})
		_, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate)
		if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
			t.Fatalf("pivot-first signed filter slot on the pivot's field: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE refusal (never silent accept, never catch-all 500)", code, err)
		}
	})

	t.Run("unpivot melt lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-melt-boundary")
		melt := ws.ConstructionStep{
			ID:     "melt-step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
				ConstructionID:    "melt-step",
				Inputs:            []ws.ConstructionUnpivotInput{{ColumnID: "melt_slot", Key: unpivotKey("v")}},
				KeyOutputColumnID: "melt_key", ValueOutputColumnID: "melt_value",
				NullRowPolicy: ws.ConstructionUnpivotDrop,
			}},
			Outputs: []ws.StageColumn{
				{ID: "melt_slot", Name: ws.ConstructionSourceProjectionName("melt_slot"), Label: "Active", Type: "boolean"},
				{ID: "melt_key", Name: "melt_key", Label: "Melt key", Type: "string"},
				{ID: "melt_value", Name: "melt_value", Label: "Melt value", Type: "boolean"},
			},
		}
		candidate := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn), melt,
			},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := construction.ConstructionProposalRequest{
			Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft",
			OutputID: filterSourceOutput, ChangedStepID: "melt-step",
			UnpivotSources: []rowsource.ConstructionUnpivotSourceSelection{{ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID, ColumnID: "melt_slot"}},
		}
		_, err := service.constructionCandidateWithUnpivotSources(context.Background(), base, request, candidate)
		if code := fs1r6LaneCode(err); code != "UNPIVOT_SOURCE_CANNOT_CROSS_STAGE" {
			t.Fatalf("pivot-first signed melt input on the pivot's field: code=%q err=%v, want UNPIVOT_SOURCE_CANNOT_CROSS_STAGE refusal", code, err)
		}
	})
}

func TestFS1ForwardAndReverseSignedCarrierCollisionRefusedNamed(t *testing.T) {
	for _, row := range []struct {
		order string
		first bool
	}{
		{"carrier-first", true},
		{"slot-first", false},
	} {
		t.Run(row.order, func(t *testing.T) {
			snapshot := filterSourceSnapshot("fs1r6-named-" + row.order)
			service := filterSourceService(t)
			carrier := ws.ConstructionStep{
				ID:        "derive-carrier",
				Inputs:    []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive},
				Outputs: []ws.StageColumn{
					{ID: "derive_slot_a", Name: ws.ConstructionSourceProjectionName("derive_slot_a"), Label: "Active", Type: "boolean"},
				},
			}
			filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
			var steps []ws.ConstructionStep
			if row.first {
				steps = []ws.ConstructionStep{carrier, filter}
			} else {
				steps = []ws.ConstructionStep{filter, carrier}
			}
			projection := fs1r6Projection("derive_slot_a", "derive-carrier", filterSourceField, "boolean")
			base := filterSourceBase(snapshot, ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}})
			candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}}
			request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
				filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
			})
			_, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate)
			if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_FIELD_ALREADY_PROJECTED" {
				t.Fatalf("%s: code=%q err=%v, want the named FILTER_SOURCE_FIELD_ALREADY_PROJECTED refusal in both arrival directions", row.order, code, err)
			}
		})
	}
}

func TestFS1LegitNonCollisionReverseOrderStillAccepted(t *testing.T) {
	snapshot := filterSourceSnapshot("fs1r6-legit")
	service := filterSourceService(t)
	carrier := ws.ConstructionStep{
		ID:        "derive-carrier",
		Inputs:    []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive},
		Outputs: []ws.StageColumn{
			{ID: "derive_slot_a", Name: ws.ConstructionSourceProjectionName("derive_slot_a"), Label: "Description", Type: "string"},
		},
	}
	projection := fs1r6Projection("derive_slot_a", "derive-carrier", "description", "string")
	filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	steps := []ws.ConstructionStep{filter, carrier}
	base := filterSourceBase(snapshot, ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}})
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}}
	request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
		filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
	})
	if _, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate); err != nil {
		t.Fatalf("distinct-field reverse-order signed slot must stay accepted, got %v", err)
	}
}

func fs1r6PivotPatientConstruction() (ws.Document, ws.Construction) {
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID:     "pivot",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"patient-id", "pv_slot"},
				CategoryColumnID: "category", ValueColumnID: "value",
				Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("A"), OutputColumnID: "d"}},
				DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
				MissingCellPolicy:      ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
			Outputs: []ws.StageColumn{
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
				{ID: "pv_slot", Name: ws.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
				{ID: "d", Name: "d", Label: "d", Type: "string"},
			},
		},
	}}
	construction.SourceProjections = []ws.ConstructionSourceProjection{{
		ColumnID: "pv_slot", OwnerStepID: "pivot", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RecordsRowDefinition(),
		Columns: []ws.Column{
			{ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{ColumnID: "category", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}},
			{ColumnID: "value", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}},
		},
		Construction: &construction,
	}
	return document, construction
}

func fs1r6SaveDraftWithPivot(t *testing.T, harness *filterSourceHarness, construction ws.Construction) {
	t.Helper()
	document, _ := fs1r6PivotPatientConstruction()
	document.Construction = &construction
	workspace, err := ws.DecodeWorkspace(harness.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	workspace.Documents = []ws.Document{document}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate fs1r6 pivot draft: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	harness.store.created.DraftConfig, harness.store.created.DraftDigest = draft, digest
}

func fs1r6SignedFilterStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "fs1r6_filter",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: "fs1r6_slot_b", Operator: ws.ConstructionFilterExists,
		}},
		Outputs: []ws.StageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "category", Name: "category", Label: "Category", Type: "string"},
			{ID: "value", Name: "value", Label: "Value", Type: "string"},
			{ID: "fs1r6_slot_b", Name: ws.ConstructionSourceProjectionName("fs1r6_slot_b"), Label: "Patient.multipleBirthInteger", Type: "integer"},
			{ID: "pv_slot", Name: ws.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
		},
	}
}

func fs1r6SignedSlotRequest() rowsource.ConstructionFilterSourceSelection {
	return rowsource.ConstructionFilterSourceSelection{ColumnID: "fs1r6_slot_b"}
}

func TestFS1FullProposeRefusesSignedStepInsertedBeforeCommittedPivot(t *testing.T) {
	harness := newFilterSourceHarness(t)
	_, durable := fs1r6PivotPatientConstruction()
	fs1r6SaveDraftWithPivot(t, harness, durable)
	candidate := durable
	candidate.Steps = append([]ws.ConstructionStep{fs1r6SignedFilterStep()}, durable.Steps...)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	_, err := harness.service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []rowsource.ConstructionFilterSourceSelection{selection},
	})
	if code := fs1r6LaneCode(err); code != "INVALID_CONSTRUCTION_CANDIDATE" {
		t.Fatalf("signed step inserted before the committed pivot: code=%q err=%v, want INVALID_CONSTRUCTION_CANDIDATE (reverse arrival unreachable by construction)", code, err)
	}
}

func TestFS1FullProposeReverseDirectionRefusedAtBoundary(t *testing.T) {
	harness := newFilterSourceHarness(t)
	_, durable := fs1r6PivotPatientConstruction()
	fs1r6SaveDraftWithPivot(t, harness, durable)
	candidate := durable
	candidate.Steps = append([]ws.ConstructionStep(nil), durable.Steps...)
	signed := fs1r6SignedFilterStep()
	signed.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot"}}
	candidate.Steps = append(candidate.Steps, signed)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	proposal, err := harness.service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []rowsource.ConstructionFilterSourceSelection{selection},
	})
	if err == nil {
		t.Fatalf("pivot-first signed slot was accepted (status=%q, %d projections): the collision must be refused as FILTER_SOURCE_CANNOT_CROSS_STAGE at the lane boundary",
			proposal.PreviewStatus, len(proposal.CandidateConstruction.SourceProjections))
	}
	if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("pivot-first full-path refusal: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE", code, err)
	}
}
