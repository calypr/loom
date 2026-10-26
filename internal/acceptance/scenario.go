package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	dfpublication "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

type ScenarioConfig struct {
	Connections   Connections
	Namespace     Namespace
	Fixture       ValidatedFixture
	WorkspacePath string
	OraclePath    string
	SourceCommit  string
	ArtifactDir   string
	HTTPClient    *http.Client
}

type ScenarioResult struct {
	Status     string
	BaseStatus string
	Stages     []StageReport
}

type Oracle struct {
	RowCount           int            `json:"row_count"`
	UniquePatientCount int            `json:"unique_patient_count"`
	Columns            []string       `json:"columns"`
	RowDigest          string         `json:"row_digest"`
	NonNullCounts      map[string]int `json:"non_null_counts,omitempty"`
	TrueCounts         map[string]int `json:"true_counts,omitempty"`
}

type publication struct {
	Project            string `json:"project"`
	Generation         string `json:"generation"`
	ExecutionID        string `json:"executionId"`
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

func RunScenario(ctx context.Context, cfg ScenarioConfig) (result ScenarioResult, err error) {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	oracle, err := loadOracle(cfg.OraclePath)
	if err != nil {
		return result, err
	}
	workspace, err := os.ReadFile(cfg.WorkspacePath)
	if err != nil {
		return result, fmt.Errorf("read acceptance workspace: %w", err)
	}
	if !json.Valid(workspace) {
		return result, errors.New("acceptance workspace is not valid JSON")
	}
	stage := func(name string, started time.Time, details map[string]any) {
		result.Stages = append(result.Stages, StageReport{Name: name, Seconds: time.Since(started).Seconds(), Details: details})
	}
	started := time.Now()
	load, err := uploadGeneration(ctx, cfg, cfg.HTTPClient)
	if err != nil {
		return result, err
	}
	if err := writeArtifact(cfg.ArtifactDir, "generation.json", load); err != nil {
		return result, err
	}
	stage("generation_upload", started, load)
	if err := compareSummary(load, cfg.Fixture.Counts); err != nil {
		return result, err
	}
	started = time.Now()
	counts, err := directArangoCounts(ctx, cfg)
	if err != nil {
		return result, err
	}
	stage("arango_counts", started, map[string]any{"counts": counts})
	if err := compareCounts(counts, cfg.Fixture.Counts); err != nil {
		return result, err
	}
	publishBody := workspace
	started = time.Now()
	migratedBody, migration, err := migrateLegacyWorkspaceForCurrentAPI(ctx, cfg, cfg.HTTPClient, workspace)
	if err != nil {
		return result, err
	}
	if migration != nil {
		publishBody = migratedBody
		stage("workspace_contract_migration", started, migration)
	}
	started = time.Now()
	pub, err := publishWorkspace(ctx, cfg, cfg.HTTPClient, publishBody)
	if err != nil {
		return result, err
	}
	stage("explorer_publish", started, map[string]any{"execution_id": pub.ExecutionID})
	if pub.ExecutionID == "" {
		return result, errors.New("Explorer publication returned no execution ID")
	}
	started = time.Now()
	execution, err := getJSON(ctx, cfg.HTTPClient, cfg.Connections.LoomURL+"/api/v1/dataframe/recipe-executions/"+url.PathEscape(pub.ExecutionID))
	if err != nil {
		return result, err
	}
	if err := writeArtifact(cfg.ArtifactDir, "execution.json", execution); err != nil {
		return result, err
	}
	stage("execution_registry", started, map[string]any{"execution_id": pub.ExecutionID, "state": execution["state"]})
	if err := successfulExecution(execution, pub); err != nil {
		return result, err
	}
	started = time.Now()
	physical, err := verifyClickHouse(ctx, cfg, pub, oracle)
	if err != nil {
		return result, err
	}
	stage("clickhouse_physical", started, physical)
	started = time.Now()
	viewer, err := getJSON(ctx, cfg.HTTPClient, cfg.Connections.LoomURL+"/api/v1/projects/"+url.PathEscape(cfg.Namespace.Project)+"/explorers/default")
	if err != nil {
		return result, err
	}
	if err := writeArtifact(cfg.ArtifactDir, "viewer.json", viewer); err != nil {
		return result, err
	}
	stage("explorer_viewer", started, map[string]any{"keys": sortedMapKeys(viewer)})
	if err := verifyViewer(viewer, pub); err != nil {
		return result, err
	}
	started = time.Now()
	graphql, err := verifyGraphQL(ctx, cfg, pub, oracle)
	if err != nil {
		return result, err
	}
	if err := writeArtifact(cfg.ArtifactDir, "graphql.json", graphql); err != nil {
		return result, err
	}
	stage("graphql", started, graphql)
	started = time.Now()
	second, err := publishWorkspace(ctx, cfg, cfg.HTTPClient, publishBody)
	if err != nil {
		return result, fmt.Errorf("idempotent second publication: %w", err)
	}
	stage("idempotent_publish", started, map[string]any{"execution_id": second.ExecutionID})
	if second.ExecutionID != pub.ExecutionID || second.Recipe != pub.Recipe || second.TranslationVersion != pub.TranslationVersion {
		return result, errors.New("idempotent publication changed execution or selector")
	}
	result.Status = "PASSED"
	return result, nil
}

func loadOracle(path string) (Oracle, error) {
	if strings.TrimSpace(path) == "" {
		return Oracle{RowCount: 100, UniquePatientCount: 100}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Oracle{}, fmt.Errorf("read acceptance oracle: %w", err)
	}
	var oracle Oracle
	if err := json.Unmarshal(raw, &oracle); err != nil {
		return Oracle{}, fmt.Errorf("decode acceptance oracle: %w", err)
	}
	if oracle.RowCount <= 0 || oracle.UniquePatientCount <= 0 {
		return Oracle{}, errors.New("acceptance oracle requires positive row counts")
	}
	return oracle, nil
}

func uploadGeneration(ctx context.Context, cfg ScenarioConfig, client *http.Client) (map[string]any, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	files, err := filepath.Glob(filepath.Join(cfg.Fixture.MetaDir, "*.ndjson"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, errors.New("fixture contains no NDJSON files")
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		part, err := writer.CreateFormFile("file", filepath.Base(path))
		if err == nil {
			_, err = io.Copy(part, file)
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("attach fixture %s: %w", filepath.Base(path), err)
		}
	}
	_ = writer.WriteField("project", cfg.Namespace.Project)
	_ = writer.WriteField("generation", cfg.Namespace.Generation)
	_ = writer.WriteField("defer_activation", "false")
	if err := writer.Close(); err != nil {
		return nil, err
	}
	path := cfg.Connections.LoomURL + "/api/v1/datasets/" + url.PathEscape(cfg.Namespace.Project) + "/generations/" + url.PathEscape(cfg.Namespace.Generation)
	return doJSON(ctx, client, http.MethodPost, path, &body, writer.FormDataContentType(), nil)
}

func compareSummary(payload map[string]any, want map[string]int) error {
	if reused, _ := payload["reused"].(bool); reused {
		return nil
	}
	summary, _ := payload["summary"].(map[string]any)
	if summary == nil {
		return errors.New("generation response omitted summary")
	}
	resources, _ := summary["resources"].(map[string]any)
	if resources == nil {
		return errors.New("generation response omitted resource counts")
	}
	for typ, count := range want {
		if got, ok := numeric(resources[typ]); !ok || got != int64(count) {
			return fmt.Errorf("generation count %s=%d want %d", typ, got, count)
		}
	}
	return nil
}

func directArangoCounts(ctx context.Context, cfg ScenarioConfig) (map[string]int, error) {
	client, err := arangostore.Open(ctx, cfg.Connections.ArangoURL, cfg.Namespace.ArangoDatabase)
	if err != nil {
		return nil, fmt.Errorf("open acceptance ArangoDB: %w", err)
	}
	defer client.Close(ctx)
	result := map[string]int{}
	for typ := range cfg.Fixture.Counts {
		count := 0
		query := "FOR doc IN @@collection FILTER doc.project == @project AND doc.dataset_generation == @generation RETURN {present: true}"
		err := client.QueryRows(ctx, query, 1000, map[string]any{"@collection": typ, "project": cfg.Namespace.Project, "generation": cfg.Namespace.Generation}, func(map[string]any) error { count++; return nil })
		if err != nil {
			return nil, fmt.Errorf("count Arango %s: %w", typ, err)
		}
		result[typ] = count
	}
	return result, nil
}

func compareCounts(got, want map[string]int) error {
	for typ, count := range want {
		if got[typ] != count {
			return fmt.Errorf("Arango count %s=%d want %d", typ, got[typ], count)
		}
	}
	return nil
}

func validateFixtureClosure(fixture ValidatedFixture, counts map[string]int) error {
	patients := make(map[string]bool)
	file, err := os.Open(filepath.Join(fixture.MetaDir, "Patient.ndjson"))
	if err != nil {
		return fmt.Errorf("open fixture patients: %w", err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		var value struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			_ = file.Close()
			return err
		}
		patients[value.ID] = true
	}
	if err := scanner.Err(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	for typ, count := range counts {
		if typ == "Patient" || typ == "ResearchStudy" {
			continue
		}
		file, err := os.Open(filepath.Join(fixture.MetaDir, typ+".ndjson"))
		if err != nil {
			return fmt.Errorf("open fixture %s: %w", typ, err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
		seen := 0
		for scanner.Scan() {
			var value any
			if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
				_ = file.Close()
				return err
			}
			refs := []string{}
			collectPatientRefs(value, &refs)
			for _, ref := range refs {
				id := strings.TrimPrefix(ref, "Patient/")
				if !patients[id] {
					_ = file.Close()
					return fmt.Errorf("fixture closure: %s references patient outside selected cohort: %s", typ, ref)
				}
			}
			seen++
		}
		closeErr := file.Close()
		if err := scanner.Err(); err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if seen != count {
			return fmt.Errorf("fixture closure: %s rows=%d want %d", typ, seen, count)
		}
	}
	return nil
}

func collectPatientRefs(value any, refs *[]string) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if key == "reference" {
				if ref, ok := child.(string); ok && strings.HasPrefix(ref, "Patient/") {
					*refs = append(*refs, ref)
				}
			}
			collectPatientRefs(child, refs)
		}
	case []any:
		for _, child := range item {
			collectPatientRefs(child, refs)
		}
	}
}

func publishWorkspace(ctx context.Context, cfg ScenarioConfig, client *http.Client, workspace []byte) (publication, error) {
	path := cfg.Connections.LoomURL + "/api/v1/projects/" + url.PathEscape(cfg.Namespace.Project) + "/generations/" + url.PathEscape(cfg.Namespace.Generation) + "/explorer-config"
	value, err := doJSON(ctx, client, http.MethodPost, path, bytes.NewReader(workspace), "application/json", map[string]string{"X-Loom-Source-Commit": cfg.SourceCommit})
	if err != nil {
		return publication{}, err
	}
	var result publication
	raw, _ := json.Marshal(value)
	if err := json.Unmarshal(raw, &result); err != nil {
		return publication{}, err
	}
	if result.Recipe == "" {
		result.Recipe = "tcga_brca_cohort"
	}
	if result.Output == "" {
		result.Output = "tcga_brca_cohort"
	}
	return result, nil
}

func migrateLegacyWorkspaceForCurrentAPI(ctx context.Context, cfg ScenarioConfig, client *http.Client, raw []byte) ([]byte, map[string]any, error) {
	workspace, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decode acceptance workspace for publication: %w", err)
	}
	if !hasLegacyAggregateWhere(workspace) {
		return raw, nil, nil
	}
	if workspace.SemanticsVersion != 3 {
		return nil, nil, fmt.Errorf("acceptance workspace contains legacy aggregate predicates at semanticsVersion %d; only v3 migration is supported", workspace.SemanticsVersion)
	}

	supported, err := authoringV2Supported(ctx, cfg, client)
	if err != nil {
		return nil, nil, err
	}
	if !supported {
		return raw, nil, nil
	}

	catalog, err := currentAcceptanceCatalog(ctx, cfg, client)
	if err != nil {
		return nil, nil, err
	}
	migrated, err := migrateLegacyAcceptanceWorkspace(workspace, catalog)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := migrated.CanonicalJSON()
	if err != nil {
		return nil, nil, fmt.Errorf("canonicalize migrated acceptance workspace: %w", err)
	}
	return canonical, map[string]any{
		"source_generation": catalog.SourceGeneration,
		"catalog_complete":  catalog.Complete,
		"column_count":      countWorkspaceColumns(migrated),
		"predicate_count":   countContributorPredicates(migrated),
	}, nil
}

