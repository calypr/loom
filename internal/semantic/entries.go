package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

const (
	entryCanonicalMissing uint32 = 1 << iota
	entryProfileMissing
	entryPathMissing
	entryKeySelectorMissing
	entrySystemMissing
	entryVersionMissing
	entryCodeMissing
	entryDisplayMissing
	entryValueSelectorMissing
	entryValueTypeMissing
	entryFallbackSelectorsMissing
	entryFallbackOnBlankMissing
	entryOwningScopeMissing
	entryExtensionURLPathMissing
	entryChoiceArmMissing
	entryLogicalTypeMissing
	entryUnitsMinimumMissing
	entryUnitsMaximumMissing
	entryCompletenessMissing
	entryStatusMissing
	entryRuleHintMissing
	entryRuleVersionMissing
	entryExampleMissing
)

type EntryPartial struct {
	Contribution     Contribution `json:"contribution"`
	ObservedUnitsMax []string     `json:"observed_units_max,omitempty"`
	Missing          uint32       `json:"missing"`
}

type semanticInventoryEntryGroupKey struct {
	AuthResourcePath string
	ResourceType     string
	BindingID        string
	ConceptID        string
}

type EntryAggregator struct {
	project           string
	datasetGeneration string
	buildID           string
	authResourcePath  string
	resourceType      string
	sourceKind        string
	entries           map[semanticInventoryEntryGroupKey]EntryPartial
}

func NewEntryAggregator(project, generation, authPath, resourceType, sourceKind string) *EntryAggregator {
	generation = strings.TrimSpace(generation)
	if sourceKind == "" {
		sourceKind = SourceFile
	}
	return &EntryAggregator{
		project: project, datasetGeneration: generation, buildID: BuildID(project, generation),
		authResourcePath: authPath, resourceType: resourceType, sourceKind: sourceKind,
		entries: make(map[semanticInventoryEntryGroupKey]EntryPartial),
	}
}

func (a *EntryAggregator) Add(value Contribution) error {
	if a == nil || a.entries == nil {
		return errorsNewEntryAggregatorNotInitialized
	}
	if value.Project != a.project || strings.TrimSpace(value.DatasetGeneration) != a.datasetGeneration ||
		value.BuildID != a.buildID || value.AuthResourcePath != a.authResourcePath || value.ResourceType != a.resourceType ||
		SourceKind(value.SourceKind) != a.sourceKind {
		return errorsNewEntryIdentityMismatch
	}
	partial := newSemanticInventoryEntryPartial(value)
	key := semanticInventoryEntryGroupKeyFor(value)
	if current, found := a.entries[key]; found {
		merged, _ := MergeEntryPartials(current, partial)
		a.entries[key] = merged
	} else {
		a.entries[key] = partial
	}
	return nil
}

func (a *EntryAggregator) AddPartial(value EntryPartial) error {
	if a == nil || a.entries == nil {
		return errorsNewEntryAggregatorNotInitialized
	}
	contribution := value.Contribution
	if contribution.Project != a.project || strings.TrimSpace(contribution.DatasetGeneration) != a.datasetGeneration ||
		contribution.BuildID != a.buildID || contribution.AuthResourcePath != a.authResourcePath || contribution.ResourceType != a.resourceType ||
		SourceKind(contribution.SourceKind) != a.sourceKind {
		return errorsNewEntryIdentityMismatch
	}
	key := semanticInventoryEntryGroupKeyFor(contribution)
	if current, found := a.entries[key]; found {
		merged, _ := MergeEntryPartials(current, value)
		a.entries[key] = merged
	} else {
		a.entries[key] = value
	}
	return nil
}

var (
	errorsNewEntryAggregatorNotInitialized = errors.New("semantic inventory entry aggregator is not initialized")
	errorsNewEntryIdentityMismatch         = errors.New("semantic inventory entry contribution does not match aggregator identity")
)

