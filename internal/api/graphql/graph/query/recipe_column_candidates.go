package queryapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

// RecipeColumnCandidates resolves authoring choices from exactly one node of
// the supplied recipe and one authorized dataset generation.
func (s *Service) RecipeColumnCandidates(ctx context.Context, req RecipeColumnCandidatesRequest) (*RecipeColumnCandidatesResponse, error) {
	project := strings.TrimSpace(req.Project)
	if project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, project, s.scopeResolver != nil); err != nil {
		return nil, classifyError(err)
	}
	generation := strings.TrimSpace(req.DatasetGeneration)
	if generation == "" {
		var err error
		generation, err = s.resolveActiveGeneration(ctx, project)
		if err != nil {
			return nil, queryBackend(err)
		}
	}
	readScope, err := s.resolveReadScopeForGeneration(ctx, principal, project, generation, req.AuthResourcePaths)
	if err != nil {
		return nil, queryBackend(err)
	}
	resourceType, err := candidateNodeResourceType(req.Recipe, req.Output, req.NodePath)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	fields, err := s.discoverFields(ctx, catalog.PopulatedFieldOptions{Project: project, DatasetGeneration: generation, ResourceType: resourceType, AuthResourcePaths: cloneStrings(readScope.AuthResourcePaths), AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(readScope.Unrestricted())})
	if err != nil {
		return nil, queryBackend(err)
	}
	fields = aggregatePopulatedFields(fields)
	candidates, err := schema.ColumnCandidates(req.Recipe, req.Output, req.NodePath, catalogFieldsToRecipeCandidates(fields))
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	offset, err := decodeCandidateCursor(req.After)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	if offset > len(candidates) {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, fmt.Errorf("candidate cursor is beyond the result set"))
	}
	first := req.First
	if first <= 0 {
		first = 100
	}
	if first > 500 {
		first = 500
	}
	end := offset + first
	if end > len(candidates) {
		end = len(candidates)
	}
	resp := &RecipeColumnCandidatesResponse{Project: project, SourceGeneration: generation, Candidates: append([]schema.ColumnCandidate(nil), candidates[offset:end]...), TotalCount: len(candidates), HasNext: end < len(candidates), Complete: true}
	if resp.HasNext {
		resp.EndCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Complete {
			continue
		}
		resp.Complete = false
		message := candidate.Diagnostic
		if message == "" {
			message = "candidate family is incomplete"
		}
		key := candidate.FamilyID + "\x00" + message
		if !seen[key] {
			seen[key] = true
			resp.Diagnostics = append(resp.Diagnostics, fmt.Sprintf("%s at %s: %s", candidate.FamilyID, candidate.NodePath, message))
		}
	}
	return resp, nil
}

func decodeCandidateCursor(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, fmt.Errorf("invalid candidate cursor")
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid candidate cursor")
	}
	return offset, nil
}

func candidateNodeResourceType(bundle recipe.Bundle, outputName string, path []string) (string, error) {
	for _, output := range bundle.Outputs {
		if output.Name != outputName {
			continue
		}
		resource := output.RootResourceType
		traversals := output.Traversals
		for _, wanted := range path {
			found := -1
			for i, t := range traversals {
				alias := t.Alias
				if alias == "" {
					alias = t.Name
				}
				if alias == wanted {
					found = i
					break
				}
			}
			if found < 0 {
				return "", fmt.Errorf("traversal alias %q was not found", wanted)
			}
			resource = traversals[found].ToResourceType
			traversals = traversals[found].Traversals
		}
		return resource, nil
	}
	return "", fmt.Errorf("output %q was not found", outputName)
}
