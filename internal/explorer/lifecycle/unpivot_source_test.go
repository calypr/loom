package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	unpivotSourceStep      = "unpivot-birth"
	unpivotSourceBSColumn  = "unpivot_source_active"
	unpivotPatientColumn   = "unpivot_source_birth"
	unpivotMeltKeyColumn   = "melt_key"
	unpivotMeltValueColumn = "melt_value"
)

func unpivotKey(value string) authoringv2.TableScalar {
	kind := authoringv2.TableScalarString
	return authoringv2.TableScalar{Kind: kind, String: &value}
}

func unpivotMeltStep(stepID string, inputIDs []string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	inputs := make([]authoringv2.ConstructionUnpivotInput, 0, len(inputIDs))
	for index, id := range inputIDs {
		inputs = append(inputs, authoringv2.ConstructionUnpivotInput{ColumnID: id, Key: unpivotKey("variant-" + string(rune('a'+index)))})
	}
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
			ConstructionID: stepID, Inputs: inputs,
			KeyOutputColumnID: unpivotMeltKeyColumn, ValueOutputColumnID: unpivotMeltValueColumn,
			NullRowPolicy: authoringv2.ConstructionUnpivotDrop,
		}},
		Outputs: outputs,
	}
}

func unpivotMeltOutputs(extra ...authoringv2.StageColumn) []authoringv2.StageColumn {
	outputs := append(append([]authoringv2.StageColumn(nil), extra...),
		authoringv2.StageColumn{ID: unpivotMeltKeyColumn, Name: unpivotMeltKeyColumn, Label: "Melt key", Type: "string"},
		authoringv2.StageColumn{ID: unpivotMeltValueColumn, Name: unpivotMeltValueColumn, Label: "Melt value", Type: "integer"},
	)
	return outputs
}

// unpivotSourceBSSnapshot reuses the BodyStructure fixture where `active`
// (boolean) and `id` (string) are populated scalars, `description` is an
// unpopulated string, and `patient` is a reference.
func unpivotSourceBSSnapshot(token string) capability.Snapshot {
	return deriveSourceSnapshot(token)
}

func unpivotSourceEmptySnapshot(token string) capability.Snapshot {
	snapshot := deriveSourceSnapshot(token)
	snapshot.Candidates = nil
	return snapshot
}

type unpivotSourceHarness struct {
	*filterSourceHarness
}

func newUnpivotSourceHarness(t *testing.T) *unpivotSourceHarness {
	t.Helper()
	return &unpivotSourceHarness{filterSourceHarness: newFilterSourceHarness(t)}
}

func unpivotPatientProposalRequest(changedStepID string, sources []ConstructionUnpivotSourceSelection) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "unpivot-source-patient-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:unpivot-source-patient-draft", OutputID: filterSourceOutput,
		ChangedStepID: changedStepID, UnpivotSources: sources,
	}
}

func unpivotBSSelections(t *testing.T, snapshot capability.Snapshot, path, columnID string) ConstructionUnpivotSourceSelection {
	t.Helper()
	return ConstructionUnpivotSourceSelection{ChoiceID: deriveSourceRowChoice(t, snapshot, deriveSourceRootType, path).ChoiceID, ColumnID: columnID}
}

// (a) Discovery offers populated root scalars of the four compiler melt kinds
// and never the unpopulated or reference fields.
func TestConstructionUnpivotSourceCapabilityExposesPopulatedUnselectedRootScalars(t *testing.T) {
	snapshot := unpivotSourceBSSnapshot("unpivot-source-bs")
	service := deriveSourceService(t)
	result, err := service.constructionUnpivotSourceCapability(context.Background(),
		deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), deriveSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Supported || result.ReasonCode != "" || result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("unpivot source capability = %#v, want a supported source-stage capability", result)
	}
	byPath := make(map[string]ConstructionUnpivotSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	for _, want := range []struct{ path, logicalType string }{{"active", "boolean"}, {"id", "string"}} {
		choice, found := byPath[want.path]
		if !found {
			t.Fatalf("populated unselected root scalar %q was not offered: %#v", want.path, result.Choices)
		}
		if choice.LogicalType != want.logicalType || !choice.IsPopulated || choice.ColumnID != "" {
			t.Fatalf("offered choice %q facts = %#v, want populated %s with no durable column yet", want.path, choice, want.logicalType)
		}
	}
	if _, offered := byPath["description"]; offered {
		t.Fatalf("unpopulated field was offered as unpivot source data: %#v", byPath["description"])
	}
	if _, offered := byPath["patient"]; offered {
		t.Fatalf("reference field was offered as unpivot source data: %#v", byPath["patient"])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"supported":true`, `"stageId":"source_projection"`, `"choiceId":"rc1.`, `"fieldPath":"active"`, `"logicalType":"boolean"`, `"isPopulated":true`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("unpivotSourceInput wire shape omitted %s: %s", want, encoded)
		}
	}
}

// Discovery reports the honest reason when no populated melt kind exists.
func TestConstructionUnpivotSourceCapabilityReportsNoEligibleFields(t *testing.T) {
	snapshot := unpivotSourceEmptySnapshot("unpivot-source-empty")
	service := deriveSourceService(t)
	result, err := service.constructionUnpivotSourceCapability(context.Background(),
		deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), deriveSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Supported || result.ReasonCode != "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS" || len(result.Choices) != 0 {
		t.Fatalf("unsupported capability = %#v, want the unpivot-source reason without choices", result)
	}
}

