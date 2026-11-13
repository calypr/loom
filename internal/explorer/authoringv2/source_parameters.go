package authoringv2

import (
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"reflect"
	"strings"
)

// ImportedSourceParameters is a closed edit of one original pinned source step.
// Caps, policies and enablement remain owned by their existing step properties.
type ImportedSourceParameters struct {
	Output     *ImportedOutputParameters     `json:"output,omitempty"`
	FieldGroup *ImportedFieldGroupParameters `json:"fieldGroup,omitempty"`
	Catalog    *ImportedCatalogParameters    `json:"catalog,omitempty"`
	Dynamic    *ImportedDynamicParameters    `json:"dynamic,omitempty"`
	Extension  *ImportedExtensionParameters  `json:"extension,omitempty"`
	Pivot      *ImportedPivotParameters      `json:"pivot,omitempty"`
	Expansion  *ImportedExpansionParameters  `json:"expansion,omitempty"`
	Identity   *ImportedIdentityParameters   `json:"identity,omitempty"`
	Traversal  *ImportedTraversalParameters  `json:"traversal,omitempty"`
}
type ImportedOutputParameters struct {
	RowGrain        string `json:"rowGrain"`
	CollisionPolicy string `json:"collisionPolicy,omitempty"`
}
type ImportedFieldGroupParameters struct {
	Fields []ImportedFieldParameters `json:"fields"`
}
type ImportedFieldParameters struct {
	FieldID   string              `json:"fieldId"`
	Enabled   bool                `json:"enabled"`
	Name      string              `json:"name"`
	Label     string              `json:"label,omitempty"`
	Expr      recipe.Expression   `json:"expr"`
	Fallbacks []recipe.Expression `json:"fallbacks,omitempty"`
	ValueMode recipe.ValueMode    `json:"valueMode,omitempty"`
}
type ImportedCatalogParameters struct {
	Name         string              `json:"name"`
	IncludePaths []string            `json:"includePaths,omitempty"`
	ExcludePaths []string            `json:"excludePaths,omitempty"`
	Kinds        []string            `json:"kinds,omitempty"`
	Naming       recipe.ColumnNaming `json:"naming,omitempty"`
}
type ImportedDynamicParameters struct {
	Name         string             `json:"name"`
	ColumnPrefix *string            `json:"columnPrefix,omitempty"`
	Source       recipe.Expression  `json:"source"`
	Key          *recipe.Expression `json:"key,omitempty"`
	Value        *recipe.Expression `json:"value,omitempty"`
}
type ImportedExtensionParameters struct {
	Name         string            `json:"name"`
	ColumnPrefix *string           `json:"columnPrefix,omitempty"`
	Source       recipe.Expression `json:"source"`
}
type ImportedPivotDiscoveryParameters struct {
	Family string `json:"family,omitempty"`
	Path   string `json:"path,omitempty"`
}
type ImportedPivotParameters struct {
	Name             string                            `json:"name"`
	Discovery        *ImportedPivotDiscoveryParameters `json:"discovery,omitempty"`
	ColumnExpr       *recipe.Expression                `json:"columnExpr,omitempty"`
	ValueExpr        *recipe.Expression                `json:"valueExpr,omitempty"`
	ValueFallbacks   []recipe.Expression               `json:"valueFallbacks,omitempty"`
	ItemSource       *recipe.Expression                `json:"itemSource,omitempty"`
	ItemResourceType string                            `json:"itemResourceType,omitempty"`
	Columns          []string                          `json:"columns,omitempty"`
	ColumnAliases    map[string]string                 `json:"columnAliases,omitempty"`
}
type ImportedExpansionParameters struct {
	From        recipe.Expression           `json:"from"`
	As          string                      `json:"as"`
	Ordinality  string                      `json:"ordinality,omitempty"`
	EmptyPolicy recipe.ExpansionEmptyPolicy `json:"emptyPolicy,omitempty"`
}
type ImportedIdentityParameters struct {
	Name string            `json:"name"`
	Expr recipe.Expression `json:"expr"`
}
type ImportedTraversalParameters struct {
	Name           string             `json:"name"`
	ToResourceType string             `json:"toResourceType"`
	Alias          string             `json:"alias,omitempty"`
	From           *recipe.Expression `json:"from,omitempty"`
}

