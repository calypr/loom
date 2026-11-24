package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileRelatedExpandSupportsScalarContributorPredicate(t *testing.T) {
	output := constructionTestOutput()
	specimenID := "specimen-123"
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "patient-observations", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES",
				ContributorPredicate: &recipe.ConstructionRelatedPredicate{
					CandidateID: "observation-id", Operator: recipe.FilterEquals,
					Value: &recipe.FilterValue{Kind: recipe.FilterString, String: &specimenID},
				},
				ContributorSource: &recipe.ConstructionRelatedFieldSource{
					CandidateID: "observation-id", NodeID: "observation-node", ResourceType: "Observation",
					Path: "Observation.id", Cardinality: "required_one", LogicalType: "string",
				},
				ContributorChoiceID: "observation-id-choice",
				EmptyPolicy:         recipe.ExpansionExclude, RelatedRecordColumnID: "observation_id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: "Group"},
				{ID: "category_id", Name: "category", Label: "Category"},
				{ID: "amount_id", Name: "amount", Label: "Amount"},
				{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string"},
			},
		}},
	}

	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".id", "== @related_expand_contributor_value"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered related expansion omitted %q: %s", expected, rendered.Query)
		}
	}
	if rendered.BindVars["related_expand_contributor_value"] != specimenID {
		t.Fatalf("related expansion contributor bind = %#v, want %q", rendered.BindVars["related_expand_contributor_value"], specimenID)
	}
}

