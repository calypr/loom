package binding

import (
	"fmt"
	"github.com/calypr/loom/internal/fhir/schema"
	"strings"
)

type CorrelatedValueScope string

type CorrelatedSelectionScope string

const (
	CorrelatedSelectionScopeExact         CorrelatedSelectionScope = "EXACT"
	CorrelatedSelectionScopeSystemVersion CorrelatedSelectionScope = "SYSTEM_VERSION"
)

const (
	CorrelatedValueScopeOwner  CorrelatedValueScope = "OWNER"
	CorrelatedValueScopeCoding CorrelatedValueScope = "CODING"
)

type CorrelatedBinding struct {
	OwnerPath   string `json:"ownerPath,omitempty"`
	KeyPath     string `json:"keyPath"`
	VersionPath string `json:"versionPath,omitempty"`

	SelectionScope CorrelatedSelectionScope `json:"selectionScope,omitempty"`

	ValueScope            CorrelatedValueScope `json:"valueScope,omitempty"`
	SystemPath            string               `json:"systemPath"`
	CodePath              string               `json:"codePath"`
	ValuePath             string               `json:"valuePath"`
	ValueFallback         []string             `json:"valueFallback,omitempty"`
	FallbackOnBlankString bool                 `json:"fallbackOnBlankString,omitempty"`
	ChoiceArms            []string             `json:"choiceArms,omitempty"`
	LogicalType           string               `json:"logicalType"`
	UnitPath              string               `json:"unitPath,omitempty"`
}

type CorrelatedKey struct {
	System  string `json:"system"`
	Code    string `json:"code"`
	Version string `json:"version,omitempty"`
}

type ExtensionBinding struct {
	OwnerPath             string   `json:"ownerPath,omitempty"`
	URLPath               []string `json:"urlPath"`
	ValuePath             string   `json:"valuePath"`
	CanonicalJSONValue    bool     `json:"canonicalJsonValue,omitempty"`
	LogicalType           string   `json:"logicalType"`
	ChoiceArms            []string `json:"choiceArms,omitempty"`
	ValueFallback         []string `json:"valueFallback,omitempty"`
	FallbackOnBlankString bool     `json:"fallbackOnBlankString,omitempty"`
	UnitPath              string   `json:"unitPath,omitempty"`
}

type CorrelatedBindingSpec struct {
	ResourceType          string
	OwnerSelector         schema.Selector
	OwnerResource         string
	KeySelector           schema.Selector
	KeyResource           string
	KeyIsArray            bool
	VersionSelector       *schema.Selector
	ValueScope            CorrelatedValueScope
	SelectionScope        CorrelatedSelectionScope
	SystemSelector        schema.Selector
	CodeSelector          schema.Selector
	ValueSelector         schema.Selector
	ValueFallbacks        []schema.Selector
	FallbackOnBlankString bool
	ChoiceArms            []string
	LogicalType           string
	ValuePrimitive        schema.PrimitiveKind
	UnitSelector          *schema.Selector
}

type ExtensionBindingSpec struct {
	ResourceType          string
	OwnerSelector         schema.Selector
	OwnerResource         string
	URLSelectors          []schema.Selector
	ValueSelector         schema.Selector
	CanonicalJSONValue    bool
	ValueFallbacks        []schema.Selector
	FallbackOnBlankString bool
	ChoiceArms            []string
	LogicalType           string
	ValuePrimitive        schema.PrimitiveKind
	UnitSelector          *schema.Selector
}

