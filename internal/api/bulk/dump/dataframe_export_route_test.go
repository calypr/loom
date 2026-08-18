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
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type fakeDataframeExporter struct{}

func (fakeDataframeExporter) ExportDataframe(_ context.Context, request dfmaterialization.ExportRequest, out io.Writer) error {
	if request.Selector == nil || request.Format.Normalize() != dfmaterialization.ExportCSV {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	_, err := io.WriteString(out, "id\n1\n")
	return err
}

type fakeSelectorExporter struct{}

func (fakeSelectorExporter) ExportDataframe(_ context.Context, request dfmaterialization.ExportRequest, out io.Writer) error {
	if request.Selector == nil || request.Selector.Recipe != "documents" || request.Selector.TranslationVersion != "v2" || request.Selector.Output != "DocumentReference" {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	_, err := io.WriteString(out, "{}\n")
	return err
}

func TestDataframeExportRouteRejectsLegacyDataType(t *testing.T) {
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
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
}

func TestDataframeExportRouteAcceptsExactSelector(t *testing.T) {
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	NewHandler(Config{DataframeExporter: fakeSelectorExporter{}}).RegisterRoutes(server.App())
	req := httptest.NewRequest(http.MethodPost, "/loom/api/v1/dataframe/export", strings.NewReader(`{"selector":{"recipe":"documents","translationVersion":"v2","output":"DocumentReference"},"format":"JSONL"}`))
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
}