// The kind gate is pure logic and must admit exactly the compiler melt class:
// lower.TableReshapeScalarKind maps STRING/DATE/DATE_TIME/CODE/UUID to the
// STRING physical class, so date, date_time, code, and uuid root scalars are
// legitimate melt value columns (finding F-1).
func TestUnpivotSourceLogicalTypeIsTableScalarAdmitsOnlyCompilerKinds(t *testing.T) {
	for _, want := range []string{"string", "integer", "decimal", "boolean", "date", "date_time", "code", "uuid", " STRING", "Decimal ", "BOOLEAN", " Date "} {
		if !unpivotSourceLogicalTypeIsTableScalar(want) {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"", "number", "object", "datetime", "date-time", "quantity", "url", "null"} {
		if unpivotSourceLogicalTypeIsTableScalar(refuse) {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = true, want false", refuse)
		}
	}
}

// The lane's own admission = compiler delegation minus the date family
// (operator decision #4). The trim+lowercase canonicalization matches the
// compiler's, so no alias reaches an eligible verdict, and every kind the
// compiler refuses stays refused.
func TestUnpivotSourceMeltEligibilityExcludesDateFamily(t *testing.T) {
	for _, want := range []string{"string", "integer", "decimal", "boolean", "code", "uuid", " STRING", "Decimal ", "BOOLEAN"} {
		if !unpivotSourceLogicalTypeIsMeltEligible(want) {
			t.Fatalf("unpivotSourceLogicalTypeIsMeltEligible(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"date", "date_time", " Date ", " DATE_TIME ", "date_time ", "", "number", "object", "datetime", "date-time", "quantity", "url", "null"} {
		if unpivotSourceLogicalTypeIsMeltEligible(refuse) {
			t.Fatalf("unpivotSourceLogicalTypeIsMeltEligible(%q) = true, want false", refuse)
		}
	}
}

// Permanent whitelist-drift guard: the signed channel's melt gate is the
// compiler decision itself. This set-equality table fails if anyone adds a
// local kind set back to either side.
func TestUnpivotSourceWhitelistIsTheCompilerDecision(t *testing.T) {
	corpus := map[string]struct {
		admitted bool
		class    string
	}{
		"string":    {true, "STRING"},
		"date":      {true, "STRING"},
		"date_time": {true, "STRING"},
		"code":      {true, "STRING"},
		"uuid":      {true, "STRING"},
		"integer":   {true, "INTEGER"},
		"decimal":   {true, "DECIMAL"},
		"boolean":   {true, "BOOLEAN"},
		" DATE ":    {true, "STRING"},
		"Number":    {false, ""},
		"object":    {false, ""},
		"datetime":  {false, ""},
		"date-time": {false, ""},
		"null":      {false, ""},
		"":          {false, ""},
	}
	for kind, want := range corpus {
		class, ok := lower.TableReshapeScalarKind(kind)
		if ok != want.admitted || (ok && class != want.class) {
			t.Fatalf("lower.TableReshapeScalarKind(%q) = (%q,%v), want (%q,%v)", kind, class, ok, want.class, want.admitted)
		}
		if got := unpivotSourceLogicalTypeIsTableScalar(kind); got != want.admitted {
			t.Fatalf("unpivotSourceLogicalTypeIsTableScalar(%q) = %v, want %v (channel drifted from the compiler decision)", kind, got, want.admitted)
		}
	}
}

// The joint satisfiability rule is the compiler's own compatibility predicate
// applied to the physical class each logical kind lowers to. An unsatisfiable
// melt must be refused by class, never by silently dropping kinds the channel
// has not yet classified. On top of that, the unpivot lane refuses the date
// family outright (operator decision #4): the compiler maps date and
// date_time to the STRING class, but compiler parity is not consent for this
// channel, so any melt carrying a date-family kind is unsatisfiable.
func TestUnpivotMeltKindsSatisfiableFollowsCompilerRule(t *testing.T) {
	for _, ok := range [][]string{nil, {}, {"integer"}, {"decimal"}, {"integer", "decimal"}, {"decimal", "integer"}, {" string "}, {"boolean"}, {"BOOLEAN", "boolean"}, {"code", "uuid", "string"}, {"Code", " STRING"}} {
		if !unpivotMeltKindsSatisfiable(ok) {
			t.Fatalf("unpivotMeltKindsSatisfiable(%v) = false, want true", ok)
		}
	}
	for _, refuse := range [][]string{{"string", "integer"}, {"string", "decimal"}, {"boolean", "string"}, {"boolean", "decimal"}, {"integer", "boolean"}, {"integer", "date"}, {"decimal", "date_time"}, {"boolean", "code"}, {"string", "integer", "uuid"}, {"date"}, {"date_time"}, {"date", "code", "uuid", "date_time", "string"}, {" Date ", "STRING"}, {"date", "date_time"}, {"string", "date"}} {
		if unpivotMeltKindsSatisfiable(refuse) {
			t.Fatalf("unpivotMeltKindsSatisfiable(%v) = true, want false", refuse)
		}
	}
}

// (c) A token from another snapshot, or a tampered token, is refused.
func TestConstructionUnpivotSourceRefusesForgedAndCrossSnapshotChoices(t *testing.T) {
	snapshot := unpivotSourceBSSnapshot("unpivot-source-current")
	other := unpivotSourceBSSnapshot("unpivot-source-other")
	service := deriveSourceService(t)
	base := deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	request := deriveSourceProposalRequest(unpivotSourceStep, nil)

	foreign := deriveSourceRowChoice(t, other, deriveSourceRootType, "active")
	if _, err := service.resolveConstructionUnpivotSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionUnpivotSourceSelection{ChoiceID: foreign.ChoiceID, ColumnID: unpivotSourceBSColumn}); err == nil {
		t.Fatal("a choice issued against another snapshot was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_UNPIVOT_SOURCE_CHOICE" {
		t.Fatalf("cross-snapshot error = %v, want INVALID_UNPIVOT_SOURCE_CHOICE", err)
	}

	parts := strings.Split(deriveSourceRowChoice(t, snapshot, deriveSourceRootType, "active").ChoiceID, ".")
	if len(parts) != 3 {
		t.Fatalf("signed choice is not a three-part token: %q", parts)
	}
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("0", len(parts[2]))
	if _, err := service.resolveConstructionUnpivotSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionUnpivotSourceSelection{ChoiceID: forged, ColumnID: unpivotSourceBSColumn}); err == nil {
		t.Fatal("a tampered choice token was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_UNPIVOT_SOURCE_CHOICE" {
		t.Fatalf("tampered token error = %v, want INVALID_UNPIVOT_SOURCE_CHOICE", err)
	}

	if _, err := service.resolveConstructionUnpivotSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionUnpivotSourceSelection{ChoiceID: "rc1.not-a-real-token.deadbeef", ColumnID: unpivotSourceBSColumn}); err == nil {
		t.Fatal("an invented choice token was accepted")
	}
}

// The melt-kind gate is enforced on admission, not only display: the planner
// still issues a signed token for a scalar date field, so the resolution
// boundary must refuse it.
func unpivotNumberCandidate() capability.Candidate {
	return capability.Candidate{
		ID: "candidate-deceased-number", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "deceasedBoolean",
		Label: "Deceased", LogicalType: "number", Cardinality: "optional_one",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 3,
	}
}

// A declared number aggregate is a construction source type but the physical
// table compiler refuses it as a melt value column, so admission (and not a
// later compile failure) must refuse it by name.
func TestConstructionUnpivotSourceRejectsNonScalarKindOnAdmission(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates, unpivotNumberCandidate())
	numberChoice := deriveSourceRowChoice(t, snapshot, "Patient", "deceasedBoolean")
	if _, err := capability.DecodeRowChoiceID(numberChoice.ChoiceID); err != nil {
		t.Fatalf("crafted number candidate produced no signed token: %v", err)
	}
	projection, err := harness.service.resolveConstructionUnpivotSource(context.Background(),
		deriveSourcePatientBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
		unpivotPatientProposalRequest(unpivotSourceStep, nil), recipe.ConstructionSourceProjectionID,
		ConstructionUnpivotSourceSelection{ChoiceID: numberChoice.ChoiceID, ColumnID: unpivotPatientColumn})
	if err == nil {
		t.Fatalf("compiler-refused number field was admitted as an unpivot source projection: %#v", projection)
	}
	if code := lifecycleErrorCode(err); code != "UNSUPPORTED_UNPIVOT_SOURCE_FIELD" {
		t.Fatalf("non-melt-kind admission error = %v, want UNSUPPORTED_UNPIVOT_SOURCE_FIELD", err)
	}
}

// Operator decision #4: the unpivot melt lane refuses the date family even
// though the compiler maps it to the STRING class. The planner still issues
// a signed token for a scalar date/date_time field, so admission must refuse
// it by name rather than fabricate an accepted melt the compiler would then
// build with silently lossy date-to-string semantics.
func TestConstructionUnpivotSourceRefusesDateFamilyOnAdmission(t *testing.T) {
	for _, row := range []struct {
		id, fieldPath, label, logicalType string
	}{
		{"candidate-birth-date", "birthDate", "Birth date", "date"},
		{"candidate-deceased-date-time", "deceasedDateTime", "Deceased", "date_time"},
	} {
		harness := newUnpivotSourceHarness(t)
		snapshot := harness.snapshot
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: row.id, NodeID: "node-patient", ResourceType: "Patient", FieldPath: row.fieldPath,
			Label: row.label, LogicalType: row.logicalType, Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		})
		dateChoice := deriveSourceRowChoice(t, snapshot, "Patient", row.fieldPath)
		projection, err := harness.service.resolveConstructionUnpivotSource(context.Background(),
			deriveSourcePatientBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
			unpivotPatientProposalRequest(unpivotSourceStep, nil), recipe.ConstructionSourceProjectionID,
			ConstructionUnpivotSourceSelection{ChoiceID: dateChoice.ChoiceID, ColumnID: unpivotPatientColumn})
		if err == nil {
			t.Fatalf("%s root scalar was admitted as an unpivot source projection: %#v", row.logicalType, projection)
		}
		if code := lifecycleErrorCode(err); code != "UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD" {
			t.Fatalf("%s admission error = %v, want UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD", row.logicalType, err)
		}
	}
}

// Operator decision #4: the discovery list must hide the date family, not
// advertise a field that admission then refuses. Permitted melt kinds stay
// advertised — the integer root scalar is the regression lock on this side.
func TestConstructionUnpivotSourceCapabilityExcludesDateMeltKind(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "candidate-birth-date", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "birthDate",
			Label: "Birth date", LogicalType: "date", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		},
		capability.Candidate{
			ID: "candidate-deceased-date-time", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "deceasedDateTime",
			Label: "Deceased", LogicalType: "date_time", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		})
	result, err := harness.service.constructionUnpivotSourceCapability(context.Background(),
		deriveSourcePatientBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), "patients", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]ConstructionUnpivotSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	for _, hidden := range []string{"birthDate", "deceasedDateTime"} {
		if choice, offered := byPath[hidden]; offered {
			t.Fatalf("discovery advertised a refused date-family melt kind: %#v", choice)
		}
	}
	if _, offered := byPath["multipleBirthInteger"]; !offered {
		t.Fatalf("discovery hid a permitted melt kind alongside the date family: %#v", result.Choices)
	}
}

