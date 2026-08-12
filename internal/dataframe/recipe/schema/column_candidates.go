package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// ColumnCandidate is one value-bearing column offered by a concrete recipe
// family at one traversal node. Identity includes the raw semantic key so
// superficially identical labels from different families remain distinct.
type ColumnCandidate struct {
	ID, Output, NodePath, FamilyID, FamilyKind, FamilyName string
	PatchPath, RawKey, RawSystem, RawCode, ExtensionURL    string
	PublicName, Label, ValueSelector, ValueType            string
	Cardinality                                            string
	Population                                             int64
	Examples                                               []string
	Selected, Complete                                     bool
	Diagnostic                                             string
	ExtensionMapping                                       *recipe.ExtensionColumnMapping
}

// ColumnCandidates enumerates all columns controlled by the authored recipe
// at outputName/nodeAliases. It never applies a presentation limit.
func ColumnCandidates(bundle recipe.Bundle, outputName string, nodeAliases []string, fields []FieldCandidate) ([]ColumnCandidate, error) {
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	var output *recipe.Output
	var outputIndex int
	for i := range bundle.Outputs {
		if bundle.Outputs[i].Name == outputName {
			output, outputIndex = &bundle.Outputs[i], i
			break
		}
	}
	if output == nil {
		return nil, fmt.Errorf("output %q was not found", outputName)
	}
	nodePath := "root"
	resourceType := output.RootResourceType
	base := fmt.Sprintf("outputs[%d]", outputIndex)
	ordinary, dynamics, extensions, pivots, projections := output.Fields, output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.CatalogProjections
	traversals := output.Traversals
	for _, wanted := range nodeAliases {
		found := -1
		for i := range traversals {
			alias := traversals[i].Alias
			if alias == "" {
				alias = traversals[i].Name
			}
			if alias == wanted {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("traversal alias %q was not found below %s", wanted, nodePath)
		}
		t := &traversals[found]
		nodePath += "." + wanted
		base += fmt.Sprintf(".traversals[%d]", found)
		resourceType, ordinary, dynamics, extensions, pivots, projections = t.ToResourceType, t.Fields, t.DynamicColumns, t.ExtensionColumns, t.Pivots, t.CatalogProjections
		traversals = t.Traversals
	}
	byPath := make(map[string]FieldCandidate, len(fields))
	for _, field := range fields {
		if field.ResourceType == resourceType {
			byPath[field.Path] = field
		}
	}
	result := make([]ColumnCandidate, 0)
	seenIDs := map[string]bool{}
	familyDefinitions := map[string]string{"FIELD:explicit": "FIELD:explicit"}
	for _, family := range projections {
		familyDefinitions["CATALOG_PROJECTION:"+family.Name] = declarationID("CATALOG_PROJECTION", family.Name, family)
	}
	for _, family := range dynamics {
		value := family
		value.Columns = nil
		value.ColumnMode = ""
		value.ColumnTypes = nil
		value.ColumnSourceKeys = nil
		value.Discovered = false
		familyDefinitions["DYNAMIC:"+family.Name] = declarationID("DYNAMIC", family.Name, value)
	}
	for _, family := range extensions {
		value := family
		value.Columns = nil
		value.ColumnMode = ""
		value.Discovered = false
		familyDefinitions["EXTENSION:"+family.Name] = declarationID("EXTENSION", family.Name, value)
	}
	for _, family := range pivots {
		value := family
		value.Columns = nil
		value.ColumnMode = ""
		value.Discovered = false
		familyDefinitions["PIVOT:"+family.Name] = declarationID("PIVOT", family.Name, value)
	}
	add := func(c ColumnCandidate) {
		familyKey := c.FamilyKind + ":" + c.FamilyName
		c.FamilyID = familyDefinitions[familyKey]
		if c.FamilyID == "" {
			c.FamilyID = familyKey
		}
		identity := strings.Join([]string{outputName, nodePath, c.FamilyID, c.RawSystem, c.RawCode, c.ExtensionURL, c.RawKey}, "\x00")
		sum := sha256.Sum256([]byte(identity))
		c.ID = hex.EncodeToString(sum[:])
		if seenIDs[c.ID] {
			return
		}
		seenIDs[c.ID] = true
		c.Output, c.NodePath = outputName, nodePath
		c.Label = columnLabel(c.PublicName)
		if c.Cardinality == "" {
			c.Cardinality = "ONE"
		}
		if c.Examples == nil {
			c.Examples = []string{}
		}
		result = append(result, c)
	}
	for i, field := range ordinary {
		path := strings.TrimPrefix(field.Expr.Select, "root.")
		if dot := strings.Index(path, "."); dot >= 0 && len(nodeAliases) > 0 {
			path = path[dot+1:]
		}
		observed, ok := byPath[path]
		if !ok || observed.Population <= 0 {
			continue
		}
		add(ColumnCandidate{FamilyKind: "FIELD", FamilyName: "explicit", PatchPath: fmt.Sprintf("%s.fields[%d]", base, i), RawKey: path, PublicName: field.Name, ValueSelector: field.Expr.Select, ValueType: observed.Kind, Population: observed.Population, Examples: observed.Examples, Selected: true, Complete: !observed.DistinctTruncated})
	}
	for _, family := range projections {
		matched := filterCandidates(fields, family)
		complete := len(matched) <= family.MaxColumns
		for _, observed := range matched {
			if observed.ResourceType != resourceType {
				continue
			}
			add(ColumnCandidate{FamilyKind: "CATALOG_PROJECTION", FamilyName: family.Name, PatchPath: fmt.Sprintf("%s.fields", base), RawKey: observed.Path, PublicName: projectionName(family, observed.Path), ValueSelector: qualifyDiscoveredSelector(lastAlias(nodeAliases), observed.Path), ValueType: observed.Kind, Population: observed.Population, Examples: observed.Examples, Complete: complete && !observed.DistinctTruncated, Diagnostic: limitDiagnostic(complete, family.MaxColumns, len(matched))})
		}
	}
	for i, family := range dynamics {
		source, key, transforms, err := dynamicDiscoveryKey(family, lastAlias(nodeAliases))
		if err != nil {
			return nil, err
		}
		observed := byPath[source+"."+key]
		complete := !observed.DistinctTruncated && (family.MaxColumns <= 0 || len(observed.DistinctValues) <= family.MaxColumns)
		selected := stringSet(family.Columns)
		found := map[string]bool{}
		for _, raw := range observed.DistinctValues {
			name, err := applyDynamicKeyTransforms(raw, transforms)
			if err != nil || name == "" {
				continue
			}
			public := name
			prefix := family.Name
			if family.ColumnPrefix != nil {
				prefix = *family.ColumnPrefix
			}
			if prefix != "" {
				public = prefix + "_" + name
			}
			found[name] = true
			semanticMatches := semanticsForKey(fields, source, raw)
			if len(semanticMatches) == 0 {
				semanticMatches = []*SemanticObservation{nil}
			}
			for _, semantic := range semanticMatches {
				population, examples, valueSelector, valueType, cardinality := observed.Population, observed.Examples, exprSelector(family.Value), "unknown", "ONE"
				if semantic != nil {
					population, examples, valueSelector, valueType, cardinality = semantic.Population, semantic.Examples, semantic.ValueSelector, semantic.ValueType, strings.ToUpper(semantic.Cardinality)
				}
				rawSystem, rawCode := "", ""
				if strings.HasSuffix(strings.ToLower(key), "system") {
					rawSystem = raw
				}
				if strings.HasSuffix(strings.ToLower(key), "code") {
					rawCode = raw
				}
				if semantic != nil {
					rawSystem, rawCode = semantic.KeySystem, semantic.KeyCode
				}
				add(ColumnCandidate{FamilyKind: "DYNAMIC", FamilyName: family.Name, PatchPath: fmt.Sprintf("%s.dynamicColumns[%d].columns", base, i), RawKey: raw, RawSystem: rawSystem, RawCode: rawCode, PublicName: public, ValueSelector: valueSelector, ValueType: valueType, Cardinality: cardinality, Population: population, Examples: examples, Selected: selected[name], Complete: complete, Diagnostic: dynamicDiagnostic(observed, family.MaxColumns)})
			}
		}
		for name := range selected {
			if !found[name] {
				public := name
				prefix := family.Name
				if family.ColumnPrefix != nil {
					prefix = *family.ColumnPrefix
				}
				if prefix != "" {
					public = prefix + "_" + name
				}
				add(ColumnCandidate{FamilyKind: "DYNAMIC", FamilyName: family.Name, PatchPath: fmt.Sprintf("%s.dynamicColumns[%d].columns", base, i), RawKey: name, PublicName: public, Selected: true, Complete: false, Diagnostic: "selected key is not populated in this dataset generation"})
			}
		}
	}
	for i, family := range extensions {
		selected := make(map[string]bool)
		for _, mapping := range family.Columns {
			selected[mapping.URL+"\x00"+mapping.Name] = true
		}
		resolved := family
		resolved.ColumnMode = recipe.ColumnModeDiscover
		resolved.Columns = nil
		resolved.MaxColumns = int(^uint(0) >> 1)
		items := []recipe.ExtensionColumn{resolved}
		if err := resolveExtensionColumns(context.Background(), Scope{}, staticDiscovery{fields}, resourceType, lastAlias(nodeAliases), items); err != nil {
			return nil, err
		}
		found := map[string]bool{}
		complete := len(items[0].Columns) <= family.MaxColumns
		for _, mapping := range items[0].Columns {
			found[mapping.URL+"\x00"+mapping.Name] = true
			prefix := family.Name + "__" + mapping.Name
			if family.ColumnPrefix != nil {
				prefix = *family.ColumnPrefix
			}
			public := mapping.Name
			if prefix != "" {
				public = prefix + "_" + mapping.Name
			}
			population, examples, cardinality := int64(0), []string{}, "ONE"
			if semantic := semanticsForKey(fields, "", mapping.URL); len(semantic) > 0 {
				population, examples, cardinality = semantic[0].Population, semantic[0].Examples, strings.ToUpper(semantic[0].Cardinality)
			}
			add(ColumnCandidate{FamilyKind: "EXTENSION", FamilyName: family.Name, PatchPath: fmt.Sprintf("%s.extensionColumns[%d].columns", base, i), RawKey: mapping.URL, ExtensionURL: mapping.URL, PublicName: public, ValueSelector: mapping.ValuePath, ValueType: mapping.ValueType, Cardinality: cardinality, Population: population, Examples: examples, Selected: selected[mapping.URL+"\x00"+mapping.Name], Complete: complete, Diagnostic: limitDiagnostic(complete, family.MaxColumns, len(items[0].Columns)), ExtensionMapping: &mapping})
		}
		for _, mapping := range family.Columns {
			if !found[mapping.URL+"\x00"+mapping.Name] {
				copy := mapping
				add(ColumnCandidate{FamilyKind: "EXTENSION", FamilyName: family.Name, PatchPath: fmt.Sprintf("%s.extensionColumns[%d].columns", base, i), RawKey: mapping.URL, ExtensionURL: mapping.URL, PublicName: extensionPublicName(family, mapping), ValueSelector: mapping.ValuePath, ValueType: mapping.ValueType, Selected: true, Complete: false, Diagnostic: "selected extension is not populated in this dataset generation", ExtensionMapping: &copy})
			}
		}
	}
	for i, pivot := range pivots {
		if pivot.Discovery == nil {
			continue
		}
		selected := stringSet(pivot.Columns)
		keys := map[string]FieldCandidate{}
		for _, field := range fields {
			if field.ResourceType != resourceType || !field.PivotCandidate {
				continue
			}
			if pivot.Discovery.Family != "" && !strings.EqualFold(field.PivotFamily, pivot.Discovery.Family) {
				continue
			}
			if pivot.Discovery.Path != "" && field.Path != pivot.Discovery.Path {
				continue
			}
			for _, key := range append(field.PivotColumns, field.DistinctValues...) {
				keys[key] = field
			}
		}
		complete := len(keys) <= pivot.Discovery.MaxColumns
		for key, field := range keys {
			add(ColumnCandidate{FamilyKind: "PIVOT", FamilyName: pivot.Name, PatchPath: fmt.Sprintf("%s.pivots[%d].columns", base, i), RawKey: key, RawCode: key, PublicName: pivot.Name + "__" + candidateKeyName(key), ValueSelector: field.PivotValueSelect, ValueType: "unknown", Population: field.Population, Examples: field.Examples, Selected: selected[key], Complete: complete && !field.DistinctTruncated, Diagnostic: limitDiagnostic(complete, pivot.Discovery.MaxColumns, len(keys))})
		}
		for key := range selected {
			if _, ok := keys[key]; !ok {
				add(ColumnCandidate{FamilyKind: "PIVOT", FamilyName: pivot.Name, PatchPath: fmt.Sprintf("%s.pivots[%d].columns", base, i), RawKey: key, RawCode: key, PublicName: pivot.Name + "__" + candidateKeyName(key), Selected: true, Complete: false, Diagnostic: "selected pivot key is not populated in this dataset generation"})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

type staticDiscovery struct{ fields []FieldCandidate }

func (d staticDiscovery) Fields(_ context.Context, _ Scope, _ string) ([]FieldCandidate, error) {
	return d.fields, nil
}

func lastAlias(path []string) string {
	if len(path) == 0 {
		return "root"
	}
	return path[len(path)-1]
}
func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func exprSelector(expr *recipe.Expression) string {
	if expr == nil {
		return ""
	}
	return expr.Select
}
func columnLabel(value string) string {
	value = strings.Trim(value, "_")
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	for i, w := range words {
		if w == strings.ToUpper(w) {
			words[i] = w
		} else {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
func limitDiagnostic(ok bool, max, found int) string {
	if ok {
		return ""
	}
	return fmt.Sprintf("family matched %d keys, exceeding maxColumns %d", found, max)
}
func dynamicDiagnostic(f FieldCandidate, max int) string {
	if f.DistinctTruncated {
		return "catalog distinct-key profiling was truncated"
	}
	return limitDiagnostic(max <= 0 || len(f.DistinctValues) <= max, max, len(f.DistinctValues))
}

func extensionPublicName(family recipe.ExtensionColumn, mapping recipe.ExtensionColumnMapping) string {
	prefix := family.Name + "__" + mapping.Name
	if family.ColumnPrefix != nil {
		prefix = *family.ColumnPrefix
	}
	if prefix == "" {
		return mapping.Name
	}
	return prefix + "_" + mapping.Name
}

func candidateKeyName(value string) string {
	return strings.Trim(strings.NewReplacer("[]", "", ".", "_", "-", "_", "/", "_").Replace(value), "_")
}

func declarationID(kind, name string, value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return kind + ":" + name + ":" + hex.EncodeToString(sum[:8])
}

func semanticsForKey(fields []FieldCandidate, source, raw string) []*SemanticObservation {
	result := make([]*SemanticObservation, 0)
	for i := range fields {
		for j := range fields[i].SemanticObservations {
			observation := &fields[i].SemanticObservations[j]
			if source != "" && observation.SourcePath != "" && !(observation.SourcePath == source || strings.HasPrefix(source, observation.SourcePath+".")) {
				continue
			}
			if observation.KeyCode == raw || observation.KeySystem == raw || observation.KeyDisplay == raw {
				result = append(result, observation)
			}
		}
	}
	return result
}
