package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

var (
	excl26FixturesMu      sync.Mutex
	excl26TrackedFixtures = map[string]bool{}
)

func excl26LiveService(t *testing.T) *pgir02Service {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql26v1")
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	excl26FixturesMu.Lock()
	defer excl26FixturesMu.Unlock()
	excl26TrackedFixtures[database] = true
	excl26TrackedFixtures[svc.schema] = true
	return svc
}

func excl26Binding(t *testing.T, svc *pgir02Service) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(svc.schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

func excl26Seed(t *testing.T, svc *pgir02Service, logicalID string, payload any) {
	t.Helper()
	svc.insertResource(t, svc.schema, excl26Project, excl26Generation, "Patient", logicalID, excl26AuthPath, payload)
}

func excl26Doc(logicalID, fields string) json.RawMessage {
	return json.RawMessage(`{"resourceType":"Patient","id":"` + logicalID + `"` + fields + `}`)
}

func excl26StringCategories() []ir.PhysicalGroupedPivotCategory {
	return []ir.PhysicalGroupedPivotCategory{
		{Output: "cell_zero", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_zero", ValueKind: "STRING"},
		{Output: "cell_one", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "cat_one", ValueKind: "STRING"},
	}
}

func excl26EvidencePlan(t *testing.T, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	plan := excl26Plan(t, excl26Config{
		categories: excl26StringCategories(),
		offset:     offset,
		limit:      limit,
		zeroOffset: offset == 0,
	})
	plan.BindVars["cat_zero"] = "alpha-zero"
	plan.BindVars["cat_one"] = "alpha-one"
	return plan
}

func excl26RenderLive(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderTableShapeExclusions(plan, excl26Binding(t, svc))
	if err != nil {
		t.Fatalf("render evidence terminal live: %v", err)
	}
	want := []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}
	if strings.Join(rendered.OutputColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("evidence output columns = %v, want the nine frozen fields", rendered.OutputColumns)
	}
	return rendered
}

func excl26ColIndex(t *testing.T, columns []string, field string) int {
	t.Helper()
	for index, name := range columns {
		if name == field {
			return index
		}
	}
	t.Fatalf("output columns %v lack %q", columns, field)
	return -1
}

func TestExclusion26LiveEvidenceRows(t *testing.T) {
	svc := excl26LiveService(t)
	excl26Seed(t, svc, "e1", excl26Doc("e1", `,"gProp":"G1","cProp":"alpha-zero","vProp":1`))
	excl26Seed(t, svc, "e2", excl26Doc("e2", `,"gProp":"G1","cProp":"alpha-one","vProp":1`))
	excl26Seed(t, svc, "e3", excl26Doc("e3", `,"gProp":"G1","cProp":"zzz","vProp":1`))
	excl26Seed(t, svc, "e4", excl26Doc("e4", `,"gProp":"G1","cProp":"zzz","vProp":2`))
	excl26Seed(t, svc, "e5", excl26Doc("e5", `,"gProp":"G1","vProp":1`))
	excl26Seed(t, svc, "e6", excl26Doc("e6", `,"gProp":"G1","cProp":null,"vProp":1`))
	excl26Seed(t, svc, "e7", excl26Doc("e7", `,"gProp":"G2","cProp":"alpha-zero","vProp":1`))
	svc.insertResource(t, svc.schema, "other-project", excl26Generation, "Patient", "x1", excl26AuthPath,
		excl26Doc("x-scope", `,"gProp":"G9","cProp":"nope"`))
	svc.insertResource(t, svc.schema, excl26Project, "other-gen", "Patient", "x2", excl26AuthPath,
		excl26Doc("x-scope", `,"gProp":"G9","cProp":"nope"`))
	svc.insertResource(t, svc.schema, excl26Project, excl26Generation, "Patient", "x3", "FHIR/Observation",
		excl26Doc("x-scope", `,"gProp":"G9","cProp":"nope"`))

	plan := excl26Plan(t, excl26Config{
		categories: excl26StringCategories(),
		presence:   "contract",
		offset:     0,
		zeroOffset: true,
		limit:      100,
	})
	plan.BindVars["cat_zero"] = "alpha-zero"
	plan.BindVars["cat_one"] = "alpha-one"
	out := excl26RenderLive(t, svc, plan)
	rows := svc.runRendered(t, out)

	idCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionOutputRowIDField)
	categoryCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionCategoryValueField)
	presentCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionCategoryPresentField)
	statusCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionIdentityStatusField)
	reasonCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionReasonField)

	if len(rows) != 4 {
		t.Fatalf("evidence rows = %d, want the four unlisted G1 contributors:\n%v", len(rows), rows)
	}
	for _, row := range rows {
		if row[reasonCol] != ir.PhysicalTableShapeExclusionReasonUnlistedCategory {
			t.Fatalf("reason = %q, want %q", row[reasonCol], ir.PhysicalTableShapeExclusionReasonUnlistedCategory)
		}
		if row[statusCol] != ir.PhysicalTableShapeExclusionIdentityExact {
			t.Fatalf("identity status = %q, want %q (AQL :75 identity-available branch)", row[statusCol], ir.PhysicalTableShapeExclusionIdentityExact)
		}
		if !strings.HasPrefix(row[idCol], `["GROUPED_PIVOT","p26-hand-construct",["STRING","G1"]]`) {
			t.Fatalf("identity = %q, want the G1 pivot identity (AQL :137-150)", row[idCol])
		}
	}
	var zzz, absent, storedNull int
	for _, row := range rows {
		switch {
		case row[categoryCol] == "zzz" && row[presentCol] == "true":
			zzz++
		case row[categoryCol] == "<null>" && row[presentCol] == "false":
			absent++
		case (row[categoryCol] == "null" || row[categoryCol] == "<null>") && row[presentCol] == "true":
			storedNull++
		}
	}
	if zzz != 2 || absent != 1 || storedNull != 1 {
		t.Fatalf("tri-state split = zzz:%d absent:%d null:%d, want 2/1/1:\n%v", zzz, absent, storedNull, rows)
	}

	var witness int
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT count(*) FROM `+svc.schema+`.fhir_resource
		  WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3 AND resource_type = 'Patient'
		    AND payload #> '{gProp}' IS NOT NULL
		    AND NOT COALESCE(jsonb_typeof(payload #> '{cProp}') = 'string'
		             AND (payload #> '{cProp}' = '"alpha-zero"'::jsonb OR payload #> '{cProp}' = '"alpha-one"'::jsonb), false)`,
		excl26Project, excl26Generation, excl26AuthPath).Scan(&witness); err != nil {
		svc.fail("direct-SQL witness", err)
	}
	if witness != 4 {
		t.Fatalf("direct-SQL witness = %d, want 4", witness)
	}

	empty := excl26EvidencePlan(t, 0, 100)
	empty.BindVars["auth_resource_paths"] = []string{"FHIR/Encounter"}
	emptyOut := excl26RenderLive(t, svc, empty)
	if rows := svc.runRendered(t, emptyOut); len(rows) != 0 {
		t.Fatalf("foreign auth scope returned %d evidence rows, want zero (no phantom row):\n%v", len(rows), rows)
	}
}

func TestExclusion26LiveIdentityUnavailable(t *testing.T) {
	svc := excl26LiveService(t)
	excl26Seed(t, svc, "d1", excl26Doc("d1", `,"gProp":"G1","cProp":"alpha-zero"`))
	excl26Seed(t, svc, "d2", nil)

	out := excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 100))
	rows := svc.runRendered(t, out)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want only the null-document contributor:\n%v", len(rows), rows)
	}
	row := rows[0]
	statusCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionIdentityStatusField)
	typeCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionResourceTypeField)
	idCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionResourceIDField)
	omissionCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionOmissionField)
	if row[statusCol] != ir.PhysicalTableShapeExclusionIdentityUnavailable {
		t.Fatalf("identity status = %q, want %q", row[statusCol], ir.PhysicalTableShapeExclusionIdentityUnavailable)
	}
	if row[typeCol] != "<null>" || row[idCol] != "<null>" {
		t.Fatalf("null-document row leaked document fields: %v", row)
	}
	if row[omissionCol] != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
		t.Fatalf("omission = %q, want %q (AQL :132 omission rides only the unavailable branch)",
			row[omissionCol], ir.PhysicalTableShapeExclusionSourceIdentityUnavailable)
	}
}

