package catalog

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	SemanticInventoryCollection        = "fhir_semantic_inventory"
	SemanticInventoryEntryCollection   = "fhir_semantic_inventory_entries"
	SemanticInventoryBuildCollection   = "fhir_semantic_inventory_builds"
	SemanticInventorySchemaVersion     = 1
	SemanticInventoryEntryIndexVersion = 2
	SemanticObservationRuleVersion     = 5
	SemanticInventoryPageLimit         = 50
	SemanticInventorySourceFile        = "file"
	SemanticInventorySourceRetained    = "retained_vertex"
)

const SemanticRuleHintCodedValueV1 = "CODED_VALUE_V1"

var ErrSemanticInventoryCursorMismatch = errors.New("semantic inventory cursor does not match request")

type SemanticInventoryState string

const (
	SemanticInventoryUnknown     SemanticInventoryState = "unknown"
	SemanticInventoryNotStarted  SemanticInventoryState = "not_started"
	SemanticInventoryRunning     SemanticInventoryState = "running"
	SemanticInventoryComplete    SemanticInventoryState = "complete"
	SemanticInventoryFailed      SemanticInventoryState = "failed"
	SemanticInventoryInvalidated SemanticInventoryState = "invalidated"
)

const (
	SemanticInventorySourceAvailabilityUnknown  = "unknown"
	SemanticInventorySourceAvailabilityVerified = "verified"
	SemanticInventorySourceAvailabilityUnproven = "unproven"
)

// SemanticInventoryBuild records completeness for one immutable source generation.
type SemanticInventoryBuild struct {
	Key                string                      `json:"_key"`
	Project            string                      `json:"project"`
	DatasetGeneration  string                      `json:"dataset_generation"`
	BuildID            string                      `json:"build_id"`
	State              SemanticInventoryState      `json:"state"`
	ObservationSchema  int                         `json:"observation_schema"`
	InventorySchema    int                         `json:"inventory_schema"`
	EntryIndexVersion  int                         `json:"entry_index_version,omitempty"`
	RuleVersion        int                         `json:"rule_version"`
	SourceKind         string                      `json:"source_kind,omitempty"`
	SourceAvailability string                      `json:"source_availability,omitempty"`
	AuthResourcePath   string                      `json:"auth_resource_path,omitempty"`
	ScannedResources   int64                       `json:"scanned_resources"`
	Checkpoint         string                      `json:"checkpoint,omitempty"`
	SourceCheckpoint   SemanticInventoryCheckpoint `json:"source_checkpoint,omitempty"`
	LeaseToken         string                      `json:"lease_token,omitempty"`
	LeaseExpiresAt     int64                       `json:"lease_expires_at,omitempty"`
	Diagnostics        []string                    `json:"diagnostics,omitempty"`
}

// SemanticInventoryCheckpoint is a typed keyset cursor over immutable retained
// resource collections. Key is scoped to Collection and advances only after
// its source contributions have been persisted.
type SemanticInventoryCheckpoint struct {
	Collection string `json:"collection,omitempty"`
	Key        string `json:"key,omitempty"`
}

// SemanticInventoryContribution is one semantic-emitter event tied to a
// stable source record locator. Replaying a generation replaces the same key.
type SemanticInventoryContribution struct {
	Key               string              `json:"_key"`
	Project           string              `json:"project"`
	DatasetGeneration string              `json:"dataset_generation"`
	AuthResourcePath  string              `json:"auth_resource_path,omitempty"`
	ResourceType      string              `json:"resource_type"`
	BuildID           string              `json:"build_id"`
	SourceKind        string              `json:"source_kind,omitempty"`
	SourceID          string              `json:"source_id"`
	Ordinal           int                 `json:"ordinal"`
	ConceptID         string              `json:"concept_id"`
	BindingID         string              `json:"binding_id"`
	Example           string              `json:"example,omitempty"`
	Observation       SemanticObservation `json:"observation"`
}

type SemanticInventoryEntry struct {
	ConceptID   string              `json:"concept_id"`
	BindingID   string              `json:"binding_id"`
	Observation SemanticObservation `json:"observation"`
}

type SemanticInventoryPageOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	ResourceType                  string
	Query                         string
	Cursor                        string
	Limit                         int
}

// SemanticInventoryReference identifies one immutable catalog row without
// including observed payload or caller-chosen storage identities.
type SemanticInventoryReference struct {
	ConceptID string `json:"concept_id"`
	BindingID string `json:"binding_id"`
}

// SemanticInventoryResolveOptions is the bounded, authorization-scoped lookup
// used when a user applies selected catalog rows to an authoring workspace.
type SemanticInventoryResolveOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	References                    []SemanticInventoryReference
}

