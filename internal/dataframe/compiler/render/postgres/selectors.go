package postgres

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func RenderSelectorExtraction(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateSelectorExtractionPlan(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, storage)
}

type extractionProjectionKind uint8

const (
	extractionDirectValue extractionProjectionKind = iota
	extractionValueExpression
	extractionSelector
	extractionPresenceSidecar
	extractionCallExpression
	extractionObject
	extractionSlice
	extractionUnitNormalized
	extractionAggregate
)

type classifiedExtractionProjection struct {
	kind         extractionProjectionKind
	projection   ir.PhysicalProjection
	expression   *ir.PhysicalExpression
	path         []string
	presence     *ir.PhysicalProjectionPresence
	presencePath []string
	unit         *unitProjectionFacts
}

type extractionEvidence struct {
	variable string
	path     string
}

func pathEvidenceKey(path []string) string {
	return strings.Join(path, "\x1f")
}

func classifyExtractionProjections(projections []ir.PhysicalProjection,
	rowType func(variable string) (resourceType string, ok bool)) ([]classifiedExtractionProjection, error) {
	classified := make([]classifiedExtractionProjection, 0, len(projections))
	extracted := make(map[extractionEvidence]bool)
	for index := range projections {
		projection := projections[index]
		c, err := classifyExtractionProjection(projection)
		if err != nil {
			return nil, err
		}
		switch c.kind {
		case extractionDirectValue:
			if _, ok := rowType(c.projection.Value.Variable); !ok {
				return nil, fmt.Errorf("%w: projection %q reads variable %q, which does not bind a readable row",
					ErrUnsupportedPostgresPlan, projection.Name, c.projection.Value.Variable)
			}
		case extractionValueExpression:
			if _, ok := rowType(c.projection.Expression.Value.Variable); !ok {
				return nil, fmt.Errorf("%w: projection %q reads variable %q, which does not bind a readable row",
					ErrUnsupportedPostgresPlan, projection.Name, c.projection.Expression.Value.Variable)
			}
		case extractionSelector:
			source := c.expression.Extract.Source
			resourceType, ok := rowType(source.Variable)
			if !ok {
				return nil, fmt.Errorf("%w: projection %q extracts from variable %q, which does not bind a readable row",
					ErrUnsupportedPostgresPlan, projection.Name, source.Variable)
			}
			if c.expression.Extract.ResourceType != resourceType {
				return nil, fmt.Errorf("%w: projection %q validates selector against resource type %q, but the row is scanned as %q",
					ErrUnsupportedPostgresPlan, projection.Name, c.expression.Extract.ResourceType, resourceType)
			}
			extracted[extractionEvidence{variable: source.Variable, path: pathEvidenceKey(c.path)}] = true
		case extractionUnitNormalized:
			resourceType, ok := rowType(c.unit.sourceVariable)
			if !ok {
				return nil, fmt.Errorf("%w: projection %q extracts from variable %q, which does not bind a readable row",
					ErrUnsupportedPostgresPlan, projection.Name, c.unit.sourceVariable)
			}
			if c.unit.resourceType != resourceType {
				return nil, fmt.Errorf("%w: projection %q validates unit selectors against resource type %q, but the row is scanned as %q",
					ErrUnsupportedPostgresPlan, projection.Name, c.unit.resourceType, resourceType)
			}
		case extractionCallExpression:
			if err := forEachCallOperandRowVariable(*projection.Expression,
				func(variable string, extract *ir.PhysicalExtract) error {
					resourceType, ok := rowType(variable)
					if !ok {
						return fmt.Errorf("%w: projection %q reads variable %q, which does not bind a readable row",
							ErrUnsupportedPostgresPlan, projection.Name, variable)
					}
					if extract != nil && extract.ResourceType != resourceType {
						return fmt.Errorf("%w: projection %q validates selector against resource type %q, but the row is scanned as %q",
							ErrUnsupportedPostgresPlan, projection.Name, extract.ResourceType, resourceType)
					}
					return nil
				}); err != nil {
				return nil, err
			}
		case extractionPresenceSidecar:
			if _, ok := rowType(c.presence.Source.Variable); !ok {
				return nil, fmt.Errorf("%w: presence projection %q proves variable %q, which does not bind a readable row",
					ErrUnsupportedPostgresPlan, projection.Name, c.presence.Source.Variable)
			}
		}
		classified = append(classified, c)
	}
	for _, c := range classified {
		if c.kind != extractionPresenceSidecar {
			continue
		}
		if !extracted[extractionEvidence{variable: c.presence.Source.Variable, path: pathEvidenceKey(c.presencePath)}] {
			return nil, fmt.Errorf("%w: presence projection %q proves path %v of variable %q, which no selector in this RETURN extracts: presence metadata must agree with a rendered projection",
				ErrUnsupportedPostgresPlan, c.projection.Name, c.presencePath, c.presence.Source.Variable)
		}
	}
	return classified, nil
}

