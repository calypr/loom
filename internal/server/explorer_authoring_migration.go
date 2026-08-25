package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/bootstrap"
	"github.com/calypr/loom/internal/projectid"
)

// ExplorerAuthoringMigrationOptions describes the one-shot, server-side
// legacy cutover. It is intentionally not exposed as an HTTP operation.
type ExplorerAuthoringMigrationOptions struct {
	Project    string
	ExplorerID string
	Actor      string
	RequestID  string
	AuditOnly  bool
	// LegacyConfig and LegacyMapping are optional external migration inputs.
	// When present they are used even if Loom has no existing Explorer record;
	// the raw bytes are retained on the resulting immutable revision.
	LegacyConfig  []byte
	LegacyMapping []byte
}

type ExplorerAuthoringMigrationReport struct {
	Mode              string   `json:"mode"`
	Project           string   `json:"project"`
	ExplorerID        string   `json:"explorerId"`
	LegacyRevisionID  string   `json:"legacyRevisionId,omitempty"`
	SourceGeneration  string   `json:"sourceGeneration"`
	Source            string   `json:"source"`
	IntentDigest      string   `json:"intentDigest"`
	ReceiptID         string   `json:"receiptId"`
	DraftVersion      int64    `json:"draftVersion"`
	PublicationID     string   `json:"publicationId"`
	DocumentCount     int      `json:"documentCount"`
	TabCount          int      `json:"tabCount"`
	OutputCount       int      `json:"outputCount"`
	MaterializationID string   `json:"materializationId,omitempty"`
	PersistedDocument bool     `json:"persistedDocument"`
	PublicationPolicy string   `json:"publicationPolicy"`
	DraftPersisted    bool     `json:"draftPersisted"`
	Materialized      bool     `json:"materialized"`
	DatasetActivated  bool     `json:"datasetActivated"`
	ExplorerActivated bool     `json:"explorerActivated"`
	Diagnostics       []string `json:"diagnostics,omitempty"`
}