func hasLegacyAggregateWhere(workspace authoringv2.Workspace) bool {
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind == authoringv2.SourceAggregate && column.Source.Aggregate != nil && column.Source.Aggregate.Where != nil {
				return true
			}
		}
	}
	return false
}

func migrateLegacyAcceptanceWorkspace(workspace authoringv2.Workspace, catalog authoringv2.CatalogSnapshot) (authoringv2.Workspace, error) {
	if err := validatePinnedContributorCatalog(catalog); err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("validate pinned acceptance catalog: %w", err)
	}
	migrated, err := authoringv2.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("migrate acceptance contributor predicates: %w", err)
	}
	migrated = authoringv2.MigrateLosslessDefaults(migrated, catalog).NormalizePresentationOrders()
	if err := migrated.ValidateForPublication(); err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("validate migrated acceptance workspace: %w", err)
	}
	return migrated, nil
}

func countContributorPredicates(workspace authoringv2.Workspace) int {
	count := 0
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Contributor != nil {
				count++
			}
		}
	}
	return count
}

func countWorkspaceColumns(workspace authoringv2.Workspace) int {
	count := 0
	for _, document := range workspace.Documents {
		count += len(document.Columns)
	}
	return count
}

func authoringV2Supported(ctx context.Context, cfg ScenarioConfig, client *http.Client) (bool, error) {
	path := cfg.Connections.LoomURL + "/api/v1/projects/" + url.PathEscape(cfg.Namespace.Project) + "/explorers/default/authoring/v2/capability"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("probe Explorer authoring v2 capability: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false, fmt.Errorf("read Explorer authoring capability: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("probe Explorer authoring v2 capability: HTTP %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var capability struct {
		APIVersion string   `json:"apiVersion"`
		Kind       string   `json:"kind"`
		Operations []string `json:"operations"`
	}
	if err := json.Unmarshal(raw, &capability); err != nil {
		return false, fmt.Errorf("decode Explorer authoring capability: %w", err)
	}
	if capability.APIVersion != authoringv2.APIVersion || capability.Kind != "ExplorerAuthoringCapabilities" {
		return false, fmt.Errorf("unexpected Explorer authoring capability protocol: apiVersion=%q kind=%q", capability.APIVersion, capability.Kind)
	}
	for _, operation := range capability.Operations {
		if operation == "builder" {
			return true, nil
		}
	}
	return false, nil
}

func currentAcceptanceCatalog(ctx context.Context, cfg ScenarioConfig, client *http.Client) (authoringv2.CatalogSnapshot, error) {
	var empty authoringv2.CatalogSnapshot
	if strings.TrimSpace(cfg.Namespace.Run) == "" {
		return empty, errors.New("acceptance run ID is required to create a verifier-owned catalog Explorer")
	}
	runDigest := sha256.Sum256([]byte(cfg.Namespace.Run))
	name := fmt.Sprintf("Acceptance catalog migration %x", runDigest[:6])
	createPath := cfg.Connections.LoomURL + "/api/v1/projects/" + url.PathEscape(cfg.Namespace.Project) + "/explorers"
	request, err := json.Marshal(map[string]string{"name": name, "title": name})
	if err != nil {
		return empty, err
	}
	created, err := doJSON(ctx, client, http.MethodPost, createPath, bytes.NewReader(request), "application/json", nil)
	if err != nil {
		return empty, fmt.Errorf("create verifier-owned catalog Explorer: %w", err)
	}
	explorerID, ok := created["explorerId"].(string)
	if !ok || strings.TrimSpace(explorerID) == "" {
		return empty, errors.New("created catalog Explorer response omitted explorerId")
	}
	builderPath := cfg.Connections.LoomURL + "/api/v1/projects/" + url.PathEscape(cfg.Namespace.Project) + "/explorers/" + url.PathEscape(explorerID) + "/authoring/v2/builder"
	state, err := getJSON(ctx, client, builderPath)
	if err != nil {
		return empty, fmt.Errorf("read verifier-owned Builder catalog: %w", err)
	}
	return decodeAcceptanceCatalog(state, cfg.Namespace.Project, explorerID, cfg.Namespace.Generation)
}

func decodeAcceptanceCatalog(state map[string]any, project, explorerID, generation string) (authoringv2.CatalogSnapshot, error) {
	var empty authoringv2.CatalogSnapshot
	if state["apiVersion"] != authoringv2.APIVersion || state["kind"] != authoringv2.StateKind {
		return empty, fmt.Errorf("unexpected Builder state protocol: apiVersion=%v kind=%v", state["apiVersion"], state["kind"])
	}
	catalogValue, ok := state["catalog"]
	if !ok {
		return empty, errors.New("Builder state omitted catalog")
	}
	catalogJSON, err := json.Marshal(catalogValue)
	if err != nil {
		return empty, fmt.Errorf("encode Builder catalog: %w", err)
	}
	var wire acceptanceCatalogWire
	if err := json.Unmarshal(catalogJSON, &wire); err != nil {
		return empty, fmt.Errorf("decode Builder catalog: %w", err)
	}
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
		Project: project, ExplorerID: explorerID,
		SourceGeneration: wire.Generation, AuthorizationScopeDigest: wire.AuthorizationScopeDigest,
		SnapshotToken: wire.SnapshotToken, Complete: wire.Complete, Truncated: wire.Truncated,
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
			RepeatedBoundaries: candidate.RepeatedBoundaries,
		})
	}
	// These scope fields are omitted from the public catalog representation;
	// bind them to the request and the Explorer created above before validation.
	if catalog.SourceGeneration != generation {
		return empty, fmt.Errorf("Builder catalog generation %q does not match acceptance generation %q", catalog.SourceGeneration, generation)
	}
	if err := validatePinnedContributorCatalog(catalog); err != nil {
		return empty, fmt.Errorf("validate verifier-owned Builder catalog: %w", err)
	}
	return catalog, nil
}