// (b) A valid selection durably owns exactly one server-derived projection,
// the melt's carrier steps list the slot, and the melt's own outputs do not.
func TestConstructionCandidateWithUnpivotSourcesMaterializesAndCarriesPrefix(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	baseCarriers := append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", baseCarriers)
	meltOutputs := unpivotMeltOutputs(authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"})
	melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"}, meltOutputs)
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
	base := deriveSourcePatientBase(snapshot, construction)
	base.stages = append(base.stages, explorer.ReceiptConstructionStage{
		ID: "derive-base", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "DERIVE",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	})

	clone, err := decodeConstruction(construction)
	if err != nil {
		t.Fatal(err)
	}
	clone.Steps[1].Operation.Unpivot.Inputs = append(clone.Steps[1].Operation.Unpivot.Inputs,
		authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})
	clone.Steps[1].Operation.Unpivot.ValueOutputColumnID = unpivotMeltValueColumn

	prepared, err := service.constructionCandidateWithUnpivotSources(context.Background(), base,
		unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
		}), clone)
	if err != nil {
		t.Fatalf("prepare unpivot sources: %v", err)
	}
	if len(prepared.SourceProjections) != 1 {
		t.Fatalf("prepared projections = %#v, want exactly one unpivot-owned projection", prepared.SourceProjections)
	}
	projection := prepared.SourceProjections[0]
	if projection.ColumnID != unpivotPatientColumn || projection.OwnerStepID != unpivotSourceStep ||
		projection.OccurrenceID != authoringv2.RootOccurrenceID || projection.FieldPath != "multipleBirthInteger" ||
		projection.FHIRType != "integer" || projection.LogicalType != "integer" || projection.Label != "Birth count" {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", projection)
	}
	carried := false
	for _, output := range prepared.Steps[0].Outputs {
		if output.ID == unpivotPatientColumn && output.Name == authoringv2.ConstructionSourceProjectionName(unpivotPatientColumn) && output.Type == "integer" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("carrier step omitted the projection output: %#v", prepared.Steps[0].Outputs)
	}
	for _, output := range prepared.Steps[1].Outputs {
		if output.ID == unpivotPatientColumn {
			t.Fatalf("the melt step must consume, not carry, the projection: %#v", prepared.Steps[1].Outputs)
		}
	}
	document := deriveSourcePatientDocument(prepared)
	analyzed, impact, err := document.AnalyzeConstructionCandidate(prepared, unpivotSourceStep, nil)
	if err != nil {
		t.Fatalf("unpivot-consuming candidate was refused by the real authoring validator: %v", err)
	}
	if impact.HasMissingInputs() {
		t.Fatalf("unpivot-consuming candidate needs repair: %#v", impact.MissingInputs)
	}
	if len(analyzed.Construction.SourceProjections) != 1 || analyzed.Construction.SourceProjections[0].OwnerStepID != unpivotSourceStep {
		t.Fatalf("analysis dropped or re-owned the unpivot projection: %#v", analyzed.Construction.SourceProjections)
	}
}

// The row-preserving prefix allow-list is enforced, not trust-me coded.
func TestConstructionUnpivotSourceCannotCrossRowChangingPrefix(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	group := filterSourceGroupStep()
	carrierOutputs := []authoringv2.StageColumn{
		{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
		{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
	}
	melt := unpivotMeltStep(unpivotSourceStep, []string{unpivotPatientColumn}, unpivotMeltOutputs(carrierOutputs...))
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: group.ID}}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{group, melt}}
	base := deriveSourcePatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{group}})
	base.stages = append(base.stages, explorer.ReceiptConstructionStage{
		ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	})

	_, err := harness.service.constructionCandidateWithUnpivotSources(context.Background(), base,
		unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
		}), candidate)
	if err == nil {
		t.Fatal("a root unpivot source was carried across a GROUP")
	}
	if code := lifecycleErrorCode(err); code != "UNPIVOT_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("root unpivot source across a GROUP error = %v, want UNPIVOT_SOURCE_CANNOT_CROSS_STAGE", err)
	}
}

