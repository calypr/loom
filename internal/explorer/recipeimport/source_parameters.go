package recipeimport

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// sourceParameters projects existing recipe facts without introducing another AST.
func sourceParameters(output recipe.Output) map[string]*authoringv2.ImportedSourceParameters {
	result := map[string]*authoringv2.ImportedSourceParameters{}
	if cloned, err := cloneOutput(output); err == nil {
		output = cloned
	}
	result["output"] = &authoringv2.ImportedSourceParameters{Output: &authoringv2.ImportedOutputParameters{RowGrain: output.RowGrain, CollisionPolicy: output.CollisionPolicy}}
	if x := output.Expand; x != nil {
		result["expand"] = &authoringv2.ImportedSourceParameters{Expansion: &authoringv2.ImportedExpansionParameters{From: x.From, As: x.As, Ordinality: x.Ordinality, EmptyPolicy: x.EmptyPolicy}}
	}
	if x := output.Identity; x != nil && x.Expansion == nil {
		result["identity"] = &authoringv2.ImportedSourceParameters{Identity: &authoringv2.ImportedIdentityParameters{Name: x.Name, Expr: x.Expr}}
	}
	var scope func(string, []recipe.Field, []recipe.CatalogProjection, []recipe.DynamicColumn, []recipe.ExtensionColumn, []recipe.Pivot, []recipe.Traversal)
	scope = func(parent string, fields []recipe.Field, catalogs []recipe.CatalogProjection, dynamics []recipe.DynamicColumn, extensions []recipe.ExtensionColumn, pivots []recipe.Pivot, traversals []recipe.Traversal) {
		if len(fields) > 0 {
			f := &authoringv2.ImportedFieldGroupParameters{Fields: make([]authoringv2.ImportedFieldParameters, 0, len(fields))}
			for i, x := range fields {
				f.Fields = append(f.Fields, authoringv2.ImportedFieldParameters{FieldID: sourceIndexPath(parent, "fields", i), Enabled: true, Name: x.Name, Label: x.Label, Expr: x.Expr, Fallbacks: x.Fallbacks, ValueMode: x.ValueMode})
			}
			result[sourcePath(parent, "fields")] = &authoringv2.ImportedSourceParameters{FieldGroup: f}
		}
		for i, x := range catalogs {
			result[sourceIndexPath(parent, "catalogProjections", i)] = &authoringv2.ImportedSourceParameters{Catalog: &authoringv2.ImportedCatalogParameters{Name: x.Name, IncludePaths: x.IncludePaths, ExcludePaths: x.ExcludePaths, Kinds: x.Kinds, Naming: x.Naming}}
		}
		for i, x := range dynamics {
			result[sourceIndexPath(parent, "dynamicColumns", i)] = &authoringv2.ImportedSourceParameters{Dynamic: &authoringv2.ImportedDynamicParameters{Name: x.Name, ColumnPrefix: x.ColumnPrefix, Source: x.Source, Key: x.Key, Value: x.Value}}
		}
		for i, x := range extensions {
			result[sourceIndexPath(parent, "extensionColumns", i)] = &authoringv2.ImportedSourceParameters{Extension: &authoringv2.ImportedExtensionParameters{Name: x.Name, ColumnPrefix: x.ColumnPrefix, Source: x.Source}}
		}
		for i, x := range pivots {
			p := &authoringv2.ImportedPivotParameters{Name: x.Name, ValueFallbacks: x.ValueFallbacks, ItemResourceType: x.ItemResourceType, Columns: x.Columns, ColumnAliases: x.ColumnAliases}
			if x.Discovery != nil {
				p.Discovery = &authoringv2.ImportedPivotDiscoveryParameters{Family: x.Discovery.Family, Path: x.Discovery.Path}
			}
			if expressionPresent(x.ColumnExpr) {
				e := x.ColumnExpr
				p.ColumnExpr = &e
			}
			if expressionPresent(x.ValueExpr) {
				e := x.ValueExpr
				p.ValueExpr = &e
			}
			if expressionPresent(x.ItemSource) {
				e := x.ItemSource
				p.ItemSource = &e
			}
			result[sourceIndexPath(parent, "pivots", i)] = &authoringv2.ImportedSourceParameters{Pivot: p}
		}
		for i, x := range traversals {
			id := sourceIndexPath(parent, "traversals", i)
			result[id] = &authoringv2.ImportedSourceParameters{Traversal: &authoringv2.ImportedTraversalParameters{Name: x.Name, ToResourceType: x.ToResourceType, Alias: x.Alias, From: x.From}}
			scope(id, x.Fields, x.CatalogProjections, x.DynamicColumns, x.ExtensionColumns, x.Pivots, x.Traversals)
		}
	}
	scope("", output.Fields, output.CatalogProjections, output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.Traversals)
	return result
}
func expressionPresent(e recipe.Expression) bool {
	return e.Select != "" || e.Call != "" || e.Literal != nil || e.Document != nil
}

