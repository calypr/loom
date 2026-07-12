package recipecompiler

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/discovery"
	"github.com/calypr/loom/internal/fhirschema"
	"github.com/calypr/loom/internal/recipe"
)

func TestBuildPatientCohortProducesCompilerReadyTypedFilters(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	birthDateID := capabilityID(t, facts, "Patient", "birthDate")

	input := patientRecipe(genderID, birthDateID)
	input.Columns[0].OutputName = "Sex"
	input.Columns[1].OutputName = "Birth date"
	input.Filters = []recipe.Filter{
		{ColumnID: genderID, Operator: recipe.FilterEquals, Values: []string{"female"}},
		{ColumnID: birthDateID, Operator: recipe.FilterBetween, Values: []string{"1970-01-01", "2000-12-31"}},
		{ColumnID: genderID, Operator: recipe.FilterNotIn, Values: []string{"unknown", "other"}},
	}

	plan, err := Build(input, facts)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if plan.Recipe.Project != "project-a" || plan.Builder.Project != "project-a" {
		t.Errorf("project normalization = recipe %q builder %q, want project-a", plan.Recipe.Project, plan.Builder.Project)
	}
	if plan.Builder.RootResourceType != "Patient" || plan.Builder.RowGrain != dataframe.RowGrainPatient {
		t.Errorf("root builder = %+v, want Patient/patient", plan.Builder)
	}
	if plan.Builder.AuthResourcePaths != nil {
		t.Errorf("bridge unexpectedly invented authorization paths: %#v", plan.Builder.AuthResourcePaths)
	}
	if got, want := len(plan.Builder.Fields), 2; got != want {
		t.Fatalf("field count = %d, want %d", got, want)
	}
	if got := plan.Builder.Fields[0]; got.Name != "Sex" || got.FieldRef != genderID || got.Select != "gender" || got.ValueMode != "AUTO" {
		t.Errorf("gender field = %+v", got)
	}
	if got := plan.Builder.Fields[1]; got.Name != "Birth date" || got.FieldRef != birthDateID || got.Select != "birthDate" || got.ValueMode != "AUTO" {
		t.Errorf("birth date field = %+v", got)
	}

	// BETWEEN is an inclusive range: the lower and upper endpoints are
	// explicitly represented as GTE and LTE rather than a string expression.
	wantOperators := []dataframe.FilterOperator{
		dataframe.FilterEquals,
		dataframe.FilterGreaterEq,
		dataframe.FilterLessEq,
		dataframe.FilterNotEquals,
		dataframe.FilterNotEquals,
	}
	if got := filterOperators(plan.Builder.Filters); !reflect.DeepEqual(got, wantOperators) {
		t.Errorf("filter operators = %v, want %v", got, wantOperators)
	}
	if got := plan.Builder.Filters[0]; got.FieldKind != dataframe.FilterString || got.Values[0].String == nil || *got.Values[0].String != "female" {
		t.Errorf("gender typed filter = %+v", got)
	}
	for index, want := range []string{"1970-01-01", "2000-12-31"} {
		filter := plan.Builder.Filters[index+1]
		if filter.FieldKind != dataframe.FilterDate || len(filter.Values) != 1 || filter.Values[0].Date == nil || *filter.Values[0].Date != want {
			t.Errorf("between filter %d = %+v, want DATE %q", index, filter, want)
		}
	}

	compiled, err := dataframe.CompileRequest(plan.Builder, 25)
	if err != nil {
		t.Fatalf("CompileRequest(recipe builder) error = %v", err)
	}
	if compiled.RootResourceType != "Patient" || compiled.RowIdentity == nil || compiled.RowIdentity.Grain != dataframe.RowGrainPatient {
		t.Errorf("compiled query metadata = %+v", compiled)
	}
	if !strings.Contains(compiled.Query, " >= ") || !strings.Contains(compiled.Query, " <= ") {
		t.Errorf("compiled inclusive BETWEEN predicates missing from query:\n%s", compiled.Query)
	}
}

