package semantic

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/digest"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	Collection                     = "fhir_semantic_inventory"
	EntryCollection                = "fhir_semantic_inventory_entries"
	BuildCollection                = "fhir_semantic_inventory_builds"
	SchemaVersion                  = 1
	EntryIndexVersion              = 2
	SemanticObservationRuleVersion = RuleVersion

	EmitterVersion = 3

	SemanticObservationLegacyV1SchemaVersion = 3
	SemanticObservationLegacyV1RuleVersion   = 4
	PageLimit                                = 50
	SourceFile                               = "file"
	SourceRetained                           = "retained_vertex"
)

var ErrSemanticInventoryCursorMismatch = errors.New("semantic inventory cursor does not match request")

type State string

const (
	Unknown     State = "unknown"
	NotStarted  State = "not_started"
	Running     State = "running"
	Complete    State = "complete"
	Failed      State = "failed"
	Invalidated State = "invalidated"
)

const (
	SourceAvailabilityUnknown  = "unknown"
	SourceAvailabilityVerified = "verified"
	SourceAvailabilityUnproven = "unproven"
)

type Build struct {
	Key                string     `json:"_key"`
	Project            string     `json:"project"`
	DatasetGeneration  string     `json:"dataset_generation"`
	BuildID            string     `json:"build_id"`
	State              State      `json:"state"`
	ObservationSchema  int        `json:"observation_schema"`
	InventorySchema    int        `json:"inventory_schema"`
	EntryIndexVersion  int        `json:"entry_index_version,omitempty"`
	RuleVersion        int        `json:"rule_version"`
	SourceKind         string     `json:"source_kind,omitempty"`
	SourceAvailability string     `json:"source_availability,omitempty"`
	AuthResourcePath   string     `json:"auth_resource_path,omitempty"`
	ScannedResources   int64      `json:"scanned_resources"`
	Checkpoint         string     `json:"checkpoint,omitempty"`
	SourceCheckpoint   Checkpoint `json:"source_checkpoint,omitempty"`
	LeaseToken         string     `json:"lease_token,omitempty"`
	LeaseExpiresAt     int64      `json:"lease_expires_at,omitempty"`
	Diagnostics        []string   `json:"diagnostics,omitempty"`
}

type Checkpoint struct {
	Collection string `json:"collection,omitempty"`
	Key        string `json:"key,omitempty"`
}

type Contribution struct {
	Key               string      `json:"_key"`
	Project           string      `json:"project"`
	DatasetGeneration string      `json:"dataset_generation"`
	AuthResourcePath  string      `json:"auth_resource_path,omitempty"`
	ResourceType      string      `json:"resource_type"`
	BuildID           string      `json:"build_id"`
	SourceKind        string      `json:"source_kind,omitempty"`
	SourceID          string      `json:"source_id"`
	Ordinal           int         `json:"ordinal"`
	ConceptID         string      `json:"concept_id"`
	BindingID         string      `json:"binding_id"`
	Example           string      `json:"example,omitempty"`
	Observation       Observation `json:"observation"`
}

// Essential: spilled inventory runs need the original source-file and ordinal key contract.
type Entry struct {
	ConceptID   string      `json:"concept_id"`
	BindingID   string      `json:"binding_id"`
	Observation Observation `json:"observation"`
}

type SelectionMode string

const (
	ExactConcept          SelectionMode = "EXACT_CONCEPT"
	SystemVersionCategory SelectionMode = "SYSTEM_VERSION_CATEGORY"
)

type FamilyReference struct {
	FamilyID     string `json:"family_id"`
	BindingID    string `json:"binding_id"`
	SourceKind   string `json:"source_kind"`
	ResourceType string `json:"resource_type"`
	SourcePath   string `json:"source_path"`
	System       string `json:"system"`
	Version      string `json:"version"`
}

type Family struct {
	FamilyReference
	Source            Source       `json:"source"`
	KeySelector       string       `json:"key_selector"`
	Value             Value        `json:"value"`
	OwningScope       string       `json:"owning_scope,omitempty"`
	LogicalType       string       `json:"logical_type,omitempty"`
	SchemaVersion     int          `json:"schema_version"`
	RuleVersion       string       `json:"rule_version"`
	Completeness      Completeness `json:"completeness"`
	Status            string       `json:"status,omitempty"`
	Population        int64        `json:"population"`
	Examples          []string     `json:"examples,omitempty"`
	ExamplesTruncated bool         `json:"examples_truncated"`
}

