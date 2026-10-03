package lower

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileExplicitGroupRowsUsesPinnedRevisionTerminal(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if len(output.Plan.Operations) != 1 || output.Plan.Operations[0].Kind != ir.PhysicalGroupRowsOp {
		t.Fatalf("grouped physical operations = %#v", output.Plan.Operations)
	}
	if output.RowIdentity == nil || output.RowIdentity.Grain != "groups" || strings.Join(output.RowIdentity.Fields, ",") != "group_revision_id,group_id" {
		t.Fatalf("group row identity = %#v", output.RowIdentity)
	}
	if output.Plan.BindVars["group_rows_revision_id"] != "grouprev_1" || output.Plan.BindVars["group_rows_resource_collection"] != "Patient" {
		t.Fatalf("group row binds = %#v", output.Plan.BindVars)
	}
	if output.Plan.BindVars["project"] != "project/1" {
		t.Fatalf("explicit group project bind = %#v, want canonical selection project", output.Plan.BindVars["project"])
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ASSERT(revision != null AND revision.state == \"COMPLETE\"",
		"SORT definition.ordinal ASC, definition.groupId ASC",
		"source_identity: {project: member.ref.project",
		"__loom_row_id: {group_revision_id: revision._key, group_id: definition.groupId}",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("group query missing %q:\n%s", want, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "FOR root IN") || strings.Contains(rendered.Query, "UNNEST") {
		t.Fatalf("group query scans roots or unnests unrelated repeated fields:\n%s", rendered.Query)
	}
}

func TestSourceOnlyGroupRowsEmitsRootContributorsForRelatedAppend(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows related append", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if len(output.Stages) != 2 || output.Stages[1].ID != "group_rows" {
		t.Fatalf("source-only explicit-group stages = %#v", output.Stages)
	}
	var contributor CompiledOutputColumn
	for _, column := range output.Stages[1].Columns {
		if column.Name == rootContributorSetColumn {
			contributor = column
		}
	}
	if !contributor.Internal || contributor.RootContributorResourceType != "Patient" || contributor.Kind != "string" || contributor.Cardinality != "many" {
		t.Fatalf("group stage root contributor column = %#v", contributor)
	}
	capabilities := make(map[recipe.ConstructionOperationKind]StageOperationCapability, len(output.Stages[1].Capabilities))
	for _, capability := range output.Stages[1].Capabilities {
		capabilities[capability.Operation] = capability
	}
	if capability := capabilities[recipe.ConstructionRelatedSourceOp]; !capability.Supported {
		t.Fatalf("source-only explicit group related-source capability = %#v", capability)
	}
	if got := compiledPublicSchemaNames(output.OutputSchema); !reflect.DeepEqual(got, []string{"group_id", "group_label", "group_ordinal", "members"}) {
		t.Fatalf("public group output schema changed: %#v", got)
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"__loom_root_contributor_keys: contributor_keys",
		"UNIQUE((FOR member_record IN member_records FILTER member_record.__loom_storage_key != null RETURN member_record.__loom_storage_key))",
		"source.auth_resource_path IN @auth_resource_paths",
		"resource.dataset_generation == member.ref.generation",
		"members: (FOR member_record IN member_records RETURN KEEP(member_record, \"source_identity\", \"payload\"))",
		"payload: source == null ? null : source.payload",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Errorf("group rows query omits emitted/scoped contributor invariant %q:\n%s", want, rendered.Query)
		}
	}
}

