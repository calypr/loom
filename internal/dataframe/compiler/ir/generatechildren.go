//go:build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type generator struct {
	structs       map[string]*ast.StructType
	emitted       map[string]bool
	active        map[string]bool
	body          strings.Builder
	operationMap  map[string]string
	expressionMap map[string]string
	predicateMap  map[string]string
}

var nodeTypes = map[string]bool{
	"PhysicalPlan": true, "PhysicalOperation": true, "PhysicalExpression": true,
	"PhysicalPredicateExpression": true, "PhysicalSubplan": true, "PhysicalValue": true,
}

func main() {
	if len(os.Args) != 3 {
		fatalf("usage: children_codegen <ir-source-dir> <output-go-file>")
	}
	inputDir, outputPath := os.Args[1], os.Args[2]
	g := &generator{
		structs: make(map[string]*ast.StructType), emitted: make(map[string]bool), active: make(map[string]bool),
		operationMap: map[string]string{
			"PhysicalRootScanOp": "RootScan", "PhysicalTraversalOp": "Traversal", "PhysicalFilterOp": "Filter",
			"PhysicalDerivedLetOp": "DerivedLet", "PhysicalExpressionLetOp": "ExpressionLet", "PhysicalSetOp": "Set",
			"PhysicalUnnestOp": "Unnest", "PhysicalSortOp": "Sort", "PhysicalLimitOp": "Limit", "PhysicalReturnOp": "Return",
			"PhysicalPathSeedOp": "PathSeed", "PhysicalPathExtendOp": "PathExtend", "PhysicalGraphReturnOp": "GraphReturn",
			"PhysicalCollectionScanOp": "CollectionScan", "PhysicalKeySetLookupOp": "KeySetLookup", "PhysicalDocumentLookupOp": "DocumentLookup",
			"PhysicalPopulationMappingReturnOp": "PopulationMappingReturn", "PhysicalCellTraceReturnOp": "CellTraceReturn",
			"PhysicalGroupRowsOp":    "GroupRows",
			"PhysicalGroupedPivotOp": "GroupedPivot", "PhysicalUnpivotOp": "Unpivot",
		},
		expressionMap: map[string]string{
			"PhysicalValueExpression": "Value", "PhysicalLiteralExpression": "Literal", "PhysicalExtractExpression": "Extract",
			"PhysicalAggregateExpression": "Aggregate", "PhysicalPivotExpression": "Pivot", "PhysicalOwnerRecordsExpression": "OwnerRecords",
			"PhysicalSliceExpression": "Slice", "PhysicalObjectLookupExpression": "ObjectLookup", "PhysicalKeyedMapExpression": "KeyedMap",
			"PhysicalObjectKeysExpression": "ObjectKeys", "PhysicalKeySetExpression": "KeySet", "PhysicalObjectExpression": "Object",
			"PhysicalSubplanExpression": "Subplan", "PhysicalCallExpression": "Call", "PhysicalRelatedFieldExpression": "RelatedField",
		},
		predicateMap: map[string]string{
			"PhysicalComparisonPredicate": "Comparison", "PhysicalAllPredicate": "Children", "PhysicalAnyPredicate": "Children",
			"PhysicalNotPredicate": "Children", "PhysicalExistsPredicate": "Exists",
		},
	}
	if err := g.parse(inputDir); err != nil {
		fatalf("parse IR package: %v", err)
	}
	if err := g.validateRegistry(); err != nil {
		fatalf("validate active payload registry: %v", err)
	}
	if err := g.generate(outputPath); err != nil {
		fatalf("generate children helpers: %v", err)
	}
}

func (g *generator) parse(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			return err
		}
		if file.Name.Name != "ir" {
			continue
		}
		for _, declaration := range file.Decls {
			genDecl, ok := declaration.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				if structure, ok := typeSpec.Type.(*ast.StructType); ok {
					g.structs[typeSpec.Name.Name] = structure
				}
			}
		}
	}
	return nil
}

func (g *generator) validateRegistry() error {
	for owner, registry := range map[string]map[string]string{
		"PhysicalOperation": g.operationMap, "PhysicalExpression": g.expressionMap, "PhysicalPredicateExpression": g.predicateMap,
	} {
		structure := g.structs[owner]
		if structure == nil {
			return fmt.Errorf("missing struct %s", owner)
		}
		actual := make(map[string]bool)
		for _, field := range structure.Fields.List {
			if len(field.Names) != 1 {
				continue
			}
			if (isPointer(field.Type) || owner == "PhysicalPredicateExpression" && field.Names[0].Name == "Children") && containsKnownPayloadType(field.Type, owner) {
				actual[field.Names[0].Name] = true
			}
		}
		registered := make(map[string]bool)
		for _, field := range registry {
			registered[field] = true
		}
		for field := range actual {
			if !registered[field] {
				return fmt.Errorf("%s.%s is an unregistered tagged payload", owner, field)
			}
		}
		for field := range registered {
			if !actual[field] {
				return fmt.Errorf("registered payload %s.%s is missing or has no matching type", owner, field)
			}
		}
	}
	return nil
}

