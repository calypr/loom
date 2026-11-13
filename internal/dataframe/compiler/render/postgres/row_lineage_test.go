package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sql23AttachTerminal(t *testing.T, plan ir.PhysicalPlan, rowID string, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	plan = ir.ClonePhysicalPlan(plan)
	if plan.StageSequence == nil || len(plan.StageSequence.Stages) != 1 || plan.StageSequence.Stages[0].Group == nil {
		t.Fatalf("fixture is not the inline single-stage GROUP shape")
	}
	root := plan.Operations[0].RootScan.Variable
	returns := 0
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		returns++
		projections := append([]ir.PhysicalProjection{}, operation.Return.Projections...)
		projections = append(projections,
			ir.PhysicalProjection{Name: rowLineageResourceIDColumn, Hidden: true,
				Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", "id"}}},
			ir.PhysicalProjection{Name: rowLineageOccurrenceKeyColumn, Hidden: true,
				Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}},
		)
		operation.Return.Projections = projections
	}
	if returns != 1 {
		t.Fatalf("fixture has %d terminal projections, want 1", returns)
	}
	plan.BindVars["row_lineage_row_id"] = rowID
	plan.BindVars["row_lineage_offset"] = offset
	plan.BindVars["row_lineage_limit"] = limit
	plan.BindVars["row_lineage_fetch_limit"] = limit + 1
	plan.StageSequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{
		RowIDBindKey: "row_lineage_row_id", OffsetBindKey: "row_lineage_offset",
		LimitBindKey: "row_lineage_limit", FetchLimitBindKey: "row_lineage_fetch_limit",
		ResourceType: "Observation", ResourceIDColumn: rowLineageResourceIDColumn,
		OccurrenceKeyColumn: rowLineageOccurrenceKeyColumn,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan with the lineage terminal must validate: %v", err)
	}
	return plan
}

func sql23LineagePlan(t *testing.T, policy recipe.ConstructionGroupMissingKeyPolicy, rowID string, offset, limit int) ir.PhysicalPlan {
	t.Helper()
	return sql23AttachTerminal(t, group13CountRowsPlan(t, policy), rowID, offset, limit)
}

func sql23Render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

func sql23ContributorCell(resourceType, resourceID, occurrenceKey string) string {
	return fmt.Sprintf(`{"occurrenceKey":%q,"resourceId":%s,"resourceType":%q}`,
		occurrenceKey, resourceID, resourceType)
}

func sql23Contributors(t *testing.T, cell string) []string {
	t.Helper()
	var members []struct {
		ResourceType  string          `json:"resourceType"`
		ResourceID    json.RawMessage `json:"resourceId"`
		OccurrenceKey string          `json:"occurrenceKey"`
	}
	if err := json.Unmarshal([]byte(cell), &members); err != nil {
		t.Fatalf("decode contributors %q: %v", cell, err)
	}
	out := make([]string, len(members))
	for i, member := range members {
		out[i] = sql23ContributorCell(member.ResourceType, string(member.ResourceID), member.OccurrenceKey)
	}
	return out
}