func TestRelatedCountAppendedToExplicitCohortUsesExactRootContributors(t *testing.T) {
	output := recipe.Output{
		Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "add_observation_count", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
					AnchorColumnID: "__loom_row_id", ChoiceID: "patient-observation-choice", SourceOccurrenceID: "observation-node",
					Source:            recipe.ConstructionRelatedFieldSource{CandidateID: "observation-id", NodeID: "observation-node", ResourceType: "Observation", Path: "Observation.id", Cardinality: "required_one", LogicalType: "string"},
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", Form: "COUNT", OutputColumnID: "observation-count",
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group_id"}, {ID: "group_label", Name: "group_label"},
					{ID: "group_ordinal", Name: "group_ordinal"}, {ID: "members", Name: "members"},
					{ID: "observation-count", Name: "observation_count"},
				},
			}},
		},
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort related count", TranslationVersion: "test", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	result := compiled.Outputs[0]
	if result.Plan.StageSequence == nil || len(result.Plan.StageSequence.Stages) != 2 ||
		result.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageCohortGroupOp ||
		result.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageRelatedSourceOp {
		t.Fatalf("cohort related COUNT stage sequence = %#v", result.Plan.StageSequence)
	}
	related := result.Plan.StageSequence.Stages[1]
	if related.RelatedSource == nil || related.RelatedSource.Form != "COUNT" || related.RelatedSource.RootContributorColumn != rootContributorSetColumn {
		t.Fatalf("related COUNT did not consume exact cohort contributors: %#v", related.RelatedSource)
	}
	if !result.Plan.StageSequence.Stages[0].CohortGroup.PreserveMissingMembers {
		t.Fatal("adding a related source to an unfiltered explicit cohort must preserve pinned missing members")
	}
	rendered, err := aql.RenderPhysicalPlan(result.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FILTER source == null OR source._key IN",
		"FILTER source == null OR @cohort_auth_resource_paths_unrestricted == true OR source.auth_resource_path IN @cohort_auth_resource_paths",
		"payload: source == null ? null : source.payload",
		"__loom_storage_key: source == null ? null : source._key",
		"FOR __loom_construction_related_0_root_key_1 IN SORTED_UNIQUE(__loom_construction_input_1.__loom_root_contributor_keys)",
		"DOCUMENT(@@root_collection, __loom_construction_related_0_root_key_1)",
		"__loom_construction_related_0_root_1._key == __loom_construction_related_0_root_key_1",
		"KEEP(",
		"dataset_generation",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Errorf("cohort-related COUNT query omits member/provenance invariant %q:\n%s", want, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "__loom_construction_related_0_root_1._key IN __loom_construction_input_1.__loom_root_contributor_keys") {
		t.Fatalf("related source uses a correlated collection IN scan instead of exact key lookups:\n%s", rendered.Query)
	}
	cloned, err := aql.RenderPhysicalPlan(ir.ClonePhysicalPlan(result.Plan))
	if err != nil {
		t.Fatalf("render cloned related COUNT plan: %v", err)
	}
	if !strings.Contains(cloned.Query, "DOCUMENT(@@root_collection, __loom_construction_related_0_root_key_1)") {
		t.Fatalf("cloning dropped the typed contributor key lookup:\n%s", cloned.Query)
	}
	findRelatedSubplan := func(plan *ir.PhysicalPlan) *ir.PhysicalSubplan {
		for stageIndex := range plan.StageSequence.Stages {
			for projectionIndex := range plan.StageSequence.Stages[stageIndex].OutputProjections {
				projection := &plan.StageSequence.Stages[stageIndex].OutputProjections[projectionIndex]
				if projection.Name != "observation_count" || projection.Expression == nil || projection.Expression.Call == nil || len(projection.Expression.Call.Args) != 1 {
					continue
				}
				expression := projection.Expression.Call.Args[0]
				if expression.Kind == ir.PhysicalSubplanExpression {
					return expression.Subplan
				}
			}
		}
		return nil
	}
	keySetPlan := ir.ClonePhysicalPlan(result.Plan)
	keySetSubplan := findRelatedSubplan(&keySetPlan)
	if keySetSubplan == nil || len(keySetSubplan.Operations) == 0 || keySetSubplan.Operations[0].KeySetLookup == nil {
		t.Fatalf("cloned plan has no typed contributor key lookup: %#v", keySetSubplan)
	}
	keySetLookup := keySetSubplan.Operations[0].KeySetLookup
	if !reflect.DeepEqual(keySetLookup.Keys.Path, []string{rootContributorSetColumn}) {
		t.Fatalf("cloned contributor lookup lost its typed keys value: %#v", keySetLookup)
	}
	keySetLookup.Keys.Path[0] = "mutated"
	originalSubplan := findRelatedSubplan(&result.Plan)
	if originalSubplan == nil || originalSubplan.Operations[0].KeySetLookup.Keys.Path[0] != rootContributorSetColumn {
		t.Fatal("cloned key-set path aliases original plan")
	}
	invalidKeysPlan := ir.ClonePhysicalPlan(result.Plan)
	findRelatedSubplan(&invalidKeysPlan).Operations[0].KeySetLookup.Keys.Variable = "unbound_key_source"
	if err := invalidKeysPlan.Validate(); err == nil {
		t.Fatal("physical validation accepted an out-of-scope contributor key-set value")
	}
	topLevelLookup := ir.ClonePhysicalPlan(result.Plan)
	topLevelLookup.Operations = append([]ir.PhysicalOperation{{
		Kind: ir.PhysicalKeySetLookupOp,
		KeySetLookup: &ir.PhysicalKeySetLookup{
			Variable: "root_doc", KeyVariable: "root_key", CollectionBindKey: "root_collection",
			Keys: ir.PhysicalValue{Variable: "input", Path: []string{rootContributorSetColumn}},
		},
	}}, topLevelLookup.Operations...)
	if err := topLevelLookup.Validate(); err == nil || !strings.Contains(err.Error(), "only legal inside a typed subplan") {
		t.Fatalf("top-level KEY_SET_LOOKUP was not rejected at the physical-plan boundary: %v", err)
	}
	for _, omission := range []struct {
		name  string
		match func(ir.PhysicalOperation) bool
	}{
		{name: "project", match: func(operation ir.PhysicalOperation) bool {
			return operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil &&
				reflect.DeepEqual(operation.Filter.Predicate.Left.Path, []string{"project"})
		}},
		{name: "dataset generation", match: func(operation ir.PhysicalOperation) bool {
			return operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil &&
				reflect.DeepEqual(operation.Filter.Predicate.Left.Path, []string{"dataset_generation"})
		}},
		{name: "authorization", match: func(operation ir.PhysicalOperation) bool {
			return operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Predicate.Right != nil &&
				operation.Filter.Predicate.Right.BindKey == "scope_allowed"
		}},
	} {
		t.Run(omission.name, func(t *testing.T) {
			invalidPlan := ir.ClonePhysicalPlan(result.Plan)
			subplan := findRelatedSubplan(&invalidPlan)
			if subplan == nil {
				t.Fatal("cloned plan has no typed related subplan")
			}
			removed := false
			for index, operation := range subplan.Operations {
				if !omission.match(operation) {
					continue
				}
				subplan.Operations = append(subplan.Operations[:index], subplan.Operations[index+1:]...)
				removed = true
				break
			}
			if !removed {
				t.Fatalf("compiled key-set route has no %s scope operation", omission.name)
			}
			if err := invalidPlan.Validate(); err == nil {
				t.Fatalf("physical validation accepted a key-set lookup without %s scope", omission.name)
			}
		})
	}
	for _, form := range []string{"PRESENCE", "ALL"} {
		variant := output
		construction := *output.Construction
		construction.SourceColumns = append([]recipe.StageColumn(nil), output.Construction.SourceColumns...)
		construction.Steps = append([]recipe.ConstructionStep(nil), output.Construction.Steps...)
		step := construction.Steps[0]
		relatedSource := *step.Operation.RelatedSource
		relatedSource.Form = form
		step.Operation.RelatedSource = &relatedSource
		construction.Steps[0] = step
		variant.Construction = &construction
		variantPlan, buildErr := semantic.BuildRecipePlan(recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort related " + strings.ToLower(form), TranslationVersion: "test",
			Outputs: []recipe.Output{variant},
		}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
		if buildErr != nil {
			t.Fatalf("build %s related cohort plan: %v", form, buildErr)
		}
		variantResolved, resolveErr := semantic.ResolveRecipePlan(variantPlan, "project-1", "generation-1")
		if resolveErr != nil {
			t.Fatalf("resolve %s related cohort plan: %v", form, resolveErr)
		}
		variantCompiled, compileErr := CompileResolvedRecipePlan(variantResolved, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile %s related cohort plan: %v", form, compileErr)
		}
		variantRendered, renderErr := aql.RenderPhysicalPlan(variantCompiled.Outputs[0].Plan)
		if renderErr != nil {
			t.Fatalf("render %s related cohort plan: %v", form, renderErr)
		}
		if !strings.Contains(variantRendered.Query, "FOR __loom_construction_related_0_root_key_1 IN SORTED_UNIQUE(__loom_construction_input_1.__loom_root_contributor_keys)") ||
			strings.Contains(variantRendered.Query, "__loom_construction_related_0_root_1._key IN __loom_construction_input_1.__loom_root_contributor_keys") {
			t.Fatalf("%s did not use exact contributor key lookup:\n%s", form, variantRendered.Query)
		}
	}
	if rendered.BindVars["cohort_dataset_generation"] != "generation-1" {
		t.Fatalf("cohort generation scope = %#v", rendered.BindVars["cohort_dataset_generation"])
	}
}

