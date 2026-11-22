package semantic

import (
	"encoding/binary"
	"errors"
	"sort"
	"strings"
)

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
	Completeness          Completeness
	Status                string
	RuleHint              string
	RuleVersion           string
}

type Aggregator struct {
	project           string
	datasetGeneration string
	buildID           string
	authResourcePath  string
	resourceType      string
	sourceID          string
	contributions     map[semanticInventoryAggregateKey]Contribution
}

func NewAggregator(project, datasetGeneration, authResourcePath, resourceType, sourceID string) *Aggregator {
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	return &Aggregator{
		project:           project,
		datasetGeneration: datasetGeneration,
		buildID:           BuildID(project, datasetGeneration),
		authResourcePath:  authResourcePath,
		resourceType:      resourceType,
		sourceID:          sourceID,
		contributions:     make(map[semanticInventoryAggregateKey]Contribution),
	}
}

func (a *Aggregator) Add(contribution Contribution) error {
	if a == nil || a.contributions == nil {
		return errors.New("semantic inventory aggregator is not initialized")
	}
	if contribution.Project != a.project || strings.TrimSpace(contribution.DatasetGeneration) != a.datasetGeneration ||
		contribution.BuildID != a.buildID || contribution.AuthResourcePath != a.authResourcePath ||
		contribution.ResourceType != a.resourceType || SourceKind(contribution.SourceKind) != SourceFile {
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
		contribution.SourceKind = SourceFile
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

func semanticInventoryContributionAggregateKey(contribution Contribution) semanticInventoryAggregateKey {
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

func (a *Aggregator) Reset() {
	if a != nil {
		a.contributions = make(map[semanticInventoryAggregateKey]Contribution)
	}
}

func (a *Aggregator) Values() []Contribution {
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
	values := make([]Contribution, 0, len(keys))
	for _, key := range keys {
		values = append(values, a.contributions[key.key])
	}
	return values
}

// Essential: spill runs must use the same exact identity and order as the in-memory reducer.
func ContributionOrderKey(contribution Contribution) string {
	return semanticInventoryAggregateSortKey(semanticInventoryContributionAggregateKey(contribution))
}

// Essential: spill runs must merge equal reducer inputs before assigning retry-stable keys.
func MergeContributions(left, right Contribution) (Contribution, bool) {
	if semanticInventoryContributionAggregateKey(left) != semanticInventoryContributionAggregateKey(right) {
		return Contribution{}, false
	}
	left.Observation.Population += right.Observation.Population
	left.Observation.ExamplesTruncated = left.Observation.ExamplesTruncated || right.Observation.ExamplesTruncated
	return left, true
}

func (a *Aggregator) Documents() []Contribution {
	if a == nil || len(a.contributions) == 0 {
		return nil
	}
	values := a.Values()
	documents := make([]Contribution, 0, len(values))
	for ordinal, contribution := range values {
		contribution.Ordinal = ordinal
		contribution.Key = fileContributionKey(a.project, a.datasetGeneration, a.authResourcePath,
			a.resourceType, a.buildID, a.sourceID, ordinal)
		documents = append(documents, contribution)
	}
	return documents
}
