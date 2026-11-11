package postgres

// Canonical missing/null/empty semantics for typed selector extraction over
// the agreed JSONB document column (pg-sql-03).
//
// This file is the single definition in this package of how PostgreSQL's
// three-valued logic maps a document path onto the two evidence values a
// null/missing-sensitive operation needs: a typed jsonb VALUE expression and
// a two-valued path-presence PROOF expression. Loom distinguishes three
// source states that naive SQL collapses, and this mapping never collapses
// them beyond the declared per-behavior coercions in the table below, whose
// agreement rules state exactly which stored states remain observable apart
// (storage keeps them distinct too: migration 000002_fhir_resource.sql
// documents payload as nullable so an absent resource object — SQL NULL —
// stays distinct from a stored JSON null):
//
//   - ABSENT: no document, or some parent along the path is missing, not an
//     object, or lacks the next key. PostgreSQL: `#>` yields SQL NULL and the
//     presence proof is false.
//   - PRESENT JSON NULL: the terminal key exists and its stored value is the
//     JSON null scalar. PostgreSQL: `#>` yields the jsonb 'null' scalar (NOT
//     SQL NULL) and the presence proof is true.
//   - PRESENT VALUE: any other stored JSON value — including the empty
//     string, false, numeric zero, the empty array, and an array containing
//     null — reads as its exact jsonb and the presence proof is true.
//
// The value side is always `#>` (jsonb), never `#>>` (text): text
// extraction maps a stored JSON null to SQL NULL and would erase exactly
// the distinction this contract exists to preserve. The three-valued world
// is admitted only in the value column; the presence bit is total — every
// row reads exactly true or false — because a presence metadata column that
// itself went NULL would recreate the collapse one level up.
//
// The scalar-cardinality behavior table this package renders (also pinned
// by golden literals in null_semantics_test.go and executed as a live
// behavior matrix in selectors_test.go):
//
//	stored state     PRESERVE_NULL    OMIT_NULLS    EMPTY_ON_NULL   present
//	absent           SQL NULL         SQL NULL      '""'::jsonb     false
//	JSON null        jsonb 'null'     SQL NULL      '""'::jsonb     true
//	empty array      jsonb '[]'       jsonb '[]'    jsonb '[]'      true
//	[null]           jsonb '[null]'   jsonb '[null]' jsonb '[null]' true
//	""               jsonb '""'       jsonb '""'    jsonb '""'      true
//	false            jsonb 'false'    jsonb 'false' jsonb 'false'   true
//	0                jsonb '0'        jsonb '0'     jsonb '0'       true
//
// Agreement between the two columns is part of the contract, per behavior:
// PRESERVE_NULL reads value IS SQL NULL exactly when presence is false;
// OMIT_NULLS reads value IS SQL NULL exactly when presence is false or the
// stored value was the JSON null scalar; EMPTY_ON_NULL reads the JSON empty
// string exactly when presence is false, the stored value was the JSON null
// scalar, or the stored value was the empty string. Under EMPTY_ON_NULL the
// presence bit separates ABSENT (false) from the two present-but-nullish
// states (true), but it does not separate those two from each other: a
// stored JSON null and a stored empty string collapse to the same
// value-and-presence pair, as the table above shows. Only PRESERVE_NULL and
// OMIT_NULLS observe those two states apart. The presence bit is therefore
// load-bearing wherever a behavior maps more than one stored state onto the
// same value, and no behavior may drop it.
//
// The presence predicate keeps AQL parity with the HAS chain the AQL
// renderer proves (render/aql/table_reshape.go renderPropertyPathPresence:
// IS_OBJECT(source) ? HAS(source, key) ? … : false). Two JSONB details make
// the gate load-bearing rather than decorative: `#>` reads SQL NULL through
// any missing or non-object parent (matching the per-level IS_OBJECT
// descent), and jsonb_exists has CONTAINS semantics over arrays —
// '["gender"]'::jsonb reports the text "gender" — so the
// jsonb_typeof(parent) = 'object' test is what keeps an array parent from
// falsely proving an object property present.
//
// Path discipline mirrors resolveValue, the package's only other path
// emitter: the whole document path is bound as one text[] parameter (and a
// presence probe binds its parent prefix plus the terminal key as separate
// bound text parameters), segments are re-checked against the frozen ir
// path contract at this boundary, elements matching the case-insensitive
// null array-literal keyword are quoted so no pattern-legal segment can
// change meaning under array-literal parsing, and no plan string ever
// becomes SQL text.
//
// documentPathStepsExpression (pg-sql-21) is this file's one extension of
// that discipline for paths that must address JSON array positions: the
// same segment re-check and the same #> text[] binding, with array
// positions carried as canonical non-negative decimal path elements
// (out-of-range and non-array reads SQL NULL = the ABSENT reading above).
// It exists here and nowhere else so this file remains the package's
// single path-binding authority; a second emitter outside it is the drift
// this claim forbids.
// The 'null'::jsonb and '""'::jsonb coercions are renderer-owned contract
// constants fixed by this file, never plan data and never parameters.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// documentColumn returns the trusted JSONB document column of one row
// alias. The identifier comes only from the storage binding's registry.
func (r *planRenderer) documentColumn(alias string) string {
	return alias + "." + r.storage.Resource.MustColumn("payload").SQLText()
}