func TestRowLineageOfflineStatementShape(t *testing.T) {
	plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 25, 10)
	rendered := sql23Render(t, plan)
	sqlText := rendered.SQL
	for _, want := range []string{
		"WITH __loom_l_src AS (", "__loom_l_rows AS (", "__loom_l_grp AS (",
		"__loom_l_sel AS (", "__loom_l_page AS (",
		`IS NOT DISTINCT FROM`,
		`COLLATE "C" ASC`,
		`"found"`, `"contributors"`, `"hasMore"`,
		"'resourceType'", "'resourceId'", "'occurrenceKey'",
		`["operation","GROUP"]`, `'["construction",'`,
		`OFFSET $`, `LIMIT $`,
		"ORDER BY l_occurrence_key", "ASC NULLS FIRST",
	} {
		if !strings.Contains(sqlText, want) {
			t.Errorf("statement is missing %q:\n%s", want, sqlText)
		}
	}
	for _, forbidden := range []string{"COLLECT", "@row_lineage", "AQL", "OFFSET @", "LIMIT @", " FOR "} {
		if strings.Contains(sqlText, forbidden) {
			t.Errorf("statement carries %q (AQL surface leaked):\n%s", forbidden, sqlText)
		}
	}
	var sawIDPath bool
	for _, parameter := range rendered.Args {
		if text, ok := parameter.Value.(string); ok && text == "{id}" {
			sawIDPath = true
		}
	}
	if !sawIDPath {
		t.Errorf("the resource-id read must bind the payload-relative {id} path:\n%s", sqlText)
	}
	if !strings.Contains(sqlText, `"logical_id" AS "c2"`) && !strings.Contains(sqlText, `"logical_id" AS "c1"`) {
		if !strings.Contains(sqlText, `"logical_id"`) {
			t.Errorf("the occurrence key must read the typed logical_id column:\n%s", sqlText)
		}
	}
	var bigints []int64
	for _, parameter := range rendered.Args {
		if parameter.Type == command.PostgreSQLParameterBigint {
			bigints = append(bigints, parameter.Value.(int64))
		}
	}
	if len(bigints) != 3 || bigints[0] != 25 || bigints[1] != 11 || bigints[2] != 10 {
		t.Fatalf("page coordinate parameters = %v, want [25 11 10]", bigints)
	}
	if strings.Join(rendered.OutputColumns, ",") != "found,contributors,hasMore" {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
	second := sql23Render(t, plan)
	if second.SQL != sqlText {
		t.Fatalf("re-render is not byte-identical")
	}
	if fmt.Sprint(second.Args) != fmt.Sprint(rendered.Args) {
		t.Fatalf("re-render arguments drifted")
	}
}

func TestRowLineageRefusalRegister(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, plan *ir.PhysicalPlan)
		want   string
	}{
		{"row-id-not-string", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars["row_lineage_row_id"] = 7
		}, "row id bind"},
		{"negative-offset", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars["row_lineage_offset"] = -1
		}, "offset bind"},
		{"zero-limit", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars["row_lineage_limit"] = 0
			plan.BindVars["row_lineage_fetch_limit"] = 1
		}, "limit bind"},
		{"fetch-not-ahead", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars["row_lineage_limit"] = 5
			plan.BindVars["row_lineage_fetch_limit"] = 5
		}, "fetch limit bind"},
		{"resource-type-blank", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.RowLineageReturn.ResourceType = " "
		}, "requires its resource type"},
		{"semantic-key", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.SemanticKey = &ir.PhysicalStageGroupSemanticKey{}
		}, "semantic-family contributor page"},
		{"coding-key", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.CodingKey = &ir.PhysicalStageGroupCodingKey{}
		}, "CodingKey contributor page"},
		{"related-occurrence", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.RelatedOccurrence = &ir.PhysicalStageGroupRelatedOccurrenceAdapter{}
		}, "related-occurrence contributor page"},
		{"root-contributor", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.RootContributorInputColumn = "keys"
		}, "root-contributor set"},
		{"missing-key-policy", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.MissingKeyPolicy = "DROP"
		}, "unsupported missing-key policy"},
		{"key-kind", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].Kind = "ARRAY"
		}, "unsupported scalar kind"},
		{"key-uncompiled", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].InputColumn = ""
		}, "must be fully compiled"},
		{"key-hidden", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.Keys[0].InputInternal = true
		}, "hidden or field-sourced input surface"},
		{"preview-window-plan", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.PreviewSourceWindowByRootID = true
		}, "preview source windowing"},
		{"preview-window-sequence", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.PreviewSourceWindowByRootID = true
		}, "preview source windowing"},
		{"auth-path-bind", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.OutputAuthResourcePathBindKey = "output_auth_resource_path"
		}, "composite Combine prefix authorization path"},
		{"preview-limit-zero", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.PreviewLimitBindKey = "limit"
			plan.BindVars["limit"] = 0
		}, "preview limit bind"},
		{"construction-blank", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.BindVars[plan.StageSequence.Stages[0].Group.ConstructionIDBindKey] = "   "
		}, "construction identity bind"},
		{"row-identity-drift", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].RowIdentityColumn = "somewhere-else"
		}, "addressed by the final row identity"},
		{"hidden-column-missing", func(t *testing.T, plan *ir.PhysicalPlan) {
			for index := range plan.Operations {
				operation := &plan.Operations[index]
				if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
					kept := operation.Return.Projections[:0:0]
					for _, projection := range operation.Return.Projections {
						if projection.Name != rowLineageOccurrenceKeyColumn {
							kept = append(kept, projection)
						}
					}
					operation.Return.Projections = kept
				}
			}
		}, "occurrence-key column"},
		{"hidden-column-wrong-shape", func(t *testing.T, plan *ir.PhysicalPlan) {
			for index := range plan.Operations {
				operation := &plan.Operations[index]
				if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
					for projectionIndex := range operation.Return.Projections {
						projection := &operation.Return.Projections[projectionIndex]
						if projection.Name == rowLineageResourceIDColumn {
							projection.Value.Path = []string{"id"}
						}
					}
				}
			}
		}, "not the compiler-minted hidden root read"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5)
			testCase.mutate(t, &plan)
			_, entryErr := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
			_, dispatchErr := renderRowLineagePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
			for label, err := range map[string]error{"entry": entryErr, "dispatch": dispatchErr} {
				if err == nil {
					t.Fatalf("%s must refuse", label)
				}
				if !errors.Is(err, ErrUnsupportedPostgresPlan) {
					t.Fatalf("%s refusal must be typed: %v", label, err)
				}
				if !strings.Contains(err.Error(), testCase.want) {
					t.Fatalf("%s refusal %q lacks %q", label, err, testCase.want)
				}
			}
			if entryErr.Error() != dispatchErr.Error() {
				t.Fatalf("entry/dispatch voices differ:\nentry=%v\ndispatch=%v", entryErr, dispatchErr)
			}
		})
	}
}