// The acceptance migration reads only node identity and field-candidate
// identity/type/repetition metadata. Decode that public projection explicitly:
// CatalogSnapshot cannot be unmarshaled directly because ConstructionChoice
// contains a closed interface source. Complete and any explicit truncated
// flag remain mandatory guards so this reduced view cannot consume a partial
// catalog.
type acceptanceCatalogWire struct {
	AuthorizationScopeDigest string                           `json:"authorizationScopeDigest"`
	Candidates               []acceptanceCatalogCandidateWire `json:"candidates"`
	Complete                 bool                             `json:"complete"`
	Generation               string                           `json:"generation"`
	Nodes                    []acceptanceCatalogNodeWire      `json:"nodes"`
	SnapshotToken            string                           `json:"snapshotToken"`
	Truncated                bool                             `json:"truncated"`
}

type acceptanceCatalogNodeWire struct {
	ID           string `json:"nodeId"`
	ResourceType string `json:"resourceType"`
}

type acceptanceCatalogCandidateWire struct {
	ID                 string                         `json:"candidateId"`
	NodeID             string                         `json:"nodeId"`
	FieldPath          string                         `json:"fieldPath"`
	LogicalType        string                         `json:"logicalType"`
	Repeated           bool                           `json:"repeated"`
	RepeatedBoundaries []authoringv2.RepeatedBoundary `json:"repeatedBoundaries"`
}

