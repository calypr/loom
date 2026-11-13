package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

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

const sourcePlanShapeDomain = "loom:source-plan-shape:v1"

func SourcePlanFingerprint(compiled compiler.CompiledQuery) string {
	if compiled.Command.Backend() == command.CompiledBackendPostgreSQL {
		return postgreSQLDiagnosticShapeFingerprint(compiled)
	}
	return CompiledQueryFingerprint(compiled)
}

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

func previewPlanFingerprint(query compiler.CompiledQuery, stream OutputStream) string {
	if stream.physicalEngine == ir.PhysicalEngineClickHouse {
		return query.PlanDiagnostics.Fingerprint
	}
	return SourcePlanFingerprint(query)
}

func requireProjectedPlanFingerprint(output string, fingerprint string) error {
	if strings.TrimSpace(fingerprint) == "" {
		return dataframeerrors.NewError(dataframeerrors.CodeInternalError, "compiled command for output "+output+" carries no dispatchable payload for its diagnostic plan fingerprint", dataframeerrors.WithRetryable(false))
	}
	return nil
}