// MigrateLegacyExplorerAuthoring converts the active legacy default, compiles
// it with the current server resolver and, unless AuditOnly is set,
// materializes it and atomically stores and activates its immutable revision.
// Any conversion/resolution failure aborts the cutover. Migration never runs a
// global draft purge; unrelated Explorer authoring state is outside its scope.
func MigrateLegacyExplorerAuthoring(ctx context.Context, service *explorer.Service, capabilities ExplorerV2LifecycleConfig, options ExplorerAuthoringMigrationOptions) (ExplorerAuthoringMigrationReport, error) {
	options.Project = projectid.Canonical(options.Project)
	options.ExplorerID = strings.TrimSpace(options.ExplorerID)
	if options.ExplorerID == "" {
		options.ExplorerID = "default"
	}
	if options.Actor == "" {
		options.Actor = "loom-authoring-migration"
	}
	if options.RequestID == "" {
		options.RequestID = "loom-authoring-migration-" + options.Project + "-" + options.ExplorerID
	}
	if service == nil || capabilities.Catalog == nil || capabilities.AuthoringCompile == nil || capabilities.Materialize == nil || capabilities.ValidateReleaseGeneration == nil || capabilities.ActivateRelease == nil {
		return ExplorerAuthoringMigrationReport{}, errors.New("Explorer authoring migration requires catalog, compiler, materializer, generation validator, and release activation")
	}

	mode := "apply"
	if options.AuditOnly {
		mode = "audit"
	}
	report := ExplorerAuthoringMigrationReport{Mode: mode, Project: options.Project, ExplorerID: options.ExplorerID, PublicationPolicy: "LAST_WRITE_WINS", PersistedDocument: !options.AuditOnly}
	owner, legacyConfig, err := migrationSource(ctx, service, options)
	if err != nil {
		return report, err
	}
	report.LegacyRevisionID = owner.ActiveRevisionID

	snapshot, err := capabilities.Catalog(ctx, options.Project, options.ExplorerID, "")
	if err != nil {
		return report, fmt.Errorf("load current authoring catalog: %w", err)
	}
	if !snapshot.Complete {
		return report, fmt.Errorf("current authoring catalog is incomplete: %v", snapshot.Diagnostics)
	}
	report.SourceGeneration = snapshot.Generation

	var activeBundle json.RawMessage
	if owner.ActiveRevisionID != "" {
		if activeRevision, revisionErr := service.Revision(ctx, owner.ActiveRevisionID); revisionErr == nil {
			activeBundle = activeRevision.AuthoringBundle
		}
	}
	var migrationDiagnostics []string
	bundle, source, err := migrationBundleWithDiagnostics(legacyConfig, options.LegacyMapping, activeBundle, snapshot.Catalog, options.Project, options.ExplorerID, &migrationDiagnostics)
	if err != nil {
		return report, err
	}
	report.Source = source
	report.Diagnostics = append(report.Diagnostics, migrationDiagnostics...)
	report.DocumentCount = len(bundle.AuthoringDocuments())
	report.TabCount = len(bundle.AuthoringTabs())
	report.OutputCount = len(bundle.AuthoringDocuments())

	compiled, err := capabilities.AuthoringCompile(ctx, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token, RequestID: options.RequestID})
	if err != nil {
		return report, fmt.Errorf("compile migrated Explorer authoring intent: %w", err)
	}
	receipt := compiled.Receipt
	report.IntentDigest, report.ReceiptID = receipt.IntentDigest, receipt.ID
	if options.AuditOnly {
		return report, nil
	}

	report.DraftVersion, report.DraftPersisted = 0, false

	revisionID := "authoring_" + strings.TrimPrefix(receipt.ID, "receipt_")
	if len(options.LegacyConfig) > 0 || len(options.LegacyMapping) > 0 {
		// Keep an externally seeded migration distinct from a previously
		// published authoring revision with the same executable receipt. This
		// preserves the old revision for rollback while making a rerun with the
		// same source inputs idempotent.
		revisionID = migrationRevisionID(receipt.ID, options.LegacyConfig, options.LegacyMapping)
	}
	if active, activeErr := service.ActiveRevision(ctx, options.Project, options.ExplorerID); activeErr == nil && active.ID == revisionID {
		report.PublicationID = revisionID
		report.DatasetActivated, report.ExplorerActivated, report.Materialized = true, true, true
		return report, nil
	}
	// Dataset lifecycle storage still uses the historical project alias. The
	// authoring bundle/API stays canonical, but release validation, runtime
	// bindings, and activation must use the storage identity or a valid active
	// generation will be reported as missing.
	storageProject := projectid.Legacy(receipt.Project)
	if err := capabilities.ValidateReleaseGeneration(ctx, storageProject, receipt.SourceGeneration); err != nil {
		return report, fmt.Errorf("validate migrated Explorer generation: %w", err)
	}
	execution, err := capabilities.Materialize(ctx, receipt.Bundle, recipe.RuntimeBindings{Project: storageProject, DatasetGeneration: receipt.SourceGeneration})
	if err != nil {
		return report, fmt.Errorf("materialize migrated Explorer receipt: %w", err)
	}
	if err := verifyQueryableOutputs(receipt.Bundle, execution); err != nil {
		return report, fmt.Errorf("validate migrated Explorer materialization: %w", err)
	}
	report.MaterializationID, report.Materialized = execution.ID, true
	materializations := explorerMaterializations(receipt.Bundle, execution)
	datasetMetadata := datasetMetadataFromExecution(receipt.Bundle, receipt.SourceGeneration, execution.ResolvedSchemaDigest, execution)
	publication := explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: receipt.SourceGeneration, ExecutionID: execution.ID, UpdatedAt: time.Now().UTC()}
	now := time.Now().UTC()
	revision := explorer.Revision{
		ID: revisionID, Project: receipt.Project, ExplorerID: receipt.ExplorerID,
		Config: receipt.CompiledConfig, ConfigDigest: receipt.IntentDigest,
		AuthoringBundle: receipt.NormalizedBundle, IntentDigest: receipt.IntentDigest,
		CompilationReceiptID: receipt.ID, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest,
		ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration,
		Materializations: materializations, EmittedColumns: receipt.EmittedColumns,
		Dataset: datasetMetadata, Publication: publication, Status: explorer.RevisionReady,
		CreatedBy: options.Actor, CreatedAt: now, ReadyAt: &now,
		Migration: migrationMetadata(options, legacyConfig, source),
	}
	if err := capabilities.ActivateRelease(ctx, storageProject, receipt.SourceGeneration, selectorsForBundle(receipt.Bundle)); err != nil {
		return report, fmt.Errorf("activate migrated dataset release: %w", err)
	}
	report.DatasetActivated = true
	published, err := service.PublishAuthoring(ctx, receipt, revision)
	if err != nil {
		return report, fmt.Errorf("atomically persist and activate migrated Explorer revision: %w", err)
	}
	report.PublicationID = published.ID
	report.ExplorerActivated = true
	return report, nil
}