func ValidateCorrelatedBinding(resourceType string, binding CorrelatedBinding) (CorrelatedBindingSpec, error) {
	if !schema.HasResource(resourceType) {
		return CorrelatedBindingSpec{}, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := schema.CanonicalizePath(binding.OwnerPath)
	keyPath := schema.CanonicalizePath(binding.KeyPath)
	if keyPath == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated keyPath is required")
	}
	if strings.TrimSpace(binding.SystemPath) == "" || strings.TrimSpace(binding.CodePath) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated binding requires systemPath and codePath")
	}
	if strings.TrimSpace(binding.ValuePath) == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated binding requires valuePath")
	}
	ownerResource := resourceType
	ownerSelector := schema.Selector{}
	if ownerPath != "" {
		var err error
		ownerSelector, err = schema.ParseSelector(ownerPath)
		if err != nil {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath: %w", err)
		}
		resolved, ok := schema.ResolvePath(resourceType, ownerPath)
		if !ok || resolved.Property.Kind != "array" || strings.TrimSpace(resolved.Property.ItemRef) == "" {
			return CorrelatedBindingSpec{}, fmt.Errorf("ownerPath %q must resolve to a repeated object", ownerPath)
		}
		ownerResource = resolved.Property.ItemRef
	}
	if ownerPath != "" && keyPath != ownerPath && !strings.HasPrefix(keyPath, ownerPath+".") {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q is outside ownerPath %q", keyPath, ownerPath)
	}
	keyRelative := keyPath
	if ownerPath != "" {
		keyRelative = strings.TrimPrefix(keyPath, ownerPath+".")
	}
	keyResolved, ok := schema.ResolvePath(ownerResource, keyRelative)
	if !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q is not represented within owner scope", keyPath)
	}
	keyIsArray := keyResolved.Property.Kind == "array"
	keyResource := ""
	if keyIsArray {
		if !strings.HasSuffix(keyRelative, "[]") {
			return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q must include [] for its repeated Coding item", keyPath)
		}
		keyResource = keyResolved.Property.ItemRef
	} else if keyResolved.Property.Kind == "object" {
		if strings.HasSuffix(keyRelative, "[]") {
			return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q cannot include [] for a singular Coding object", keyPath)
		}
		keyResource = keyResolved.Property.Ref
	}
	if keyResource == "" {
		return CorrelatedBindingSpec{}, fmt.Errorf("keyPath %q must resolve to a Coding object or repeated Coding array within owner scope", keyPath)
	}
	system, err := schema.ParseSelector(binding.SystemPath)
	if err != nil {
		return CorrelatedBindingSpec{}, fmt.Errorf("systemPath: %w", err)
	}
	code, err := schema.ParseSelector(binding.CodePath)
	if err != nil {
		return CorrelatedBindingSpec{}, fmt.Errorf("codePath: %w", err)
	}
	if _, ok := schema.ResolvePath(keyResource, schema.CanonicalizePath(binding.SystemPath)); !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("systemPath %q is not a field of Coding item %q", binding.SystemPath, keyResource)
	}
	if _, ok := schema.ResolvePath(keyResource, schema.CanonicalizePath(binding.CodePath)); !ok {
		return CorrelatedBindingSpec{}, fmt.Errorf("codePath %q is not a field of Coding item %q", binding.CodePath, keyResource)
	}
	valueResource := ownerResource
	var versionSelector *schema.Selector
	switch binding.ValueScope {
	case "", CorrelatedValueScopeOwner:
		if !keyIsArray {
			return CorrelatedBindingSpec{}, fmt.Errorf("owner value scope requires keyPath to identify a repeated Coding item")
		}
		if strings.TrimSpace(binding.VersionPath) != "" {
			return CorrelatedBindingSpec{}, fmt.Errorf("versionPath requires CODING value scope")
		}
	case CorrelatedValueScopeCoding:
		if keyResource != "Coding" {
			return CorrelatedBindingSpec{}, fmt.Errorf("CODING value scope requires keyPath to resolve to Coding, got %q", keyResource)
		}
		valueResource = "Coding"
		if versionPath := schema.CanonicalizePath(binding.VersionPath); versionPath != "" {
			if versionPath != "version" {
				return CorrelatedBindingSpec{}, fmt.Errorf("versionPath %q must identify Coding.version", versionPath)
			}
			versionMetadata, ok := schema.ResolveTerminalScalarMetadata("Coding", versionPath)
			if !ok || versionMetadata.Primitive != schema.PrimitiveString {
				return CorrelatedBindingSpec{}, fmt.Errorf("versionPath %q must resolve to a string in Coding", versionPath)
			}
			selector, parseErr := schema.ParseSelector(versionPath)
			if parseErr != nil {
				return CorrelatedBindingSpec{}, fmt.Errorf("versionPath: %w", parseErr)
			}
			versionSelector = &selector
		}
	default:
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated valueScope %q is unsupported", binding.ValueScope)
	}
	switch binding.SelectionScope {
	case "", CorrelatedSelectionScopeExact:
	case CorrelatedSelectionScopeSystemVersion:
		if binding.ValueScope != CorrelatedValueScopeCoding || versionSelector == nil {
			return CorrelatedBindingSpec{}, fmt.Errorf("SYSTEM_VERSION selection scope requires CODING value scope and versionPath")
		}
		if system.CanonicalPath() != "system" || code.CanonicalPath() != "code" {
			return CorrelatedBindingSpec{}, fmt.Errorf("SYSTEM_VERSION selection requires Coding.system and Coding.code selectors")
		}
	default:
		return CorrelatedBindingSpec{}, fmt.Errorf("correlated selectionScope %q is unsupported", binding.SelectionScope)
	}
	value, valueErr := validateBindingValue(valueResource, binding.ValuePath, binding.ValueFallback, binding.FallbackOnBlankString, binding.ChoiceArms, binding.LogicalType, binding.UnitPath)
	if valueErr != nil {
		return CorrelatedBindingSpec{}, valueErr
	}
	return CorrelatedBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: ownerResource, KeySelector: mustParseSelector(keyRelative), KeyResource: keyResource, KeyIsArray: keyIsArray, VersionSelector: versionSelector, ValueScope: binding.ValueScope, SelectionScope: binding.SelectionScope, SystemSelector: system, CodeSelector: code, ValueSelector: value.ValueSelector, ValueFallbacks: value.ValueFallbacks, FallbackOnBlankString: value.FallbackOnBlankString, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, UnitSelector: value.UnitSelector}, nil
}