func containsKnownPayloadType(expr ast.Expr, owner string) bool {
	name := baseTypeName(expr)
	if owner == "PhysicalOperation" {
		return name == "PhysicalRootScan" || name == "PhysicalTraversal" || name == "PhysicalFilter" || name == "PhysicalDerivedLet" || name == "PhysicalExpressionLet" || name == "PhysicalSet" || name == "PhysicalUnnest" || name == "PhysicalSort" || name == "PhysicalLimit" || name == "PhysicalReturn" || name == "PhysicalPathSeed" || name == "PhysicalPathExtend" || name == "PhysicalGraphReturn" || name == "PhysicalCollectionScan" || name == "PhysicalKeySetLookup" || name == "PhysicalDocumentLookup" || name == "PhysicalPopulationMappingReturn" || name == "PhysicalCellTraceReturn" || name == "PhysicalGroupRows" || name == "PhysicalGroupedPivot" || name == "PhysicalUnpivot"
	}
	if owner == "PhysicalExpression" {
		return name == "PhysicalValue" || name == "PhysicalLiteral" || name == "PhysicalExtract" || name == "PhysicalAggregate" || name == "PhysicalPivotMap" || name == "PhysicalOwnerRecords" || name == "PhysicalSlice" || name == "PhysicalObjectLookup" || name == "PhysicalKeyedMap" || name == "PhysicalObjectKeys" || name == "PhysicalKeySet" || name == "PhysicalObject" || name == "PhysicalSubplan" || name == "PhysicalCall" || name == "PhysicalRelatedField"
	}
	if owner == "PhysicalPredicateExpression" {
		return name == "PhysicalPredicate" || name == "PhysicalSubplan" || name == "PhysicalPredicateExpression"
	}
	return false
}

func (g *generator) generate(outputPath string) error {
	var source strings.Builder
	source.WriteString("// Code generated by children_codegen; DO NOT EDIT.\npackage ir\n\n")
	source.WriteString("// Node is a physical IR pointer whose nested children can be traversed or mutated.\n")
	source.WriteString("type Node interface { physicalNode(); isNil() bool }\n")
	nodes := []string{"PhysicalPlan", "PhysicalOperation", "PhysicalExpression", "PhysicalPredicateExpression", "PhysicalSubplan", "PhysicalValue"}
	for _, name := range nodes {
		fmt.Fprintf(&source, "func (*%s) physicalNode() {}\nfunc (n *%s) isNil() bool { return n == nil }\n", name, name)
	}
	source.WriteString("\nfunc appendNode(dst *[]Node, n Node) { if n != nil && !n.isNil() { *dst = append(*dst, n) } }\n")
	source.WriteString(`
// Children enumerates direct physical children through the active tagged payload.
func Children(node Node) []Node {
 if node == nil || node.isNil() { return nil }
 children := make([]Node, 0)
 switch n := node.(type) {
 case *PhysicalPlan: appendPhysicalPlanChildren(&children, n)
 case *PhysicalOperation: appendPhysicalOperationChildren(&children, n)
 case *PhysicalExpression: appendPhysicalExpressionChildren(&children, n)
 case *PhysicalPredicateExpression: appendPhysicalPredicateExpressionChildren(&children, n)
 case *PhysicalSubplan: appendPhysicalSubplanChildren(&children, n)
 case *PhysicalValue:
 default: panic("unsupported physical IR node")
 }
 return children
}
`)

	for _, name := range nodes[:5] {
		g.ensureHelper(name)
	}
	source.WriteString(g.body.String())
	formatted, err := formatSource(source.String())
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, formatted, 0644)
}

func (g *generator) ensureHelper(name string) {
	if g.emitted[name] || g.active[name] {
		return
	}
	structure := g.structs[name]
	if structure == nil {
		return
	}
	g.active[name] = true
	var body strings.Builder
	if name == "PhysicalOperation" {
		body.WriteString("\tswitch value.Kind {\n")
		for _, kind := range sortedKeys(g.operationMap) {
			field := g.operationMap[kind]
			fmt.Fprintf(&body, "\tcase %s:\n\t\t%s\n", kind, g.emitField(name, field, "value."+field))
		}
		body.WriteString("\t}\n")
	} else if name == "PhysicalExpression" {
		body.WriteString("\tswitch value.Kind {\n")
		for _, kind := range sortedKeys(g.expressionMap) {
			field := g.expressionMap[kind]
			fmt.Fprintf(&body, "\tcase %s:\n\t\t%s\n", kind, g.emitField(name, field, "value."+field))
		}
		body.WriteString("\t}\n")
	} else if name == "PhysicalPredicateExpression" {
		body.WriteString("\tswitch value.Kind {\n")
		for _, kind := range sortedKeys(g.predicateMap) {
			field := g.predicateMap[kind]
			fmt.Fprintf(&body, "\tcase %s:\n\t\t%s\n", kind, g.emitField(name, field, "value."+field))
		}
		body.WriteString("\t}\n")
	} else {
		for _, field := range structure.Fields.List {
			for _, fieldName := range fieldNames(field) {
				body.WriteString(g.emitField(name, fieldName, "value."+fieldName))
			}
		}
	}
	fmt.Fprintf(&g.body, "\nfunc append%sChildren(dst *[]Node, value *%s) {\n", name, name)
	if name != "PhysicalPlan" && name != "PhysicalOperation" && name != "PhysicalExpression" && name != "PhysicalPredicateExpression" && name != "PhysicalSubplan" {
		g.body.WriteString("\tif value == nil { return }\n")
	} else {
		g.body.WriteString("\tif value == nil { return }\n")
	}
	g.body.WriteString(body.String())
	g.body.WriteString("}\n")
	g.active[name] = false
	g.emitted[name] = true
}

