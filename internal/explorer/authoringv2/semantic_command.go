package authoringv2

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type SemanticReadinessStatus string

const (
	SemanticReadinessReady            SemanticReadinessStatus = "READY"
	SemanticReadinessReadyWithWarning SemanticReadinessStatus = "READY_WITH_WARNING"
	SemanticReadinessNeedsMapping     SemanticReadinessStatus = "NEEDS_MAPPING"
	SemanticReadinessUnsupported      SemanticReadinessStatus = "UNSUPPORTED"
)

type SemanticSelectionReadiness struct {
	Status  SemanticReadinessStatus `json:"status"`
	Code    string                  `json:"code"`
	Message string                  `json:"message"`
}

func (r SemanticSelectionReadiness) Addable() bool {
	return r.Status == SemanticReadinessReady || r.Status == SemanticReadinessReadyWithWarning
}

// SemanticSelectionPlan is the shared result used by inventory browsing and
// command application. Source is absent when the observation cannot yet be
// represented as one complete compiler input.
type SemanticSelectionPlan struct {
	Source      *ColumnSource
	LogicalType string
	Readiness   SemanticSelectionReadiness
}

func (s SemanticSelection) validate() error {
	if strings.TrimSpace(s.ConceptID) == "" || strings.TrimSpace(s.BindingID) == "" {
		return fmt.Errorf("conceptId and bindingId are required")
	}
	if s.RouteEdgeIDs == nil {
		return fmt.Errorf("routeEdgeIds must be explicit, including an empty root route")
	}
	if !contains([]string{"VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT"}, strings.ToUpper(strings.TrimSpace(s.ProjectionMode))) {
		return fmt.Errorf("projectionMode must be an explicit supported value policy")
	}
	return nil
}

func applySemanticSelections(workspace *Workspace, catalogSnapshot CatalogSnapshot, commandID string, commandIndex int, command Command) (CommandResult, error) {
	documentIndex := documentIndex(workspace, command.OutputID)
	if documentIndex < 0 {
		return CommandResult{}, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := &workspace.Documents[documentIndex]
	result := CommandResult{Type: CommandResultSemanticSelectionsAdded, OutputID: command.OutputID, SemanticSelections: make([]SemanticSelectionResult, 0, len(command.SemanticSelections))}
	for selectionIndex, selection := range command.SemanticSelections {
		entry := selection.ResolvedObservation
		if entry == nil || entry.ConceptID != selection.ConceptID || entry.BindingID != selection.BindingID {
			return CommandResult{}, fmt.Errorf("semantic selection %d has no matching server-resolved inventory row", selectionIndex)
		}
		observation := entry.Observation
		plan := ResolveSemanticSelectionPlan(observation)
		if !plan.Readiness.Addable() {
			return CommandResult{}, fmt.Errorf("semantic selection %d: %s: %s", selectionIndex, plan.Readiness.Code, plan.Readiness.Message)
		}
		if plan.Source == nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d: %s: %s", selectionIndex, "SELECTION_PLAN_INCOMPLETE", "the selected semantic field has no complete compiler source")
		}
		occurrenceID, err := ensureSemanticRoute(document, catalogSnapshot, commandID, commandIndex, selectionIndex, selection.RouteEdgeIDs, observation.Source.Type)
		if err != nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d route: %w", selectionIndex, err)
		}
		source := plan.Source.Normalized()
		projectionMode := strings.ToUpper(strings.TrimSpace(selection.ProjectionMode))
		switch {
		case source.Field != nil:
			source.Field.ProjectionMode = projectionMode
		case source.Lookup != nil:
			source.Lookup.ProjectionMode = projectionMode
		case source.Categorical != nil:
			source.Categorical.ProjectionMode = projectionMode
		default:
			return CommandResult{}, fmt.Errorf("semantic selection %d: source does not accept a projection mode", selectionIndex)
		}
		source = editableSource(occurrenceID, source)
		if err := source.validate("semantic selection source"); err != nil {
			return CommandResult{}, err
		}
		if err := validateEditableSource(*document, catalogSnapshot, occurrenceID, source); err != nil {
			return CommandResult{}, err
		}
		columnID := commandGeneratedID("col_", "semantic-selection/v1", command.OutputID, selection.ConceptID, selection.BindingID, strings.Join(selection.RouteEdgeIDs, "\x00"), source.ProjectionMode())
		status := SemanticSelectionAdded
		found := false
		for i := range document.Columns {
			existing := &document.Columns[i]
			if existing.Column != columnID {
				continue
			}
			if existing.OccurrenceID != occurrenceID || existing.LogicalType != plan.LogicalType || !sourceEqual(existing.Source, source) {
				return CommandResult{}, fmt.Errorf("semantic column identity %q conflicts with a different feature", columnID)
			}
			status = SemanticSelectionAlreadyPresent
			found = true
			break
		}
		if !found {
			label := strings.TrimSpace(selection.Title)
			if label == "" {
				label = strings.TrimSpace(observation.SlotLabel)
			}
			if label == "" {
				label = strings.TrimSpace(observation.Key.Display)
			}
			if label == "" {
				label = strings.TrimSpace(observation.Key.Code)
			}
			column := Column{Column: columnID, Label: label, LogicalType: plan.LogicalType, OccurrenceID: occurrenceID, Source: source}
			applyInitialPresentation(&column, InitialPresentationTable, nextTableOrder(document.Columns))
			document.Columns = append(document.Columns, column)
		}
		result.SemanticSelections = append(result.SemanticSelections, SemanticSelectionResult{
			ConceptID: selection.ConceptID,
			BindingID: selection.BindingID,
			ColumnID:  columnID,
			Status:    status,
		})
	}
	return result, nil
}