func TestExclusion26LiveMatchesPivotUnlistedCount(t *testing.T) {
	svc := excl26LiveService(t)
	seeds := []struct {
		id     string
		fields string
	}{
		{"m1", `,"gProp":"GA","cProp":"alpha-zero","vProp":1`},
		{"m2", `,"gProp":"GA","cProp":"beta","vProp":1`},
		{"m3", `,"gProp":"GA","cProp":"beta","vProp":2`},
		{"m4", `,"gProp":"GA","vProp":1`},
		{"m5", `,"gProp":"GB","cProp":"alpha-one","vProp":1`},
		{"m6", `,"gProp":"GB","cProp":"gamma","vProp":1`},
		{"m7", `,"gProp":"GB","cProp":null,"vProp":1`},
		{"m8", `,"gProp":"GC","cProp":"alpha-zero","vProp":1`},
	}
	for _, seed := range seeds {
		excl26Seed(t, svc, seed.id, excl26Doc(seed.id, seed.fields))
	}

	pivotPlan := excl26EvidencePlan(t, 0, 100)
	pivot := pivotPlan.Operations[1].TableShapeExclusionReturn.Pivot
	pivot.UnlistedEvidenceColumn = "__loom_reshape_unlisted_count"
	pivotPlan.Operations[1] = ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &pivot}
	pivotPlan.Operations = append(pivotPlan.Operations, ir.PhysicalOperation{
		Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: pivotReturnProjections(pivot)},
	})
	pivotRendered, err := RenderPhysicalPlan(pivotPlan, excl26Binding(t, svc))
	if err != nil {
		t.Fatalf("render pivot leg live: %v", err)
	}
	grpCol := excl26ColIndex(t, pivotRendered.OutputColumns, "grp")
	countCol := excl26ColIndex(t, pivotRendered.OutputColumns, "__loom_reshape_unlisted_count")
	pivotRows := svc.runRendered(t, pivotRendered)
	wantPerGroup := map[string]string{}
	for _, row := range pivotRows {
		wantPerGroup[row[grpCol]] = row[countCol]
	}
	if wantPerGroup["GA"] != "3" || wantPerGroup["GB"] != "2" || wantPerGroup["GC"] != "0" {
		t.Fatalf("pivot unlisted counts drifted from the fixture (GA/GB/GC = %v/%v/%v):\n%v",
			wantPerGroup["GA"], wantPerGroup["GB"], wantPerGroup["GC"], pivotRows)
	}

	out := excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 100))
	rows := svc.runRendered(t, out)
	idCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionOutputRowIDField)
	gotPerGroup := map[string]int{}
	for _, row := range rows {
		for _, group := range []string{"GA", "GB", "GC"} {
			if strings.Contains(row[idCol], `,"`+group+`"]]`) {
				gotPerGroup[group]++
			}
		}
	}
	for group, want := range wantPerGroup {
		if fmt.Sprint(gotPerGroup[group]) != want {
			t.Fatalf("group %s: evidence rows %d, pivot unlisted count %s — predicate re-port diverged:\n%v",
				group, gotPerGroup[group], want, rows)
		}
	}

	if _, err := svc.iso.Exec(svc.ctx,
		`UPDATE `+svc.schema+`.fhir_resource SET payload = payload || '{"vProp":42}'::jsonb
		  WHERE project = $1 AND dataset_generation = $2`, excl26Project, excl26Generation); err != nil {
		svc.fail("perturb value column", err)
	}
	after := svc.runRendered(t, out)
	if strings.Join(flattenRows(after), "|") != strings.Join(flattenRows(rows), "|") {
		t.Fatalf("value-column perturbation perturbed the evidence rows:\nbefore %v\nafter  %v", rows, after)
	}
}