func (g *generator) emitField(owner, field, expression string) string {
	structure := g.structs[owner]
	var fieldType ast.Expr
	for _, item := range structure.Fields.List {
		if containsName(item, field) {
			fieldType = item.Type
			break
		}
	}
	if fieldType == nil {
		return ""
	}
	return g.emitValue(fieldType, expression, "\t")
}

func (g *generator) emitValue(expr ast.Expr, expression, indent string) string {
	if name := directTypeName(expr); nodeTypes[name] {
		if isPointer(expr) {
			return fmt.Sprintf("%sappendNode(dst, %s)\n", indent, expression)
		}
		return fmt.Sprintf("%sappendNode(dst, &%s)\n", indent, expression)
	}
	switch typed := expr.(type) {
	case *ast.StarExpr:
		if nested := baseTypeName(typed.X); g.hasChild(nested) {
			g.ensureHelper(nested)
			return fmt.Sprintf("%sif %s != nil { append%sChildren(dst, %s) }\n", indent, expression, nested, expression)
		}
	case *ast.ArrayType:
		if name := directTypeName(typed.Elt); nodeTypes[name] {
			if isPointer(typed.Elt) {
				return fmt.Sprintf("%sfor i := range %s { appendNode(dst, %s[i]) }\n", indent, expression, expression)
			}
			return fmt.Sprintf("%sfor i := range %s { appendNode(dst, &%s[i]) }\n", indent, expression, expression)
		}
		name := baseTypeName(typed.Elt)
		if g.hasChild(name) {
			g.ensureHelper(name)
			if isPointer(typed.Elt) {
				return fmt.Sprintf("%sfor i := range %s { if %s[i] != nil { append%sChildren(dst, %s[i]) } }\n", indent, expression, expression, name, expression)
			}
			return fmt.Sprintf("%sfor i := range %s { append%sChildren(dst, &%s[i]) }\n", indent, expression, name, expression)
		}
	case *ast.MapType:
		if g.typeContainsChild(expr) {
			fatalf("map-valued child field %s needs a deterministic ordering rule", expression)
		}
	case *ast.Ident:
		if g.hasChild(typed.Name) {
			g.ensureHelper(typed.Name)
			return fmt.Sprintf("%sappend%sChildren(dst, &%s)\n", indent, typed.Name, expression)
		}
	case *ast.SelectorExpr:
		return ""
	}
	return ""
}

func (g *generator) hasChild(name string) bool {
	if nodeTypes[name] {
		return true
	}
	structure := g.structs[name]
	if structure == nil {
		return false
	}
	for _, field := range structure.Fields.List {
		if g.typeContainsChild(field.Type) {
			return true
		}
	}
	return false
}

func (g *generator) typeContainsChild(expr ast.Expr) bool {
	if nodeTypes[baseTypeName(expr)] {
		return true
	}
	switch value := expr.(type) {
	case *ast.StarExpr:
		return g.typeContainsChild(value.X)
	case *ast.ArrayType:
		return g.typeContainsChild(value.Elt)
	case *ast.MapType:
		return g.typeContainsChild(value.Value)
	case *ast.Ident:
		if g.active[value.Name] {
			return false
		}
		structure := g.structs[value.Name]
		if structure == nil {
			return false
		}
		g.active[value.Name] = true
		defer delete(g.active, value.Name)
		for _, field := range structure.Fields.List {
			if g.typeContainsChild(field.Type) {
				return true
			}
		}
	case *ast.StructType:
		for _, field := range value.Fields.List {
			if g.typeContainsChild(field.Type) {
				return true
			}
		}
	}
	return false
}

func isPointer(expr ast.Expr) bool { _, ok := expr.(*ast.StarExpr); return ok }
func directTypeName(expr ast.Expr) string {
	if p, ok := expr.(*ast.StarExpr); ok {
		return directTypeName(p.X)
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
func baseTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.StarExpr:
		return baseTypeName(value.X)
	case *ast.ArrayType:
		return baseTypeName(value.Elt)
	case *ast.MapType:
		return baseTypeName(value.Value)
	case *ast.Ident:
		return value.Name
	}
	return ""
}
func fieldNames(field *ast.Field) []string {
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return names
}
func containsName(field *ast.Field, name string) bool {
	for _, current := range field.Names {
		if current.Name == name {
			return true
		}
	}
	return false
}
func fatalf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }

func formatSource(source string) ([]byte, error) { return format.Source([]byte(source)) }

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