func validatePinnedContributorCatalog(catalog authoringv2.CatalogSnapshot) error {
	if catalog.APIVersion != authoringv2.APIVersion || catalog.Kind != authoringv2.CatalogKind {
		return errors.New("catalog protocol or kind is unsupported")
	}
	if strings.TrimSpace(catalog.Project) == "" || strings.TrimSpace(catalog.ExplorerID) == "" || strings.TrimSpace(catalog.SourceGeneration) == "" {
		return errors.New("catalog project, Explorer, or generation is missing")
	}
	if strings.TrimSpace(catalog.SnapshotToken) == "" || strings.TrimSpace(catalog.AuthorizationScopeDigest) == "" {
		return errors.New("catalog snapshot token or authorization scope digest is missing")
	}
	if !catalog.Complete || catalog.Truncated {
		return errors.New("catalog snapshot is incomplete or truncated")
	}
	nodes := make(map[string]authoringv2.CatalogNode, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.ResourceType) == "" {
			return errors.New("catalog contains an invalid node")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("catalog contains duplicate node %q", node.ID)
		}
		nodes[node.ID] = node
	}
	candidateIDs := make(map[string]bool, len(catalog.Candidates))
	for _, candidate := range catalog.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.FieldPath) == "" || strings.TrimSpace(candidate.LogicalType) == "" {
			return errors.New("catalog contains an invalid candidate")
		}
		if _, ok := nodes[candidate.NodeID]; !ok {
			return fmt.Errorf("catalog candidate %q references a missing node", candidate.ID)
		}
		if candidateIDs[candidate.ID] {
			return fmt.Errorf("catalog contains duplicate candidate %q", candidate.ID)
		}
		candidateIDs[candidate.ID] = true
		for _, boundary := range candidate.RepeatedBoundaries {
			if strings.TrimSpace(boundary.Path) == "" || boundary.MaxItems < 0 {
				return fmt.Errorf("catalog candidate %q has an invalid repeated boundary", candidate.ID)
			}
		}
	}
	return nil
}

