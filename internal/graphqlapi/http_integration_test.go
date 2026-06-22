package graphqlapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"arangodb-proto/internal/dataframe"
	"arangodb-proto/internal/graphqlapi"
	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

func TestGraphQLIntrospectionEndpoint(t *testing.T) {
	graphService := graphqlapi.NewService(graphqlapi.ServiceConfig{
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			return []proto.PopulatedReference{
				{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 10},
			}, nil
		},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			if opts.PivotOnly {
				return []proto.PopulatedField{
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
			return []proto.PopulatedField{
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
	})
	svc, err := writeapi.NewService(writeapi.ServiceConfig{
		Runner: fakeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := writeapi.NewHTTPServer(writeapi.HTTPConfig{
		Service:                  svc,
		Authenticator:            writeapi.StaticAuthenticator{Principal: writeapi.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}},
		GraphQLHandler:           graphqlapi.NewHandler(graphqlapi.NewResolver(graphService)),
		GraphQLPlaygroundHandler: graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:     graphqlapi.NewApolloSandboxHandler("/graphql"),
	})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"query($input: DataframeBuilderIntrospectionInput!) { dataframeBuilderIntrospection(input: $input) { project rootResourceType authResourcePaths root { resourceType fields { resourceType path selector kind } pivotFields { resourceType path selector pivotCandidate pivotKind pivotColumns } traversals { fromType label toType edgeCount } } relatedResources { viaLabel edgeCount target { resourceType fields { resourceType path selector kind } pivotFields { resourceType path selector pivotCandidate pivotKind pivotColumns } } } traversals { fromType label toType edgeCount } fields { resourceType path selector kind docCount sampleCount distinctValues distinctTruncated pivotCandidate pivotKind pivotColumns } pivotFields { resourceType path selector pivotCandidate pivotKind pivotColumns } } }","variables":{"input":{"project":"P1","rootResourceType":"Patient"}}}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(queryBody))
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
	if len(payload.Data.Introspection.Traversals) != 1 || len(payload.Data.Introspection.Fields) != 1 || len(payload.Data.Introspection.PivotFields) != 1 {
		t.Fatalf("unexpected response sizes: %#v", payload.Data.Introspection)
	}
	if payload.Data.Introspection.Root["resourceType"] != "Patient" || len(payload.Data.Introspection.RelatedResources) != 1 {
		t.Fatalf("unexpected structured introspection: %#v", payload.Data.Introspection)
	}
}

func TestGraphQLSchemaIntrospectionEndpoint(t *testing.T) {
	graphService := graphqlapi.NewService(graphqlapi.ServiceConfig{})
	svc, err := writeapi.NewService(writeapi.ServiceConfig{
		Runner: fakeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := writeapi.NewHTTPServer(writeapi.HTTPConfig{
		Service:                  svc,
		Authenticator:            writeapi.StaticAuthenticator{Principal: writeapi.Principal{Subject: "u1"}},
		GraphQLHandler:           graphqlapi.NewHandler(graphqlapi.NewResolver(graphService)),
		GraphQLPlaygroundHandler: graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:     graphqlapi.NewApolloSandboxHandler("/graphql"),
	})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"query { __schema { queryType { name } mutationType { name } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(queryBody))
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

func TestGraphQLPreviewMutation(t *testing.T) {
	dfService := dataframe.NewService(dataframe.ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			return []proto.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
		},
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			return []proto.PopulatedReference{}, nil
		},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"_key": "p1", "gender": "female"})
		},
	})
	graphService := graphqlapi.NewService(graphqlapi.ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		Dataframes:        dfService,
	})
	svc, err := writeapi.NewService(writeapi.ServiceConfig{
		Runner: fakeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := writeapi.NewHTTPServer(writeapi.HTTPConfig{
		Service:                  svc,
		Authenticator:            writeapi.StaticAuthenticator{Principal: writeapi.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}},
		GraphQLHandler:           graphqlapi.NewHandler(graphqlapi.NewResolver(graphService)),
		GraphQLPlaygroundHandler: graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:     graphqlapi.NewApolloSandboxHandler("/graphql"),
	})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"mutation($input: FhirDataframeInput!, $mode: DataframeRunMode!, $previewLimit: Int) { runFhirDataframe(input: $input, mode: $mode, previewLimit: $previewLimit) { mode preview { columns rows { data } rowCount } } }","variables":{"mode":"PREVIEW","previewLimit":25,"input":{"project":"P1","rootResourceType":"Patient","rootFields":[{"name":"gender","fhirPath":"gender","valueMode":"AUTO"}]}}}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(queryBody))
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
				Mode    string `json:"mode"`
				Preview struct {
					Columns  []string `json:"columns"`
					Rows     []struct {
						Data map[string]any `json:"data"`
					} `json:"rows"`
					RowCount int      `json:"rowCount"`
				} `json:"preview"`
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
	if payload.Data.Run.Mode != "PREVIEW" || payload.Data.Run.Preview.RowCount != 1 {
		t.Fatalf("unexpected preview payload: %#v", payload.Data.Run)
	}
}