func TestCompileRelatedExpandSupportsRepeatedScalarContributorPredicate(t *testing.T) {
	code := "target-code"
	output := constructionTestOutput()
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{{
			ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "patient-observations", TargetNodeID: "observation-node", TargetResourceType: "Observation",
				Route: []recipe.ConstructionRelatedRouteStep{{
					EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorPolicy: "ALL_MATCHES",
				ContributorPredicate: &recipe.ConstructionRelatedPredicate{
					CandidateID: "observation-code", Quantifier: recipe.QuantifierAny, Operator: recipe.FilterEquals,
					Value: &recipe.FilterValue{Kind: recipe.FilterCode, Code: &recipe.CodeValue{Code: code}},
				},
				ContributorSource: &recipe.ConstructionRelatedFieldSource{
					CandidateID: "observation-code", NodeID: "observation-node", ResourceType: "Observation",
					Path: "Observation.category[].coding[].code", Cardinality: "many", LogicalType: "code",
					RepeatedBoundaries: []recipe.ConstructionRelatedRepeatedBoundary{
						{Path: "category[]", MaxItems: 8},
						{Path: "category[].coding[]", MaxItems: 8},
					},
				},
				ContributorChoiceID: "observation-code-choice",
				EmptyPolicy:         recipe.ExpansionExclude, RelatedRecordColumnID: "observation_id",
			}},
			Outputs: []recipe.StageColumn{
				{ID: "group_id", Name: "group", Label: "Group"},
				{ID: "category_id", Name: "category", Label: "Category"},
				{ID: "amount_id", Name: "amount", Label: "Amount"},
				{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string"},
			},
		}},
	}
	if err := output.Construction.Validate(output.Fields); err != nil {
		t.Fatalf("valid repeated scalar contributor was rejected: %v", err)
	}

	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Kind != ir.PhysicalStageRelatedExpandOp || stage.RelatedExpand == nil || !stage.RelatedExpand.RelatedRecords.Unique {
		t.Fatalf("repeated predicate changed terminal-record uniqueness: %#v", stage)
	}
	filterFound := false
	for _, operation := range stage.RelatedExpand.RelatedRecords.Operations {
		if operation.Filter == nil || operation.Filter.Expression == nil || operation.Filter.Expression.Comparison == nil {
			continue
		}
		comparison := operation.Filter.Expression.Comparison
		if comparison.Operator == string(recipe.FilterEquals) {
			filterFound = true
			if string(comparison.Quantifier) != string(recipe.QuantifierAny) {
				t.Fatalf("repeated contributor predicate quantifier = %q, want ANY", comparison.Quantifier)
			}
		}
	}
	if !filterFound {
		t.Fatal("related terminal subplan omitted the repeated contributor filter")
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".category", ".coding", "== @related_expand_contributor_value"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered repeated related expansion omitted %q: %s", expected, rendered.Query)
		}
	}
	if got := rendered.BindVars["related_expand_contributor_value"]; got != code {
		t.Fatalf("CODE predicate bound %#v, want its code string %q", got, code)
	}

	cloneWith := func(change func(*recipe.ConstructionRelatedFieldSource, *recipe.ConstructionRelatedPredicate)) recipe.Output {
		cloned := output
		construction := *output.Construction
		steps := append([]recipe.ConstructionStep(nil), construction.Steps...)
		operation := steps[0].Operation
		related := *operation.RelatedExpand
		source := *related.ContributorSource
		source.RepeatedBoundaries = append([]recipe.ConstructionRelatedRepeatedBoundary(nil), source.RepeatedBoundaries...)
		predicate := *related.ContributorPredicate
		change(&source, &predicate)
		related.ContributorSource = &source
		related.ContributorPredicate = &predicate
		operation.RelatedExpand = &related
		steps[0].Operation = operation
		construction.Steps = steps
		cloned.Construction = &construction
		return cloned
	}
	existsOutput := cloneWith(func(_ *recipe.ConstructionRelatedFieldSource, predicate *recipe.ConstructionRelatedPredicate) {
		predicate.Operator, predicate.Value = recipe.FilterExists, nil
	})
	existsCompiled := compileDerivedTestOutput(t, existsOutput)
	existsStage := existsCompiled.Plan.StageSequence.Stages[0]
	if existsStage.RelatedExpand == nil {
		t.Fatal("repeated EXISTS predicate lost its related expansion")
	}
	existsFound := false
	for _, operation := range existsStage.RelatedExpand.RelatedRecords.Operations {
		if operation.Filter == nil || operation.Filter.Expression == nil || operation.Filter.Expression.Comparison == nil {
			continue
		}
		comparison := operation.Filter.Expression.Comparison
		if comparison.Operator == string(recipe.FilterExists) && string(comparison.Quantifier) == string(recipe.QuantifierAny) {
			existsFound = true
		}
	}
	if !existsFound {
		t.Fatal("repeated EXISTS predicate did not carry explicit ANY into the terminal subplan")
	}
	terminalRepeatedOutput := cloneWith(func(source *recipe.ConstructionRelatedFieldSource, _ *recipe.ConstructionRelatedPredicate) {
		source.Path = "Observation.category[].coding[].code[]"
		source.RepeatedBoundaries = append(source.RepeatedBoundaries, recipe.ConstructionRelatedRepeatedBoundary{
			Path: "category[].coding[].code[]", MaxItems: 8,
		})
	})
	if err := terminalRepeatedOutput.Construction.Validate(terminalRepeatedOutput.Fields); err != nil {
		t.Fatalf("primitive repeated terminal selector was rejected: %v", err)
	}
	terminalRepeatedCompiled := compileDerivedTestOutput(t, terminalRepeatedOutput)
	terminalRepeatedQuery, err := aql.RenderPhysicalPlan(terminalRepeatedCompiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{".category", ".coding", ".code", "FLATTEN(", " != null"} {
		if !strings.Contains(terminalRepeatedQuery.Query, expected) {
			t.Fatalf("terminal repeated scalar query omitted %q: %s", expected, terminalRepeatedQuery.Query)
		}
	}
	for _, mismatch := range []struct {
		name   string
		change func(*recipe.ConstructionRelatedFieldSource, *recipe.ConstructionRelatedPredicate)
	}{
		{name: "repeated source omits ANY", change: func(_ *recipe.ConstructionRelatedFieldSource, predicate *recipe.ConstructionRelatedPredicate) {
			predicate.Quantifier = ""
		}},
		{name: "scalar source has ANY", change: func(source *recipe.ConstructionRelatedFieldSource, _ *recipe.ConstructionRelatedPredicate) {
			source.Path, source.Cardinality, source.RepeatedBoundaries = "Observation.code", "optional_one", nil
		}},
		{name: "repeated boundary differs from selector", change: func(source *recipe.ConstructionRelatedFieldSource, _ *recipe.ConstructionRelatedPredicate) {
			source.RepeatedBoundaries[1].Path = "category[].component[]"
		}},
	} {
		t.Run(mismatch.name, func(t *testing.T) {
			invalid := cloneWith(mismatch.change)
			if err := invalid.Construction.Validate(invalid.Fields); err == nil {
				t.Fatal("invalid repeated/scalar contributor shape was accepted")
			}
		})
	}
}