// ResolveSemanticSelectionPlan validates the observed FHIR structure and
// produces the one source form used by both browsing and command application.
func ResolveSemanticSelectionPlan(observation catalog.SemanticObservation) SemanticSelectionPlan {
	if observation.SchemaVersion != catalog.SemanticObservationSchemaVersion || observation.RuleVersion != strconv.Itoa(catalog.SemanticObservationRuleVersion) {
		return semanticUnsupported("SEMANTIC_OBSERVATION_VERSION_UNSUPPORTED", "This catalog observation uses an unsupported schema or rule version.")
	}
	if observation.Completeness != catalog.SemanticComplete {
		return semanticUnsupported("SEMANTIC_OBSERVATION_INCOMPLETE", "The catalog observation does not contain enough evidence to build a compiler source.")
	}
	if observation.Status == catalog.SemanticStatusUnsupportedValueProjection {
		return semanticUnsupported("VALUE_PROJECTION_UNSUPPORTED", "The observed value does not contain the scalar selected by the current projection.")
	}
	if observation.Status == "MIXED_CHOICE" {
		return semanticNeedsMapping("MULTIPLE_VALUE_CHOICES", "This concept uses multiple FHIR value choices; choose a value arm before adding it.")
	}

	switch catalog.SemanticObservationRoleOf(observation) {
	case catalog.SemanticRoleIdentifier:
		return resolveIdentifierSelectionPlan(observation)
	case catalog.SemanticRoleExtension:
		return resolveExtensionSelectionPlan(observation)
	case catalog.SemanticRoleCodedValue:
		return resolveCodedValueSelectionPlan(observation)
	case catalog.SemanticRoleCategoricalSlot:
		return resolveCategoricalSelectionPlan(observation)
	case catalog.SemanticRoleStructuredSlot:
		logicalType, ok := fhirschema.ResolveTerminalLogicalType(observation.Source.Type, observation.Value.Selector)
		if !ok || logicalType != "object" {
			return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The structured field is not represented by the active schema.")
		}
		return semanticReadyPlan(ColumnSource{Kind: SourceField, Field: &FieldSource{Path: observation.Value.Selector}}, logicalType, observation.Status)
	default:
		return semanticUnsupported("SEMANTIC_RULE_UNSUPPORTED", "Loom does not have an authoring rule for this observed FHIR structure.")
	}
}

