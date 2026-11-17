package ir

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type unionWitness struct {
	name       string
	kind       any
	parentType reflect.Type
	payload    string
}

var operationPayloadRegistry = []unionWitness{
	{"ROOT_SCAN", PhysicalRootScanOp, reflect.TypeOf(PhysicalOperation{}), "RootScan"},
	{"TRAVERSAL", PhysicalTraversalOp, reflect.TypeOf(PhysicalOperation{}), "Traversal"},
	{"FILTER", PhysicalFilterOp, reflect.TypeOf(PhysicalOperation{}), "Filter"},
	{"DERIVED_LET", PhysicalDerivedLetOp, reflect.TypeOf(PhysicalOperation{}), "DerivedLet"},
	{"EXPRESSION_LET", PhysicalExpressionLetOp, reflect.TypeOf(PhysicalOperation{}), "ExpressionLet"},
	{"SET", PhysicalSetOp, reflect.TypeOf(PhysicalOperation{}), "Set"},
	{"UNNEST", PhysicalUnnestOp, reflect.TypeOf(PhysicalOperation{}), "Unnest"},
	{"SORT", PhysicalSortOp, reflect.TypeOf(PhysicalOperation{}), "Sort"},
	{"LIMIT", PhysicalLimitOp, reflect.TypeOf(PhysicalOperation{}), "Limit"},
	{"RETURN", PhysicalReturnOp, reflect.TypeOf(PhysicalOperation{}), "Return"},
	{"PATH_SEED", PhysicalPathSeedOp, reflect.TypeOf(PhysicalOperation{}), "PathSeed"},
	{"PATH_EXTEND", PhysicalPathExtendOp, reflect.TypeOf(PhysicalOperation{}), "PathExtend"},
	{"GRAPH_RETURN", PhysicalGraphReturnOp, reflect.TypeOf(PhysicalOperation{}), "GraphReturn"},
	{"COLLECTION_SCAN", PhysicalCollectionScanOp, reflect.TypeOf(PhysicalOperation{}), "CollectionScan"},
	{"KEY_SET_LOOKUP", PhysicalKeySetLookupOp, reflect.TypeOf(PhysicalOperation{}), "KeySetLookup"},
	{"DOCUMENT_LOOKUP", PhysicalDocumentLookupOp, reflect.TypeOf(PhysicalOperation{}), "DocumentLookup"},
	{"POPULATION_MAPPING_RETURN", PhysicalPopulationMappingReturnOp, reflect.TypeOf(PhysicalOperation{}), "PopulationMappingReturn"},
	{"CELL_TRACE_RETURN", PhysicalCellTraceReturnOp, reflect.TypeOf(PhysicalOperation{}), "CellTraceReturn"},
	{"TABLE_SHAPE_EXCLUSION_RETURN", PhysicalTableShapeExclusionReturnOp, reflect.TypeOf(PhysicalOperation{}), "TableShapeExclusionReturn"},
	{"GROUP_ROWS", PhysicalGroupRowsOp, reflect.TypeOf(PhysicalOperation{}), "GroupRows"},
	{"GROUPED_PIVOT", PhysicalGroupedPivotOp, reflect.TypeOf(PhysicalOperation{}), "GroupedPivot"},
	{"UNPIVOT", PhysicalUnpivotOp, reflect.TypeOf(PhysicalOperation{}), "Unpivot"},
}

var expressionPayloadRegistry = []unionWitness{
	{"VALUE", PhysicalValueExpression, reflect.TypeOf(PhysicalExpression{}), "Value"},
	{"LITERAL", PhysicalLiteralExpression, reflect.TypeOf(PhysicalExpression{}), "Literal"},
	{"EXTRACT", PhysicalExtractExpression, reflect.TypeOf(PhysicalExpression{}), "Extract"},
	{"AGGREGATE", PhysicalAggregateExpression, reflect.TypeOf(PhysicalExpression{}), "Aggregate"},
	{"PIVOT", PhysicalPivotExpression, reflect.TypeOf(PhysicalExpression{}), "Pivot"},
	{"OWNER_RECORDS", PhysicalOwnerRecordsExpression, reflect.TypeOf(PhysicalExpression{}), "OwnerRecords"},
	{"SLICE", PhysicalSliceExpression, reflect.TypeOf(PhysicalExpression{}), "Slice"},
	{"OBJECT_LOOKUP", PhysicalObjectLookupExpression, reflect.TypeOf(PhysicalExpression{}), "ObjectLookup"},
	{"KEYED_MAP", PhysicalKeyedMapExpression, reflect.TypeOf(PhysicalExpression{}), "KeyedMap"},
	{"OBJECT_KEYS", PhysicalObjectKeysExpression, reflect.TypeOf(PhysicalExpression{}), "ObjectKeys"},
	{"KEY_SET", PhysicalKeySetExpression, reflect.TypeOf(PhysicalExpression{}), "KeySet"},
	{"OBJECT", PhysicalObjectExpression, reflect.TypeOf(PhysicalExpression{}), "Object"},
	{"SUBPLAN", PhysicalSubplanExpression, reflect.TypeOf(PhysicalExpression{}), "Subplan"},
	{"CALL", PhysicalCallExpression, reflect.TypeOf(PhysicalExpression{}), "Call"},
	{"RELATED_FIELD", PhysicalRelatedFieldExpression, reflect.TypeOf(PhysicalExpression{}), "RelatedField"},
}