// A selection the melt does not declare as a value input is refused, and the
// joint scalar-kind rule refuses a melt outside the compiler's compatibility
// predicate.
func TestConstructionUnpivotSourceColumnMismatchAndIncompatibleKinds(t *testing.T) {
	snapshot := unpivotSourceBSSnapshot("unpivot-source-kinds")
	service := deriveSourceService(t)
	base := deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	base.stages = []explorer.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: deriveSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "boolean"},
			{ID: "note_col", Name: "note_col", Label: "Note", Type: "string"},
		},
	}}

	notInput := unpivotMeltStep(unpivotSourceStep, []string{"note_col"}, unpivotMeltOutputs())
	notInput.Operation.Unpivot.ValueOutputColumnID = unpivotMeltValueColumn
	_, err := service.constructionCandidateWithUnpivotSources(context.Background(), base,
		ConstructionProposalRequest{
			Project: deriveSourceProject, ExplorerID: deriveSourceExplorer, SnapshotToken: "unpivot-source-kinds",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:unpivot-source-draft", OutputID: deriveSourceOutput,
			ChangedStepID: unpivotSourceStep,
			UnpivotSources: []ConstructionUnpivotSourceSelection{
				unpivotBSSelections(t, snapshot, "active", "foreign_column"),
			},
		}, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{notInput}})
	if err == nil {
		t.Fatal("a selection outside the melt inputs was accepted")
	}
	if code := lifecycleErrorCode(err); code != "UNPIVOT_SOURCE_COLUMN_MISMATCH" {
		t.Fatalf("selection outside melt inputs error = %v, want UNPIVOT_SOURCE_COLUMN_MISMATCH", err)
	}

	mixed := unpivotMeltStep(unpivotSourceStep, []string{"note_col", unpivotSourceBSColumn}, unpivotMeltOutputs())
	_, err = service.constructionCandidateWithUnpivotSources(context.Background(), base,
		ConstructionProposalRequest{
			Project: deriveSourceProject, ExplorerID: deriveSourceExplorer, SnapshotToken: "unpivot-source-kinds",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:unpivot-source-draft", OutputID: deriveSourceOutput,
			ChangedStepID: unpivotSourceStep,
			UnpivotSources: []ConstructionUnpivotSourceSelection{
				unpivotBSSelections(t, snapshot, "active", unpivotSourceBSColumn),
			},
		}, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{mixed}})
	if err == nil {
		t.Fatal("a melt mixing string and boolean value columns was accepted")
	}
	if code := lifecycleErrorCode(err); code != "UNPIVOT_SOURCE_TYPES_INCOMPATIBLE" {
		t.Fatalf("mixed-kind melt error = %v, want UNPIVOT_SOURCE_TYPES_INCOMPATIBLE", err)
	}
}

// A signed slot whose field is already an authored column of the table is
// refused with the shared refuse-not-dedupe decision.
func TestConstructionUnpivotSourceRefusesSecondCarrierOfSameField(t *testing.T) {
	snapshot := unpivotSourceBSSnapshot("unpivot-source-collision")
	service := deriveSourceService(t)
	base := deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	document := deriveSourceDocument()
	document.Columns = append(document.Columns, authoringv2.Column{
		ColumnID: "active_flag", Column: "active_flag", Label: "Active", LogicalType: "boolean",
		OccurrenceID: authoringv2.RootOccurrenceID,
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "active", ProjectionMode: "VALUE"}},
	})
	base.document = document
	base.stages = []explorer.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: deriveSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "boolean"},
			{ID: "active_flag", Name: "active_flag", Label: "Active", Type: "boolean"},
		},
	}}

	melt := unpivotMeltStep(unpivotSourceStep, []string{"active_flag", unpivotSourceBSColumn}, unpivotMeltOutputs())
	_, err := service.constructionCandidateWithUnpivotSources(context.Background(), base,
		ConstructionProposalRequest{
			Project: deriveSourceProject, ExplorerID: deriveSourceExplorer, SnapshotToken: "unpivot-source-collision",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:unpivot-source-draft", OutputID: deriveSourceOutput,
			ChangedStepID: unpivotSourceStep,
			UnpivotSources: []ConstructionUnpivotSourceSelection{
				unpivotBSSelections(t, snapshot, "active", unpivotSourceBSColumn),
			},
		}, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{melt}})
	if err == nil {
		t.Fatal("a signed slot duplicating an authored column was accepted")
	}
	if code := lifecycleErrorCode(err); code != "UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED" {
		t.Fatalf("duplicate field error = %v, want UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED", err)
	}
}

// Dropping the selections strips the projections from the carriers.
func TestConstructionUnpivotSourceRemovalStripsProjection(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: unpivotPatientColumn, OwnerStepID: unpivotSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}
	carriers := append(deriveSourcePatientIdentityColumns(),
		authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		authoringv2.StageColumn{ID: unpivotPatientColumn, Name: authoringv2.ConstructionSourceProjectionName(unpivotPatientColumn), Label: "Patient.multipleBirthInteger", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
	meltOutputs := unpivotMeltOutputs(
		authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	melt := unpivotMeltStep(unpivotSourceStep, []string{unpivotPatientColumn}, meltOutputs)
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
	saved := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{carrier, melt},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	savedDocument := deriveSourcePatientDocument(saved)
	if err := savedDocument.Validate(); err != nil {
		t.Fatalf("a saved unpivot-owned construction is not durable authoring state: %v", err)
	}

	cleared, err := service.constructionCandidateWithUnpivotSources(context.Background(),
		deriveSourcePatientBase(snapshot, saved), unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{}), saved)
	if err != nil {
		t.Fatalf("clearing the unpivot source selections failed: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("cleared selection kept an unpivot-owned projection: %#v", cleared.SourceProjections)
	}
	for _, step := range cleared.Steps {
		for _, output := range step.Outputs {
			if output.ID == unpivotPatientColumn {
				t.Fatalf("cleared selection left the projection column in step %q outputs: %#v", step.ID, step.Outputs)
			}
		}
	}
}

// An omitted unpivotSources channel is a no-op on the candidate.
func TestConstructionUnpivotSourceLeavesSelectedColumnBehaviorUnchanged(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		unpivotMeltStep(unpivotSourceStep, []string{"derived_total"}, unpivotMeltOutputs(deriveSourcePatientIdentityColumns()...)),
	}}
	construction.Steps[0].Operation.Unpivot.Inputs = []authoringv2.ConstructionUnpivotInput{
		{ColumnID: "derived_total", Key: unpivotKey("variant-a")},
	}
	unchanged, err := harness.service.constructionCandidateWithUnpivotSources(context.Background(),
		deriveSourcePatientBase(harness.snapshot, construction),
		unpivotPatientProposalRequest(unpivotSourceStep, nil), construction)
	if err != nil {
		t.Fatalf("unpivot request without unpivotSources failed: %v", err)
	}
	if !reflect.DeepEqual(unchanged, construction) {
		t.Fatalf("omitted unpivotSources mutated the candidate: %#v", unchanged)
	}
}