func migrationSource(ctx context.Context, service *explorer.Service, options ExplorerAuthoringMigrationOptions) (*explorer.Explorer, []byte, error) {
	project := projectid.Canonical(options.Project)
	owner, err := service.Get(ctx, project, options.ExplorerID)
	if errors.Is(err, explorer.ErrNotFound) && (len(options.LegacyConfig) > 0 || len(options.LegacyMapping) > 0) {
		title := migrationTitle(options.LegacyConfig, options.LegacyMapping)
		owner, err = service.EnsureRepositoryExplorer(ctx, project, options.ExplorerID, title, options.Actor)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load legacy Explorer state: %w", err)
	}
	legacyConfig := append([]byte(nil), options.LegacyConfig...)
	if len(legacyConfig) == 0 {
		legacyConfig = owner.ActiveConfig
	}
	if owner.ActiveRevisionID != "" {
		if revision, revisionErr := service.Revision(ctx, owner.ActiveRevisionID); revisionErr == nil && len(revision.Config) > 0 {
			if len(options.LegacyConfig) == 0 {
				legacyConfig = revision.Config
			}
		}
	}
	if len(legacyConfig) == 0 && len(options.LegacyMapping) == 0 {
		return nil, nil, errors.New("legacy Explorer has no active document")
	}
	return owner, legacyConfig, nil
}

func migrationBundle(rawConfig, rawMapping, existingBundle json.RawMessage, catalog explorer.Catalog, project, explorerID string) (explorer.ExplorerAuthoringBundleV1, string, error) {
	return migrationBundleWithDiagnostics(rawConfig, rawMapping, existingBundle, catalog, project, explorerID, nil)
}