func TestCohortMissingMembersFollowPreCohortFilterBoundary(t *testing.T) {
	compile := func(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) (bool, string) {
		t.Helper()
		plan, err := semantic.BuildRecipePlan(recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort missing member policy", TranslationVersion: "test",
			Outputs: []recipe.Output{output},
		}, bindings)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		physical := compiled.Outputs[0].Plan
		var cohort *ir.PhysicalStageCohortGroup
		for _, stage := range physical.StageSequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				cohort = stage.CohortGroup
				break
			}
		}
		if cohort == nil {
			t.Fatal("compiled plan has no explicit cohort stage")
		}
		rendered, err := aql.RenderPhysicalPlan(physical)
		if err != nil {
			t.Fatal(err)
		}
		return cohort.PreserveMissingMembers, rendered.Query
	}

	bindings := recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"}
	base := recipe.Output{
		Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
		Fields:       []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		GroupRows:    &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}},
	}
	postCohortFilter := base
	postCohortFilter.GroupRows = &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}
	postCohortFilter.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}}
	groupID := "many"
	postCohortFilter.Construction.Steps = []recipe.ConstructionStep{{
		ID: "keep_group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "group_id", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &groupID}},
		}},
		Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group_id", Type: "string"}, {ID: "group_label", Name: "group_label", Type: "string"},
			{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"}, {ID: "members", Name: "members", Type: "array"}},
	}}
	if preserve, query := compile(t, postCohortFilter, bindings); !preserve || !strings.Contains(query, "source == null OR source._key IN") {
		t.Fatalf("post-cohort filter changed the pinned member list: preserve=%t query=%s", preserve, query)
	}

	preCohortFilter := base
	preCohortFilter.GroupRows = &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}
	preCohortFilter.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}}
	preCohortFilter.GroupRows.AfterStepID = "exclude_missing"
	patientID := "missing"
	preCohortFilter.Construction.Steps = []recipe.ConstructionStep{{
		ID: "exclude_missing", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "patient_id", Operator: recipe.FilterNotEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &patientID}},
		}},
		Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
	}}
	if preserve, query := compile(t, preCohortFilter, bindings); preserve || !strings.Contains(query, "source != null AND source._key IN") {
		t.Fatalf("pre-cohort filter retained a missing member that could not reach its input: preserve=%t query=%s", preserve, query)
	}

	legacySourceFilter := postCohortFilter
	legacySourceFilter.GroupRows = &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}
	legacySourceFilter.Construction = &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}, Steps: append([]recipe.ConstructionStep(nil), postCohortFilter.Construction.Steps...)}
	legacySourceFilter.Filters = []recipe.Filter{{
		Select: "root.id", Operator: recipe.FilterNotEquals,
		Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &patientID}},
	}}
	if preserve, query := compile(t, legacySourceFilter, bindings); preserve || !strings.Contains(query, "source != null AND source._key IN") {
		t.Fatalf("legacy source filter retained a missing member that could not reach its input: preserve=%t query=%s", preserve, query)
	}
}

