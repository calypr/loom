package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/unit"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type operatorOracleRawState string

const (
	operatorOracleInteger     operatorOracleRawState = "INTEGER"
	operatorOracleBoolean     operatorOracleRawState = "BOOLEAN"
	operatorOracleStateString operatorOracleRawState = "STRING"
	operatorOracleQuantity    operatorOracleRawState = "QUANTITY"
	operatorOracleMissing     operatorOracleRawState = "MISSING"
	operatorOracleAbsent      operatorOracleRawState = "RECORDED_ABSENCE"
)

type operatorOracleFixture struct {
	project     string
	patientID   string
	category    string
	rootAnchor  string
	sourceID    string
	state       operatorOracleRawState
	integer     *int64
	boolean     *bool
	text        *string
	quantity    *float64
	unitSystem  string
	unitCode    string
	timestamp   string
	absenceText string
}

func operatorOracleInt(value int64) *int64       { return &value }
func operatorOracleBool(value bool) *bool        { return &value }
func operatorOracleText(value string) *string    { return &value }
func operatorOracleFloat(value float64) *float64 { return &value }

type operatorOracleContributor struct {
	resourceType string
	resourceID   string
	value        any
}

type operatorOracleOutputWant struct {
	patientID string
	category  string
	count     any
	exists    bool
	minimum   any
	maximum   any
	mean      any
	sum       any
	earliest  any
	latest    any
	unitSum   any
}

type operatorOracleTraceResult struct {
	value        any
	status       string
	omission     string
	contributors []operatorOracleContributor
}