func successfulExecution(value map[string]any, pub publication) error {
	state, _ := value["state"].(string)
	if !strings.EqualFold(state, "PUBLISHED") && !strings.EqualFold(state, "READY") {
		return fmt.Errorf("execution %s state %q is not successful", pub.ExecutionID, state)
	}
	outputs, _ := value["outputs"].([]any)
	if len(outputs) == 0 {
		return errors.New("execution registry omitted outputs")
	}
	return nil
}

func verifyClickHouse(ctx context.Context, cfg ScenarioConfig, pub publication, oracle Oracle) (map[string]any, error) {
	arangoClient, err := arangostore.Open(ctx, cfg.Connections.ArangoURL, cfg.Namespace.ArangoDatabase)
	if err != nil {
		return nil, fmt.Errorf("open publication registry: %w", err)
	}
	defer arangoClient.Close(ctx)
	registry, err := publicationarango.New(arangoClient)
	if err != nil {
		return nil, err
	}
	record, err := registry.GetExecution(ctx, pub.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("resolve publication execution %s: %w", pub.ExecutionID, err)
	}
	output, err := publicationOutput(record, pub.Output)
	if err != nil {
		return nil, err
	}
	table := output.PhysicalTable
	if err := compareColumns(output.Columns, oracle.Columns); err != nil {
		return nil, err
	}
	client, err := clickhousestore.New(clickhousestore.Options{URL: cfg.Connections.ClickHouseURL, Database: cfg.Namespace.ClickHouseDatabase, Username: cfg.Connections.ClickHouseUsername, Password: cfg.Connections.ClickHousePassword})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	rows, err := client.QueryRowsArgs(ctx, "SELECT count() AS row_count FROM `"+table+"`", []string{"row_count"})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("ClickHouse count returned %d rows", len(rows))
	}
	count, ok := numeric(rows[0]["row_count"])
	if !ok || count != int64(oracle.RowCount) {
		return nil, fmt.Errorf("ClickHouse row count=%d want %d", count, oracle.RowCount)
	}
	patientColumn := executionPatientColumn(output.Columns)
	if patientColumn == "" {
		return nil, errors.New("execution output has no patient identity column")
	}
	unique, err := client.QueryRowsArgs(ctx, "SELECT uniqExact(`"+patientColumn+"`) AS unique_patients FROM `"+table+"`", []string{"unique_patients"})
	if err != nil {
		return nil, err
	}
	uniqueCount := int64(0)
	if len(unique) > 0 {
		uniqueCount, _ = numeric(unique[0]["unique_patients"])
	}
	if uniqueCount != int64(oracle.UniquePatientCount) {
		return nil, fmt.Errorf("ClickHouse unique patient count=%d want %d", uniqueCount, oracle.UniquePatientCount)
	}
	return map[string]any{"table": table, "row_count": count, "unique_patients": uniqueCount}, nil
}