func TestBuildMapsEverySupportedRootFilterOperator(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	birthDateID := capabilityID(t, facts, "Patient", "birthDate")

	tests := []struct {
		name     string
		columnID string
		operator recipe.FilterOperator
		values   []string
		want     []dataframe.FilterOperator
		wantKind dataframe.FilterValueKind
	}{
		{name: "equals", columnID: genderID, operator: recipe.FilterEquals, values: []string{"female"}, want: []dataframe.FilterOperator{dataframe.FilterEquals}, wantKind: dataframe.FilterString},
		{name: "not equals", columnID: genderID, operator: recipe.FilterNotEquals, values: []string{"male"}, want: []dataframe.FilterOperator{dataframe.FilterNotEquals}, wantKind: dataframe.FilterString},
		{name: "in", columnID: genderID, operator: recipe.FilterIn, values: []string{"female", "male"}, want: []dataframe.FilterOperator{dataframe.FilterIn}, wantKind: dataframe.FilterString},
		{name: "not in", columnID: genderID, operator: recipe.FilterNotIn, values: []string{"unknown", "other"}, want: []dataframe.FilterOperator{dataframe.FilterNotEquals, dataframe.FilterNotEquals}, wantKind: dataframe.FilterString},
		{name: "exists", columnID: genderID, operator: recipe.FilterExists, want: []dataframe.FilterOperator{dataframe.FilterExists}, wantKind: dataframe.FilterString},
		{name: "missing", columnID: genderID, operator: recipe.FilterMissing, want: []dataframe.FilterOperator{dataframe.FilterMissing}, wantKind: dataframe.FilterString},
		{name: "contains", columnID: genderID, operator: recipe.FilterContains, values: []string{"em"}, want: []dataframe.FilterOperator{dataframe.FilterContains}, wantKind: dataframe.FilterString},
		{name: "greater than", columnID: birthDateID, operator: recipe.FilterGreaterThan, values: []string{"1970-01-01"}, want: []dataframe.FilterOperator{dataframe.FilterGreaterThan}, wantKind: dataframe.FilterDate},
		{name: "less than", columnID: birthDateID, operator: recipe.FilterLessThan, values: []string{"2000-12-31"}, want: []dataframe.FilterOperator{dataframe.FilterLessThan}, wantKind: dataframe.FilterDate},
		{name: "between inclusive", columnID: birthDateID, operator: recipe.FilterBetween, values: []string{"1970-01-01", "2000-12-31"}, want: []dataframe.FilterOperator{dataframe.FilterGreaterEq, dataframe.FilterLessEq}, wantKind: dataframe.FilterDate},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := patientRecipe(test.columnID)
			input.Filters = []recipe.Filter{{ColumnID: test.columnID, Operator: test.operator, Values: test.values}}
			plan, err := Build(input, facts)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if got := filterOperators(plan.Builder.Filters); !reflect.DeepEqual(got, test.want) {
				t.Errorf("operators = %v, want %v", got, test.want)
			}
			for _, filter := range plan.Builder.Filters {
				if filter.FieldKind != test.wantKind {
					t.Errorf("filter kind = %q, want %q", filter.FieldKind, test.wantKind)
				}
			}
		})
	}
}

func TestBuildRejectsStaleUnknownAndRawFHIRPathCapabilities(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	freshFacts := discovery.CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{{
			Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar", DocCount: 1,
		}},
	}

	tests := []struct {
		name  string
		id    string
		facts discovery.CatalogFacts
	}{
		{name: "stale opaque ID", id: genderID, facts: freshFacts},
		{name: "raw FHIR path", id: "gender", facts: facts},
		{name: "unknown opaque shaped ID", id: "col_" + strings.Repeat("0", 64), facts: facts},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := patientRecipe(test.id)
			plan, err := Build(input, test.facts)
			if !errors.Is(err, ErrColumnCapabilityUnavailable) {
				t.Fatalf("Build() error = %v, want ErrColumnCapabilityUnavailable", err)
			}
			if !errors.Is(err, discovery.ErrColumnUnavailable) {
				t.Errorf("Build() error = %v, want discovery.ErrColumnUnavailable cause", err)
			}
			if len(plan.Builder.Fields) != 0 || len(plan.Builder.Filters) != 0 {
				t.Errorf("failed bridge returned a usable builder: %+v", plan.Builder)
			}
			if strings.Contains(fmt.Sprintf("%#v", plan), "gender") {
				t.Errorf("raw recipe path leaked into failed plan: %#v", plan)
			}
		})
	}
}

func TestBuildRejectsRelatedPivotAndRepeatedChoices(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	observationValueID := capabilityID(t, facts, "Observation", "valueInteger")
	observationCodeID := capabilityID(t, facts, "Observation", "code")
	repeatedNameID := capabilityID(t, facts, "Patient", "name[].family")

	t.Run("related root column", func(t *testing.T) {
		_, err := Build(patientRecipe(genderID, observationValueID), facts)
		if !errors.Is(err, ErrRelatedResource) {
			t.Fatalf("Build() error = %v, want ErrRelatedResource", err)
		}
	})

	t.Run("composite pivot only choice", func(t *testing.T) {
		input := observationRecipe(observationCodeID)
		_, err := Build(input, facts)
		if !errors.Is(err, ErrPivotChoiceUnsupported) {
			t.Fatalf("Build() error = %v, want ErrPivotChoiceUnsupported", err)
		}
	})

	t.Run("repeated root column", func(t *testing.T) {
		_, err := Build(patientRecipe(repeatedNameID), facts)
		if !errors.Is(err, ErrRepeatedColumn) {
			t.Fatalf("Build() error = %v, want ErrRepeatedColumn", err)
		}
	})

	t.Run("repeated root filter needs quantifier", func(t *testing.T) {
		input := patientRecipe(repeatedNameID)
		input.Filters = []recipe.Filter{{ColumnID: repeatedNameID, Operator: recipe.FilterExists}}
		_, err := Build(input, facts)
		if !errors.Is(err, ErrRepeatedFilter) {
			t.Fatalf("Build() error = %v, want ErrRepeatedFilter", err)
		}
	})
}