// Duplicate or blank selection facts are refused at the request boundary.
func TestConstructionUnpivotSourceSelectionsRequireExactDeduplicatedFacts(t *testing.T) {
	duplicate := unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{
		{ChoiceID: "rc1.a.b", ColumnID: "c1"}, {ChoiceID: "rc1.a.b", ColumnID: "c2"},
	})
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "unpivotSources") {
		t.Fatalf("duplicate unpivotSources error = %v, want an unpivotSources dedup refusal", err)
	}
	blank := unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{{ChoiceID: " ", ColumnID: "c1"}})
	if err := blank.Validate(); err == nil || !strings.Contains(err.Error(), "unpivotSources[0].choiceId") {
		t.Fatalf("blank choiceId error = %v, want unpivotSources[0].choiceId refusal", err)
	}
	blankColumn := unpivotPatientProposalRequest(unpivotSourceStep, []ConstructionUnpivotSourceSelection{{ChoiceID: "rc1.a.b", ColumnID: " "}})
	if err := blankColumn.Validate(); err == nil || !strings.Contains(err.Error(), "unpivotSources[0].columnId") {
		t.Fatalf("blank columnId error = %v, want unpivotSources[0].columnId refusal", err)
	}
	encoded, err := json.Marshal(ConstructionUnpivotSourceSelection{ChoiceID: "rc1.x.y", ColumnID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"choiceId":"rc1.x.y","columnId":"c"}` {
		t.Fatalf("unpivotSources selection wire shape = %s", encoded)
	}
}

// (g) The advertised choice object is EXACTLY the six pinned wire fields.
func TestConstructionUnpivotSourceChoiceWireShapeIsExactlySixFields(t *testing.T) {
	wantFields := map[string]bool{"choiceId": true, "fieldPath": true, "label": true, "logicalType": true, "isPopulated": true, "columnId": true}
	structFields := make(map[string]bool, reflect.TypeOf(ConstructionUnpivotSourceChoice{}).NumField())
	for index := range reflect.TypeOf(ConstructionUnpivotSourceChoice{}).NumField() {
		tag := strings.Split(reflect.TypeOf(ConstructionUnpivotSourceChoice{}).Field(index).Tag.Get("json"), ",")[0]
		structFields[tag] = true
	}
	if !reflect.DeepEqual(structFields, wantFields) {
		t.Fatalf("ConstructionUnpivotSourceChoice struct fields = %v, want exactly %v", structFields, wantFields)
	}
	for _, pivotExtra := range []string{"occurrenceId", "fhirType", "valueType", "isIdentifier", "isReference"} {
		if structFields[pivotExtra] {
			t.Fatalf("pivot-style field %q must never appear on an unpivot source choice: %#v", pivotExtra, structFields)
		}
	}
	encoded, err := json.Marshal(ConstructionUnpivotSourceChoice{
		ChoiceID: "rc1.a.b", FieldPath: "multipleBirthInteger", Label: "Patient.multipleBirthInteger", LogicalType: "integer", IsPopulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(wantFields) {
		t.Fatalf("encoded choice has %d keys, want exactly %d: %s", len(decoded), len(wantFields), encoded)
	}
}

// The advertised columnId only survives while the owner melt still declares
// this exact slot as a value input at this exact stage.
func TestConstructionUnpivotSourceCapabilityAdvertisesDurableColumnIdOnlyWhileSigned(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: unpivotPatientColumn, OwnerStepID: unpivotSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}
	liveMelt := unpivotMeltStep(unpivotSourceStep, []string{unpivotPatientColumn}, unpivotMeltOutputs(authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
	shrunkMelt := unpivotMeltStep(unpivotSourceStep, []string{"patient-id"}, unpivotMeltOutputs())

	choiceColumnID := func(t *testing.T, construction authoringv2.Construction, stageID string) string {
		t.Helper()
		base := deriveSourcePatientBase(snapshot, construction)
		if stageID != recipe.ConstructionSourceProjectionID {
			base.stages = append(base.stages, explorer.ReceiptConstructionStage{ID: stageID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"})
		}
		result, err := service.constructionUnpivotSourceCapability(context.Background(), base, "patients", stageID)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range result.Choices {
			if choice.FieldPath == "multipleBirthInteger" {
				return choice.ColumnID
			}
		}
		return "<missing choice>"
	}

	live := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{liveMelt},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, live, recipe.ConstructionSourceProjectionID); got != unpivotPatientColumn {
		t.Fatalf("live durable materialization advertised columnId %q, want %q", got, unpivotPatientColumn)
	}

	repointed := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{shrunkMelt},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, repointed, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("saved melt no longer declaring the source column was still advertised as columnId %q", got)
	}

	foreignOwner := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{liveMelt},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: unpivotPatientColumn, OwnerStepID: "another-step", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
		}},
	}
	if got := choiceColumnID(t, foreignOwner, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("materialization not owned by this stage's unpivot was advertised as columnId %q", got)
	}
}

// (a, wiring) GetConstructionCapabilities assembles unpivotSourceInput beside
// the pivot, filter, and derive source inputs.
func TestGetConstructionCapabilitiesAssemblesUnpivotSourceInputBesideDeriveSourceInput(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	capabilities, err := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("capabilities with unpivot source discovery: %v", err)
	}
	if capabilities.DeriveSourceInput.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("derive source input was displaced: %#v", capabilities.DeriveSourceInput)
	}
	result := capabilities.UnpivotSourceInput
	if result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("unpivot source input stage = %#v, want the selected source stage", result)
	}
	byPath := make(map[string]ConstructionUnpivotSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	birth, found := byPath["multipleBirthInteger"]
	if !found {
		t.Fatalf("populated unselected root scalar missing from capabilities: %#v", result.Choices)
	}
	if !birth.IsPopulated || birth.LogicalType != "integer" || birth.Label != "Birth count" || birth.ColumnID != "" {
		t.Fatalf("birth count choice = %#v, want populated integer with the generated label and no durable column", birth)
	}
	identity, err := capability.DecodeRowChoiceID(birth.ChoiceID)
	if err != nil || identity.SnapshotToken != harness.snapshot.Token || identity.SchemaDigest != harness.snapshot.Identity.SchemaDigest {
		t.Fatalf("capability choice id is not pinned to the current snapshot: %v %#v", err, identity)
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"unpivotSourceInput":{`) {
		t.Fatalf("capabilities response omitted the pinned unpivotSourceInput field: %s", encoded)
	}
}

