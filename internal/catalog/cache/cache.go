package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/loom/internal/catalog"
)

type Cache struct {
	mu         sync.RWMutex
	fields     map[string][]catalog.PopulatedField
	references map[string][]catalog.PopulatedReference
}

func New() *Cache {
	return &Cache{
		fields:     make(map[string][]catalog.PopulatedField),
		references: make(map[string][]catalog.PopulatedReference),
	}
}

func (c *Cache) DiscoverFields(fn func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)) func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	return func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
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

func (c *Cache) DiscoverReferences(fn func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)) func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
	return func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
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
	c.fields = make(map[string][]catalog.PopulatedField)
	c.references = make(map[string][]catalog.PopulatedReference)
}

func fieldKey(opts catalog.PopulatedFieldOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%s|%t|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(opts.ResourceType), opts.PivotOnly, scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func referenceKey(opts catalog.PopulatedReferenceOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(opts.NodeType), strings.TrimSpace(opts.Mode), scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func datasetGenerationKey(generation string) string {
	generation = catalog.NormalizeDatasetGeneration(generation)
	if !catalog.HasDatasetGeneration(generation) {
		return "legacy"
	}
	encoded, _ := json.Marshal(generation)
	return "generation:" + string(encoded)
}

func authScopeKey(paths []string, explicitUnrestricted *bool) (string, error) {
	if catalog.EffectiveAuthResourcePathsUnrestricted(paths, explicitUnrestricted) {
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

func cloneFields(in []catalog.PopulatedField) []catalog.PopulatedField {
	if len(in) == 0 {
		return []catalog.PopulatedField{}
	}
	out := make([]catalog.PopulatedField, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].DistinctValues != nil {
			out[i].DistinctValues = append([]string(nil), in[i].DistinctValues...)
		}
		if in[i].PivotColumns != nil {
			out[i].PivotColumns = append([]string(nil), in[i].PivotColumns...)
		}
	}
	return out
}

func cloneReferences(in []catalog.PopulatedReference) []catalog.PopulatedReference {
	if len(in) == 0 {
		return []catalog.PopulatedReference{}
	}
	out := make([]catalog.PopulatedReference, len(in))
	copy(out, in)
	return out
}
