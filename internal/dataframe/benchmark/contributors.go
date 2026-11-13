package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// Aggregate predicates on the writable wire.
//
// The battery authors v3 templates with aggregate.where (the offline
// decoder accepts them), but the writable wire omits AggregateSource.Where
// (json:"-"): the server's strict body bind refuses the field with
// unknown field "where". The writable contract is the catalog-bound
// Column.Contributor form, and the repository's own
// MigrateLegacyContributors turns the v3 intent into it against the live
// builder catalog — the same path the acceptance lane runs
// (internal/acceptance/scenario.go). Live legs therefore publish migrated
// v4 canonical bytes, never the legacy where wire.

// contributorCatalogWire is the public builder-state catalog projection the
// contributor migration consumes. CatalogSnapshot cannot be unmarshaled
// directly because ConstructionChoice is a closed interface source; this
// reduced view carries exactly the fields MigrateLegacyContributors reads.
type contributorCatalogWire struct {
	Generation               string `json:"generation"`
	SnapshotToken            string `json:"snapshotToken"`
	AuthorizationScopeDigest string `json:"authorizationScopeDigest"`
	Complete                 bool   `json:"complete"`
	Truncated                bool   `json:"truncated"`
	Nodes                    []struct {
		ID           string `json:"nodeId"`
		ResourceType string `json:"resourceType"`
	} `json:"nodes"`
	Candidates []struct {
		ID                 string                         `json:"candidateId"`
		NodeID             string                         `json:"nodeId"`
		FieldPath          string                         `json:"fieldPath"`
		LogicalType        string                         `json:"logicalType"`
		Repeated           bool                           `json:"repeated"`
		RepeatedBoundaries []authoringv2.RepeatedBoundary `json:"repeatedBoundaries"`
	} `json:"candidates"`
}

// FetchContributorCatalog reads the live builder catalog of one explorer
// (the battery uses the seeded "default"). It fails closed on an incomplete,
// truncated, or scope-less catalog: a partial catalog could silently drop a
// predicate candidate and mis-author a workload.
func (a *Arm) FetchContributorCatalog(ctx context.Context, token, project, explorer string) (authoringv2.CatalogSnapshot, error) {
	endpoint := a.authoringURL(project, explorer) + "/builder"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return authoringv2.CatalogSnapshot{}, err
	}
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return authoringv2.CatalogSnapshot{}, fmt.Errorf("benchmark: contributor catalog on %s: %w", a.Backend, err)
	}
	catalogValue, ok := value["catalog"]
	if !ok {
		return authoringv2.CatalogSnapshot{}, fmt.Errorf("benchmark: builder state on %s omitted the catalog", a.Backend)
	}
	raw, err := json.Marshal(catalogValue)
	if err != nil {
		return authoringv2.CatalogSnapshot{}, fmt.Errorf("benchmark: builder catalog on %s: %w", a.Backend, err)
	}
	var wire contributorCatalogWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return authoringv2.CatalogSnapshot{}, fmt.Errorf("benchmark: builder catalog shape on %s: %w", a.Backend, err)
	}
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: project, ExplorerID: explorer,
		SourceGeneration: wire.Generation, AuthorizationScopeDigest: wire.AuthorizationScopeDigest,
		SnapshotToken: wire.SnapshotToken,
		Complete:      wire.Complete, Truncated: wire.Truncated,
		Nodes:      make([]authoringv2.CatalogNode, 0, len(wire.Nodes)),
		Candidates: make([]authoringv2.CatalogCandidate, 0, len(wire.Candidates)),
	}
	for _, node := range wire.Nodes {
		catalog.Nodes = append(catalog.Nodes, authoringv2.CatalogNode{ID: node.ID, ResourceType: node.ResourceType})
	}
	for _, candidate := range wire.Candidates {
		catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
			LogicalType: candidate.LogicalType, Repeated: candidate.Repeated,
			RepeatedBoundaries: append([]authoringv2.RepeatedBoundary(nil), candidate.RepeatedBoundaries...),
		})
	}
	if err := validateContributorCatalog(catalog); err != nil {
		return authoringv2.CatalogSnapshot{}, fmt.Errorf("benchmark: contributor catalog on %s: %w", a.Backend, err)
	}
	return catalog, nil
}

func validateContributorCatalog(catalog authoringv2.CatalogSnapshot) error {
	if strings.TrimSpace(catalog.Project) == "" || strings.TrimSpace(catalog.ExplorerID) == "" || strings.TrimSpace(catalog.SourceGeneration) == "" {
		return errors.New("catalog project, Explorer, or generation is missing")
	}
	if strings.TrimSpace(catalog.SnapshotToken) == "" || strings.TrimSpace(catalog.AuthorizationScopeDigest) == "" {
		return errors.New("catalog snapshot token or authorization scope digest is missing")
	}
	if !catalog.Complete || catalog.Truncated {
		return errors.New("catalog snapshot is incomplete or truncated")
	}
	return nil
}

// PreparePublicationWorkspace turns one authored battery workspace into the
// bytes to publish. Workspaces without aggregate predicates pass through
// byte-identical (the arms already share them); workspaces with legacy
// aggregate.where are migrated against the arm's live catalog and published
// as canonical v4 contributor intent. A predicate path the catalog cannot
// resolve unambiguously fails the run before any timing: guessing a
// candidate id or dropping the filter would silently change the workload.
func PreparePublicationWorkspace(raw []byte, generation string, catalog authoringv2.CatalogSnapshot) ([]byte, error) {
	workspace, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		return nil, fmt.Errorf("benchmark: decode authored workspace: %w", err)
	}
	if !workspaceHasAggregateWhere(workspace) {
		return raw, nil
	}
	if workspace.SemanticsVersion != wsSemanticsVersion {
		return nil, fmt.Errorf("benchmark: authored workspace carries aggregate predicates at semanticsVersion %d; only v%d migration is supported",
			workspace.SemanticsVersion, wsSemanticsVersion)
	}
	if err := validateContributorCatalog(catalog); err != nil {
		return nil, fmt.Errorf("benchmark: contributor catalog unusable for migration: %w", err)
	}
	if catalog.SourceGeneration != generation {
		return nil, fmt.Errorf("benchmark: contributor catalog generation %q does not match benchmark generation %q", catalog.SourceGeneration, generation)
	}
	migrated, err := authoringv2.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		return nil, fmt.Errorf("benchmark: migrate aggregate predicates: %w", err)
	}
	migrated = authoringv2.MigrateLosslessDefaults(migrated, catalog).NormalizePresentationOrders()
	if err := migrated.ValidateForPublication(); err != nil {
		return nil, fmt.Errorf("benchmark: validate migrated workspace: %w", err)
	}
	canonical, err := migrated.CanonicalJSON()
	if err != nil {
		return nil, fmt.Errorf("benchmark: canonicalize migrated workspace: %w", err)
	}
	return canonical, nil
}

func workspaceHasAggregateWhere(workspace authoringv2.Workspace) bool {
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind == authoringv2.SourceAggregate && column.Source.Aggregate != nil && column.Source.Aggregate.Where != nil {
				return true
			}
		}
	}
	return false
}