func resolveCategoricalSelectionPlan(observation catalog.SemanticObservation) SemanticSelectionPlan {
	if observation.Status != "SUPPORTED" && observation.Status != "DATA_QUALITY_WARNING" {
		return semanticStatusPlan(observation.Status)
	}
	resourceType := strings.TrimSpace(observation.Source.Type)
	valuePath := fhirschema.CanonicalizePath(observation.Value.Selector)
	logicalType := firstNonBlank(observation.LogicalType, observation.Value.Type)
	if resourceType == "" || valuePath == "" || logicalType == "" {
		return semanticUnsupported("CATEGORICAL_SLOT_INCOMPLETE", "The catalog observation does not contain a complete categorical field binding.")
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, valuePath)
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
		return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The categorical value path does not resolve to a scalar in the generated FHIR schema.")
	}
	keyPath := fhirschema.CanonicalizePath(observation.Key.Selector)
	directCategoricalField := keyPath == valuePath
	if !directCategoricalField && strings.TrimSpace(observation.Key.System) == "" {
		_, keyIsObject := fhirschema.ResolvePath(resourceType, keyPath)
		directCategoricalField = !keyIsObject
	}
	if directCategoricalField {
		return semanticReadyPlan(ColumnSource{Kind: SourceField, Field: &FieldSource{Path: valuePath}}, logicalType, observation.Status)
	}
	if strings.TrimSpace(observation.Key.System) == "" {
		return semanticNeedsMapping("CATEGORICAL_SYSTEM_MISSING", "This categorical Coding has no system namespace; select or map an explicit Coding system.")
	}
	ownerPath := strings.Trim(strings.TrimSpace(observation.OwningScope), ".")
	if ownerPath == keyPath {
		// A singleton Coding is itself the owner; its key selector is rooted at
		// the resource rather than an empty selector relative to itself.
		ownerPath = ""
	}
	binding := fhirschema.CategoricalBinding{
		OwnerPath:         ownerPath,
		KeyPath:           keyPath,
		SystemPath:        "system",
		ValuePath:         "code",
		ValueFallback:     []string{"display"},
		LogicalType:       logicalType,
		ValuePresentation: firstNonBlank(observation.Value.Presentation, fhirschema.ValuePresentationDisplayOrCode),
	}
	checked, err := fhirschema.ValidateCategoricalBinding(resourceType, binding)
	if err != nil {
		return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The observed categorical paths do not match the generated FHIR schema.")
	}
	binding.OwnerPath = checked.OwnerSelector.CanonicalPath()
	binding.KeyPath = qualifySemanticPath(binding.OwnerPath, checked.KeySelector.CanonicalPath())
	binding.SystemPath = checked.SystemSelector.CanonicalPath()
	binding.ValuePath = checked.ValueSelector.CanonicalPath()
	binding.ValueFallback = make([]string, 0, len(checked.ValueFallbacks))
	for _, fallback := range checked.ValueFallbacks {
		binding.ValueFallback = append(binding.ValueFallback, fallback.CanonicalPath())
	}
	binding.LogicalType = checked.LogicalType
	binding.ValuePresentation = checked.ValuePresentation
	source := ColumnSource{Kind: SourceCategoricalBySystem, Categorical: &CategoricalSource{Binding: binding, System: observation.Key.System}}
	return semanticReadyPlan(source, checked.LogicalType, observation.Status)
}

func resolveIdentifierSelectionPlan(observation catalog.SemanticObservation) SemanticSelectionPlan {
	if observation.Status == "UNRESOLVED_SYSTEM" || strings.TrimSpace(observation.Key.System) == "" {
		return semanticNeedsMapping("IDENTIFIER_SYSTEM_MISSING", "This Identifier has no system URI to select; it needs an explicit identifier namespace.")
	}
	if observation.Status != "SUPPORTED" && observation.Status != "DATA_QUALITY_WARNING" {
		return semanticStatusPlan(observation.Status)
	}
	ownerPath := strings.TrimSpace(observation.OwningScope)
	systemPath, systemOK := relativeSemanticPath(ownerPath, observation.Key.Selector)
	valuePath, valueOK := relativeSemanticPath(ownerPath, observation.Value.Selector)
	logicalType := firstNonBlank(observation.LogicalType, observation.Value.Type)
	if ownerPath == "" || !systemOK || !valueOK || strings.TrimSpace(observation.Source.Type) == "" || logicalType == "" {
		return semanticUnsupported("IDENTIFIER_BINDING_INCOMPLETE", "The catalog observation does not contain a complete Identifier system/value pair.")
	}
	binding := fhirschema.IdentifierBinding{
		OwnerPath: ownerPath, SystemPath: systemPath, ValuePath: valuePath,
		SystemURI: observation.Key.System, LogicalType: logicalType,
	}
	checked, err := fhirschema.ValidateIdentifierBinding(observation.Source.Type, binding)
	if err != nil {
		return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The observed Identifier paths do not match the generated FHIR schema.")
	}
	binding.OwnerPath = checked.OwnerSelector.CanonicalPath()
	binding.SystemURI = checked.SystemURI
	binding.LogicalType = checked.LogicalType
	source := ColumnSource{Kind: SourceIdentifierBySystem, Lookup: &LookupSource{Identifier: &binding}}
	return semanticReadyPlan(source, logicalType, observation.Status)
}