func ValidateCorrelatedKey(scope CorrelatedSelectionScope, key CorrelatedKey) error {
	if strings.TrimSpace(key.System) == "" {
		return fmt.Errorf("correlated selection requires a non-empty system")
	}
	switch scope {
	case "", CorrelatedSelectionScopeExact:
		if strings.TrimSpace(key.Code) == "" {
			return fmt.Errorf("exact correlated selection requires a non-empty code")
		}
	case CorrelatedSelectionScopeSystemVersion:
		if key.Code != "" {
			return fmt.Errorf("SYSTEM_VERSION selection must not supply a code")
		}
	default:
		return fmt.Errorf("correlated selectionScope %q is unsupported", scope)
	}
	return nil
}

type checkedBindingValue struct {
	ValueSelector         schema.Selector
	ValueFallbacks        []schema.Selector
	FallbackOnBlankString bool
	ChoiceArms            []string
	LogicalType           string
	ValuePrimitive        schema.PrimitiveKind
	UnitSelector          *schema.Selector
}

func validateBindingValue(valueResource, valuePath string, valueFallback []string, fallbackOnBlankString bool, requestedChoiceArms []string, requestedLogicalType, unitPath string) (checkedBindingValue, error) {
	valuePath = schema.CanonicalizePath(valuePath)
	if valuePath == "" {
		return checkedBindingValue{}, fmt.Errorf("valuePath is required")
	}
	if _, ok := schema.ResolvePath(valueResource, valuePath); !ok {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q is not represented in owner resource %q", valuePath, valueResource)
	}
	value, err := schema.ParseSelector(valuePath)
	if err != nil {
		return checkedBindingValue{}, fmt.Errorf("valuePath: %w", err)
	}
	if strings.TrimSpace(requestedLogicalType) == "" {
		return checkedBindingValue{}, fmt.Errorf("logicalType is required")
	}
	logicalType, ok := normalizeCorrelatedLogicalType(requestedLogicalType)
	if !ok {
		return checkedBindingValue{}, fmt.Errorf("logicalType %q is unsupported", requestedLogicalType)
	}
	valueMetadata, ok := schema.ResolveTerminalScalarMetadata(valueResource, valuePath)
	if !ok || valueMetadata.Primitive == schema.PrimitiveUnknown {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q does not resolve to a scalar in owner resource %q", valuePath, valueResource)
	}
	choiceArms := append([]string(nil), requestedChoiceArms...)
	for index, arm := range choiceArms {
		choiceArms[index] = strings.TrimSpace(arm)
		if choiceArms[index] == "" {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] is empty", index)
		}
	}
	valueArm := strings.TrimSuffix(strings.Split(valuePath, ".")[0], "[]")
	valueArmMetadata, valueArmExists := schema.ResolvePath(valueResource, valueArm)
	valueChoiceGroup := ""
	if valueArmExists {
		valueChoiceGroup = strings.TrimSpace(valueArmMetadata.Property.ChoiceGroup)
	}
	for index, arm := range choiceArms {
		resolved, exists := schema.ResolvePath(valueResource, arm)
		choiceGroup := ""
		if exists {
			choiceGroup = strings.TrimSpace(resolved.Property.ChoiceGroup)
		}
		if choiceGroup == "" {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] %q is not a choice arm of %s", index, arm, valueResource)
		}
		if valueChoiceGroup == "" || choiceGroup != valueChoiceGroup {
			return checkedBindingValue{}, fmt.Errorf("choiceArms[%d] %q is outside valuePath %q choice group", index, arm, valuePath)
		}
	}
	if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, valueArm) {
		return checkedBindingValue{}, fmt.Errorf("valuePath %q is outside declared choiceArms", valuePath)
	}
	if len(choiceArms) > 1 && logicalType != "string" {
		return checkedBindingValue{}, fmt.Errorf("mixed choice arms require an explicit string logicalType")
	}
	if !correlatedPrimitiveCompatible(logicalType, valueMetadata.Primitive, len(choiceArms) > 1) {
		return checkedBindingValue{}, fmt.Errorf("logicalType %q is incompatible with valuePath %q primitive %q", logicalType, valuePath, valueMetadata.Primitive)
	}
	fallbacks := make([]schema.Selector, 0, len(valueFallback))
	for index, fallbackPath := range valueFallback {
		fallbackPath = schema.CanonicalizePath(fallbackPath)
		if _, ok := schema.ResolvePath(valueResource, fallbackPath); !ok {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is not represented in owner resource %q", index, fallbackPath, valueResource)
		}
		fallback, parseErr := schema.ParseSelector(fallbackPath)
		if parseErr != nil {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d]: %w", index, parseErr)
		}
		fallbackMetadata, metadataOK := schema.ResolveTerminalScalarMetadata(valueResource, fallbackPath)
		if !metadataOK || fallbackMetadata.Primitive == schema.PrimitiveUnknown || !correlatedPrimitiveCompatible(logicalType, fallbackMetadata.Primitive, len(choiceArms) > 1) {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is incompatible with logicalType %q", index, fallbackPath, logicalType)
		}
		fallbackArm := strings.TrimSuffix(strings.Split(fallbackPath, ".")[0], "[]")
		if len(choiceArms) > 0 && !containsCorrelatedString(choiceArms, fallbackArm) {
			return checkedBindingValue{}, fmt.Errorf("valueFallback[%d] %q is outside declared choiceArms", index, fallbackPath)
		}
		fallbacks = append(fallbacks, fallback)
	}
	if fallbackOnBlankString && (logicalType != "string" || len(fallbacks) == 0) {
		return checkedBindingValue{}, fmt.Errorf("fallbackOnBlankString requires a string value and at least one fallback selector")
	}
	var unit *schema.Selector
	if strings.TrimSpace(unitPath) == "" {
		if inferred, ok := inferQuantityUnitSelector(valueResource, valuePath); ok {
			unit = &inferred
		}
	} else {
		unitSelector, parseErr := schema.ParseSelector(schema.CanonicalizePath(unitPath))
		if parseErr != nil {
			return checkedBindingValue{}, fmt.Errorf("unitPath: %w", parseErr)
		}
		if _, ok := schema.ResolvePath(valueResource, unitSelector.CanonicalPath()); !ok {
			return checkedBindingValue{}, fmt.Errorf("unitPath %q is not represented in owner resource %q", unitPath, valueResource)
		}
		unitMetadata, metadataOK := schema.ResolveTerminalScalarMetadata(valueResource, unitSelector.CanonicalPath())
		if !metadataOK || unitMetadata.Primitive != schema.PrimitiveString {
			return checkedBindingValue{}, fmt.Errorf("unitPath %q must resolve to a string", unitPath)
		}
		unit = &unitSelector
	}
	return checkedBindingValue{ValueSelector: value, ValueFallbacks: fallbacks, FallbackOnBlankString: fallbackOnBlankString, ChoiceArms: choiceArms, LogicalType: logicalType, ValuePrimitive: valueMetadata.Primitive, UnitSelector: unit}, nil
}