func compareColumns(got []dfpublication.PhysicalColumn, want []string) error {
	available := make(map[string]bool, len(got))
	for _, column := range got {
		available[column.Name] = true
	}
	for _, name := range want {
		if !available[name] {
			return fmt.Errorf("ClickHouse publication omitted expected column %q", name)
		}
	}
	return nil
}

func publicationOutput(execution dfpublication.BundleExecution, name string) (dfpublication.BundleOutputRecord, error) {
	for _, output := range execution.Outputs {
		if output.Name == name {
			if !output.Queryable() {
				return dfpublication.BundleOutputRecord{}, fmt.Errorf("publication output %q is not queryable", name)
			}
			return output, nil
		}
	}
	return dfpublication.BundleOutputRecord{}, fmt.Errorf("publication execution %s omitted output %q", execution.ID, name)

}

func executionPatientColumn(columns []dfpublication.PhysicalColumn) string {
	for _, column := range columns {
		lower := strings.ToLower(column.Name)
		if strings.Contains(lower, "patient") || lower == "id" {
			return column.Name
		}
	}
	return ""
}

func verifyViewer(viewer map[string]any, pub publication) error {
	if len(viewer) == 0 {
		return errors.New("Explorer viewer returned an empty state")
	}
	raw, _ := json.Marshal(viewer)
	if pub.Recipe != "" && !bytes.Contains(bytes.ToLower(raw), []byte(strings.ToLower(pub.Recipe))) {
		return fmt.Errorf("Explorer viewer does not contain publication recipe %q", pub.Recipe)
	}
	return nil
}

