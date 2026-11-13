package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	fpDDLProject = "pgcat01-ddl"
	fpDDLGen     = "g1"
	fpDDLType    = "Observation"
)

func fpDDLText(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

var fpDDLProfileDefaults = map[string]string{
	"project":                  fpDDLText(fpDDLProject),
	"dataset_generation":       fpDDLText(fpDDLGen),
	"auth_resource_path":       "''",
	"resource_type":            fpDDLText(fpDDLType),
	"path":                     "'obs.value'",
	"profile_key":              "'pgcat01-ddl-key'",
	"record_type":              "''",
	"kind":                     "'string'",
	"doc_count":                "0",
	"max_items":                "0",
	"sample_count":             "0",
	"distinct_values":          "NULL",
	"distinct_truncated":       "false",
	"enrichment_complete":      "NULL",
	"enrichment_truncated":     "false",
	"pivot_candidate":          "false",
	"pivot_kind":               "''",
	"pivot_columns":            "NULL",
	"pivot_family":             "''",
	"pivot_column_selector":    "''",
	"pivot_value_selector":     "''",
	"pivot_item_source":        "''",
	"pivot_item_resource_type": "''",
	"pivot_value_selectors":    "NULL",
	"extension_values":         "NULL",
	"semantic_observations":    "NULL",
}

var fpDDLProfileColumns = []string{
	"project", "dataset_generation", "auth_resource_path", "resource_type", "path",
	"profile_key", "record_type", "kind", "doc_count", "max_items", "sample_count",
	"distinct_values", "distinct_truncated", "enrichment_complete", "enrichment_truncated",
	"pivot_candidate", "pivot_kind", "pivot_columns", "pivot_family", "pivot_column_selector",
	"pivot_value_selector", "pivot_item_source", "pivot_item_resource_type", "pivot_value_selectors",
	"extension_values", "semantic_observations",
}

func fpDDLProfileStmt(svc *migrationService, overrides map[string]string) string {
	cols := make([]string, 0, len(fpDDLProfileColumns))
	vals := make([]string, 0, len(fpDDLProfileColumns))
	for _, column := range fpDDLProfileColumns {
		literal, ok := fpDDLProfileDefaults[column]
		if !ok {
			panic("fpDDLProfileDefaults lacks column " + column)
		}
		if override, changed := overrides[column]; changed {
			literal = override
		}
		cols = append(cols, column)
		vals = append(vals, literal)
	}
	return fmt.Sprintf(`INSERT INTO "%s".fhir_field_profile (%s) VALUES (%s)`,
		svc.schema, strings.Join(cols, ", "), strings.Join(vals, ", "))
}

func fpDDLExec(t *testing.T, svc *migrationService, stmt string) {
	t.Helper()
	if _, err := svc.iso.Exec(svc.ctx, stmt); err != nil {
		svc.fail("exec "+firstStatementWords(stmt), err)
	}
}

func fpDDLCount(t *testing.T, svc *migrationService, table string) int64 {
	t.Helper()
	var n int64
	if err := svc.iso.QueryRow(svc.ctx, fmt.Sprintf(`SELECT count(*) FROM "%s".%s`, svc.schema, table)).Scan(&n); err != nil {
		svc.fail("count "+table, err)
	}
	return n
}

func fpDDLExpectViolation(t *testing.T, svc *migrationService, table, stmt string, wantCode, wantConstraint string, wantRows int64) {
	t.Helper()
	_, err := svc.iso.Exec(svc.ctx, stmt)
	if err == nil {
		t.Fatalf("statement accepted, want rejection by %s (%s): %s", wantConstraint, wantCode, firstStatementWords(stmt))
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		svc.fail("non-PgError rejection "+firstStatementWords(stmt), err)
	}
	accepted := []string{wantConstraint}
	if wantConstraint == "<either-record-constraint>" {
		accepted = []string{"fhir_field_profile_record_shape", "fhir_field_profile_record_type_known"}
	}
	if pgErr.Code != wantCode || !slices.Contains(accepted, pgErr.ConstraintName) {
		t.Fatalf("rejection = (%s, %q), want (%s, one of %v): %v",
			pgErr.Code, pgErr.ConstraintName, wantCode, accepted, svc.scrub(err))
	}
	if got := fpDDLCount(t, svc, table); got != wantRows {
		t.Fatalf("table %s holds %d rows after refused insert, want %d", table, got, wantRows)
	}
}

func firstStatementWords(stmt string) string {
	words := strings.Fields(stmt)
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, " ") + " ..."
}