func TestCohortMissingMembersTrackPreAnchorDropStages(t *testing.T) {
	route := []recipe.ConstructionRelatedRouteStep{{
		EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}

	compile := func(t *testing.T, step recipe.ConstructionStep, fields []recipe.Field, sourceColumns []recipe.StageColumn) (bool, string) {
		t.Helper()
		output := recipe.Output{
			Name: "CohortDropBoundary", RootResourceType: "Patient", RowGrain: "groups",
			Fields:       fields,
			GroupRows:    &recipe.GroupRows{RevisionID: "grouprev_boundary", UnassignedMemberPolicy: "EXCLUDE", AfterStepID: step.ID},
			Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: []recipe.ConstructionStep{step}},
		}
		bindings := recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"}
		plan, err := semantic.BuildRecipePlan(recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output},
		}, bindings)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		physical := compiled.Outputs[0].Plan
		for _, stage := range physical.StageSequence.Stages {
			if stage.Kind == ir.PhysicalStageCohortGroupOp {
				rendered, renderErr := aql.RenderPhysicalPlan(physical)
				if renderErr != nil {
					t.Fatal(renderErr)
				}
				return stage.CohortGroup.PreserveMissingMembers, rendered.Query
			}
		}
		t.Fatal("compiled plan has no COHORT_GROUP stage")
		return false, ""
	}

	patientFields := []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}
	patientColumns := []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}}
	for _, test := range []struct {
		name      string
		step      recipe.ConstructionStep
		fields    []recipe.Field
		columns   []recipe.StageColumn
		wantKeep  bool
		wantQuery string
	}{
		{
			name: "related eligibility",
			step: recipe.ConstructionStep{
				ID: "eligible", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
					AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: route, ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
				}},
				Outputs: patientColumns,
			},
			fields: patientFields, columns: patientColumns, wantKeep: false, wantQuery: "FILTER",
		},
		{
			name: "related expand exclude",
			step: recipe.ConstructionStep{
				ID: "expand", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: route, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "observation_id",
				}},
				Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}, {ID: "observation_id", Name: "observation_id", Type: "string"}},
			},
			fields: patientFields, columns: patientColumns, wantKeep: false, wantQuery: "LENGTH(",
		},
		{
			name: "unpivot drop",
			step: recipe.ConstructionStep{
				ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
					ConstructionID: "unpivot_boundary", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "active", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: cohortBoundaryStringPointer("active")}}},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id", NullRowPolicy: recipe.UnpivotNullDrop,
				}},
				Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}, {ID: "measure_id", Name: "measure", Type: "string"}, {ID: "value_id", Name: "value", Type: "boolean"}},
			},
			fields:   append(append([]recipe.Field(nil), patientFields...), recipe.Field{Name: "active", ColumnID: "active", Expr: recipe.Expression{Select: "root.active"}}),
			columns:  append(append([]recipe.StageColumn(nil), patientColumns...), recipe.StageColumn{ID: "active", Name: "active", Type: "boolean", Nullable: true}),
			wantKeep: false, wantQuery: "FILTER",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			preserve, query := compile(t, test.step, test.fields, test.columns)
			if preserve != test.wantKeep {
				t.Fatalf("PreserveMissingMembers = %t, want %t", preserve, test.wantKeep)
			}
			if !strings.Contains(query, test.wantQuery) {
				t.Fatalf("rendered query omits operation predicate %q:\n%s", test.wantQuery, query)
			}
		})
	}
}