// documentPathExpression renders the jsonb extraction of one whole document
// path from the row's document column. The path is bound as a single text[]
// parameter (deduplicated by its literal) exactly as resolveValue binds
// payload comparisons, so selector paths never reach SQL text. Every
// segment is re-checked against the frozen ir path contract here, so a
// future loosening upstream cannot make an array literal ambiguous — and
// the whole-path array literal is composed so no pattern-legal segment can
// change meaning when PostgreSQL parses it (see arrayLiteralElement).
//
// It delegates to documentPathFromExpression so this file stays the
// package's single path-binding authority: renderer-computed jsonb bases
// (such as the unnest expansion item in unnest.go) take the identical path
// discipline through the same emitter.
func (r *planRenderer) documentPathExpression(alias string, path []string) (string, error) {
	return r.documentPathFromExpression(r.documentColumn(alias), path)
}

// documentPathFromExpression renders the jsonb extraction of one whole
// document path from an already-rendered jsonb base expression. The base
// must itself come from a trusted emitter (documentColumn for stored rows,
// or a renderer-owned derived value); only the path crosses the parameter
// boundary, and it crosses it exactly as documentPathExpression binds it.
func (r *planRenderer) documentPathFromExpression(base string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("%w: a document path must have at least one segment", ErrUnsupportedPostgresPlan)
	}
	for _, segment := range path {
		if !payloadPathSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("%w: payload path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment)
		}
	}
	elements := make([]string, len(path))
	for index, segment := range path {
		elements[index] = arrayLiteralElement(segment)
	}
	pathLiteral := "{" + strings.Join(elements, ",") + "}"
	return base + " #> $" + strconv.Itoa(r.textParameter(pathLiteral, "path:"+pathLiteral)) + "::text[]", nil
}

// documentPathStep is one step of a document path that may address either
// a JSON object key or a JSON array position (pg-sql-21: the registered
// non-negative indexed selector steps of unit-normalization selectors).
type documentPathStep struct {
	field   string
	index   int
	indexed bool
}

// documentPathStepsExpression renders the jsonb extraction of one whole
// document path that mixes property keys and non-negative array indexes
// from a row alias's document column. It composes through
// documentPathStepsFromExpression so stored rows and renderer-computed
// bases share one emitter.
func (r *planRenderer) documentPathStepsExpression(alias string, steps []documentPathStep) (string, error) {
	return r.documentPathStepsFromExpression(r.documentColumn(alias), steps)
}