type SemanticInventoryResolveResult struct {
	Build   SemanticInventoryBuild
	State   SemanticInventoryState
	Entries []SemanticInventoryEntry
}

type SemanticInventoryPage struct {
	Entries    []SemanticInventoryEntry `json:"entries"`
	Build      SemanticInventoryBuild   `json:"build"`
	State      SemanticInventoryState   `json:"state"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type SemanticInventoryObservationSink func(SemanticInventoryContribution)

type semanticInventoryCursor struct {
	Version       int    `json:"v"`
	RequestDigest string `json:"r"`
	BindingID     string `json:"b"`
	ConceptID     string `json:"c"`
}

func SemanticInventoryBuildID(project, datasetGeneration string) string {
	return catalogIdentityDigest(
		"semantic-inventory-build/v1",
		project,
		NormalizeDatasetGeneration(datasetGeneration),
		strconv.Itoa(SemanticInventorySchemaVersion),
		strconv.Itoa(SemanticObservationSchemaVersion),
		strconv.Itoa(SemanticObservationRuleVersion),
	)
}

func SemanticInventoryBuildKey(project, datasetGeneration string) string {
	return "sib_" + catalogIdentityDigest(
		"semantic-inventory-build-key/v1",
		project,
		NormalizeDatasetGeneration(datasetGeneration),
		SemanticInventoryBuildID(project, datasetGeneration),
	)
}

func NewSemanticInventoryBuild(project, datasetGeneration, authResourcePath string) SemanticInventoryBuild {
	buildID := SemanticInventoryBuildID(project, datasetGeneration)
	return SemanticInventoryBuild{
		Key:                SemanticInventoryBuildKey(project, datasetGeneration),
		Project:            project,
		DatasetGeneration:  NormalizeDatasetGeneration(datasetGeneration),
		BuildID:            buildID,
		State:              SemanticInventoryRunning,
		ObservationSchema:  SemanticObservationSchemaVersion,
		InventorySchema:    SemanticInventorySchemaVersion,
		RuleVersion:        SemanticObservationRuleVersion,
		SourceKind:         SemanticInventorySourceFile,
		SourceAvailability: SemanticInventorySourceAvailabilityUnknown,
		AuthResourcePath:   authResourcePath,
	}
}

// SemanticInventoryEmitter runs the same semantic walker as the profiler but
// does not retain field summaries or per-scope maps. It is intended for
// bounded scans of already-retained resources.
type SemanticInventoryEmitter struct {
	project           string
	datasetGeneration string
}

func NewSemanticInventoryEmitter(project, datasetGeneration string) SemanticInventoryEmitter {
	return SemanticInventoryEmitter{project: project, datasetGeneration: NormalizeDatasetGeneration(datasetGeneration)}
}

func semanticInventorySourceKind(kind string) string {
	if kind == SemanticInventorySourceRetained {
		return kind
	}
	return SemanticInventorySourceFile
}

func (e SemanticInventoryEmitter) ObservePayload(payload map[string]any, resourceType, authResourcePath, sourceID string, sink SemanticInventoryObservationSink) {
	if payload == nil || sourceID == "" || sink == nil {
		return
	}
	p := Profiler{
		project:            e.project,
		datasetGeneration:  e.datasetGeneration,
		authResourcePath:   authResourcePath,
		resourceType:       resourceType,
		semanticOnly:       true,
		semanticSourceKind: SemanticInventorySourceRetained,
	}
	p.observeSemanticObservations(payload, sourceID, sink)
}

func (p *Profiler) semanticInventoryContribution(sourceID string, ordinal int, observation SemanticObservation, examples []any) SemanticInventoryContribution {
	observation.SchemaVersion = SemanticObservationSchemaVersion
	observation.Population = 1
	observation.Examples = observation.Examples[:0]
	observation.ExamplesTruncated = false
	for _, example := range examples {
		if text, ok := safeSemanticExample(example); ok {
			observation.Examples = append(observation.Examples, text)
			if len(observation.Examples) == maxSemanticExamples {
				observation.ExamplesTruncated = true
				break
			}
		}
	}
	conceptID := semanticConceptID(observation)
	bindingID := semanticBindingID(observation)
	example := ""
	if len(observation.Examples) > 0 {
		example = observation.Examples[0]
	}
	buildID := SemanticInventoryBuildID(p.project, p.datasetGeneration)
	keyParts := []string{
		"semantic-inventory-contribution/v1",
		p.project,
		NormalizeDatasetGeneration(p.datasetGeneration),
		p.authResourcePath,
		p.resourceType,
		buildID,
		sourceID,
		strconv.Itoa(ordinal),
	}
	if p.semanticSourceKind == SemanticInventorySourceRetained {
		keyParts = append(keyParts, p.semanticSourceKind)
	}
	return SemanticInventoryContribution{
		Key: catalogIdentityDigest(
			keyParts...,
		),
		Project:           p.project,
		DatasetGeneration: NormalizeDatasetGeneration(p.datasetGeneration),
		AuthResourcePath:  p.authResourcePath,
		ResourceType:      p.resourceType,
		BuildID:           buildID,
		SourceKind:        semanticInventorySourceKind(p.semanticSourceKind),
		SourceID:          sourceID,
		Ordinal:           ordinal,
		ConceptID:         conceptID,
		BindingID:         bindingID,
		Example:           example,
		Observation:       observation,
	}
}

func semanticConceptID(observation SemanticObservation) string {
	if observation.Key.Code != "" {
		return catalogIdentityDigest(
			"semantic-concept/coding/v1",
			observation.Key.System,
			observation.Key.Code,
			observation.Key.Version,
		)
	}
	parts := []string{
		"semantic-concept/structural/v1",
		observation.Source.Type,
		observation.Source.Path,
		observation.Key.Selector,
		observation.Key.System,
	}
	parts = append(parts, observation.ExtensionURLPath...)
	return catalogIdentityDigest(parts...)
}

func semanticBindingID(observation SemanticObservation) string {
	parts := []string{
		"semantic-binding/v1",
		observation.Source.Type,
		observation.Source.Profile,
		observation.Source.Path,
		observation.Source.Canonical,
		observation.OwningScope,
		observation.Key.Selector,
		observation.Value.Selector,
		observation.Value.Type,
		observation.ChoiceArm,
		observation.LogicalType,
		observation.RuleHint,
		observation.RuleVersion,
	}
	parts = append(parts, observation.ExtensionURLPath...)
	return catalogIdentityDigest(parts...)
}

func SemanticInventoryPageDigest(opts SemanticInventoryPageOptions, buildID string, limit int) string {
	paths := append([]string(nil), opts.AuthResourcePaths...)
	sort.Strings(paths)
	paths = compactStrings(paths)
	unrestricted := EffectiveAuthResourcePathsUnrestricted(paths, opts.AuthResourcePathsUnrestricted)
	parts := []string{
		"semantic-inventory-page/v1",
		opts.Project,
		NormalizeDatasetGeneration(opts.DatasetGeneration),
		buildID,
		strconv.FormatBool(unrestricted),
		opts.ResourceType,
		opts.Query,
		strconv.Itoa(limit),
	}
	parts = append(parts, paths...)
	return catalogIdentityDigest(parts...)
}

func EncodeSemanticInventoryCursor(opts SemanticInventoryPageOptions, buildID, bindingID, conceptID string) string {
	limit := semanticInventoryLimit(opts.Limit)
	cursor := semanticInventoryCursor{
		Version:       1,
		RequestDigest: SemanticInventoryPageDigest(opts, buildID, limit),
		BindingID:     bindingID,
		ConceptID:     conceptID,
	}
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func DecodeSemanticInventoryCursor(raw string, opts SemanticInventoryPageOptions, buildID string) (string, string, error) {
	if raw == "" {
		return "", "", nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	var cursor semanticInventoryCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.Version != 1 {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	limit := semanticInventoryLimit(opts.Limit)
	if cursor.RequestDigest != SemanticInventoryPageDigest(opts, buildID, limit) || cursor.BindingID == "" || cursor.ConceptID == "" {
		return "", "", ErrSemanticInventoryCursorMismatch
	}
	return cursor.BindingID, cursor.ConceptID, nil
}

func semanticInventoryLimit(limit int) int {
	if limit <= 0 || limit > SemanticInventoryPageLimit {
		return SemanticInventoryPageLimit
	}
	return limit
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] != values[write-1] {
			values[write] = values[read]
			write++
		}
	}
	return values[:write]
}

func ValidateSemanticInventoryBuild(build SemanticInventoryBuild) error {
	if strings.TrimSpace(build.Project) == "" || strings.TrimSpace(build.DatasetGeneration) == "" {
		return fmt.Errorf("semantic inventory build requires project and dataset generation")
	}
	if build.BuildID != SemanticInventoryBuildID(build.Project, build.DatasetGeneration) {
		return fmt.Errorf("semantic inventory build identity does not match its generation and extractor version")
	}
	if build.Key != SemanticInventoryBuildKey(build.Project, build.DatasetGeneration) {
		return fmt.Errorf("semantic inventory build key does not match its identity")
	}
	return nil
}