func fpDDLFindMigration(t *testing.T, migrations []Migration, version int64) Migration {
	t.Helper()
	for _, m := range migrations {
		if m.Version == version {
			return m
		}
	}
	t.Fatalf("embedded migrations have no version %d", version)
	return Migration{}
}

type fpDDLSchemaFingerprint struct {
	Columns     []string
	Constraints []string
	Indexes     []string
}

func fpDDLCollectFingerprint(t *testing.T, svc *migrationService) fpDDLSchemaFingerprint {
	t.Helper()
	fp := fpDDLSchemaFingerprint{}
	fp.Columns = fpDDLStringColumn(t, svc, fmt.Sprintf(
		`SELECT column_name || '|' || data_type || '|' || is_nullable || '|' || coalesce(column_default,'<none>')
		   FROM information_schema.columns
		  WHERE table_schema = $1 AND table_name IN ('fhir_field_profile', 'fhir_field_profile_coverage')
		  ORDER BY table_name, ordinal_position`), svc.schema)
	fp.Constraints = fpDDLStringColumn(t, svc, fmt.Sprintf(
		`SELECT c.conrelid::regclass::text || '|' || c.conname || '|' || pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		  WHERE c.connamespace = $1::regnamespace
		    AND c.conrelid::regclass::text IN ($1::text||'.fhir_field_profile', $1::text||'.fhir_field_profile_coverage')
		  ORDER BY 1`), svc.schema)
	fp.Indexes = fpDDLStringColumn(t, svc, fmt.Sprintf(
		`SELECT tablename || '|' || indexname || '|' || indexdef
		   FROM pg_indexes
		  WHERE schemaname = $1 AND tablename IN ('fhir_field_profile', 'fhir_field_profile_coverage')
		  ORDER BY 1`), svc.schema)
	if len(fp.Columns) != 26+7 {
		t.Fatalf("fingerprint sees %d columns, want 33 (26 + 7): %v", len(fp.Columns), fp.Columns)
	}
	return fp
}

func fpDDLStringColumn(t *testing.T, svc *migrationService, query string, arg any) []string {
	t.Helper()
	rows, err := svc.iso.Query(svc.ctx, query, arg)
	if err != nil {
		svc.fail("fingerprint query "+firstStatementWords(query), err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			svc.fail("fingerprint scan", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		svc.fail("fingerprint rows", err)
	}
	return out
}

func TestFieldProfileDDLDoubleApplyIsIdempotent(t *testing.T) {
	svc := newMigrationService(t)
	embedded := mustEmbeddedMigrations(t)

	first, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema)
	if err != nil {
		svc.fail("MigrateSchema", err)
	}
	if len(first.Applied) != len(embedded) || first.LatestVersion != embedded[len(embedded)-1].Version {
		t.Fatalf("first migrate = %+v, want %d applied rows up to version %d",
			first, len(embedded), embedded[len(embedded)-1].Version)
	}
	before := fpDDLCollectFingerprint(t, svc)

	m := fpDDLFindMigration(t, embedded, 7)
	if m.Name != "field_profile" {
		t.Fatalf("embedded version 7 is %q, want field_profile", m.Name)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.iso.Exec(svc.ctx, m.SQL); err != nil {
			svc.fail("direct re-apply of 000007 DDL", err)
		}
	}

	second, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema)
	if err != nil {
		svc.fail("re-apply MigrateSchema", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("runner re-apply changed the result:\n1st: %+v\n2nd: %+v", first, second)
	}
	after := fpDDLCollectFingerprint(t, svc)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("schema changed across double apply:\nbefore: %+v\nafter:  %+v", before, after)
	}
	ledger := svc.readLedger(t)
	if len(ledger) != len(embedded) {
		t.Fatalf("ledger holds %d rows after double apply, want %d", len(ledger), len(embedded))
	}
	var sevens int
	for _, rec := range ledger {
		if rec.Version == 7 {
			sevens++
		}
	}
	if sevens != 1 {
		t.Fatalf("ledger records version 7 %d times, want exactly once", sevens)
	}
}