func TestRowLineageUnclaimedShapesDualVoices(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(t *testing.T, plan *ir.PhysicalPlan)
		entryWant    string
		dispatchWant string
	}{
		{"stages-composed", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages = append(plan.StageSequence.Stages, ir.ClonePhysicalPlan(*plan).StageSequence.Stages[0])
		}, "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
			"admits exactly one GROUP construction stage"},
		{"traced-terminal", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.RowLineageReturn.Trace = &ir.PhysicalRowLineageTrace{RootKeyBindKey: "row_lineage_root_key"}
		}, "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
			"traced row-lineage terminal"},
		{"missing-bind-keys", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.RowLineageReturn.OffsetBindKey = ""
		}, "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
			"requires its row-id, offset, limit, and fetch-limit bind keys"},
		{"identity-columns-renamed", func(t *testing.T, plan *ir.PhysicalPlan) {
			plan.StageSequence.RowLineageReturn.ResourceIDColumn = "mine"
		}, "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route",
			"compiler-minted identity columns"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5)
			testCase.mutate(t, &plan)
			_, entryErr := RenderPhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
			if entryErr == nil || !errors.Is(entryErr, ErrUnsupportedPostgresPlan) ||
				!strings.Contains(entryErr.Error(), testCase.entryWant) {
				t.Fatalf("entry must keep the merged voice %q: %v", testCase.entryWant, entryErr)
			}
			_, dispatchErr := renderRowLineagePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
			if dispatchErr == nil || !errors.Is(dispatchErr, ErrUnsupportedPostgresPlan) ||
				!strings.Contains(dispatchErr.Error(), testCase.dispatchWant) {
				t.Fatalf("direct door call must speak the slice contract %q: %v", testCase.dispatchWant, dispatchErr)
			}
		})
	}
}

func TestRowLineageDoorClaimsNothingElse(t *testing.T) {
	t.Run("cell-trace-companion", func(t *testing.T) {
		plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5)
		plan.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err == nil || !strings.Contains(err.Error(), "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route") {
			t.Fatalf("companion cell-trace must keep the merged door voice: %v", err)
		}
		_, dispatchErr := renderRowLineagePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "cell-trace returns have no PostgreSQL compiled route") {
			t.Fatalf("direct call must name the companion contract: %v", dispatchErr)
		}
	})
	t.Run("population-mapping-companion", func(t *testing.T) {
		plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5)
		plan.StageSequence.PopulationMappingReturn = &ir.PhysicalStagePopulationMappingReturn{}
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err == nil || !strings.Contains(err.Error(), "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route") {
			t.Fatalf("companion population-mapping must keep the merged door voice: %v", err)
		}
		_, dispatchErr := renderRowLineagePhysicalPlan(ir.ClonePhysicalPlan(plan), mustBinding(t))
		if dispatchErr == nil || !strings.Contains(dispatchErr.Error(), "population-mapping returns have no PostgreSQL compiled route") {
			t.Fatalf("direct call must name the companion contract: %v", dispatchErr)
		}
	})
	t.Run("traced-falls-to-registered-voice", func(t *testing.T) {
		plan := sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5)
		plan.StageSequence.RowLineageReturn.Trace = &ir.PhysicalRowLineageTrace{RootKeyBindKey: "row_lineage_root_key"}
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err == nil || !strings.Contains(err.Error(), "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route") {
			t.Fatalf("a traced page must fall through to the registered refusal: %v", err)
		}
		if !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("fall-through refusal must stay typed: %v", err)
		}
	})
	t.Run("hand-assembled-empty-terminal-falls-through", func(t *testing.T) {
		plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
		plan.StageSequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{}
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err == nil || !strings.Contains(err.Error(), "row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route") {
			t.Fatalf("an unclaimed terminal must keep the merged voice: %v", err)
		}
	})
}