func migrationBundleWithDiagnostics(rawConfig, rawMapping, existingBundle json.RawMessage, catalog explorer.Catalog, project, explorerID string, diagnostics *[]string) (explorer.ExplorerAuthoringBundleV1, string, error) {
	// Explicit migration inputs always win over whatever happens to be active
	// in Loom. When both inputs are supplied, the legacy config is the source
	// of field intent and the mapping is only a starting point: old mapping
	// exports can contain an output with an empty candidate list even though
	// the current catalog contains exact matches for its recipe fields.
	if len(rawConfig) > 0 {
		bundle, conversionReport, err := bootstrap.ConvertDefault(rawConfig, catalog, project)
		if err == nil {
			if bundle.ExplorerID != explorerID {
				return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("converted bundle explorerId %q does not match %q", bundle.ExplorerID, explorerID)
			}
			return bundle, "legacy-config", nil
		}
		if len(rawMapping) > 0 {
			mapped, mappingErr := mappingAuthoringBundle(rawMapping, project, explorerID)
			if mappingErr == nil {
				repaired, repairDiagnostics, repairErr := repairMappedBundle(rawConfig, mapped, catalog, project, explorerID)
				if repairErr == nil {
					// Preserve explicit information about legacy fields that the
					// current catalog does not expose. They are not guessed into a
					// different field, but the migrated outputs must still contain
					// every candidate that can be resolved and compiled.
					if diagnostics != nil {
						*diagnostics = append(*diagnostics, repairDiagnostics...)
					}
					return repaired, "frontend-mapping-repaired", nil
				}
				if conversionReportJSON, marshalErr := conversionReport.CanonicalJSON(); marshalErr == nil {
					return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %s: %w; mapping repair also failed: %v", conversionReportJSON, err, repairErr)
				}
				return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %w; mapping repair also failed: %v", err, repairErr)
			}
			if conversionReportJSON, marshalErr := conversionReport.CanonicalJSON(); marshalErr == nil {
				return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %s: %w; decode frontend authoring mapping: %v", conversionReportJSON, err, mappingErr)
			}
			return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %w; decode frontend authoring mapping: %v", err, mappingErr)
		}
		if conversionReportJSON, marshalErr := conversionReport.CanonicalJSON(); marshalErr == nil {
			return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %s: %w", conversionReportJSON, err)
		}
		return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", err
	}
	if len(rawMapping) > 0 {
		bundle, err := mappingAuthoringBundle(rawMapping, project, explorerID)
		if err != nil {
			return explorer.ExplorerAuthoringBundleV1{}, "frontend-mapping", fmt.Errorf("decode frontend authoring mapping: %w", err)
		}
		return bundle, "frontend-mapping", nil
	}
	if len(existingBundle) > 0 {
		bundle, err := explorer.DecodeAuthoringBundleV1ForMigration(existingBundle)
		if err == nil && bundle.Project == project && bundle.ExplorerID == explorerID {
			return bundle, "active-v1-bundle", nil
		}
	}
	bundle, conversionReport, err := bootstrap.ConvertDefault(rawConfig, catalog, project)
	if err != nil {
		if conversionReportJSON, marshalErr := conversionReport.CanonicalJSON(); marshalErr == nil {
			return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("typed legacy Explorer conversion failed: %s: %w", conversionReportJSON, err)
		}
		return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", err
	}
	if bundle.ExplorerID != explorerID {
		return explorer.ExplorerAuthoringBundleV1{}, "legacy-config", fmt.Errorf("converted bundle explorerId %q does not match %q", bundle.ExplorerID, explorerID)
	}
	return bundle, "legacy-config", nil
}

