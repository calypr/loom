package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type capturingAQLExecutor struct {
	query    string
	bindVars map[string]interface{}
	err      error
}

func (e *capturingAQLExecutor) ExecuteAQL(_ context.Context, query string, bindVars map[string]interface{}) error {
	e.query = query
	e.bindVars = bindVars
	return e.err
}

func TestAccumulateRelationshipCatalogExecutesWriteWithoutObjectRowDecoding(t *testing.T) {
	executor := &capturingAQLExecutor{}
	docs := []RelationshipCatalogDocument{{
		Key:              "relationship-key",
		Project:          "P1",
		AuthResourcePath: "/programs/P/projects/P1",
		FromType:         "BodyStructure",
		Label:            "patient",
		ToType:           "Patient",
		EdgeCount:        2,
	}}
	timings := map[string]float64{}

	if err := AccumulateRelationshipCatalog(context.Background(), executor, docs, timings); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(executor.query, "RETURN 1") {
		t.Fatal("write query still returns a scalar result")
	}
	rows, ok := executor.bindVars["docs"].([]map[string]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("docs bind = %#v", executor.bindVars["docs"])
	}
	want := map[string]any{
		"_key":               "relationship-key",
		"project":            "P1",
		"dataset_generation": nil,
		"auth_resource_path": "/programs/P/projects/P1",
		"from_type":          "BodyStructure",
		"label":              "patient",
		"to_type":            "Patient",
		"edge_count":         int64(2),
	}
	if !reflect.DeepEqual(rows[0], want) {
		t.Fatalf("docs bind row = %#v, want %#v", rows[0], want)
	}
	if timings["relationship_catalog_accumulate"] <= 0 {
		t.Fatalf("timing was not recorded: %#v", timings)
	}
}

func TestAccumulateRelationshipCatalogReturnsExecuteError(t *testing.T) {
	want := errors.New("write failed")
	executor := &capturingAQLExecutor{err: want}
	err := AccumulateRelationshipCatalog(context.Background(), executor, []RelationshipCatalogDocument{{Key: "key"}}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