// applyParameterOverrides edits the deep-cloned pin by ORIGINAL paths, before
// enablement removes entries. Children never shift their persisted identities.
func applyParameterOverrides(output *recipe.Output, steps map[string]authoringv2.ImportedSourceStep) error {
	if p := steps["output"].Parameters; p != nil && p.Output != nil {
		output.RowGrain = p.Output.RowGrain
		output.CollisionPolicy = p.Output.CollisionPolicy
	}
	if p := steps["expand"].Parameters; p != nil && p.Expansion != nil {
		if output.Expand == nil {
			return fmt.Errorf("source expansion is unavailable")
		}
		x := p.Expansion
		output.Expand.From = x.From
		output.Expand.As = x.As
		output.Expand.Ordinality = x.Ordinality
		output.Expand.EmptyPolicy = x.EmptyPolicy
	}
	if p := steps["identity"].Parameters; p != nil && p.Identity != nil {
		if output.Identity == nil || output.Identity.Expansion != nil {
			return fmt.Errorf("source identity expression is unavailable")
		}
		output.Identity.Name = p.Identity.Name
		output.Identity.Expr = p.Identity.Expr
	}
	var scope func(string, *[]recipe.Field, []recipe.CatalogProjection, []recipe.DynamicColumn, []recipe.ExtensionColumn, []recipe.Pivot, []recipe.Traversal) error
	scope = func(parent string, fields *[]recipe.Field, catalogs []recipe.CatalogProjection, dynamics []recipe.DynamicColumn, extensions []recipe.ExtensionColumn, pivots []recipe.Pivot, traversals []recipe.Traversal) error {
		if p := steps[sourcePath(parent, "fields")].Parameters; p != nil && p.FieldGroup != nil {
			f := p.FieldGroup.Fields
			if len(f) != len(*fields) {
				return fmt.Errorf("source field edits must retain the original field set")
			}
			out := make([]recipe.Field, 0, len(f))
			for i, x := range f {
				if x.FieldID != sourceIndexPath(parent, "fields", i) {
					return fmt.Errorf("source field %d must retain its original path identity and order", i)
				}
				if !x.Enabled {
					continue
				}
				old := (*fields)[i]
				old.Name = x.Name
				old.Label = x.Label
				old.Expr = x.Expr
				old.Fallbacks = x.Fallbacks
				old.ValueMode = x.ValueMode
				out = append(out, old)
			}
			*fields = out
		}
		for i := range catalogs {
			if p := steps[sourceIndexPath(parent, "catalogProjections", i)].Parameters; p != nil && p.Catalog != nil {
				x := p.Catalog
				catalogs[i].Name = x.Name
				catalogs[i].IncludePaths = x.IncludePaths
				catalogs[i].ExcludePaths = x.ExcludePaths
				catalogs[i].Kinds = x.Kinds
				catalogs[i].Naming = x.Naming
			}
		}
		for i := range dynamics {
			if p := steps[sourceIndexPath(parent, "dynamicColumns", i)].Parameters; p != nil && p.Dynamic != nil {
				x := p.Dynamic
				dynamics[i].Name = x.Name
				dynamics[i].ColumnPrefix = x.ColumnPrefix
				dynamics[i].Source = x.Source
				dynamics[i].Key = x.Key
				dynamics[i].Value = x.Value
			}
		}
		for i := range extensions {
			if p := steps[sourceIndexPath(parent, "extensionColumns", i)].Parameters; p != nil && p.Extension != nil {
				x := p.Extension
				extensions[i].Name = x.Name
				extensions[i].ColumnPrefix = x.ColumnPrefix
				extensions[i].Source = x.Source
			}
		}
		for i := range pivots {
			if p := steps[sourceIndexPath(parent, "pivots", i)].Parameters; p != nil && p.Pivot != nil {
				x := p.Pivot
				pivots[i].Name = x.Name
				pivots[i].ColumnExpr = recipe.Expression{}
				pivots[i].ValueExpr = recipe.Expression{}
				pivots[i].ItemSource = recipe.Expression{}
				if x.ColumnExpr != nil {
					pivots[i].ColumnExpr = *x.ColumnExpr
				}
				if x.ValueExpr != nil {
					pivots[i].ValueExpr = *x.ValueExpr
				}
				if x.ItemSource != nil {
					pivots[i].ItemSource = *x.ItemSource
				}
				pivots[i].ValueFallbacks = x.ValueFallbacks
				pivots[i].ItemResourceType = x.ItemResourceType
				pivots[i].Columns = x.Columns
				pivots[i].ColumnAliases = x.ColumnAliases
				cap := 0
				if pivots[i].Discovery != nil {
					cap = pivots[i].Discovery.MaxColumns
				}
				pivots[i].Discovery = nil
				if x.Discovery != nil {
					pivots[i].Discovery = &recipe.PivotDiscovery{Family: x.Discovery.Family, Path: x.Discovery.Path, MaxColumns: cap}
				}
			}
		}
		for i := range traversals {
			id := sourceIndexPath(parent, "traversals", i)
			x := &traversals[i]
			if p := steps[id].Parameters; p != nil && p.Traversal != nil {
				v := p.Traversal
				x.Name = v.Name
				x.ToResourceType = v.ToResourceType
				x.Alias = v.Alias
				x.From = v.From
			}
			if err := scope(id, &x.Fields, x.CatalogProjections, x.DynamicColumns, x.ExtensionColumns, x.Pivots, x.Traversals); err != nil {
				return err
			}
		}
		return nil
	}
	return scope("", &output.Fields, output.CatalogProjections, output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.Traversals)
}