func TestFieldProfileDDLChecksRejectViolations(t *testing.T) {
	svc := newMigrationService(t)
	if _, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema); err != nil {
		svc.fail("MigrateSchema", err)
	}

	const (
		codeCheck = "23514"
		codeUniq  = "23505"
	)
	cases := []struct {
		name           string
		table          string
		stmt           string
		code           string
		constraint     string
		rowsBefore     int64
		precedingStmts []string
	}{
		{"project_blank", "fhir_field_profile", "", codeCheck, "fhir_field_profile_project_present", 0, nil},
		{"resource_type_blank", "fhir_field_profile", "", codeCheck, "fhir_field_profile_type_present", 0, nil},
		{"profile_key_blank", "fhir_field_profile", "", codeCheck, "fhir_field_profile_key_present", 0, nil},
		{"status_row_with_path", "fhir_field_profile", "", codeCheck, "fhir_field_profile_record_shape", 0, nil},
		{"field_row_without_kind", "fhir_field_profile", "", codeCheck, "fhir_field_profile_record_shape", 0, nil},
		{"record_type_unknown", "fhir_field_profile", "", codeCheck, "<either-record-constraint>", 0, nil},
		{"doc_count_negative", "fhir_field_profile", "", codeCheck, "fhir_field_profile_counts_nonnegative", 0, nil},
		{"max_items_negative", "fhir_field_profile", "", codeCheck, "fhir_field_profile_counts_nonnegative", 0, nil},
		{"sample_count_negative", "fhir_field_profile", "", codeCheck, "fhir_field_profile_counts_nonnegative", 0, nil},
		{"extension_object", "fhir_field_profile", "", codeCheck, "fhir_field_profile_extension_is_array", 0, nil},
		{"extension_json_null", "fhir_field_profile", "", codeCheck, "fhir_field_profile_extension_is_array", 0, nil},
		{"semantic_scalar", "fhir_field_profile", "", codeCheck, "fhir_field_profile_semantic_is_array", 0, nil},
		{"duplicate_identity", "fhir_field_profile", "", codeUniq, "fhir_field_profile_pkey", 1, []string{"<base>"}},
		{"coverage_project_blank", "fhir_field_profile_coverage", "", codeCheck, "fhir_field_profile_coverage_project_present", 0, nil},
		{"coverage_type_blank", "fhir_field_profile_coverage", "", codeCheck, "fhir_field_profile_coverage_type_present", 0, nil},
		{"coverage_duplicate", "fhir_field_profile_coverage", "", codeUniq, "fhir_field_profile_coverage_pkey", 1, []string{"<coverage-base>"}},
	}

	overridesByCase := map[string]map[string]string{
		"project_blank":          {"project": "''"},
		"resource_type_blank":    {"resource_type": "''"},
		"profile_key_blank":      {"profile_key": "''"},
		"status_row_with_path":   {"record_type": "'profile_status'", "path": "'obs.value'"},
		"field_row_without_kind": {"record_type": "''", "kind": "''"},
		"record_type_unknown":    {"record_type": "'bogus'", "path": "'obs.value'"},
		"doc_count_negative":     {"doc_count": "-1"},
		"max_items_negative":     {"max_items": "-1"},
		"sample_count_negative":  {"sample_count": "-1"},
		"extension_object":       {"extension_values": `'{"a":1}'::jsonb`},
		"extension_json_null":    {"extension_values": `'null'::jsonb`},
		"semantic_scalar":        {"semantic_observations": `'7'::jsonb`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stmt string
			switch tc.name {
			case "duplicate_identity":
				fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{"path": "'obs.dup'"}))
				stmt = fpDDLProfileStmt(svc, map[string]string{"path": "'obs.dup'", "profile_key": "'changed-key'"})
			case "coverage_duplicate":
				fpDDLExec(t, svc, fpDDLCoverageStmt(svc, nil))
				stmt = fpDDLCoverageStmt(svc, map[string]string{"enrichment_truncated": "true"})
			case "coverage_project_blank":
				stmt = fpDDLCoverageStmt(svc, map[string]string{"project": "''"})
			case "coverage_type_blank":
				stmt = fpDDLCoverageStmt(svc, map[string]string{"resource_type": "''"})
			default:
				overrides, ok := overridesByCase[tc.name]
				if !ok {
					t.Fatalf("case %q has no overrides", tc.name)
				}
				stmt = fpDDLProfileStmt(svc, overrides)
			}
			fpDDLExpectViolation(t, svc, tc.table, stmt, tc.code, tc.constraint, tc.rowsBefore)
		})
	}

	t.Run("control_legal_shapes", func(t *testing.T) {
		rows, err := svc.iso.Query(svc.ctx,
			`SELECT conname FROM pg_constraint
			  WHERE connamespace = $1::regnamespace
			    AND conrelid::regclass::text IN ($1::text||'.fhir_field_profile', $1::text||'.fhir_field_profile_coverage')`,
			svc.schema)
		if err != nil {
			svc.fail("constraint catalog witness", err)
		}
		present := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				svc.fail("constraint catalog scan", err)
			}
			present[n] = true
		}
		rows.Close()
		for _, want := range []string{
			"fhir_field_profile_pkey", "fhir_field_profile_project_present",
			"fhir_field_profile_type_present", "fhir_field_profile_key_present",
			"fhir_field_profile_record_shape", "fhir_field_profile_record_type_known",
			"fhir_field_profile_counts_nonnegative", "fhir_field_profile_extension_is_array",
			"fhir_field_profile_semantic_is_array", "fhir_field_profile_coverage_pkey",
			"fhir_field_profile_coverage_project_present", "fhir_field_profile_coverage_type_present",
		} {
			if !present[want] {
				t.Errorf("constraint %s missing from migrated schema", want)
			}
		}

		fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{"path": "''", "record_type": "'profile_status'", "kind": "''"}))
		fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{"path": "'obs.jsonb-null'", "extension_values": "NULL", "semantic_observations": "NULL"}))
		fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{"path": "'obs.jsonb-empty'", "extension_values": `'[]'::jsonb`, "semantic_observations": `'[]'::jsonb`}))
		fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{"path": "''", "record_type": "'profile_status'", "kind": "''", "auth_resource_path": "'cov/status-null'", "enrichment_complete": "NULL"}))
	})
}