func TestRowLineagePolicyAndOrderArms(t *testing.T) {
	t.Run("exclude-drops-null-key-rows", func(t *testing.T) {
		rendered := sql23Render(t, sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyExclude, "opaque-row-id", 0, 5))
		if !strings.Contains(rendered.SQL, `IS NOT NULL`) {
			t.Fatalf("EXCLUDE must drop null-key rows with a typed-read IS NOT NULL conjunct:\n%s", rendered.SQL)
		}
	})
	t.Run("error-policy-raises-through-the-guard", func(t *testing.T) {
		rendered := sql23Render(t, sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyError, "opaque-row-id", 0, 5))
		if !strings.Contains(rendered.SQL, groupMissingKeyTag) || !strings.Contains(rendered.SQL, "guard_barrier") {
			t.Fatalf("ERROR must raise %q through the guard barrier:\n%s", groupMissingKeyTag, rendered.SQL)
		}
	})
	t.Run("key-type-witness-present", func(t *testing.T) {
		rendered := sql23Render(t, sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 0, 5))
		if !strings.Contains(rendered.SQL, groupKeyTypeTag) {
			t.Fatalf("the STRING key must carry the registered type witness:\n%s", rendered.SQL)
		}
	})
	t.Run("keyless-group-exists-only-with-rows", func(t *testing.T) {
		plan := sql23AttachTerminal(t, group13KeylessPlan(t), "opaque-row-id", 0, 5)
		rendered := sql23Render(t, plan)
		if !strings.Contains(rendered.SQL, "HAVING COUNT(*) > 0") {
			t.Fatalf("the keyless group must exist only when at least one source row does (AQL COLLECT WITH COUNT INTO shape):\n%s", rendered.SQL)
		}
	})
	t.Run("page-capped-at-limit-not-fetch", func(t *testing.T) {
		rendered := sql23Render(t, sql23LineagePlan(t, recipe.ConstructionGroupMissingKeyGroup, "opaque-row-id", 3, 7))
		if strings.Count(rendered.SQL, "LIMIT $") != 2 || !strings.Contains(rendered.SQL, "LIMIT 1") {
			t.Fatalf("statement must bound the page at the fetch limit, cap the published array at the limit, and select one group:\n%s", rendered.SQL)
		}
		var bigints []int64
		for _, parameter := range rendered.Args {
			if parameter.Type == command.PostgreSQLParameterBigint {
				bigints = append(bigints, parameter.Value.(int64))
			}
		}
		if len(bigints) != 3 || bigints[0] != 3 || bigints[1] != 8 || bigints[2] != 7 {
			t.Fatalf("page coordinates = %v, want [3 8 7]", bigints)
		}
	})
}

const sql23Namespace = "verify_sql23v1_"

var (
	sql23FixturesMu      sync.Mutex
	sql23TrackedFixtures = map[string]bool{}
)

func sql23TrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sql23FixturesMu.Lock()
	defer sql23FixturesMu.Unlock()
	sql23TrackedFixtures[database] = true
	sql23TrackedFixtures[svc.schema] = true
}

func sql23SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'verify_sql23v1_%'`+
			` UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify_sql23v1_%'`)
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