// ImportedFieldSlot is an exact original field location and its compiler naming
// symbol. Name is recipe syntax, never a presentation label or fuzzy match.
type ImportedFieldSlot struct {
	FieldID, SourceStepID, Name string
	Field                       recipe.Field
}

func ImportedFieldSlots(output recipe.Output) []ImportedFieldSlot {
	var slots []ImportedFieldSlot
	var walk func(string, string, []recipe.Field, []recipe.Traversal)
	walk = func(parent, prefix string, fields []recipe.Field, traversals []recipe.Traversal) {
		for i, f := range fields {
			name := f.Name
			if prefix != "" {
				name = prefix + "__" + name
			}
			slots = append(slots, ImportedFieldSlot{sourceIndexPath(parent, "fields", i), sourcePath(parent, "fields"), name, f})
		}
		for i, t := range traversals {
			alias := t.Alias
			if alias == "" {
				alias = t.ToResourceType
			}
			next := alias
			switch output.TraversalColumnNaming.Normalized() {
			case recipe.TraversalColumnNamingExact:
				next = ""
			case recipe.TraversalColumnNamingAlias:
			default:
				if prefix != "" {
					next = prefix + "__" + alias
				}
			}
			walk(sourceIndexPath(parent, "traversals", i), next, t.Fields, t.Traversals)
		}
	}
	walk("", "", output.Fields, output.Traversals)
	return slots
}
func applyFieldBindings(output *recipe.Output, bindings []authoringv2.ImportedSourceFieldBinding) error {
	ids := map[string]string{}
	for _, b := range bindings {
		ids[b.FieldID] = b.ColumnID
	}
	var walk func(string, []recipe.Field, []recipe.Traversal) error
	walk = func(parent string, fields []recipe.Field, traversals []recipe.Traversal) error {
		for i := range fields {
			key := sourceIndexPath(parent, "fields", i)
			if id, ok := ids[key]; ok {
				if fields[i].ColumnID != "" && fields[i].ColumnID != id {
					return fmt.Errorf("field binding %q conflicts with pinned column identity", key)
				}
				fields[i].ColumnID = id
				delete(ids, key)
			}
		}
		for i, t := range traversals {
			if err := walk(sourceIndexPath(parent, "traversals", i), t.Fields, t.Traversals); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("", output.Fields, output.Traversals); err != nil {
		return err
	}
	if len(ids) > 0 {
		return fmt.Errorf("field binding does not identify an original pinned field")
	}
	return nil
}
