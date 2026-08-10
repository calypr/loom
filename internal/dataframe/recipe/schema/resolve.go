// Package schema resolves catalog-backed recipe declarations into a
// concrete, typed recipe before semantic compilation. It deliberately knows
// nothing about AQL, Arango collections, or output-specific behavior.
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

type Scope struct {
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     string
}

type FieldCandidate struct {
	ResourceType      string
	Path              string
	Kind              string
	DistinctValues    []string
	DistinctTruncated bool
	// ExtensionValues is optional profile-level correlation metadata. When
	// present it preserves the URL/value[x] pairing that a flattened field
	// catalog cannot represent with independent distinct-value lists.
	ExtensionValues       []ExtensionValueObservation
	PivotCandidate        bool
	PivotFamily           string
	PivotColumns          []string
	PivotColumnSelect     string
	PivotValueSelect      string
	PivotItemSource       string
	PivotItemResourceType string
	PivotValueSelectors   []string
}

type ExtensionValueObservation struct {
	URL        string
	SourcePath string
	ValuePath  string
	ValueType  string
	URLPath    []string
}

// Discovery is the only backend-facing seam needed by recipe resolution. The
// implementation in server adapts Loom's existing field catalog reader.
type Discovery interface {
	Fields(context.Context, Scope, string) ([]FieldCandidate, error)
}

type Resolved struct {
	Bundle               recipe.Bundle
	Scope                Scope
	StoredRecipeDigest   string
	ResolvedSchemaDigest string
	ScopeDigest          string
	SourceSnapshot       SourceSnapshot
}

// SourceSnapshot records the exact logical dataset namespace used during
// discovery. An empty generation is the legacy-null namespace, not an
// unscoped request.
type SourceSnapshot struct {
	Project           string
	DatasetGeneration string
	LegacyNull        bool
	AuthScopeMode     string
	AuthResourcePaths []string
}

// memoizedDiscovery keeps one immutable catalog snapshot per resource type for
// the lifetime of a resolution. A recipe can have several projection sets,
// pivots, and keyed maps on the same node; issuing the catalog query once per
// declaration needlessly adds latency and can produce an internally
// inconsistent schema if the catalog changes between reads.
type memoizedDiscovery struct {
	delegate Discovery
	byType   map[string][]FieldCandidate
}

func (d *memoizedDiscovery) Fields(ctx context.Context, scope Scope, resourceType string) ([]FieldCandidate, error) {
	if fields, ok := d.byType[resourceType]; ok {
		return cloneCandidates(fields), nil
	}
	fields, err := d.delegate.Fields(ctx, scope, resourceType)
	if err != nil {
		return nil, err
	}
	d.byType[resourceType] = cloneCandidates(fields)
	return cloneCandidates(fields), nil
}

func Resolve(ctx context.Context, bundle recipe.Bundle, scope Scope, discovery Discovery) (Resolved, error) {
	if err := bundle.Validate(); err != nil {
		return Resolved{}, err
	}
	storedDigest, err := bundle.Digest()
	if err != nil {
		return Resolved{}, fmt.Errorf("digest stored recipe: %w", err)
	}
	base := Resolved{Scope: scope, StoredRecipeDigest: storedDigest, SourceSnapshot: sourceSnapshot(scope)}
	if discovery == nil {
		if hasCatalogDeclarations(bundle) {
			return Resolved{}, fmt.Errorf("catalog-backed recipe requires schema discovery")
		}
		base.Bundle = bundle
		base.ScopeDigest, base.ResolvedSchemaDigest, err = resolutionDigests(storedDigest, bundle, scope)
		if err != nil {
			return Resolved{}, err
		}
		return base, nil
	}
	resolved, err := cloneBundle(bundle)
	if err != nil {
		return Resolved{}, err
	}
	memo := &memoizedDiscovery{delegate: discovery, byType: make(map[string][]FieldCandidate)}
	for outputIndex := range resolved.Outputs {
		if err := resolveOutput(ctx, &resolved.Outputs[outputIndex], scope, memo); err != nil {
			return Resolved{}, fmt.Errorf("output %q: %w", resolved.Outputs[outputIndex].Name, err)
		}
	}
	if err := resolved.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("resolved recipe: %w", err)
	}
	base.Bundle = resolved
	base.ScopeDigest, base.ResolvedSchemaDigest, err = resolutionDigests(storedDigest, resolved, scope)
	if err != nil {
		return Resolved{}, err
	}
	return base, nil
}

func sourceSnapshot(scope Scope) SourceSnapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	return SourceSnapshot{Project: scope.Project, DatasetGeneration: scope.DatasetGeneration, LegacyNull: strings.TrimSpace(scope.DatasetGeneration) == "", AuthScopeMode: scope.AuthScopeMode, AuthResourcePaths: paths}
}

