package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Cache struct {
	mu         sync.RWMutex
	fields     map[string][]PopulatedField
	references map[string][]PopulatedReference
}

func NewCache() *Cache {
	return &Cache{
		fields:     make(map[string][]PopulatedField),
		references: make(map[string][]PopulatedReference),
	}
}

func (c *Cache) DiscoverFields(fn func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error)) func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error) {
	return func(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
		key, err := fieldKey(opts)
		if err != nil {
			return nil, err
		}
		c.mu.RLock()
		cached, ok := c.fields[key]
		c.mu.RUnlock()
		if ok {
			return cloneFields(cached), nil
		}
		results, err := fn(ctx, opts)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.fields[key] = cloneFields(results)
		c.mu.Unlock()
		return cloneFields(results), nil
	}
}

func (c *Cache) DiscoverReferences(fn func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error)) func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error) {
	return func(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
		key, err := referenceKey(opts)
		if err != nil {
			return nil, err
		}
		c.mu.RLock()
		cached, ok := c.references[key]
		c.mu.RUnlock()
		if ok {
			return cloneReferences(cached), nil
		}
		results, err := fn(ctx, opts)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.references[key] = cloneReferences(results)
		c.mu.Unlock()
		return cloneReferences(results), nil
	}
}

func (c *Cache) InvalidateProject(project string) {
	project = strings.TrimSpace(project)
	if project == "" {
		c.InvalidateAll()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.fields {
		if strings.HasPrefix(key, project+"|") {
			delete(c.fields, key)
		}
	}
	for key := range c.references {
		if strings.HasPrefix(key, project+"|") {
			delete(c.references, key)
		}
	}
}

func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fields = make(map[string][]PopulatedField)
	c.references = make(map[string][]PopulatedReference)
}

func fieldKey(opts PopulatedFieldOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%s|%t|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(opts.ResourceType), opts.PivotOnly, scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func referenceKey(opts PopulatedReferenceOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	mode := opts.Mode
	if mode == "" {
		mode = TraversalModeStorage
	}
	filter := opts.FromType
	if mode == TraversalModeBuilder {
		filter = opts.NodeType
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(filter), mode, scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func datasetGenerationKey(generation string) string {
	generation = NormalizeDatasetGeneration(generation)
	if !HasDatasetGeneration(generation) {
		return "legacy"
	}
	encoded, _ := json.Marshal(generation)
	return "generation:" + string(encoded)
}

func authScopeKey(paths []string, explicitUnrestricted *bool) (string, error) {
	if EffectiveAuthResourcePathsUnrestricted(paths, explicitUnrestricted) {
		return "unrestricted", nil
	}
	normalized := append([]string(nil), paths...)
	sort.Strings(normalized)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return "restricted:" + string(encoded), nil
}

func cloneFields(in []PopulatedField) []PopulatedField {
	if len(in) == 0 {
		return []PopulatedField{}
	}
	out := make([]PopulatedField, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].DistinctValues != nil {
			out[i].DistinctValues = append([]string(nil), in[i].DistinctValues...)
		}
		if in[i].PivotColumns != nil {
			out[i].PivotColumns = append([]string(nil), in[i].PivotColumns...)
		}
		if in[i].ExtensionValues != nil {
			out[i].ExtensionValues = append([]ExtensionValueObservation(nil), in[i].ExtensionValues...)
			for j := range out[i].ExtensionValues {
				out[i].ExtensionValues[j].URLPath = append([]string(nil), in[i].ExtensionValues[j].URLPath...)
			}
		}
	}
	return out
}

func cloneReferences(in []PopulatedReference) []PopulatedReference {
	if len(in) == 0 {
		return []PopulatedReference{}
	}
	out := make([]PopulatedReference, len(in))
	copy(out, in)
	return out
}