func inferQuantityUnitSelector(valueResource, valuePath string) (schema.Selector, bool) {
	valuePath = schema.CanonicalizePath(valuePath)
	separator := strings.LastIndex(valuePath, ".")
	if separator < 1 || valuePath[separator+1:] != "value" {
		return schema.Selector{}, false
	}
	parentPath := valuePath[:separator]
	parent, ok := schema.ResolvePath(valueResource, parentPath)
	if !ok || parent.PropertyRef != "Quantity" {
		return schema.Selector{}, false
	}
	unitPath := parentPath + ".unit"
	metadata, ok := schema.ResolveTerminalScalarMetadata(valueResource, unitPath)
	if !ok || metadata.Primitive != schema.PrimitiveString {
		return schema.Selector{}, false
	}
	unit, err := schema.ParseSelector(unitPath)
	return unit, err == nil
}

func ValidateExtensionBinding(resourceType string, binding ExtensionBinding) (ExtensionBindingSpec, error) {
	ownerSelector, extensionDepth, err := extensionOwnerPath(resourceType, binding.OwnerPath)
	if err != nil {
		return ExtensionBindingSpec{}, err
	}
	if len(binding.URLPath) != extensionDepth {
		return ExtensionBindingSpec{}, fmt.Errorf("urlPath must contain one URL per extension boundary (got %d, want %d)", len(binding.URLPath), extensionDepth)
	}
	urls := make([]string, len(binding.URLPath))
	for index, value := range binding.URLPath {
		urls[index] = strings.TrimSpace(value)
		if urls[index] == "" {
			return ExtensionBindingSpec{}, fmt.Errorf("urlPath[%d] is empty", index)
		}
	}
	value := checkedBindingValue{LogicalType: binding.LogicalType}
	if binding.CanonicalJSONValue {
		if strings.TrimSpace(binding.ValuePath) != "" || len(binding.ValueFallback) != 0 || len(binding.ChoiceArms) != 0 || binding.UnitPath != "" || binding.FallbackOnBlankString {
			return ExtensionBindingSpec{}, fmt.Errorf("canonical JSON Extension values cannot also select a value[x] arm or fallback")
		}
		if logical := strings.TrimSpace(binding.LogicalType); logical != "" && !strings.EqualFold(logical, "string") {
			return ExtensionBindingSpec{}, fmt.Errorf("canonical JSON Extension values must have string logical type")
		}
		value.LogicalType = "string"
	} else {
		var err error
		value, err = validateBindingValue("Extension", binding.ValuePath, binding.ValueFallback, binding.FallbackOnBlankString, binding.ChoiceArms, binding.LogicalType, binding.UnitPath)
		if err != nil {
			return ExtensionBindingSpec{}, err
		}
	}
	urlSelectors := make([]schema.Selector, extensionDepth)
	for index := range urlSelectors {
		urlSelectors[index], _ = schema.ParseSelector("url")
	}
	return ExtensionBindingSpec{ResourceType: resourceType, OwnerSelector: ownerSelector, OwnerResource: "Extension", URLSelectors: urlSelectors, ValueSelector: value.ValueSelector, CanonicalJSONValue: binding.CanonicalJSONValue, ValueFallbacks: value.ValueFallbacks, FallbackOnBlankString: value.FallbackOnBlankString, ChoiceArms: value.ChoiceArms, LogicalType: value.LogicalType, ValuePrimitive: value.ValuePrimitive, UnitSelector: value.UnitSelector}, nil
}