func fpDDLCoverageStmt(svc *migrationService, overrides map[string]string) string {
	defaults := map[string]string{
		"project":              fpDDLText(fpDDLProject),
		"dataset_generation":   fpDDLText(fpDDLGen),
		"auth_resource_path":   "'cov/ddl'",
		"resource_type":        fpDDLText(fpDDLType),
		"enrichment_complete":  "NULL",
		"enrichment_truncated": "false",
	}
	cols := []string{"project", "dataset_generation", "auth_resource_path", "resource_type", "enrichment_complete", "enrichment_truncated"}
	vals := make([]string, 0, len(cols))
	for _, column := range cols {
		literal := defaults[column]
		if override, changed := overrides[column]; changed {
			literal = override
		}
		vals = append(vals, literal)
	}
	return fmt.Sprintf(`INSERT INTO "%s".fhir_field_profile_coverage (%s) VALUES (%s)`,
		svc.schema, strings.Join(cols, ", "), strings.Join(vals, ", "))
}

func fpDDLTextArrayWitness(t *testing.T, svc *migrationService, column string, paths [3]string) error {
	t.Helper()
	want := [3][3]bool{
		{true, false, false},
		{false, true, false},
		{false, false, true},
	}
	for i, path := range paths {
		var isNull, isEmpty, isPopulated bool
		if err := svc.iso.QueryRow(svc.ctx, fmt.Sprintf(
			`SELECT %s IS NULL, coalesce(%s = '{}'::text[], false), coalesce(%s = ARRAY['a','b']::text[], false)
			   FROM "%s".fhir_field_profile WHERE project=$1 AND dataset_generation=$2 AND resource_type=$3 AND path=$4`,
			column, column, column, svc.schema),
			fpDDLProject, fpDDLGen, fpDDLType, path).Scan(&isNull, &isEmpty, &isPopulated); err != nil {
			return fmt.Errorf("witness row %s: %w", path, err)
		}
		got := [3]bool{isNull, isEmpty, isPopulated}
		if got != want[i] {
			return fmt.Errorf("column %s row %s encodings = %v, want %v (NULL/'{}'/populated must be three distinct states)",
				column, path, got, want[i])
		}
	}
	return nil
}