func TestS04CompiledOperatorOraclesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}

	projectPrefix := "loom_s04_operator_" + uuid.NewString()
	projects := map[string]string{
		"main":                  projectPrefix + "_main",
		"unsupported":           projectPrefix + "_unsupported",
		"missing-time":          projectPrefix + "_missing_time",
		"window-missing-time":   projectPrefix + "_window_missing_time",
		"window-malformed-time": projectPrefix + "_window_malformed_time",
	}
	const generation = "generation-s04-operator-oracle"
	anchor := "2020-01-10T00:00:00Z"
	fixtures := []operatorOracleFixture{
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-early", state: operatorOracleInteger, integer: operatorOracleInt(2), timestamp: "2020-01-09T23:00:00Z"},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-tie-a", state: operatorOracleInteger, integer: operatorOracleInt(3), timestamp: anchor},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-tie-b", state: operatorOracleInteger, integer: operatorOracleInt(5), timestamp: anchor},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-after-window", state: operatorOracleInteger, integer: operatorOracleInt(99), timestamp: "2020-01-10T00:00:01Z"},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-zero", state: operatorOracleInteger, integer: operatorOracleInt(0), timestamp: "2020-01-09T23:45:00Z"},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-missing", state: operatorOracleMissing},
		{project: "main", patientID: "patient-numeric", category: "female", sourceID: "obs-absence", state: operatorOracleAbsent, absenceText: "not-performed"},
		{project: "main", patientID: "patient-units", category: "female", rootAnchor: "2025-01-03T00:00:00Z", sourceID: "obs-height-cm", state: operatorOracleQuantity, quantity: operatorOracleFloat(180), unitSystem: "http://unitsofmeasure.org", unitCode: "cm", timestamp: "2025-01-01T00:00:00Z"},
		{project: "main", patientID: "patient-units", category: "female", sourceID: "obs-height-m", state: operatorOracleQuantity, quantity: operatorOracleFloat(1.8), unitSystem: "http://unitsofmeasure.org", unitCode: "m", timestamp: "2025-01-02T00:00:00Z"},
		{project: "main", patientID: "patient-units", category: "female", sourceID: "obs-height-zero", state: operatorOracleQuantity, quantity: operatorOracleFloat(0), unitSystem: "http://unitsofmeasure.org", unitCode: "cm", timestamp: "2025-01-02T00:00:00Z"},
		{project: "main", patientID: "patient-units", category: "female", sourceID: "obs-height-outside", state: operatorOracleQuantity, quantity: operatorOracleFloat(900), unitSystem: "http://unitsofmeasure.org", unitCode: "cm", timestamp: "2025-01-10T00:00:00Z"},
		{project: "main", patientID: "patient-states", category: "Female", sourceID: "obs-state-zero", state: operatorOracleInteger, integer: operatorOracleInt(0), timestamp: anchor},
		{project: "main", patientID: "patient-states", category: "Female", sourceID: "obs-state-false", state: operatorOracleBoolean, boolean: operatorOracleBool(false)},
		{project: "main", patientID: "patient-states", category: "Female", sourceID: "obs-state-empty", state: operatorOracleStateString, text: operatorOracleText("")},
		{project: "main", patientID: "patient-states", category: "Female", sourceID: "obs-state-missing", state: operatorOracleMissing},
		{project: "main", patientID: "patient-states", category: "Female", sourceID: "obs-state-absence", state: operatorOracleAbsent, absenceText: "unknown"},
		{project: "main", patientID: "patient-unknown-category", category: "mystery", sourceID: "obs-unknown-category", state: operatorOracleInteger, integer: operatorOracleInt(4), timestamp: anchor},
		{project: "unsupported", patientID: "patient-unsupported-unit", category: "female ", sourceID: "obs-unsupported-unit", state: operatorOracleQuantity, quantity: operatorOracleFloat(7), unitSystem: "http://unitsofmeasure.org", unitCode: "furlong"},
		{project: "missing-time", patientID: "patient-missing-time", category: "female", sourceID: "obs-missing-time", state: operatorOracleInteger, integer: operatorOracleInt(1)},
		{project: "window-missing-time", patientID: "patient-window-missing-time", category: "female", rootAnchor: "2025-01-03T00:00:00Z", sourceID: "obs-window-valid", state: operatorOracleQuantity, quantity: operatorOracleFloat(25), unitSystem: "http://unitsofmeasure.org", unitCode: "cm", timestamp: "2025-01-02T00:00:00Z"},
		{project: "window-missing-time", patientID: "patient-window-missing-time", category: "female", sourceID: "obs-window-missing-time", state: operatorOracleQuantity, quantity: operatorOracleFloat(700), unitSystem: "http://unitsofmeasure.org", unitCode: "cm"},
		{project: "window-malformed-time", patientID: "patient-window-malformed-time", category: "female", rootAnchor: "2025-01-03T00:00:00Z", sourceID: "obs-window-malformed-time", state: operatorOracleQuantity, quantity: operatorOracleFloat(42), unitSystem: "http://unitsofmeasure.org", unitCode: "cm", timestamp: "not-an-instant"},
	}

	patients := make(map[string]map[string]string)
	patientDocs := make([]json.RawMessage, 0)
	observations := make([]json.RawMessage, 0, len(fixtures))
	edges := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		project := projects[fixture.project]
		patientKey := project + "_" + fixture.patientID
		if patients[project] == nil {
			patients[project] = make(map[string]string)
		}
		if _, exists := patients[project][fixture.patientID]; !exists {
			patients[project][fixture.patientID] = fixture.category
			rootAnchor := fixture.rootAnchor
			if rootAnchor == "" {
				rootAnchor = anchor
			}
			patientRaw, marshalErr := json.Marshal(map[string]any{
				"_key": patientKey, "id": fixture.patientID, "project": project, "project_id": project,
				"dataset_generation": generation, "resourceType": "Patient",
				"payload": map[string]any{"id": fixture.patientID, "resourceType": "Patient", "gender": fixture.category, "meta": map[string]any{"lastUpdated": rootAnchor}},
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			patientDocs = append(patientDocs, patientRaw)
		}

		payload := map[string]any{"id": fixture.sourceID, "resourceType": "Observation", "status": "final"}
		if fixture.integer != nil {
			payload["valueInteger"] = *fixture.integer
		}
		if fixture.boolean != nil {
			payload["valueBoolean"] = *fixture.boolean
		}
		if fixture.text != nil {
			payload["valueString"] = *fixture.text
		}
		if fixture.quantity != nil {
			payload["valueQuantity"] = map[string]any{
				"value": *fixture.quantity, "system": fixture.unitSystem, "code": fixture.unitCode, "unit": fixture.unitCode,
			}
		}
		if fixture.timestamp != "" {
			payload["effectiveDateTime"] = fixture.timestamp
		}
		if fixture.absenceText != "" {
			payload["dataAbsentReason"] = map[string]any{"text": fixture.absenceText}
		}
		observationKey := project + "_" + fixture.sourceID
		observationRaw, marshalErr := json.Marshal(map[string]any{
			"_key": observationKey, "id": fixture.sourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		observations = append(observations, observationRaw)
		edgeRaw, marshalErr := json.Marshal(map[string]any{
			"_key":  fmt.Sprintf("%s_edge_%d", project, len(edges)),
			"_from": "Observation/" + observationKey, "_to": "Patient/" + patientKey,
			"project": project, "project_id": project, "dataset_generation": generation,
			"label": "subject_Patient", "from_type": "Observation", "to_type": "Patient",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		edges = append(edges, edgeRaw)
	}
	for collection, documents := range map[string][]json.RawMessage{
		"Patient": patientDocs, "Observation": observations, "fhir_edge": edges,
	} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert %s fixtures: %v", collection, err)
		}
	}

	policy, err := unit.ResolveApprovedUnitPolicy("to-centimeters", "1")
	if err != nil {
		t.Fatal(err)
	}
	countExpr := recipe.Expression{Select: "valueInteger"}
	quantityExpr := recipe.Expression{Select: "valueQuantity.value"}
	ordered := func(name string, direction recipe.TemporalDirection, tie recipe.TemporalTiePolicy) recipe.Aggregate {
		return recipe.Aggregate{
			Name: name, OutputName: name, Operation: recipe.AggregateFirstOrdered, Expr: &countExpr,
			ContributorWindow: &recipe.ContributorWindow{
				Timestamp:   recipe.Expression{Select: "observation.effectiveDateTime"},
				Anchor:      recipe.Expression{Select: "root.meta.lastUpdated"},
				LowerOffset: -3600, UpperOffset: 0, LowerInclusive: true, UpperInclusive: true,
				Precision: recipe.TemporalPrecisionInstant,
			},
			Ordering: &recipe.TemporalOrdering{Timestamp: recipe.Expression{Select: "observation.effectiveDateTime"}, Direction: direction, TiePolicy: tie},
		}
	}
	unitAggregate := recipe.Aggregate{
		Name: "unit_sum", OutputName: "unit_sum", Operation: recipe.AggregateSum, Expr: &quantityExpr,
		UnitNormalization: &recipe.UnitNormalizationPolicy{
			SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code", Target: policy.Target, Rules: policy.Rules,
		},
	}
	aggregates := []recipe.Aggregate{
		{Name: "count", OutputName: "count", Operation: recipe.AggregateCount, Expr: &countExpr},
		{Name: "exists", OutputName: "exists", Operation: recipe.AggregateExists, Expr: &countExpr},
		{Name: "minimum", OutputName: "minimum", Operation: recipe.AggregateMin, Expr: &countExpr},
		{Name: "maximum", OutputName: "maximum", Operation: recipe.AggregateMax, Expr: &countExpr},
		{Name: "mean", OutputName: "mean", Operation: recipe.AggregateMean, Expr: &countExpr},
		{Name: "sum", OutputName: "sum", Operation: recipe.AggregateSum, Expr: &countExpr},
		ordered("earliest", recipe.TemporalAscending, recipe.TemporalTieResourceKey),
		ordered("latest", recipe.TemporalDescending, recipe.TemporalTieResourceKey),
		unitAggregate,
	}
	window := &recipe.ContributorWindow{
		Timestamp:   recipe.Expression{Select: "observation.effectiveDateTime"},
		Anchor:      recipe.Expression{Select: "root.meta.lastUpdated"},
		LowerOffset: -172800, UpperOffset: 0, LowerInclusive: true, UpperInclusive: false,
		Precision: recipe.TemporalPrecisionInstant,
	}
	windowedAggregate := func(name string, operation recipe.AggregateOperation, expr *recipe.Expression, normalize bool) recipe.Aggregate {
		aggregate := recipe.Aggregate{Name: name, OutputName: name, Operation: operation, Expr: expr, ContributorWindow: window}
		if normalize {
			aggregate.UnitNormalization = &recipe.UnitNormalizationPolicy{
				SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code", Target: policy.Target, Rules: policy.Rules,
			}
		}
		return aggregate
	}
	windowedSummary := recipe.Output{
		Name: "windowed_units", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation",
			Aggregates: []recipe.Aggregate{
				windowedAggregate("window_count", recipe.AggregateCount, nil, false),
				windowedAggregate("window_exists", recipe.AggregateExists, nil, false),
				windowedAggregate("window_minimum", recipe.AggregateMin, &quantityExpr, true),
				windowedAggregate("window_maximum", recipe.AggregateMax, &quantityExpr, true),
				windowedAggregate("window_mean", recipe.AggregateMean, &quantityExpr, true),
				windowedAggregate("window_sum", recipe.AggregateSum, &quantityExpr, true),
				{
					Name: "window_first_ordered", OutputName: "window_first_ordered", Operation: recipe.AggregateFirstOrdered, Expr: &quantityExpr,
					ContributorWindow: window,
					Ordering:          &recipe.TemporalOrdering{Timestamp: recipe.Expression{Select: "observation.effectiveDateTime"}, Direction: recipe.TemporalDescending, TiePolicy: recipe.TemporalTieResourceKey},
					UnitNormalization: &recipe.UnitNormalizationPolicy{SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code", Target: policy.Target, Rules: policy.Rules},
				},
			},
		}},
	}
	keepUnknown := recipe.ColumnTransformation{
		Column: "category", Transformation: columntransform.ValueTransformation{
			Kind: columntransform.KindExactCategoryRecode,
			ExactCategoryRecode: &columntransform.ExactCategoryRecode{
				Mappings: []columntransform.CategoryMapping{{From: "female", To: "woman"}}, UnknownPolicy: columntransform.UnknownKeepOriginal,
			},
		},
	}
	patientSummary := recipe.Output{
		Name: "summary", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", Expr: recipe.Expression{Select: "root.gender"}},
		},
		ColumnTransformations: []recipe.ColumnTransformation{keepUnknown},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation", Aggregates: aggregates,
		}},
	}
	strictCategory := patientSummary
	strictCategory.Name = "strict_category"
	strictCategory.Traversals = nil
	strictCategory.ColumnTransformations = []recipe.ColumnTransformation{{
		Column: "category", Transformation: columntransform.ValueTransformation{
			Kind: columntransform.KindExactCategoryRecode,
			ExactCategoryRecode: &columntransform.ExactCategoryRecode{
				Mappings: []columntransform.CategoryMapping{{From: "female", To: "woman"}}, UnknownPolicy: columntransform.UnknownError,
			},
		},
	}}
	rawOutput := recipe.Output{
		Name: "raw_values", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "source_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "integer_value", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "boolean_value", Expr: recipe.Expression{Select: "root.valueBoolean"}},
			{Name: "string_value", Expr: recipe.Expression{Select: "root.valueString"}},
			{Name: "quantity_value", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
			{Name: "quantity_system", Expr: recipe.Expression{Select: "root.valueQuantity.system"}},
			{Name: "quantity_code", Expr: recipe.Expression{Select: "root.valueQuantity.code"}},
			{Name: "absence_reason", Expr: recipe.Expression{Select: "root.dataAbsentReason.text"}},
		},
	}
	mainCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-operator-oracle", TranslationVersion: "1",
		Outputs: []recipe.Output{patientSummary, strictCategory, rawOutput, windowedSummary},
	}, projects["main"], generation)
	wants := []operatorOracleOutputWant{
		{patientID: "patient-numeric", category: "woman", count: float64(5), exists: true, minimum: float64(0), maximum: float64(99), mean: float64(21.8), sum: float64(109), earliest: float64(2), latest: float64(3), unitSum: nil},
		{patientID: "patient-units", category: "woman", count: float64(0), exists: false, minimum: nil, maximum: nil, mean: nil, sum: nil, earliest: nil, latest: nil, unitSum: float64(1260)},
		{patientID: "patient-states", category: "Female", count: float64(1), exists: true, minimum: float64(0), maximum: float64(0), mean: float64(0), sum: float64(0), earliest: float64(0), latest: float64(0), unitSum: nil},
		{patientID: "patient-unknown-category", category: "mystery", count: float64(1), exists: true, minimum: float64(4), maximum: float64(4), mean: float64(4), sum: float64(4), earliest: float64(4), latest: float64(4), unitSum: nil},
	}
	windowedRows := executeOracleQuery(t, ctx, client, mainCompiled.Outputs[3])
	var windowedRow map[string]any
	for _, row := range windowedRows {
		if row["patient_id"] == "patient-units" {
			windowedRow = row
			break
		}
	}
	if windowedRow == nil {
		t.Fatalf("windowed output omitted patient-units: %#v", windowedRows)
	}
	for column, expected := range map[string]any{
		"window_count": float64(3), "window_exists": true, "window_minimum": float64(0), "window_maximum": float64(180),
		"window_mean": float64(120), "window_sum": float64(360), "window_first_ordered": float64(180),
	} {
		if got := windowedRow[column]; !reflect.DeepEqual(got, expected) {
			t.Errorf("patient-units.%s = %#v, want literal %#v", column, got, expected)
		}
	}
	windowedTrace := executeOracleTrace(t, ctx, client, mainCompiled.Outputs[3], "window_sum")
	windowedWant := []operatorOracleContributor{
		{resourceType: "Observation", resourceID: "obs-height-cm", value: float64(180)},
		{resourceType: "Observation", resourceID: "obs-height-m", value: float64(180)},
		{resourceType: "Observation", resourceID: "obs-height-zero", value: float64(0)},
	}
	if got := windowedTrace["patient-units"]; !reflect.DeepEqual(got, windowedWant) {
		t.Errorf("window_sum contributors = %#v, want exact eligible normalized contributors %#v", got, windowedWant)
	}
	windowedFirstTrace := executeOracleTrace(t, ctx, client, mainCompiled.Outputs[3], "window_first_ordered")
	windowedFirstWant := []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-height-m", value: float64(180)}}
	if got := windowedFirstTrace["patient-units"]; !reflect.DeepEqual(got, windowedFirstWant) {
		t.Errorf("window_first_ordered contributors = %#v, want first keyed eligible contributor %#v", got, windowedFirstWant)
	}
	windowMissingTimeCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-window-missing-time", TranslationVersion: "1", Outputs: []recipe.Output{windowedSummary},
	}, projects["window-missing-time"], generation)
	windowMissingTimeRows := executeOracleQuery(t, ctx, client, windowMissingTimeCompiled.Outputs[0])
	if len(windowMissingTimeRows) != 1 {
		t.Fatalf("windowed missing-time rows = %#v, want exactly one patient row", windowMissingTimeRows)
	}
	windowMissingTimeRow := windowMissingTimeRows[0]
	for column, expected := range map[string]any{
		"window_count": float64(1), "window_exists": true, "window_minimum": float64(25), "window_maximum": float64(25),
		"window_mean": float64(25), "window_sum": float64(25), "window_first_ordered": float64(25),
	} {
		if got := windowMissingTimeRow[column]; !reflect.DeepEqual(got, expected) {
			t.Errorf("missing-timestamp contributor patient.%s = %#v, want %#v", column, got, expected)
		}
	}
	windowMissingTimeTrace := executeOracleTrace(t, ctx, client, windowMissingTimeCompiled.Outputs[0], "window_sum")
	windowMissingTimeWant := []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-window-valid", value: float64(25)}}
	if got := windowMissingTimeTrace["patient-window-missing-time"]; !reflect.DeepEqual(got, windowMissingTimeWant) {
		t.Errorf("windowed missing-time trace = %#v, want only the timestamp-eligible contributor %#v", got, windowMissingTimeWant)
	}
	windowMalformedTimeCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-window-malformed-time", TranslationVersion: "1", Outputs: []recipe.Output{windowedSummary},
	}, projects["window-malformed-time"], generation)
	windowMalformedTimeRows, windowMalformedTimeErr := queryOracleOutput(ctx, client, windowMalformedTimeCompiled.Outputs[0])
	if windowMalformedTimeErr == nil || !strings.Contains(windowMalformedTimeErr.Error(), "TEMPORAL_PRECISION_UNSUPPORTED") {
		t.Fatalf("windowed malformed-time query returned rows=%#v error=%v; want TEMPORAL_PRECISION_UNSUPPORTED", windowMalformedTimeRows, windowMalformedTimeErr)
	}
	rows := executeOracleQuery(t, ctx, client, mainCompiled.Outputs[0])
	if len(rows) != len(wants) {
		t.Fatalf("summary rows = %#v, want %d patient rows", rows, len(wants))
	}
	byPatient := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		patientID, _ := row["patient_id"].(string)
		byPatient[patientID] = row
	}
	for _, want := range wants {
		row, ok := byPatient[want.patientID]
		if !ok {
			t.Fatalf("summary omitted %q: %#v", want.patientID, rows)
		}
		for column, expected := range map[string]any{
			"category": want.category, "count": want.count, "exists": want.exists,
			"minimum": want.minimum, "maximum": want.maximum, "mean": want.mean, "sum": want.sum,
			"earliest": want.earliest, "latest": want.latest, "unit_sum": want.unitSum,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("%s.%s = %#v, want literal %#v", want.patientID, column, got, expected)
			}
		}
	}

	contributorCases := []struct {
		patientID string
		column    string
		want      []operatorOracleContributor
	}{
		{patientID: "patient-numeric", column: "count", want: []operatorOracleContributor{
			{resourceType: "Observation", resourceID: "obs-after-window", value: float64(99)},
			{resourceType: "Observation", resourceID: "obs-early", value: float64(2)},
			{resourceType: "Observation", resourceID: "obs-tie-a", value: float64(3)},
			{resourceType: "Observation", resourceID: "obs-tie-b", value: float64(5)},
			{resourceType: "Observation", resourceID: "obs-zero", value: float64(0)},
		}},
		{patientID: "patient-numeric", column: "exists", want: []operatorOracleContributor{
			{resourceType: "Observation", resourceID: "obs-after-window", value: float64(99)},
			{resourceType: "Observation", resourceID: "obs-early", value: float64(2)},
			{resourceType: "Observation", resourceID: "obs-tie-a", value: float64(3)},
			{resourceType: "Observation", resourceID: "obs-tie-b", value: float64(5)},
			{resourceType: "Observation", resourceID: "obs-zero", value: float64(0)},
		}},
		{patientID: "patient-numeric", column: "minimum", want: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-zero", value: float64(0)}}},
		{patientID: "patient-numeric", column: "maximum", want: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-after-window", value: float64(99)}}},
		{patientID: "patient-numeric", column: "mean", want: []operatorOracleContributor{
			{resourceType: "Observation", resourceID: "obs-after-window", value: float64(99)},
			{resourceType: "Observation", resourceID: "obs-early", value: float64(2)},
			{resourceType: "Observation", resourceID: "obs-tie-a", value: float64(3)},
			{resourceType: "Observation", resourceID: "obs-tie-b", value: float64(5)},
			{resourceType: "Observation", resourceID: "obs-zero", value: float64(0)},
		}},
		{patientID: "patient-numeric", column: "sum", want: []operatorOracleContributor{
			{resourceType: "Observation", resourceID: "obs-after-window", value: float64(99)},
			{resourceType: "Observation", resourceID: "obs-early", value: float64(2)},
			{resourceType: "Observation", resourceID: "obs-tie-a", value: float64(3)},
			{resourceType: "Observation", resourceID: "obs-tie-b", value: float64(5)},
			{resourceType: "Observation", resourceID: "obs-zero", value: float64(0)},
		}},
		{patientID: "patient-numeric", column: "earliest", want: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-early", value: float64(2)}}},
		{patientID: "patient-numeric", column: "latest", want: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-tie-a", value: float64(3)}}},
		{patientID: "patient-units", column: "unit_sum", want: []operatorOracleContributor{
			{resourceType: "Observation", resourceID: "obs-height-cm", value: float64(180)},
			{resourceType: "Observation", resourceID: "obs-height-m", value: float64(180)},
			{resourceType: "Observation", resourceID: "obs-height-zero", value: float64(0)},
		}},
	}
	for _, tc := range contributorCases {
		trace := executeOracleTrace(t, ctx, client, mainCompiled.Outputs[0], tc.column)
		got := trace[tc.patientID]
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s contributors for %s = %#v, want exact literal %#v", tc.column, tc.patientID, got, tc.want)
		}
	}

	retained := executeOracleTrace(t, ctx, client, mainCompiled.Outputs[0], "patient_id")
	for _, want := range wants {
		expected := []operatorOracleContributor{{resourceType: "Patient", resourceID: want.patientID, value: want.patientID}}
		if got := retained[want.patientID]; !reflect.DeepEqual(got, expected) {
			t.Errorf("retained patient_id contributors for %s = %#v, want %#v", want.patientID, got, expected)
		}
	}
	categoryTrace, categoryCells := executeOracleTraceResults(t, ctx, client, mainCompiled.Outputs[0], "category")
	wantCategory := operatorOracleTraceResult{
		value: "woman", status: "VALUE",
		contributors: []operatorOracleContributor{{resourceType: "Patient", resourceID: "patient-numeric", value: "female"}},
	}
	if got := categoryCells["patient-numeric"]; !reflect.DeepEqual(got, wantCategory) {
		t.Errorf("recoded category trace = %#v, want literal %#v (omission %q)", got, wantCategory, categoryTrace.OmissionColumn)
	}

	rawRows := executeOracleQuery(t, ctx, client, mainCompiled.Outputs[2])
	rawByID := make(map[string]map[string]any, len(rawRows))
	for _, row := range rawRows {
		id, _ := row["source_id"].(string)
		rawByID[id] = row
	}
	rawCases := []struct {
		sourceID string
		state    operatorOracleRawState
		integer  any
		boolean  any
		text     any
		quantity any
		system   any
		code     any
		absence  any
	}{
		{sourceID: "obs-state-zero", state: operatorOracleInteger, integer: float64(0)},
		{sourceID: "obs-state-false", state: operatorOracleBoolean, boolean: false},
		{sourceID: "obs-state-empty", state: operatorOracleStateString, text: ""},
		{sourceID: "obs-state-missing", state: operatorOracleMissing},
		{sourceID: "obs-state-absence", state: operatorOracleAbsent, absence: "unknown"},
	}
	for _, tc := range rawCases {
		row, ok := rawByID[tc.sourceID]
		if !ok {
			t.Fatalf("raw-value output omitted fixture %s (%s)", tc.sourceID, tc.state)
		}
		for column, expected := range map[string]any{
			"integer_value": tc.integer, "boolean_value": tc.boolean,
			"string_value": tc.text, "quantity_value": tc.quantity,
			"quantity_system": tc.system, "quantity_code": tc.code, "absence_reason": tc.absence,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("%s.%s = %#v for %s, want %#v", tc.sourceID, column, got, tc.state, expected)
			}
		}
	}
	for _, tc := range []struct {
		sourceID string
		value    float64
		unitCode string
	}{
		{sourceID: "obs-height-cm", value: 180, unitCode: "cm"},
		{sourceID: "obs-height-m", value: 1.8, unitCode: "m"},
		{sourceID: "obs-height-zero", value: 0, unitCode: "cm"},
	} {
		row := rawByID[tc.sourceID]
		for column, expected := range map[string]any{
			"quantity_value": tc.value, "quantity_system": "http://unitsofmeasure.org", "quantity_code": tc.unitCode,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("%s.%s = %#v, want original coded measurement %#v", tc.sourceID, column, got, expected)
			}
		}
	}
	rawTrace := executeOracleTrace(t, ctx, client, mainCompiled.Outputs[2], "source_id")
	for _, tc := range rawCases {
		want := []operatorOracleContributor{{resourceType: "Observation", resourceID: tc.sourceID, value: tc.sourceID}}
		if got := rawTrace[tc.sourceID]; !reflect.DeepEqual(got, want) {
			t.Errorf("retained source_id contributors for %s = %#v, want %#v", tc.sourceID, got, want)
		}
	}
	stateTraceCases := []struct {
		sourceID     string
		column       string
		value        any
		status       string
		contributors []operatorOracleContributor
	}{
		{sourceID: "obs-state-zero", column: "integer_value", value: float64(0), status: "VALUE", contributors: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-state-zero", value: float64(0)}}},
		{sourceID: "obs-state-false", column: "boolean_value", value: false, status: "VALUE", contributors: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-state-false", value: false}}},
		{sourceID: "obs-state-empty", column: "string_value", value: "", status: "VALUE", contributors: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-state-empty", value: ""}}},
		{sourceID: "obs-state-missing", column: "integer_value", value: nil, status: "NO_MATCH", contributors: []operatorOracleContributor{}},
		{sourceID: "obs-state-absence", column: "absence_reason", value: "unknown", status: "VALUE", contributors: []operatorOracleContributor{{resourceType: "Observation", resourceID: "obs-state-absence", value: "unknown"}}},
	}
	for _, tc := range stateTraceCases {
		trace, cells := executeOracleTraceResults(t, ctx, client, mainCompiled.Outputs[2], tc.column)
		want := operatorOracleTraceResult{value: tc.value, status: tc.status, contributors: tc.contributors}
		if got := cells[tc.sourceID]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s trace for %s = %#v, want literal %#v (omission column %q)", tc.column, tc.sourceID, got, want, trace.OmissionColumn)
		}
	}

	strictRows, strictErr := queryOracleOutput(ctx, client, mainCompiled.Outputs[1])
	if strictErr == nil || !strings.Contains(strictErr.Error(), "CATEGORY_RECODE_UNKNOWN_VALUE") {
		t.Fatalf("strict exact category query returned rows=%#v error=%v; want CATEGORY_RECODE_UNKNOWN_VALUE for mystery", strictRows, strictErr)
	}

	uniqueTemporal := patientSummary
	uniqueTemporal.Name = "unique_temporal"
	uniqueTemporal.Traversals = []recipe.Traversal{{
		Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation",
		Aggregates: []recipe.Aggregate{ordered("latest", recipe.TemporalDescending, recipe.TemporalTieRequireUnique)},
	}}
	uniqueCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-unique-tie", TranslationVersion: "1", Outputs: []recipe.Output{uniqueTemporal},
	}, projects["main"], generation)
	uniqueRows, uniqueErr := queryOracleOutput(ctx, client, uniqueCompiled.Outputs[0])
	if uniqueErr == nil || !strings.Contains(uniqueErr.Error(), "TEMPORAL_TIE_AMBIGUOUS") {
		t.Fatalf("REQUIRE_UNIQUE temporal query returned rows=%#v error=%v; want TEMPORAL_TIE_AMBIGUOUS", uniqueRows, uniqueErr)
	}
	uniqueWindowed := windowedSummary
	uniqueWindowed.Name = "unique_windowed_temporal"
	uniqueAggregate := windowedSummary.Traversals[0].Aggregates[6]
	uniqueAggregate.Name = "window_first_ordered_unique"
	uniqueAggregate.OutputName = "window_first_ordered_unique"
	uniqueAggregate.Ordering = &recipe.TemporalOrdering{Timestamp: recipe.Expression{Select: "observation.effectiveDateTime"}, Direction: recipe.TemporalDescending, TiePolicy: recipe.TemporalTieRequireUnique}
	uniqueWindowed.Traversals = []recipe.Traversal{{
		Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation",
		Aggregates: []recipe.Aggregate{uniqueAggregate},
	}}
	uniqueWindowedCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-unique-windowed-tie", TranslationVersion: "1", Outputs: []recipe.Output{uniqueWindowed},
	}, projects["main"], generation)
	uniqueWindowedRows, uniqueWindowedErr := queryOracleOutput(ctx, client, uniqueWindowedCompiled.Outputs[0])
	if uniqueWindowedErr == nil || !strings.Contains(uniqueWindowedErr.Error(), "TEMPORAL_TIE_AMBIGUOUS") {
		t.Fatalf("windowed REQUIRE_UNIQUE query returned rows=%#v error=%v; want TEMPORAL_TIE_AMBIGUOUS for the two eligible Jan 2 items", uniqueWindowedRows, uniqueWindowedErr)
	}

	unsupportedOutput := recipe.Output{
		Name: "unsupported_unit", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields:     []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation", Aggregates: []recipe.Aggregate{unitAggregate}}},
	}
	unsupportedCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-unsupported-unit", TranslationVersion: "1", Outputs: []recipe.Output{unsupportedOutput},
	}, projects["unsupported"], generation)
	unsupportedRows, unsupportedErr := queryOracleOutput(ctx, client, unsupportedCompiled.Outputs[0])
	if unsupportedErr == nil || !strings.Contains(unsupportedErr.Error(), "UNIT_IDENTITY_UNKNOWN") {
		t.Fatalf("unsupported-unit query returned rows=%#v error=%v; want UNIT_IDENTITY_UNKNOWN for UCUM furlong", unsupportedRows, unsupportedErr)
	}

	missingTimeOutput := recipe.Output{
		Name: "missing_time", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", Alias: "observation", ToResourceType: "Observation",
			Aggregates: []recipe.Aggregate{ordered("latest", recipe.TemporalDescending, recipe.TemporalTieResourceKey)},
		}},
	}
	missingTimeCompiled := compileOracleBundle(t, recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-missing-temporal-time", TranslationVersion: "1", Outputs: []recipe.Output{missingTimeOutput},
	}, projects["missing-time"], generation)
	missingTimeRows, missingTimeErr := queryOracleOutput(ctx, client, missingTimeCompiled.Outputs[0])
	if missingTimeErr != nil || len(missingTimeRows) != 1 {
		t.Fatalf("missing-time temporal query returned rows=%#v error=%v; want one row with no selected value", missingTimeRows, missingTimeErr)
	}
	if latest, exists := missingTimeRows[0]["latest"]; !exists || latest != nil {
		t.Fatalf("missing-time latest = %#v (present=%t), want null", latest, exists)
	}
	missingTimeTrace := executeOracleTrace(t, ctx, client, missingTimeCompiled.Outputs[0], "latest")
	if contributors, exists := missingTimeTrace["patient-missing-time"]; !exists || len(contributors) != 0 {
		t.Fatalf("missing-time temporal trace = %#v (present=%t), want an empty contributor list", contributors, exists)
	}
}

