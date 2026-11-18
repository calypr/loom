package catalog

import (
	"encoding/base64"
	"encoding/binary"
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

	SemanticInventoryEmitterVersion = 3

	SemanticObservationLegacyV1SchemaVersion = 3
	SemanticObservationLegacyV1RuleVersion   = 4
	SemanticInventoryPageLimit               = 50
	SemanticInventorySourceFile              = "file"
	SemanticInventorySourceRetained          = "retained_vertex"
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

type SemanticInventoryCheckpoint struct {
	Collection string `json:"collection,omitempty"`
	Key        string `json:"key,omitempty"`
}

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

type semanticInventoryAggregateKey struct {
	AuthResourcePath      string
	ResourceType          string
	BindingID             string
	ConceptID             string
	Example               string
	Canonical             string
	SourceType            string
	Profile               string
	Path                  string
	KeySelector           string
	System                string
	Version               string
	Code                  string
	Display               string
	ValueSelector         string
	ValueType             string
	FallbackSelectors     string
	FallbackOnBlankString bool
	OwningScope           string
	ExtensionURLPath      string
	ChoiceArm             string
	LogicalType           string
	ObservedUnits         string
	Completeness          SemanticObservationCompleteness
	Status                string
	RuleHint              string
	RuleVersion           string
}

type SemanticInventoryAggregator struct {
	project           string
	datasetGeneration string
	buildID           string
	authResourcePath  string
	resourceType      string
	sourceID          string
	contributions     map[semanticInventoryAggregateKey]SemanticInventoryContribution
}

func NewSemanticInventoryAggregator(project, datasetGeneration, authResourcePath, resourceType, sourceID string) *SemanticInventoryAggregator {
	datasetGeneration = NormalizeDatasetGeneration(datasetGeneration)
	return &SemanticInventoryAggregator{
		project:           project,
		datasetGeneration: datasetGeneration,
		buildID:           SemanticInventoryBuildID(project, datasetGeneration),
		authResourcePath:  authResourcePath,
		resourceType:      resourceType,
		sourceID:          sourceID,
		contributions:     make(map[semanticInventoryAggregateKey]SemanticInventoryContribution),
	}
}

func (a *SemanticInventoryAggregator) Add(contribution SemanticInventoryContribution) error {
	if a == nil || a.contributions == nil {
		return errors.New("semantic inventory aggregator is not initialized")
	}
	if contribution.Project != a.project || NormalizeDatasetGeneration(contribution.DatasetGeneration) != a.datasetGeneration ||
		contribution.BuildID != a.buildID || contribution.AuthResourcePath != a.authResourcePath ||
		contribution.ResourceType != a.resourceType || semanticInventorySourceKind(contribution.SourceKind) != SemanticInventorySourceFile {
		return errors.New("semantic inventory contribution does not match aggregator identity")
	}
	observation := contribution.Observation
	population := observation.Population
	truncated := observation.ExamplesTruncated
	observation.Population = 0
	observation.ExamplesTruncated = false
	identity := semanticInventoryContributionAggregateKey(contribution)
	current, exists := a.contributions[identity]
	if !exists {
		contribution.Project = a.project
		contribution.DatasetGeneration = a.datasetGeneration
		contribution.BuildID = a.buildID
		contribution.SourceID = a.sourceID
		contribution.SourceKind = SemanticInventorySourceFile
		contribution.Ordinal = 0
		contribution.Key = ""
		a.contributions[identity] = contribution
		return nil
	}
	current.Observation.Population += population
	current.Observation.ExamplesTruncated = current.Observation.ExamplesTruncated || truncated
	a.contributions[identity] = current
	return nil
}

func semanticInventoryContributionAggregateKey(contribution SemanticInventoryContribution) semanticInventoryAggregateKey {
	observation := contribution.Observation
	return semanticInventoryAggregateKey{
		AuthResourcePath: contribution.AuthResourcePath, ResourceType: contribution.ResourceType,
		BindingID: contribution.BindingID, ConceptID: contribution.ConceptID, Example: contribution.Example,
		Canonical: observation.Source.Canonical, SourceType: observation.Source.Type, Profile: observation.Source.Profile, Path: observation.Source.Path,
		KeySelector: observation.Key.Selector, System: observation.Key.System, Version: observation.Key.Version,
		Code: observation.Key.Code, Display: observation.Key.Display, ValueSelector: observation.Value.Selector,
		ValueType: observation.Value.Type, FallbackSelectors: semanticInventoryStringSliceKey(observation.Value.FallbackSelectors),
		FallbackOnBlankString: observation.Value.FallbackOnBlankString, OwningScope: observation.OwningScope,
		ExtensionURLPath: semanticInventoryStringSliceKey(observation.ExtensionURLPath), ChoiceArm: observation.ChoiceArm,
		LogicalType: observation.LogicalType, ObservedUnits: semanticInventoryStringSliceKey(observation.ObservedUnits),
		Completeness: observation.Completeness, Status: observation.Status, RuleHint: observation.RuleHint,
		RuleVersion: observation.RuleVersion,
	}
}

func semanticInventoryStringSliceKey(values []string) string {
	if len(values) == 0 {
		return "\x00"
	}
	key := make([]byte, 1, 1+len(values)*8)
	key[0] = 1
	for _, value := range values {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		key = append(key, size[:]...)
		key = append(key, value...)
	}
	return string(key)
}

func semanticInventoryAggregateSortKey(key semanticInventoryAggregateKey) string {
	values := [...]string{
		key.AuthResourcePath, key.ResourceType, key.BindingID, key.ConceptID, key.Example,
		key.Canonical, key.SourceType, key.Profile, key.Path, key.KeySelector, key.System, key.Version,
		key.Code, key.Display, key.ValueSelector, key.ValueType, key.FallbackSelectors, key.OwningScope,
		key.ExtensionURLPath, key.ChoiceArm, key.LogicalType, key.ObservedUnits, string(key.Completeness),
		key.Status, key.RuleHint, key.RuleVersion,
	}
	encoded := make([]byte, 0, len(values)*16)
	for _, value := range values {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
		encoded = append(encoded, size[:]...)
		encoded = append(encoded, value...)
	}
	if key.FallbackOnBlankString {
		return string(append(encoded, 1))
	}
	return string(append(encoded, 0))
}

func (a *SemanticInventoryAggregator) Merge(other *SemanticInventoryAggregator) error {
	if other == nil {
		return nil
	}
	for _, contribution := range other.contributions {
		if err := a.Add(contribution); err != nil {
			return err
		}
	}
	return nil
}

func (a *SemanticInventoryAggregator) Len() int {
	if a == nil {
		return 0
	}
	return len(a.contributions)
}

func (a *SemanticInventoryAggregator) Reset() {
	if a != nil {
		a.contributions = make(map[semanticInventoryAggregateKey]SemanticInventoryContribution)
	}
}

func (a *SemanticInventoryAggregator) Values() []SemanticInventoryContribution {
	if a == nil || len(a.contributions) == 0 {
		return nil
	}
	type orderedContribution struct {
		identity string
		key      semanticInventoryAggregateKey
	}
	keys := make([]orderedContribution, 0, len(a.contributions))
	for key := range a.contributions {
		keys = append(keys, orderedContribution{identity: semanticInventoryAggregateSortKey(key), key: key})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].identity < keys[j].identity })
	values := make([]SemanticInventoryContribution, 0, len(keys))
	for _, key := range keys {
		values = append(values, a.contributions[key.key])
	}
	return values
}