func sql23RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	beforeList, err := sql23SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: snapshot before run: %v\n", err)
		return inner()
	}
	if len(beforeList) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: pre-snapshot found %d surviving verify_sql23v1_* resources before this run: %v\n", len(beforeList), beforeList)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := sql23SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	sql23FixturesMu.Lock()
	defer sql23FixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if sql23TrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	var survivors []string
	for _, name := range after {
		if !before[name] && sql23TrackedFixtures[name] {
			survivors = append(survivors, name)
		}
	}
	if len(survivors) > 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-23 delta audit: %d verify_sql23v1_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}

func newSQL23Service(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "verify_sql23v1")
}

func sql23Insert(t *testing.T, svc *pgir02Service, logicalID string, payload map[string]any) {
	t.Helper()
	svc.insertResource(t, svc.schema, group13Project, group13Generation, "Observation", logicalID, group13AuthPath, payload)
}

func sql23Payload(id, status string) map[string]any {
	row := map[string]any{}
	if id != "" {
		row["id"] = id
	}
	if status != "" {
		row["status"] = status
	}
	return row
}

func sql23PreviewIdentity(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan, statusKey string) string {
	t.Helper()
	preview := ir.ClonePhysicalPlan(plan)
	preview.StageSequence.RowLineageReturn = nil
	rendered := group13RenderSvc(t, svc, preview)
	rows := svc.runRendered(t, rendered)
	for _, row := range rows {
		cell := row[0]
		if statusKey == "<null>" && cell != "<null>" {
			continue
		}
		if statusKey != "<null>" && cell != statusKey {
			continue
		}
		return row[len(row)-1]
	}
	t.Fatalf("preview published no group with key %q: %v", statusKey, rows)
	return ""
}

func sql23RunPage(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) (found, hasMore string, contributors []string) {
	t.Helper()
	rendered := group13RenderSvc(t, svc, plan)
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := svc.iso.Query(svc.ctx,
		`SELECT w."found"::text, w."contributors"::text, w."hasMore"::text FROM (`+rendered.SQL+`) AS w`, args...)
	if err != nil {
		t.Fatalf("execute contributor page: %v", err)
	}
	defer rows.Close()
	var foundCells, contributorCells, hasMoreCells []string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("read contributor page row: %v", err)
		}
		if len(values) != 3 {
			t.Fatalf("contributor page row must carry found/contributors/hasMore: %v", values)
		}
		foundCells = append(foundCells, stringifyCell(values[0]))
		contributorCells = append(contributorCells, stringifyCell(values[1]))
		hasMoreCells = append(hasMoreCells, stringifyCell(values[2]))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("finish contributor page: %v", err)
	}
	if len(foundCells) != 1 {
		t.Fatalf("the contributor page must return exactly one found/contributors/hasMore row: %d", len(foundCells))
	}
	return foundCells[0], hasMoreCells[0], sql23Contributors(t, contributorCells[0])
}

func TestRowLineageLivePreviewRoundTrip(t *testing.T) {
	svc := newSQL23Service(t)
	sql23TrackFixtures(t, svc)
	sql23Insert(t, svc, "obs-a", sql23Payload("obs-a", "final"))
	sql23Insert(t, svc, "obs-b", sql23Payload("obs-b", "final"))
	sql23Insert(t, svc, "obs-c", sql23Payload("obs-c", "amended"))
	sql23Insert(t, svc, "obs-d", map[string]any{"id": nil, "status": "final"})

	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	identity := sql23PreviewIdentity(t, svc, plan, "final")
	if want := group13Identity(t, plan, []group13Member{group13TextMember("status_group", "final")}); identity != want {
		t.Fatalf("preview identity %q, want %q", identity, want)
	}

	page := sql23AttachTerminal(t, plan, identity, 0, 10)
	found, hasMore, contributors := sql23RunPage(t, svc, page)
	if found != "true" || hasMore != "false" {
		t.Fatalf("found/hasMore = %s/%s, want true/false", found, hasMore)
	}
	want := []string{
		sql23ContributorCell("Observation", `"obs-a"`, "obs-a"),
		sql23ContributorCell("Observation", `"obs-b"`, "obs-b"),
		sql23ContributorCell("Observation", `null`, "obs-d"),
	}
	if strings.Join(contributors, "|") != strings.Join(want, "|") {
		t.Fatalf("contributors mismatch:\n got %v\nwant %v", contributors, want)
	}

	if again, _, againMembers := sql23RunPage(t, svc, page); again != "true" || strings.Join(againMembers, "|") != strings.Join(want, "|") {
		t.Fatalf("page drifted between executions: %v", againMembers)
	}

	rendered := group13RenderSvc(t, svc, page)
	for _, leaked := range []string{group13Project, group13Generation, group13AuthPath, "final", "amended", identity} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, rendered.SQL)
		}
	}

	missing := sql23AttachTerminal(t, plan, `[["construction","pg13_group"],["operation","GROUP"],["status_group","cancelled"]]`, 0, 10)
	found, hasMore, contributors = sql23RunPage(t, svc, missing)
	if found != "false" || hasMore != "false" || strings.Join(contributors, "|") != "" {
		t.Fatalf("missing row must read found=false with an empty array: %s %s %v", found, hasMore, contributors)
	}
}