func cohortBoundaryStringPointer(value string) *string { return &value }

func TestConstructionOperationCanDropRowsUsesSuccessfulPolicies(t *testing.T) {
	tests := []struct {
		name      string
		operation recipe.ConstructionOperation
		want      bool
	}{
		{name: "filter", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp}, want: true},
		{name: "related eligibility", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp}, want: true},
		{name: "related expand exclude", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{EmptyPolicy: recipe.ExpansionExclude}}, want: true},
		{name: "related expand preserve", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{EmptyPolicy: recipe.ExpansionPreserveParent}}},
		{name: "related expand error", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{EmptyPolicy: recipe.ExpansionError}}},
		{name: "unpivot drop", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{NullRowPolicy: recipe.UnpivotNullDrop}}, want: true},
		{name: "unpivot preserve", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{NullRowPolicy: recipe.UnpivotNullPreserve}}},
		{name: "group missing key exclude", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{MissingKeyPolicy: recipe.ConstructionGroupMissingKeyExclude}}, want: true},
		{name: "group missing key group", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup}}},
		{name: "group missing key error", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{MissingKeyPolicy: recipe.ConstructionGroupMissingKeyError}}},
		{name: "ordinary pivot", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp}},
		{name: "coded pivot", operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedPivotOp}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := constructionOperationCanDropRows(test.operation); got != test.want {
				t.Fatalf("constructionOperationCanDropRows() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestExplicitGroupRowsMemberValuesRetainTypedStageCapabilities(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows with member values", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
			Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueAll}},
			},
		}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if output.Plan.StageSequence != nil {
		t.Fatal("expected the non-composed explicit-group path without authored construction")
	}
	if len(output.Stages) != 2 || output.Stages[1].Operation != "GROUP_ROWS" {
		t.Fatalf("explicit-group stages = %#v", output.Stages)
	}
	stage := output.Stages[1]
	wantColumns := map[string]struct{ kind, cardinality string }{
		"group_label":   {kind: "string", cardinality: "required_one"},
		"group_ordinal": {kind: "integer", cardinality: "required_one"},
		"specimen_id":   {kind: "string", cardinality: "many"},
	}
	for _, column := range stage.Columns {
		if want, ok := wantColumns[column.ID]; ok {
			if column.Kind != want.kind || column.Cardinality != want.cardinality {
				t.Errorf("column %q has type %s/%s, want %s/%s", column.ID, column.Kind, column.Cardinality, want.kind, want.cardinality)
			}
			delete(wantColumns, column.ID)
		}
	}
	if len(wantColumns) != 0 {
		t.Errorf("group stage omitted required typed columns: %#v", wantColumns)
	}
	capabilities := make(map[recipe.ConstructionOperationKind]StageOperationCapability, len(stage.Capabilities))
	for _, capability := range stage.Capabilities {
		capabilities[capability.Operation] = capability
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionFilterOp, recipe.ConstructionOperationKind("ROW_VALUES")} {
		if capability, ok := capabilities[operation]; !ok || !capability.Supported {
			t.Errorf("GROUP_ROWS capability %q = %#v, supported = %t; want supported", operation, capability, ok && capability.Supported)
		}
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionCodedGroupOp, recipe.ConstructionCodedPivotOp} {
		if capability, ok := capabilities[operation]; !ok || capability.Supported {
			t.Errorf("GROUP_ROWS capability %q = %#v, want the schema-aware typed capability to remain unsupported", operation, capability)
		}
	}
}

