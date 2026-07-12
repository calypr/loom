package graphqlapi

import (
	"context"
	"encoding/json"
	"io"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

func MarshalJSON(v json.RawMessage) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		_, _ = w.Write(v)
	})
}

func (ec *executionContext) unmarshalInputJSON(ctx context.Context, v any) (json.RawMessage, error) {
	return json.Marshal(v)
}

func (ec *executionContext) _JSON(ctx context.Context, sel ast.SelectionSet, v json.RawMessage) graphql.Marshaler {
	return MarshalJSON(v)
}
