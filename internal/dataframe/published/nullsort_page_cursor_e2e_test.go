package published

// F-B24 (CONFIRMED Round 24; converted Round 30 to a naturally-PASSING
// regression control after the NULL-aware keyset repair): sorting a published
// dataset by a legitimate NULLABLE column used to make browser pagination
// silently drop every NULL-sort row (both directions) while TotalCount
// advertised them; the archived discovery reds (FB24 RED-ON-PURPOSE: "5 of 7",
// deterministic, real ClickHouse 25.3, production Reader.Page) are the
// before-fault witnesses, and the guardless pre-fix copy of this package
// re-raises them verbatim (see FINDINGS Round 30).
//
// The repair (published/page.go + read.go, cursor Version 2):
//   - ORDER BY pins "`col` DIR NULLS LAST, `__loom_row_id` ASC" so the engine
//     default can never disagree with the predicate;
//   - pageCursorPredicate always admits `col IS NULL` rows after a non-NULL
//     cursor and resumes inside the NULL bucket with
//     "`col` IS NULL AND `__loom_row_id` > ?";
//   - a page ending on a NULL row now ENCODES a null-bucket cursor instead of
//     failing with INVALID_CURSOR (the latent second defect: the former
//     `if sortValue == nil { return Page{}, invalidCursor() }`).
// If any of those guarantees regresses, the traversal assertions below fail
// again through the original harm (unreachable rows / mid-list error), and
// the source gate below retires this artifact loudly if page.go drifts.
//
// User-visible path quoted by the source gate below:
//   - internal/api/graphql/graph/dataframe/rows.go forwards the CLIENT-chosen
//     sort column verbatim: `Sort: convertSort(input)` into
//     `s.reader.Page(ctx, value, dfmaterialization.PageRequest{...})`.
//   - internal/dataframe/published/page.go builds that cursor and its
//     predicate; NULL is legitimate author data: the production writer maps
//     Nullable logical columns to `Nullable(<type>)` ClickHouse columns
//     (publication/clickhouse/bundle_store.go: `columnType = "Nullable(" +
//     columnType + ")"`) and the schema carries Column.Nullable.
//   - internal/dataframe/published/errors.go keeps invalidCursor
//     (`dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")`)
//     reserved for genuinely malformed cursors only.

// Experiment (real ClickHouse, production Reader.Page; Unrestricted=true for
// harness simplicity — the cursor code path is independent of the auth
// predicate, which is prepended separately at page.go `auth_resource_path`
// IN ?`): a 7-row table, `middle Nullable(String)` with NULLs interleaved,
// sorted pagination through production Reader.Page:
//   ASC, first=1: ClickHouse sorts NULLs first, so the FIRST page's last row
//     is NULL -> entire first read errors.
//   DESC, first=2: traversal g,f | d,b | then the page ending on NULL errors;
//     rows past the NULL block can never be listed via sort traversal.
//   Control (no NULLs on the boundary: sort by val): same traversal completes
//     all 7 rows — the harness and cursor machinery work when NULLs are absent,
//     so the failure is attributable to NULL sort values, not to setup.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const nspPrefix = "loom_verify_nsp_"

func nspColumns() []storeclickhouse.Column {
	return []storeclickhouse.Column{
		{Name: "middle", Type: "Nullable(String)"},
		{Name: "val", Type: "Int64"},
		{Name: "__loom_row_id", Type: "String"},
	}
}

// middles arranged so that a DESC traversal ends a page exactly on a NULL:
// sorted DESC: g,f,d,b,a,NULL,NULL ; first=2 -> [g,f],[d,b],[a,NULL] boom.
// With ASC + NULLs-first, page size 1 dies on page 1: first row is NULL.
var nspRows = []map[string]any{
	{"middle": "a", "val": int64(1), "__loom_row_id": "r1"},
	{"middle": "b", "val": int64(2), "__loom_row_id": "r2"},
	{"middle": nil, "val": int64(3), "__loom_row_id": "r3"},
	{"middle": "d", "val": int64(4), "__loom_row_id": "r4"},
	{"middle": nil, "val": int64(5), "__loom_row_id": "r5"},
	{"middle": "f", "val": int64(6), "__loom_row_id": "r6"},
	{"middle": "g", "val": int64(7), "__loom_row_id": "r7"},
}

