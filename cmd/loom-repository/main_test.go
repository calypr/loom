package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/repositoryseed"
)

func TestLaunchOptionsDeriveComposeAndPublicEndpoints(t *testing.T) {
	options, err := newLaunchOptions("custom-stack", "0.0.0.0", 18080, "127.0.0.2", 13080)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(options.composeArgs("/loom"), " "); got != "compose --project-name custom-stack -f /loom/compose.yaml -f /loom/compose.repository.yaml" {
		t.Fatalf("compose arguments = %q", got)
	}
	if got := options.apiURL(); got != "http://127.0.0.1:18080" {
		t.Fatalf("API URL = %q", got)
	}
	if got := options.uiURL(); got != "http://127.0.0.2:13080" {
		t.Fatalf("UI URL = %q", got)
	}
	if got := strings.Join(options.composeEnvironment(), "\n"); !strings.Contains(got, "LOOM_API_HOST=0.0.0.0") || !strings.Contains(got, "LOOM_API_PORT=18080") || !strings.Contains(got, "LOOM_UI_HOST=127.0.0.2") || !strings.Contains(got, "LOOM_UI_PORT=13080") {
		t.Fatalf("Compose environment = %q", got)
	}
}

func TestLaunchOptionsRejectInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		project string
		apiPort int
		uiPort  int
	}{
		{name: "empty project", apiPort: 8080, uiPort: 3080},
		{name: "invalid project", project: "not valid!", apiPort: 8080, uiPort: 3080},
		{name: "invalid API port", project: "repo-stack", uiPort: 3080},
		{name: "invalid UI port", project: "repo-stack", apiPort: 8080, uiPort: 70000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newLaunchOptions(test.project, "127.0.0.1", test.apiPort, "127.0.0.1", test.uiPort); err == nil {
				t.Fatal("newLaunchOptions succeeded, want validation error")
			}
		})
	}
}

func TestLaunchOptionsProbeMatchingLoopbackFamiliesForWildcardBindings(t *testing.T) {
	options, err := newLaunchOptions("repo-stack", "::", 18080, "::", 13080)
	if err != nil {
		t.Fatal(err)
	}
	if got := options.apiURL(); got != "http://[::1]:18080" {
		t.Fatalf("API URL = %q", got)
	}
	if got := options.uiURL(); got != "http://[::1]:13080" {
		t.Fatalf("UI URL = %q", got)
	}
}

func TestRepositoryURLsUseStorageAndPublicProjectIdentities(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "HTAN_INT/BForePC", Generation: "repo-0123456789abcdef"}

	load := uploadURL("http://127.0.0.1:8080", manifest)
	if !strings.Contains(load, "/api/v1/datasets/HTAN_INT-BForePC/generations/") {
		t.Fatalf("generation upload URL = %q, want legacy storage project %q", load, projectid.Legacy(manifest.Project))
	}

	publication := publishURL("http://127.0.0.1:8080", manifest)
	if !strings.Contains(publication, "/api/v1/projects/HTAN_INT%2FBForePC/generations/") {
		t.Fatalf("repository publication URL = %q, want canonical public project %q", publication, manifest.Project)
	}

	viewer := viewerURL("http://127.0.0.1:8080", manifest)
	if !strings.Contains(viewer, "/api/v1/projects/HTAN_INT%2FBForePC/explorers/default") {
		t.Fatalf("Viewer URL = %q, want canonical public project %q", viewer, manifest.Project)
	}
}

func TestRepositoryURLsDoNotChangeOpaqueProjectIDs(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "NCPI_ACCEPTANCE", Generation: "repo-0123456789abcdef"}
	if got := uploadURL("http://127.0.0.1:8080", manifest); !strings.Contains(got, "/datasets/NCPI_ACCEPTANCE/") {
		t.Fatalf("opaque upload URL = %q", got)
	}
}