func resolveExtensionSelectionPlan(observation catalog.SemanticObservation) SemanticSelectionPlan {
	if observation.Status != "SUPPORTED" && observation.Status != "DATA_QUALITY_WARNING" {
		return semanticStatusPlan(observation.Status)
	}
	ownerPath := strings.TrimSpace(observation.OwningScope)
	valuePath, valueOK := relativeSemanticPath(ownerPath, observation.Value.Selector)
	logicalType := firstNonBlank(observation.LogicalType, observation.Value.Type)
	if ownerPath == "" || !valueOK || logicalType == "" || len(observation.ExtensionURLPath) == 0 || strings.TrimSpace(observation.ChoiceArm) == "" {
		return semanticUnsupported("EXTENSION_BINDING_INCOMPLETE", "The catalog observation does not contain a complete Extension URL/value binding.")
	}
	binding := fhirschema.ExtensionBinding{
		OwnerPath: ownerPath, URLPath: append([]string(nil), observation.ExtensionURLPath...),
		ValuePath: valuePath, ChoiceArms: []string{observation.ChoiceArm}, LogicalType: logicalType,
		ValuePresentation: observation.Value.Presentation,
	}
	checked, err := fhirschema.ValidateExtensionBinding(observation.Source.Type, binding)
	if err != nil {
		return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The observed Extension paths do not match the generated FHIR schema.")
	}
	binding.ValuePath = checked.ValueSelector.CanonicalPath()
	binding.ChoiceArms = append([]string(nil), checked.ChoiceArms...)
	binding.LogicalType = checked.LogicalType
	if checked.UnitSelector != nil {
		binding.UnitPath = checked.UnitSelector.CanonicalPath()
	}
	source := ColumnSource{Kind: SourceExtensionByURL, Lookup: &LookupSource{Extension: &binding}}
	return semanticReadyPlan(source, logicalType, observation.Status)
}