func hiddenRootKeyRead(projection ir.PhysicalProjection) bool {
	return projection.Expression == nil && !projection.PresenceOutput && projection.Presence == nil &&
		projection.Value.Variable != "" && projection.Value.BindKey == "" &&
		len(projection.Value.Path) == 1 && (projection.Value.Path[0] == "id" || projection.Value.Path[0] == "_key")
}

func hiddenAuthResourcePathRead(projection ir.PhysicalProjection) bool {
	return projection.Expression == nil && !projection.PresenceOutput && projection.Presence == nil &&
		projection.Value.Variable != "" && projection.Value.BindKey == "" &&
		len(projection.Value.Path) == 1 && projection.Value.Path[0] == "auth_resource_path"
}

func classifyExtractionProjection(projection ir.PhysicalProjection) (classifiedExtractionProjection, error) {
	if strings.TrimSpace(projection.Name) == "" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set",
			ErrUnsupportedPostgresPlan)
	}
	if projection.Hidden {
		if projection.Name == ir.PreviewSourceResourceIDColumn && hiddenRootKeyRead(projection) {
			return classifiedExtractionProjection{kind: extractionDirectValue, projection: projection}, nil
		}
		if projection.Name == "auth_resource_path" && hiddenAuthResourcePathRead(projection) {
			return classifiedExtractionProjection{kind: extractionDirectValue, projection: projection}, nil
		}
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q is hidden executor-side metadata with no registered PostgreSQL rendering",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	hasValue := projection.Value.Variable != "" || projection.Value.BindKey != "" || len(projection.Value.Path) != 0
	if projection.PresenceOutput {
		if projection.Presence == nil {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: presence projection %q has no presence proof to emit",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if hasValue || projection.Expression != nil {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: presence projection %q must carry only its typed presence proof",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		presence := projection.Presence
		if presence.Source.BindKey != "" {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: presence projection %q must prove a row path, not a bind value",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if len(presence.Source.Path) != 1 || presence.Source.Path[0] != "payload" {
			return classifiedExtractionProjection{}, fmt.Errorf(`%w: presence projection %q must prove a path of the row document at ["payload"], the exact source form lowering emits; got %v`,
				ErrUnsupportedPostgresPlan, projection.Name, presence.Source.Path)
		}
		if len(presence.Paths) != 1 {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: presence projection %q carries %d candidate paths; multi-path presence proofs depend on selector fallbacks, which are a separate capability",
				ErrUnsupportedPostgresPlan, projection.Name, len(presence.Paths))
		}
		return classifiedExtractionProjection{
			kind:         extractionPresenceSidecar,
			projection:   projection,
			presence:     presence,
			presencePath: append([]string(nil), presence.Paths[0]...),
		}, nil
	}
	if projection.Expression != nil {
		if hasValue {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q sets both a direct value and an expression; exactly one is allowed",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		expression := projection.Expression
		switch expression.Kind {
		case ir.PhysicalValueExpression:
			if expression.Value == nil {
				return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q uses a VALUE expression with no value",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			if projection.Presence != nil {
				return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q carries presence metadata on a value expression; presence proofs pair with selector extractions only",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			return classifiedExtractionProjection{kind: extractionValueExpression, projection: projection}, nil
		case ir.PhysicalExtractExpression:
			if expression.Extract != nil && expression.Extract.UnitNormalization != nil {
				return classifyUnitNormalizedProjection(projection)
			}
			path, err := validateSelectorExtraction(projection)
			if err != nil {
				return classifiedExtractionProjection{}, err
			}
			return classifiedExtractionProjection{
				kind:       extractionSelector,
				projection: projection,
				expression: expression,
				path:       path,
			}, nil
		case ir.PhysicalCallExpression:
			if err := validateCallProjection(projection); err != nil {
				return classifiedExtractionProjection{}, err
			}
			return classifiedExtractionProjection{
				kind:       extractionCallExpression,
				projection: projection,
				expression: expression,
			}, nil
		case ir.PhysicalAggregateExpression:
			if projection.Presence != nil {
				return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q carries presence metadata on an AGGREGATE expression; presence proofs pair with selector extractions only",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			// Shape validation continues in renderSetAggregateExpression, which needs the
			// registered set bodies and projections; the classifier is row-scoped.
			return classifiedExtractionProjection{
				kind:       extractionAggregate,
				projection: projection,
				expression: expression,
			}, nil
		case ir.PhysicalObjectExpression, ir.PhysicalSliceExpression:
			if projection.Presence != nil {
				return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q carries presence metadata on a %s expression; presence proofs pair with selector extractions only",
					ErrUnsupportedPostgresPlan, projection.Name, string(expression.Kind))
			}
			if expression.Kind == ir.PhysicalObjectExpression {
				if expression.Object == nil {
					return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q uses an OBJECT expression with no payload",
						ErrUnsupportedPostgresPlan, projection.Name)
				}
				return classifiedExtractionProjection{kind: extractionObject, projection: projection}, nil
			}
			if expression.Slice == nil {
				return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q uses a SLICE expression with no payload",
					ErrUnsupportedPostgresPlan, projection.Name)
			}
			return classifiedExtractionProjection{kind: extractionSlice, projection: projection}, nil
		default:
			return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q uses expression kind %q, which has no registered PostgreSQL rendering",
				ErrUnsupportedPostgresPlan, projection.Name, string(expression.Kind))
		}
	}
	if !hasValue {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q must read exactly one typed row value",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Value.BindKey != "" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q must read a row value, not a bind value",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Presence != nil {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q carries presence metadata on a direct value; presence proofs pair with selector extractions only",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	return classifiedExtractionProjection{kind: extractionDirectValue, projection: projection}, nil
}

func validateSelectorExtraction(projection ir.PhysicalProjection) ([]string, error) {
	expression := projection.Expression
	extract := expression.Extract
	if extract == nil {
		return nil, fmt.Errorf("%w: projection %q: EXTRACT expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality {
		return nil, fmt.Errorf("%w: projection %q: selector extraction is registered for SCALAR cardinality only; array and object selector projections belong to a later capability",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	switch expression.NullBehavior {
	case ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull:
	default:
		return nil, fmt.Errorf("%w: projection %q: null behavior %q has no registered PostgreSQL extraction form",
			ErrUnsupportedPostgresPlan, projection.Name, string(expression.NullBehavior))
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return nil, fmt.Errorf("%w: projection %q: CONDITIONAL_ARRAY selector mode requires repeated-step rendering, which is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan, projection.Name)
	default:
		return nil, fmt.Errorf("%w: projection %q: unknown selector execution mode %q",
			ErrUnsupportedPostgresPlan, projection.Name, string(extract.ExecutionMode))
	}
	if extract.Distinct {
		return nil, fmt.Errorf("%w: projection %q: DISTINCT projection is meaningful only for array-valued selectors",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Fallbacks) != 0 {
		return nil, fmt.Errorf("%w: projection %q: ordered fallback selectors are a separate capability",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Prepared != nil {
		return nil, fmt.Errorf("%w: projection %q: prepared-set selector references are not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.UnitNormalization != nil {
		return nil, fmt.Errorf("%w: projection %q: unit-normalized extraction is not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Selector.Filter != nil {
		return nil, fmt.Errorf("%w: projection %q: selector CONTAINS filters are not implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Selector.Steps) == 0 {
		return nil, fmt.Errorf("%w: projection %q: the selector is empty", ErrUnsupportedPostgresPlan, projection.Name)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		switch {
		case step.Field == "":
			return nil, fmt.Errorf("%w: projection %q: selector step %d has no property",
				ErrUnsupportedPostgresPlan, projection.Name, index)
		case step.Iterate:
			return nil, fmt.Errorf("%w: projection %q: repeated ([]) selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, projection.Name, index)
		case step.Index != nil:
			return nil, fmt.Errorf("%w: projection %q: indexed selector step %d requires array rendering, which is not registered",
				ErrUnsupportedPostgresPlan, projection.Name, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return nil, fmt.Errorf("%w: projection %q: selector step %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, projection.Name, step.Field)
		}
		path = append(path, step.Field)
	}
	if extract.Source.BindKey != "" {
		return nil, fmt.Errorf("%w: projection %q: the selector source must be a row document, not a bind value",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return nil, fmt.Errorf(`%w: projection %q: the selector source must be the row document at ["payload"], the exact source shape lowering emits (lower/root_projection.go); got %v`,
			ErrUnsupportedPostgresPlan, projection.Name, extract.Source.Path)
	}
	if strings.TrimSpace(extract.ResourceType) == "" {
		return nil, fmt.Errorf("%w: projection %q: selector extraction must name the schema resource type it was validated against",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Presence != nil {
		presence := projection.Presence
		if presence.Source.Variable != extract.Source.Variable || presence.Source.BindKey != "" ||
			!reflect.DeepEqual(presence.Source.Path, extract.Source.Path) {
			return nil, fmt.Errorf("%w: projection %q: presence metadata must prove the selector's own document source",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if len(presence.Paths) != 1 || !reflect.DeepEqual(presence.Paths[0], path) {
			return nil, fmt.Errorf("%w: projection %q: presence metadata must be exactly the selector path; multi-path presence claims belong to the selector-fallback capability",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return path, nil
}

func validateSelectorExtractionPlan(plan ir.PhysicalPlan) error {
	if len(plan.Operations) != 2 {
		return fmt.Errorf(
			"%w: the selector-extraction slice is exactly one ROOT_SCAN followed by one RETURN, got %d operation(s); filters, sorts, limits, traversals, and every other operation belong to the full read spine or to a later capability, not to RenderSelectorExtraction",
			ErrUnsupportedPostgresPlan, len(plan.Operations))
	}
	rootOperation, returnOperation := plan.Operations[0], plan.Operations[1]
	if rootOperation.Kind != ir.PhysicalRootScanOp || rootOperation.RootScan == nil {
		return fmt.Errorf("%w: the leading operation must be ROOT_SCAN with its payload set; the selector-extraction slice never renders a traversal, a collection scan, or a payload-less ROOT_SCAN",
			ErrUnsupportedPostgresPlan)
	}
	if returnOperation.Kind != ir.PhysicalReturnOp || returnOperation.Return == nil {
		return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
	}

	scan := rootOperation.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if scan.Variable == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if scan.CollectionBindKey == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	if len(returnOperation.Return.Projections) == 0 {
		return fmt.Errorf("%w: the selector-extraction RETURN must project at least one selector extraction or typed scope column",
			ErrUnsupportedPostgresPlan)
	}

	classified, err := classifyExtractionProjections(returnOperation.Return.Projections,
		func(variable string) (string, bool) {
			if variable == scan.Variable {
				return resourceType, true
			}
			return "", false
		})
	if err != nil {
		return err
	}
	for _, c := range classified {
		switch c.kind {
		case extractionDirectValue:
			value := c.projection.Value
			if len(value.Path) != 1 || !resourceFilterColumns[canonicalKeyColumn(value.Path[0])] {
				return fmt.Errorf("%w: projection %q must read exactly one typed scope column of the root row; payload values reach PostgreSQL through EXTRACT selectors, not direct values",
					ErrUnsupportedPostgresPlan, c.projection.Name)
			}
		case extractionValueExpression:
			return fmt.Errorf("%w: projection %q uses a VALUE expression; the selector-extraction slice emits typed scope columns, EXTRACT selector values, and presence sidecars",
				ErrUnsupportedPostgresPlan, c.projection.Name)
		case extractionCallExpression:
			return fmt.Errorf("%w: projection %q uses a CALL expression; derived scalar calls register on the full read spine (RenderPhysicalPlan), not the closed selector-extraction slice",
				ErrUnsupportedPostgresPlan, c.projection.Name)
		case extractionUnitNormalized:
			return fmt.Errorf("%w: projection %q uses a unit-normalized extraction; the selector-extraction slice emits typed scope columns, EXTRACT selector values, and presence sidecars — unit normalization belongs to the read spine registration (unit_normalization.go)",
				ErrUnsupportedPostgresPlan, c.projection.Name)
		case extractionObject, extractionSlice:
			return fmt.Errorf("%w: projection %q uses a %s expression; the selector-extraction slice emits typed scope columns, EXTRACT selector values, and presence sidecars — composites belong to the read spine registration (objects.go)",
				ErrUnsupportedPostgresPlan, c.projection.Name, string(c.projection.Expression.Kind))
		case extractionAggregate:
			return fmt.Errorf("%w: projection %q uses an AGGREGATE expression; the selector-extraction slice emits typed scope columns, EXTRACT selector values, and presence sidecars — set reducers belong to the read spine registration (post_window.go)",
				ErrUnsupportedPostgresPlan, c.projection.Name)
		case extractionSelector, extractionPresenceSidecar:
		}
	}
	return nil
}