func compileOracleBundle(t *testing.T, bundle recipe.Bundle, project, generation string) lower.CompiledRecipe {
	t.Helper()
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-s04", generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func executeOracleQuery(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput) []map[string]any {
	t.Helper()
	rows, err := queryOracleOutput(ctx, client, output)
	if err != nil {
		t.Fatalf("execute %s recipe: %v", output.Name, err)
	}
	return rows
}

func queryOracleOutput(ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput) ([]map[string]any, error) {
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0)
	err = client.QueryRows(ctx, rendered.Query, 500, rendered.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return rows, fmt.Errorf("%w\n%s", err, rendered.Query)
	}
	return rows, err
}

func executeOracleTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column string) map[string][]operatorOracleContributor {
	t.Helper()
	_, cells := executeOracleTraceResults(t, ctx, client, output, column)
	contributors := make(map[string][]operatorOracleContributor, len(cells))
	for identity, result := range cells {
		if result.omission != "" {
			t.Fatalf("unexpected %s trace omission %q", column, result.omission)
		}
		contributors[identity] = result.contributors
	}
	return contributors
}

func executeOracleTraceResults(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column string) (CompiledCellTraceQuery, map[string]operatorOracleTraceResult) {
	t.Helper()
	trace, err := CompileCellTraceOutputWithPolicy(output, column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile %s cell trace: %v", column, err)
	}
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, trace.Query, 500, trace.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute %s cell trace: %v\n%s", column, err, trace.Query)
	}
	project, _ := output.Plan.BindVars["project"].(string)
	return trace, oracleTraceResultsByIdentity(t, rows, trace, project)
}