func resolveCodedValueSelectionPlan(observation catalog.SemanticObservation) SemanticSelectionPlan {
	if strings.TrimSpace(observation.Key.Version) != "" {
		return semanticUnsupported("CODED_VALUE_VERSION_UNSUPPORTED", "Version-specific coding selections are not supported by the current compiler.")
	}
	if observation.Status == "UNRESOLVED_SYSTEM" || strings.TrimSpace(observation.Key.System) == "" {
		return semanticNeedsMapping("CODED_VALUE_SYSTEM_MISSING", "This coded value has no coding system URI; select or map its terminology identity.")
	}
	if observation.Status == "UNRESOLVED_CODE" || strings.TrimSpace(observation.Key.Code) == "" {
		return semanticNeedsMapping("CODED_VALUE_CODE_MISSING", "This coded value has no coding code; select or map its terminology identity.")
	}
	if observation.Status != "SUPPORTED" && observation.Status != "DATA_QUALITY_WARNING" {
		return semanticStatusPlan(observation.Status)
	}
	ownerPath := strings.Trim(strings.TrimSpace(observation.OwningScope), ".")
	keyPath := strings.Trim(strings.TrimSpace(observation.Key.Selector), ".")
	valuePath := strings.Trim(strings.TrimSpace(observation.Value.Selector), ".")
	logicalType := firstNonBlank(observation.LogicalType, observation.Value.Type)
	if strings.TrimSpace(observation.Source.Type) == "" || keyPath == "" || valuePath == "" || logicalType == "" {
		return semanticUnsupported("CODED_VALUE_BINDING_INCOMPLETE", "The catalog observation does not contain a complete owner, coding key, and scalar value binding.")
	}
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: ownerPath, KeyPath: qualifySemanticPath(ownerPath, keyPath),
		SystemPath: "system", CodePath: "code", ValuePath: valuePath,
		LogicalType:       logicalType,
		ValuePresentation: observation.Value.Presentation,
	}
	if observation.ChoiceArm != "" {
		binding.ChoiceArms = []string{observation.ChoiceArm}
	}
	checked, err := fhirschema.ValidateCorrelatedBinding(observation.Source.Type, binding)
	if err != nil {
		return semanticUnsupported("FHIR_BINDING_UNSUPPORTED", "The observed coded-value paths do not match the generated FHIR schema.")
	}
	binding.OwnerPath = checked.OwnerSelector.CanonicalPath()
	binding.KeyPath = qualifySemanticPath(binding.OwnerPath, checked.KeySelector.CanonicalPath())
	binding.SystemPath = checked.SystemSelector.CanonicalPath()
	binding.CodePath = checked.CodeSelector.CanonicalPath()
	binding.ValuePath = checked.ValueSelector.CanonicalPath()
	binding.ChoiceArms = append([]string(nil), checked.ChoiceArms...)
	binding.LogicalType = checked.LogicalType
	if checked.UnitSelector != nil {
		binding.UnitPath = checked.UnitSelector.CanonicalPath()
	}
	lookup := &LookupSource{Binding: &binding, Key: &fhirschema.CorrelatedKey{System: observation.Key.System, Code: observation.Key.Code}}
	source := ColumnSource{Kind: SourceCodedValue, Lookup: lookup}
	return semanticReadyPlan(source, checked.LogicalType, observation.Status)
}