func TestPublicProjectPathEscapesCanonicalSlash(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "HTAN_INT/BForePC", Generation: "repo-0123456789abcdef"}
	for _, path := range []string{publishURL("http://127.0.0.1:8080", manifest), viewerURL("http://127.0.0.1:8080", manifest)} {
		if strings.Contains(path, "/HTAN_INT/BForePC/") {
			t.Fatalf("unescaped canonical slash in URL %q", path)
		}
		if !strings.Contains(path, url.PathEscape(manifest.Project)) {
			t.Fatalf("URL %q does not contain escaped canonical project", path)
		}
	}
}

func TestLaunchStopsUIBeforeInfrastructure(t *testing.T) {
	var calls [][]string
	if err := stopStaleUI(func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "stop loom-ui" {
		t.Fatalf("stale UI command = %#v, want [stop loom-ui]", calls)
	}
}

func TestLoadRepositoryMetaSkipsUploadForReusableGeneration(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "NCPI_ACCEPTANCE", Generation: "repo-0123456789abcdef"}
	var methods []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.Method != http.MethodGet {
			return jsonResponse(t, http.StatusMethodNotAllowed, map[string]any{"error": "unexpected method"}), nil
		}
		return jsonResponse(t, http.StatusOK, map[string]any{
			"project": manifest.Project, "generation": manifest.Generation,
			"state": "STAGED", "reusable": true,
		}), nil
	})}

	result, err := loadRepositoryMeta(context.Background(), client, "http://loom.test", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if reused, _ := result["reused"].(bool); !reused {
		t.Fatalf("result = %#v, want reused", result)
	}
	if len(methods) != 1 {
		t.Fatalf("requests = %v, want one status request", methods)
	}
}

func TestLoadRepositoryMetaRejectsExistingNonReusableGeneration(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "NCPI_ACCEPTANCE", Generation: "repo-0123456789abcdef"}
	var methods []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		return jsonResponse(t, http.StatusOK, map[string]any{
			"project": manifest.Project, "generation": manifest.Generation,
			"state": "FAILED", "reusable": false,
		}), nil
	})}

	_, err := loadRepositoryMeta(context.Background(), client, "http://loom.test", manifest)
	if err == nil || !strings.Contains(err.Error(), "FAILED") {
		t.Fatalf("loadRepositoryMeta error = %v, want FAILED state", err)
	}
	if len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("requests = %v, want only GET", methods)
	}
}

func TestLoadRepositoryMetaUploadsGenerationAfterNotFound(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "NCPI_ACCEPTANCE", Generation: "repo-0123456789abcdef"}
	var methods []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.Method == http.MethodGet {
			return jsonResponse(t, http.StatusNotFound, map[string]any{"code": "DATASET_NOT_FOUND"}), nil
		}
		if request.Method != http.MethodPost {
			return jsonResponse(t, http.StatusMethodNotAllowed, map[string]any{"error": "unexpected method"}), nil
		}
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(t, http.StatusOK, map[string]any{"reused": false}), nil
	})}

	result, err := loadRepositoryMeta(context.Background(), client, "http://loom.test", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if reused, _ := result["reused"].(bool); reused {
		t.Fatalf("result = %#v, want new load", result)
	}
	if strings.Join(methods, ",") != "GET,POST" {
		t.Fatalf("requests = %v, want GET then POST", methods)
	}
}

func TestLoadRepositoryMetaDoesNotUploadAfterStatusFailure(t *testing.T) {
	manifest := repositoryseed.Manifest{Project: "NCPI_ACCEPTANCE", Generation: "repo-0123456789abcdef"}
	var methods []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		return jsonResponse(t, http.StatusServiceUnavailable, map[string]any{"code": "BACKEND_UNAVAILABLE"}), nil
	})}

	_, err := loadRepositoryMeta(context.Background(), client, "http://loom.test", manifest)
	if err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("loadRepositoryMeta error = %v, want service unavailable", err)
	}
	if len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("requests = %v, want only GET", methods)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