func TestFieldProfileTextArrayEncodingsDistinguishThreeStates(t *testing.T) {
	svc := newMigrationService(t)
	if _, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema); err != nil {
		svc.fail("MigrateSchema", err)
	}
	repo, err := NewFieldProfileRepository(svc.iso, svc.schema)
	if err != nil {
		t.Fatalf("NewFieldProfileRepository: %v", err)
	}

	for _, column := range []string{"distinct_values", "pivot_columns", "pivot_value_selectors"} {
		t.Run(column, func(t *testing.T) {
			paths := [3]string{column + "/enc/null", column + "/enc/empty", column + "/enc/populated"}
			encodings := []map[string]string{
				{column: "NULL", "path": fpDDLText(paths[0])},
				{column: "'{}'::text[]", "path": fpDDLText(paths[1])},
				{column: "ARRAY['a','b']::text[]", "path": fpDDLText(paths[2])},
			}
			for _, override := range encodings {
				fpDDLExec(t, svc, fpDDLProfileStmt(svc, override))
			}
			if err := fpDDLTextArrayWitness(t, svc, column, paths); err != nil {
				t.Fatalf("real storage failed the three-state witness: %v", err)
			}

			unrestricted := true
			docs, err := repo.ReadFieldProfileRowsFiltered(svc.ctx, FieldProfileFilter{
				Project: fpDDLProject, DatasetGeneration: fpDDLGen,
				ResourceTypes:                 []string{fpDDLType},
				AuthResourcePathsUnrestricted: &unrestricted,
			})
			if err != nil {
				svc.fail("ReadFieldProfileRowsFiltered", err)
			}
			byPath := map[string]catalog.FieldCatalogDocument{}
			for _, doc := range docs {
				byPath[doc.Path] = doc
			}
			for _, path := range paths {
				if _, ok := byPath[path]; !ok {
					t.Fatalf("fixture row %s missing from read-back (read %d rows)", path, len(docs))
				}
			}
			if got := fpDDLColumnSlice(column, byPath[paths[0]]); got != nil {
				t.Errorf("NULL-encoded %s decoded to %#v, want nil", column, got)
			}
			if got := fpDDLColumnSlice(column, byPath[paths[1]]); got != nil {
				t.Errorf("'{}'-encoded %s decoded to %#v, want nil (documented profiler contract)", column, got)
			}
			if got := fpDDLColumnSlice(column, byPath[paths[2]]); !reflect.DeepEqual(got, []string{"a", "b"}) {
				t.Errorf("populated %s decoded to %#v, want [a b]", column, got)
			}

			writeScope := FieldProfileScope{Project: fpDDLProject, DatasetGeneration: fpDDLGen, ResourceType: fpDDLType}
			doc := fpDDLBaseDoc(column + "/enc/write-nil")
			if err := repo.UpsertFieldProfileBatch(svc.ctx, writeScope, []catalog.FieldCatalogDocument{doc},
				FieldProfileCoverage{FieldProfileScope: writeScope}, 0); err != nil {
				svc.fail("UpsertFieldProfileBatch nil", err)
			}
			var storedNull bool
			if err := svc.iso.QueryRow(svc.ctx, fmt.Sprintf(
				`SELECT %[1]s IS NULL FROM "%s".fhir_field_profile WHERE path=$1`, column, svc.schema),
				fmt.Sprintf("%s/enc/write-nil", column),
			).Scan(&storedNull); err != nil {
				svc.fail("witness nil write", err)
			}
			if !storedNull {
				t.Errorf("repository wrote a nil %s as non-NULL; the collapse of NULL into '{}' would make never-set and seeded-empty indistinguishable", column)
			}

			fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{
				"path": fpDDLText(column + "/enc/collapsed"), column: "NULL",
			}))
			collapsedWitnessPaths := [3]string{paths[0], paths[1], column + "/enc/collapsed"}
			if _, err := svc.iso.Exec(svc.ctx, fmt.Sprintf(
				`UPDATE "%s".fhir_field_profile SET %s = '{}'::text[] WHERE path = $1`,
				svc.schema, column), column+"/enc/collapsed"); err != nil {
				svc.fail("collapse simulation update", err)
			}
			if err := fpDDLTextArrayWitness(t, svc, column, collapsedWitnessPaths); err == nil {
				t.Fatalf("three-state witness accepted a collapsed '{}' row; the witness is vacuous")
			}
		})
	}
}

func fpDDLColumnSlice(column string, doc catalog.FieldCatalogDocument) []string {
	switch column {
	case "distinct_values":
		return doc.DistinctValues
	case "pivot_columns":
		return doc.PivotColumns
	case "pivot_value_selectors":
		return doc.PivotValueSelectors
	}
	panic("unknown text[] column " + column)
}

func fpDDLBaseDoc(path string) catalog.FieldCatalogDocument {
	return catalog.FieldCatalogDocument{
		Key: "pgcat01-ddl-" + path, Project: fpDDLProject, DatasetGeneration: fpDDLGen,
		ResourceType: fpDDLType, Path: path, Kind: "string",
	}
}

const (
	fpDDLExtensionPopulated = `[{"url":"http://loom.test/ext/a","source_path":"extension","value_path":"valueString","value_type":"string","url_path":null}]`
	fpDDLSemanticPopulated  = `[{"schema_version":2,"source":{"canonical":"http://loom.test/cs","type":"coding","path":"code"},"key":{"system":"http://loom.test/cs","code":"C1","display":"One"},"value":{"selector":"code","type":"code","fallback_selectors":null},"completeness":"complete","population":5,"examples_truncated":false}]`
)