func resolutionDigests(storedDigest string, bundle recipe.Bundle, scope Scope) (string, string, error) {
	snapshot := sourceSnapshot(scope)
	scopeBytes, err := json.Marshal(snapshot)
	if err != nil {
		return "", "", err
	}
	scopeSum := sha256.Sum256(scopeBytes)
	scopeDigest := hex.EncodeToString(scopeSum[:])
	bundleDigest, err := bundle.Digest()
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(struct {
		Stored, Resolved, Scope string
	}{storedDigest, bundleDigest, scopeDigest})
	if err != nil {
		return "", "", err
	}
	resolvedSum := sha256.Sum256(payload)
	return scopeDigest, hex.EncodeToString(resolvedSum[:]), nil
}

func cloneCandidates(in []FieldCandidate) []FieldCandidate {
	if len(in) == 0 {
		return []FieldCandidate{}
	}
	out := make([]FieldCandidate, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].DistinctValues = append([]string(nil), in[i].DistinctValues...)
		out[i].ExtensionValues = append([]ExtensionValueObservation(nil), in[i].ExtensionValues...)
		for j := range out[i].ExtensionValues {
			out[i].ExtensionValues[j].URLPath = append([]string(nil), in[i].ExtensionValues[j].URLPath...)
		}
		out[i].PivotColumns = append([]string(nil), in[i].PivotColumns...)
	}
	return out
}

func resolveOutput(ctx context.Context, output *recipe.Output, scope Scope, discovery Discovery) error {
	fields, err := resolveProjectionSets(ctx, scope, discovery, output.RootResourceType, "root", output.CatalogProjections)
	if err != nil {
		return fmt.Errorf("catalog projections: %w", err)
	}
	output.Fields = appendUniqueFields(output.Fields, fields)
	output.Pivots, err = resolvePivots(ctx, scope, discovery, output.RootResourceType, "root", output.Pivots)
	if err != nil {
		return fmt.Errorf("pivots: %w", err)
	}
	if err := resolveDynamicColumns(ctx, scope, discovery, output.RootResourceType, "root", output.DynamicColumns); err != nil {
		return fmt.Errorf("dynamic columns: %w", err)
	}
	if err := resolveExtensionColumns(ctx, scope, discovery, output.RootResourceType, "root", output.ExtensionColumns); err != nil {
		return fmt.Errorf("extension columns: %w", err)
	}
	for index := range output.Traversals {
		if err := resolveTraversal(ctx, &output.Traversals[index], scope, discovery); err != nil {
			return err
		}
	}
	return nil
}

func resolveTraversal(ctx context.Context, traversal *recipe.Traversal, scope Scope, discovery Discovery) error {
	alias := traversal.Alias
	if alias == "" {
		alias = traversal.Name
	}
	fields, err := resolveProjectionSets(ctx, scope, discovery, traversal.ToResourceType, alias, traversal.CatalogProjections)
	if err != nil {
		return fmt.Errorf("traversal %q catalog projections: %w", traversal.Name, err)
	}
	traversal.Fields = appendUniqueFields(traversal.Fields, fields)
	traversal.Pivots, err = resolvePivots(ctx, scope, discovery, traversal.ToResourceType, alias, traversal.Pivots)
	if err != nil {
		return fmt.Errorf("traversal %q pivots: %w", traversal.Name, err)
	}
	if err := resolveDynamicColumns(ctx, scope, discovery, traversal.ToResourceType, alias, traversal.DynamicColumns); err != nil {
		return fmt.Errorf("traversal %q dynamic columns: %w", traversal.Name, err)
	}
	if err := resolveExtensionColumns(ctx, scope, discovery, traversal.ToResourceType, alias, traversal.ExtensionColumns); err != nil {
		return fmt.Errorf("traversal %q extension columns: %w", traversal.Name, err)
	}
	for index := range traversal.Traversals {
		if err := resolveTraversal(ctx, &traversal.Traversals[index], scope, discovery); err != nil {
			return err
		}
	}
	return nil
}

func appendUniqueFields(existing, discovered []recipe.Field) []recipe.Field {
	seen := make(map[string]struct{}, len(existing)+len(discovered))
	for _, field := range existing {
		seen[field.Name] = struct{}{}
	}
	result := append([]recipe.Field(nil), existing...)
	for _, field := range discovered {
		if _, ok := seen[field.Name]; ok {
			continue
		}
		seen[field.Name] = struct{}{}
		result = append(result, field)
	}
	return result
}