// documentPathStepsFromExpression renders one mixed key/index document
// path from an already-rendered jsonb base through the SAME #> text[]
// reading as documentPathFromExpression: the whole path crosses as one
// bound text[] parameter (deduplicated by its literal), with array
// positions carried as their canonical non-negative decimal text — the
// only PostgreSQL form in which one path addresses both object keys and
// array positions (jsonb_extract_path takes text elements only and never
// indexes arrays). Each key is re-checked here against the frozen
// payloadPathSegmentPattern — the same boundary
// documentPathFromExpression enforces for property-only paths. #> reads a
// missing parent, a non-object key step, and an out-of-range or
// non-array index step all as SQL NULL, which is this file's ABSENT
// reading; a stored JSON null terminal stays the jsonb 'null' scalar.
// Indexes arrive validated non-negative by the calling capability
// (unit_normalization.go refuses negative indexes as an unregistered
// capability gap); this boundary re-reads that pin so no future caller
// can smuggle PostgreSQL's distinct-from-AQL negative subscript reading
// through this emitter.
func (r *planRenderer) documentPathStepsFromExpression(base string, steps []documentPathStep) (string, error) {
	if len(steps) == 0 {
		return "", fmt.Errorf("%w: a document path must have at least one segment", ErrUnsupportedPostgresPlan)
	}
	elements := make([]string, 0, len(steps))
	for _, step := range steps {
		if !payloadPathSegmentPattern.MatchString(step.field) {
			return "", fmt.Errorf("%w: payload path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.field)
		}
		if step.indexed {
			if step.index < 0 {
				return "", fmt.Errorf("%w: negative document path index %d under segment %q has no registered PostgreSQL reading (ArangoDB tail-index semantics belong to the pg-sql-16 array-rendering capability)",
					ErrUnsupportedPostgresPlan, step.index, step.field)
			}
			// One selector step {field, index} addresses field[index]:
			// AQL renders the same pair (render/aql/selector.go:34,47),
			// so the key AND the position are both path elements —
			// dropping the key would index the PARENT instead.
			elements = append(elements, arrayLiteralElement(step.field))
			elements = append(elements, arrayLiteralElement(strconv.Itoa(step.index)))
			continue
		}
		elements = append(elements, arrayLiteralElement(step.field))
	}
	pathLiteral := "{" + strings.Join(elements, ",") + "}"
	return base + " #> $" + strconv.Itoa(r.textParameter(pathLiteral, "path:"+pathLiteral)) + "::text[]", nil
}

// arrayLiteralElement renders one validated path segment as a PostgreSQL
// text[] array-literal element. Every segment the frozen
// payloadPathSegmentPattern admits round-trips unquoted except one:
// PostgreSQL parses an unquoted, case-insensitive null element as a SQL
// NULL path element rather than the key literally named null, which would
// make a document storing that key read as ABSENT — erasing the
// PRESENT-VALUE row of this file's behavior table. Such elements are
// double-quoted, which pins them to the exact-case string; the pattern
// admits no double quote or backslash, so quoting needs no escaping and
// the case-insensitive keyword is the only rewrite required here.
func arrayLiteralElement(segment string) string {
	if strings.EqualFold(segment, "null") {
		return `"` + segment + `"`
	}
	return segment
}

// scalarExtractValueExpression renders the typed jsonb value of one
// scalar-cardinality selector extraction under its declared null behavior,
// following the behavior table in this file's contract comment. The result
// is parenthesized (or function-wrapped) as a single expression.
func (r *planRenderer) scalarExtractValueExpression(alias string, path []string, behavior ir.PhysicalNullBehavior) (string, error) {
	extraction, err := r.documentPathExpression(alias, path)
	if err != nil {
		return "", err
	}
	switch behavior {
	case ir.PhysicalPreserveNull:
		// Missing and SQL-NULL payload read SQL NULL; a stored JSON null
		// stays the jsonb 'null' scalar — the source distinctions survive.
		return "(" + extraction + ")", nil
	case ir.PhysicalOmitNulls:
		// A stored JSON null is reported as omitted (SQL NULL); every other
		// stored value, including arrays that contain null, passes through
		// byte-for-byte.
		return "NULLIF((" + extraction + "), 'null'::jsonb)", nil
	case ir.PhysicalEmptyOnNull:
		// Null-or-missing becomes the JSON empty string; stored values —
		// including the empty array and [null] — are never touched.
		return "COALESCE(NULLIF((" + extraction + "), 'null'::jsonb), '\"\"'::jsonb)", nil
	default:
		return "", fmt.Errorf("%w: null behavior %q has no registered PostgreSQL extraction form",
			ErrUnsupportedPostgresPlan, string(behavior))
	}
}

// propertyPresenceExpression renders the two-valued presence proof for one
// document path: true exactly when every parent segment along the path is
// present and stored as a JSON object and the terminal key exists in its
// parent object — the jsonb equivalent of the AQL IS_OBJECT/HAS descent in
// render/aql/table_reshape.go renderPropertyPathPresence. A stored JSON
// null at the terminal is present; a missing key, a non-object parent, and
// an absent document all read false. COALESCE(..., false) makes the proof
// total: three-valued logic never reaches the presence column itself.
func (r *planRenderer) propertyPresenceExpression(alias string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("%w: a presence proof needs at least one property", ErrUnsupportedPostgresPlan)
	}
	for _, segment := range path {
		if !payloadPathSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("%w: presence path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment)
		}
	}
	parent := r.documentColumn(alias)
	if len(path) > 1 {
		prefix, err := r.documentPathExpression(alias, path[:len(path)-1])
		if err != nil {
			return "", err
		}
		parent = prefix
	}
	keyParameter := r.textParameter(path[len(path)-1], "text:"+path[len(path)-1])
	return "COALESCE(jsonb_typeof(" + parent + ") = 'object' AND jsonb_exists(" + parent + ", $" + strconv.Itoa(keyParameter) + "), false)", nil
}