func newSemanticInventoryEntryPartial(value Contribution) EntryPartial {
	value.Observation.Value.FallbackSelectors = append([]string(nil), value.Observation.Value.FallbackSelectors...)
	value.Observation.ExtensionURLPath = append([]string(nil), value.Observation.ExtensionURLPath...)
	value.Observation.ObservedUnits = append([]string(nil), value.Observation.ObservedUnits...)
	observation := value.Observation
	partial := EntryPartial{Contribution: value, ObservedUnitsMax: append([]string(nil), observation.ObservedUnits...)}
	if value.Example == "" {
		partial.Missing |= entryExampleMissing
	}
	if observation.Source.Canonical == "" {
		partial.Missing |= entryCanonicalMissing
	}
	if observation.Source.Profile == "" {
		partial.Missing |= entryProfileMissing
	}
	if observation.Source.Path == "" {
		partial.Missing |= entryPathMissing
	}
	if observation.Key.Selector == "" {
		partial.Missing |= entryKeySelectorMissing
	}
	if observation.Key.System == "" {
		partial.Missing |= entrySystemMissing
	}
	if observation.Key.Version == "" {
		partial.Missing |= entryVersionMissing
	}
	if observation.Key.Code == "" {
		partial.Missing |= entryCodeMissing
	}
	if observation.Key.Display == "" {
		partial.Missing |= entryDisplayMissing
	}
	if observation.Value.Selector == "" {
		partial.Missing |= entryValueSelectorMissing
	}
	if observation.Value.Type == "" {
		partial.Missing |= entryValueTypeMissing
	}
	if len(observation.Value.FallbackSelectors) == 0 {
		partial.Missing |= entryFallbackSelectorsMissing
	}
	if !observation.Value.FallbackOnBlankString {
		partial.Missing |= entryFallbackOnBlankMissing
	}
	if observation.OwningScope == "" {
		partial.Missing |= entryOwningScopeMissing
	}
	if len(observation.ExtensionURLPath) == 0 {
		partial.Missing |= entryExtensionURLPathMissing
	}
	if observation.ChoiceArm == "" {
		partial.Missing |= entryChoiceArmMissing
	}
	if observation.LogicalType == "" {
		partial.Missing |= entryLogicalTypeMissing
	}
	if len(observation.ObservedUnits) == 0 {
		partial.Missing |= entryUnitsMinimumMissing | entryUnitsMaximumMissing
	}
	if observation.Completeness == "" {
		partial.Missing |= entryCompletenessMissing
	}
	if observation.Status == "" {
		partial.Missing |= entryStatusMissing
	}
	if observation.RuleHint == "" {
		partial.Missing |= entryRuleHintMissing
	}
	if observation.RuleVersion == "" {
		partial.Missing |= entryRuleVersionMissing
	}
	return partial
}