// (b) Through ProposeConstruction with the real compiler, the accepted
// candidate melts the unselected field's compiled slot.
func TestProposeConstructionUnpivotSourceMeltsUnselectedFieldWithRealCompiler(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	carriers := append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	carrier.Operation.Derive.Left = authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(2)}}
	melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"}, unpivotMeltOutputs(authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
	saved := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
	harness.saveDraft(t, saved)
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
	acceptedBefore := append([]byte(nil), harness.store.created.DraftConfig...)

	candidate, err := decodeConstruction(saved)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
		authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})

	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         unpivotSourceStep,
		CandidateConstruction: candidate,
		UnpivotSources:        []ConstructionUnpivotSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction with unpivotSources: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("unpivot source proposal = %#v, want a compiled preview proposal", proposal)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 1 {
		t.Fatalf("proposal projections = %#v, want the durable unpivot-owned projection", proposal.CandidateConstruction.SourceProjections)
	}
	durable := proposal.CandidateConstruction.SourceProjections[0]
	if durable.OwnerStepID != unpivotSourceStep || durable.ColumnID != unpivotPatientColumn || durable.FieldPath != "multipleBirthInteger" ||
		durable.FHIRType != "integer" || durable.LogicalType != "integer" || durable.OccurrenceID != authoringv2.RootOccurrenceID {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", durable)
	}
	if harness.store.saveDraftCalls != 0 || string(acceptedBefore) != string(harness.store.created.DraftConfig) {
		t.Fatalf("proposal changed the accepted draft: saves=%d", harness.store.saveDraftCalls)
	}

	receipt := harness.store.receipt
	if receipt == nil || len(receipt.Bundle.Outputs) != 1 {
		t.Fatalf("compiled receipt omits the candidate bundle: %#v", receipt)
	}
	output := receipt.Bundle.Outputs[0]
	name := authoringv2.ConstructionSourceProjectionName(unpivotPatientColumn)
	var slotFieldRef, slotSelect string
	for index := range output.Fields {
		if output.Fields[index].Name == name {
			slotFieldRef, slotSelect = output.Fields[index].FieldRef, output.Fields[index].Expr.Select
		}
	}
	if slotFieldRef != "multipleBirthInteger" || !strings.HasSuffix(slotSelect, ".multipleBirthInteger") {
		t.Fatalf("compiled slot for the unselected field = fieldRef %q select %q, want it to read the unselected field", slotFieldRef, slotSelect)
	}
	melted := false
	for _, step := range output.Construction.Steps {
		if step.Operation.Kind != recipe.ConstructionUnpivotOp || step.Operation.Unpivot == nil {
			continue
		}
		for _, input := range step.Operation.Unpivot.Inputs {
			if input.ColumnID == unpivotPatientColumn {
				melted = true
			}
		}
	}
	if !melted {
		t.Fatalf("compiled UNPIVOT step does not melt the signed slot: %#v", output.Construction.Steps)
	}
}

// unpivotSaveDraftPublicNote saves the draft with one extra public authored
// field column (Patient.gender, string) so a signed STRING-class melt input
// has a compiler-compatible public partner without touching shared fixtures.
func unpivotSaveDraftPublicNote(t *testing.T, harness *unpivotSourceHarness, construction authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(harness.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	document := workspace.Documents[0]
	document.Rows = authoringv2.RecordsRowDefinition()
	for index := range document.Columns {
		if document.Columns[index].Column == "patient_id" && document.Columns[index].ColumnID == "" {
			document.Columns[index].ColumnID = "patient-id"
		}
	}
	document.Columns = append(document.Columns, authoringv2.Column{
		ColumnID: "note", Column: "note", Label: "Note", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "gender", ProjectionMode: "VALUE"}},
	})
	document.TableShape = nil
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate public-note unpivot draft: %v", err)
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

// Operator decision #4 accept-side: the durable proposal path (real compiler
// wiring) must refuse a signed date/date_time melt with the named code, not
// preview it. Before the refusal shipped this reached PREVIEW_PENDING at 200
// (authority /tmp/opencode/jrev/probe-date-melt-dp4.log; finding F-1's
// accept-side lock was inverted by the operator ruling).
func TestProposeConstructionUnpivotSourceRefusesDateFamilyWithRealCompiler(t *testing.T) {
	for _, row := range []struct {
		id, fieldPath, label, logicalType string
	}{
		{"candidate-birth-date", "birthDate", "Birth date", "date"},
		{"candidate-deceased-date-time", "deceasedDateTime", "Deceased", "date_time"},
	} {
		harness := newUnpivotSourceHarness(t)
		snapshot := harness.snapshot
		snapshot.Candidates = append(snapshot.Candidates,
			capability.Candidate{
				ID: row.id, NodeID: "node-patient", ResourceType: "Patient", FieldPath: row.fieldPath,
				Label: row.label, LogicalType: row.logicalType, Cardinality: "optional_one",
				ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
				SupportedOperations: []capability.Operation{capability.OperationSelect},
				Observed:            true, Populated: true, ObservedDocumentCount: 12,
			},
			capability.Candidate{
				ID: "candidate-gender", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "gender",
				Label: "Gender", LogicalType: "string", Cardinality: "optional_one",
				ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
				SupportedOperations: []capability.Operation{capability.OperationSelect},
				Observed:            true, Populated: true, ObservedDocumentCount: 12,
			})
		harness.snapshot = snapshot
		carriers := append(deriveSourcePatientIdentityColumns(),
			authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
			authoringv2.StageColumn{ID: "note", Name: "note", Label: "Note", Type: "string"})
		carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
		carrier.Operation.Derive.OutputColumnID = "derived_total"
		melt := unpivotMeltStep(unpivotSourceStep, []string{"note"}, []authoringv2.StageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
			{ID: unpivotMeltKeyColumn, Name: unpivotMeltKeyColumn, Label: "Melt key", Type: "string"},
			{ID: unpivotMeltValueColumn, Name: unpivotMeltValueColumn, Label: "Melt value", Type: "string"},
		})
		melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
		saved := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
		unpivotSaveDraftPublicNote(t, harness, saved)
		version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest

		candidate, err := decodeConstruction(saved)
		if err != nil {
			t.Fatal(err)
		}
		candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
			authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})

		choice := deriveSourceRowChoice(t, snapshot, "Patient", row.fieldPath)
		proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
			Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
			ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
			ChangedStepID:         unpivotSourceStep,
			CandidateConstruction: candidate,
			UnpivotSources:        []ConstructionUnpivotSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: unpivotPatientColumn}},
		})
		if err == nil {
			t.Fatalf("%s melt reached a preview instead of a refusal: %#v", row.logicalType, proposal)
		}
		if code := lifecycleErrorCode(err); code != "UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD" {
			t.Fatalf("%s melt proposal error = %v, want UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD", row.logicalType, err)
		}
	}
}

