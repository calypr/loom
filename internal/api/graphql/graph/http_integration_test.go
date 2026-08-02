package graphqlapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphresolver "github.com/calypr/loom/generated/graphql/graph/resolver"
	graph "github.com/calypr/loom/internal/api/graphql/graph"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestGraphQLIntrospectionEndpoint(t *testing.T) {
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			ConnectionOptions: arangostore.ConnectionOptions{},
			DiscoverReferences: func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
				return []catalog.PopulatedReference{
					{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 10},
				}, nil
			},
			DiscoverFields: func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
				if opts.PivotOnly {
					return []catalog.PopulatedField{
						{
							ResourceType:      "Patient",
							Path:              "valueCodeableConcept",
							Kind:              "codeable_concept",
							DocCount:          3,
							SampleCount:       1,
							PivotCandidate:    true,
							PivotKind:         "codeable_concept_display_value",
							PivotColumns:      []string{"Stage IVA"},
							DistinctValues:    []string{"M0"},
							DistinctTruncated: false,
						},
					}, nil
				}
				return []catalog.PopulatedField{
					{
						ResourceType:      "Patient",
						Path:              "identifier[].value",
						Kind:              "scalar",
						DocCount:          5,
						SampleCount:       1,
						DistinctValues:    []string{"TCGA-01"},
						DistinctTruncated: false,
						PivotCandidate:    false,
						PivotColumns:      []string{},
					},
				}, nil
			},
		},
	})
	server, err := newGraphServer(graphResolver, authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"query($input: DataframeBuilderIntrospectionInput!) { dataframeBuilderIntrospection(input: $input) { project rootResourceType authResourcePaths root { resourceType fields { resourceType fieldRef label path selector { sourcePath where { path op value } valuePath } kind } pivotFields { resourceType fieldRef path selector { sourcePath where { path op value } valuePath } pivotCandidate pivotKind pivotColumns } traversals { fromType label toType edgeCount } } relatedResources { viaLabel edgeCount target { resourceType fields { resourceType fieldRef path selector { sourcePath where { path op value } valuePath } kind } pivotFields { resourceType fieldRef path selector { sourcePath where { path op value } valuePath } pivotCandidate pivotKind pivotColumns } } } traversals { fromType label toType edgeCount } fields { resourceType fieldRef label path selector { sourcePath where { path op value } valuePath } kind docCount sampleCount distinctValues distinctTruncated pivotCandidate pivotKind pivotColumns } pivotFields { resourceType fieldRef path selector { sourcePath where { path op value } valuePath } pivotCandidate pivotKind pivotColumns } } }","variables":{"input":{"project":"P1","rootResourceType":"Patient"}}}`
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
			Introspection struct {
				Project           string           `json:"project"`
				RootResourceType  string           `json:"rootResourceType"`
				AuthResourcePaths []string         `json:"authResourcePaths"`
				Root              map[string]any   `json:"root"`
				RelatedResources  []map[string]any `json:"relatedResources"`
				Traversals        []map[string]any `json:"traversals"`
				Fields            []map[string]any `json:"fields"`
				PivotFields       []map[string]any `json:"pivotFields"`
			} `json:"dataframeBuilderIntrospection"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Errors) != 0 {
		t.Fatalf("unexpected graphql errors: %#v", payload.Errors)
	}
	if payload.Data.Introspection.Project != "P1" || payload.Data.Introspection.RootResourceType != "Patient" {
		t.Fatalf("unexpected introspection header: %#v", payload.Data.Introspection)
	}
	if got := payload.Data.Introspection.AuthResourcePaths; len(got) != 1 || got[0] != "pathA" {
		t.Fatalf("unexpected auth resource paths: %#v", got)
	}
	if len(payload.Data.Introspection.Traversals) != 1 || len(payload.Data.Introspection.Fields) == 0 || len(payload.Data.Introspection.PivotFields) != 1 {
		t.Fatalf("unexpected response sizes: %#v", payload.Data.Introspection)
	}
	if payload.Data.Introspection.Root["resourceType"] != "Patient" || len(payload.Data.Introspection.RelatedResources) != 1 {
		t.Fatalf("unexpected structured introspection: %#v", payload.Data.Introspection)
	}
	firstField, _ := payload.Data.Introspection.Fields[0]["selector"].(map[string]any)
	if firstField == nil || firstField["valuePath"] == "" {
		t.Fatalf("expected structured selector payload: %#v", payload.Data.Introspection.Fields[0])
	}
}

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
}

func TestGraphQLRunDataframeMutation(t *testing.T) {
	dfService := runtime.NewService(runtime.ServiceConfig{
		ConnectionOptions: arangostore.ConnectionOptions{},
		ExecuteRows: func(ctx context.Context, opts runtime.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET child_set_") || !strings.Contains(query, "LENGTH(child_set_") {
				t.Fatalf("expected physical child-set query, got:\n%s", query)
			}
			return visit(map[string]any{"_key": "p1", "gender": "female", "condition__condition_count": 1})
		},
	})
	graphResolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			ConnectionOptions: arangostore.ConnectionOptions{},
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
		ConnectionOptions: arangostore.ConnectionOptions{},
		ExecuteRows: func(ctx context.Context, opts runtime.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
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
			ConnectionOptions: arangostore.ConnectionOptions{},
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