func verifyGraphQL(ctx context.Context, cfg ScenarioConfig, pub publication, oracle Oracle) (map[string]any, error) {
	selector := map[string]any{"recipe": pub.Recipe, "translationVersion": pub.TranslationVersion, "output": pub.Output}
	query := `query($input:DataframeDatasetInput!){dataframeDataset(input:$input){id name projectId datasetGeneration state rowCount selector{recipe translationVersion output} columns{name}}}`
	dataset, err := graphql(ctx, cfg, query, map[string]any{"input": map[string]any{"projectId": cfg.Namespace.Project, "selector": selector}})
	if err != nil {
		return nil, err
	}
	data, _ := dataset["data"].(map[string]any)
	materialization, _ := data["dataframeDataset"].(map[string]any)
	if materialization == nil {
		return nil, errors.New("GraphQL dataset metadata is null")
	}
	if state, _ := materialization["state"].(string); state != "READY" {
		return nil, fmt.Errorf("GraphQL dataset state=%q want READY", state)
	}
	if err := compareGraphQLColumns(materialization, oracle.Columns); err != nil {
		return nil, err
	}
	count, ok := numeric(materialization["rowCount"])
	if !ok {
		return nil, errors.New("GraphQL dataset rowCount is missing or has the wrong shape")
	}
	if count != int64(oracle.RowCount) {
		return nil, fmt.Errorf("GraphQL dataset rowCount=%d want %d", count, oracle.RowCount)
	}
	rowsQuery := `query($input:DataframeRowsInput!){dataframeRows(input:$input){columns rows totalCount pageInfo{hasNextPage}}}`
	rows, err := graphql(ctx, cfg, rowsQuery, map[string]any{"input": map[string]any{"projectId": cfg.Namespace.Project, "selector": selector, "first": oracle.RowCount, "sort": map[string]any{"column": patientColumn(materialization), "desc": false}}})
	if err != nil {
		return nil, err
	}
	rowsData, ok := rows["data"].(map[string]any)
	if !ok {
		return nil, errors.New("GraphQL rows data is missing or has the wrong shape")
	}
	connection, ok := rowsData["dataframeRows"].(map[string]any)
	if !ok {
		return nil, errors.New("GraphQL dataframeRows is missing or has the wrong shape")
	}
	total, ok := numeric(connection["totalCount"])
	if !ok {
		return nil, errors.New("GraphQL dataframeRows totalCount is missing or has the wrong shape")
	}
	if total != int64(oracle.RowCount) {
		return nil, fmt.Errorf("GraphQL rows totalCount=%d want %d", total, oracle.RowCount)
	}
	rowValues, ok := connection["rows"].([]any)
	if !ok {
		return nil, errors.New("GraphQL dataframeRows rows is missing or has the wrong shape")
	}
	if err := verifyRowProfiles(rowValues, oracle); err != nil {
		return nil, err
	}
	if oracle.RowDigest != "" {
		if got := normalizedRowsDigest(rowValues); got != oracle.RowDigest {
			return nil, fmt.Errorf("GraphQL normalized row digest=%s want %s", got, oracle.RowDigest)
		}
	}
	countQuery := `query($input:DataframeAggregateInput!){dataframeAggregate(input:$input){columns rows}}`
	_, err = graphql(ctx, cfg, countQuery, map[string]any{"input": map[string]any{"projectId": cfg.Namespace.Project, "selector": selector, "operation": "COUNT"}})
	if err != nil {
		return nil, err
	}
	facetQuery := `query($input:DataframeAggregationsInput!){dataframeAggregations(input:$input){aggregations}}`
	_, err = graphql(ctx, cfg, facetQuery, map[string]any{"input": map[string]any{"projectId": cfg.Namespace.Project, "selector": selector, "specs": []any{map[string]any{"name": "patient_values", "kind": "TERMS", "column": firstColumn(materialization), "size": 10}}}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"dataset": materialization, "rows": rows["data"], "row_digest": graphqlRowsDigest(rows)}, nil
}

func verifyRowProfiles(rows []any, oracle Oracle) error {
	nonNull := make(map[string]int, len(oracle.NonNullCounts))
	trueValues := make(map[string]int, len(oracle.TrueCounts))
	for index, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("GraphQL row %d has the wrong shape", index)
		}
		for column := range oracle.NonNullCounts {
			if value, exists := row[column]; exists && value != nil {
				nonNull[column]++
			}
		}
		for column := range oracle.TrueCounts {
			if value, exists := row[column]; exists {
				if boolean, ok := value.(bool); ok && boolean {
					trueValues[column]++
				}
			}
		}
	}
	for column, want := range oracle.NonNullCounts {
		if got := nonNull[column]; got != want {
			return fmt.Errorf("GraphQL column %s non-null rows=%d want %d", column, got, want)
		}
	}
	for column, want := range oracle.TrueCounts {
		if got := trueValues[column]; got != want {
			return fmt.Errorf("GraphQL column %s true rows=%d want %d", column, got, want)
		}
	}
	return nil
}