var predicatePayloadRegistry = []unionWitness{
	{"COMPARISON", PhysicalComparisonPredicate, reflect.TypeOf(PhysicalPredicateExpression{}), "Comparison"},
	{"ALL", PhysicalAllPredicate, reflect.TypeOf(PhysicalPredicateExpression{}), "Children"},
	{"ANY", PhysicalAnyPredicate, reflect.TypeOf(PhysicalPredicateExpression{}), "Children"},
	{"NOT", PhysicalNotPredicate, reflect.TypeOf(PhysicalPredicateExpression{}), "Children"},
	{"EXISTS", PhysicalExistsPredicate, reflect.TypeOf(PhysicalPredicateExpression{}), "Exists"},
}

var physicalNodeTypes = map[reflect.Type]bool{
	reflect.TypeOf(PhysicalPlan{}):                true,
	reflect.TypeOf(PhysicalOperation{}):           true,
	reflect.TypeOf(PhysicalExpression{}):          true,
	reflect.TypeOf(PhysicalPredicateExpression{}): true,
	reflect.TypeOf(PhysicalSubplan{}):             true,
	reflect.TypeOf(PhysicalValue{}):               true,
}

func TestPhysicalChildrenPayloadRegistriesAreExhaustive(t *testing.T) {
	assertKindsRegistered(t, "PhysicalOperationKind", operationPayloadRegistry)
	assertKindsRegistered(t, "PhysicalExpressionKind", expressionPayloadRegistry)
	assertKindsRegistered(t, "PhysicalPredicateKind", predicatePayloadRegistry)
	assertPayloadFieldsRegistered(t, reflect.TypeOf(PhysicalOperation{}), operationPayloadRegistry)
	assertPayloadFieldsRegistered(t, reflect.TypeOf(PhysicalExpression{}), expressionPayloadRegistry)
	assertPayloadFieldsRegistered(t, reflect.TypeOf(PhysicalPredicateExpression{}), predicatePayloadRegistry)
}

func TestPhysicalChildrenMatchesEveryActivePayloadChildField(t *testing.T) {
	for _, registry := range [][]unionWitness{operationPayloadRegistry, expressionPayloadRegistry, predicatePayloadRegistry} {
		for _, witness := range registry {
			witness := witness
			t.Run(witness.name, func(t *testing.T) {
				parent := activePayloadWitness(t, witness)
				assertChildrenEqualReflection(t, parent)
			})
		}
	}
	plan := reflect.New(reflect.TypeOf(PhysicalPlan{}))
	for index := 0; index < plan.Elem().NumField(); index++ {
		seedWrapper(plan.Elem().Field(index), map[reflect.Type]bool{})
	}
	if len(reflectedChildAddresses(t, plan.Interface().(Node))) == 0 {
		t.Fatal("physical plan witness has no children")
	}
	for _, root := range []Node{plan.Interface().(Node), &PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalLimitOp, Limit: &PhysicalLimit{}}}, Return: PhysicalExpression{Kind: PhysicalValueExpression, Value: &PhysicalValue{}}, Sort: &PhysicalValue{}, DistinctBy: &PhysicalValue{}}} {
		t.Run(fmt.Sprintf("%T", root), func(t *testing.T) { assertChildrenEqualReflection(t, root) })
	}
}

