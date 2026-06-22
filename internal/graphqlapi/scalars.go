package graphqlapi

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

func MarshalJSON(v map[string]any) graphql.Marshaler {
	return graphql.MarshalMap(v)
}

func UnmarshalJSON(v any) (map[string]any, error) {
	return graphql.UnmarshalMap(v)
}

func (ec *executionContext) unmarshalInputJSON(ctx context.Context, v any) (map[string]any, error) {
	return graphql.UnmarshalMap(v)
}

func (ec *executionContext) _JSON(ctx context.Context, sel ast.SelectionSet, v map[string]any) graphql.Marshaler {
	return graphql.MarshalMap(v)
}