func graphqlRowsDigest(response map[string]any) string {
	data, _ := response["data"].(map[string]any)
	connection, _ := data["dataframeRows"].(map[string]any)
	return normalizedRowsDigest(connection["rows"])
}

func firstColumn(materialization map[string]any) string {
	cols, _ := materialization["columns"].([]any)
	for _, raw := range cols {
		if col, ok := raw.(map[string]any); ok {
			name, _ := col["name"].(string)
			if !strings.HasPrefix(name, "__loom_") {
				return name
			}
		}
	}
	return "patient_id"
}

func patientColumn(materialization map[string]any) string {
	cols, _ := materialization["columns"].([]any)
	for _, raw := range cols {
		if col, ok := raw.(map[string]any); ok {
			name, _ := col["name"].(string)
			if name == "patient_id" {
				return name
			}
		}
	}
	return firstColumn(materialization)
}

func compareGraphQLColumns(materialization map[string]any, want []string) error {
	cols, _ := materialization["columns"].([]any)
	available := make(map[string]bool, len(cols))
	for _, raw := range cols {
		if col, ok := raw.(map[string]any); ok {
			name, _ := col["name"].(string)
			available[name] = true
		}
	}
	for _, name := range want {
		if !available[name] {
			return fmt.Errorf("GraphQL dataset omitted expected column %q", name)
		}
	}
	return nil
}

func graphql(ctx context.Context, cfg ScenarioConfig, query string, variables map[string]any) (map[string]any, error) {
	payload := map[string]any{"query": query, "variables": variables}
	raw, _ := json.Marshal(payload)
	value, err := doJSON(ctx, cfg.HTTPClient, http.MethodPost, cfg.Connections.LoomURL+"/graphql/graph", bytes.NewReader(raw), "application/json", nil)
	if err != nil {
		return nil, err
	}
	if errorsValue, ok := value["errors"]; ok && errorsValue != nil {
		return nil, fmt.Errorf("GraphQL errors: %v", errorsValue)
	}
	return value, nil
}

func getJSON(ctx context.Context, client *http.Client, target string) (map[string]any, error) {
	return doJSON(ctx, client, http.MethodGet, target, nil, "", nil)
}

func doJSON(ctx context.Context, client *http.Client, method, target string, body io.Reader, contentType string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if readErr != nil {
		return nil, readErr
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode HTTP %s response (%s): %w", method, resp.Status, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s %s: %s", method, resp.Status, strings.TrimSpace(string(raw)))
	}
	return value, nil
}

func numeric(value any) (int64, bool) {
	switch item := value.(type) {
	case int:
		return int64(item), true
	case int64:
		return item, true
	case float64:
		return int64(item), item == float64(int64(item))
	case json.Number:
		n, err := item.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(item, 10, 64)
		return n, err == nil
	}
	return 0, false
}
func sortedMapKeys(value map[string]any) []string {
	result := make([]string, 0, len(value))
	for key := range value {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func normalizedRowsDigest(rows any) string {
	raw, _ := json.Marshal(rows)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeArtifact(dir, name string, value any) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