func TestPhysicalChildrenTraversesPageCandidateSeedAndNestedSubplans(t *testing.T) {
	plan := rootPageCandidateSeedPlan()
	assertChildrenEqualReflection(t, &plan)

	foundCollectionScan := false
	foundNestedValue := false
	err := Walk(&plan, func(node Node) error {
		switch value := node.(type) {
		case *PhysicalOperation:
			foundCollectionScan = foundCollectionScan || value.Kind == PhysicalCollectionScanOp
		case *PhysicalValue:
			foundNestedValue = foundNestedValue || value.Variable == "candidate_child"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !foundCollectionScan || !foundNestedValue {
		t.Fatalf("Walk missed PageCandidateSeed descendants: collectionScan=%v nestedValue=%v", foundCollectionScan, foundNestedValue)
	}
}

func TestPhysicalWalkVisitsSharedNodesPerOccurrenceAndRejectsCycles(t *testing.T) {
	shared := &PhysicalSubplan{}
	plan := &PhysicalPlan{Operations: []PhysicalOperation{
		{Kind: PhysicalExpressionLetOp, ExpressionLet: &PhysicalExpressionLet{Expression: PhysicalExpression{Kind: PhysicalSubplanExpression, Subplan: shared}}},
		{Kind: PhysicalExpressionLetOp, ExpressionLet: &PhysicalExpressionLet{Expression: PhysicalExpression{Kind: PhysicalSubplanExpression, Subplan: shared}}},
	}}
	sharedVisits := 0
	if err := Walk(plan, func(node Node) error {
		if node == shared {
			sharedVisits++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sharedVisits != 2 {
		t.Fatalf("shared subplan visits = %d, want 2 occurrences", sharedVisits)
	}

	cycle := &PhysicalSubplan{}
	predicate := PhysicalPredicateExpression{Kind: PhysicalExistsPredicate, Exists: cycle}
	cycle.Operations = []PhysicalOperation{{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Expression: &predicate}}}
	if err := Walk(cycle, func(Node) error { return nil }); err == nil {
		t.Fatal("Walk accepted an ancestry cycle")
	}
}

func TestPhysicalChildrenReturnsMutablePointersToValueLeaves(t *testing.T) {
	value := PhysicalValue{Variable: "before"}
	expression := &PhysicalExpression{Kind: PhysicalValueExpression, Value: &value}
	children := Children(expression)
	if len(children) != 1 {
		t.Fatalf("Children(expression) = %d children, want one PhysicalValue", len(children))
	}
	child, ok := children[0].(*PhysicalValue)
	if !ok {
		t.Fatalf("child type = %T, want *PhysicalValue", children[0])
	}
	child.Variable = "after"
	if value.Variable != "after" {
		t.Fatalf("child pointer did not mutate parent field: %#v", value)
	}
}

func activePayloadWitness(t *testing.T, witness unionWitness) Node {
	t.Helper()
	parent := reflect.New(witness.parentType).Elem()
	kindField := parent.FieldByName("Kind")
	kind := reflect.ValueOf(witness.kind)
	if !kind.Type().AssignableTo(kindField.Type()) {
		t.Fatalf("%v cannot set %s.Kind", kind.Type(), witness.parentType)
	}
	kindField.Set(kind)
	for index := 0; index < parent.NumField(); index++ {
		field := witness.parentType.Field(index)
		if field.Name == "Kind" || isPayloadField(witness.parentType.Name(), field) {
			continue
		}
		seedWrapper(parent.Field(index), map[reflect.Type]bool{})
	}
	payload := parent.FieldByName(witness.payload)
	if witness.payload == "Children" {
		children := reflect.MakeSlice(payload.Type(), 1, 1)
		children.Index(0).Set(newNodeValue(children.Type().Elem()))
		payload.Set(children)
	} else {
		child := reflect.New(payload.Type().Elem())
		seedWrapper(child.Elem(), map[reflect.Type]bool{})
		payload.Set(child)
	}
	return parent.Addr().Interface().(Node)
}

func newNodePointer(typeOf reflect.Type) reflect.Value {
	base := typeOf
	if base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	value := reflect.New(base)
	switch typed := value.Interface().(type) {
	case *PhysicalOperation:
		typed.Kind, typed.Limit = PhysicalLimitOp, &PhysicalLimit{}
	case *PhysicalExpression:
		typed.Kind, typed.Value = PhysicalValueExpression, &PhysicalValue{}
	case *PhysicalPredicateExpression:
		typed.Kind, typed.Exists = PhysicalExistsPredicate, &PhysicalSubplan{}
	case *PhysicalValue:
		typed.Variable = "leaf"
	}
	if typeOf.Kind() == reflect.Pointer {
		return value
	}
	return value.Elem()
}

func newNodeValue(typeOf reflect.Type) reflect.Value { return newNodePointer(typeOf) }

func seedWrapper(value reflect.Value, active map[reflect.Type]bool) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Pointer {
		element := value.Type().Elem()
		if physicalNodeTypes[element] {
			if value.IsNil() {
				value.Set(newNodePointer(value.Type()))
			}
			return
		}
		if !typeContainsPhysicalNode(element, map[reflect.Type]bool{}) {
			return
		}
		if value.IsNil() {
			value.Set(reflect.New(element))
		}
		seedWrapper(value.Elem(), active)
		return
	}
	if value.Kind() == reflect.Struct {
		if physicalNodeTypes[value.Type()] {
			return
		}
		if active[value.Type()] {
			return
		}
		active[value.Type()] = true
		defer delete(active, value.Type())
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if !field.CanSet() || !typeContainsPhysicalNode(field.Type(), map[reflect.Type]bool{}) {
				continue
			}
			seedWrapper(field, active)
		}
		return
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		if value.Kind() == reflect.Slice && value.IsNil() {
			value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		}
		for index := 0; index < value.Len(); index++ {
			seedWrapper(value.Index(index), active)
		}
		return
	}
	if value.Kind() == reflect.Map && value.IsNil() {
		value.Set(reflect.MakeMap(value.Type()))
		key := reflect.Zero(value.Type().Key())
		item := reflect.New(value.Type().Elem()).Elem()
		seedWrapper(item, active)
		value.SetMapIndex(key, item)
	}
}

func typeContainsPhysicalNode(typeOf reflect.Type, active map[reflect.Type]bool) bool {
	if typeOf.Kind() == reflect.Pointer && physicalNodeTypes[typeOf.Elem()] {
		return true
	}
	if physicalNodeTypes[typeOf] {
		return true
	}
	if active[typeOf] {
		return false
	}
	active[typeOf] = true
	defer delete(active, typeOf)
	switch typeOf.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return typeContainsPhysicalNode(typeOf.Elem(), active)
	case reflect.Map:
		return typeContainsPhysicalNode(typeOf.Key(), active) || typeContainsPhysicalNode(typeOf.Elem(), active)
	case reflect.Struct:
		for index := 0; index < typeOf.NumField(); index++ {
			if typeContainsPhysicalNode(typeOf.Field(index).Type, active) {
				return true
			}
		}
	}
	return false
}

func reflectedChildAddresses(t *testing.T, root Node) []uintptr {
	t.Helper()
	addresses := make([]uintptr, 0)
	value := reflect.ValueOf(root)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		t.Fatalf("invalid root node %T", root)
	}
	for index := 0; index < value.Elem().NumField(); index++ {
		collectReflectedNodes(value.Elem().Field(index), &addresses)
	}
	return addresses
}

func collectReflectedNodes(value reflect.Value, addresses *[]uintptr) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Interface {
		if !value.IsNil() {
			collectReflectedNodes(value.Elem(), addresses)
		}
		return
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		if physicalNodeTypes[value.Type().Elem()] {
			*addresses = append(*addresses, value.Pointer())
			return
		}
		collectReflectedNodes(value.Elem(), addresses)
		return
	}
	if physicalNodeTypes[value.Type()] && value.CanAddr() {
		*addresses = append(*addresses, value.Addr().Pointer())
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			collectReflectedNodes(value.Field(index), addresses)
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			collectReflectedNodes(value.Index(index), addresses)
		}
	case reflect.Map:
		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
		for _, key := range keys {
			collectReflectedNodes(value.MapIndex(key), addresses)
		}
	}
}

