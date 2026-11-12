package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// CompiledQueryFingerprint returns a deterministic, value-safe identifier for
// a compiled plan. Bind values are intentionally excluded; rendered query
// shape, bind-key names, and output metadata participate instead. The query
// text and bind-key names are derived through closed dispatch: only an
// AQL-tagged command contributes query shape and named bind keys, while a
// PostgreSQL-tagged or untagged command contributes the same empty shape the
// pre-boundary fingerprint computed for those plans, so the hash input is
// byte-identical.
func CompiledQueryFingerprint(compiled compiler.CompiledQuery) string {
	var queryText string
	var bindVars map[string]any
	if compiled.Command.Backend() == command.CompiledBackendAQL {
		text, binds, dispatchErr := compiled.Command.AQLParameters()
		if dispatchErr != nil {
			return ""
		}
		queryText, bindVars = text, binds
	}
	keys := make([]string, 0, len(bindVars))
	for key := range bindVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(queryText)
	builder.WriteByte('\x00')
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.Columns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.PublicColumns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, field := range compiled.PivotFields {
		builder.WriteString(field)
		builder.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// sourcePlanShapeDomain scopes the PostgreSQL diagnostic-shape digest to its
// versioned meaning. It is part of every hash input so a future encoding
// change cannot collide with these bytes, and the shape can never be
// confused with the value-bound artifact digest family.
const sourcePlanShapeDomain = "loom:source-plan-shape:v1"

// SourcePlanFingerprint returns the backend-neutral diagnostic shape
// fingerprint of one compiled source command — the single meaning the public
// planFingerprint field carries on both lanes, at Explain and at preview
// projection alike. It is computed at projection only: no compiled query
// stores it, and it is never a result-cache, authorization, or artifact
// fencing key. Those roles keep the value-bound identities
// (CompiledQueryFingerprint's historical AQL shape is value-free but stays as
// the AQL public bytes; OutputStream.planFingerprint/Identity.PlanDigest keep
// the bound artifact key bytes).
//
// Dispatch is closed over the command tag:
//
//   - AQL-tagged (and, preserving the historical policy surface, untagged)
//     commands report CompiledQueryFingerprint, whose bytes are pinned by
//     golden and unchanged.
//   - PostgreSQL-tagged commands report postgreSQLDiagnosticShapeFingerprint,
//     a value-free digest over the compiler-rendered SQL, the ordered
//     parameter types, and the output/public/pivot metadata. Bound values are
//     excluded, so rebinding a value keeps the shape while the value-bound
//     artifact digest moves.
//   - A command whose dispatch fails (a corrupt tagged payload) reports the
//     empty string. The command constructors reject such payloads, so this
//     is a compiled-command invariant violation rather than a reachable
//     shape; requireProjectedPlanFingerprint fails both public projections
//     closed on it, so no Explain or preview response ever carries an empty
//     planFingerprint with a nil error.
func SourcePlanFingerprint(compiled compiler.CompiledQuery) string {
	if compiled.Command.Backend() == command.CompiledBackendPostgreSQL {
		return postgreSQLDiagnosticShapeFingerprint(compiled)
	}
	return CompiledQueryFingerprint(compiled)
}

// postgreSQLDiagnosticShapeFingerprint hashes the value-free PostgreSQL plan
// shape: a domain/version tag, the dialect, the compiler-rendered statement
// SQL, the ordered positional parameter TYPES (arity included, values
// excluded), and the declared/public/pivot output metadata. Fields are
// length-prefixed so no boundary between adjacent fields can be forged by
// embedding separators in the SQL text. An empty statement or a dispatch
// failure yields the empty string, which requireProjectedPlanFingerprint
// refuses at the Explain and preview projections as a compiled-command
// defect (validatePreviewPlan's non-empty check guards the bound-digest
// admission gate, not this shape).
func postgreSQLDiagnosticShapeFingerprint(compiled compiler.CompiledQuery) string {
	sqlText, args, err := compiled.Command.PostgreSQLParameters()
	if err != nil || strings.TrimSpace(sqlText) == "" {
		return ""
	}
	var builder strings.Builder
	writeField := func(field string) {
		builder.WriteString(strconv.Itoa(len(field)))
		builder.WriteByte(':')
		builder.WriteString(field)
	}
	writeField(sourcePlanShapeDomain)
	writeField(string(command.CompiledBackendPostgreSQL))
	writeField(sqlText)
	writeField(strconv.Itoa(len(args)))
	for _, arg := range args {
		writeField(string(arg.Type))
	}
	for _, column := range compiled.Columns {
		writeField(column)
	}
	for _, column := range compiled.PublicColumns {
		writeField(column)
	}
	for _, field := range compiled.PivotFields {
		writeField(field)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// previewPlanFingerprint projects the single diagnostic meaning the preview
// summary reports through the existing PlanFingerprint field. Source lanes
// (AQL and PostgreSQL) report SourcePlanFingerprint, so Explain and preview
// expose the same bytes for the same plan. A ClickHouse combine stream has no
// source command at all; it keeps the ClickHouse-owned combine digest it
// carried before, which is part of its artifact contract and out of this
// source-diagnostics boundary.
func previewPlanFingerprint(query compiler.CompiledQuery, stream OutputStream) string {
	if stream.physicalEngine == ir.PhysicalEngineClickHouse {
		return query.PlanDiagnostics.Fingerprint
	}
	return SourcePlanFingerprint(query)
}

// requireProjectedPlanFingerprint is the fail-closed gate the public
// projections run on every projected planFingerprint before the response is
// assembled. A compiled source command that cannot dispatch (a corrupt
// tagged payload the command constructors' invariants make unreachable on
// the happy path) yields the empty fingerprint; on the compile-only Explain
// path nothing else ever dispatches the command, so without this gate a
// defect would surface as an empty public planFingerprint with a nil error.
// A ClickHouse combine preview's digest reaches the projection through
// validatePreviewPlan's non-empty admission check; this gate covers it too.
func requireProjectedPlanFingerprint(output string, fingerprint string) error {
	if strings.TrimSpace(fingerprint) == "" {
		return fmt.Errorf("output %q: compiled command carries no dispatchable payload for its diagnostic plan fingerprint", output)
	}
	return nil
}