func oracleTraceResultsByIdentity(t *testing.T, rows []map[string]any, trace CompiledCellTraceQuery, project string) map[string]operatorOracleTraceResult {
	t.Helper()
	results := make(map[string]operatorOracleTraceResult, len(rows))
	for _, row := range rows {
		parts, ok := row[trace.IdentityPartsColumn].([]any)
		if !ok || len(parts) != 2 {
			t.Fatalf("trace identity = %#v, want [project, resource key]", row[trace.IdentityPartsColumn])
		}
		key, ok := parts[1].(string)
		if !ok || !strings.HasPrefix(key, project+"_") {
			t.Fatalf("trace resource key = %#v, want %q prefix", parts[1], project+"_")
		}
		identity := strings.TrimPrefix(key, project+"_")
		value, ok := row[trace.ContributionsColumn].([]any)
		if !ok {
			t.Fatalf("trace contributions = %#v, want array", row[trace.ContributionsColumn])
		}
		items := make([]operatorOracleContributor, 0, len(value))
		for _, entry := range value {
			item, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("trace contributor = %#v, want object", entry)
			}
			resourceType, _ := item["resourceType"].(string)
			resourceID, _ := item["resourceId"].(string)
			items = append(items, operatorOracleContributor{resourceType: resourceType, resourceID: resourceID, value: item["value"]})
		}
		status, _ := row[trace.StatusColumn].(string)
		omission, _ := row[trace.OmissionColumn].(string)
		results[identity] = operatorOracleTraceResult{value: row[trace.ValueColumn], status: status, omission: omission, contributors: items}
	}
	return results
}
