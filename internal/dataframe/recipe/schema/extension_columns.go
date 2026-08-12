package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// resolveExtensionColumns freezes URL-to-value mappings from the same scoped
// catalog snapshot used by the other recipe discovery features. Catalog paths
// below nested Extension arrays are deliberately retained; the semantic layer
// lowers each frozen mapping as a bounded dynamic family.
func resolveExtensionColumns(ctx context.Context, scope Scope, discovery Discovery, resourceType, alias string, items []recipe.ExtensionColumn) error {
	for index := range items {
		item := &items[index]
		item.Discovered = true
		selector := strings.TrimSpace(item.Source.Select)
		if selector == "" {
			return fmt.Errorf("extension column %q source must be a selector", item.Name)
		}
		selector = strings.TrimPrefix(selector, alias+".")
		selector = strings.TrimPrefix(selector, "root.")
		selector = strings.TrimPrefix(selector, ".")
		if !strings.HasSuffix(selector, "extension[]") {
			return fmt.Errorf("extension column %q source must select a repeated Extension", item.Name)
		}
		resolved, ok := fhirschema.ResolvePath(resourceType, selector)
		if !ok || resolved.PropertyRef != "Extension" {
			// Generated/default recipes may carry optional extension families for
			// several FHIR roots. When a selected root does not expose that path,
			// freeze the family to an empty set. This is equivalent to discovery
			// finding no populated extension keys and keeps otherwise valid fields
			// previewable without inventing a selector for the resource.
			item.Columns = []recipe.ExtensionColumnMapping{}
			continue
		}
		candidates, err := discovery.Fields(ctx, scope, resourceType)
		if err != nil {
			return err
		}
		byURL := map[string][]extensionValueCandidate{}
		for _, candidate := range candidates {
			for _, observation := range candidate.ExtensionValues {
				if strings.TrimSpace(observation.URL) == "" {
					continue
				}
				if !extensionSourceWithin(selector, observation.SourcePath) {
					continue
				}
				urlPath := append([]string(nil), observation.URLPath...)
				identity := strings.Join(append(append([]string(nil), urlPath...), observation.URL), "\x00")
				byURL[identity] = append(byURL[identity], extensionValueCandidate{url: observation.URL, path: observation.SourcePath, valuePath: observation.ValuePath, valueType: observation.ValueType, urlPath: urlPath, fields: candidates})
			}
			if len(candidate.ExtensionValues) > 0 {
				// Correlated observations supersede the legacy flattened
				// distinct-value fallback for this URL field. Mixing both would
				// incorrectly assign every observed value[x] kind to every URL.
				continue
			}
			if !strings.HasPrefix(candidate.Path, selector+".") || !strings.HasSuffix(candidate.Path, ".url") {
				continue
			}
			extensionPath := strings.TrimSuffix(candidate.Path, ".url")
			if candidate.DistinctTruncated {
				return fmt.Errorf("extension column %q URL discovery at %q was truncated", item.Name, candidate.Path)
			}
			for _, url := range candidate.DistinctValues {
				if strings.TrimSpace(url) == "" {
					continue
				}
				byURL[url] = append(byURL[url], extensionValueCandidate{url: url, path: extensionPath, fields: candidates})
			}
		}
		mappings := make([]recipe.ExtensionColumnMapping, 0, len(byURL))
		for _, values := range byURL {
			url := values[0].url
			name, err := applyDynamicKeyTransforms(url, []string{"last_segment", "sanitize_name"})
			if err != nil || name == "" {
				continue
			}
			valuePath, valueType, sourcePath := extensionValueMapping(resourceType, values)
			if len(values) > 0 && len(values[0].urlPath) > 0 {
				parts := make([]string, 0, len(values[0].urlPath)+1)
				for _, ancestor := range values[0].urlPath {
					segment, segmentErr := applyDynamicKeyTransforms(ancestor, []string{"last_segment", "sanitize_name"})
					if segmentErr == nil && segment != "" {
						parts = append(parts, segment)
					}
				}
				parts = append(parts, name)
				name = strings.Join(parts, "__")
			}
			mappings = append(mappings, recipe.ExtensionColumnMapping{Name: name, URL: url, SourcePath: sourcePath, ValuePath: valuePath, ValueType: valueType})
		}
		sort.Slice(mappings, func(i, j int) bool {
			if mappings[i].Name == mappings[j].Name {
				return mappings[i].URL < mappings[j].URL
			}
			return mappings[i].Name < mappings[j].Name
		})
		if len(mappings) > item.MaxColumns {
			return fmt.Errorf("extension column %q discovery found %d columns, exceeding maxColumns %d", item.Name, len(mappings), item.MaxColumns)
		}
		item.Columns = mappings
	}
	return nil
}

type extensionValueCandidate struct {
	url       string
	path      string
	valuePath string
	valueType string
	urlPath   []string
	fields    []FieldCandidate
}

func extensionSourceWithin(declared, observed string) bool {
	declared = strings.TrimSuffix(strings.TrimSpace(declared), ".")
	observed = strings.TrimSuffix(strings.TrimSpace(observed), ".")
	return observed == declared || strings.HasPrefix(observed, declared+".extension[]")
}

func extensionValueMapping(resourceType string, candidates []extensionValueCandidate) (string, string, string) {
	type observed struct{ path, kind string }
	values := map[string]observed{}
	sourcePath := ""
	for _, candidate := range candidates {
		if sourcePath == "" || candidate.path < sourcePath {
			sourcePath = candidate.path
		}
		if candidate.valueType != "" {
			values[candidate.valuePath+"\x00"+candidate.valueType] = observed{path: candidate.valuePath, kind: candidate.valueType}
			continue
		}
		prefix := candidate.path + "."
		for _, field := range candidate.fields {
			if !strings.HasPrefix(field.Path, prefix) || !strings.Contains(strings.TrimPrefix(field.Path, prefix), "value") {
				continue
			}
			relative := strings.TrimPrefix(field.Path, prefix)
			if strings.Contains(relative, ".") || !strings.HasPrefix(relative, "value") {
				continue
			}
			metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, field.Path)
			if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown || metadata.Repeated {
				continue
			}
			kind := string(metadata.Primitive)
			values[kind] = observed{path: relative, kind: kind}
		}
	}
	if len(values) != 1 {
		// Complex and mixed value[x] representations are preserved as a JSON
		// string by the semantic/physical extension mapper.
		return "", "string", sourcePath
	}
	for _, value := range values {
		return value.path, value.kind, sourcePath
	}
	return "", "string", ""
}