// Operator decision #4 regression lock, same harness shape as the refusal
// above: the permitted STRING class still melts through the real compiler.
func TestProposeConstructionUnpivotSourceStillMeltsSignedStringWithRealCompiler(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "candidate-gender", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "gender",
			Label: "Gender", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		},
		capability.Candidate{
			ID: "candidate-language", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "language",
			Label: "Language", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		})
	harness.snapshot = snapshot
	carriers := append(deriveSourcePatientIdentityColumns(),
		authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		authoringv2.StageColumn{ID: "note", Name: "note", Label: "Note", Type: "string"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	melt := unpivotMeltStep(unpivotSourceStep, []string{"note"}, []authoringv2.StageColumn{
		{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
		{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		{ID: unpivotMeltKeyColumn, Name: unpivotMeltKeyColumn, Label: "Melt key", Type: "string"},
		{ID: unpivotMeltValueColumn, Name: unpivotMeltValueColumn, Label: "Melt value", Type: "string"},
	})
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
	saved := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
	unpivotSaveDraftPublicNote(t, harness, saved)
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest

	candidate, err := decodeConstruction(saved)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
		authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-language")})

	choice := deriveSourceRowChoice(t, snapshot, "Patient", "language")
	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         unpivotSourceStep,
		CandidateConstruction: candidate,
		UnpivotSources:        []ConstructionUnpivotSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: unpivotPatientColumn}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction melting a signed string field: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("string melt proposal = %#v, want a compiled preview proposal", proposal)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 1 {
		t.Fatalf("proposal projections = %#v, want the durable string projection", proposal.CandidateConstruction.SourceProjections)
	}
	durable := proposal.CandidateConstruction.SourceProjections[0]
	if durable.FieldPath != "language" {
		t.Fatalf("durable string-class projection = %#v, want server-derived language facts", durable)
	}
}

// Operator decision #4 joint gate: an integer+date melt must be refused by
// name. The per-selection date admission refusal fires before the class
// compatibility rule for the signed date field, so the honest code is the
// date-family refusal, not the generic UNPIVOT_SOURCE_TYPES_INCOMPATIBLE.
func TestProposeConstructionUnpivotSourceRefusesIntegerWithDateMelt(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "candidate-birth-date", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "birthDate",
		Label: "Birth date", LogicalType: "date", Cardinality: "optional_one",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 12,
	})
	harness.snapshot = snapshot
	carriers := append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	carrier.Operation.Derive.Right = authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(2)}}
	melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"}, unpivotMeltOutputs(authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
	melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
	saved := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
	harness.saveDraft(t, saved)
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest

	candidate, err := decodeConstruction(saved)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
		authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})

	choice := deriveSourceRowChoice(t, snapshot, "Patient", "birthDate")
	_, err = harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         unpivotSourceStep,
		CandidateConstruction: candidate,
		UnpivotSources:        []ConstructionUnpivotSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: unpivotPatientColumn}},
	})
	if err == nil || lifecycleErrorCode(err) != "UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD" {
		t.Fatalf("integer+date melt error = %v, want UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD", err)
	}
}

// (6) An accepted proposal whose field stops qualifying is refused at Apply.
func TestApplyConstructionProposalRefusesStaleUnpivotSourceBinding(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			harness := newUnpivotSourceHarness(t)
			carriers := append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
			carrier := deriveSourceLiteralOnlyStep("derive-base", carriers)
			carrier.Operation.Derive.OutputColumnID = "derived_total"
			melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"}, unpivotMeltOutputs(authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
			melt.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}}
			saved := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, melt}}
			harness.saveDraft(t, saved)
			version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
			candidate, err := decodeConstruction(saved)
			if err != nil {
				t.Fatal(err)
			}
			candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
				authoringv2.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})
			proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
				Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
				ChangedStepID:         unpivotSourceStep,
				CandidateConstruction: candidate,
				UnpivotSources:        []ConstructionUnpivotSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn}},
			})
			if err != nil {
				t.Fatalf("ProposeConstruction: %v", err)
			}
			if stale {
				harness.unobserveBirthCount()
			}
			_, err = harness.service.ApplyCommands(context.Background(), harness.store.created.Project, harness.store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
				CommandID: "unpivot-source-apply", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
				Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
			}, "unpivot-source-actor")
			if stale {
				if err == nil || lifecycleErrorCode(err) != "STALE_UNPIVOT_SOURCE_BINDING" {
					t.Fatalf("stale unpivot source apply error = %v, want STALE_UNPIVOT_SOURCE_BINDING", err)
				}
				if harness.store.created.DraftVersion != version || harness.store.created.DraftDigest != digest || harness.store.saveDraftCalls != 0 {
					t.Fatalf("stale apply changed the accepted draft: version=%d digest=%q saves=%d", harness.store.created.DraftVersion, harness.store.created.DraftDigest, harness.store.saveDraftCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyCommands with a current unpivot source: %v", err)
			}
			applied := harness.store.created
			if applied == nil {
				t.Fatal("apply did not save the draft")
			}
			workspace, decodeErr := authoringv2.DecodeWorkspace(applied.DraftConfig)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			savedConstruction := workspace.Documents[0].Construction
			if savedConstruction == nil || len(savedConstruction.SourceProjections) != 1 || savedConstruction.SourceProjections[0].OwnerStepID != unpivotSourceStep ||
				savedConstruction.SourceProjections[0].FieldPath != "multipleBirthInteger" {
				t.Fatalf("applied construction lost the durable unpivot projection: %#v", savedConstruction)
			}
			restored := ""
			restoreBase := deriveSourcePatientBase(harness.snapshot, *savedConstruction)
			restoreBase.stages = append(restoreBase.stages, explorer.ReceiptConstructionStage{
				ID: "derive-base", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "DERIVE",
			})
			restoreCapability, capErr := harness.service.constructionUnpivotSourceCapability(context.Background(), restoreBase, "patients", "derive-base")
			if capErr != nil {
				t.Fatalf("capabilities after the durable unpivot source applied: %v", capErr)
			}
			for _, choice := range restoreCapability.Choices {
				if choice.FieldPath == "multipleBirthInteger" {
					restored = choice.ColumnID
				}
			}
			if restored != unpivotPatientColumn {
				t.Fatalf("durable materialization should advertise columnId %q for UI restore, got %q", unpivotPatientColumn, restored)
			}
		})
	}
}

func decodeConstruction(construction authoringv2.Construction) (authoringv2.Construction, error) {
	encoded, err := json.Marshal(construction)
	if err != nil {
		return authoringv2.Construction{}, err
	}
	var clone authoringv2.Construction
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return authoringv2.Construction{}, err
	}
	return clone, nil
}

// --- ROW-SOURCE-03 S-A: precise per-skipped-class lane reason codes -------

