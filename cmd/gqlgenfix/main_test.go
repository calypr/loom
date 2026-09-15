package main

import (
	"strings"
	"testing"
)

func TestFixGeneratedSourceAddsJSONScalarSupportIdempotently(t *testing.T) {
	source := `package executor

import (
	"context"
	"encoding/json"
	"errors"
)

func (ec *executionContext) unmarshalNJSON2(ctx context.Context, v any) (json.RawMessage, error) {
	var res json.RawMessage
	var err error
	return &res, graphql.ErrorOnPath(ctx, err)
}
`
	got, err := fixGeneratedSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"io"`) || !strings.Contains(got, "func MarshalJSON(") {
		t.Fatalf("JSON scalar support was not appended:\n%s", got)
	}
	if strings.Contains(got, "return &res, graphql.ErrorOnPath") {
		t.Fatal("invalid JSON pointer return was not fixed")
	}
	again, err := fixGeneratedSource(got)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatal("post-processing is not idempotent")
	}
}

func TestFixGeneratedSourceAcceptsGeneratedFunctionNameAndFormattingDrift(t *testing.T) {
	source := `package executor

import (
    "context"
    "encoding/json"
)

func (ec *executionContext) unmarshalNJSON2encodingRawMessage(ctx context.Context, v any) (json.RawMessage, error) {
    var res json.RawMessage
    var err error
    return &res,
        graphql.ErrorOnPath(ctx, err)
}
`
	got, err := fixGeneratedSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "return &res, graphql.ErrorOnPath") {
		t.Fatalf("pointer return was not fixed:\n%s", got)
	}
	if !strings.Contains(got, "return res,") || !strings.Contains(got, "\"io\"") {
		t.Fatalf("drifted generated source was not corrected:\n%s", got)
	}
}
