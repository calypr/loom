package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func fs1r6LaneCode(err error) string {
	if err == nil {
		return ""
	}
	var le *Error
	if errors.As(err, &le) {
		return le.Code
	}
	return "untyped:" + err.Error()
}

func fs1r6Projection(columnID, ownerStepID, fieldPath, logicalType string) authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: fieldPath, FHIRType: logicalType, LogicalType: logicalType, Label: "BodyStructure." + fieldPath,
	}
}

func TestFS1SignedSlotCollisionPredicateIsOrderInvariant(t *testing.T) {
	doc := filterSourceDocument()
	step := func(id string, kind authoringv2.ConstructionOperationKind) authoringv2.ConstructionStep {
		return authoringv2.ConstructionStep{ID: id, Operation: authoringv2.ConstructionOperation{Kind: kind}}
	}
	newSlot := step("filter-new", authoringv2.ConstructionOperationFilter)
	carrier := step("derive-carrier", authoringv2.ConstructionOperationDerive)
	pivot := step("pivot-one", authoringv2.ConstructionOperationPivot)

	carrierPair := []authoringv2.ConstructionSourceProjection{
		fs1r6Projection("slot_a", "derive-carrier", filterSourceField, "boolean"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}
	pivotPair := []authoringv2.ConstructionSourceProjection{
		fs1r6Projection("pivot_input", "pivot-one", filterSourceField, "boolean"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}
	distinct := []authoringv2.ConstructionSourceProjection{
		fs1r6Projection("slot_a", "derive-carrier", "description", "string"),
		fs1r6Projection("slot_b", "filter-new", filterSourceField, "boolean"),
	}

	for _, row := range []struct {
		name        string
		steps       []authoringv2.ConstructionStep
		projections []authoringv2.ConstructionSourceProjection
		slot        string
		want        bool
	}{
		{"carrier forward [carrier, new]", []authoringv2.ConstructionStep{carrier, newSlot}, carrierPair, "slot_b", true},
		{"carrier reverse [new, carrier]", []authoringv2.ConstructionStep{newSlot, carrier}, carrierPair, "slot_b", true},
		{"carrier reverse checked from the other slot", []authoringv2.ConstructionStep{newSlot, carrier}, carrierPair, "slot_a", true},
		{"pivot-first vs pivot-owned field", []authoringv2.ConstructionStep{pivot, newSlot}, pivotPair, "slot_b", false},
		{"signed-first vs pivot-owned field", []authoringv2.ConstructionStep{newSlot, pivot}, pivotPair, "slot_b", false},
		{"distinct field reverse", []authoringv2.ConstructionStep{newSlot, carrier}, distinct, "slot_b", false},
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
		baseConstruction := authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			Steps:   []authoringv2.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn)},
			SourceProjections: []authoringv2.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		return service, filterSourceBase(snapshot, baseConstruction), snapshot
	}

	t.Run("filter lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-filter-boundary")
		candidate := authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			Steps: []authoringv2.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
				filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			},
			SourceProjections: []authoringv2.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{
			filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
		})
		_, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate)
		if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
			t.Fatalf("pivot-first signed filter slot on the pivot's field: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE refusal (never silent accept, never catch-all 500)", code, err)
		}
	})

	t.Run("unpivot melt lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-melt-boundary")
		melt := authoringv2.ConstructionStep{
			ID:     "melt-step",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
				ConstructionID:    "melt-step",
				Inputs:            []authoringv2.ConstructionUnpivotInput{{ColumnID: "melt_slot", Key: unpivotKey("v")}},
				KeyOutputColumnID: "melt_key", ValueOutputColumnID: "melt_value",
				NullRowPolicy: authoringv2.ConstructionUnpivotDrop,
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "melt_slot", Name: authoringv2.ConstructionSourceProjectionName("melt_slot"), Label: "Active", Type: "boolean"},
				{ID: "melt_key", Name: "melt_key", Label: "Melt key", Type: "string"},
				{ID: "melt_value", Name: "melt_value", Label: "Melt value", Type: "boolean"},
			},
		}
		candidate := authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			Steps: []authoringv2.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn), melt,
			},
			SourceProjections: []authoringv2.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := ConstructionProposalRequest{
			Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft",
			OutputID: filterSourceOutput, ChangedStepID: "melt-step",
			UnpivotSources: []ConstructionUnpivotSourceSelection{{ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID, ColumnID: "melt_slot"}},
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
			carrier := authoringv2.ConstructionStep{
				ID:        "derive-carrier",
				Inputs:    []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive},
				Outputs: []authoringv2.StageColumn{
					{ID: "derive_slot_a", Name: authoringv2.ConstructionSourceProjectionName("derive_slot_a"), Label: "Active", Type: "boolean"},
				},
			}
			filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
			var steps []authoringv2.ConstructionStep
			if row.first {
				steps = []authoringv2.ConstructionStep{carrier, filter}
			} else {
				steps = []authoringv2.ConstructionStep{filter, carrier}
			}
			projection := fs1r6Projection("derive_slot_a", "derive-carrier", filterSourceField, "boolean")
			base := filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps, SourceProjections: []authoringv2.ConstructionSourceProjection{projection}})
			candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps, SourceProjections: []authoringv2.ConstructionSourceProjection{projection}}
			request := filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{
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
	carrier := authoringv2.ConstructionStep{
		ID:        "derive-carrier",
		Inputs:    []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive},
		Outputs: []authoringv2.StageColumn{
			{ID: "derive_slot_a", Name: authoringv2.ConstructionSourceProjectionName("derive_slot_a"), Label: "Description", Type: "string"},
		},
	}
	projection := fs1r6Projection("derive_slot_a", "derive-carrier", "description", "string")
	filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	steps := []authoringv2.ConstructionStep{filter, carrier}
	base := filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps, SourceProjections: []authoringv2.ConstructionSourceProjection{projection}})
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps, SourceProjections: []authoringv2.ConstructionSourceProjection{projection}}
	request := filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{
		filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
	})
	if _, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate); err != nil {
		t.Fatalf("distinct-field reverse-order signed slot must stay accepted, got %v", err)
	}
}