func EntryForFamily(family Family) Entry {
	return Entry{
		ConceptID: family.FamilyID, BindingID: family.BindingID,
		Observation: Observation{
			SchemaVersion:     family.SchemaVersion,
			Source:            family.Source,
			Key:               Key{Selector: family.KeySelector, System: family.System, Version: family.Version},
			Value:             family.Value,
			OwningScope:       family.OwningScope,
			LogicalType:       family.LogicalType,
			Completeness:      family.Completeness,
			Status:            family.Status,
			Population:        family.Population,
			Examples:          append([]string(nil), family.Examples...),
			ExamplesTruncated: family.ExamplesTruncated,
			RuleHint:          RuleHintCodedCategoryV1,
			RuleVersion:       family.RuleVersion,
		},
	}
}

func FamilyID(reference FamilyReference) string {
	return digest.Digest("semantic-inventory-family/v1", reference.BindingID,
		reference.SourceKind, reference.ResourceType, reference.SourcePath, reference.System, reference.Version)
}

type PageOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	ResourceType                  string
	BindingID                     string
	SelectionMode                 SelectionMode
	Query                         string
	Cursor                        string
	Limit                         int
}

type Reference struct {
	ConceptID string `json:"concept_id"`
	BindingID string `json:"binding_id"`
}

type ResolveOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	References                    []Reference
	FamilyReferences              []FamilyReference
}

type ResolveResult struct {
	Build    Build
	State    State
	Entries  []Entry
	Families []Family
}

