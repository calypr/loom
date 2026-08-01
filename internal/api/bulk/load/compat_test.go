package load

import (
	"bytes"
	"context"
	"errors"
	"github.com/calypr/loom/internal/api/bulk/dump"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
)

type HTTPConfig struct {
	Service                      *Service
	RawExporter                  dump.RawExporter
	Authenticator                authscope.Authenticator
	Authorizer                   authscope.Authorizer
	DisableSingleResourceImports bool
}
type RawDumpRequest = dump.RawDumpRequest
type denyAuthorizer struct{}

func (denyAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return errors.New("nope")
}
func NewHTTPServer(cfg HTTPConfig) (*httpapi.HTTPServer, error) {
	s, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: cfg.Authenticator, Authorizer: cfg.Authorizer})
	if err != nil {
		return nil, err
	}
	h, err := NewHandler(Config{Service: cfg.Service, Authorizer: cfg.Authorizer, DisableSingleResourceImports: cfg.DisableSingleResourceImports})
	if err != nil {
		return nil, err
	}
	h.RegisterRoutes(s.App())
	if cfg.RawExporter != nil {
		dump.NewHandler(dump.Config{RawExporter: cfg.RawExporter, DisableSingleResourceImports: cfg.DisableSingleResourceImports}).RegisterRoutes(s.App())
	}
	return s, nil
}
func newMultipartRequest(t testingT, fields map[string]string, fileField, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	p, _ := w.CreateFormFile(fileField, fileName)
	_, _ = p.Write(content)
	_ = w.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/imports", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r
}

type testingT interface {
	Helper()
	Fatalf(string, ...any)
}