func assertChildrenEqualReflection(t *testing.T, parent Node) {
	t.Helper()
	want := reflectedChildAddresses(t, parent)
	got := make([]uintptr, 0)
	for _, child := range Children(parent) {
		got = append(got, reflect.ValueOf(child).Pointer())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Children(%T) child addresses = %v, want reflected fields %v", parent, got, want)
	}
}

func assertPayloadFieldsRegistered(t *testing.T, parent reflect.Type, registry []unionWitness) {
	t.Helper()
	want := make(map[string]bool)
	for _, witness := range registry {
		want[witness.payload] = true
	}
	got := make(map[string]bool)
	for index := 0; index < parent.NumField(); index++ {
		field := parent.Field(index)
		if isPayloadField(parent.Name(), field) {
			got[field.Name] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s payload fields = %v, registry fields = %v", parent.Name(), got, want)
	}
}

func isPayloadField(parent string, field reflect.StructField) bool {
	if field.Name == "Children" && parent == "PhysicalPredicateExpression" {
		return true
	}
	if field.Type.Kind() != reflect.Pointer {
		return false
	}
	payloadTypes := map[string]bool{
		"PhysicalRootScan": true, "PhysicalTraversal": true, "PhysicalFilter": true, "PhysicalDerivedLet": true,
		"PhysicalExpressionLet": true, "PhysicalSet": true, "PhysicalUnnest": true, "PhysicalSort": true,
		"PhysicalLimit": true, "PhysicalReturn": true, "PhysicalPathSeed": true, "PhysicalPathExtend": true,
		"PhysicalGraphReturn": true, "PhysicalCollectionScan": true, "PhysicalKeySetLookup": true,
		"PhysicalDocumentLookup": true, "PhysicalPopulationMappingReturn": true, "PhysicalCellTraceReturn": true,
		"PhysicalTableShapeExclusionReturn": true, "PhysicalGroupRows": true, "PhysicalGroupedPivot": true,
		"PhysicalUnpivot": true, "PhysicalLiteral": true, "PhysicalExtract": true, "PhysicalAggregate": true,
		"PhysicalPivotMap": true, "PhysicalOwnerRecords": true, "PhysicalSlice": true, "PhysicalObjectLookup": true,
		"PhysicalKeyedMap": true, "PhysicalObjectKeys": true, "PhysicalKeySet": true, "PhysicalObject": true,
		"PhysicalSubplan": true, "PhysicalCall": true, "PhysicalRelatedField": true, "PhysicalValue": true,
		"PhysicalPredicate": true,
	}
	return payloadTypes[field.Type.Elem().Name()]
}

func physicalKindValues(t *testing.T, directory, typeName string) map[string]bool {
	t.Helper()
	filenames, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var declarations []ast.Decl
	for _, filename := range filenames {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		if file.Name.Name != "ir" {
			continue
		}
		for _, declaration := range file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			if gen.Tok == token.CONST {
				declarations = append(declarations, gen)
			}
			if gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					typ := spec.(*ast.TypeSpec)
					if _, scalar := typ.Type.(*ast.Ident); scalar {
						declarations = append(declarations, &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{typ}})
					}
				}
			}
		}
	}
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object)}
	syntax := &ast.File{Name: ast.NewIdent("ir"), Decls: declarations}
	config := types.Config{}
	if _, err := config.Check("ir-enum-exhaustiveness", fset, []*ast.File{syntax}, info); err != nil {
		t.Fatalf("type-check IR enum declarations: %v", err)
	}
	declared := make(map[string]bool)
	for _, object := range info.Defs {
		value, ok := object.(*types.Const)
		if !ok {
			continue
		}
		named, ok := types.Unalias(value.Type()).(*types.Named)
		if !ok || named.Obj().Name() != typeName {
			continue
		}
		if value.Val().Kind() != constant.String {
			t.Fatalf("%s.%s is not a string constant", typeName, value.Name())
		}
		declared[constant.StringVal(value.Val())] = true
	}
	return declared
}

func assertKindsRegistered(t *testing.T, typeName string, registry []unionWitness) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	declared := physicalKindValues(t, filepath.Dir(thisFile), typeName)
	registered := make(map[string]bool)
	for _, witness := range registry {
		registered[fmt.Sprint(witness.kind)] = true
	}
	if !reflect.DeepEqual(declared, registered) {
		t.Fatalf("%s constants = %v, registered kinds = %v", typeName, declared, registered)
	}
}

func TestPhysicalKindValuesIncludeInferredAndAliasedConstants(t *testing.T) {
	directory := t.TempDir()
	source := `package ir
 type PhysicalOperationKind string
 type Alias = PhysicalOperationKind
 const Known PhysicalOperationKind = "KNOWN"
 const Inferred = PhysicalOperationKind("INFERRED")
 const Aliased Alias = "ALIASED"
 const Combined = Known + "_SUFFIX"
 `
	if err := os.WriteFile(filepath.Join(directory, "future_kind.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	got := physicalKindValues(t, directory, "PhysicalOperationKind")
	want := map[string]bool{"KNOWN": true, "INFERRED": true, "ALIASED": true, "KNOWN_SUFFIX": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}