type Page struct {
	Entries    []Entry  `json:"entries"`
	Families   []Family `json:"families,omitempty"`
	Build      Build    `json:"build"`
	State      State    `json:"state"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type ObservationSink func(Contribution)

func BuildID(project, datasetGeneration string) string {
	return digest.Digest(
		"semantic-inventory-build/v1",
		project,
		strings.TrimSpace(datasetGeneration),
		strconv.Itoa(SchemaVersion),
		strconv.Itoa(ObservationSchemaVersion),
		strconv.Itoa(SemanticObservationRuleVersion),
		strconv.Itoa(EmitterVersion),
	)
}

func BuildKey(project, datasetGeneration string) string {
	return "sib_" + digest.Digest(
		"semantic-inventory-build-key/v1",
		project,
		strings.TrimSpace(datasetGeneration),
		BuildID(project, datasetGeneration),
	)
}

func NewBuild(project, datasetGeneration, authResourcePath string) Build {
	buildID := BuildID(project, datasetGeneration)
	return Build{
		Key:                BuildKey(project, datasetGeneration),
		Project:            project,
		DatasetGeneration:  strings.TrimSpace(datasetGeneration),
		BuildID:            buildID,
		State:              Running,
		ObservationSchema:  ObservationSchemaVersion,
		InventorySchema:    SchemaVersion,
		RuleVersion:        SemanticObservationRuleVersion,
		SourceKind:         SourceFile,
		SourceAvailability: SourceAvailabilityUnknown,
		AuthResourcePath:   authResourcePath,
	}
}

type Emitter struct {
	project           string
	datasetGeneration string
	buildID           string
}

func NewEmitter(project, datasetGeneration string) Emitter {
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	return Emitter{
		project:           project,
		datasetGeneration: datasetGeneration,
		buildID:           BuildID(project, datasetGeneration),
	}
}

func (e Emitter) buildIdentity() string {
	if e.buildID != "" {
		return e.buildID
	}
	return BuildID(e.project, e.datasetGeneration)
}

func SourceKind(kind string) string {
	if kind == SourceRetained {
		return kind
	}
	return SourceFile
}

func (e Emitter) ObservePayload(payload map[string]any, resourceType, authResourcePath, sourceID string, sink ObservationSink) error {
	if payload == nil || sourceID == "" || sink == nil {
		return nil
	}
	source := ContributionSource{
		Project: e.project, DatasetGeneration: e.datasetGeneration, BuildID: e.buildIdentity(),
		AuthResourcePath: authResourcePath, ResourceType: resourceType, Kind: SourceRetained,
	}
	profile := ProfileForPayload(payload)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated FHIR schema for semantic observations: %w", err)
	}
	ordinal := 0
	var extractor Extractor
	return extractor.Extract(index, resourceType, payload, profile, func(record Record) {
		sink(source.Contribution(sourceID, ordinal, record.Observation, record.Examples))
		ordinal++
	})
}

type ContributionSource struct {
	Project           string
	DatasetGeneration string
	BuildID           string
	AuthResourcePath  string
	ResourceType      string
	Kind              string
}

func (source ContributionSource) Contribution(sourceID string, ordinal int, observation Observation, examples []any) Contribution {
	observation.SchemaVersion = ObservationSchemaVersion
	observation.Population = 1
	observation.Examples = observation.Examples[:0]
	observation.ExamplesTruncated = false
	for _, example := range examples {
		if text, ok := SafeExample(example); ok {
			observation.Examples = append(observation.Examples, text)
			if len(observation.Examples) == maxContributionExamples {
				observation.ExamplesTruncated = true
				break
			}
		}
	}
	conceptID := ConceptID(observation)
	bindingID := BindingID(observation)
	example := ""
	if len(observation.Examples) > 0 {
		example = observation.Examples[0]
	}
	BuildID := source.BuildID
	keyParts := []string{
		"semantic-inventory-contribution/v1",
		source.Project,
		strings.TrimSpace(source.DatasetGeneration),
		source.AuthResourcePath,
		source.ResourceType,
		BuildID,
		sourceID,
		strconv.Itoa(ordinal),
	}
	if source.Kind == SourceRetained {
		keyParts = append(keyParts, source.Kind)
	}
	return Contribution{
		Key: digest.Digest(
			keyParts...,
		),
		Project:           source.Project,
		DatasetGeneration: strings.TrimSpace(source.DatasetGeneration),
		AuthResourcePath:  source.AuthResourcePath,
		ResourceType:      source.ResourceType,
		BuildID:           BuildID,
		SourceKind:        SourceKind(source.Kind),
		SourceID:          sourceID,
		Ordinal:           ordinal,
		ConceptID:         conceptID,
		BindingID:         bindingID,
		Example:           example,
		Observation:       observation,
	}
}

func ConceptID(observation Observation) string {
	if observation.Key.Code != "" {
		return digest.Digest(
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
	return digest.Digest(parts...)
}

func BindingID(observation Observation) string {
	parts := []string{
		"semantic-binding/v2",
		observation.Source.Type,
		observation.Source.Profile,
		observation.Source.Path,
		observation.Source.Canonical,
		observation.OwningScope,
		observation.Key.Selector,
		observation.Value.Selector,
		observation.Value.Type,
		strings.Join(observation.Value.FallbackSelectors, "\x1f"),
		strconv.FormatBool(observation.Value.FallbackOnBlankString),
		observation.ChoiceArm,
		observation.LogicalType,
		observation.RuleHint,
		observation.RuleVersion,
	}
	parts = append(parts, observation.ExtensionURLPath...)
	return digest.Digest(parts...)
}

func LegacySemanticBindingIDV1(observation Observation) string {
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
		strconv.Itoa(SemanticObservationLegacyV1RuleVersion),
	}
	parts = append(parts, observation.ExtensionURLPath...)
	return digest.Digest(parts...)
}

func ValidateBuild(build Build) error {
	if strings.TrimSpace(build.Project) == "" || strings.TrimSpace(build.DatasetGeneration) == "" {
		return fmt.Errorf("semantic inventory build requires project and dataset generation")
	}
	if build.BuildID != BuildID(build.Project, build.DatasetGeneration) {
		return fmt.Errorf("semantic inventory build identity does not match its generation and extractor version")
	}
	if build.Key != BuildKey(build.Project, build.DatasetGeneration) {
		return fmt.Errorf("semantic inventory build key does not match its identity")
	}
	return nil
}

const maxContributionExamples = 32
