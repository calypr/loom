package graphqlapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graph "github.com/calypr/loom/internal/api/graphql/graph"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestGraphQLSchemaIntrospectionEndpoint(t *testing.T) {
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{})
	server, err := newGraphServer(graphResolver, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "u1"}})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"query { __schema { queryType { name } mutationType { name } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(queryBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Schema struct {
				QueryType struct {
					Name string `json:"name"`
				} `json:"queryType"`
				MutationType struct {
					Name string `json:"name"`
				} `json:"mutationType"`
			} `json:"__schema"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("unexpected graphql errors: %#v", payload.Errors)
	}
	if payload.Data.Schema.QueryType.Name != "Query" || payload.Data.Schema.MutationType.Name != "Mutation" {
		t.Fatalf("unexpected schema payload: %#v", payload.Data.Schema)
	}
	aliasReq := httptest.NewRequest(http.MethodPost, "/graphql/flat", strings.NewReader(queryBody))
	aliasReq.Header.Set("Content-Type", "application/json")
	aliasResp, err := server.App().Test(aliasReq)
	if err != nil {
		t.Fatal(err)
	}
	defer aliasResp.Body.Close()
	if aliasResp.StatusCode != http.StatusNotFound {
		t.Fatalf("retired flat alias status = %d, want %d", aliasResp.StatusCode, http.StatusNotFound)
	}
}

func TestProjectDataframeDatasetsResolverIsWired(t *testing.T) {
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{})
	server, err := newGraphServer(graphResolver, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "u1", Projects: []string{"P1"}}})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(`{"query":"query { projectDataframeDatasets(projectId: \"P1\") { id } }"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
	var payload struct {
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Extensions.Code != "BACKEND_UNAVAILABLE" {
		t.Fatalf("errors = %#v", payload.Errors)
	}
	if strings.Contains(payload.Errors[0].Message, "not implemented") {
		t.Fatalf("resolver is not wired: %s", payload.Errors[0].Message)
	}
}

func TestGraphQLRunDataframeMutation(t *testing.T) {
	dfService := runtime.NewService(runtime.ServiceConfig{
		QueryRows: func(ctx context.Context, query string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET child_set_") || !strings.Contains(query, "LENGTH(child_set_") {
				t.Fatalf("expected physical child-set query, got:\n%s", query)
			}
			return visit(map[string]any{"_key": "p1", "gender": "female", "condition__condition_count": 1})
		},
	})
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			ActiveManifestResolver: testActiveManifestResolver{},
			DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
				switch opts.ResourceType {
				case "Patient":
					return []catalog.PopulatedField{
						{ResourceType: "Patient", Path: "gender", Kind: "scalar"},
						{ResourceType: "Patient", Path: "id", Kind: "scalar"},
					}, nil
				case "Condition":
					return []catalog.PopulatedField{
						{ResourceType: "Condition", Path: "id", Kind: "scalar"},
					}, nil
				case "Specimen":
					return []catalog.PopulatedField{
						{ResourceType: "Specimen", Path: "type[].coding[].display", Kind: "scalar"},
					}, nil
				default:
					return []catalog.PopulatedField{}, nil
				}
			},
			DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
				if opts.NodeType == "Patient" {
					return []catalog.PopulatedReference{
						{FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 1},
						{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1},
					}, nil
				}
				return []catalog.PopulatedReference{}, nil
			},
			Dataframes: dfService,
		},
	})
	server, err := newGraphServer(graphResolver, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rows rowCount diagnostics { inputResolutionMs requestPreparationMs compilationMs arangoQueryMs rowMaterializationMs resultAssemblyMs totalMs plan { traversalSets sharedTraversalCount scopedSharingCandidateGroups scopedSharingCandidateSets richSourceReuse { sourceSet aggregateConsumers pivotConsumers sliceConsumers totalConsumers } } } } }","variables":{"limit":25,"input":{"project":"P1","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"},"valueMode":"AUTO"}],"traverse":[{"edgeLabel":"subject_Patient","toResourceType":"Condition","alias":"condition","aggregates":[{"name":"condition_count","operation":"COUNT"}]},{"edgeLabel":"subject_Patient","toResourceType":"Specimen","alias":"specimen","aggregates":[{"name":"specimen_count","operation":"COUNT"}]}]}}}`
	req := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(queryBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Run struct {
				Columns     []string         `json:"columns"`
				Rows        []map[string]any `json:"rows"`
				RowCount    int              `json:"rowCount"`
				Diagnostics struct {
					CompilationMs float64 `json:"compilationMs"`
					TotalMs       float64 `json:"totalMs"`
				} `json:"diagnostics"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("unexpected graphql errors: %#v", payload.Errors)
	}
	if payload.Data.Run.RowCount != 1 {
		t.Fatalf("unexpected dataframe payload: %#v", payload.Data.Run)
	}
	if payload.Data.Run.Diagnostics.TotalMs <= 0 || payload.Data.Run.Diagnostics.CompilationMs <= 0 {
		t.Fatalf("missing dataframe diagnostics: %#v", payload.Data.Run.Diagnostics)
	}
}

func TestGraphQLRunDataframeTraversalBuilder(t *testing.T) {
	dfService := runtime.NewService(runtime.ServiceConfig{
		QueryRows: func(ctx context.Context, query string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET child_set_") || !strings.Contains(query, "LENGTH(child_set_") {
				t.Fatalf("expected physical child-set query, got:\n%s", query)
			}
			return visit(map[string]any{
				"_key":                     "p1",
				"gender":                   "female",
				"specimen__specimen_count": 1,
				"specimen__specimen_types": []string{"Blood"},
			})
		},
	})
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			ActiveManifestResolver: testActiveManifestResolver{},
			DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
				switch opts.ResourceType {
				case "Patient":
					return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
				case "Specimen":
					return []catalog.PopulatedField{{ResourceType: "Specimen", Path: "type[].coding[].display", Kind: "scalar"}}, nil
				case "Condition":
					return []catalog.PopulatedField{{ResourceType: "Condition", Path: "id", Kind: "scalar"}}, nil
				default:
					return []catalog.PopulatedField{}, nil
				}
			},
			DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
				if opts.NodeType == "Patient" {
					return []catalog.PopulatedReference{
						{
							FromType:  "Patient",
							Label:     "subject_Patient",
							ToType:    "Specimen",
							EdgeCount: 2,
						},
						{
							FromType:  "Patient",
							Label:     "subject_Patient",
							ToType:    "Condition",
							EdgeCount: 1,
						},
					}, nil
				}
				return []catalog.PopulatedReference{}, nil
			},
			Dataframes: dfService,
		},
	})
	server, err := newGraphServer(graphResolver, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"mutation($input: FhirDataframeInput!, $limit: Int) { runFhirDataframe(input: $input, limit: $limit) { columns rows rowCount } }","variables":{"limit":25,"input":{"project":"P1","rootResourceType":"Patient","rootFields":[{"name":"gender","selector":{"valuePath":"gender"},"valueMode":"AUTO"}],"traverse":[{"edgeLabel":"subject_Patient","toResourceType":"Specimen","alias":"specimen","aggregates":[{"name":"specimen_count","operation":"COUNT"},{"name":"specimen_types","operation":"DISTINCT_VALUES","fhirPath":"type[].coding[].display","valueMode":"AUTO"}]},{"edgeLabel":"subject_Patient","toResourceType":"Condition","alias":"condition","aggregates":[{"name":"condition_count","operation":"COUNT"}]}]}}}`
	req := httptest.NewRequest(http.MethodPost, "/graphql/graph", strings.NewReader(queryBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			Run struct {
				Columns  []string         `json:"columns"`
				Rows     []map[string]any `json:"rows"`
				RowCount int              `json:"rowCount"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("unexpected graphql errors: %#v", payload.Errors)
	}
	if payload.Data.Run.RowCount != 1 {
		t.Fatalf("unexpected dataframe payload: %#v", payload.Data.Run)
	}
}

func newGraphServer(root *graphresolver.Resolver, auth authscope.Authenticator) (*api.HTTPServer, error) {
	server, err := api.NewHTTPServer(api.HTTPConfig{Authenticator: auth, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		return nil, err
	}
	graph.RegisterRoutes(server.App(), graph.RouteConfig{Handler: graph.NewHandler(root), Playground: graph.NewPlaygroundHandler("/graphql/graph"), Sandbox: graph.NewApolloSandboxHandler("/graphql/graph")})
	return server, nil
}

type testActiveManifestResolver struct{}

func (testActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (publication.Manifest, error) {
	schema, err := publication.NewSchemaSnapshot("urn:loom:graphql-test", "", strings.Repeat("a", 64), []string{"Patient", "Specimen"})
	if err != nil {
		return publication.Manifest{}, err
	}
	ref, err := publication.NewRef(project, "generation-1")
	if err != nil {
		return publication.Manifest{}, err
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		return publication.Manifest{}, err
	}
	return manifest.Transition(publication.StateReady)
}