func relativeSemanticPath(ownerPath, fullPath string) (string, bool) {
	ownerPath = strings.Trim(strings.TrimSpace(ownerPath), ".")
	fullPath = strings.Trim(strings.TrimSpace(fullPath), ".")
	if ownerPath == "" {
		return fullPath, fullPath != ""
	}
	prefix := ownerPath + "."
	if ownerPath == "" || !strings.HasPrefix(fullPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(fullPath, prefix), true
}

func qualifySemanticPath(ownerPath, relativePath string) string {
	ownerPath = strings.Trim(strings.TrimSpace(ownerPath), ".")
	relativePath = strings.Trim(strings.TrimSpace(relativePath), ".")
	if ownerPath == "" {
		return relativePath
	}
	if relativePath == "" {
		return ownerPath
	}
	return ownerPath + "." + relativePath
}

func semanticReadyPlan(source ColumnSource, logicalType, observationStatus string) SemanticSelectionPlan {
	readiness := SemanticSelectionReadiness{Status: SemanticReadinessReady, Code: "READY", Message: "This semantic field is ready to add."}
	if observationStatus == "DATA_QUALITY_WARNING" {
		readiness = SemanticSelectionReadiness{Status: SemanticReadinessReadyWithWarning, Code: "DATA_QUALITY_WARNING", Message: "The FHIR structure is usable, but the observed data has a known quality warning."}
	}
	return SemanticSelectionPlan{Source: &source, LogicalType: logicalType, Readiness: readiness}
}

func semanticNeedsMapping(code, message string) SemanticSelectionPlan {
	return SemanticSelectionPlan{Readiness: SemanticSelectionReadiness{Status: SemanticReadinessNeedsMapping, Code: code, Message: message}}
}

func semanticUnsupported(code, message string) SemanticSelectionPlan {
	return SemanticSelectionPlan{Readiness: SemanticSelectionReadiness{Status: SemanticReadinessUnsupported, Code: code, Message: message}}
}

func semanticStatusPlan(status string) SemanticSelectionPlan {
	switch status {
	case "UNRESOLVED_SYSTEM":
		return semanticNeedsMapping("SEMANTIC_SYSTEM_MISSING", "This concept has no system URI; select or map its terminology identity.")
	case "UNRESOLVED_CODE":
		return semanticNeedsMapping("SEMANTIC_CODE_MISSING", "This concept has no code; select or map its terminology identity.")
	case "UNRESOLVED_BINDING":
		return semanticNeedsMapping("SEMANTIC_BINDING_MISSING", "This catalog row has no complete terminology binding; map it to a FHIR concept.")
	case "MIXED_CHOICE":
		return semanticNeedsMapping("MULTIPLE_VALUE_CHOICES", "This concept uses multiple FHIR value choices; choose a value arm before adding it.")
	default:
		return semanticUnsupported("SEMANTIC_BINDING_UNSUPPORTED", "The observed semantic binding is not supported by the current compiler.")
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func ensureSemanticRoute(document *Document, catalogSnapshot CatalogSnapshot, commandID string, commandIndex, selectionIndex int, edgeIDs []string, targetResource string) (string, error) {
	if document == nil || document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return "", fmt.Errorf("table root route is invalid")
	}
	if catalogSnapshot.RoutePolicy.MaxHops != nil && len(edgeIDs) > *catalogSnapshot.RoutePolicy.MaxHops {
		return "", fmt.Errorf("ROUTE_TOO_LONG: route exceeds capability route policy (maxSteps=%d, steps=%d)", *catalogSnapshot.RoutePolicy.MaxHops, len(edgeIDs))
	}
	current := &document.Route
	for edgeIndex, edgeID := range edgeIDs {
		edge, found := catalogEdge(catalogSnapshot, edgeID)
		if !found {
			return "", fmt.Errorf("route edge %q was not found", edgeID)
		}
		from, fromFound := catalogNode(catalogSnapshot, edge.FromNodeID)
		to, toFound := catalogNode(catalogSnapshot, edge.ToNodeID)
		currentNodeID, currentNodeFound := catalogNodeIDForOccurrence(document.Route, current.OccurrenceID, catalogSnapshot)
		if !fromFound || !toFound || !currentNodeFound || edge.FromNodeID != currentNodeID || from.ResourceType != current.ResourceType {
			return "", fmt.Errorf("route edge %q does not extend resource %q", edgeID, current.ResourceType)
		}
		if from.ResourceType == to.ResourceType && !catalogSnapshot.RoutePolicy.AllowSelfLoops {
			return "", fmt.Errorf("route edge %q is a self-loop but self-loops are not allowed", edgeID)
		}
		if !catalogSnapshot.RoutePolicy.AllowRepeatedEdges {
			used, err := routePathUsesCatalogEdge(document.Route, current.OccurrenceID, edgeID, catalogSnapshot)
			if err != nil {
				return "", fmt.Errorf("route parent does not resolve to an exact path: %w", err)
			}
			if used {
				return "", fmt.Errorf("route edge %q is already used in this route", edgeID)
			}
		}
		matchedIndex := -1
		for i := range current.Children {
			child := &current.Children[i]
			if child.ResourceType != to.ResourceType || child.Relationship != edge.Label {
				continue
			}
			existingEdgeID := child.CatalogEdgeID
			if existingEdgeID == "" {
				legacyEdge, err := resolveCatalogRouteEdge(catalogSnapshot, edge.FromNodeID, *current, *child)
				if err != nil {
					return "", fmt.Errorf("route edge %q has an ambiguous legacy occurrence: %w", edgeID, err)
				}
				child.CatalogEdgeID = legacyEdge.ID
				existingEdgeID = legacyEdge.ID
			}
			if existingEdgeID != edge.ID {
				continue
			}
			if matchedIndex >= 0 {
				return "", fmt.Errorf("route edge %q matches multiple existing route occurrences", edgeID)
			}
			matchedIndex = i
		}
		if matchedIndex < 0 {
			occurrenceID := commandGeneratedID("occ_", "semantic-route/v1", document.Output.ID, commandID, commandIndex, selectionIndex, edgeIndex, edge.ID)
			current.Children = append(current.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, CatalogEdgeID: edge.ID, Relationship: edge.Label})
			matchedIndex = len(current.Children) - 1
		}
		current = &current.Children[matchedIndex]
	}
	if current.ResourceType != targetResource {
		return "", fmt.Errorf("selected route ends at %q, not the source resource %q", current.ResourceType, targetResource)
	}
	return current.OccurrenceID, nil
}