func TestGraphQLPreviewMutationTraversalBuilder(t *testing.T) {
	dfService := dataframe.NewService(dataframe.ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		DiscoverFields: func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
			switch opts.ResourceType {
			case "Patient":
				return []proto.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
			case "Specimen":
				return []proto.PopulatedField{{ResourceType: "Specimen", Path: "type[].coding[].display", Kind: "scalar"}}, nil
			default:
				return []proto.PopulatedField{}, nil
			}
		},
		DiscoverReferences: func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
			if opts.NodeType == "Patient" {
				return []proto.PopulatedReference{{
					FromType: "Patient",
					Label:    "subject_Patient",
					ToType:   "Specimen",
					EdgeCount: 2,
				}}, nil
			}
			return []proto.PopulatedReference{}, nil
		},
		ExecuteRows: func(ctx context.Context, opts proto.ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
			if !strings.Contains(query, "LET specimen_nodes") || !strings.Contains(query, "__edge.label") {
				t.Fatalf("expected traversal query, got:\n%s", query)
			}
			return visit(map[string]any{
				"_key":                  "p1",
				"gender":                "female",
				"specimen__type_display": "Blood",
			})
		},
	})
	graphService := graphqlapi.NewService(graphqlapi.ServiceConfig{
		ConnectionOptions: proto.ConnectionOptions{Backend: "arango"},
		Dataframes:        dfService,
	})
	svc, err := writeapi.NewService(writeapi.ServiceConfig{
		Runner: fakeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := writeapi.NewHTTPServer(writeapi.HTTPConfig{
		Service:                  svc,
		Authenticator:            writeapi.StaticAuthenticator{Principal: writeapi.Principal{Subject: "u1", Projects: []string{"P1"}, AuthResourcePaths: []string{"pathA"}}},
		GraphQLHandler:           graphqlapi.NewHandler(graphqlapi.NewResolver(graphService)),
		GraphQLPlaygroundHandler: graphqlapi.NewPlaygroundHandler("/graphql"),
		ApolloSandboxHandler:     graphqlapi.NewApolloSandboxHandler("/graphql"),
	})
	if err != nil {
		t.Fatal(err)
	}

	queryBody := `{"query":"mutation($input: FhirDataframeInput!, $mode: DataframeRunMode!, $previewLimit: Int) { runFhirDataframe(input: $input, mode: $mode, previewLimit: $previewLimit) { mode preview { columns rows { data } rowCount } } }","variables":{"mode":"PREVIEW","previewLimit":25,"input":{"project":"P1","rootResourceType":"Patient","rootFields":[{"name":"gender","fhirPath":"gender","valueMode":"AUTO"}],"traverse":[{"edgeLabel":"subject_Patient","toResourceType":"Specimen","alias":"specimen","fields":[{"name":"type_display","fhirPath":"type[].coding[].display","valueMode":"AUTO"}]}]}}}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(queryBody))
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
				Mode    string `json:"mode"`
				Preview struct {
					Columns  []string `json:"columns"`
					Rows     []struct {
						Data map[string]any `json:"data"`
					} `json:"rows"`
					RowCount int      `json:"rowCount"`
				} `json:"preview"`
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
	if payload.Data.Run.Preview.RowCount != 1 {
		t.Fatalf("unexpected preview payload: %#v", payload.Data.Run)
	}
}

type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, req writeapi.ImportRequest, sink proto.EventSink) (proto.LoadSummary, error) {
	return proto.LoadSummary{}, nil
}
