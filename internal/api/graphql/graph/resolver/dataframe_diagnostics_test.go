package resolver

import (
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

func TestDataframeDiagnosticsProjectsSourceQueryMsOnBothBackends(t *testing.T) {
	const sourceQuery = 1234567 * time.Microsecond
	for _, lane := range []struct {
		name       string
		tag        command.CompiledBackend
		wantArango float64
	}{
		{name: "AQL", tag: command.CompiledBackendAQL, wantArango: 1234.567},
		{name: "PostgreSQL", tag: command.CompiledBackendPostgreSQL, wantArango: 0},
		{name: "untagged fail-closed to zero", tag: "", wantArango: 0},
	} {
		t.Run(lane.name, func(t *testing.T) {
			out := dataframeDiagnostics(dataframeexecution.QueryDiagnostics{
				SourceQuery:   sourceQuery,
				SourceBackend: lane.tag,
			})
			if out.SourceQueryMs != 1234.567 {
				t.Fatalf("sourceQueryMs = %v, want the source interval in ms", out.SourceQueryMs)
			}
			if out.ArangoQueryMs != lane.wantArango {
				t.Fatalf("%s: arangoQueryMs = %v, want %v", lane.name, out.ArangoQueryMs, lane.wantArango)
			}
			if lane.tag == command.CompiledBackendAQL && out.ArangoQueryMs != out.SourceQueryMs {
				t.Fatal("the legacy arangoQueryMs projection must equal sourceQueryMs byte-for-byte on the AQL lane")
			}
		})
	}
}