func TestCompileExplicitGroupRowsWithSourceOnlyConstructionMetadata(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows with source metadata", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
			Fields:       []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
			Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}}, Steps: []recipe.ConstructionStep{}},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "gender", Policy: recipe.ConstructionRowValueOne}},
			},
		}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatalf("source-only projection metadata should not block explicit groups: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile explicit groups with source-only metadata: %v", err)
	}
	output := compiled.Outputs[0]
	if len(output.Plan.Operations) != 1 || output.Plan.Operations[0].Kind != ir.PhysicalGroupRowsOp {
		t.Fatalf("grouped physical operations = %#v, want the explicit-group source", output.Plan.Operations)
	}
	if got := compiledSchemaNames(output.OutputSchema); !equalStringSlices(got, []string{"group_revision_id", "group_id", "group_label", "group_ordinal", "members", "__loom_row_id", "gender", rootContributorSetColumn}) {
		t.Fatalf("source-only group output schema = %#v", got)
	}
	if len(output.Stages) != 2 || output.Stages[1].Operation != "GROUP_ROWS" {
		t.Fatalf("source-only explicit-group stage descriptors = %#v", output.Stages)
	}
	for _, column := range output.Stages[1].Columns {
		if column.ID == "" || column.Name == "" || column.Label == "" {
			t.Fatalf("group-row stage column is missing compiler-owned identity or presentation metadata: %#v", column)
		}
	}
}