func MergeEntryPartials(left, right EntryPartial) (EntryPartial, bool) {
	if semanticInventoryEntryGroupKeyFor(left.Contribution) != semanticInventoryEntryGroupKeyFor(right.Contribution) {
		return EntryPartial{}, false
	}
	left.Contribution.Observation.Population += right.Contribution.Observation.Population
	left.Contribution.Observation.ExamplesTruncated = left.Contribution.Observation.ExamplesTruncated || right.Contribution.Observation.ExamplesTruncated
	mergeString := func(destination *string, source string, bit uint32) {
		leftMissing, rightMissing := left.Missing&bit != 0, right.Missing&bit != 0
		switch {
		case leftMissing && rightMissing:
			left.Missing |= bit
			*destination = ""
		case leftMissing:
			left.Missing &^= bit
			*destination = source
		case !rightMissing && source < *destination:
			*destination = source
		}
	}
	mergeSlice := func(destination *[]string, source []string, bit uint32) {
		leftMissing, rightMissing := left.Missing&bit != 0, right.Missing&bit != 0
		switch {
		case leftMissing && rightMissing:
			left.Missing |= bit
			*destination = nil
		case leftMissing:
			left.Missing &^= bit
			*destination = append([]string(nil), source...)
		case !rightMissing && compareStringSlices(source, *destination) < 0:
			*destination = append([]string(nil), source...)
		}
	}
	leftExampleMissing, rightExampleMissing := left.Missing&entryExampleMissing != 0, right.Missing&entryExampleMissing != 0
	switch {
	case leftExampleMissing && rightExampleMissing:
		left.Missing |= entryExampleMissing
		left.Contribution.Example = ""
	case leftExampleMissing:
		left.Missing &^= entryExampleMissing
		left.Contribution.Example = right.Contribution.Example
	case !rightExampleMissing && right.Contribution.Example < left.Contribution.Example:
		left.Contribution.Example = right.Contribution.Example
	}
	mergeString(&left.Contribution.Observation.Source.Canonical, right.Contribution.Observation.Source.Canonical, entryCanonicalMissing)
	mergeString(&left.Contribution.Observation.Source.Profile, right.Contribution.Observation.Source.Profile, entryProfileMissing)
	mergeString(&left.Contribution.Observation.Source.Path, right.Contribution.Observation.Source.Path, entryPathMissing)
	mergeString(&left.Contribution.Observation.Key.Selector, right.Contribution.Observation.Key.Selector, entryKeySelectorMissing)
	mergeString(&left.Contribution.Observation.Key.System, right.Contribution.Observation.Key.System, entrySystemMissing)
	mergeString(&left.Contribution.Observation.Key.Version, right.Contribution.Observation.Key.Version, entryVersionMissing)
	mergeString(&left.Contribution.Observation.Key.Code, right.Contribution.Observation.Key.Code, entryCodeMissing)
	mergeString(&left.Contribution.Observation.Key.Display, right.Contribution.Observation.Key.Display, entryDisplayMissing)
	mergeString(&left.Contribution.Observation.Value.Selector, right.Contribution.Observation.Value.Selector, entryValueSelectorMissing)
	mergeString(&left.Contribution.Observation.Value.Type, right.Contribution.Observation.Value.Type, entryValueTypeMissing)
	mergeSlice(&left.Contribution.Observation.Value.FallbackSelectors, right.Contribution.Observation.Value.FallbackSelectors, entryFallbackSelectorsMissing)
	leftFallbackMissing, rightFallbackMissing := left.Missing&entryFallbackOnBlankMissing != 0, right.Missing&entryFallbackOnBlankMissing != 0
	if leftFallbackMissing && rightFallbackMissing {
		left.Missing |= entryFallbackOnBlankMissing
		left.Contribution.Observation.Value.FallbackOnBlankString = false
	} else if leftFallbackMissing {
		left.Missing &^= entryFallbackOnBlankMissing
		left.Contribution.Observation.Value.FallbackOnBlankString = right.Contribution.Observation.Value.FallbackOnBlankString
	}
	mergeString(&left.Contribution.Observation.OwningScope, right.Contribution.Observation.OwningScope, entryOwningScopeMissing)
	mergeSlice(&left.Contribution.Observation.ExtensionURLPath, right.Contribution.Observation.ExtensionURLPath, entryExtensionURLPathMissing)
	mergeString(&left.Contribution.Observation.ChoiceArm, right.Contribution.Observation.ChoiceArm, entryChoiceArmMissing)
	mergeString(&left.Contribution.Observation.LogicalType, right.Contribution.Observation.LogicalType, entryLogicalTypeMissing)
	leftUnitsMinimumMissing, rightUnitsMinimumMissing := left.Missing&entryUnitsMinimumMissing != 0, right.Missing&entryUnitsMinimumMissing != 0
	if leftUnitsMinimumMissing && rightUnitsMinimumMissing {
		left.Missing |= entryUnitsMinimumMissing
		left.Contribution.Observation.ObservedUnits = nil
	} else if leftUnitsMinimumMissing {
		left.Missing &^= entryUnitsMinimumMissing
		left.Contribution.Observation.ObservedUnits = append([]string(nil), right.Contribution.Observation.ObservedUnits...)
	} else if !rightUnitsMinimumMissing && compareStringSlices(right.Contribution.Observation.ObservedUnits, left.Contribution.Observation.ObservedUnits) < 0 {
		left.Contribution.Observation.ObservedUnits = append([]string(nil), right.Contribution.Observation.ObservedUnits...)
	}
	if left.Missing&entryUnitsMaximumMissing != 0 && right.Missing&entryUnitsMaximumMissing != 0 {
		left.Missing |= entryUnitsMaximumMissing
		left.ObservedUnitsMax = nil
	} else if left.Missing&entryUnitsMaximumMissing != 0 {
		left.Missing &^= entryUnitsMaximumMissing
		left.ObservedUnitsMax = append([]string(nil), right.ObservedUnitsMax...)
	} else if right.Missing&entryUnitsMaximumMissing == 0 && compareStringSlices(right.ObservedUnitsMax, left.ObservedUnitsMax) > 0 {
		left.ObservedUnitsMax = append([]string(nil), right.ObservedUnitsMax...)
	}
	mergeString((*string)(&left.Contribution.Observation.Completeness), string(right.Contribution.Observation.Completeness), entryCompletenessMissing)
	mergeString(&left.Contribution.Observation.Status, right.Contribution.Observation.Status, entryStatusMissing)
	mergeString(&left.Contribution.Observation.RuleHint, right.Contribution.Observation.RuleHint, entryRuleHintMissing)
	mergeString(&left.Contribution.Observation.RuleVersion, right.Contribution.Observation.RuleVersion, entryRuleVersionMissing)
	left.Contribution.Observation.ExamplesTruncated = left.Contribution.Observation.ExamplesTruncated || right.Contribution.Observation.ExamplesTruncated
	return left, true
}