func pivotReturnProjections(pivot ir.PhysicalGroupedPivot) []ir.PhysicalProjection {
	names := []string{pivotRowIDColumn}
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		names = append(names, name)
	}
	for _, category := range pivot.Categories {
		names = append(names, category.Output)
	}
	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		names = append(names, pivot.UnlistedEvidenceColumn)
	}
	projections := make([]ir.PhysicalProjection, 0, len(names))
	for _, name := range names {
		projections = append(projections, ir.PhysicalProjection{
			Name:  name,
			Value: ir.PhysicalValue{Variable: pivot.OutputRowVariable, Path: []string{name}},
		})
	}
	return projections
}

func flattenRows(rows [][]string) []string {
	var out []string
	for _, row := range rows {
		out = append(out, strings.Join(row, "\x1f"))
	}
	return out
}

func TestExclusion26LivePaging(t *testing.T) {
	svc := excl26LiveService(t)
	for index := 0; index < 5; index++ {
		excl26Seed(t, svc, fmt.Sprintf("p%d", index),
			excl26Doc(fmt.Sprintf("p%d", index), fmt.Sprintf(`,"gProp":"G1","cProp":"unlisted-%d"`, index)))
	}
	full := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 100)))
	if len(full) != 5 {
		t.Fatalf("unpaginated evidence = %d, want 5:\n%v", len(full), full)
	}
	page1 := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 2)))
	page2 := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 2, 2)))
	if len(page1) != 3 || len(page2) != 3 {
		t.Fatalf("probe pages = %d/%d, want the limit+1 fetch of 2/2 (3 each):\n%v\n%v", len(page1), len(page2), page1, page2)
	}
	recombined := append(append([][]string{}, page1[:2]...), page2[:2]...)
	for index := range recombined {
		if strings.Join(recombined[index], "\x1f") != strings.Join(full[index], "\x1f") {
			t.Fatalf("page1++page2 diverges from the unpaginated list at %d:\n%v\n%v", index, recombined, full)
		}
	}
	tail := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 4, 2)))
	if len(tail) != 1 {
		t.Fatalf("tail page = %d rows, want 1 (no phantom probe row):\n%v", len(tail), tail)
	}
	probe := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 4)))
	if len(probe) != 5 {
		t.Fatalf("limit-4 probe returned %d rows, want the fifth row to ride the limit+1 fetch:\n%v", len(probe), probe)
	}
	again := svc.runRendered(t, excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 2)))
	if strings.Join(flattenRows(again), "|") != strings.Join(flattenRows(page1), "|") {
		t.Fatalf("page order is not deterministic:\n%v\n%v", page1, again)
	}
}