// Essential: spill runs must use the same exact identity and order as the in-memory reducer.
func SemanticInventoryContributionOrderKey(contribution SemanticInventoryContribution) string {
	return semanticInventoryAggregateSortKey(semanticInventoryContributionAggregateKey(contribution))
}

// Essential: spill runs must merge equal reducer inputs before assigning retry-stable keys.
func MergeSemanticInventoryContributions(left, right SemanticInventoryContribution) (SemanticInventoryContribution, bool) {
	if semanticInventoryContributionAggregateKey(left) != semanticInventoryContributionAggregateKey(right) {
		return SemanticInventoryContribution{}, false
	}
	left.Observation.Population += right.Observation.Population
	left.Observation.ExamplesTruncated = left.Observation.ExamplesTruncated || right.Observation.ExamplesTruncated
	return left, true
}

func (a *SemanticInventoryAggregator) Documents() []SemanticInventoryContribution {
	if a == nil || len(a.contributions) == 0 {
		return nil
	}
	values := a.Values()
	documents := make([]SemanticInventoryContribution, 0, len(values))
	for ordinal, contribution := range values {
		contribution.Ordinal = ordinal
		contribution.Key = semanticInventoryFileContributionKey(a.project, a.datasetGeneration, a.authResourcePath,
			a.resourceType, a.buildID, a.sourceID, ordinal)
		documents = append(documents, contribution)
	}
	return documents
}