func compareStringSlices(left, right []string) int {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func EntryGroupOrderKey(value Contribution) string {
	groupKey := semanticInventoryEntryGroupKeyFor(value)
	var keyBytes []byte
	for _, part := range [...]string{groupKey.AuthResourcePath, groupKey.ResourceType, groupKey.BindingID, groupKey.ConceptID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		keyBytes = append(keyBytes, size[:]...)
		keyBytes = append(keyBytes, part...)
	}
	return string(keyBytes)
}

// Essential: callers that aggregate spill runs must use the same four-field identity as the in-memory reducer.
func semanticInventoryEntryGroupKeyFor(value Contribution) semanticInventoryEntryGroupKey {
	return semanticInventoryEntryGroupKey{
		AuthResourcePath: value.AuthResourcePath,
		ResourceType:     value.ResourceType,
		BindingID:        value.BindingID,
		ConceptID:        value.ConceptID,
	}
}

func EntryKey(project, generation, buildID, sourceKind, authPath, bindingID, conceptID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{project, generation, buildID, sourceKind, authPath, bindingID, conceptID}, "|")))
	return hex.EncodeToString(digest[:])
}

func (a *EntryAggregator) Len() int {
	if a == nil {
		return 0
	}
	return len(a.entries)
}
func (a *EntryAggregator) Reset() {
	if a != nil {
		a.entries = make(map[semanticInventoryEntryGroupKey]EntryPartial)
	}
}
func (a *EntryAggregator) Values() []EntryPartial {
	if a == nil || len(a.entries) == 0 {
		return nil
	}
	keys := make([]semanticInventoryEntryGroupKey, 0, len(a.entries))
	for key := range a.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return compareSemanticInventoryEntryGroupKeys(keys[i], keys[j]) < 0
	})
	values := make([]EntryPartial, 0, len(keys))
	for _, key := range keys {
		values = append(values, a.entries[key])
	}
	return values
}

func compareSemanticInventoryEntryGroupKeys(left, right semanticInventoryEntryGroupKey) int {
	leftParts := [...]string{left.AuthResourcePath, left.ResourceType, left.BindingID, left.ConceptID}
	rightParts := [...]string{right.AuthResourcePath, right.ResourceType, right.BindingID, right.ConceptID}
	for i := range leftParts {
		if len(leftParts[i]) < len(rightParts[i]) {
			return -1
		}
		if len(leftParts[i]) > len(rightParts[i]) {
			return 1
		}
		if leftParts[i] < rightParts[i] {
			return -1
		}
		if leftParts[i] > rightParts[i] {
			return 1
		}
	}
	return 0
}