func TestCompileRelatedExpandUsesDistinctTerminalIdentityAndExplicitEmptyPolicy(t *testing.T) {
	for _, policy := range []recipe.ExpansionEmptyPolicy{
		recipe.ExpansionError,
		recipe.ExpansionExclude,
		recipe.ExpansionPreserveParent,
	} {
		t.Run(string(policy), func(t *testing.T) {
			output := constructionTestOutput()
			output.Construction = &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"},
					{ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"},
				},
				Steps: []recipe.ConstructionStep{{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "_key", ChoiceID: "related-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: policy, RelatedRecordColumnID: "observation_id",
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group", Label: "Group"},
						{ID: "category_id", Name: "category", Label: "Category"},
						{ID: "amount_id", Name: "amount", Label: "Amount"},
						{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: policy == recipe.ExpansionPreserveParent},
					},
				}},
			}

			compiled := compileDerivedTestOutput(t, output)
			if len(compiled.Plan.StageSequence.Stages) != 1 {
				t.Fatalf("physical related expansion stage count = %d", len(compiled.Plan.StageSequence.Stages))
			}
			stage := compiled.Plan.StageSequence.Stages[0]
			if stage.Kind != ir.PhysicalStageRelatedExpandOp || stage.RelatedExpand == nil || !stage.RelatedExpand.RelatedRecords.Unique {
				t.Fatalf("related expansion lowering did not deduplicate terminal resources: %#v", stage)
			}
			parentIdentityColumn := stage.RelatedExpand.ParentIdentityColumnID
			terminalIdentityColumn := stage.RelatedExpand.TerminalIdentityColumn
			if stage.RelatedExpand.Route[0].EdgeID != "patient-observation" || stage.RelatedExpand.TargetResourceType != "Observation" {
				t.Fatalf("compiled related route metadata = %#v", stage.RelatedExpand)
			}
			if !hasCompiledColumn(compiled.OutputSchema, "_key", true) ||
				!hasCompiledColumn(compiled.OutputSchema, parentIdentityColumn, true) ||
				!hasCompiledColumn(compiled.OutputSchema, terminalIdentityColumn, true) {
				t.Fatalf("related expansion lost separate root, parent, or terminal identity: %#v", compiled.OutputSchema)
			}
			if compiled.Stages[1].RelatedExpand == nil || compiled.Stages[1].RelatedExpand.TargetNodeID != "observation-node" ||
				compiled.Stages[1].RelatedExpand.ParentIdentityColumn != "_key" || compiled.Stages[1].RelatedExpand.TerminalIdentityColumn != terminalIdentityColumn {
				t.Fatalf("related expansion stage descriptor lacks downstream identity contract: %#v", compiled.Stages[1])
			}

			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			foundTraversal := false
			for _, operation := range stage.RelatedExpand.RelatedRecords.Operations {
				if operation.Traversal == nil {
					continue
				}
				foundTraversal = true
				if rendered.BindVars["@"+operation.Traversal.EdgeCollectionBindKey] != "fhir_edge" {
					t.Fatalf("related expansion edge collection bind = %#v", rendered.BindVars["@"+operation.Traversal.EdgeCollectionBindKey])
				}
			}
			if !foundTraversal {
				t.Fatal("related expansion did not retain its graph traversal")
			}
			recordsVariable := stage.RelatedExpand.RelatedRecordsVariable
			for _, expected := range []string{"SORTED_UNIQUE", "terminal_id", "resource_id", parentIdentityColumn, terminalIdentityColumn, `TO_STRING([["input"`} {
				if !strings.Contains(rendered.Query, expected) {
					t.Fatalf("rendered related expansion omitted %q: %s", expected, rendered.Query)
				}
			}
			if !strings.Contains(rendered.Query, ".id") || strings.Contains(rendered.Query, "related_key") {
				t.Fatalf("public related ID must be the required FHIR document id, not Arango _key: %s", rendered.Query)
			}
			switch policy {
			case recipe.ExpansionError:
				if !strings.Contains(rendered.Query, "FILTER ASSERT(LENGTH("+recordsVariable+") > 0") {
					t.Fatalf("ERROR policy did not reject a missing related resource: %s", rendered.Query)
				}
			case recipe.ExpansionExclude:
				if !strings.Contains(rendered.Query, "LENGTH("+recordsVariable+") == 0 ? []") {
					t.Fatalf("EXCLUDE policy did not remove empty parents: %s", rendered.Query)
				}
			case recipe.ExpansionPreserveParent:
				if !strings.Contains(rendered.Query, "LENGTH("+recordsVariable+") == 0 ? [null]") {
					t.Fatalf("PRESERVE_PARENT policy did not emit an empty related row: %s", rendered.Query)
				}
			}
		})
	}
}