func mappingAuthoringBundle(raw []byte, project, explorerID string) (explorer.ExplorerAuthoringBundleV1, error) {
	var envelope struct {
		Bundle json.RawMessage `json:"bundle"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, err
	}
	if len(envelope.Bundle) == 0 {
		return explorer.ExplorerAuthoringBundleV1{}, errors.New("mapping does not contain a bundle")
	}
	bundle, err := explorer.DecodeAuthoringBundleV1ForMigration(envelope.Bundle)
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, err
	}
	if projectid.Canonical(bundle.Project) != projectid.Canonical(project) || bundle.ExplorerID != explorerID {
		return explorer.ExplorerAuthoringBundleV1{}, fmt.Errorf("mapping identity %q/%q does not match %q/%q", bundle.Project, bundle.ExplorerID, project, explorerID)
	}
	bundle.Project = projectid.Canonical(project)
	bundle.IntentDigest = ""
	digest, err := bundle.DocumentDigest()
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, err
	}
	bundle.IntentDigest = digest
	return bundle, nil
}

// repairMappedBundle fills gaps in a frontend mapping export from the legacy
// recipe and the current Loom catalog. Mapping exports are useful migration
// hints, but they are not authoritative: older exports were able to persist a
// graph and an output while dropping all candidate IDs for that output. The
// authoring compiler cannot invent those IDs, so an empty list would compile
// into an empty column set forever.
//
// The repair deliberately resolves only explicit legacy selections. A legacy
// call such as first(root.identifier[].value) still names an exact catalog
// field and is therefore repaired from its nested select. Calls with multiple
// selections contribute each exact catalog candidate; no opaque field is
// guessed or silently replaced.
func repairMappedBundle(rawConfig []byte, bundle explorer.ExplorerAuthoringBundleV1, catalog explorer.Catalog, project, explorerID string) (explorer.ExplorerAuthoringBundleV1, []string, error) {
	if len(rawConfig) == 0 {
		return bundle, nil, nil
	}
	cfg, legacy, err := explorer.DecodeDefaultConfigV2(rawConfig, project)
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("decode legacy config for mapping repair: %w", err)
	}
	documents := bundle.AuthoringDocuments()
	if len(documents) == 0 {
		return explorer.ExplorerAuthoringBundleV1{}, nil, errors.New("mapping bundle contains no authoring documents")
	}
	nodeByResource := make(map[string]string, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		if prior, exists := nodeByResource[node.ResourceType]; exists && prior != node.ID {
			return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("catalog has multiple node IDs for resource type %q", node.ResourceType)
		}
		nodeByResource[node.ResourceType] = node.ID
	}
	selectionByID := catalog.Selections
	unmapped := make([]string, 0)
	for outputIndex, output := range legacy.Outputs {
		documentIndex := mappedDocumentIndex(documents, output.Name)
		if documentIndex < 0 {
			return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("mapping bundle has no document for legacy output %q", output.Name)
		}
		document := &documents[documentIndex]
		if strings.TrimSpace(document.BaseNodeID) == "" {
			document.BaseNodeID = nodeByResource[output.RootResourceType]
		}
		if document.BaseNodeID == "" {
			return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("legacy output %q has no catalog node for resource type %q", output.Name, output.RootResourceType)
		}
		if strings.TrimSpace(document.RowNodeID) == "" {
			document.RowNodeID = document.BaseNodeID
		}
		// Preserve existing valid IDs, but remove stale IDs before adding IDs
		// resolved from the current catalog. This makes reruns safe across
		// catalog generations and prevents a stale mapping from poisoning the
		// one-shot compile with STALE_CANDIDATE.
		valid := make([]string, 0, len(document.CandidateIDs))
		seen := make(map[string]bool, len(document.CandidateIDs))
		fieldCandidates := make(map[string][]string, len(output.Fields))
		for _, candidateID := range document.CandidateIDs {
			selection, ok := selectionByID[candidateID]
			if !ok || selection.NodeID != document.BaseNodeID || seen[candidateID] {
				continue
			}
			seen[candidateID] = true
			valid = append(valid, candidateID)
		}
		for _, reference := range document.CandidateOccurrences {
			if reference.OccurrenceID != "base" || seen[reference.CandidateID] {
				continue
			}
			selection, ok := selectionByID[reference.CandidateID]
			if ok && selection.NodeID == document.BaseNodeID {
				seen[reference.CandidateID] = true
				valid = append(valid, reference.CandidateID)
			}
		}
		for fieldIndex, field := range output.Fields {
			paths := legacySelectionPaths(field.Expr)
			if len(paths) == 0 {
				unmapped = append(unmapped, fmt.Sprintf("outputs[%d].fields[%d].expr", outputIndex, fieldIndex))
				continue
			}
			matched := false
			for _, path := range paths {
				candidateID, ok := catalogCandidateForPath(catalog, document.BaseNodeID, path)
				if !ok {
					continue
				}
				matched = true
				if !containsString(fieldCandidates[field.Name], candidateID) {
					fieldCandidates[field.Name] = append(fieldCandidates[field.Name], candidateID)
				}
				if !seen[candidateID] {
					seen[candidateID] = true
					valid = append(valid, candidateID)
				}
			}
			if !matched {
				unmapped = append(unmapped, fmt.Sprintf("outputs[%d].fields[%d].%s", outputIndex, fieldIndex, field.Name))
			}
		}
		if len(valid) == 0 {
			return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("legacy output %q has no fields that resolve against the current catalog", output.Name)
		}
		sort.Strings(valid)
		document.CandidateIDs = valid
		// Presentation keys are emission identities and therefore change when a
		// legacy candidate/node identity is rebound. Retain only bindings that
		// already name a current base emission; the legacy view below restores
		// labels, order, visibility, filters, and charts under canonical IDs.
		canonicalPresentation := make(map[string]explorer.ExplorerPresentationBindingV1, len(valid))
		for _, candidateID := range valid {
			emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00base\x00"+candidateID)
			if binding, ok := document.Presentation[emissionID]; ok {
				canonicalPresentation[emissionID] = binding
			}
		}
		document.Presentation = canonicalPresentation
		// The canonical authoring contract always records the exact occurrence
		// for every selected candidate, including the zero-hop base occurrence.
		// Legacy inference is confined to this migration and never runs while the
		// Builder is reading or reconciling an already-migrated bundle.
		occurrences := make([]explorer.ExplorerCandidateOccurrenceV1, 0, len(valid))
		for _, candidateID := range valid {
			occurrences = append(occurrences, explorer.ExplorerCandidateOccurrenceV1{CandidateID: candidateID, OccurrenceID: "base"})
		}
		document.CandidateOccurrences = occurrences
		repairDocumentPresentation(cfg.Views, output, document, fieldCandidates, &unmapped)
	}
	bundle.Document = explorer.ExplorerBuilderDocumentV1{}
	bundle.Documents = documents
	bundle.Project = projectid.Canonical(project)
	bundle.ExplorerID = explorerID
	bundle.IntentDigest = ""
	digest, err := bundle.DocumentDigest()
	if err != nil {
		return explorer.ExplorerAuthoringBundleV1{}, nil, fmt.Errorf("digest repaired authoring bundle: %w", err)
	}
	bundle.IntentDigest = digest
	return bundle, unmapped, nil
}

func repairDocumentPresentation(views []explorer.ConfigView, output recipe.Output, document *explorer.ExplorerBuilderDocumentV1, fieldCandidates map[string][]string, diagnostics *[]string) {
	if document.Presentation == nil {
		document.Presentation = map[string]explorer.ExplorerPresentationBindingV1{}
	}
	aliases := make(map[string][]string)
	for fieldName, candidateIDs := range fieldCandidates {
		if len(candidateIDs) != 1 {
			continue
		}
		emissionID := explorer.OpaqueID("em_", document.Output.ID+"\x00base\x00"+candidateIDs[0])
		for _, alias := range []string{
			fieldName,
			output.Name + fieldName,
			output.RootResourceType + fieldName,
			document.Output.Title + fieldName,
		} {
			key := normalizeLegacyColumnAlias(alias)
			if key != "" && !containsString(aliases[key], emissionID) {
				aliases[key] = append(aliases[key], emissionID)
			}
		}
	}
	resolve := func(column string) (string, bool) {
		matches := aliases[normalizeLegacyColumnAlias(column)]
		return firstUniqueString(matches)
	}
	for viewIndex, view := range views {
		if !strings.EqualFold(strings.TrimSpace(view.Output), strings.TrimSpace(output.Name)) {
			continue
		}
		for columnIndex, column := range view.Table.Columns {
			emissionID, ok := resolve(column.Column)
			if !ok {
				if hasPresentationTableOrder(document.Presentation, columnIndex) {
					continue
				}
				*diagnostics = append(*diagnostics, fmt.Sprintf("views[%d].table.columns[%d].%s", viewIndex, columnIndex, column.Column))
				continue
			}
			binding := document.Presentation[emissionID]
			if strings.TrimSpace(binding.Label) == "" {
				binding.Label = firstNonEmpty(column.Label, column.Column)
			}
			if binding.Visible == nil {
				visible := column.Visible
				binding.Visible = &visible
			}
			if binding.Order == nil {
				order := columnIndex
				binding.Order = &order
			}
			if binding.Table == nil {
				binding.Table = &explorer.ExplorerTableBindingV1{}
			}
			document.Presentation[emissionID] = binding
		}
		for filterIndex, filter := range view.Filters {
			emissionID, ok := resolve(filter.Column)
			if !ok {
				if presentationBindingCount(document.Presentation, "filter") > filterIndex {
					continue
				}
				*diagnostics = append(*diagnostics, fmt.Sprintf("views[%d].filters[%d].%s", viewIndex, filterIndex, filter.Column))
				continue
			}
			binding := document.Presentation[emissionID]
			if binding.Filter == nil {
				binding.Filter = &explorer.ExplorerFilterBindingV1{Label: filter.Label}
			}
			document.Presentation[emissionID] = binding
		}
		for chartIndex, chart := range view.Charts {
			emissionID, ok := resolve(chart.Column)
			if !ok {
				if presentationBindingCount(document.Presentation, "chart") > chartIndex {
					continue
				}
				*diagnostics = append(*diagnostics, fmt.Sprintf("views[%d].charts[%d].%s", viewIndex, chartIndex, chart.Column))
				continue
			}
			binding := document.Presentation[emissionID]
			if binding.Chart == nil {
				binding.Chart = &explorer.ExplorerChartBindingV1{Type: chart.Type, Title: chart.Title}
			}
			document.Presentation[emissionID] = binding
		}
	}
}

func hasPresentationTableOrder(presentation map[string]explorer.ExplorerPresentationBindingV1, order int) bool {
	for _, binding := range presentation {
		if binding.Table != nil && binding.Order != nil && *binding.Order == order {
			return true
		}
	}
	return false
}

func presentationBindingCount(presentation map[string]explorer.ExplorerPresentationBindingV1, kind string) int {
	count := 0
	for _, binding := range presentation {
		if kind == "filter" && binding.Filter != nil || kind == "chart" && binding.Chart != nil {
			count++
		}
	}
	return count
}

func normalizeLegacyColumnAlias(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func firstUniqueString(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func mappedDocumentIndex(documents []explorer.ExplorerBuilderDocumentV1, output string) int {
	stable := explorer.StableExplorerID(output)
	for index, document := range documents {
		if document.Output.ID == stable || strings.EqualFold(document.Output.ID, output) || strings.EqualFold(document.Output.Title, output) {
			return index
		}
	}
	return -1
}

func legacySelectionPaths(expression recipe.Expression) []string {
	paths := make([]string, 0, 1)
	var walk func(recipe.Expression)
	walk = func(current recipe.Expression) {
		if strings.TrimSpace(current.Select) != "" {
			path := strings.TrimSpace(current.Select)
			if !containsString(paths, path) {
				paths = append(paths, path)
			}
		}
		for _, argument := range current.Args {
			walk(argument)
		}
	}
	walk(expression)
	return paths
}

func catalogCandidateForPath(catalog explorer.Catalog, nodeID, path string) (string, bool) {
	want := normalizeCatalogSelectionPath(path)
	for candidateID, selection := range catalog.Selections {
		if selection.NodeID != nodeID || normalizeCatalogSelectionPath(selection.Select) != want {
			continue
		}
		return candidateID, true
	}
	return "", false
}

func normalizeCatalogSelectionPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "root.") {
		path = strings.TrimPrefix(path, "root.")
	}
	return strings.ToLower(strings.ReplaceAll(path, "[]", ""))
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func migrationTitle(config, mapping []byte) string {
	var mappingEnvelope struct {
		Title  string `json:"title"`
		Bundle struct {
			Title string `json:"title"`
		} `json:"bundle"`
	}
	if len(mapping) > 0 && json.Unmarshal(mapping, &mappingEnvelope) == nil {
		if strings.TrimSpace(mappingEnvelope.Title) != "" {
			return mappingEnvelope.Title
		}
		if strings.TrimSpace(mappingEnvelope.Bundle.Title) != "" {
			return mappingEnvelope.Bundle.Title
		}
	}
	var envelope struct {
		Explorer struct {
			Title string `json:"title"`
		} `json:"explorer"`
	}
	if len(config) > 0 && json.Unmarshal(config, &envelope) == nil && strings.TrimSpace(envelope.Explorer.Title) != "" {
		return envelope.Explorer.Title
	}
	return "Explorer"
}

func migrationMetadata(options ExplorerAuthoringMigrationOptions, originalConfig []byte, source string) *explorer.MigrationMetadata {
	metadata := &explorer.MigrationMetadata{
		Kind:             "ExplorerAuthoringMigration",
		Source:           source,
		SourceProject:    projectid.Canonical(options.Project),
		SourceExplorerID: options.ExplorerID,
		OriginalConfig:   append([]byte(nil), originalConfig...),
		OriginalMapping:  append([]byte(nil), options.LegacyMapping...),
		Actor:            options.Actor,
		RequestID:        options.RequestID,
		MigratedAt:       time.Now().UTC(),
	}
	metadata.OriginalConfigDigest = rawDigest(originalConfig)
	metadata.OriginalMappingDigest = rawDigest(options.LegacyMapping)
	return metadata
}

func rawDigest(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func migrationRevisionID(receiptID string, config, mapping []byte) string {
	sum := sha256.Sum256([]byte(receiptID + "\x00" + rawDigest(config) + "\x00" + rawDigest(mapping)))
	return "authoring_migration_" + hex.EncodeToString(sum[:])
}