func semanticInventoryFileContributionKey(project, datasetGeneration, authResourcePath, resourceType, buildID, sourceID string, ordinal int) string {
	return catalogIdentityDigest("semantic-inventory-contribution/v1", project, datasetGeneration,
		authResourcePath, resourceType, buildID, sourceID, strconv.Itoa(ordinal))
}

// Essential: spilled inventory runs need the original source-file and ordinal key contract.
func SemanticInventoryContributionKey(project, datasetGeneration, authResourcePath, resourceType, buildID, sourceID string, ordinal int) string {
	return semanticInventoryFileContributionKey(project, datasetGeneration, authResourcePath, resourceType, buildID, sourceID, ordinal)
}

type SemanticInventoryEntry struct {
	ConceptID   string              `json:"concept_id"`
	BindingID   string              `json:"binding_id"`
	Observation SemanticObservation `json:"observation"`
}

type SemanticInventorySelectionMode string

const (
	SemanticInventoryExactConcept          SemanticInventorySelectionMode = "EXACT_CONCEPT"
	SemanticInventorySystemVersionCategory SemanticInventorySelectionMode = "SYSTEM_VERSION_CATEGORY"
)

type SemanticInventoryFamilyReference struct {
	FamilyID     string `json:"family_id"`
	BindingID    string `json:"binding_id"`
	SourceKind   string `json:"source_kind"`
	ResourceType string `json:"resource_type"`
	SourcePath   string `json:"source_path"`
	System       string `json:"system"`
	Version      string `json:"version"`
}

type SemanticInventoryFamily struct {
	SemanticInventoryFamilyReference
	Source            SemanticObservationSource       `json:"source"`
	KeySelector       string                          `json:"key_selector"`
	Value             SemanticObservationValue        `json:"value"`
	OwningScope       string                          `json:"owning_scope,omitempty"`
	LogicalType       string                          `json:"logical_type,omitempty"`
	SchemaVersion     int                             `json:"schema_version"`
	RuleVersion       string                          `json:"rule_version"`
	Completeness      SemanticObservationCompleteness `json:"completeness"`
	Status            string                          `json:"status,omitempty"`
	Population        int64                           `json:"population"`
	Examples          []string                        `json:"examples,omitempty"`
	ExamplesTruncated bool                            `json:"examples_truncated"`
}

func SemanticInventoryFamilyID(reference SemanticInventoryFamilyReference) string {
	return catalogIdentityDigest("semantic-inventory-family/v1", reference.BindingID,
		reference.SourceKind, reference.ResourceType, reference.SourcePath, reference.System, reference.Version)
}

type SemanticInventoryPageOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	ResourceType                  string
	BindingID                     string
	SelectionMode                 SemanticInventorySelectionMode
	Query                         string
	Cursor                        string
	Limit                         int
}

type SemanticInventoryReference struct {
	ConceptID string `json:"concept_id"`
	BindingID string `json:"binding_id"`
}

type SemanticInventoryResolveOptions struct {
	Project                       string
	DatasetGeneration             string
	AuthResourcePathsUnrestricted *bool
	AuthResourcePaths             []string
	References                    []SemanticInventoryReference
	FamilyReferences              []SemanticInventoryFamilyReference
}