func fpDDLJSONBWitness(t *testing.T, svc *migrationService, column string, paths [3]string) error {
	t.Helper()
	want := [3][3]bool{
		{true, false, false},
		{false, true, false},
		{false, false, true},
	}
	for i, path := range paths {
		var isNull, isEmpty, isPopulated bool
		if err := svc.iso.QueryRow(svc.ctx, fmt.Sprintf(
			`SELECT %[1]s IS NULL, coalesce(%[1]s = '[]'::jsonb, false), coalesce(jsonb_typeof(%[1]s) = 'array' AND %[1]s <> '[]'::jsonb, false)
			   FROM "%[2]s".fhir_field_profile WHERE project=$1 AND dataset_generation=$2 AND resource_type=$3 AND path=$4`,
			column, svc.schema),
			fpDDLProject, fpDDLGen, fpDDLType, path).Scan(&isNull, &isEmpty, &isPopulated); err != nil {
			return fmt.Errorf("witness row %s: %w", path, err)
		}
		got := [3]bool{isNull, isEmpty, isPopulated}
		if got != want[i] {
			return fmt.Errorf("column %s row %s encodings = %v, want %v (NULL/'[]'/populated must be three distinct states)",
				column, path, got, want[i])
		}
	}
	return nil
}

func TestFieldProfileJSONBEncodingsDistinguishThreeStates(t *testing.T) {
	svc := newMigrationService(t)
	if _, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema); err != nil {
		svc.fail("MigrateSchema", err)
	}
	repo, err := NewFieldProfileRepository(svc.iso, svc.schema)
	if err != nil {
		t.Fatalf("NewFieldProfileRepository: %v", err)
	}
	populatedLiteral := map[string]string{
		"extension_values":      fpDDLText(fpDDLExtensionPopulated),
		"semantic_observations": fpDDLText(fpDDLSemanticPopulated),
	}

	for _, column := range []string{"extension_values", "semantic_observations"} {
		t.Run(column, func(t *testing.T) {
			paths := [3]string{column + "/json/null", column + "/json/empty", column + "/json/populated"}
			encodings := []map[string]string{
				{column: "NULL", "path": fpDDLText(paths[0])},
				{column: "'[]'::jsonb", "path": fpDDLText(paths[1])},
				{column: populatedLiteral[column], "path": fpDDLText(paths[2])},
			}
			for _, override := range encodings {
				fpDDLExec(t, svc, fpDDLProfileStmt(svc, override))
			}
			if err := fpDDLJSONBWitness(t, svc, column, paths); err != nil {
				t.Fatalf("real storage failed the three-state witness: %v", err)
			}

			unrestricted := true
			docs, err := repo.ReadFieldProfileRowsFiltered(svc.ctx, FieldProfileFilter{
				Project: fpDDLProject, DatasetGeneration: fpDDLGen,
				ResourceTypes:                 []string{fpDDLType},
				AuthResourcePathsUnrestricted: &unrestricted,
			})
			if err != nil {
				svc.fail("ReadFieldProfileRowsFiltered", err)
			}
			byPath := map[string]catalog.FieldCatalogDocument{}
			for _, doc := range docs {
				byPath[doc.Path] = doc
			}
			if len(byPath) < 3 {
				t.Fatalf("read back %d rows, want at least 3", len(byPath))
			}
			if got := fpDDLJSONBLen(column, byPath[paths[0]]); got != 0 || !fpDDLJSONBIsNil(column, byPath[paths[0]]) {
				t.Errorf("NULL-encoded %s must decode to a nil slice, got len %d nil=%v", column, got, fpDDLJSONBIsNil(column, byPath[paths[0]]))
			}
			if fpDDLJSONBIsNil(column, byPath[paths[1]]) || fpDDLJSONBLen(column, byPath[paths[1]]) != 0 {
				t.Errorf("'[]'-encoded %s must decode to an empty-but-non-nil slice, got len %d nil=%v",
					column, fpDDLJSONBLen(column, byPath[paths[1]]), fpDDLJSONBIsNil(column, byPath[paths[1]]))
			}
			if fpDDLJSONBLen(column, byPath[paths[2]]) != 1 {
				t.Fatalf("populated %s decoded to %d elements, want 1", column, fpDDLJSONBLen(column, byPath[paths[2]]))
			}
			if column == "extension_values" {
				want := catalog.ExtensionValueObservation{
					URL: "http://loom.test/ext/a", SourcePath: "extension", ValuePath: "valueString", ValueType: "string",
				}
				if got := byPath[paths[2]].ExtensionValues[0]; !reflect.DeepEqual(got, want) {
					t.Errorf("populated extension decoded to %+v, want %+v", got, want)
				}
			} else {
				got := byPath[paths[2]].SemanticObservations[0]
				if got.SchemaVersion != 2 || got.Key.Code != "C1" || got.Completeness != catalog.SemanticComplete || got.Population != 5 {
					t.Errorf("populated semantic observation decoded to %+v", got)
				}
			}

			writeScope := FieldProfileScope{Project: fpDDLProject, DatasetGeneration: fpDDLGen, ResourceType: fpDDLType}
			nilDoc := fpDDLBaseDoc(column + "/json/write-nil")
			emptyDoc := fpDDLBaseDoc(column + "/json/write-empty")
			popDoc := fpDDLBaseDoc(column + "/json/write-populated")
			if column == "extension_values" {
				emptyDoc.ExtensionValues = []catalog.ExtensionValueObservation{}
				popDoc.ExtensionValues = []catalog.ExtensionValueObservation{
					{URL: "http://loom.test/ext/b", SourcePath: "extension", ValuePath: "valueCode", ValueType: "code"},
				}
			} else {
				emptyDoc.SemanticObservations = []catalog.SemanticObservation{}
				popDoc.SemanticObservations = []catalog.SemanticObservation{
					{SchemaVersion: 2, Key: catalog.SemanticObservationKey{Code: "C2"},
						Completeness: catalog.SemanticPartial, Population: 1},
				}
			}
			if err := repo.UpsertFieldProfileBatch(svc.ctx, writeScope,
				[]catalog.FieldCatalogDocument{nilDoc, emptyDoc, popDoc},
				FieldProfileCoverage{FieldProfileScope: writeScope}, 0); err != nil {
				svc.fail("UpsertFieldProfileBatch jsonb encodings", err)
			}
			writePaths := [3]string{column + "/json/write-nil", column + "/json/write-empty", column + "/json/write-populated"}
			if err := fpDDLJSONBWitness(t, svc, column, writePaths); err != nil {
				t.Fatalf("repository encodings failed the three-state witness: %v", err)
			}

			fpDDLExec(t, svc, fpDDLProfileStmt(svc, map[string]string{
				"path": fpDDLText(column + "/json/collapsed"), column: "NULL",
			}))
			if _, err := svc.iso.Exec(svc.ctx, fmt.Sprintf(
				`UPDATE "%s".fhir_field_profile SET %s = '[]'::jsonb WHERE path = $1`,
				svc.schema, column), column+"/json/collapsed"); err != nil {
				svc.fail("collapse simulation update", err)
			}
			if err := fpDDLJSONBWitness(t, svc, column, [3]string{paths[0], paths[1], column + "/json/collapsed"}); err == nil {
				t.Fatal("three-state witness accepted a collapsed '[]' row; the witness is vacuous")
			}

			var raw json.RawMessage
			if err := svc.iso.QueryRow(svc.ctx, fmt.Sprintf(
				`SELECT %s FROM "%s".fhir_field_profile WHERE path=$1`, column, svc.schema), column+"/json/empty").Scan(&raw); err != nil {
				svc.fail("witness raw '[]' row", err)
			}
			collapsedDoc := catalog.FieldCatalogDocument{}
			collapsedDecode := func() {
				if column == "extension_values" {
					_ = json.Unmarshal(raw, &collapsedDoc.ExtensionValues)
					if len(collapsedDoc.ExtensionValues) == 0 {
						collapsedDoc.ExtensionValues = nil
					}
				} else {
					_ = json.Unmarshal(raw, &collapsedDoc.SemanticObservations)
					if len(collapsedDoc.SemanticObservations) == 0 {
						collapsedDoc.SemanticObservations = nil
					}
				}
			}
			collapsedDecode()
			if !fpDDLJSONBIsNil(column, collapsedDoc) {
				t.Fatal("collapsed decoder variant did not collapse '[]' to nil; probe B is vacuous")
			}
			realDoc := catalog.FieldCatalogDocument{}
			if err := decodeFieldProfileJSON(&realDoc, map[string]json.RawMessage{
				"extension_values": raw, "semantic_observations": nil,
			}[column], map[string]json.RawMessage{
				"extension_values": nil, "semantic_observations": raw,
			}[column]); err != nil {
				svc.fail("real decode of '[]' row", err)
			}
			if fpDDLJSONBIsNil(column, realDoc) {
				t.Fatal("the real decoder collapsed the stored '[]' row to nil; the round trip is broken")
			}
		})
	}
}