func TestRelatedExpandAfterGroupUsesCompilerOwnedRootContributors(t *testing.T) {
	output := recipe.Output{
		Name: "group_related_expand", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Label: "Gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_by_gender",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender_id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient_count_id"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_gender_id", Name: "gender", Type: "string"},
						{ID: "patient_count_id", Name: "patient_count", Type: "integer"},
					},
				},
				{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_patients"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: rootContributorSetColumn, ChoiceID: "patient-observation-choice",
						TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent,
						RelatedRecordColumnID: "observation_id",
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_gender_id", Name: "gender", Type: "string"},
						{ID: "patient_count_id", Name: "patient_count", Type: "integer"},
						{ID: "observation_id", Name: "observation_id", Type: "string", Nullable: true},
					},
				},
			},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Stages) != 3 {
		t.Fatalf("compiled stages = %d, want source, Group, related expansion", len(compiled.Stages))
	}
	if anchors := compiled.Stages[0].RelatedExpandAnchors; len(anchors) != 1 || anchors[0].Kind != "root" || anchors[0].AnchorColumnID != "_key" {
		t.Fatalf("direct source discovery must prefer its retained scalar root key: %#v", anchors)
	}
	group := compiled.Plan.StageSequence.Stages[0]
	if group.Group == nil || group.Group.RootContributorOutputColumn != rootContributorSetColumn {
		t.Fatalf("Group did not materialize contributors needed by the following expansion: %#v", group.Group)
	}
	contributors, found := physicalStageColumnsForTest(group.OutputColumns)[rootContributorSetColumn]
	if !found || !contributors.Internal || contributors.Kind != "string" || contributors.Cardinality != string("many") ||
		contributors.RootContributorResourceType != "Patient" {
		t.Fatalf("Group contributor output = %#v, want a hidden Patient key set", contributors)
	}
	stage := compiled.Plan.StageSequence.Stages[1]
	related := stage.RelatedExpand
	if related == nil || related.AnchorKind != "rootContributors" || related.AnchorColumnID != rootContributorSetColumn ||
		related.RootContributorColumn != rootContributorSetColumn || related.RootResourceType != "Patient" ||
		related.EmptyPolicy != ir.PhysicalUnnestPreserveParent || !related.RelatedRecords.Unique || related.RelatedRecords.Sort == nil ||
		related.RelatedRecords.Sort.Variable == "" || len(related.RelatedRecords.Sort.Path) != 1 || related.RelatedRecords.Sort.Path[0] != "_id" {
		t.Fatalf("related expansion lacks typed contributor union, stable deduplication, or empty-row behavior: %#v", related)
	}
	if len(related.RelatedRecords.Operations) < 2 || related.RelatedRecords.Operations[0].CollectionScan == nil ||
		related.RelatedRecords.Operations[1].Filter == nil || related.RelatedRecords.Operations[1].Filter.Predicate.Operator != "IN" ||
		related.RelatedRecords.Operations[1].Filter.Predicate.Right == nil ||
		related.RelatedRecords.Operations[1].Filter.Predicate.Right.Path[0] != rootContributorSetColumn {
		t.Fatalf("root scan does not select exactly the input row's compiler-owned contributor set: %#v", related.RelatedRecords.Operations)
	}
	if !hasCompiledColumn(compiled.OutputSchema, "gender", false) || !hasCompiledColumn(compiled.OutputSchema, "patient_count", false) ||
		!hasCompiledColumn(compiled.OutputSchema, related.TerminalIdentityColumn, true) {
		t.Fatalf("related expansion lost grouped values or exact terminal identity: %#v", compiled.OutputSchema)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"_key IN", "project == @project", "dataset_generation == @dataset_generation",
		"auth_resource_path", "SORTED_UNIQUE", "LENGTH(", "? [null]",
	} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("grouped related expansion query omitted %q:\n%s", expected, rendered.Query)
		}
	}
	if err := compiled.Plan.Validate(); err != nil {
		t.Fatalf("valid typed contributor expansion plan does not validate: %v", err)
	}
	relatedStage := &compiled.Plan.StageSequence.Stages[1]
	originalRelated := *relatedStage.RelatedExpand
	originalInputColumns := append([]ir.PhysicalStageColumn(nil), relatedStage.InputColumns...)
	for _, test := range []struct {
		name   string
		mutate func()
	}{
		{name: "non-string contributor set", mutate: func() {
			for index := range relatedStage.InputColumns {
				if relatedStage.InputColumns[index].ID == rootContributorSetColumn {
					relatedStage.InputColumns[index].Kind = "integer"
				}
			}
		}},
		{name: "scalar contributor cardinality", mutate: func() {
			for index := range relatedStage.InputColumns {
				if relatedStage.InputColumns[index].ID == rootContributorSetColumn {
					relatedStage.InputColumns[index].Cardinality = "required_one"
				}
			}
		}},
		{name: "wrong contributor resource metadata", mutate: func() {
			relatedStage.RelatedExpand.RootResourceType = "Observation"
		}},
	} {
		t.Run("rejects malformed contributor anchor/"+test.name, func(t *testing.T) {
			relatedStage.RelatedExpand = &originalRelated
			relatedStage.InputColumns = append([]ir.PhysicalStageColumn(nil), originalInputColumns...)
			test.mutate()
			if err := compiled.Plan.Validate(); err == nil {
				t.Fatal("malformed root contributor anchor unexpectedly validated")
			}
		})
	}
	relatedStage.RelatedExpand = &originalRelated
	relatedStage.InputColumns = originalInputColumns
}