func TestBuildRejectsInvalidTypedFilterValues(t *testing.T) {
	facts := bridgeFacts()
	booleanID := capabilityID(t, facts, "Patient", "deceasedBoolean")
	integerID := capabilityID(t, facts, "Patient", "multipleBirthInteger")
	dateID := capabilityID(t, facts, "Patient", "birthDate")

	tests := []struct {
		name  string
		id    string
		value string
	}{
		{name: "boolean", id: booleanID, value: "yes"},
		{name: "integer", id: integerID, value: "twelve"},
		{name: "date", id: dateID, value: "2024-99-99"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := patientRecipe(test.id)
			input.Filters = []recipe.Filter{{ColumnID: test.id, Operator: recipe.FilterEquals, Values: []string{test.value}}}
			_, err := Build(input, facts)
			if !errors.Is(err, ErrUnsupportedValueConversion) {
				t.Fatalf("Build() error = %v, want ErrUnsupportedValueConversion", err)
			}
		})
	}
}

func TestBuildRejectsPinnedRecipeWithoutGenerationBinding(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	input := patientRecipe(genderID)
	input.GenerationPolicy = recipe.GenerationPinned
	input.Generation = "generation-42"

	_, err := Build(input, facts)
	if !errors.Is(err, ErrGenerationBindingRequired) {
		t.Fatalf("Build() error = %v, want ErrGenerationBindingRequired", err)
	}
}

func TestBuildRejectsProjectMismatchedCatalogFacts(t *testing.T) {
	facts := bridgeFacts()
	genderID := capabilityID(t, facts, "Patient", "gender")
	input := patientRecipe(genderID)
	input.Project = "another-project"

	_, err := Build(input, facts)
	if !errors.Is(err, ErrCatalogProjectMismatch) {
		t.Fatalf("Build() error = %v, want ErrCatalogProjectMismatch", err)
	}
}

func patientRecipe(columnIDs ...string) recipe.Recipe {
	return productRecipe(recipe.TemplatePatientCohort, recipe.GrainPatient, columnIDs...)
}

func observationRecipe(columnIDs ...string) recipe.Recipe {
	return productRecipe(recipe.TemplateLabsObservations, recipe.GrainObservation, columnIDs...)
}

func productRecipe(template recipe.TemplateID, grain recipe.Grain, columnIDs ...string) recipe.Recipe {
	columns := make([]recipe.ColumnSelection, 0, len(columnIDs))
	for _, id := range columnIDs {
		columns = append(columns, recipe.ColumnSelection{ID: id})
	}
	return recipe.Recipe{
		Version:          recipe.VersionV1,
		Template:         template,
		TemplateVersion:  1,
		Project:          "project-a",
		GenerationPolicy: recipe.GenerationLatest,
		Grain:            grain,
		Columns:          columns,
		Destination:      recipe.Destination{Type: recipe.DestinationPreview},
	}
}

func bridgeFacts() discovery.CatalogFacts {
	return discovery.CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{
			{Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 3, DistinctValues: []string{"female", "male"}},
			{Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar", DocCount: 3, DistinctValues: []string{"1970-01-01", "2000-12-31"}},
			{Project: "project-a", ResourceType: "Patient", Path: "name[].family", Kind: "scalar", DocCount: 3, DistinctValues: []string{"Ng", "Smith"}},
			{Project: "project-a", ResourceType: "Patient", Path: "deceasedBoolean", Kind: "scalar", DocCount: 3, DistinctValues: []string{"false", "true"}},
			{Project: "project-a", ResourceType: "Patient", Path: "multipleBirthInteger", Kind: "scalar", DocCount: 3, DistinctValues: []string{"1", "2"}},
			{
				Project: "project-a", ResourceType: "Observation", Path: "code", Kind: "codeable_concept", DocCount: 3,
				PivotCandidate: true, PivotFamily: fhirschema.PivotFamilyObservationCodeValue,
				PivotColumnSelect: "code.coding[].display", PivotValueSelect: "valueInteger", PivotColumns: []string{"Hemoglobin"},
			},
			{Project: "project-a", ResourceType: "Observation", Path: "valueInteger", Kind: "scalar", DocCount: 3, DistinctValues: []string{"12", "13"}},
		},
	}
}

func capabilityID(t *testing.T, facts discovery.CatalogFacts, resourceType, canonicalPath string) string {
	t.Helper()
	snapshot, err := discovery.BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	resolver, err := discovery.NewCapabilityResolver(facts)
	if err != nil {
		t.Fatalf("NewCapabilityResolver() error = %v", err)
	}
	for _, column := range snapshot.Columns {
		resolved, err := resolver.ResolveColumn(column.ID)
		if err != nil {
			t.Fatalf("ResolveColumn(%q) error = %v", column.ID, err)
		}
		if resolved.ResourceType == resourceType && resolved.CanonicalPath == canonicalPath {
			return string(column.ID)
		}
	}
	t.Fatalf("no capability for %s.%s", resourceType, canonicalPath)
	return ""
}

func filterOperators(filters []dataframe.TypedFilter) []dataframe.FilterOperator {
	operators := make([]dataframe.FilterOperator, 0, len(filters))
	for _, filter := range filters {
		operators = append(operators, filter.Operator)
	}
	return operators
}