func TestExclusion26LiveHostileValues(t *testing.T) {
	svc := excl26LiveService(t)
	hostile := []string{
		`a"b`,
		`a'b`,
		`a\b`,
		`'; DROP TABLE fhir_resource;--`,
		`é中`,
		`１２３`,
	}
	for index, value := range hostile {
		encoded, err := json.Marshal(map[string]string{
			"resourceType": "Patient",
			"id":           fmt.Sprintf("h%d", index),
			"gProp":        value,
			"cProp":        fmt.Sprintf(`un"listed\%c`, 'A'+rune(index)),
		})
		if err != nil {
			t.Fatalf("marshal hostile fixture: %v", err)
		}
		excl26Seed(t, svc, fmt.Sprintf("h%d", index), json.RawMessage(encoded))
	}
	out := excl26RenderLive(t, svc, excl26EvidencePlan(t, 0, 100))
	rows := svc.runRendered(t, out)
	if len(rows) != len(hostile) {
		t.Fatalf("rows = %d, want %d", len(rows), len(hostile))
	}
	idCol := excl26ColIndex(t, out.OutputColumns, ir.PhysicalTableShapeExclusionOutputRowIDField)
	var got, want []string
	for _, row := range rows {
		got = append(got, row[idCol])
	}
	for _, value := range hostile {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal identity member: %v", err)
		}
		want = append(want, `["GROUPED_PIVOT","p26-hand-construct",["STRING",`+string(encoded)+`]]`)
	}
	sortStrings(want)
	sortStrings(got)
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("hostile identity %d = %q, want %q", index, got[index], want[index])
		}
	}
	for _, value := range hostile {
		if strings.Contains(out.SQL, value) {
			t.Fatalf("hostile value %q reached the statement text", value)
		}
	}
	var survivors int
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT count(*) FROM `+svc.schema+`.fhir_resource WHERE project = $1`, excl26Project).Scan(&survivors); err != nil {
		svc.fail("count survivors", err)
	}
	if survivors != len(hostile) {
		t.Fatalf("surviving rows = %d, want %d (parameters are inert)", survivors, len(hostile))
	}
}

func excl26SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify_sql26v1_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_sql26v1_%'`)
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %v", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func excl26RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := excl26SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: pre-snapshot found %d surviving verify_sql26v1_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := excl26SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	excl26FixturesMu.Lock()
	defer excl26FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if excl26TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	survivors, err := excl26SnapshotOwnedResources(ctx, raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-26 delta audit: %d verify_sql26v1_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

func TestZZNoLeftoverExcl26Resources(t *testing.T) {
	if err := auditNoLeftoverVerifyNamespace(excl26Prefix); err != nil {
		t.Fatalf("zero-leftover census: %v", err)
	}
}