func TestRowLineageLiveNullKeyGroup(t *testing.T) {
	svc := newSQL23Service(t)
	sql23TrackFixtures(t, svc)
	sql23Insert(t, svc, "obs-n1", sql23Payload("obs-n1", ""))
	sql23Insert(t, svc, "obs-n2", map[string]any{"id": "obs-n2", "status": nil})
	sql23Insert(t, svc, "obs-k", sql23Payload("obs-k", "final"))

	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	identity := sql23PreviewIdentity(t, svc, plan, "<null>")
	if want := group13Identity(t, plan, []group13Member{group13NullMember("status_group")}); identity != want {
		t.Fatalf("null-key identity %q, want %q", identity, want)
	}
	found, hasMore, contributors := sql23RunPage(t, svc, sql23AttachTerminal(t, plan, identity, 0, 10))
	if found != "true" || hasMore != "false" {
		t.Fatalf("the null-key group must resolve: %s/%s", found, hasMore)
	}
	want := []string{
		sql23ContributorCell("Observation", `"obs-n1"`, "obs-n1"),
		sql23ContributorCell("Observation", `"obs-n2"`, "obs-n2"),
	}
	if strings.Join(contributors, "|") != strings.Join(want, "|") {
		t.Fatalf("null-key contributors (AQL == is null-equal; SQL = would drop them):\n got %v\nwant %v", contributors, want)
	}

	found, hasMore, contributors = sql23RunPage(t, svc, sql23AttachTerminal(t,
		group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyExclude), identity, 0, 10))
	if found != "false" || hasMore != "false" || strings.Join(contributors, "|") != "" {
		t.Fatalf("EXCLUDE must erase the null-key group: %s %s %v", found, hasMore, contributors)
	}
}

func TestRowLineageLivePaging(t *testing.T) {
	svc := newSQL23Service(t)
	sql23TrackFixtures(t, svc)
	for _, id := range []string{"p-a", "p-b", "p-c", "p-d", "p-e"} {
		sql23Insert(t, svc, id, sql23Payload(id, "final"))
	}
	svc.insertResource(t, svc.schema, group13Project, group13Generation, "Observation", "w-denied", "FHIR/Hidden", sql23Payload("w-denied", "final"))
	svc.insertResource(t, svc.schema, group13Project, "p23-old-gen", "Observation", "w-oldgen", group13AuthPath, sql23Payload("w-oldgen", "final"))
	svc.insertResource(t, svc.schema, "p23-foreign", group13Generation, "Observation", "w-foreign", group13AuthPath, sql23Payload("w-foreign", "final"))
	svc.insertResource(t, svc.schema, group13Project, group13Generation, "Patient", "w-patient", group13AuthPath, sql23Payload("w-patient", "final"))

	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	identity := sql23PreviewIdentity(t, svc, plan, "final")

	cells := func(ids ...string) string {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = sql23ContributorCell("Observation", `"`+id+`"`, id)
		}
		return strings.Join(parts, "|")
	}
	cases := []struct {
		offset, limit int
		want          string
		hasMore       string
	}{
		{0, 2, cells("p-a", "p-b"), "true"},
		{2, 2, cells("p-c", "p-d"), "true"},
		{4, 2, cells("p-e"), "false"},
		{5, 2, "", "false"},
		{0, 4, cells("p-a", "p-b", "p-c", "p-d"), "true"},
		{0, 5, cells("p-a", "p-b", "p-c", "p-d", "p-e"), "false"},
		{0, 100, cells("p-a", "p-b", "p-c", "p-d", "p-e"), "false"},
	}
	for _, testCase := range cases {
		found, hasMore, contributors := sql23RunPage(t, svc, sql23AttachTerminal(t, plan, identity, testCase.offset, testCase.limit))
		if found != "true" {
			t.Fatalf("offset %d limit %d: found = %s, want true", testCase.offset, testCase.limit, found)
		}
		if hasMore != testCase.hasMore {
			t.Fatalf("offset %d limit %d: hasMore = %s, want %s", testCase.offset, testCase.limit, hasMore, testCase.hasMore)
		}
		if got := strings.Join(contributors, "|"); got != testCase.want {
			t.Fatalf("offset %d limit %d: contributors\n got %q\nwant %q", testCase.offset, testCase.limit, got, testCase.want)
		}
		if len(contributors) > testCase.limit {
			t.Fatalf("page exceeded the limit: %d > %d", len(contributors), testCase.limit)
		}
	}

	if _, _, all := sql23RunPage(t, svc, sql23AttachTerminal(t,
		group13KeylessPlan(t), group13Identity(t, group13KeylessPlan(t), nil), 0, 100)); len(all) != 5 {
		t.Fatalf("keyless page must hold exactly the in-scope rows: %v", all)
	}
}