func (p *ImportedSourceParameters) UnmarshalJSON(raw []byte) error {
	type wire ImportedSourceParameters
	var v wire
	if err := strictDecode(raw, &v); err != nil {
		return err
	}
	*p = ImportedSourceParameters(v)
	return nil
}

func (p *ImportedSourceParameters) Validate(kind string) error {
	if p == nil {
		return nil
	}
	arms := []struct {
		kind    string
		present bool
	}{
		{"OUTPUT", p.Output != nil}, {"FIELD_GROUP", p.FieldGroup != nil}, {"CATALOG_PROJECTION", p.Catalog != nil},
		{"DYNAMIC_COLUMN", p.Dynamic != nil}, {"EXTENSION_COLUMN", p.Extension != nil},
		{"PIVOT", p.Pivot != nil}, {"EXPANSION", p.Expansion != nil}, {"IDENTITY", p.Identity != nil}, {"TRAVERSAL", p.Traversal != nil},
	}
	count := 0
	for _, arm := range arms {
		if arm.present {
			count++
			if kind != "" && kind != arm.kind {
				return fmt.Errorf("source parameters must match source step kind %s", kind)
			}
		}
	}
	if count != 1 {
		return fmt.Errorf("source parameters require exactly one kind")
	}
	nodes := 0
	var visit func(reflect.Value, int) error
	visit = func(v reflect.Value, depth int) error {
		if depth > 64 {
			return fmt.Errorf("source parameters exceed depth 64")
		}
		if !v.IsValid() {
			return nil
		}
		if v.Type() == reflect.TypeOf(recipe.Expression{}) {
			e := v.Interface().(recipe.Expression)
			nodes++
			if nodes > 4096 {
				return fmt.Errorf("source parameters exceed 4096 expression nodes")
			}
			if e.Document != nil {
				return fmt.Errorf("source parameters cannot reference other documents")
			}
			if _, err := e.MarshalJSON(); err != nil {
				return err
			}
			if e.Call != "" && kind == "" {
				switch e.Call {
				case "first", "all", "reference_id", "last_segment", "coalesce", "coalesce_string", "join", "concat", "uuid5":
				default:
					return fmt.Errorf("source expression function %q is unsupported", e.Call)
				}
			}
			if kind == "" && len(e.Literal) > 4096 {
				return fmt.Errorf("source literal exceeds 4096 bytes")
			}
			if kind == "" && len(e.Literal) > 0 {
				var literal any
				if err := json.Unmarshal(e.Literal, &literal); err != nil {
					return err
				}
				switch literal.(type) {
				case nil, bool, string, float64:
				default:
					return fmt.Errorf("source literals must be primitive values")
				}
			}
			for _, a := range e.Args {
				if err := visit(reflect.ValueOf(a), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				return visit(v.Elem(), depth)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if err := visit(v.Field(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if v.Len() > 4096 {
				return fmt.Errorf("source parameter array exceeds 4096 entries")
			}
			for i := 0; i < v.Len(); i++ {
				if err := visit(v.Index(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			if v.Len() > 4096 {
				return fmt.Errorf("source parameter map exceeds 4096 entries")
			}
			for _, k := range v.MapKeys() {
				if err := visit(k, depth+1); err != nil {
					return err
				}
				if err := visit(v.MapIndex(k), depth+1); err != nil {
					return err
				}
			}
		case reflect.String:
			if len(v.String()) > 2048 {
				return fmt.Errorf("source parameter text exceeds 2048 bytes")
			}
		}
		return nil
	}
	if err := visit(reflect.ValueOf(p), 0); err != nil {
		return err
	}
	if p.FieldGroup != nil {
		seen := map[string]bool{}
		for _, f := range p.FieldGroup.Fields {
			if strings.TrimSpace(f.FieldID) == "" || seen[f.FieldID] {
				return fmt.Errorf("source field identities must be nonempty and unique")
			}
			seen[f.FieldID] = true
			if !f.ValueMode.Valid() {
				return fmt.Errorf("invalid field value mode")
			}
		}
	}
	if p.Expansion != nil && !p.Expansion.EmptyPolicy.Valid() {
		return fmt.Errorf("invalid expansion missing policy")
	}
	return nil
}