func ValidateExtensionOwnerPath(resourceType, path string) error {
	_, _, err := extensionOwnerPath(resourceType, path)
	return err
}

func extensionOwnerPath(resourceType, path string) (schema.Selector, int, error) {
	if !schema.HasResource(resourceType) {
		return schema.Selector{}, 0, fmt.Errorf("resource type %q is not represented by generated FHIR schema", resourceType)
	}
	ownerPath := schema.CanonicalizePath(path)
	if ownerPath == "" {
		return schema.Selector{}, 0, fmt.Errorf("ownerPath is required")
	}
	parts := strings.Split(ownerPath, ".")
	baseParts := make([]string, 0, len(parts))
	currentResource := resourceType
	startedExtension := false
	extensionDepth := 0
	for index, part := range parts {
		resolved, ok := schema.ResolvePath(currentResource, part)
		if !ok {
			return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q is not represented in resource %q", part, currentResource)
		}
		name := strings.TrimSuffix(part, "[]")
		isExtension := name == "extension" && strings.HasSuffix(part, "[]")
		if isExtension {
			if resolved.Property.Kind != "array" || resolved.Property.ItemRef != "Extension" {
				return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q must be a repeated Extension", part)
			}
			startedExtension = true
			extensionDepth++
			currentResource = "Extension"
			continue
		}
		if startedExtension {
			return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q follows an extension boundary and is not allowed", part)
		}
		if !strings.HasSuffix(part, "[]") && index == len(parts)-1 {
			return schema.Selector{}, 0, fmt.Errorf("ownerPath must end at a repeated extension")
		}
		baseParts = append(baseParts, part)
		switch resolved.Property.Kind {
		case "array":
			if strings.TrimSpace(resolved.Property.ItemRef) == "" {
				return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q must resolve to a repeated object", part)
			}
			currentResource = resolved.Property.ItemRef
		case "object":
			if strings.TrimSpace(resolved.Property.Ref) == "" {
				return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q must resolve to a named object", part)
			}
			currentResource = resolved.Property.Ref
		default:
			return schema.Selector{}, 0, fmt.Errorf("ownerPath segment %q is not an object boundary", part)
		}
	}
	if extensionDepth == 0 {
		return schema.Selector{}, 0, fmt.Errorf("ownerPath must contain at least one repeated extension")
	}
	ownerSelector := schema.Selector{}
	if len(baseParts) > 0 {
		basePath := strings.Join(baseParts, ".")
		var err error
		ownerSelector, err = schema.ParseSelector(basePath)
		if err != nil {
			return schema.Selector{}, 0, fmt.Errorf("ownerPath base: %w", err)
		}
		resolved, ok := schema.ResolvePath(resourceType, basePath)
		if !ok || !((resolved.Property.Kind == "array" && strings.TrimSpace(resolved.Property.ItemRef) != "") ||
			(resolved.Property.Kind == "object" && strings.TrimSpace(resolved.Property.Ref) != "")) {
			return schema.Selector{}, 0, fmt.Errorf("ownerPath base %q must resolve to an object or repeated object", basePath)
		}
	}
	return ownerSelector, extensionDepth, nil
}

func normalizeCorrelatedLogicalType(input string) (string, bool) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input), "-", "_"))
	switch value {
	case "string", "code", "boolean", "integer", "decimal", "date", "date_time":
		return value, true
	case "number":
		return value, true
	default:
		return "", false
	}
}

func containsCorrelatedString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func correlatedPrimitiveCompatible(logicalType string, primitive schema.PrimitiveKind, explicitMixed bool) bool {
	if explicitMixed && logicalType == "string" {
		return primitive != schema.PrimitiveUnknown
	}
	switch logicalType {
	case "string", "code":
		return primitive == schema.PrimitiveString
	case "boolean":
		return primitive == schema.PrimitiveBoolean
	case "integer":
		return primitive == schema.PrimitiveInteger
	case "decimal":
		return primitive == schema.PrimitiveDecimal
	case "number":
		return primitive == schema.PrimitiveDecimal || primitive == schema.PrimitiveInteger
	case "date":
		return primitive == schema.PrimitiveDate
	case "date_time":
		return primitive == schema.PrimitiveDateTime
	default:
		return false
	}
}

func mustParseSelector(path string) schema.Selector {
	selector, _ := schema.ParseSelector(path)
	return selector
}