func TestRowLineageLiveCollationOrder(t *testing.T) {
	svc := newSQL23Service(t)
	sql23TrackFixtures(t, svc)
	for _, id := range []string{"k-B", "k-C", "k-a", "k-b"} {
		sql23Insert(t, svc, id, sql23Payload(id, "case"))
	}

	plan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	identity := sql23PreviewIdentity(t, svc, plan, "case")

	cases := []struct {
		offset, limit int
		want          []string
		hasMore       string
	}{
		{0, 1, []string{"k-B"}, "true"},
		{0, 2, []string{"k-B", "k-C"}, "true"},
		{1, 1, []string{"k-C"}, "true"},
		{0, 4, []string{"k-B", "k-C", "k-a", "k-b"}, "false"},
	}
	for _, testCase := range cases {
		found, hasMore, contributors := sql23RunPage(t, svc,
			sql23AttachTerminal(t, plan, identity, testCase.offset, testCase.limit))
		if found != "true" {
			t.Fatalf("collation page not found: offset %d limit %d", testCase.offset, testCase.limit)
		}
		if hasMore != testCase.hasMore {
			t.Fatalf("offset %d limit %d: hasMore = %s, want %s", testCase.offset, testCase.limit, hasMore, testCase.hasMore)
		}
		if got := strings.Join(contributors, "|"); got != strings.Join(cellsOf(testCase.want), "|") {
			t.Fatalf("offset %d limit %d: collation order\n got %v\nwant %v", testCase.offset, testCase.limit, contributors, testCase.want)
		}
	}
}

func cellsOf(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = sql23ContributorCell("Observation", `"`+id+`"`, id)
	}
	return out
}

func TestRowLineageLiveGuardRaises(t *testing.T) {
	svc := newSQL23Service(t)
	sql23TrackFixtures(t, svc)
	sql23Insert(t, svc, "ok-1", sql23Payload("ok-1", "final"))
	sql23Insert(t, svc, "ok-2", sql23Payload("ok-2", "final"))

	base := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup)
	identity := sql23PreviewIdentity(t, svc, base, "final")

	if found, _, members := sql23RunPage(t, svc, sql23AttachTerminal(t, base, identity, 0, 10)); found != "true" || len(members) != 2 {
		t.Fatalf("healthy page: %s %v", found, members)
	}

	sql23Insert(t, svc, "poison-null", sql23Payload("poison-null", ""))
	errorPlan := group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyError)
	group13ExpectRaise(t, svc, group13RenderSvc(t, svc, sql23AttachTerminal(t, errorPlan, identity, 0, 10)), groupMissingKeyTag)

	sql23Insert(t, svc, "poison-type", map[string]any{"id": "poison-type", "status": 7})
	group13ExpectRaise(t, svc, group13RenderSvc(t, svc, sql23AttachTerminal(t,
		group13CountRowsPlan(t, recipe.ConstructionGroupMissingKeyGroup), identity, 0, 10)), groupKeyTypeTag)
}
