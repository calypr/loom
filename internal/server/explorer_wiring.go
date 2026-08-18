package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

// explorerCatalogReader assembles the complete catalog before exposing any
// opaque identifier. A failed dependency returns an explicitly incomplete
// snapshot; callers must never confuse an empty response with a usable one.
func explorerCatalogReader(discoverFields func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error), discoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error), scopes *authscope.ScopeResolver, manifests dataset.ActiveResolver) func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
	return func(ctx context.Context, project, _ string, requestedGeneration string) (explorer.CatalogSnapshot, error) {
		principal, _ := authscope.PrincipalFromContext(ctx)
		if err := authscope.AuthorizeProject(principal, project, scopes != nil); err != nil {
			return explorer.CatalogSnapshot{}, err
		}
		generation := requestedGeneration
		schemaDigest := ""
		if generation == "" && manifests != nil {
			manifest, err := dataset.ResolveActive(ctx, manifests, project)
			if err != nil {
				return incompleteExplorerCatalog(project, generation, "", err)
			}
			generation, schemaDigest = manifest.Dataset.Generation, manifest.SchemaIdentity.SchemaSHA256
		}
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		if scopes != nil {
			resolved, err := scopes.ResolveReadScopeForGeneration(ctx, principal, project, generation, nil)
			if err != nil {
				return incompleteExplorerCatalog(project, generation, schemaDigest, err)
			}
			scope = resolved
		}
		unrestricted := scope.Unrestricted()
		options := catalog.PopulatedFieldOptions{Project: project, DatasetGeneration: generation, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthResourcePathsUnrestricted: &unrestricted}
		fields, err := discoverFields(ctx, options)
		if err != nil {
			return incompleteExplorerCatalog(project, generation, schemaDigest, err)
		}
		references, err := discoverReferences(ctx, catalog.PopulatedReferenceOptions{Project: project, DatasetGeneration: generation, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthResourcePathsUnrestricted: &unrestricted, Mode: catalog.TraversalModeBuilder})
		if err != nil {
			return incompleteExplorerCatalog(project, generation, schemaDigest, err)
		}
		assembled := explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}
		nodes := map[string]explorer.CatalogNode{}
		for _, field := range fields {
			if field.ResourceType == "" || isLoomMetadataField(field.Path) {
				continue
			}
			nodeID := explorer.OpaqueID("n_", field.ResourceType)
			nodes[nodeID] = explorer.CatalogNode{ID: nodeID, ResourceType: field.ResourceType}
			selectionID := explorer.OpaqueID("s_", field.ResourceType+"\x00"+field.Path)
			assembled.Selections[selectionID] = explorer.CatalogSelection{ID: selectionID, NodeID: nodeID, FieldRef: explorerFieldRef(field.ResourceType, field.Path), Select: field.Path, LogicalType: field.Kind, Filterable: field.Kind != "object" && field.Kind != "array", Chartable: field.Kind != "object" && field.Kind != "array"}
		}
		for _, reference := range references {
			if reference.FromType == "" || reference.ToType == "" {
				continue
			}
			from, to := explorer.OpaqueID("n_", reference.FromType), explorer.OpaqueID("n_", reference.ToType)
			nodes[from] = explorer.CatalogNode{ID: from, ResourceType: reference.FromType}
			nodes[to] = explorer.CatalogNode{ID: to, ResourceType: reference.ToType}
			assembled.Edges = append(assembled.Edges, explorer.CatalogEdge{ID: explorer.OpaqueID("e_", reference.FromType+"\x00"+reference.Label+"\x00"+reference.ToType), FromNodeID: from, ToNodeID: to, Label: reference.Label})
		}
		for _, node := range nodes {
			assembled.Nodes = append(assembled.Nodes, node)
		}
		sort.Slice(assembled.Nodes, func(i, j int) bool { return assembled.Nodes[i].ID < assembled.Nodes[j].ID })
		sort.Slice(assembled.Edges, func(i, j int) bool { return assembled.Edges[i].ID < assembled.Edges[j].ID })
		snapshot, err := explorer.NewCatalogSnapshot(project, generation, explorerScopeDigest(scope), assembled, true, false, nil)
		if err != nil {
			return explorer.CatalogSnapshot{}, err
		}
		snapshot.ResolvedSchemaDigest = schemaDigest
		return snapshot, nil
	}
}

func incompleteExplorerCatalog(project, generation, schemaDigest string, cause error) (explorer.CatalogSnapshot, error) {
	snapshot, err := explorer.NewCatalogSnapshot(project, generation, "unavailable", explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}, false, false, []explorer.Diagnostic{{Severity: "ERROR", Code: "CATALOG_UNAVAILABLE", Message: cause.Error(), Retryable: true}})
	snapshot.ResolvedSchemaDigest = schemaDigest
	return snapshot, err
}

func explorerScopeDigest(scope authscope.ReadScope) string {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:])
}

func explorerFieldRef(resourceType, path string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(path) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		} else if builder.Len() > 0 {
			builder.WriteByte('_')
		}
	}
	return fmt.Sprintf("%s.%s", resourceType, strings.Trim(builder.String(), "_"))
}