type SemanticInventoryResolveResult struct {
	Build    SemanticInventoryBuild
	State    SemanticInventoryState
	Entries  []SemanticInventoryEntry
	Families []SemanticInventoryFamily
}

type SemanticInventoryPage struct {
	Entries    []SemanticInventoryEntry  `json:"entries"`
	Families   []SemanticInventoryFamily `json:"families,omitempty"`
	Build      SemanticInventoryBuild    `json:"build"`
	State      SemanticInventoryState    `json:"state"`
	NextCursor string                    `json:"next_cursor,omitempty"`
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
		strconv.Itoa(SemanticInventoryEmitterVersion),
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

type SemanticInventoryEmitter struct {
	project           string
	datasetGeneration string
	buildID           string
}

func NewSemanticInventoryEmitter(project, datasetGeneration string) SemanticInventoryEmitter {
	datasetGeneration = NormalizeDatasetGeneration(datasetGeneration)
	return SemanticInventoryEmitter{
		project:           project,
		datasetGeneration: datasetGeneration,
		buildID:           SemanticInventoryBuildID(project, datasetGeneration),
	}
}

func (e SemanticInventoryEmitter) semanticBuildID() string {
	if e.buildID != "" {
		return e.buildID
	}
	return SemanticInventoryBuildID(e.project, e.datasetGeneration)
}

func semanticInventorySourceKind(kind string) string {
	if kind == SemanticInventorySourceRetained {
		return kind
	}
	return SemanticInventorySourceFile
}

func (e SemanticInventoryEmitter) ObservePayload(payload map[string]any, resourceType, authResourcePath, sourceID string, sink SemanticInventoryObservationSink) error {
	if payload == nil || sourceID == "" || sink == nil {
		return nil
	}
	p := Profiler{
		project:                  e.project,
		datasetGeneration:        e.datasetGeneration,
		semanticInventoryBuildID: e.semanticBuildID(),
		authResourcePath:         authResourcePath,
		resourceType:             resourceType,
		semanticOnly:             true,
		semanticSourceKind:       SemanticInventorySourceRetained,
	}
	return p.observeSemanticObservations(payload, sourceID, sink)
}

func (e SemanticInventoryEmitter) ObservePayloadWithCoverage(
	payload map[string]any,
	resourceType string,
	authResourcePath string,
	sourceID string,
	schemaIdentity string,
	semanticSink SemanticInventoryObservationSink,
	emissionSink SemanticCoverageEmissionSink,
	coverageSink SemanticCoverageSourceSink,
) error {
	if payload == nil {
		return nil
	}
	if sourceID == "" {
		return fmt.Errorf("semantic coverage source ID is required")
	}
	if coverageSink == nil {
		return fmt.Errorf("semantic coverage source sink is required")
	}
	p := Profiler{
		project:                  e.project,
		datasetGeneration:        e.datasetGeneration,
		semanticInventoryBuildID: e.semanticBuildID(),
		authResourcePath:         authResourcePath,
		resourceType:             resourceType,
		semanticOnly:             true,
		semanticSourceKind:       SemanticInventorySourceRetained,
	}
	rawPaths := make(map[string]struct{})
	for _, field := range buildShapePlan(payload).fields {
		rawPaths[field.Path] = struct{}{}
	}
	return p.observeSemanticObservationsWithCoverage(payload, sourceID, schemaIdentity, semanticSink, emissionSink, coverageSink, rawPaths)
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
	buildID := p.semanticInventoryBuildID
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
	return catalogIdentityDigest(parts...)
}

func LegacySemanticBindingIDV1(observation SemanticObservation) string {
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
		opts.BindingID,
		opts.Query,
		strconv.Itoa(limit),
	}

	if opts.SelectionMode != "" && opts.SelectionMode != SemanticInventoryExactConcept {
		parts[0] = "semantic-inventory-page/" + string(opts.SelectionMode) + "/v1"
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