func fs1r6PivotPatientConstruction() (authoringv2.Document, authoringv2.Construction) {
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID:     "pivot",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"patient-id", "pv_slot"},
				CategoryColumnID: "category", ValueColumnID: "value",
				Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("A"), OutputColumnID: "d"}},
				DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
				MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
				{ID: "pv_slot", Name: authoringv2.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
				{ID: "d", Name: "d", Label: "d", Type: "string"},
			},
		},
	}}
	construction.SourceProjections = []authoringv2.ConstructionSourceProjection{{
		ColumnID: "pv_slot", OwnerStepID: "pivot", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}}
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{
			{ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{ColumnID: "category", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
			{ColumnID: "value", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
		},
		Construction: &construction,
	}
	return document, construction
}

func fs1r6SaveDraftWithPivot(t *testing.T, harness *filterSourceHarness, construction authoringv2.Construction) {
	t.Helper()
	document, _ := fs1r6PivotPatientConstruction()
	document.Construction = &construction
	workspace, err := authoringv2.DecodeWorkspace(harness.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	workspace.Documents = []authoringv2.Document{document}
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

func fs1r6SignedFilterStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     "fs1r6_filter",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: "fs1r6_slot_b", Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "category", Name: "category", Label: "Category", Type: "string"},
			{ID: "value", Name: "value", Label: "Value", Type: "string"},
			{ID: "fs1r6_slot_b", Name: authoringv2.ConstructionSourceProjectionName("fs1r6_slot_b"), Label: "Patient.multipleBirthInteger", Type: "integer"},
			{ID: "pv_slot", Name: authoringv2.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
		},
	}
}

func fs1r6SignedSlotRequest() ConstructionFilterSourceSelection {
	return ConstructionFilterSourceSelection{ColumnID: "fs1r6_slot_b"}
}

func TestFS1FullProposeRefusesSignedStepInsertedBeforeCommittedPivot(t *testing.T) {
	harness := newFilterSourceHarness(t)
	_, durable := fs1r6PivotPatientConstruction()
	fs1r6SaveDraftWithPivot(t, harness, durable)
	candidate := durable
	candidate.Steps = append([]authoringv2.ConstructionStep{fs1r6SignedFilterStep()}, durable.Steps...)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	_, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []ConstructionFilterSourceSelection{selection},
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
	candidate.Steps = append([]authoringv2.ConstructionStep(nil), durable.Steps...)
	signed := fs1r6SignedFilterStep()
	signed.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "pivot"}}
	candidate.Steps = append(candidate.Steps, signed)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []ConstructionFilterSourceSelection{selection},
	})
	if err == nil {
		t.Fatalf("pivot-first signed slot was accepted (status=%q, %d projections): the collision must be refused as FILTER_SOURCE_CANNOT_CROSS_STAGE at the lane boundary",
			proposal.PreviewStatus, len(proposal.CandidateConstruction.SourceProjections))
	}
	if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("pivot-first full-path refusal: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE", code, err)
	}
}
