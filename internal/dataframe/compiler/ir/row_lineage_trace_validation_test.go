package ir

import "testing"

func TestPhysicalRelatedRowLineageTraceAcceptsScopedOrderedOwners(t *testing.T) {
	sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
	if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err != nil {
		t.Fatalf("valid ordered trace was rejected: %v", err)
	}
}

func TestPhysicalRelatedRowLineageTraceRejectsInvalidOwnerWitnesses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalStageSequence, *PhysicalRowLineageReturn, *PhysicalRowLineageTrace, map[string]any, []PhysicalOperation)
	}{
		{
			name: "out of order owners",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages[0], trace.Stages[1] = trace.Stages[1], trace.Stages[0]
			},
		},
		{
			name: "missing owner",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages = trace.Stages[:1]
			},
		},
		{
			name: "repeated owner",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages[1] = trace.Stages[0]
			},
		},
		{
			name: "forged terminal resource path",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, bindVars map[string]any, _ []PhysicalOperation) {
				bindVars["terminal_0"] = "Encounter/forged"
			},
		},
		{
			name: "empty identity without preserve policy",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, bindVars map[string]any, _ []PhysicalOperation) {
				trace.Stages[0].RelatedRowKind = "EMPTY"
				bindVars["terminal_0"] = ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
			test.mutate(&sequence, &terminal, &trace, bindVars, source)
			if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err == nil {
				t.Fatal("invalid row lineage trace unexpectedly validated")
			}
		})
	}
}

func TestPhysicalRelatedRowLineageTraceRequiresIndexedRootKeyFilter(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]PhysicalOperation) []PhysicalOperation
	}{
		{
			name: "root key filter removed",
			mutate: func(operations []PhysicalOperation) []PhysicalOperation {
				return []PhysicalOperation{operations[0], operations[len(operations)-1]}
			},
		},
		{
			name: "root key filter bound to another identity",
			mutate: func(operations []PhysicalOperation) []PhysicalOperation {
				copy := append([]PhysicalOperation(nil), operations...)
				filter := *copy[1].Filter
				predicate := filter.Predicate
				right := *predicate.Right
				right.BindKey = "other_root_key"
				predicate.Right = &right
				filter.Predicate = predicate
				copy[1].Filter = &filter
				return copy
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
			source = test.mutate(source)
			if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err == nil {
				t.Fatal("trace without its exact root-key source filter unexpectedly validated")
			}
		})
	}
}

func TestClonePhysicalPlanCopiesRowLineageTraceStages(t *testing.T) {
	plan := PhysicalPlan{StageSequence: &PhysicalStageSequence{RowLineageReturn: &PhysicalRowLineageReturn{
		Trace: &PhysicalRowLineageTrace{RootKeyBindKey: "root_key", Stages: []PhysicalRowLineageStageMatch{{
			StageID: "related_one", Kind: PhysicalStageRelatedExpandOp,
			StageRowIDBindKey: "row_one", RelatedTerminalIDBindKey: "terminal_one", RelatedRowKind: "RELATED",
			IdentityKeyBindKeys: []string{"owner_key"},
		}}},
	}}}
	cloned := ClonePhysicalPlan(plan)
	cloned.StageSequence.RowLineageReturn.Trace.Stages[0].StageID = "changed"
	cloned.StageSequence.RowLineageReturn.Trace.Stages[0].IdentityKeyBindKeys[0] = "changed_key"
	if plan.StageSequence.RowLineageReturn.Trace.Stages[0].StageID != "related_one" {
		t.Fatal("mutating a cloned trace changed the source Trace.Stages backing array")
	}
	if plan.StageSequence.RowLineageReturn.Trace.Stages[0].IdentityKeyBindKeys[0] != "owner_key" {
		t.Fatal("mutating a cloned trace changed an owner key bind in the source plan")
	}
	if cloned.StageSequence.RowLineageReturn.Trace == plan.StageSequence.RowLineageReturn.Trace {
		t.Fatal("cloned plan shares the row lineage Trace pointer")
	}
}

func validRelatedLineageFixture() (PhysicalStageSequence, PhysicalRowLineageReturn, PhysicalRowLineageTrace, map[string]any, []PhysicalOperation) {
	sequence := PhysicalStageSequence{
		SourceStageID: "source", SourceRowIdentity: "_key", FinalStageID: "related_two", FinalRowIdentity: "related_row_two",
		Stages: []PhysicalConstructionStage{
			{ID: "related_one", Kind: PhysicalStageRelatedExpandOp, RowIdentityColumn: "related_row_one", RelatedExpand: &PhysicalStageRelatedExpand{
				AnchorKind: "root", AnchorColumnID: "_key", ParentIdentityColumn: "_key", TargetResourceType: "Observation", EmptyPolicy: PhysicalUnnestExclude,
			}},
			{ID: "related_two", Kind: PhysicalStageRelatedExpandOp, RowIdentityColumn: "related_row_two", RelatedExpand: &PhysicalStageRelatedExpand{
				AnchorKind: "activeRelatedRecord", AnchorColumnID: "active_observation", ParentIdentityColumn: "related_row_one", TargetResourceType: "Specimen", EmptyPolicy: PhysicalUnnestExclude,
			}},
		},
	}
	trace := PhysicalRowLineageTrace{RootKeyBindKey: "root_key", Stages: []PhysicalRowLineageStageMatch{
		{StageID: "related_one", Kind: PhysicalStageRelatedExpandOp, StageRowIDBindKey: "row_one", RelatedTerminalIDBindKey: "terminal_0", RelatedRowKind: "RELATED"},
		{StageID: "related_two", Kind: PhysicalStageRelatedExpandOp, StageRowIDBindKey: "row_two", RelatedTerminalIDBindKey: "terminal_1", RelatedRowKind: "RELATED"},
	}}
	terminal := PhysicalRowLineageReturn{ResourceType: "Patient", Trace: &trace}
	bindVars := map[string]any{
		"root_collection": "Patient", "root_key": "patient-key", "row_one": "row-id-one", "row_two": "row-id-two",
		"terminal_0": "Observation/observation-key", "terminal_1": "Specimen/specimen-key",
	}
	rootFilter := PhysicalOperation{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
		Operator: "EQUALS", Left: PhysicalValue{Variable: "root", Path: []string{"_key"}}, Right: &PhysicalValue{BindKey: "root_key"},
	}}}
	source := []PhysicalOperation{
		{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		rootFilter,
		{Kind: PhysicalReturnOp, Return: &PhysicalReturn{}},
	}
	return sequence, terminal, trace, bindVars, source
}