func TestCompileCohortAtPersistedConstructionBoundary(t *testing.T) {
	selectedID := "a"
	selectedGroup := "pair"
	output := recipe.Output{
		Name: "CohortComposition", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "keep_a", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedID}},
					}},
					Outputs: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
				},
				{
					ID: "keep_pair", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "group_id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedGroup}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group_id", Type: "string"},
						{ID: "group_label", Name: "group_label", Type: "string"},
						{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
						{ID: "members", Name: "members", Type: "array"},
						{ID: "id", Name: "id", Type: "array"},
					},
				},
			},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "keep_a",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort composition", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Outputs[0]
	if got.Plan.StageSequence == nil {
		t.Fatal("composed cohort output has no typed stage sequence")
	}
	want := []string{"keep_a", recipe.ConstructionCohortGroupStageID, "keep_pair"}
	if len(got.Plan.StageSequence.Stages) != len(want) {
		t.Fatalf("effective stage sequence = %#v", got.Plan.StageSequence.Stages)
	}
	for index, id := range want {
		if got.Plan.StageSequence.Stages[index].ID != id {
			t.Fatalf("stage %d ID = %q, want %q", index, got.Plan.StageSequence.Stages[index].ID, id)
		}
	}
	if got.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageCohortGroupOp {
		t.Fatalf("cohort stage kind = %q", got.Plan.StageSequence.Stages[1].Kind)
	}
	if got.Stages[2].Columns == nil {
		t.Fatal("cohort stage omitted the compiler-owned output capability schema")
	}
	capabilities := make(map[recipe.ConstructionOperationKind]StageOperationCapability, len(got.Stages[1].Capabilities))
	for _, capability := range got.Stages[1].Capabilities {
		capabilities[capability.Operation] = capability
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionFilterOp} {
		if capability, ok := capabilities[operation]; !ok || !capability.Supported {
			t.Errorf("composed cohort capability %q = %#v, supported = %t; want supported", operation, capability, ok && capability.Supported)
		}
	}
	rendered, err := aql.RenderPhysicalPlan(got.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, wantQuery := range []string{
		"LET __loom_construction_stage_1 = (",
		"FILTER", "_key IN __loom_physical_construction_cohort_root_contributors", "source == null ? null :", "EXPLICIT_GROUP_SOURCE_STALE",
		"LET __loom_construction_stage_3 = (", "group_id",
	} {
		if !strings.Contains(rendered.Query, wantQuery) {
			t.Fatalf("composed AQL is missing %q:\n%s", wantQuery, rendered.Query)
		}
	}
}

func TestCohortAfterGroupUsesRetainedRootContributorSet(t *testing.T) {
	output := recipe.Output{
		Name: "GroupedCohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{
			{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "status", ColumnID: "status", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}, {ID: "status", Name: "status", Type: "boolean"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status", Keys: []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_key"}},
					Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}},
					MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				}},
				Outputs: []recipe.StageColumn{{ID: "status_key", Name: "status_key", Type: "boolean"}, {ID: "records", Name: "records", Type: "integer"}},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE", AfterStepID: "group_status",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "grouped cohort", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	compiled := physical.Outputs[0]
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 {
		t.Fatalf("group→cohort plan has no two-stage sequence: %#v", compiled.Plan.StageSequence)
	}
	group, cohort := compiled.Plan.StageSequence.Stages[0], compiled.Plan.StageSequence.Stages[1]
	if group.Group == nil || group.Group.RootContributorOutputColumn != rootContributorSetColumn {
		t.Fatalf("preceding GROUP did not retain root contributor identity: %#v", group)
	}
	if cohort.CohortGroup == nil || cohort.CohortGroup.ContributorInputColumn != rootContributorSetColumn {
		t.Fatalf("cohort did not consume the compiler-proven contributor set: %#v", cohort)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "_key IN __loom_physical_construction_cohort_root_contributors") || !strings.Contains(rendered.Query, "source == null ? null :") || !strings.Contains(rendered.Query, "UNIQUE(FLATTEN") {
		t.Fatalf("cohort did not intersect pinned members with retained grouped contributors:\n%s", rendered.Query)
	}
}