func EntryDocument(value EntryPartial, project, generation, buildID, sourceKind string) map[string]any {
	c := value.Contribution
	o := c.Observation
	missing := value.Missing
	nullableString := func(text string, bit uint32) any {
		if missing&bit != 0 {
			return nil
		}
		return text
	}
	nullableSlice := func(items []string, bit uint32) any {
		if missing&bit != 0 {
			return nil
		}
		return items
	}
	example := nullableString(c.Example, entryExampleMissing)
	examples := []string{}
	if text, ok := example.(string); ok && text != "" {
		examples = append(examples, text)
	}
	key := EntryKey(project, generation, buildID, sourceKind, c.AuthResourcePath, c.BindingID, c.ConceptID)
	searchParts := []string{c.ResourceType, o.Source.Path, o.Value.Selector, o.OwningScope, o.Key.System, o.Key.Code, o.Key.Display}
	searchValues := make([]string, 0, len(searchParts))
	for i, part := range searchParts {
		bit := []uint32{0, entryPathMissing, entryValueSelectorMissing, entryOwningScopeMissing, entrySystemMissing, entryCodeMissing, entryDisplayMissing}[i]
		if bit == 0 || missing&bit == 0 {
			searchValues = append(searchValues, part)
		}
	}
	unitsMinimum := nullableSlice(o.ObservedUnits, entryUnitsMinimumMissing)
	unitsTruncated := compareStringSlices(o.ObservedUnits, value.ObservedUnitsMax) != 0
	observation := map[string]any{
		"schema_version": o.SchemaVersion,
		"source":         map[string]any{"canonical": nullableString(o.Source.Canonical, entryCanonicalMissing), "type": c.ResourceType, "profile": nullableString(o.Source.Profile, entryProfileMissing), "path": nullableString(o.Source.Path, entryPathMissing)},
		"key":            map[string]any{"selector": nullableString(o.Key.Selector, entryKeySelectorMissing), "system": nullableString(o.Key.System, entrySystemMissing), "version": nullableString(o.Key.Version, entryVersionMissing), "code": nullableString(o.Key.Code, entryCodeMissing), "display": nullableString(o.Key.Display, entryDisplayMissing)},
		"value":          map[string]any{"selector": nullableString(o.Value.Selector, entryValueSelectorMissing), "type": nullableString(o.Value.Type, entryValueTypeMissing), "fallback_selectors": nullableSlice(o.Value.FallbackSelectors, entryFallbackSelectorsMissing), "fallback_on_blank_string": nullableBool(o.Value.FallbackOnBlankString, missing&entryFallbackOnBlankMissing != 0)},
		"owning_scope":   nullableString(o.OwningScope, entryOwningScopeMissing), "extension_url_path": nullableSlice(o.ExtensionURLPath, entryExtensionURLPathMissing),
		"choice_arm": nullableString(o.ChoiceArm, entryChoiceArmMissing), "logical_type": nullableString(o.LogicalType, entryLogicalTypeMissing),
		"observed_units": emptyIfNull(unitsMinimum), "observed_units_truncated": unitsTruncated,
		"completeness": nullableString(string(o.Completeness), entryCompletenessMissing), "status": nullableString(o.Status, entryStatusMissing),
		"population": o.Population, "examples": examples, "examples_truncated": o.ExamplesTruncated || o.Population > 1,
		"rule_hint": nullableString(o.RuleHint, entryRuleHintMissing), "rule_version": nullableString(o.RuleVersion, entryRuleVersionMissing),
	}
	return map[string]any{
		"_key": key, "project": project, "dataset_generation": generation, "build_id": buildID,
		"source_kind": sourceKind, "auth_resource_path": c.AuthResourcePath, "resource_type": c.ResourceType,
		"binding_id": c.BindingID, "concept_id": c.ConceptID, "search_text": strings.ToLower(strings.Join(searchValues, " ")),
		"example": example, "observation": observation,
	}
}

func nullableBool(value bool, missing bool) any {
	if missing {
		return nil
	}
	return value
}
func emptyIfNull(value any) any {
	if value == nil {
		return []string{}
	}
	return value
}