func fpDDLJSONBLen(column string, doc catalog.FieldCatalogDocument) int {
	if column == "extension_values" {
		return len(doc.ExtensionValues)
	}
	return len(doc.SemanticObservations)
}

func fpDDLJSONBIsNil(column string, doc catalog.FieldCatalogDocument) bool {
	if column == "extension_values" {
		return doc.ExtensionValues == nil
	}
	return doc.SemanticObservations == nil
}

func TestFieldProfileCoverageMarkerDistinguishesNeverProfiledFromKnownEmpty(t *testing.T) {
	svc := newMigrationService(t)
	if _, err := MigrateSchema(svc.ctx, svc.openClient(t), svc.schema); err != nil {
		svc.fail("MigrateSchema", err)
	}
	repo, err := NewFieldProfileRepository(svc.iso, svc.schema)
	if err != nil {
		t.Fatalf("NewFieldProfileRepository: %v", err)
	}

	neverScope := FieldProfileScope{Project: fpDDLProject, DatasetGeneration: fpDDLGen, AuthResourcePath: "cov/never", ResourceType: fpDDLType}
	emptyScope := FieldProfileScope{Project: fpDDLProject, DatasetGeneration: fpDDLGen, AuthResourcePath: "cov/known-empty", ResourceType: fpDDLType}

	complete := true
	if err := repo.UpsertFieldProfileBatch(svc.ctx, emptyScope, nil,
		FieldProfileCoverage{FieldProfileScope: emptyScope, EnrichmentComplete: &complete, EnrichmentTruncated: false}, 0); err != nil {
		svc.fail("UpsertFieldProfileBatch known-empty", err)
	}

	witness := func() error {
		neverRows, err := repo.ReadFieldProfileRows(svc.ctx, neverScope)
		if err != nil {
			return fmt.Errorf("read never-profiled rows: %w", err)
		}
		emptyRows, err := repo.ReadFieldProfileRows(svc.ctx, emptyScope)
		if err != nil {
			return fmt.Errorf("read known-empty rows: %w", err)
		}
		if len(neverRows) != 0 || len(emptyRows) != 0 {
			return fmt.Errorf("both scopes must sit at zero profile rows to test the collapse point, got %d and %d",
				len(neverRows), len(emptyRows))
		}
		restricted := false
		neverCoverage, err := repo.ReadProfileCoverage(svc.ctx, FieldProfileFilter{
			Project: fpDDLProject, DatasetGeneration: fpDDLGen,
			ResourceTypes: []string{fpDDLType}, AuthResourcePathsUnrestricted: &restricted,
			AuthResourcePaths: []string{"cov/never"},
		})
		if err != nil {
			return fmt.Errorf("read never-profiled coverage: %w", err)
		}
		emptyCoverage, err := repo.ReadProfileCoverage(svc.ctx, FieldProfileFilter{
			Project: fpDDLProject, DatasetGeneration: fpDDLGen,
			ResourceTypes: []string{fpDDLType}, AuthResourcePathsUnrestricted: &restricted,
			AuthResourcePaths: []string{"cov/known-empty"},
		})
		if err != nil {
			return fmt.Errorf("read known-empty coverage: %w", err)
		}
		if len(neverCoverage) != 0 {
			return fmt.Errorf("never-profiled scope carries %d coverage markers, want 0", len(neverCoverage))
		}
		if len(emptyCoverage) != 1 {
			return fmt.Errorf("known-empty scope carries %d coverage markers, want 1", len(emptyCoverage))
		}
		rec := emptyCoverage[0]
		if rec.EnrichmentComplete == nil || *rec.EnrichmentComplete != true || rec.EnrichmentTruncated {
			return fmt.Errorf("known-empty marker = %+v, want EnrichmentComplete=true truncated=false", rec)
		}
		return nil
	}

	if err := witness(); err != nil {
		t.Fatalf("coverage marker witness failed: %v", err)
	}

	if _, err := svc.iso.Exec(svc.ctx, fmt.Sprintf(
		`DELETE FROM "%s".fhir_field_profile_coverage WHERE auth_resource_path = 'cov/known-empty'`,
		svc.schema)); err != nil {
		svc.fail("remove marker row", err)
	}
	if err := witness(); err == nil {
		t.Fatal("witness accepted a state where known-empty and never-profiled are indistinguishable; the probe is vacuous")
	}
}
