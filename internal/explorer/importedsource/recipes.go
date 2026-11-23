package importedsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"

	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"

	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
)

type Sources struct {
	registry       recipe.Reader
	engine         *dataframeexecution.Engine
	capabilities   *capabilityresolver.Resolver
	defaultName    string
	defaultVersion string
}

func New(registry recipe.Reader, engine *dataframeexecution.Engine, capabilities *capabilityresolver.Resolver, defaultName, defaultVersion string) Sources {
	return Sources{registry: registry, engine: engine, capabilities: capabilities, defaultName: defaultName, defaultVersion: defaultVersion}
}

type importedRecipeResolution struct {
	bundle        recipe.Bundle
	resolved      dataframeexecution.Resolved
	digest        string
	pinnedSchemas map[string][]facade.CompiledOutputColumn
}

func (r Sources) load(ctx context.Context, project, name, version, digest string, authorized ports.AuthorizedCapability, documents ...ws.Document) (importedRecipeResolution, error) {
	if r.registry == nil || r.engine == nil || name == "" || name != r.defaultName {
		return importedRecipeResolution{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported source recipes are not configured"}
	}
	if projectid.Canonical(project) != projectid.Canonical(authorized.Snapshot.Identity.Project) {
		return importedRecipeResolution{}, capability.ErrStaleSnapshot
	}
	if err := capabilityresolver.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return importedRecipeResolution{}, err
	}
	entry, err := r.registry.LoadRecipeVersion(ctx, name, version)
	if err != nil {
		return importedRecipeResolution{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the pinned dataframe recipe could not be loaded", Cause: err}
	}
	bundle, err := entry.Bundle.CanonicalBundle()
	if err != nil {
		return importedRecipeResolution{}, err
	}
	actual, err := bundle.Digest()
	if err != nil {
		return importedRecipeResolution{}, err
	}
	if entry.Digest != actual || (digest != "" && digest != actual) {
		return importedRecipeResolution{}, fmt.Errorf("the imported dataframe recipe changed; reload its exact source version")
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(project), SelectionProject: projectid.Canonical(project), DatasetGeneration: authorized.Snapshot.Identity.Generation, AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...), AuthScopeMode: authorized.Scope.Mode}
	var pinnedSchemas map[string][]facade.CompiledOutputColumn
	pinnedCompiled := map[string]facade.CompiledRecipeOutput{}
	pinnedNative := map[string]recipeplan.OutputPlan{}
	for _, document := range documents {
		if document.ImportedRecipe == nil {
			continue
		}
		needsProof := false
		for _, original := range bundle.Outputs {
			if original.Name == document.ImportedRecipe.Output {
				needsProof = requiresPristineProjectionIdentity(original, document.ImportedSource)
			}
		}
		if !needsProof {
			continue
		}
		pristine, err := r.engine.ResolveBundle(ctx, bundle, bindings)
		if err != nil {
			return importedRecipeResolution{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the pinned dataframe projection could not be compiled", Cause: err}
		}
		pinnedSchemas = map[string][]facade.CompiledOutputColumn{}
		for _, output := range pristine.Compiled.Outputs {
			output.OutputSchema = bindImportedSourceIDs(output.Name, output.OutputSchema)
			pinnedSchemas[output.Name] = output.OutputSchema
			pinnedCompiled[output.Name] = output
		}
		for _, output := range pristine.Semantic.SemanticPlan.Outputs {
			pinnedNative[output.Name] = output
		}
		break
	}
	effective := bundle
	if len(documents) > 0 {
		effective.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
		seen := make(map[string]bool, len(documents))
		for _, document := range documents {
			if document.ImportedRecipe == nil || document.ImportedRecipe.Name != name || document.ImportedRecipe.TranslationVersion != version || document.ImportedRecipe.Digest != actual {
				return importedRecipeResolution{}, fmt.Errorf("source document requires a matching exact recipe pin")
			}
			if seen[document.ImportedRecipe.Output] {
				return importedRecipeResolution{}, fmt.Errorf("duplicate imported source output")
			}
			seen[document.ImportedRecipe.Output] = true
			found := false
			for i := range effective.Outputs {
				if effective.Outputs[i].Name != document.ImportedRecipe.Output {
					continue
				}
				if document.ImportedSource != nil && len(document.ImportedSource.FieldBindings) > 0 {
					expected, err := pinnedFieldBindingIDs(effective.Outputs[i], pinnedSchemas[effective.Outputs[i].Name])
					if err != nil {
						return importedRecipeResolution{}, err
					}
					if err := recipeimport.VerifyPinnedFieldBindings(document.ImportedSource.FieldBindings, expected); err != nil {
						return importedRecipeResolution{}, err
					}
				}
				if err := recipeimport.ValidateRelationshipOverlay(effective.Outputs[i], document.ImportedSource, ws.CatalogFromCapability(authorized.Snapshot, "")); err != nil {
					return importedRecipeResolution{}, err
				}
				output, applyErr := recipeimport.ApplySourcePlan(effective.Outputs[i], document.ImportedSource)
				if applyErr != nil {
					return importedRecipeResolution{}, applyErr
				}
				effective.Outputs[i] = output
				found = true
			}
			if !found {
				return importedRecipeResolution{}, fmt.Errorf("pinned source output was not found")
			}
		}
	}
	resolved, err := r.engine.ResolveBundle(ctx, effective, bindings)
	if err != nil {
		return importedRecipeResolution{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the imported dataframe source could not be compiled", Cause: err}
	}
	resolved.Compiled.Outputs = append([]facade.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
	for i := range resolved.Compiled.Outputs {
		output := &resolved.Compiled.Outputs[i]
		output.OutputSchema = bindImportedSourceIDs(output.Name, output.OutputSchema)
		for _, document := range documents {
			if document.ImportedRecipe == nil || document.ImportedRecipe.Output != output.Name {
				continue
			}
			for _, original := range bundle.Outputs {
				if original.Name != output.Name || !requiresPristineProjectionIdentity(original, document.ImportedSource) {
					continue
				}
				plan, err := recipeimport.HydrateSourcePlan(original, document.ImportedSource)
				if err != nil {
					return importedRecipeResolution{}, err
				}
				var native *recipeplan.OutputPlan
				for j := range resolved.Semantic.SemanticPlan.Outputs {
					if resolved.Semantic.SemanticPlan.Outputs[j].Name == output.Name {
						native = &resolved.Semantic.SemanticPlan.Outputs[j]
					}
				}
				baseline, ok := pinnedCompiled[output.Name]
				if !ok || native == nil {
					return importedRecipeResolution{}, fmt.Errorf("source projection identity proof is unavailable")
				}
				if err := preserveImportedGeneratedIDs(document, original, &plan, baseline, pinnedNative[output.Name], output, *native, ws.CatalogFromCapability(authorized.Snapshot, "")); err != nil {
					return importedRecipeResolution{}, err
				}
			}
		}
	}
	return importedRecipeResolution{bundle: bundle, resolved: resolved, digest: actual, pinnedSchemas: pinnedSchemas}, nil
}

func (r Sources) DefaultWorkspace(ctx context.Context, project string, snapshot capability.Snapshot, catalog ws.CatalogSnapshot) (ws.Workspace, error) {
	if r.capabilities == nil {
		return ws.Workspace{}, fmt.Errorf("dataframe capability resolver is unavailable")
	}
	authorized, err := r.capabilities.ResolveForCompilation(ctx, project, snapshot.Token)
	if err != nil {
		return ws.Workspace{}, err
	}
	source, err := r.load(ctx, project, r.defaultName, r.defaultVersion, "", authorized)
	if err != nil {
		return ws.Workspace{}, err
	}
	schemas := make(map[string][]recipe.StageColumn, len(source.resolved.Compiled.Outputs))
	for _, output := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(output.OutputSchema)
		if err != nil {
			return ws.Workspace{}, err
		}
		for _, column := range columns {
			schemas[output.Name] = append(schemas[output.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(source.bundle, catalog, schemas)
	if err != nil {
		return ws.Workspace{}, fmt.Errorf("import configured dataframe into the graphical builder: %w", err)
	}
	workspace.Explorer.Title = "Default dataframe"
	workspace.Explorer.Description = "Based on the shared default recipe. Edits are saved for this project."
	return workspace, nil
}

func (r Sources) Resolve(ctx context.Context, project string, workspace ws.Workspace, authorized ports.AuthorizedCapability) ([]explorercompilation.ResolvedImportedRecipe, error) {
	var result []explorercompilation.ResolvedImportedRecipe
	cache := map[string]importedRecipeResolution{}
	for _, document := range workspace.Documents {
		if document.ImportedRecipe == nil {
			continue
		}
		ref := *document.ImportedRecipe
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		key := ref.Name + "\x00" + ref.TranslationVersion + "\x00" + ref.Digest
		source, ok := cache[key]
		if !ok {
			var err error
			var documents []ws.Document
			for _, candidate := range workspace.Documents {
				pin := candidate.ImportedRecipe
				if pin != nil && pin.Name == ref.Name && pin.TranslationVersion == ref.TranslationVersion && pin.Digest == ref.Digest {
					documents = append(documents, candidate)
				}
			}
			source, err = r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, documents...)
			if err != nil {
				return nil, err
			}
			cache[key] = source
		}
		found := false
		for _, output := range source.resolved.Compiled.Outputs {
			if output.Name != ref.Output {
				continue
			}
			columns, err := importedSourceSchema(output.OutputSchema)
			if err != nil {
				return nil, err
			}
			result = append(result, explorercompilation.ResolvedImportedRecipe{Reference: ref, Bundle: source.bundle, ResolvedBundle: source.resolved.Bundle, SourceColumns: columns})
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("the pinned dataframe recipe has no output %q", ref.Output)
		}
	}
	return result, nil
}

func importedSourceSchema(columns []facade.CompiledOutputColumn) ([]explorercompilation.ResolvedImportedSourceColumn, error) {
	result := make([]explorercompilation.ResolvedImportedSourceColumn, 0, len(columns))
	for _, column := range columns {
		if column.Internal || column.Identity {
			continue
		}
		cardinality := expression.Cardinality(column.Cardinality)
		if !cardinality.Valid() {
			return nil, fmt.Errorf("imported source column %q has unsupported cardinality %q", column.Name, column.Cardinality)
		}
		if strings.TrimSpace(column.ID) == "" {
			return nil, fmt.Errorf("imported source column %q has no stable compiler identity", column.Name)
		}
		label := column.Label
		if label == "" {
			label = column.Name
		}
		shape := "scalar"
		if cardinality.AllowsMany() {
			shape = "array"
		}
		result = append(result, explorercompilation.ResolvedImportedSourceColumn{Column: recipe.StageColumn{ID: column.ID, Name: column.Name, Label: label, Type: column.Kind, Nullable: column.Nullable, SourceChild: column.SourceChild}, Cardinality: column.Cardinality, Shape: shape, SemanticPath: column.SemanticPath})
	}
	return result, nil
}

func (r Sources) SourceStage(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
	if request.Document.ImportedRecipe == nil {
		return receiptmodel.ReceiptConstructionStage{}, fmt.Errorf("an imported recipe reference is required")
	}
	ref := request.Document.ImportedRecipe
	if err := ref.Validate(); err != nil {
		return receiptmodel.ReceiptConstructionStage{}, err
	}
	source, err := r.load(ctx, request.Project, ref.Name, ref.TranslationVersion, ref.Digest, request.Authorized, request.Document)
	if err != nil {
		return receiptmodel.ReceiptConstructionStage{}, err
	}
	for _, output := range source.resolved.Compiled.Outputs {
		if output.Name != ref.Output {
			continue
		}
		descriptor, err := facade.DescribeConstructionSourceStage(output.OutputSchema, request.Document.RootResourceType, &output.Plan)
		if err != nil {
			return receiptmodel.ReceiptConstructionStage{}, err
		}
		return explorercompilation.ReceiptConstructionStageFromDescriptor(descriptor)
	}
	return receiptmodel.ReceiptConstructionStage{}, fmt.Errorf("the pinned source has no dataframe %q", ref.Output)
}

func bindImportedSourceIDs(output string, schema []facade.CompiledOutputColumn) []facade.CompiledOutputColumn {
	bound := append([]facade.CompiledOutputColumn(nil), schema...)
	for i := range bound {
		column := &bound[i]
		if column.ID != "" || column.Internal || column.Identity {
			continue
		}
		sum := sha256.Sum256([]byte(output + "\x00" + column.Name + "\x00" + column.SemanticPath))
		column.ID = "import_" + hex.EncodeToString(sum[:12])
	}
	return bound
}
