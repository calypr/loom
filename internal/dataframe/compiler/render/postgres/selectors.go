package postgres

// This file registers typed selector extraction as an implemented, closed
// PostgreSQL operation (pg-sql-03). It is the renderer-facing capability
// registration point for the operation named by the physical IR's
// PhysicalExtract: one scoped direct root scan whose RETURN projects typed
// scope columns, scalar-cardinality FHIR selector values extracted from the
// agreed JSONB document column (ir.PostgresResourceLogicalTable, migration
// 000002_fhir_resource.sql), and boolean path-presence sidecars carrying the
// evidence null/missing-sensitive operations need. The value and presence
// expressions themselves are defined once in null_semantics.go; this file is
// shape validation and registration, not a second SQL builder.
//
// The accepted selector shape mirrors what lowering emits in production and
// is re-read, never trusted: PhysicalExpression{Kind: EXTRACT, Cardinality:
// SCALAR, NullBehavior: registered, Extract: PhysicalExtract{Source:
// {Variable: <root>, Path: ["payload"]}, ResourceType: <scanned type>,
// Selector: direct property path}} — the exact construction in
// internal/dataframe/compiler/lower/root_projection.go
// (lowerSemanticFieldProjection and physicalProjectionPresence) over the
// root plan shape in internal/dataframe/compiler/lower/plan_orchestration.go
// (root_collection bind plus ROOT_SCAN). Anything outside it — array and
// object cardinality, fallbacks, prepared sets, unit normalization,
// selector filters, repeated or indexed steps, DISTINCT, conditional-array
// modes, nested document sources, or a resource type that contradicts the
// scanned row — is a capability gap: a typed ErrUnsupportedPostgresPlan
// refusal that yields no SQL. There is no fallback to the AQL renderer and
// no AQL-to-SQL conversion here.
//
// Planner-facing command registration (the compiler's target-aware PostgreSQL compile path)
// stays fail-closed for PostgreSQL exactly as in pg-sql-01/02: this entry
// point — not command dispatch — is where the selector-extraction operation
// is implemented and registered.
//
// Statement assembly is inherited unchanged from RenderPhysicalPlan, which
// validates the plan through ir.Validate first (so selector paths, resource
// types, and presence proofs are schema-checked before any rendering) and
// then assembles one deterministic statement. classifyExtractionProjections
// is the single projection-contract definition: the closed slice applies it
// before delegation and renderReturn applies it again during assembly, so
// every entry point enforces the same contract (defense in depth, not two
// contracts).

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderSelectorExtraction renders exactly one scoped direct root scan with
// typed selector extraction onto the trusted PostgreSQL storage binding.
//
// The accepted plan shape is exactly two operations: a leading ROOT_SCAN
// with a direct scan payload (no population, cohort, or candidate-seed
// source) followed by a terminal RETURN. Every RETURN projection is exactly
// one of: a direct typed scope column of the root row (the root-scan form),
// a scalar-cardinality EXTRACT selector over the root row document with a
// registered null behavior, or a PresenceOutput sidecar proving the
// presence of a selector path this RETURN also renders. Selector resource
// types must agree with the scanned root type — the same identity the
// canonical lowering proves for the root_collection bind — and every
// selector path segment is bound as data, never concatenated into SQL text.
//
// Plan scope binds are re-read through the canonical mapping in scope.go,
// never trusted downstream. The result is deterministic: the same plan and
// binding always yield byte-identical SQL and value-identical ordered
// arguments. Failures are typed: ErrUnsupportedPostgresPlan for shapes
// outside this slice, ErrInvalidPostgresPlanScope for missing or
// contradictory scope binds, and ir.ErrUnregisteredPostgresIdentifier for a
// binding that did not come from ir.NewPostgresStorageBinding. A rejected
// plan never produces SQL and never routes to AQL.
func RenderSelectorExtraction(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateSelectorExtractionPlan(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	// The closed shape passed, so RenderPhysicalPlan's own gates (binding
	// trust, plan validation including schema validation of selector
	// paths, engine/ClickHouse/stage refusals, scope re-reading,
	// deterministic assembly) all operate over a plan this slice has
	// already pinned to one direct root scan with classified projections.
	// There is no second SQL builder here by design: one statement-shape
	// authority keeps this registration and the full read spine
	// byte-identical for the shapes they share.
	return RenderPhysicalPlan(plan, storage)
}

// extractionProjectionKind is the classification of one RETURN projection
// inside the PostgreSQL render surface.
type extractionProjectionKind uint8

const (
	// extractionDirectValue is a direct ir.PhysicalValue projection — the
	// root-scan form (typed columns) and the payload-path form of the
	// full read spine.
	extractionDirectValue extractionProjectionKind = iota
	// extractionValueExpression is a VALUE expression wrapping exactly one
	// direct value — the full read spine's expression form.
	extractionValueExpression
	// extractionSelector is a scalar-cardinality EXTRACT expression over a
	// row document — the operation this slice registers.
	extractionSelector
	// extractionPresenceSidecar is a PresenceOutput projection emitting the
	// boolean path-presence proof for a selector this RETURN renders.
	extractionPresenceSidecar
	// extractionCallExpression is a SCALAR-cardinality PRESERVE_NULL CALL
	// expression projection — the derived scalar surface calls.go registers
	// (pg-sql-10) on the full read spine.
	extractionCallExpression
)

// classifiedExtractionProjection is one fully checked RETURN projection:
// the original projection plus the contract facts assembly needs, so
// rendering never re-derives (or diverges from) what validation decided.
type classifiedExtractionProjection struct {
	kind       extractionProjectionKind
	projection ir.PhysicalProjection
	// extractionSelector facts.
	expression *ir.PhysicalExpression
	path       []string
	// extractionPresenceSidecar facts.
	presence     *ir.PhysicalProjectionPresence
	presencePath []string
}

// extractionEvidence identifies one selector value/presence pair: the row
// variable and the document path. Presence sidecars must agree with a
// rendered selector in both, so the pairing is provable from the plan alone.
type extractionEvidence struct {
	variable string
	path     string
}

func pathEvidenceKey(path []string) string {
	return strings.Join(path, "\x1f")
}

// classifyExtractionProjections applies the single projection contract to
// every RETURN projection in order, resolving row variables through
// rowType. It refuses hidden projections, unpaired presence sidecars,
// selector resource-type contradictions, and every shape outside the
// registered forms with typed ErrUnsupportedPostgresPlan errors that name
// the boundary they enforce.
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
			// The selector's schema provenance must name the same resource
			// the scan reads: lowering derives both from one semantic node
			// (lower/root_projection.go uses node.ResourceType while
			// lower/plan_orchestration.go binds root_collection to the root
			// resource type), so a mismatch means the selector was schema
			// validated against a document this row is not.
			if c.expression.Extract.ResourceType != resourceType {
				return nil, fmt.Errorf("%w: projection %q validates selector against resource type %q, but the row is scanned as %q",
					ErrUnsupportedPostgresPlan, projection.Name, c.expression.Extract.ResourceType, resourceType)
			}
			extracted[extractionEvidence{variable: source.Variable, path: pathEvidenceKey(c.path)}] = true
		case extractionCallExpression:
			// Every row variable a CALL operand reads must bind a readable
			// row, and every EXTRACT operand's schema provenance must name
			// the type its row is scanned as — the same identities the
			// selector and value arms prove; the walk is calls.go's so the
			// operand classes stay defined in one place.
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
	// Presence sidecars must agree with a rendered selector: the proof
	// exists to explain a projection result, never on its own.
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

// classifyExtractionProjection enforces the structural half of the
// projection contract (everything checkable without plan context).
func classifyExtractionProjection(projection ir.PhysicalProjection) (classifiedExtractionProjection, error) {
	if strings.TrimSpace(projection.Name) == "" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set",
			ErrUnsupportedPostgresPlan)
	}
	if projection.Hidden {
		// The canonical preview source identity column (compiler/output and
		// recipe_execution attach it when IncludeSourceIdentity is set) is
		// the one hidden projection with a registered PostgreSQL rendering:
		// exactly the root row's logical id, published through the internal
		// column contract and never as public output.
		if projection.Name == ir.PreviewSourceResourceIDColumn &&
			projection.Expression == nil && !projection.PresenceOutput && projection.Presence == nil &&
			projection.Value.Variable != "" && projection.Value.BindKey == "" &&
			len(projection.Value.Path) == 1 && (projection.Value.Path[0] == "id" || projection.Value.Path[0] == "_key") {
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
			// The deep shape contract (posture, registered name, arity,
			// TargetKind matrix) is owned by calls.go — this classifier only
			// recognizes the kind and hands it through; rendering never
			// re-derives the classification itself.
			if err := validateCallProjection(projection); err != nil {
				return classifiedExtractionProjection{}, err
			}
			return classifiedExtractionProjection{
				kind:       extractionCallExpression,
				projection: projection,
				expression: expression,
			}, nil
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

// validateSelectorExtraction enforces the closed selector payload: the
// scalar direct-property form this slice registers, rejecting every
// richer selector shape by name. The selector itself is schema-validated
// earlier by ir.Validate (validatePhysicalExtract against the generated
// FHIR schema); this is the renderer-side boundary that keeps the slice
// closed, including the re-check of the frozen path contract.
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
		// Inert executor-side metadata is legal only when it proves the
		// selector's own source path: the same single-path pairing the
		// presence sidecar form requires, so a plan can never smuggle a
		// fallback-shaped or foreign-path presence claim into a value
		// projection.
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

// validateSelectorExtractionPlan enforces the closed plan slice before any
// rendering work begins. Every refusal is an ErrUnsupportedPostgresPlan so
// callers classify the whole family as a capability gap, and the root-scan
// payload checks run first so a malformed plan can never reach a payload
// dereference downstream — the same ordering discipline
// validateRootScanOperation applies.
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
		case extractionSelector, extractionPresenceSidecar:
		}
	}
	return nil
}