func TestOnwardRelatedExpandLooksUpActiveIdentityAndPreservesNullParentRows(t *testing.T) {
	output := constructionTestOutput()
	_, observationIdentity := relatedExpandIdentityColumnNames("expand_observations")
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation_id",
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"}, {ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				},
			},
			{
				ID: "expand_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: observationIdentity, ChoiceID: "observation-specimen", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
					Route: []recipe.ConstructionRelatedRouteStep{{
						EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node",
						FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen",
						StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "specimen_id",
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"},
					{ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
					{ID: "specimen_id", Name: "specimen_id", Label: "Specimen ID", Type: "string", Nullable: true},
				},
			},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	if len(compiled.Plan.StageSequence.Stages) != 2 {
		t.Fatalf("onward related expansion stages = %d, want 2", len(compiled.Plan.StageSequence.Stages))
	}
	first, second := compiled.Plan.StageSequence.Stages[0], compiled.Plan.StageSequence.Stages[1]
	if second.RelatedExpand == nil || second.RelatedExpand.AnchorKind != "activeRelatedRecord" ||
		second.RelatedExpand.AnchorNodeID != "observation-node" || second.RelatedExpand.AnchorResourceType != "Observation" ||
		second.RelatedExpand.AnchorColumnID != observationIdentity {
		t.Fatalf("onward expansion did not retain its exact active source identity: %#v", second.RelatedExpand)
	}
	if first.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || second.RelatedExpand.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		t.Fatalf("both expansions must preserve parents with missing related records: first=%q second=%q", first.RelatedExpand.EmptyPolicy, second.RelatedExpand.EmptyPolicy)
	}
	if !hasCompiledColumn(compiled.OutputSchema, "_key", true) {
		t.Fatalf("onward expansion dropped the original root key needed for sibling routes: %#v", compiled.OutputSchema)
	}
	var activeLookup *ir.PhysicalDocumentLookup
	for _, operation := range second.RelatedExpand.RelatedRecords.Operations {
		if operation.Kind == ir.PhysicalDocumentLookupOp {
			activeLookup = operation.DocumentLookup
			break
		}
	}
	if activeLookup == nil || activeLookup.ExactID.Path[0] != observationIdentity || compiled.Plan.BindVars[activeLookup.CollectionBindKey] != "Observation" {
		t.Fatalf("onward expansion must point-lookup the selected Observation identity: lookup=%#v binds=%#v", activeLookup, compiled.Plan.BindVars)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	parseIndex := strings.Index(query, "PARSE_IDENTIFIER(")
	if parseIndex < 0 {
		t.Fatalf("active source lookup did not parse the exact FHIR id: %s", query)
	}
	guardIndex := strings.LastIndex(query[:parseIndex], "FILTER ")
	if guardIndex < 0 || !strings.Contains(query[guardIndex:parseIndex], " != null") {
		t.Fatalf("null-preserved parent identity must be filtered before PARSE_IDENTIFIER: %s", query)
	}
	for _, expected := range []string{
		activeLookup.Variable + ".project == @project",
		activeLookup.Variable + ".dataset_generation == @dataset_generation",
		activeLookup.Variable + ".auth_resource_path IN @auth_resource_paths",
		"LENGTH(" + second.RelatedExpand.RelatedRecordsVariable + ") == 0 ? [null]",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("onward expansion query omitted %q: %s", expected, query)
		}
	}
}