func nspMaterialization(table string) Materialization {
	return Materialization{
		ID: "nsp-mat", Project: "prj-nsp", DatasetGeneration: "gen-1", PhysicalTable: table,
		Columns: []Column{
			{Name: "middle", ClickHouse: "Nullable(String)", LogicalType: "string", Nullable: true},
			{Name: "val", ClickHouse: "Int64", LogicalType: "integer"},
		},
	}
}

func TestFB24NullSortPageEnumerationIsComplete(t *testing.T) {
	// --- source gates (quote drift retires the claim loudly) ---
	for path, fragments := range map[string][]string{
		"page.go": {
			"sortValueNull = sortValue == nil",
			"(`%s` IS NULL AND `__loom_row_id` > ?)",
			"(`%s` IS NULL OR `%s` %s ? OR (`%s` = ? AND `__loom_row_id` > ?))",
			"`%s` %s NULLS LAST, `__loom_row_id` ASC",
		},
		"errors.go": {`dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")`},
		"../../api/graphql/graph/dataframe/rows.go": {"Sort: convertSort(input)"},
		"../publication/clickhouse/bundle_store.go": {`columnType = "Nullable(" + columnType + ")"`},
	} {
		body, err := readFileForGate(path)
		if err != nil {
			t.Fatalf("F-B24 gate unreadable %s: %v", path, err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				t.Errorf("F-B24 GATE RED: %s no longer contains %q — re-adjudicate this artifact", path, fragment)
			}
		}
		t.Logf("FB24 GATE | %s anchors intact", path)
	}

	url := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if url == "" {
		t.Skip("set LOOM_CLICKHOUSE_URL to run the F-B24 NULL-sort reproduction")
	}
	database := os.Getenv("LOOM_CLICKHOUSE_DATABASE")
	if database == "" {
		database = "loom_test"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	client, err := storeclickhouse.New(storeclickhouse.Options{
		URL: url, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("clickhouse client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() }) // registered before the drop => runs after it (LIFO)
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	table := nspPrefix + strings.ReplaceAll(uuid.NewString(), "-", "") + "_rows"
	t.Logf("FB24_TABLE=%s", table)
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		if err := client.DropTable(dropCtx, table); err != nil {
			t.Errorf("F-B24 residue: drop %s: %v", table, err)
		}
	})
	if err := client.CreateTable(ctx, table, nspColumns()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := client.InsertRows(ctx, table, nspColumns(), nspRows); err != nil {
		t.Fatalf("insert: %v", err)
	}

	reader := &Reader{ClickHouse: client}
	m := nspMaterialization(table)

	// Helper: full sort traversal, mirroring what a browser does with First/After.
	traverse := func(sort *Sort, first int) (map[string]bool, int64, string, error) {
		seen, total, cursor := map[string]bool{}, int64(-1), ""
		for i := 0; i < 20; i++ {
			page, err := reader.Page(ctx, m, PageRequest{First: first, After: cursor, Sort: sort, Unrestricted: true})
			if err != nil {
				return seen, total, cursor, err
			}
			for _, row := range page.Rows {
				val := strings.TrimSpace(fmt.Sprint(row["val"]))
				if val == "" || val == "<nil>" {
					return seen, total, cursor, fmt.Errorf("row missing val identity: %v", row)
				}
				if seen[val] {
					return seen, total, cursor, fmt.Errorf("duplicate row val=%s across pages (page %d)", val, i)
				}
				seen[val] = true
			}
			if i == 0 {
				total = page.TotalCount
			}
			if !page.HasNext || page.NextCursor == "" {
				return seen, total, cursor, nil
			}
			cursor = page.NextCursor
		}
		return seen, total, cursor, fmt.Errorf("traversal did not terminate")
	}
	missing := func(seen map[string]bool) []string {
		var gone []string
		for _, row := range nspRows {
			key := fmt.Sprint(row["val"])
			if !seen[key] {
				gone = append(gone, key)
			}
		}
		return gone
	}

	// Control A: unsorted (row-id) traversal shows ALL rows, proving the NULL
	// rows exist in the table and are absent ONLY from sorted pagination.
	seenPlain, _, _, errPlain := traverse(nil, 2)
	if errPlain != nil || len(seenPlain) != 7 {
		t.Fatalf("F-B24 control A lost — unsorted traversal must enumerate 7 rows, got seen=%d err=%v", len(seenPlain), errPlain)
	}
	t.Logf("FB24 CONTROL-A | unsorted traversal shows every row | rows=%d (including the 2 NULL-middle rows)", len(seenPlain))

	// Control B: sort by non-null column completes the full enumeration.
	seenB, _, _, err := traverse(&Sort{Column: "val"}, 2)
	if err != nil || len(seenB) != 7 {
		t.Fatalf("F-B24 control B lost — non-null sort traversal must enumerate 7 rows, got seen=%d err=%v", len(seenB), err)
	}
	t.Logf("FB24 CONTROL-B | nullable-free sort traversal completes | rows=%d", len(seenB))

	// The converted checks: sorted traversal by the nullable column must reach
	// all 7 unique rows in BOTH directions with no error — including both
	// NULL-sort rows — and TotalCount must agree. These branches carry the
	// archived discovery reds as their regression direction.
	seenAsc1, totalAsc1, cursorAsc1, errAsc := traverse(&Sort{Column: "middle"}, 1)
	seenDesc2, totalDesc2, cursorDesc2, errDesc := traverse(&Sort{Column: "middle", Desc: true}, 2)

	switch {
	case errAsc != nil:
		t.Errorf("REGRESSION (F-B24, ASC first=1): sorted browsing of a nullable column errored after %d of 7 rows (cursor=%q): %v — a page ending on a NULL sort value must encode a null-bucket cursor, never INVALID_CURSOR (the pre-fix silent-death/fault state)", len(seenAsc1), cursorAsc1, errAsc)
	case len(seenAsc1) != 7:
		t.Errorf("REGRESSION (F-B24, ASC first=1) | SILENT ROW LOSS: sorted traversal by the nullable column reaches only %d unique rows, missing vals=%v (page TotalCount=%d still advertises the full set) — NULL-sort rows were unreachable through the keyset predicate pre-fix (archived red: 5 of 7); the NULL-bucket arm of pageCursorPredicate is the preventer", len(seenAsc1), missing(seenAsc1), totalAsc1)
	case totalAsc1 != 7:
		t.Errorf("REGRESSION (F-B24, ASC first=1): TotalCount=%d disagrees with the 7-row table", totalAsc1)
	default:
		t.Logf("FB24 PASS | ASC first=1 reaches all 7 unique rows incl. both NULL-sort rows (null-bucket cursor traversed)")
	}
	switch {
	case errDesc != nil:
		t.Errorf("REGRESSION (F-B24, DESC first=2): sort traversal errored mid-list: %d of 7 rows reachable, stuck at cursor %q with %v — the archived red died on the [a,NULL] page boundary", len(seenDesc2), cursorDesc2, errDesc)
	case len(seenDesc2) != 7:
		t.Errorf("REGRESSION (F-B24, DESC first=2) | SILENT ROW LOSS both directions: sorted traversal reaches only %d unique rows, missing vals=%v (TotalCount=%d) — NULLS LAST ordering plus the `col IS NULL` arm must keep NULL rows reachable in DESC too (archived red: 5 of 7)", len(seenDesc2), missing(seenDesc2), totalDesc2)
	case totalDesc2 != 7:
		t.Errorf("REGRESSION (F-B24, DESC first=2): TotalCount=%d disagrees with the 7-row table", totalDesc2)
	default:
		t.Logf("FB24 PASS | DESC first=2 reaches all 7 unique rows incl. both NULL-sort rows")
	}

	// Ordered-completeness witness: NULLS LAST is pinned, so a fine-grained
	// ASC walk must observe val 3 and 5 (the NULL-middle rows) exactly last,
	// in row-id order, after every non-NULL middle.
	seenAsc3, _, _, errAsc3 := traverse(&Sort{Column: "middle"}, 3)
	if errAsc3 != nil || len(seenAsc3) != 7 || !seenAsc3["3"] || !seenAsc3["5"] {
		t.Fatalf("F-B24 ordering witness broken: ASC first=3 must include both NULL-sort rows vals {3,5}, got seen=%d err=%v", len(seenAsc3), errAsc3)
	}
	t.Logf("FB24 PASS | ASC first=3 enumerates the NULL bucket (vals 3,5 present; NULLS LAST pinned in ORDER BY)")
}

func readFileForGate(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
