package recipeimport

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func sourceParameters(output recipe.Output) map[string]*ws.ImportedSourceParameters {
	result := map[string]*ws.ImportedSourceParameters{}
	if cloned, err := cloneOutput(output); err == nil {
		output = cloned
	}
	result["output"] = &ws.ImportedSourceParameters{Output: &ws.ImportedOutputParameters{RowGrain: output.RowGrain, CollisionPolicy: output.CollisionPolicy}}
	if x := output.Expand; x != nil {
		result["expand"] = &ws.ImportedSourceParameters{Expansion: &ws.ImportedExpansionParameters{From: x.From, As: x.As, Ordinality: x.Ordinality, EmptyPolicy: x.EmptyPolicy}}
	}
	if x := output.Identity; x != nil && x.Expansion == nil {
		result["identity"] = &ws.ImportedSourceParameters{Identity: &ws.ImportedIdentityParameters{Name: x.Name, Expr: x.Expr}}
	}
	var scope func(string, []recipe.Field, []recipe.CatalogProjection, []recipe.DynamicColumn, []recipe.ExtensionColumn, []recipe.Pivot, []recipe.Traversal)
	scope = func(parent string, fields []recipe.Field, catalogs []recipe.CatalogProjection, dynamics []recipe.DynamicColumn, extensions []recipe.ExtensionColumn, pivots []recipe.Pivot, traversals []recipe.Traversal) {
		if len(fields) > 0 {
			f := &ws.ImportedFieldGroupParameters{Fields: make([]ws.ImportedFieldParameters, 0, len(fields))}
			for i, x := range fields {
				f.Fields = append(f.Fields, ws.ImportedFieldParameters{FieldID: sourceIndexPath(parent, "fields", i), Enabled: true, Name: x.Name, Label: x.Label, Expr: x.Expr, Fallbacks: x.Fallbacks, ValueMode: x.ValueMode})
			}
			result[sourcePath(parent, "fields")] = &ws.ImportedSourceParameters{FieldGroup: f}
		}
		for i, x := range catalogs {
			outputs := append([]ws.ImportedCatalogOutput(nil), x.OutputOverrides...)
			result[sourceIndexPath(parent, "catalogProjections", i)] = &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{Name: x.Name, IncludePaths: x.IncludePaths, ExcludePaths: x.ExcludePaths, Outputs: outputs, Kinds: x.Kinds, Naming: x.Naming}}
		}
		for i, x := range dynamics {
			var outputs []ws.ImportedDynamicOutput
			for _, override := range x.OutputOverrides {
				outputs = append(outputs, ws.ImportedDynamicOutput{Key: override.Key, Enabled: override.Enabled})
			}
			result[sourceIndexPath(parent, "dynamicColumns", i)] = &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{Name: x.Name, ColumnPrefix: x.ColumnPrefix, Source: x.Source, Key: x.Key, Value: x.Value, Outputs: outputs}}
		}
		for i, x := range extensions {
			var outputs []ws.ImportedExtensionOutput
			for _, override := range x.OutputOverrides {
				outputs = append(outputs, ws.ImportedExtensionOutput{Key: override.Key, Enabled: override.Enabled})
			}
			result[sourceIndexPath(parent, "extensionColumns", i)] = &ws.ImportedSourceParameters{Extension: &ws.ImportedExtensionParameters{Name: x.Name, ColumnPrefix: x.ColumnPrefix, Source: x.Source, Outputs: outputs}}
		}
		for i, x := range pivots {
			var outputs []ws.ImportedPivotOutput
			for _, override := range x.OutputOverrides {
				outputs = append(outputs, ws.ImportedPivotOutput{Key: override.Key, Enabled: override.Enabled, Name: cloneOptionalString(override.Name)})
			}
			p := &ws.ImportedPivotParameters{Name: x.Name, ValueFallbacks: x.ValueFallbacks, ItemResourceType: x.ItemResourceType, Columns: x.Columns, ColumnAliases: x.ColumnAliases, Outputs: outputs}
			if x.Discovery != nil {
				p.Discovery = &ws.ImportedPivotDiscoveryParameters{Family: x.Discovery.Family, Path: x.Discovery.Path}
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
			result[sourceIndexPath(parent, "pivots", i)] = &ws.ImportedSourceParameters{Pivot: p}
		}
		for i, x := range traversals {
			id := sourceIndexPath(parent, "traversals", i)
			result[id] = &ws.ImportedSourceParameters{Traversal: &ws.ImportedTraversalParameters{Name: x.Name, ToResourceType: x.ToResourceType, Alias: x.Alias, From: x.From}}
			scope(id, x.Fields, x.CatalogProjections, x.DynamicColumns, x.ExtensionColumns, x.Pivots, x.Traversals)
		}
	}
	scope("", output.Fields, output.CatalogProjections, output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.Traversals)
	return result
}
func expressionPresent(e recipe.Expression) bool {
	return e.Select != "" || e.Call != "" || e.Literal != nil || e.Document != nil
}

func applyParameterOverrides(output *recipe.Output, steps map[string]ws.ImportedSourceStep) error {
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
				catalogs[i].OutputOverrides = nil
				for _, override := range x.Outputs {
					catalogs[i].OutputOverrides = append(catalogs[i].OutputOverrides, recipe.CatalogProjectionOutputOverride{FieldPath: override.FieldPath, Enabled: override.Enabled, Name: override.Name})
				}
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
				dynamics[i].OutputOverrides = nil
				for _, override := range x.Outputs {
					dynamics[i].OutputOverrides = append(dynamics[i].OutputOverrides, recipe.DynamicColumnOutputOverride{Key: override.Key, Enabled: override.Enabled})
				}
			}
		}
		for i := range extensions {
			if p := steps[sourceIndexPath(parent, "extensionColumns", i)].Parameters; p != nil && p.Extension != nil {
				x := p.Extension
				extensions[i].Name = x.Name
				extensions[i].ColumnPrefix = x.ColumnPrefix
				extensions[i].Source = x.Source
				extensions[i].OutputOverrides = nil
				for _, override := range x.Outputs {
					extensions[i].OutputOverrides = append(extensions[i].OutputOverrides, recipe.ExtensionColumnOutputOverride{Key: override.Key, Enabled: override.Enabled})
				}
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
				pivots[i].OutputOverrides = nil
				for _, override := range x.Outputs {
					pivots[i].OutputOverrides = append(pivots[i].OutputOverrides, recipe.PivotOutputOverride{Key: override.Key, Enabled: override.Enabled, Name: cloneOptionalString(override.Name)})
				}
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

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

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
func applyFieldBindings(output *recipe.Output, bindings []ws.ImportedSourceFieldBinding) error {
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
