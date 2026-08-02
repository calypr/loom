package dump

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/materialization"
)

type fakeDataframeExporter struct{}

func (fakeDataframeExporter) ExportDataframe(_ context.Context, request dfmaterialization.ExportRequest, out io.Writer) error {
	if request.DataType != "files" || request.Format.Normalize() != dfmaterialization.ExportCSV {
		return &apiError{Status: http.StatusBadRequest, Code: "bad_request", Message: "unexpected request"}
	}
	_, err := io.WriteString(out, "id\n1\n")
	return err
}

func TestDataframeExportRoute(t *testing.T) {
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	NewHandler(Config{DataframeExporter: fakeDataframeExporter{}}).RegisterRoutes(server.App())
	req := httptest.NewRequest(http.MethodPost, "/loom/api/v1/dataframe/export", strings.NewReader(`{"dataType":"files","format":"CSV","fileName":"../files.csv"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("content type = %q", got)
	}
	if got := resp.Header.Get("Content-Disposition"); strings.Contains(got, "../") || !strings.Contains(got, "files.csv") {
		t.Fatalf("content disposition = %q", got)
	}
}