func TestConstructionUnpivotSourceCapabilityReportsPreciseSkipClassReasonCodes(t *testing.T) {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	harness := newUnpivotSourceHarness(t)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	rowChanging := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{filterSourceGroupStep()}}
	blockedStages := []explorer.ReceiptConstructionStage{{ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"}}
	patient := func(candidates ...capability.Candidate) capability.Snapshot {
		snapshot := harness.snapshot
		snapshot.Candidates = candidates
		return snapshot
	}
	meltDate := func(id, path, logicalType string) capability.Candidate {
		return rowSourceLaneCandidate("node-patient", "Patient", id, path, logicalType, "optional_one", true)
	}
	rows := []struct {
		name     string
		service  *Service
		base     constructionBase
		outputID string
		stageID  string
		wantCode string
	}{
		{
			name:    "the only melt-kind field has no observed values",
			service: deriveSourceService(t),
			base: deriveSourcePatientBase(patient(capability.Candidate{
				ID: "candidate-birth-count", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "multipleBirthInteger",
				Label: "Birth count", LogicalType: "integer", Cardinality: "optional_one",
				ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
				SupportedOperations: []capability.Operation{capability.OperationSelect},
			}), empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "NO_OBSERVED_VALUES_UNPIVOT_SOURCE_FIELDS",
		},
		{
			name:    "every populated field is a refused date-family melt kind",
			service: deriveSourceService(t),
			base: deriveSourcePatientBase(patient(
				meltDate("candidate-birth-date", "birthDate", "date"),
				meltDate("candidate-deceased-date-time", "deceasedDateTime", "date_time")), empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "NO_TYPE_ELIGIBLE_UNPIVOT_SOURCE_FIELDS",
		},
		{
			name:    "every field is repeated at the row root",
			service: deriveSourceService(t),
			base: deriveSourcePatientBase(patient(rowSourceLaneCandidate("node-patient", "Patient",
				"candidate-birth-count", "multipleBirthInteger", "integer", "many", true)), empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ROUTE_ELIGIBLE_UNPIVOT_SOURCE_FIELDS",
		},
		{
			name:     "choices were issued for a previous dataset snapshot",
			service:  rowSourceServiceWithPlanner(staleRowSourceChoicePlanner{inner: resolver, issued: rowSourceSnapshotWithGeneration(harness.snapshot, "unpivot-source-precise-stale-issued")}),
			base:     deriveSourcePatientBase(harness.snapshot, empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "STALE_UNPIVOT_SOURCE_SNAPSHOT",
		},
		{
			name:    "the construction prefix cannot carry root fields to the stage",
			service: deriveSourceService(t),
			base: func() constructionBase {
				base := deriveSourceBase(unpivotSourceBSSnapshot("unpivot-source-precise-reach"), rowChanging)
				base.stages = append(base.stages, blockedStages...)
				return base
			}(),
			outputID: deriveSourceOutput, stageID: "group-first",
			wantCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE",
		},
		{
			name:     "the authorized choice planner is not configured",
			service:  rowSourceServiceWithPlanner(nil),
			base:     deriveSourcePatientBase(harness.snapshot, empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "ROW_CHOICE_UNAVAILABLE",
		},
		{
			name:     "the planner enumerates no fields at all",
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}),
			base:     deriveSourcePatientBase(harness.snapshot, empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS",
		},
		{
			name:    "distinct skipped classes stay on the generic fallback",
			service: deriveSourceService(t),
			base: deriveSourcePatientBase(patient(
				capability.Candidate{
					ID: "candidate-birth-count", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "multipleBirthInteger",
					Label: "Birth count", LogicalType: "integer", Cardinality: "optional_one",
					ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
					SupportedOperations: []capability.Operation{capability.OperationSelect},
				},
				meltDate("candidate-birth-date", "birthDate", "date")), empty),
			outputID: "patients", stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS",
		},
	}
	genericCode := "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS"
	distinct := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			result, err := row.service.constructionUnpivotSourceCapability(context.Background(), row.base, row.outputID, row.stageID)
			if err != nil {
				t.Fatalf("unpivot source capability: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode {
				t.Fatalf("reasonCode = %q (reason %q), want %q", result.ReasonCode, result.Reason, row.wantCode)
			}
			if strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reason is blank for %s", row.wantCode)
			}
			if previous, seen := distinct[result.ReasonCode]; seen && previous != row.name && result.ReasonCode != genericCode {
				t.Fatalf("rows %q and %q share reasonCode %q", previous, row.name, result.ReasonCode)
			}
			if _, seen := distinct[result.ReasonCode]; !seen {
				distinct[result.ReasonCode] = row.name
			}
			if result.StageID != row.stageID {
				t.Fatalf("stageId = %q, want %q", result.StageID, row.stageID)
			}
		})
	}
	if len(distinct) != 7 {
		t.Fatalf("expected seven distinct lane reason codes, got %#v", distinct)
	}
}

func TestConstructionUnpivotSourceCapabilityNamesInventoryFailureDistinctly(t *testing.T) {
	const internalDetail = "capability-store-dsn=secret-token"
	inventoryErr := errors.New("row-choice inventory store unreachable: " + internalDetail)
	harness := newUnpivotSourceHarness(t)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	patient := func(candidates ...capability.Candidate) capability.Snapshot {
		snapshot := harness.snapshot
		snapshot.Candidates = candidates
		return snapshot
	}
	unpopulatedBirth := capability.Candidate{
		ID: "candidate-birth-count", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "multipleBirthInteger",
		Label: "Birth count", LogicalType: "integer", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	rows := []struct {
		name     string
		service  *Service
		base     constructionBase
		wantCode string
		wantFail bool
	}{
		{name: "planner enumeration failure names the inventory class",
			service:  rowSourceServiceWithPlanner(failingRowSourcePlanner{err: inventoryErr}),
			base:     deriveSourcePatientBase(harness.snapshot, empty),
			wantCode: "UNPIVOT_SOURCE_INVENTORY_UNAVAILABLE", wantFail: true},
		{name: "a planner that enumerates nothing stays generic",
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}),
			base:     deriveSourcePatientBase(harness.snapshot, empty),
			wantCode: "NO_ELIGIBLE_POPULATED_UNPIVOT_SOURCE_FIELDS"},
		{name: "truly empty data names the observed class, not the inventory class",
			service: deriveSourceService(t), base: deriveSourcePatientBase(patient(unpopulatedBirth), empty),
			wantCode: "NO_OBSERVED_VALUES_UNPIVOT_SOURCE_FIELDS"},
	}
	distinct := map[string]bool{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			result, err := row.service.constructionUnpivotSourceCapability(context.Background(),
				row.base, "patients", recipe.ConstructionSourceProjectionID)
			if row.wantFail {
				if err == nil {
					t.Fatal("a failed enumeration was swallowed: the capability must still return the wrapped error")
				}
				if !errors.Is(err, inventoryErr) {
					t.Fatalf("the planner cause was not wrapped through the typed error: %v", err)
				}
				if code := lifecycleErrorCode(err); code != row.wantCode {
					t.Fatalf("typed error code = %q, want %q", code, row.wantCode)
				}
				if strings.Contains(result.Reason, internalDetail) || strings.Contains(result.ReasonCode, internalDetail) {
					t.Fatalf("planner error detail %q crossed into advertised payload: %#v", internalDetail, result)
				}
			} else if err != nil {
				t.Fatalf("soft empty verdict must not surface as an error: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want fail-closed unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode || strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reasonCode = %q (reason %q), want %q with a non-blank reason", result.ReasonCode, result.Reason, row.wantCode)
			}
			if result.StageID != recipe.ConstructionSourceProjectionID {
				t.Fatalf("stageId = %q, want the requested stage even inside the failure class", result.StageID)
			}
			distinct[result.ReasonCode] = true
		})
	}
	if len(distinct) != 3 {
		t.Fatalf("inventory-error and empty-data verdicts collapsed: %#v", distinct)
	}
}
