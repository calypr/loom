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
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

// explorerCatalogReader assembles the complete catalog before exposing any
// opaque identifier. A failed dependency returns an explicitly incomplete
// snapshot; callers must never confuse an empty response with a usable one.
func explorerCatalogReader(discoverFields func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error), discoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error), scopes *authscope.ScopeResolver, manifests dataset.ActiveResolver) func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
	return func(ctx context.Context, project, _ string, requestedGeneration string) (explorer.CatalogSnapshot, error) {
		project = projectid.Canonical(project)
		storageProject := projectid.Legacy(project)
		principal, _ := authscope.PrincipalFromContext(ctx)
		if err := authscope.AuthorizeProject(principal, project, scopes != nil); err != nil {
			return explorer.CatalogSnapshot{}, err
		}
		generation := requestedGeneration
		schemaDigest := ""
		if generation == "" && manifests != nil {
			manifest, err := dataset.ResolveActive(ctx, manifests, storageProject)
			if err != nil {
				return incompleteExplorerCatalog(project, generation, "", err)
			}
			generation, schemaDigest = manifest.Dataset.Generation, manifest.SchemaIdentity.SchemaSHA256
		}
		scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
		if scopes != nil {
			resolved, err := scopes.ResolveReadScopeForGeneration(ctx, principal, storageProject, generation, nil)
			if err != nil {
				return incompleteExplorerCatalog(project, generation, schemaDigest, err)
			}
			scope = resolved
		}
		unrestricted := scope.Unrestricted()
		options := catalog.PopulatedFieldOptions{Project: storageProject, DatasetGeneration: generation, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthResourcePathsUnrestricted: &unrestricted}
		fields, err := discoverFields(ctx, options)
		if err != nil {
			return incompleteExplorerCatalog(project, generation, schemaDigest, err)
		}
		references, err := discoverReferences(ctx, catalog.PopulatedReferenceOptions{Project: storageProject, DatasetGeneration: generation, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthResourcePathsUnrestricted: &unrestricted, Mode: catalog.TraversalModeBuilder})
		if err != nil {
			return incompleteExplorerCatalog(project, generation, schemaDigest, err)
		}
		assembled := explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}
		nodes := map[string]explorer.CatalogNode{}
		for _, field := range fields {
			resourceType := canonicalExplorerCatalogResourceType(field.ResourceType)
			if resourceType == "" || isLoomMetadataField(field.Path) {
				continue
			}
			nodeID := explorer.OpaqueID("n_", resourceType)
			nodes[nodeID] = explorer.CatalogNode{ID: nodeID, ResourceType: resourceType}
			selectionID := explorer.OpaqueID("s_", resourceType+"\x00"+field.Path)
			assembled.Selections[selectionID] = explorer.CatalogSelection{ID: selectionID, NodeID: nodeID, FieldRef: explorerFieldRef(resourceType, field.Path), Select: field.Path, LogicalType: field.Kind, Filterable: field.Kind != "object" && field.Kind != "array", Chartable: field.Kind != "object" && field.Kind != "array"}
		}
		for _, reference := range references {
			fromType := canonicalExplorerCatalogResourceType(reference.FromType)
			toType := canonicalExplorerCatalogResourceType(reference.ToType)
			if fromType == "" || toType == "" {
				continue
			}
			from, to := explorer.OpaqueID("n_", fromType), explorer.OpaqueID("n_", toType)
			nodes[from] = explorer.CatalogNode{ID: from, ResourceType: fromType}
			nodes[to] = explorer.CatalogNode{ID: to, ResourceType: toType}
			assembled.Edges = append(assembled.Edges, explorer.CatalogEdge{ID: explorer.OpaqueID("e_", fromType+"\x00"+reference.Label+"\x00"+toType), FromNodeID: from, ToNodeID: to, Label: reference.Label})
		}
		for _, node := range nodes {
			assembled.Nodes = append(assembled.Nodes, node)
		}
		sort.Slice(assembled.Nodes, func(i, j int) bool { return assembled.Nodes[i].ID < assembled.Nodes[j].ID })
		sort.Slice(assembled.Edges, func(i, j int) bool { return assembled.Edges[i].ID < assembled.Edges[j].ID })
		snapshot, err := explorer.NewCatalogSnapshotWithSchema(project, generation, explorerScopeDigest(scope), schemaDigest, assembled, true, false, nil)
		if err != nil {
			return explorer.CatalogSnapshot{}, err
		}
		return snapshot, nil
	}
}

func canonicalExplorerCatalogResourceType(resourceType string) string {
	if canonical, ok := fhirschema.ConcreteResourceType(resourceType); ok {
		return canonical
	}
	return ""
}

func incompleteExplorerCatalog(project, generation, schemaDigest string, cause error) (explorer.CatalogSnapshot, error) {
	snapshot, err := explorer.NewCatalogSnapshotWithSchema(project, generation, "unavailable", schemaDigest, explorer.Catalog{Selections: map[string]explorer.CatalogSelection{}}, false, false, []explorer.Diagnostic{{Severity: "ERROR", Code: "CATALOG_UNAVAILABLE", Message: cause.Error(), Retryable: true}})
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
